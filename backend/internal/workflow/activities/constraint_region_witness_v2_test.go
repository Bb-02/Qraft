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

func multiRegionCoverageFixtureV2(t *testing.T) (domain.SemanticSpecV1, domain.AuthoringPlanV1, TestManifestV2, []IntegerBoundaryCoverageCaseInputV1) {
	t.Helper()
	spec, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{
		Purpose: "overlapping maximum region", ConstraintRegion: "region-max-elements",
		BoundaryRefs: []string{"a-element-max", "a-length-max"},
	})
	manifest.AuthoringPlanSHA256, _ = canonicalJSONSHA256(plan)
	manifest.ConstraintRegionRuleVersion = ConstraintRegionMatchingRuleVersionV2
	manifest.BoundaryCoverageReceiptSHA256 = ""
	return spec, plan, manifest, inputs
}

func completeMultiRegionManifestV2(t *testing.T, spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1, manifest TestManifestV2, inputs []IntegerBoundaryCoverageCaseInputV1) (TestManifestV2, IntegerBoundaryCoverageReceiptV1) {
	t.Helper()
	coverage, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.NoError(t, err)
	rule, err := boundaryCoverageReceiptRuleForManifestV2(spec, true, manifest.ConstraintRegionRuleVersion)
	require.NoError(t, err)
	_, digest, err := CanonicalIntegerBoundaryCoverageReceiptV1(*coverage, rule)
	require.NoError(t, err)
	manifest.BoundaryCoverageReceiptSHA256 = digest
	manifest.RegionWitnesses = coverage.RegionWitnesses
	require.NoError(t, manifest.Validate())
	return manifest, *coverage
}

func TestMultiRegionCoverageV2SharesOneRealCaseWithoutChangingCountOrPrimaryGroups(t *testing.T) {
	spec, plan, manifest, inputs := multiRegionCoverageFixtureV2(t)
	legacy := manifest
	legacy.ConstraintRegionRuleVersion = ""
	_, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, legacy, inputs)
	require.ErrorContains(t, err, "has no single matching case", "legacy completed evidence retains its original exclusive-label semantics")
	final, coverage := completeMultiRegionManifestV2(t, spec, plan, manifest, inputs)
	require.Equal(t, 3, final.TestCount)
	require.Equal(t, manifest.Cases, final.Cases)
	require.Equal(t, FullCaseIntegerMultiRegionCoverageRuleVersionV3, coverage.RuleVersion)
	require.Len(t, coverage.RegionWitnesses, len(plan.TestIntents))
	maxWitnesses := 0
	for _, witness := range coverage.RegionWitnesses {
		if witness.ConstraintRegion == "region-max" || witness.ConstraintRegion == "region-max-elements" {
			maxWitnesses++
			require.Equal(t, "case-c-max", witness.TestID)
			require.Equal(t, manifest.Cases[2].InputSHA256, witness.InputSHA256)
		}
	}
	require.Equal(t, 2, maxWitnesses, "the unique maximum case proves both overlapping obligations")
	encoded, _, err := CanonicalTestManifestV2JSON(final)
	require.NoError(t, err)
	parsed, err := ParseTestManifestV2JSON(encoded)
	require.NoError(t, err)
	require.Equal(t, final, *parsed, "the reader used by Store and export retains the same evidence")
	_, err = ValidateTestCaseBoundaryCoverageV2(spec, plan, final, inputs)
	require.NoError(t, err)
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(coverage)
	require.Error(t, err, "a v3 self-declared receipt cannot widen the default legacy reader")
}

