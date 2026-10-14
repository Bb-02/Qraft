package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func tokenStringSpecV2(kind string) domain.SemanticSpecV1 {
	zero, one, three := int64(0), int64(1), int64(3)
	field := domain.SemanticGrammarFieldV1{Symbol: "s", Mode: domain.SemanticGrammarFieldScalar}
	minimum := &one
	if kind == domain.SemanticSymbolStringSequence {
		field.Mode, field.Count, minimum = domain.SemanticGrammarFieldSequence, &domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}, &zero
	}
	return domain.SemanticSpecV1{SchemaVersion: domain.SemanticSpecSchemaV1, Symbols: []domain.SemanticSymbolV1{{Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput}, {Name: "s", Type: kind, Scope: domain.SemanticSymbolScopeInput}},
		InputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{
			{ID: "count", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}}},
			{ID: "text", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{field}},
		}},
		Constraints: []domain.SemanticConstraintV1{{Subject: "n", Kind: domain.SemanticConstraintIntegerRange, Min: &zero, Max: &three}, {Subject: "s", Kind: domain.SemanticConstraintLengthRange, Min: minimum, Max: &three}},
		Relations:   []domain.SemanticRelationV1{{ID: "length", Left: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "s"}, Operator: domain.SemanticRelationEqual, Right: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}}}}
}

func TestTokenStringInputV2ScalarSequenceRepeatAndCanonicalBindings(t *testing.T) {
	for _, tc := range []struct {
		name, kind, raw, canonical string
		repeat                     bool
	}{
		{"scalar", domain.SemanticSymbolString, "3\r\n abc \r\n", "3\nabc\n", false},
		{"sequence", domain.SemanticSymbolStringSequence, "3\n a   bb c \n", "3\na bb c\n", false},
		{"empty sequence", domain.SemanticSymbolStringSequence, "0\n\n", "0\n\n", false},
		{"repeated", domain.SemanticSymbolStringSequence, "2\na\nbb\n", "2\na\nbb\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tokenStringSpecV2(tc.kind)
			if tc.repeat {
				spec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
				spec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldElementPerRepeat
				spec.InputGrammar.Lines[1].Fields[0].Count = nil
			}
			parsed, err := speccontract.ParseSampleInputV1(spec, tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.canonical, parsed.CanonicalInput)
			require.Equal(t, speccontract.SampleInputParserTokenStringsRuleVersionV2, parsed.ParserRuleVersion)
			require.Equal(t, tc.kind, parsed.Bindings[1].Kind)
			encoded, err := json.Marshal(parsed.Bindings)
			require.NoError(t, err)
			require.Equal(t, sha256Hex(encoded), parsed.BindingSHA256)
			again, err := speccontract.ParseSampleInputV1(spec, parsed.CanonicalInput)
			require.NoError(t, err)
			require.Equal(t, parsed, again)
			require.NoError(t, validateParsedSampleInputReceiptV1(parsed, parsed.InputGrammarSHA256, speccontract.SampleInputParserRuleVersionForSpecV1(spec)))
			require.Error(t, validateParsedSampleInputReceiptV1(parsed, parsed.InputGrammarSHA256), "legacy reader cannot accept v2 based on a self-declared header")
		})
	}
}

func TestTokenStringInputV2RejectsUnsupportedTextAndRealConstraintFailures(t *testing.T) {
	spec := tokenStringSpecV2(domain.SemanticSymbolString)
	for _, raw := range []string{"1\n\n", "2\na b\n", "1\né\n", "1\n中\n"} {
		_, err := speccontract.ParseSampleInputV1(spec, raw)
		require.ErrorContains(t, err, "token string requires non-empty ASCII")
	}
	for _, raw := range []string{"2\na\n", "3\nlong\n", "1\na\nextra\n", "1\n\xff\n", "1\na\t\n"} {
		_, err := speccontract.ParseSampleInputV1(spec, raw)
		require.Error(t, err)
	}
	zero := int64(0)
	spec.Constraints[1].Min = &zero
	require.ErrorContains(t, speccontract.ValidateSampleInputProfileV1(spec), "empty string")
	spec = tokenStringSpecV2(domain.SemanticSymbolString)
	spec.Boundaries = []domain.SemanticBoundaryV1{{ID: "forbid-one", Symbol: "s", Measure: domain.SemanticBoundaryMeasureLength, Value: 1, Allowed: false}}
	_, err := speccontract.ParseSampleInputV1(spec, "1\na\n")
	require.ErrorContains(t, err, "forbidden boundary")
	spec.Symbols[1].Type = domain.SemanticSymbolGraph
	require.ErrorContains(t, speccontract.ValidateSampleInputProfileV1(spec), "unsupported parser type")
}

