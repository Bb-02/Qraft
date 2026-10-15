package activities

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func materializeNormalizedInputFixtureV1(t *testing.T, raw string, durable, tokenStrings bool) (*Activities, ArtifactStore, *captureProvenanceRecorder, TestDataResult, MaterializeS3CasePlanResultV1) {
	t.Helper()
	fixture := newStatementDraftFixture(t, nil)
	if tokenStrings {
		fixture = tokenStringAuthoringFixtureV2(t, nil).statementDraftFixture
	}
	fixture.bundle.RequestedTestCaseCount, fixture.bundle.MinTestCaseCount, fixture.bundle.MaxTestCaseCount, fixture.bundle.AdaptiveTestCaseCount = 1, 0, 0, false
	fixture.replaceBundle(t)
	provenance := &captureProvenanceRecorder{}
	acts := New(&Dependencies{ArtifactStore: fixture.store, ProvenanceRecorder: provenance})
	testCase := TestCaseData{Input: raw, Origin: TestCaseOriginGenerator, GeneratorSeed: 71}
	if durable {
		ref := s3BatchSeedArtifactV1(t, fixture.store, []byte(raw), "text/plain", "GenerateS3TestDataActivityV1")
		testCase.Input, testCase.InputArtifact = "", &ref
	}
	data := TestDataResult{PayloadVersion: ActivityPayloadVersion, TestCases: []TestCaseData{testCase}}
	result := executeS3BatchTestActivityV1[MaterializeS3CasePlanResultV1](t, acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: fixture.input.BundleArtifact, ExpectedAuthoringBundleSHA: fixture.input.ExpectedBundleSHA256, TestData: data})
	return acts, fixture.store, provenance, data, result
}

func TestS3CaseInputNormalizationUsesTypedCanonicalBytesAndKeepsOriginalCAS(t *testing.T) {
	for _, test := range []struct {
		name, raw, canonical string
		strings              bool
	}{
		{"multiple spaces", "1   \n   1   \n", "1\n1\n", false},
		{"CRLF and integer spelling", " +01 \r\n 0001  ", "1\n1\n", false},
		{"missing final LF", "1\n1", "1\n1\n", false},
		{"string token spelling is data", "2  \n  +001   0003  \n", "2\n+001 0003\n", true},
	} {
		for _, durable := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: " inline", true: " durable"}[durable], func(t *testing.T) {
				acts, store, provenance, source, materialized := materializeNormalizedInputFixtureV1(t, test.raw, durable, test.strings)
				plan, _, err := acts.loadS3CasePlanV1(context.Background(), *materialized.CasePlanArtifact)
				require.NoError(t, err)
				item := plan.Cases[0]
				require.NotNil(t, item.OriginalInputArtifact)
				require.Equal(t, S3CaseInputNormalizationRuleV1, item.InputNormalizationRuleVersion)
				require.Equal(t, sha256Hex([]byte(test.raw)), item.OriginalInputArtifact.SHA256)
				require.Equal(t, sha256Hex([]byte(test.canonical)), item.InputArtifact.SHA256)
				require.NotEqual(t, item.OriginalInputArtifact.SHA256, item.InputArtifact.SHA256)
				original, err := store.Get(context.Background(), *item.OriginalInputArtifact)
				require.NoError(t, err)
				require.Equal(t, test.raw, string(original))
				canonical, err := store.Get(context.Background(), item.InputArtifact)
				require.NoError(t, err)
				require.Equal(t, test.canonical, string(canonical))
				require.Equal(t, int64(71), item.Seed, "generator audit seed must not be rewritten as if the generator emitted canonical bytes")
				sourceSHA, err := canonicalJSONSHA256(source)
				require.NoError(t, err)
				require.Equal(t, sourceSHA, plan.TestDataIdentitySHA256, "source identity still binds the original generator output")
				if durable {
					require.Equal(t, *source.TestCases[0].InputArtifact, *item.OriginalInputArtifact)
				} else {
					require.Equal(t, test.raw, source.TestCases[0].Input)
				}
				found := false
				for _, record := range provenance.records {
					if record.SourceType == "semantic_spec_input_canonicalization_v1" {
						found = true
						var metadata map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(record.Metadata, &metadata))
						var originalRef ArtifactRef
						require.NoError(t, json.Unmarshal(metadata["original_input_artifact"], &originalRef))
						require.Equal(t, *item.OriginalInputArtifact, originalRef)
						require.JSONEq(t, `"`+item.InputArtifact.SHA256+`"`, string(metadata["canonical_input_sha256"]))
						require.NotEmpty(t, metadata["binding_sha256"])
						require.NotEmpty(t, metadata["input_parser_rule_version"])
					}
				}
				require.True(t, found)
			})
		}
	}
}