func TestMultiRegionCoverageV2RejectsCrossCaseUnionsFalseHitsAndDifferentRawBytes(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*domain.AuthoringPlanV1, *TestManifestV2, *[]IntegerBoundaryCoverageCaseInputV1)
	}{
		{"cross-case union", "has no single matching case", func(plan *domain.AuthoringPlanV1, _ *TestManifestV2, _ *[]IntegerBoundaryCoverageCaseInputV1) {
			plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "incompatible same-case obligation", ConstraintRegion: "empty-and-maximum", BoundaryRefs: []string{"a-length-zero", "n-max"}})
		}},
		{"false hit", "but its input does not hit it", func(_ *domain.AuthoringPlanV1, manifest *TestManifestV2, _ *[]IntegerBoundaryCoverageCaseInputV1) {
			manifest.Cases[0].BoundaryRefs = []string{"a-element-max", "a-length-zero", "n-zero"}
		}},
		{"different bytes", "resolved bytes do not match input artifact", func(_ *domain.AuthoringPlanV1, _ *TestManifestV2, inputs *[]IntegerBoundaryCoverageCaseInputV1) {
			(*inputs)[2].Input = "2\n0 8\n"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec, plan, manifest, inputs := multiRegionCoverageFixtureV2(t)
			test.mutate(&plan, &manifest, &inputs)
			manifest.AuthoringPlanSHA256, _ = canonicalJSONSHA256(plan)
			_, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
			require.ErrorContains(t, err, test.want)
			if test.name == "cross-case union" {
				var missing *ConstraintRegionCoverageErrorV2
				require.ErrorAs(t, err, &missing)
				require.Equal(t, "empty-and-maximum", missing.ConstraintRegion)
			}
		})
	}
}

