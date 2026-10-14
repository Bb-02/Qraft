package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func executeS3BatchTestActivityV1[T any](t *testing.T, fn any, input any) T {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(fn)
	encoded, err := env.ExecuteActivity(fn, input)
	require.NoError(t, err)
	var result T
	require.NoError(t, encoded.Get(&result))
	return result
}

type s3BatchGateFixtureV1 struct {
	acts             *Activities
	store            ArtifactStore
	oracleInput      S3OracleGateInputV1
	authoringRef     ArtifactRef
	semanticRef      ArtifactRef
	materialized     MaterializeS3CasePlanResultV1
	validatedInput   ValidateAuthoringSampleCandidatesInput
	calls            int
	mismatch         bool
	sanitizerFinding bool
}

func newS3BatchGateFixtureV1(t *testing.T) *s3BatchGateFixtureV1 {
	t.Helper()
	sampleInputs := []string{"0\n\n", "1\na\n", "2\nbb c\n", "3\nd ee fff\n"}
	allInputs := make([]string, 12)
	for i := range allInputs {
		allInputs[i] = sampleInputs[i%4]
	}
	allInputs[11] = "3\ng hh iii\n"
	authoring := tokenStringAuthoringFixtureV2(t, allInputs)
	authoring.bundle.RequestedTestCaseCount = 12
	authoring.replaceBundle(t)
	f := &s3BatchGateFixtureV1{store: authoring.store, authoringRef: authoring.statementDraftFixture.input.BundleArtifact}
	f.validatedInput = authoring.input
	f.validatedInput.BundleArtifact = f.authoringRef
	f.validatedInput.ExpectedBundleSHA256 = f.authoringRef.SHA256
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		f.calls++
		require.EqualValues(t, 8<<20, limits.OutputLimitBytes)
		require.LessOrEqual(t, int64(len(inputs))*limits.OutputLimitBytes, int64(64<<20))
		r := batchSandboxResponseV1(inputs, limits, fmt.Sprintf("real-batch-%d", f.calls))
		for i, input := range inputs {
			reader := strings.NewReader(input)
			var n, total int
			_, err := fmt.Fscan(reader, &n)
			require.NoError(t, err)
			for j := 0; j < n; j++ {
				var token string
				_, err := fmt.Fscan(reader, &token)
				require.NoError(t, err)
				total += len(token)
			}
			r.Results[i].Stdout = fmt.Sprintf("%d \n", total)
			if input == "3\ng hh iii\n" {
				if source == "synthetic-oracle" && f.mismatch {
					r.Results[i].Stdout += "changed\n"
				}
				if limits.Profile != "" && f.sanitizerFinding {
					r.Results[i].Verdict = remotesandbox.VerdictRE
				}
			}
		}
		return r, nil
	}}
	f.acts = New(&Dependencies{ArtifactStore: f.store, ProvenanceRecorder: &captureProvenanceRecorder{},
		RemoteSandboxFactory:    func(time.Duration) (remotesandbox.RemoteExecutor, error) { return fake, nil },
		S3SandboxIdentityPolicy: &S3SandboxIdentityPolicyV1{ImageDigest: s3TestDigest("a"), ToolchainManifestDigest: s3TestDigest("b"), SeccompPolicyDigest: s3TestDigest("c")}})
	data := TestDataResult{PayloadVersion: ActivityPayloadVersion}
	for i := 0; i < 12; i++ {
		input := authoring.bundle.SemanticSpec.SampleInputs[i]
		data.TestCases = append(data.TestCases, TestCaseData{Input: input, Origin: TestCaseOriginCustom, IsSample: i < 4})
	}
	f.materialized = executeS3BatchTestActivityV1[MaterializeS3CasePlanResultV1](t, f.acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: f.authoringRef, ExpectedAuthoringBundleSHA: f.authoringRef.SHA256, TestData: data})
	semanticBytes, err := json.Marshal(authoring.bundle.SemanticSpec)
	require.NoError(t, err)
	f.semanticRef = s3BatchSeedArtifactV1(t, f.store, semanticBytes, "application/json", "ExtractSemanticSpecArtifactActivityV1")
	promotion := validOraclePromotionInputV1(t)
	mainRef := s3BatchSeedArtifactV1(t, f.store, []byte("synthetic-main"), "text/plain", "GenerateMainSolutionActivityV1")
	oracleRef := s3BatchSeedArtifactV1(t, f.store, []byte("synthetic-oracle"), "text/plain", "GenerateOracleCandidateActivityV1")
	candidate := promotion.Candidate
	candidate.CandidateSourceArtifact, candidate.CandidateSourceSHA256 = &oracleRef, oracleRef.SHA256
	candidate.SourceBinding.SemanticSpecSHA256 = f.semanticRef.SHA256
	candidate.SourceBinding.SemanticSpecArtifactSHA256 = f.semanticRef.SHA256
	candidate.SourceBinding.CandidateSourceSHA256 = oracleRef.SHA256
	candidate.SourceArtifacts = []*ArtifactRef{&f.semanticRef, candidate.ProviderResponseArtifact, &oracleRef}
	f.oracleInput = S3OracleGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, ExpectedSemanticSpecSHA256: f.semanticRef.SHA256,
		MainCandidate: GenerateMainSolutionResultV1{SchemaVersion: GenerateMainSolutionSchemaV1, SemanticSpecSHA256: f.semanticRef.SHA256,
			Language: "cpp", CandidateSourceArtifact: &mainRef, CandidateSourceSHA256: mainRef.SHA256, ProviderResponseArtifact: &promotion.MainProviderArtifact},
		OracleCandidate: candidate, CasePlanArtifact: *f.materialized.CasePlanArtifact, Limits: ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64}}
	return f
}

