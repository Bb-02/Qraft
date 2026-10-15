package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/stretchr/testify/require"
)

func explicitRegionFixtureV4(t *testing.T) (domain.SemanticSpecV1, domain.AuthoringPlanV1, TestManifestV2, []IntegerBoundaryCoverageCaseInputV1) {
	t.Helper()
	spec, plan, manifest, inputs := validIntegerCoverageFixtureV1(t)
	manifest.ConstraintRegionRuleVersion = ConstraintRegionAssociationRuleVersionV4
	manifest.BoundaryCoverageReceiptSHA256 = ""
	derived, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	require.NoError(t, err)
	for i := range manifest.Cases {
		for _, ob := range derived.Obligations {
			if ob.Kind == IntegerBoundaryObligationRegion && ob.ConstraintRegion == manifest.Cases[i].ConstraintRegion {
				manifest.Cases[i].RegionObligationIDs = append(manifest.Cases[i].RegionObligationIDs, ob.ID)
			}
		}
		manifest.Cases[i].ConstraintRegion = "unassigned"
	}
	return spec, plan, manifest, inputs
}

func TestExplicitRegionV4DoesNotInferNaturalPredicatesFromSharedBoundaryRefs(t *testing.T) {
	spec, plan, manifest, inputs := explicitRegionFixtureV4(t)
	plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "exact total", ConstraintRegion: "sum(a)=9", BoundaryRefs: []string{"a-element-max"}}, domain.AuthoringTestIntentV1{Purpose: "larger total", ConstraintRegion: "sum(a)>9", BoundaryRefs: []string{"a-element-max"}})
	manifest.AuthoringPlanSHA256, _ = canonicalJSONSHA256(plan)
	derived, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	require.NoError(t, err)
	ids := map[string]string{}
	for _, ob := range derived.Obligations {
		if ob.Kind == IntegerBoundaryObligationRegion {
			ids[ob.ConstraintRegion] = ob.ID
		}
	}
	manifest.Cases[2].RegionObligationIDs = append(manifest.Cases[2].RegionObligationIDs, ids["sum(a)=9"])
	sort.Strings(manifest.Cases[2].RegionObligationIDs)
	_, err = ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.ErrorContains(t, err, "has no explicitly associated matching case")
	require.Contains(t, err.Error(), ids["sum(a)>9"], "same maximum-element hit cannot donate a different natural region")
	// A second concrete case explicitly owns the different claim. The server
	// still proves only boundary prerequisites, not a general sum predicate.
	c := manifest.Cases[2]
	c.TestID = "case-d-larger"
	c.TestIndex = 3
	c.RegionObligationIDs = []string{ids["sum(a)>9"]}
	ref := manifestV2FixtureArtifact("2\n9 9\n", "text/plain")
	c.InputArtifact = &ref
	c.InputSHA256 = ref.SHA256
	manifest.Cases = append(manifest.Cases, c)
	manifest.TestCount = 4
	inputs = append(inputs, IntegerBoundaryCoverageCaseInputV1{TestID: c.TestID, Input: "2\n9 9\n"})
	r, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.NoError(t, err)
	require.Equal(t, RegionEvidenceExplicitAssociationV4, r.RegionEvidenceKind)
	require.Equal(t, FullCaseIntegerExplicitRegionCoverageRuleVersionV5, r.RuleVersion)
	for _, w := range r.RegionWitnesses {
		if w.ObligationID == ids["sum(a)=9"] {
			require.Equal(t, "case-c-max", w.TestID)
		}
		if w.ObligationID == ids["sum(a)>9"] {
			require.Equal(t, "case-d-larger", w.TestID)
		}
	}
	_, sha, err := CanonicalIntegerBoundaryCoverageReceiptV1(*r, r.RuleVersion)
	require.NoError(t, err)
	manifest.RegionWitnesses = r.RegionWitnesses
	manifest.BoundaryCoverageReceiptSHA256 = sha
	encoded, _, err := CanonicalTestManifestV2JSON(manifest)
	require.NoError(t, err)
	loaded, err := ParseTestManifestV2JSON(encoded)
	require.NoError(t, err)
	_, err = ValidateTestCaseBoundaryCoverageV2(spec, plan, *loaded, inputs)
	require.NoError(t, err)
	orphan := cloneTestManifestV2(t, *loaded)
	orphan.Cases[0].RegionObligationIDs = append(orphan.Cases[0].RegionObligationIDs, "constraint_region:"+strings.Repeat("f", 64))
	sort.Strings(orphan.Cases[0].RegionObligationIDs)
	_, _, err = CanonicalTestManifestV2JSON(orphan)
	require.ErrorContains(t, err, "absent from complete region witnesses")
	bad := *r
	bad.RegionEvidenceKind = "mechanically_proved_natural_predicate"
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(bad, r.RuleVersion)
	require.ErrorContains(t, err, "evidence kind")
}