func tokenStringAuthoringFixtureV2(t *testing.T, samples []string) *validatedSampleCandidatesFixture {
	t.Helper()
	fixture := newValidatedSampleCandidatesFixture(t, samples)
	spec := fixture.bundle.SemanticSpec
	zero, three := int64(0), int64(3)
	spec.ProblemDefinition = "Read n nonempty ASCII tokens and print their total character count."
	spec.Sections.Input.Summary = "Read n, then exactly n nonempty ASCII tokens separated by spaces on one line."
	spec.Sections.Output.Summary = "Print the total number of ASCII characters."
	spec.Sections.Constraints.Summary = "The token count is in [0,3]; each token is nonempty ASCII without whitespace."
	for i := range spec.Symbols {
		if spec.Symbols[i].Name == "a" {
			spec.Symbols[i].Type, spec.Symbols[i].Definition = domain.SemanticSymbolStringSequence, "ordered nonempty ASCII tokens"
		}
	}
	spec.Constraints = spec.Constraints[:2]
	for i := range spec.Constraints {
		spec.Constraints[i].Min, spec.Constraints[i].Max = &zero, &three
	}
	spec.Boundaries = nil
	for _, subject := range []string{"n", "a"} {
		measure := domain.SemanticBoundaryMeasureValue
		if subject == "a" {
			measure = domain.SemanticBoundaryMeasureLength
		}
		for _, value := range []int64{0, 1, 2, 3} {
			spec.Boundaries = append(spec.Boundaries, domain.SemanticBoundaryV1{ID: fmt.Sprintf("%s-%d", subject, value), Symbol: subject, Measure: measure, Value: value, Allowed: true, Meaning: "explicit token-count boundary"})
		}
	}
	spec.SampleInputs = append([]string(nil), samples...)
	plan := fixture.bundle.AuthoringPlan
	plan.IntendedSolution.Summary, plan.BruteForceBaseline.Summary, plan.OracleCandidateStrategy.Summary = "sum ASCII token lengths", "direct token-length sum", "independent character-count sum"
	plan.TestIntents = nil
	for _, value := range []int64{0, 1, 2, 3} {
		plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "token-count edge", ConstraintRegion: fmt.Sprintf("n=%d", value), BoundaryRefs: []string{fmt.Sprintf("n-%d", value), fmt.Sprintf("a-%d", value)}})
	}
	semanticSHA := speccontract.SemanticSpecSHA256V1(*spec)
	plan.SemanticSpecSHA256 = semanticSHA
	lint := speccontract.LintV1(speccontract.LintInputV1{SemanticSpec: *spec, AuthoringPlan: *plan, ExpectedBriefSHA256: fixture.bundle.BriefSHA256, ExpectedDifficulty: fixture.input.ExpectedDifficulty, RequiredKnowledgePoints: fixture.input.RequiredKnowledgePoints})
	require.True(t, lint.Passed, "%+v", lint.Issues)
	fixture.bundle.LintReport = &lint
	fixture.bundle.RequestedTestCaseCount, fixture.bundle.MinTestCaseCount, fixture.bundle.MaxTestCaseCount, fixture.bundle.AdaptiveTestCaseCount = 4, 0, 0, false
	fixture.statementDraftFixture.input.ExpectedSemanticSpecSHA256 = semanticSHA
	fixture.input.ExpectedSemanticSpecSHA256 = semanticSHA
	fixture.replaceBundle(t)
	fixture.input.BundleArtifact, fixture.input.ExpectedBundleSHA256 = fixture.statementDraftFixture.input.BundleArtifact, fixture.statementDraftFixture.input.ExpectedBundleSHA256
	return fixture
}

