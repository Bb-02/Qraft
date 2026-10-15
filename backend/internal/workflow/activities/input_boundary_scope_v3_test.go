package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
)

func addOutputAndDerivedCoverageContractsV3(spec *domain.SemanticSpecV1, answerMin, answerMax int64) {
	zero, nine := int64(0), int64(9)
	answer := domain.SemanticSymbolV1{Name: "answer", Type: domain.SemanticSymbolString, Scope: domain.SemanticSymbolScopeOutput, Role: "answer text", Definition: "text emitted by the solution", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit}
	found := false
	for i := range spec.Symbols {
		if spec.Symbols[i].Name == answer.Name {
			spec.Symbols[i], found = answer, true
		}
	}
	if !found {
		spec.Symbols = append(spec.Symbols, answer)
	}
	spec.Symbols = append(spec.Symbols, domain.SemanticSymbolV1{Name: "derived_total", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeDerived, Role: "derived total", Definition: "a value derived from the input", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit})
	if spec.Sections.Constraints != nil {
		spec.Sections.Constraints.SymbolRefs = append(spec.Sections.Constraints.SymbolRefs, "answer", "derived_total")
	}
	spec.Constraints = append(spec.Constraints,
		domain.SemanticConstraintV1{Subject: "answer", Kind: domain.SemanticConstraintLengthRange, Min: &answerMin, Max: &answerMax, Meaning: "output text length"},
		domain.SemanticConstraintV1{Subject: "derived_total", Kind: domain.SemanticConstraintIntegerRange, Min: &zero, Max: &nine, Meaning: "derived value range"},
	)
	spec.Boundaries = append(spec.Boundaries,
		domain.SemanticBoundaryV1{ID: "answer-length-min", Symbol: "answer", Measure: domain.SemanticBoundaryMeasureLength, Value: answerMin, Allowed: true, Meaning: "output text boundary"},
		domain.SemanticBoundaryV1{ID: "derived-total-nine", Symbol: "derived_total", Measure: domain.SemanticBoundaryMeasureValue, Value: nine, Allowed: true, Meaning: "derived value boundary"},
	)
}

func inputScopeCoverageFixtureV3(t *testing.T) (domain.SemanticSpecV1, domain.AuthoringPlanV1, TestManifestV2, []IntegerBoundaryCoverageCaseInputV1) {
	t.Helper()
	spec, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	addOutputAndDerivedCoverageContractsV3(&spec, 2, 3)
	plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(spec)
	manifest.SemanticSpecSHA256 = plan.SemanticSpecSHA256
	manifest.AuthoringPlanSHA256, _ = canonicalJSONSHA256(plan)
	manifest.ConstraintRegionRuleVersion = ConstraintRegionMatchingRuleVersionV3
	manifest.BoundaryCoverageReceiptSHA256 = ""
	return spec, plan, manifest, inputs
}

func TestInputBoundaryScopeV3DerivesOnlyBoundInputsWithoutChangingFullContracts(t *testing.T) {
	spec, plan, manifest, inputs := inputScopeCoverageFixtureV3(t)
	beforeSpec, err := json.Marshal(spec)
	require.NoError(t, err)
	beforePlan, err := json.Marshal(plan)
	require.NoError(t, err)
	derived, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	require.NoError(t, err)
	require.Equal(t, IntegerInputBoundaryCasePlanRuleVersionV3, derived.RuleVersion)
	require.Equal(t, manifest.SemanticSpecSHA256, derived.SemanticSpecSHA256)
	require.Equal(t, manifest.AuthoringPlanSHA256, derived.AuthoringPlanSHA256)
	for _, obligation := range derived.Obligations {
		require.NotEqual(t, "answer", obligation.Subject)
		require.NotEqual(t, "derived_total", obligation.Subject)
	}
	_, _, err = CanonicalIntegerBoundaryCasePlanV1(*derived)
	require.Error(t, err, "the legacy default reader must not accept a self-declared new scope rule")
	_, _, err = CanonicalIntegerBoundaryCasePlanV1(*derived, inputBoundaryCasePlanRuleForSpecV3(spec))
	require.NoError(t, err)
	final, coverage := completeMultiRegionManifestV2(t, spec, plan, manifest, inputs)
	require.Equal(t, FullCaseIntegerInputScopeCoverageRuleVersionV4, coverage.RuleVersion)
	for _, edge := range coverage.CoveredRangeEdges {
		require.False(t, strings.Contains(edge, "answer") || strings.Contains(edge, "derived_total"))
	}
	require.Equal(t, manifest.SemanticSpecSHA256, final.SemanticSpecSHA256)
	afterSpec, err := json.Marshal(spec)
	require.NoError(t, err)
	afterPlan, err := json.Marshal(plan)
	require.NoError(t, err)
	require.Equal(t, string(beforeSpec), string(afterSpec), "projection cannot mutate the full spec or its shared slices")
	require.Equal(t, string(beforePlan), string(afterPlan), "projection cannot mutate the full authoring plan")
	legacy := manifest
	legacy.ConstraintRegionRuleVersion = ConstraintRegionMatchingRuleVersionV2
	_, err = ValidateTestCaseBoundaryCoverageV2(spec, plan, legacy, inputs)
	require.ErrorContains(t, err, "is not covered", "completed v2 evidence retains its original derivation")
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(coverage, FullCaseIntegerMultiRegionCoverageRuleVersionV3)
	require.Error(t, err)
	coverage.ConstraintRegionRuleVersion = ConstraintRegionMatchingRuleVersionV2
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(coverage, FullCaseIntegerInputScopeCoverageRuleVersionV4)
	require.Error(t, err, "new receipt identity cannot be selected by relabeling an old header")
}