func s3BatchSeedArtifactV1(t *testing.T, store ArtifactStore, data []byte, contentType, producer string) ArtifactRef {
	t.Helper()
	ref, err := store.Put(context.Background(), data, contentType, ArtifactMetadata{
		Producer: producer, Provider: "fixture-provider", Model: "fixture-model", ModelRevision: "fixture-revision", WorkflowID: "fixture-workflow", PayloadVersion: ActivityPayloadVersion})
	require.NoError(t, err)
	return ref
}

func TestS3BatchVerificationSanitizerAndManifestKeepAllTwelveCases(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusPass, verified.Gate.Status)
	require.Equal(t, 4, f.calls, "two complete batches per independent program")
	require.Len(t, verified.MainRun.Outputs, 12)
	require.Len(t, verified.MainRun.Batches, 2)
	require.Empty(t, verified.MainRun.Audit.RunID)
	data, err := f.store.Get(context.Background(), *verified.ReceiptArtifact)
	require.NoError(t, err)
	var oracle S3OracleGateReceiptV1
	require.NoError(t, decodeCanonicalSampleClosureJSONV1(data, &oracle))
	require.Len(t, oracle.MainBatches, 2)
	require.Len(t, oracle.OracleBatches, 2)
	require.Len(t, oracle.DifferentialCaseIDs, 12)
	require.Equal(t, "real-batch-1", oracle.MainBatches[0].Audit.RunID)
	require.Equal(t, "real-batch-2", oracle.MainBatches[1].Audit.RunID)
	require.Empty(t, oracle.SandboxIdentity.RunID)
	require.Empty(t, oracle.SandboxIdentity.ManifestDigest)

	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp",
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact,
		ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	require.Equal(t, qualitygate.GateStatusPass, sanitized.Gate.Status)
	require.Equal(t, 6, f.calls, "the sanitizer also preserves all cases in its own bounded batches")
	data, err = f.store.Get(context.Background(), *sanitized.ReceiptArtifact)
	require.NoError(t, err)
	var sanitizer S3SanitizerReceiptV1
	require.NoError(t, decodeCanonicalSampleClosureJSONV1(data, &sanitizer))
	require.Len(t, sanitizer.Execution.Batches, 2)
	require.Equal(t, SandboxAuditMetadata{}, sanitizer.Execution.Audit)

	manifest := executeS3BatchTestActivityV1[S3ManifestGateResultV1](t, f.acts.BuildS3TestManifestActivityV1, S3ManifestGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef,
		CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun, OracleReceiptArtifact: *verified.ReceiptArtifact,
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SanitizerReceiptArtifact: *sanitized.ReceiptArtifact})
	require.Equal(t, qualitygate.GateStatusPass, manifest.TestManifestGate.Status)
	require.Equal(t, qualitygate.GateStatusPass, manifest.BoundaryCoverageGate.Status)
	data, err = f.store.Get(context.Background(), *manifest.TestManifestArtifact)
	require.NoError(t, err)
	parsedManifest, err := ParseTestManifestV2JSON(data)
	require.NoError(t, err)
	require.Equal(t, 12, parsedManifest.TestCount)

	validatedSamples := executeS3BatchTestActivityV1[ValidateAuthoringSampleCandidatesResult](t, f.acts.ValidateAuthoringSampleCandidatesActivityV1, f.validatedInput)
	sampleRun := executeS3BatchTestActivityV1[AuthoringSampleSandboxRunV1](t, f.acts.RunVerifiedAuthoringSamplesActivityV1, RunVerifiedAuthoringSamplesInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, Phase: AuthoringSampleSandboxRunPhaseFirst,
		ProgramArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp",
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SourceArtifact: *validatedSamples.ValidatedBundleArtifact, Limits: f.oracleInput.Limits})
	require.Len(t, sampleRun.SandboxOutput.Batches, 2)
	require.Equal(t, 8, f.calls)
	data, err = f.store.Get(context.Background(), *verified.VerifiedProgramReceiptArtifact)
	require.NoError(t, err)
	programReceipt, err := decodeCanonicalVerifiedProgramReceiptV1(data)
	require.NoError(t, err)
	require.NoError(t, validateAuthoringSampleSandboxRunV1(sampleRun, sampleRun.Phase, *programReceipt, verified.VerifiedProgramReceiptArtifact.SHA256, sampleRun.InputSHA256))
}