func TestTokenStringInputV2ValidatedCASStageAndFullcaseCoverage(t *testing.T) {
	samples := []string{"0\n\n", "1\na\n", "2\nbb c\n", "3\nd ee fff\n"}
	fixture := tokenStringAuthoringFixtureV2(t, samples)
	acts := &Activities{deps: &Dependencies{ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: fixture.store}
	validated, err := executeValidatedSampleCandidatesActivity(t, acts, fixture.input)
	require.NoError(t, err)
	require.Equal(t, speccontract.SampleInputParserTokenStringsRuleVersionV2, validated.ParserRuleVersion)
	bytes, err := fixture.store.Get(context.Background(), *validated.ValidatedBundleArtifact)
	require.NoError(t, err)
	var bundle ValidatedSampleCandidatesBundleV1
	require.NoError(t, decodeCanonicalSampleClosureJSONV1(bytes, &bundle))
	require.NoError(t, validateSampleCandidatesForClosureV1(bundle, fixture.bundle, fixture.input.ExpectedSemanticSpecSHA256, fixture.input.ExpectedBundleSHA256, validated.ValidatedBundleSHA256))
	tampered := bundle
	tampered.ParserRuleVersion = speccontract.SampleInputParserRuleVersionV1
	require.Error(t, validateSampleCandidatesForClosureV1(tampered, fixture.bundle, fixture.input.ExpectedSemanticSpecSHA256, fixture.input.ExpectedBundleSHA256, validated.ValidatedBundleSHA256))
	draft := seedSampleClosureStatementDraft(t, fixture)
	receipt := VerifiedProgramReceiptV1{SchemaVersion: VerifiedProgramReceiptSchemaV1, Status: VerifiedProgramReceiptStatusV1, ProgramSHA256: strings.Repeat("c", 64), IndependentOracleReceiptSHA256: strings.Repeat("d", 64), SandboxImageDigest: "sha256:" + strings.Repeat("1", 64), ToolchainManifestDigest: "sha256:" + strings.Repeat("2", 64), SeccompPolicyDigest: "sha256:" + strings.Repeat("3", 64), LimitProfile: "synthetic-token-strings-v2"}
	receiptBytes, err := json.Marshal(receipt)
	require.NoError(t, err)
	receiptRef := seedSampleClosureJSONArtifact(t, fixture.store, receiptBytes, VerifiedProgramReceiptProducerV1)
	inputSHA := make([]string, len(samples))
	for i := range samples {
		inputSHA[i] = bundle.Candidates[i].CanonicalInputSHA256
	}
	outputs := []string{"0\n", "1\n", "3\n", "6\n"}
	stageInput := StageAuthoringStatementSamplesInput{PayloadVersion: StageAuthoringStatementSamplesPayloadVersion, AuthoringBundleArtifact: fixture.input.BundleArtifact, ExpectedAuthoringBundleSHA256: fixture.input.ExpectedBundleSHA256, StatementDraftArtifact: draft, ExpectedStatementDraftSHA256: draft.SHA256, ValidatedSamplesArtifact: *validated.ValidatedBundleArtifact, ExpectedValidatedSamplesSHA256: validated.ValidatedBundleSHA256, VerifiedProgramReceiptArtifact: receiptRef, ExpectedVerifiedProgramReceiptSHA256: receiptRef.SHA256, FirstRun: sampleClosureSandboxRun(AuthoringSampleSandboxRunPhaseFirst, receiptRef.SHA256, receipt, inputSHA, outputs)}
	staged, err := executeStageAuthoringSamples(t, acts, stageInput)
	require.NoError(t, err)
	closure := &sampleClosureFixture{validatedFixture: fixture, activities: acts, store: fixture.store, receipt: receipt, validated: bundle, stageInput: stageInput}
	_, err = executeFinalizeAuthoringSamples(t, acts, closure.finalizeInput(t, staged, outputs))
	require.NoError(t, err)
	data := TestDataResult{PayloadVersion: ActivityPayloadVersion}
	for _, raw := range samples {
		data.TestCases = append(data.TestCases, TestCaseData{Input: raw, Origin: TestCaseOriginCustom})
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.MaterializeS3CasePlanActivityV1)
	value, err := env.ExecuteActivity(acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: fixture.input.BundleArtifact, ExpectedAuthoringBundleSHA: fixture.input.ExpectedBundleSHA256, TestData: data})
	require.NoError(t, err)
	var materialized MaterializeS3CasePlanResultV1
	require.NoError(t, value.Get(&materialized))
	casePlan, _, err := acts.loadS3CasePlanV1(context.Background(), *materialized.CasePlanArtifact)
	require.NoError(t, err)
	require.Equal(t, speccontract.TestCaseInputParserTokenStringsRuleVersionV2, casePlan.InputParserRuleVersion)
	fullCase, err := validateCasePlanInputParserRuleV2(*casePlan, *fixture.bundle.SemanticSpec)
	require.NoError(t, err)
	require.True(t, fullCase)
	manifest := TestManifestV2{SchemaVersion: TestManifestSchemaVersionV2, SemanticSpecSHA256: fixture.input.ExpectedSemanticSpecSHA256, OraclePromotionReceiptSHA256: strings.Repeat("b", 64), SanitizerReceiptSHA256: strings.Repeat("c", 64), TestCount: len(samples)}
	manifest.AuthoringPlanSHA256, err = canonicalJSONSHA256(*fixture.bundle.AuthoringPlan)
	require.NoError(t, err)
	inputs := make([]IntegerBoundaryCoverageCaseInputV1, len(samples))
	for i, item := range casePlan.Cases {
		out := manifestV2FixtureArtifact(outputs[i], "text/plain")
		input := item.InputArtifact
		manifest.Cases = append(manifest.Cases, TestManifestCaseV2{TestIndex: i, TestID: item.TestID, Purpose: item.Purpose, ConstraintRegion: item.ConstraintRegion, BoundaryRefs: item.BoundaryRefs, Seed: &item.Seed, InputArtifact: &input, InputSHA256: input.SHA256, OutputArtifact: &out, OutputSHA256: out.SHA256, KilledWrongIDs: []string{}, KilledWrongIDsRetentionReason: "synthetic fixture"})
		inputs[i] = IntegerBoundaryCoverageCaseInputV1{TestID: item.TestID, Input: samples[i]}
	}
	coverage, err := ValidateTestCaseBoundaryCoverageV2(*fixture.bundle.SemanticSpec, *fixture.bundle.AuthoringPlan, manifest, inputs)
	require.NoError(t, err)
	require.Equal(t, FullCaseTokenStringBoundaryCoverageRuleVersionV2, coverage.RuleVersion)
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(*coverage)
	require.Error(t, err, "self-declared v2 receipt cannot widen a v1 reader")
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(*coverage, boundaryCoverageReceiptRuleForSpecV1(*fixture.bundle.SemanticSpec, true))
	require.NoError(t, err)
	tamperedPlan := *casePlan
	tamperedPlan.InputParserRuleVersion = speccontract.TestCaseInputParserRuleVersionV2
	_, err = validateCasePlanInputParserRuleV2(tamperedPlan, *fixture.bundle.SemanticSpec)
	require.Error(t, err)
	inputs[1].Input = "1\né\n"
	manifest.Cases[1].InputSHA256 = sha256Hex([]byte(inputs[1].Input))
	manifest.Cases[1].InputArtifact = artifactRefPointer(manifestV2FixtureArtifact(inputs[1].Input, "text/plain"))
	_, err = ValidateTestCaseBoundaryCoverageV2(*fixture.bundle.SemanticSpec, *fixture.bundle.AuthoringPlan, manifest, inputs)
	require.ErrorContains(t, err, "token string requires non-empty ASCII")
}