func TestInputBoundaryScopeV3FailsClosedOnInvalidScopeAndNonInputIntentRefs(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*domain.SemanticSpecV1, *domain.AuthoringPlanV1)
	}{
		{"empty scope", "unknown or empty scope", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) { spec.Symbols[0].Scope = "" }},
		{"unknown scope", "unknown or empty scope", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) { spec.Symbols[2].Scope = "unknown" }},
		{"unbound input", "absent from the input grammar", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.Symbols = append(spec.Symbols, domain.SemanticSymbolV1{Name: "extra", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput})
		}},
		{"unknown constraint", "undefined symbol", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) { spec.Constraints[0].Subject = "unknown" }},
		{"unknown boundary", "undefined symbol", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) { spec.Boundaries[0].Symbol = "unknown" }},
		{"noninput grammar field", "not input-scoped", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.InputGrammar.Lines[0].Fields[0].Symbol = "answer"
		}},
		{"output intent ref", "cannot prove test intent boundary", func(_ *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			plan.TestIntents[0].BoundaryRefs = []string{"answer-length-min"}
		}},
		{"derived intent ref", "cannot prove test intent boundary", func(_ *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			plan.TestIntents[0].BoundaryRefs = []string{"derived-total-nine"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec, plan, _, _ := inputScopeCoverageFixtureV3(t)
			test.mutate(&spec, &plan)
			plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(spec)
			_, err := DeriveInputBoundaryCasePlanV3(spec, plan)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestInputBoundaryScopeV3DoesNotRequireOutputTypesToHaveAnInputParser(t *testing.T) {
	spec, plan, manifest, inputs := inputScopeCoverageFixtureV3(t)
	for i := range spec.Symbols {
		if spec.Symbols[i].Name == "answer" {
			spec.Symbols[i].Type = domain.SemanticSymbolSet
		}
	}
	for i := range spec.Constraints {
		if spec.Constraints[i].Subject == "answer" {
			spec.Constraints[i].Kind = domain.SemanticConstraintCardinality
		}
	}
	for i := range spec.Boundaries {
		if spec.Boundaries[i].Symbol == "answer" {
			spec.Boundaries[i].Measure = domain.SemanticBoundaryMeasureCardinality
		}
	}
	plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(spec)
	manifest.SemanticSpecSHA256, manifest.AuthoringPlanSHA256 = plan.SemanticSpecSHA256, ""
	manifest.AuthoringPlanSHA256, _ = canonicalJSONSHA256(plan)
	_, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	require.NoError(t, err)
	_, err = ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.NoError(t, err, "valid output-only set/cardinality representation must not become an unsupported input requirement")
}

func TestInputBoundaryScopeV3KeepsInputRangesZeroRelationsAndJointCoverageStrict(t *testing.T) {
	spec, plan, manifest, inputs := inputScopeCoverageFixtureV3(t)
	for _, test := range []struct{ name, raw string }{
		{"outside input range", "1\n10\n"},
		{"input relation or grammar mismatch", "2\n0\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := append([]IntegerBoundaryCoverageCaseInputV1(nil), inputs...)
			changed[1].Input = test.raw
			copy := cloneTestManifestV2(t, manifest)
			ref := manifestV2FixtureArtifact(test.raw, "text/plain")
			copy.Cases[1].InputArtifact, copy.Cases[1].InputSHA256 = &ref, ref.SHA256
			_, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, copy, changed)
			require.Error(t, err)
		})
	}
	copy := cloneTestManifestV2(t, manifest)
	copy.Cases, copy.TestCount = copy.Cases[1:], 2
	copy.Cases[0].TestIndex, copy.Cases[1].TestIndex = 0, 1
	_, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, copy, inputs[1:])
	require.ErrorContains(t, err, "is not covered", "input zero/minimum boundary remains required")
	plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "same-case obligation", ConstraintRegion: "empty-and-max", BoundaryRefs: []string{"a-length-zero", "n-max"}})
	manifest.AuthoringPlanSHA256, _ = canonicalJSONSHA256(plan)
	_, err = ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.ErrorContains(t, err, "has no single matching case")
}