func TestS3CaseInputNormalizationLeavesAlreadyCanonicalContractBytesUnchanged(t *testing.T) {
	acts, _, provenance, _, result := materializeNormalizedInputFixtureV1(t, "1\n1\n", false, false)
	plan, _, err := acts.loadS3CasePlanV1(context.Background(), *result.CasePlanArtifact)
	require.NoError(t, err)
	item := plan.Cases[0]
	require.Nil(t, item.OriginalInputArtifact)
	require.Empty(t, item.InputNormalizationRuleVersion)
	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	// Freeze the pre-normalization field order/shape for completed histories.
	legacy := struct {
		TestID                        string      `json:"test_id"`
		Purpose                       string      `json:"purpose"`
		ConstraintRegion              string      `json:"constraint_region"`
		BoundaryRefs                  []string    `json:"boundary_refs"`
		Seed                          int64       `json:"seed"`
		InputArtifact                 ArtifactRef `json:"input_artifact"`
		KilledWrongIDs                []string    `json:"killed_wrong_ids"`
		KilledWrongIDsRetentionReason string      `json:"killed_wrong_ids_retention_reason"`
	}{item.TestID, item.Purpose, item.ConstraintRegion, item.BoundaryRefs, item.Seed, item.InputArtifact, item.KilledWrongIDs, item.KilledWrongIDsRetentionReason}
	want, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.Equal(t, string(want), string(encoded))
	for _, record := range provenance.records {
		require.NotEqual(t, "semantic_spec_input_canonicalization_v1", record.SourceType)
	}
}

func TestS3CaseInputNormalizationStillRejectsInvalidGrammarAndConstraintValues(t *testing.T) {
	for _, raw := range []string{"2\n1\n", "1\n1000000001\n", "1\n1\n2\n", "1\n1\t\n", "1\nnot-an-integer\n"} {
		fixture := newStatementDraftFixture(t, nil)
		fixture.bundle.RequestedTestCaseCount, fixture.bundle.MinTestCaseCount, fixture.bundle.MaxTestCaseCount, fixture.bundle.AdaptiveTestCaseCount = 1, 0, 0, false
		fixture.replaceBundle(t)
		acts := New(&Dependencies{ArtifactStore: fixture.store, ProvenanceRecorder: &captureProvenanceRecorder{}})
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		env.RegisterActivity(acts.MaterializeS3CasePlanActivityV1)
		_, err := env.ExecuteActivity(acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: fixture.input.BundleArtifact, ExpectedAuthoringBundleSHA: fixture.input.ExpectedBundleSHA256, TestData: TestDataResult{PayloadVersion: ActivityPayloadVersion, TestCases: []TestCaseData{{Input: raw, Origin: TestCaseOriginLLMInline}}}})
		require.ErrorContains(t, err, "does not satisfy SemanticSpec input", raw)
		require.NotContains(t, err.Error(), "<nil>")
	}
}

type canonicalCaseExecutionCheckerV1 struct {
	remotesandbox.RemoteExecutor
	t       *testing.T
	allowed map[string]bool
}

func (c canonicalCaseExecutionCheckerV1) Execute(ctx context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
	for _, input := range inputs {
		require.True(c.t, c.allowed[input], "sandbox execution must use canonical case/sample bytes: %q", input)
	}
	return c.RemoteExecutor.Execute(ctx, language, source, inputs, limits)
}