func artifactRefPointer(ref ArtifactRef) *ArtifactRef { return &ref }

func TestFullcaseInputV2AcceptsMaximumIntegerInputWithoutWideningSamples(t *testing.T) {
	spec, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	maximum, elementMax := int64(200000), int64(1000000000)
	spec.Constraints[0].Max, spec.Constraints[1].Max, spec.Constraints[2].Max = &maximum, &maximum, &elementMax
	for i := range spec.Boundaries {
		switch spec.Boundaries[i].ID {
		case "n-max", "a-length-max":
			spec.Boundaries[i].Value = maximum
		case "a-element-max":
			spec.Boundaries[i].Value = elementMax
		}
	}
	plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(spec)
	manifest.SemanticSpecSHA256 = plan.SemanticSpecSHA256
	var err error
	manifest.AuthoringPlanSHA256, err = canonicalJSONSHA256(plan)
	require.NoError(t, err)
	raw := "200000\n" + strings.TrimSuffix(strings.Repeat("1000000000 ", 200000), " ") + "\n"
	require.Greater(t, len(raw), 2<<20)
	parsed, err := speccontract.ParseTestCaseInputV2(spec, raw)
	require.NoError(t, err)
	require.Equal(t, speccontract.TestCaseInputParserRuleVersionV2, parsed.ParserRuleVersion)
	require.Equal(t, raw, parsed.CanonicalInput)
	require.Len(t, parsed.Bindings[1].Integers, 200000)
	_, err = speccontract.ParseSampleInputV1(spec, raw)
	require.ErrorContains(t, err, "exceeds 1048576 bytes")
	_, err = speccontract.ParseTestCaseInputV2(spec, strings.Repeat("1", (8<<20)+1))
	require.ErrorContains(t, err, "exceeds 8388608 bytes")
	// Exercise the production materializer with a real CAS store as well as
	// the parser. The maximum case is valid pressure data, not a sample.
	fixture := newStatementDraftFixture(t, nil)
	fixture.bundle.RequestedTestCaseCount, fixture.bundle.MinTestCaseCount, fixture.bundle.MaxTestCaseCount, fixture.bundle.AdaptiveTestCaseCount = 3, 0, 0, false
	fixture.replaceBundle(t)
	acts := New(&Dependencies{ArtifactStore: fixture.store, ProvenanceRecorder: &captureProvenanceRecorder{}})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.MaterializeS3CasePlanActivityV1)
	data := TestDataResult{PayloadVersion: ActivityPayloadVersion, TestCases: []TestCaseData{{Input: "1\n-1000000000\n", Origin: TestCaseOriginCustom}, {Input: "1\n1000000000\n", Origin: TestCaseOriginCustom}, {Input: raw, Origin: TestCaseOriginCustom}}}
	value, err := env.ExecuteActivity(acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: fixture.input.BundleArtifact, ExpectedAuthoringBundleSHA: fixture.input.ExpectedBundleSHA256, TestData: data})
	require.NoError(t, err)
	var materialized MaterializeS3CasePlanResultV1
	require.NoError(t, value.Get(&materialized))
	casePlan, _, err := acts.loadS3CasePlanV1(context.Background(), *materialized.CasePlanArtifact)
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), casePlan.Cases[2].InputArtifact.SizeBytes)
	require.Equal(t, speccontract.TestCaseInputParserRuleVersionV2, casePlan.InputParserRuleVersion)
	stored, err := fixture.store.Get(context.Background(), casePlan.Cases[2].InputArtifact)
	require.NoError(t, err)
	require.Equal(t, raw, string(stored))
	inputs[2].Input = raw
	inputRef := manifestV2FixtureArtifact(raw, "text/plain")
	manifest.Cases[2].InputArtifact, manifest.Cases[2].InputSHA256 = &inputRef, inputRef.SHA256
	coverage, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.NoError(t, err)
	require.Equal(t, FullCaseIntegerBoundaryCoverageRuleVersionV2, coverage.RuleVersion)
	require.Equal(t, speccontract.TestCaseInputParserRuleVersionV2, coverage.InputParserRuleVersion)
	_, err = ValidateIntegerBoundaryCoverageV1(spec, plan, manifest, inputs)
	require.ErrorContains(t, err, "exceeds 1048576 bytes")
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(*coverage, boundaryCoverageReceiptRuleForSpecV1(spec, true))
	require.NoError(t, err)
	coverage.InputParserRuleVersion = speccontract.SampleInputParserRuleVersionV1
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(*coverage, boundaryCoverageReceiptRuleForSpecV1(spec, true))
	require.Error(t, err)
}