func TestExplicitRegionV4RejectsWrongPrerequisitesAndSplicedAssociations(t *testing.T) {
	spec, plan, manifest, inputs := explicitRegionFixtureV4(t)
	manifest.Cases[0].RegionObligationIDs = append(manifest.Cases[0].RegionObligationIDs, manifest.Cases[2].RegionObligationIDs...)
	sort.Strings(manifest.Cases[0].RegionObligationIDs)
	_, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.ErrorContains(t, err, "actual input does not hit boundary")
	_, _, manifest, inputs = explicitRegionFixtureV4(t)
	r, err := ValidateTestCaseBoundaryCoverageV2(spec, plan, manifest, inputs)
	require.NoError(t, err)
	manifest.RegionWitnesses = r.RegionWitnesses
	manifest.BoundaryCoverageReceiptSHA256 = strings.Repeat("a", 64)
	manifest.Cases[2].RegionObligationIDs = nil
	require.ErrorContains(t, manifest.Validate(), "explicit association")
	legacy := *r
	legacy.RuleVersion = FullCaseIntegerInputScopeCoverageRuleVersionV4
	legacy.ConstraintRegionRuleVersion = ConstraintRegionMatchingRuleVersionV3
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(legacy, legacy.RuleVersion)
	require.ErrorContains(t, err, "evidence kind")
}

func TestExplicitRegionV4MaterializeVerifyAndManifestPreserveAllAssociations(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	raw, err := f.store.Get(context.Background(), f.authoringRef)
	require.NoError(t, err)
	bundle, err := decodeCanonicalAuthoringBundleV1(raw)
	require.NoError(t, err)
	_, contract, err := enrichS3TestDataParamsV6(domain.ProblemGenParams{}, *bundle.SemanticSpec, *bundle.AuthoringPlan)
	require.NoError(t, err)
	derived, err := DeriveInputBoundaryCasePlanV3(*bundle.SemanticSpec, *bundle.AuthoringPlan)
	require.NoError(t, err)
	old, _, err := f.acts.loadS3CasePlanV1(context.Background(), f.oracleInput.CasePlanArtifact)
	require.NoError(t, err)
	data := TestDataResult{PayloadVersion: ActivityPayloadVersion, S3Coverage: contract}
	for _, c := range old.Cases {
		b, err := f.store.Get(context.Background(), c.InputArtifact)
		require.NoError(t, err)
		var n int
		_, err = fmt.Sscan(string(b), &n)
		require.NoError(t, err)
		item := TestCaseData{InputArtifact: &c.InputArtifact, Origin: TestCaseOriginCustom}
		for index, ob := range derived.Obligations {
			if ob.Kind == IntegerBoundaryObligationRegion && ob.ConstraintRegion == fmt.Sprintf("n=%d", n) {
				item.RegionObligationIndexes = append(item.RegionObligationIndexes, index)
			}
		}
		data.TestCases = append(data.TestCases, item)
	}
	f.materialized = executeS3BatchTestActivityV1[MaterializeS3CasePlanResultV1](t, f.acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: f.authoringRef, ExpectedAuthoringBundleSHA: f.authoringRef.SHA256, TestData: data})
	f.oracleInput.CasePlanArtifact = *f.materialized.CasePlanArtifact
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp", VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact, ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	result := executeS3BatchTestActivityV1[S3ManifestGateResultV1](t, f.acts.BuildS3TestManifestActivityV1, S3ManifestGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef, CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun, OracleReceiptArtifact: *verified.ReceiptArtifact, VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SanitizerReceiptArtifact: *sanitized.ReceiptArtifact})
	require.Equal(t, qualitygate.GateStatusPass, result.BoundaryCoverageGate.Status)
	raw, err = f.store.Get(context.Background(), *result.TestManifestArtifact)
	require.NoError(t, err)
	manifest, err := ParseTestManifestV2JSON(raw)
	require.NoError(t, err)
	require.Equal(t, ConstraintRegionAssociationRuleVersionV4, manifest.ConstraintRegionRuleVersion)
	for _, c := range manifest.Cases {
		require.Equal(t, "unassigned", c.ConstraintRegion)
		require.Len(t, c.RegionObligationIDs, 1)
	}
	raw, err = f.store.Get(context.Background(), *result.BoundaryReceiptArtifact)
	require.NoError(t, err)
	var receipt IntegerBoundaryCoverageReceiptV1
	require.NoError(t, json.Unmarshal(raw, &receipt))
	require.Equal(t, RegionEvidenceExplicitAssociationV4, receipt.RegionEvidenceKind)
	_, _, err = CanonicalIntegerBoundaryCoverageReceiptV1(receipt, FullCaseTokenStringExplicitRegionCoverageRuleVersionV5)
	require.NoError(t, err)
}