func TestS3BatchVerificationDoesNotApproveMismatchInTheLastBatch(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	f.mismatch = true
	result := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusBlocked, result.Gate.Status)
	require.Nil(t, result.VerifiedProgramReceiptArtifact)
	data, err := f.store.Get(context.Background(), *result.ReceiptArtifact)
	require.NoError(t, err)
	var receipt S3OracleGateReceiptV1
	require.NoError(t, decodeCanonicalSampleClosureJSONV1(data, &receipt))
	require.Equal(t, "case-000011", receipt.FirstMismatchCaseID)
	require.Equal(t, 4, f.calls)
}

func TestS3BatchSanitizerKeepsFindingInTheLastBatchBlocked(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	f.sanitizerFinding = true
	result := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp",
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact,
		ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	require.Equal(t, qualitygate.GateStatusBlocked, result.Gate.Status)
	require.Equal(t, 6, f.calls)
	require.NotEmpty(t, result.Gate.Blockers)
}

func TestS3BatchedEvidenceRejectsMissingOverlappingOrDifferentRequestIdentities(t *testing.T) {
	audit := SandboxAuditMetadata{RunID: "batch-one", ManifestDigest: s3TestDigest("1"), ImageDigest: s3TestDigest("a"),
		ToolchainManifestDigest: s3TestDigest("b"), SeccompPolicyDigest: s3TestDigest("c"), LimitProfile: "fixed-profile"}
	base := SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: make([]string, 9), TimeTaken: make([]time.Duration, 9), MemoryUsed: make([]int64, 9),
		Batches: []SandboxExecutionBatch{{FirstCase: 0, CaseCount: 8, Audit: audit}, {FirstCase: 8, CaseCount: 1, Audit: audit}}}
	_, err := s3SandboxRunIdentityV1(base, 9)
	require.NoError(t, err)
	for name, mutate := range map[string]func(*SandboxResult){
		"claims single receipt":       func(run *SandboxResult) { run.Audit = audit },
		"missing final batch":         func(run *SandboxResult) { run.Batches = run.Batches[:1] },
		"overlap":                     func(run *SandboxResult) { run.Batches[1].FirstCase = 7 },
		"missing run":                 func(run *SandboxResult) { run.Batches[1].Audit.RunID = "" },
		"different image":             func(run *SandboxResult) { run.Batches[1].Audit.ImageDigest = s3TestDigest("d") },
		"different limits":            func(run *SandboxResult) { run.Batches[1].Audit.LimitProfile = "different-profile" },
		"different sanitizer profile": func(run *SandboxResult) { run.Batches[1].Audit.Profile = remotesandbox.SanitizerCAndCPPProfileV1 },
		"different seed":              func(run *SandboxResult) { run.Batches[1].Audit.Seed++ },
	} {
		t.Run(name, func(t *testing.T) {
			run := base
			run.Batches = append([]SandboxExecutionBatch(nil), base.Batches...)
			mutate(&run)
			_, err := s3SandboxRunIdentityV1(run, 9)
			require.Error(t, err)
			receipt := VerifiedProgramReceiptV1{ProgramSHA256: strings.Repeat("a", 64), IndependentOracleReceiptSHA256: strings.Repeat("b", 64),
				SandboxImageDigest: audit.ImageDigest, ToolchainManifestDigest: audit.ToolchainManifestDigest, SeccompPolicyDigest: audit.SeccompPolicyDigest, LimitProfile: audit.LimitProfile}
			inputSHA := make([]string, 9)
			for i := range inputSHA {
				inputSHA[i] = strings.Repeat("c", 64)
			}
			envelope := sampleClosureSandboxRun(AuthoringSampleSandboxRunPhaseFirst, strings.Repeat("d", 64), receipt, inputSHA, run.Outputs)
			envelope.SandboxOutput = run
			require.Error(t, validateAuthoringSampleSandboxRunV1(envelope, envelope.Phase, receipt, envelope.VerifiedProgramReceiptSHA256, inputSHA))
		})
	}
}