func TestS3InputScopeMaterializerOracleManifestAndStoreKeepFullSpecAndTwelveCases(t *testing.T) {
	f := newS3BatchGateFixtureV1(t, func(authoring *validatedSampleCandidatesFixture) {
		addOutputAndDerivedCoverageContractsV3(authoring.bundle.SemanticSpec, 1, 2)
		sha := speccontract.SemanticSpecSHA256V1(*authoring.bundle.SemanticSpec)
		authoring.bundle.AuthoringPlan.SemanticSpecSHA256 = sha
		authoring.statementDraftFixture.input.ExpectedSemanticSpecSHA256, authoring.input.ExpectedSemanticSpecSHA256 = sha, sha
		lint := speccontract.LintV1(speccontract.LintInputV1{SemanticSpec: *authoring.bundle.SemanticSpec, AuthoringPlan: *authoring.bundle.AuthoringPlan, ExpectedBriefSHA256: authoring.bundle.BriefSHA256, ExpectedDifficulty: authoring.input.ExpectedDifficulty, RequiredKnowledgePoints: authoring.input.RequiredKnowledgePoints})
		require.True(t, lint.Passed, "%+v", lint.Issues)
		authoring.bundle.LintReport = &lint
	})
	fullSpecBefore, err := f.store.Get(context.Background(), f.semanticRef)
	require.NoError(t, err)
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusPass, verified.Gate.Status)
	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp", VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact, ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	result := executeS3BatchTestActivityV1[S3ManifestGateResultV1](t, f.acts.BuildS3TestManifestActivityV1, S3ManifestGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef, CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun, OracleReceiptArtifact: *verified.ReceiptArtifact, VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SanitizerReceiptArtifact: *sanitized.ReceiptArtifact})
	raw, err := f.store.Get(context.Background(), *result.TestManifestArtifact)
	require.NoError(t, err)
	manifest, err := ParseTestManifestV2JSON(raw)
	require.NoError(t, err)
	require.Equal(t, ConstraintRegionMatchingRuleVersionV3, manifest.ConstraintRegionRuleVersion)
	require.Equal(t, f.semanticRef.SHA256, manifest.SemanticSpecSHA256)
	stored, _ := s3QualityDraftTestCasesV1(manifest)
	require.Len(t, stored, 12)
	raw, err = f.store.Get(context.Background(), *result.BoundaryReceiptArtifact)
	require.NoError(t, err)
	var receipt IntegerBoundaryCoverageReceiptV1
	require.NoError(t, json.Unmarshal(raw, &receipt))
	require.Equal(t, FullCaseTokenStringInputScopeCoverageRuleVersionV4, receipt.RuleVersion)
	for _, edge := range receipt.CoveredRangeEdges {
		require.NotContains(t, edge, "answer")
		require.NotContains(t, edge, "derived_total")
	}
	fullSpecAfter, err := f.store.Get(context.Background(), f.semanticRef)
	require.NoError(t, err)
	require.Equal(t, string(fullSpecBefore), string(fullSpecAfter))
	require.Contains(t, string(fullSpecAfter), `"subject":"answer"`)
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(receipt, FullCaseTokenStringInputScopeCoverageRuleVersionV4)
	require.NoError(t, err)
}