func TestTokenStringInputV2PreservesLegacyIntegerReceiptBytesAndRules(t *testing.T) {
	spec, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	parsed, err := speccontract.ParseSampleInputV1(spec, "1\n0\n")
	require.NoError(t, err)
	require.Equal(t, speccontract.SampleInputParserRuleVersionV1, parsed.ParserRuleVersion)
	const expectedBindings = `[{"symbol":"n","kind":"integer","integer":1},{"symbol":"a","kind":"integer_sequence","integers":[0]}]`
	encoded, err := json.Marshal(parsed.Bindings)
	require.NoError(t, err)
	require.Equal(t, expectedBindings, string(encoded))
	require.Equal(t, sha256Hex([]byte(expectedBindings)), parsed.BindingSHA256)
	coverage, err := ValidateIntegerBoundaryCoverageV1(spec, plan, manifest, inputs)
	require.NoError(t, err)
	require.Equal(t, IntegerBoundaryCoverageRuleVersion, coverage.RuleVersion)
	encoded, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(*coverage)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "input_parser_rule_version")
	legacy := S3CasePlanBundleV1{}
	fullCase, err := validateCasePlanInputParserRuleV2(legacy, spec)
	require.NoError(t, err)
	require.False(t, fullCase)
	_, err = validateCasePlanInputParserRuleV2(legacy, tokenStringSpecV2(domain.SemanticSymbolString))
	require.Error(t, err, "new strings cannot claim a legacy integer-only plan")
}