func TestS3QualityDraftMaterializationKeepsActualBatchReceipts(t *testing.T) {
	fixture := newS3QualityDraftMaterializationFixture(t, generationapi.JobIDPrefix+strings.Repeat("6", 64))
	data, err := fixture.store.Get(fixture.ctx, fixture.input.OracleReceiptArtifact)
	require.NoError(t, err)
	var oracle S3OracleGateReceiptV1
	require.NoError(t, json.Unmarshal(data, &oracle))
	limits := remotesandbox.NewRemoteLimits(1000, 64)
	limits.OutputLimitBytes = S3StableOutputLimitBytesV1
	audit := batchSandboxResponseV1([]string{"1"}, limits, "first").Audit
	first := SandboxAuditMetadata{RunID: audit.RunID, ManifestDigest: audit.ManifestDigest, ImageDigest: audit.ImageDigest,
		ToolchainManifestDigest: audit.ToolchainManifestDigest, SeccompPolicyDigest: audit.SeccompPolicyDigest, LimitProfile: audit.LimitProfile}
	second := first
	second.RunID = "second"
	second.ManifestDigest = s3TestDigest("2")
	oracle.MainBatches = []SandboxExecutionBatch{{FirstCase: 0, CaseCount: 1, Audit: first}, {FirstCase: 1, CaseCount: 1, Audit: second}}
	oracle.OracleBatches = append([]SandboxExecutionBatch(nil), oracle.MainBatches...)
	oracle.SandboxIdentity = first
	oracle.SandboxIdentity.RunID, oracle.SandboxIdentity.ManifestDigest = "", ""
	data, err = json.Marshal(oracle)
	require.NoError(t, err)
	fixture.input.OracleReceiptArtifact = fixture.put(data, "application/json", VerifiedProgramReceiptProducerV1)

	data, err = fixture.store.Get(fixture.ctx, fixture.input.FinalStatementArtifact)
	require.NoError(t, err)
	var final FinalAuthoringStatementSamplesBundleV1
	require.NoError(t, json.Unmarshal(data, &final))
	final.IndependentOracleReceiptSHA256 = fixture.input.OracleReceiptArtifact.SHA256
	data, err = json.Marshal(final)
	require.NoError(t, err)
	fixture.input.FinalStatementArtifact = fixture.put(data, "application/json", "FinalizeAuthoringStatementSamplesActivityV1")
	fixture.input.SubjectRevision = fixture.input.FinalStatementArtifact.SHA256

	data, err = fixture.store.Get(fixture.ctx, fixture.input.TestManifestArtifact)
	require.NoError(t, err)
	manifest, err := ParseTestManifestV2JSON(data)
	require.NoError(t, err)
	manifest.OraclePromotionReceiptSHA256 = fixture.input.OracleReceiptArtifact.SHA256
	data, _, err = CanonicalTestManifestV2JSON(*manifest)
	require.NoError(t, err)
	fixture.input.TestManifestArtifact = fixture.put(data, "application/json", "BuildS3TestManifestActivityV1")
	var qualityAudit qualitygate.AuditV1
	require.NoError(t, json.Unmarshal(fixture.auditBytes, &qualityAudit))
	qualityAudit.SubjectRevision = fixture.input.SubjectRevision
	data, _, err = qualitygate.CanonicalAuditV1(qualityAudit)
	require.NoError(t, err)
	fixture.input.AuditArtifact = fixture.put(data, "application/json", "RecomputeS3VerdictActivityV1")

	stored, err := fixture.activities.buildS3QualityDraftStoreInputV1(fixture.ctx, fixture.input)
	require.NoError(t, err)
	require.Equal(t, SandboxAuditMetadata{}, stored.SandboxOutput.Audit)
	require.Equal(t, oracle.MainBatches, stored.SandboxOutput.Batches)
	data, err = json.Marshal(stored)
	require.NoError(t, err)
	var restored StoreInput
	require.NoError(t, json.Unmarshal(data, &restored))
	require.NoError(t, fixture.activities.validateS3QualityPassDraftStoreInputV1(fixture.ctx, restored))
	require.NoError(t, fixture.activities.validateTestManifestStoreInput(fixture.ctx, restored))
	require.Equal(t, oracle.MainBatches, restored.SandboxOutput.Batches)
}