func TestS3NormalizedInputsKeepOracleSanitizerManifestSampleAndStoreBindings(t *testing.T) {
	// Reuse the real activity fixture with interior cases, so normalization
	// also crosses the present-empty boundary_refs branch of manifest building.
	f, _ := newS3ManifestInteriorCaseFixtureV1(t)
	oldPlan, _, err := f.acts.loadS3CasePlanV1(context.Background(), f.oracleInput.CasePlanArtifact)
	require.NoError(t, err)
	data := TestDataResult{PayloadVersion: ActivityPayloadVersion}
	allowed := map[string]bool{}
	for i, item := range oldPlan.Cases {
		input, err := f.store.Get(context.Background(), item.InputArtifact)
		require.NoError(t, err)
		allowed[string(input)] = true
		raw := string(input)
		if i == 10 {
			raw = "2  \r\n  bb   c   "
		}
		data.TestCases = append(data.TestCases, TestCaseData{Input: raw, Origin: TestCaseOriginGenerator, GeneratorSeed: int64(i + 1), IsSample: i < 4})
	}
	materializeInput := MaterializeS3CasePlanInputV1{PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: f.authoringRef, ExpectedAuthoringBundleSHA: f.authoringRef.SHA256, TestData: data}
	f.materialized = executeS3BatchTestActivityV1[MaterializeS3CasePlanResultV1](t, f.acts.MaterializeS3CasePlanActivityV1, materializeInput)
	repeated := executeS3BatchTestActivityV1[MaterializeS3CasePlanResultV1](t, f.acts.MaterializeS3CasePlanActivityV1, materializeInput)
	require.Equal(t, f.materialized.CasePlanSHA256, repeated.CasePlanSHA256)
	require.Equal(t, f.materialized.SanitizerSuiteSHA256, repeated.SanitizerSuiteSHA256)
	f.oracleInput.CasePlanArtifact = *f.materialized.CasePlanArtifact
	plan, _, err := f.acts.loadS3CasePlanV1(context.Background(), f.oracleInput.CasePlanArtifact)
	require.NoError(t, err)
	require.NotNil(t, plan.Cases[10].OriginalInputArtifact)
	require.NotNil(t, plan.Cases[10].BoundaryRefs)
	require.Empty(t, plan.Cases[10].BoundaryRefs)
	baseFactory := f.acts.deps.RemoteSandboxFactory
	f.acts.deps.RemoteSandboxFactory = func(timeout time.Duration) (remotesandbox.RemoteExecutor, error) {
		executor, err := baseFactory(timeout)
		return canonicalCaseExecutionCheckerV1{RemoteExecutor: executor, t: t, allowed: allowed}, err
	}
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusPass, verified.Gate.Status)
	oracleBytes, err := f.store.Get(context.Background(), *verified.ReceiptArtifact)
	require.NoError(t, err)
	var oracle S3OracleGateReceiptV1
	require.NoError(t, json.Unmarshal(oracleBytes, &oracle))
	require.Equal(t, plan.Cases[10].InputArtifact.SHA256, oracle.OrderedInputSHA256[10])
	require.NotEqual(t, plan.Cases[10].OriginalInputArtifact.SHA256, oracle.OrderedInputSHA256[10])
	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp", VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact, ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	require.Equal(t, qualitygate.GateStatusPass, sanitized.Gate.Status)
	manifestInput := S3ManifestGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef, CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun, OracleReceiptArtifact: *verified.ReceiptArtifact, VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SanitizerReceiptArtifact: *sanitized.ReceiptArtifact}
	manifestResult := executeS3BatchTestActivityV1[S3ManifestGateResultV1](t, f.acts.BuildS3TestManifestActivityV1, manifestInput)
	require.Equal(t, qualitygate.GateStatusPass, manifestResult.TestManifestGate.Status)
	manifestBytes, err := f.store.Get(context.Background(), *manifestResult.TestManifestArtifact)
	require.NoError(t, err)
	manifest, err := ParseTestManifestV2JSON(manifestBytes)
	require.NoError(t, err)
	require.NotNil(t, manifest.Cases[10].BoundaryRefs)
	require.Empty(t, manifest.Cases[10].BoundaryRefs)
	cases, _ := s3QualityDraftTestCasesV1(manifest)
	for i, item := range manifest.Cases {
		require.Equal(t, plan.Cases[i].InputArtifact.SHA256, item.InputSHA256)
		require.Equal(t, plan.Cases[i].InputArtifact, *cases[i].InputArtifact)
	}
	validated := executeS3BatchTestActivityV1[ValidateAuthoringSampleCandidatesResult](t, f.acts.ValidateAuthoringSampleCandidatesActivityV1, f.validatedInput)
	sampleRun := executeS3BatchTestActivityV1[AuthoringSampleSandboxRunV1](t, f.acts.RunVerifiedAuthoringSamplesActivityV1, RunVerifiedAuthoringSamplesInputV1{PayloadVersion: S3QualityPayloadVersionV1, Phase: AuthoringSampleSandboxRunPhaseFirst, ProgramArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp", VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SourceArtifact: *validated.ValidatedBundleArtifact, Limits: f.oracleInput.Limits})
	sampleBytes, err := f.store.Get(context.Background(), *validated.ValidatedBundleArtifact)
	require.NoError(t, err)
	var sampleBundle ValidatedSampleCandidatesBundleV1
	require.NoError(t, json.Unmarshal(sampleBytes, &sampleBundle))
	require.Equal(t, speccontract.SampleInputParserTokenStringsRuleVersionV2, sampleBundle.ParserRuleVersion)
	require.Len(t, sampleRun.InputSHA256, 12)
	// An execution receipt for original raw bytes must not be relabeled as
	// canonical evidence, even though both spellings parse to the same values.
	oracle.OrderedInputSHA256[10] = plan.Cases[10].OriginalInputArtifact.SHA256
	tamperedBytes, err := json.Marshal(oracle)
	require.NoError(t, err)
	manifestInput.OracleReceiptArtifact = s3BatchSeedArtifactV1(t, f.store, tamperedBytes, "application/json", verified.ReceiptArtifact.Producer)
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(f.acts.BuildS3TestManifestActivityV1)
	_, err = env.ExecuteActivity(f.acts.BuildS3TestManifestActivityV1, manifestInput)
	require.ErrorContains(t, err, "case lineage differs")
}

func TestS3CaseInputNormalizationRejectsUnboundOrForgedSourceMetadata(t *testing.T) {
	acts, _, _, _, result := materializeNormalizedInputFixtureV1(t, "1  \n 1  \n", false, false)
	plan, _, err := acts.loadS3CasePlanV1(context.Background(), *result.CasePlanArtifact)
	require.NoError(t, err)
	encoded, err := json.Marshal(plan.Cases[0])
	require.NoError(t, err)
	for _, test := range []struct {
		name   string
		mutate func(*S3ManifestCasePlanV1)
	}{
		{"missing original", func(c *S3ManifestCasePlanV1) { c.OriginalInputArtifact = nil }},
		{"missing rule", func(c *S3ManifestCasePlanV1) { c.InputNormalizationRuleVersion = "" }},
		{"unknown rule", func(c *S3ManifestCasePlanV1) { c.InputNormalizationRuleVersion = "untrusted" }},
		{"same source hash", func(c *S3ManifestCasePlanV1) { source := c.InputArtifact; c.OriginalInputArtifact = &source }},
		{"model claiming canonical input", func(c *S3ManifestCasePlanV1) { c.InputArtifact.Producer = "GenerateS3TestDataActivityV1" }},
		{"other bucket", func(c *S3ManifestCasePlanV1) { c.OriginalInputArtifact.Bucket = "foreign-fixture" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var item S3ManifestCasePlanV1
			require.NoError(t, json.Unmarshal(encoded, &item))
			test.mutate(&item)
			require.Error(t, validateS3CaseInputNormalizationV1(item))
		})
	}
}