func TestMultiRegionManifestAndReceiptReadersRejectWrongRulesAndForgedWitnesses(t *testing.T) {
	spec, plan, base, inputs := multiRegionCoverageFixtureV2(t)
	manifest, coverage := completeMultiRegionManifestV2(t, spec, plan, base, inputs)
	for _, test := range []struct {
		name   string
		mutate func(*TestManifestV2)
	}{
		{"unknown rule", func(m *TestManifestV2) { m.ConstraintRegionRuleVersion = "untrusted" }},
		{"downgraded rule", func(m *TestManifestV2) { m.ConstraintRegionRuleVersion = "" }},
		{"missing proof", func(m *TestManifestV2) { m.RegionWitnesses = nil }},
		{"unknown case", func(m *TestManifestV2) { m.RegionWitnesses[0].TestID = "case-does-not-exist" }},
		{"different input hash", func(m *TestManifestV2) { m.RegionWitnesses[0].InputSHA256 = strings.Repeat("f", 64) }},
		{"cross-case refs", func(m *TestManifestV2) {
			m.RegionWitnesses[0].BoundaryRefs = []string{"a-length-zero", "n-max"}
		}},
		{"duplicate proof", func(m *TestManifestV2) { m.RegionWitnesses = append(m.RegionWitnesses, m.RegionWitnesses[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := cloneTestManifestV2(t, manifest)
			test.mutate(&copy)
			encoded, err := json.Marshal(copy)
			require.NoError(t, err)
			_, err = ParseTestManifestV2JSON(encoded)
			require.Error(t, err)
		})
	}
	for _, mutate := range []func(*IntegerBoundaryCoverageReceiptV1){
		func(r *IntegerBoundaryCoverageReceiptV1) { r.ConstraintRegionRuleVersion = "" },
		func(r *IntegerBoundaryCoverageReceiptV1) {
			r.InputParserRuleVersion = speccontract.SampleInputParserRuleVersionV1
		},
		func(r *IntegerBoundaryCoverageReceiptV1) { r.RegionWitnesses = nil },
		func(r *IntegerBoundaryCoverageReceiptV1) { r.CoveredRegions = []string{"fabricated"} },
		func(r *IntegerBoundaryCoverageReceiptV1) { r.CoveredBoundaryRefs = []string{} },
	} {
		copy := coverage
		mutate(&copy)
		_, _, err := CanonicalIntegerBoundaryCoverageReceiptV1(copy, FullCaseIntegerMultiRegionCoverageRuleVersionV3)
		require.Error(t, err)
	}
	// Even a structurally plausible witness must be recomputed from the
	// current typed plan and actual inputs at the coverage boundary.
	copy := cloneTestManifestV2(t, manifest)
	copy.RegionWitnesses[0].ConstraintRegion = "invented-region"
	_, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, copy, inputs)
	require.ErrorContains(t, err, "do not match actual input evidence")
	_, err = ValidateIntegerBoundaryCoverageV1(spec, plan, base, inputs)
	require.ErrorContains(t, err, "full-case input phase")
}

func TestS3MultiRegionMaterializerOracleManifestAndStoreKeepAllTwelveCases(t *testing.T) {
	f := newS3BatchGateFixtureV1(t, func(authoring *validatedSampleCandidatesFixture) {
		plan := authoring.bundle.AuthoringPlan
		plan.TestIntents = append([]domain.AuthoringTestIntentV1{{Purpose: "general input", ConstraintRegion: "general", BoundaryRefs: []string{}}}, plan.TestIntents...)
		plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "overlapping maximum input", ConstraintRegion: "maximum-again", BoundaryRefs: []string{"a-3", "n-3"}})
		lint := speccontract.LintV1(speccontract.LintInputV1{SemanticSpec: *authoring.bundle.SemanticSpec, AuthoringPlan: *plan, ExpectedBriefSHA256: authoring.bundle.BriefSHA256,
			ExpectedDifficulty: authoring.input.ExpectedDifficulty, RequiredKnowledgePoints: authoring.input.RequiredKnowledgePoints})
		require.True(t, lint.Passed, "%+v", lint.Issues)
		authoring.bundle.LintReport = &lint
	})
	plan, _, err := f.acts.loadS3CasePlanV1(context.Background(), f.oracleInput.CasePlanArtifact)
	require.NoError(t, err)
	require.Equal(t, ConstraintRegionMatchingRuleVersionV2, plan.ConstraintRegionRuleVersion)
	for _, item := range plan.Cases {
		require.Equal(t, "general", item.ConstraintRegion, "primary scoring group remains unchanged")
	}
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusPass, verified.Gate.Status)
	require.Len(t, verified.MainRun.Batches, 2)
	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp",
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact,
		ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	result := executeS3BatchTestActivityV1[S3ManifestGateResultV1](t, f.acts.BuildS3TestManifestActivityV1, S3ManifestGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef,
		CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun, OracleReceiptArtifact: *verified.ReceiptArtifact,
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SanitizerReceiptArtifact: *sanitized.ReceiptArtifact})
	require.Equal(t, qualitygate.GateStatusPass, result.BoundaryCoverageGate.Status)
	raw, err := f.store.Get(context.Background(), *result.TestManifestArtifact)
	require.NoError(t, err)
	manifest, err := ParseTestManifestV2JSON(raw)
	require.NoError(t, err)
	require.Equal(t, 12, manifest.TestCount)
	require.Len(t, manifest.RegionWitnesses, 6)
	storedCases, _ := s3QualityDraftTestCasesV1(manifest)
	require.Len(t, storedCases, 12)
	for i, item := range manifest.Cases {
		require.Equal(t, plan.Cases[i].InputArtifact, *storedCases[i].InputArtifact)
		require.Equal(t, "general", item.ConstraintRegion)
	}
	raw, err = f.store.Get(context.Background(), *result.BoundaryReceiptArtifact)
	require.NoError(t, err)
	var coverage IntegerBoundaryCoverageReceiptV1
	require.NoError(t, json.Unmarshal(raw, &coverage))
	require.Equal(t, FullCaseTokenStringMultiRegionCoverageRuleVersionV3, coverage.RuleVersion)
	require.Equal(t, manifest.RegionWitnesses, coverage.RegionWitnesses)
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(coverage, FullCaseTokenStringMultiRegionCoverageRuleVersionV3)
	require.NoError(t, err)
}
