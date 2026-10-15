package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func newS3ManifestInteriorCaseFixtureV1(t *testing.T) (*s3BatchGateFixtureV1, S3ManifestGateInputV1) {
	t.Helper()
	f := newS3BatchGateFixtureV1(t, func(authoring *validatedSampleCandidatesFixture) {
		// Count two is an interior value, not a declared boundary. Keep the
		// minimum, unit, and maximum obligations and their real input cases.
		spec := authoring.bundle.SemanticSpec
		boundaries := make([]domain.SemanticBoundaryV1, 0, len(spec.Boundaries))
		for _, boundary := range spec.Boundaries {
			if boundary.Value != 2 {
				boundaries = append(boundaries, boundary)
			}
		}
		spec.Boundaries = boundaries
		plan := authoring.bundle.AuthoringPlan
		intents := make([]domain.AuthoringTestIntentV1, 0, len(plan.TestIntents))
		for _, intent := range plan.TestIntents {
			if intent.ConstraintRegion != "n=2" {
				intents = append(intents, intent)
			}
		}
		plan.TestIntents = intents
		semanticSHA := speccontract.SemanticSpecSHA256V1(*spec)
		plan.SemanticSpecSHA256 = semanticSHA
		lint := speccontract.LintV1(speccontract.LintInputV1{SemanticSpec: *spec, AuthoringPlan: *plan,
			ExpectedBriefSHA256: authoring.bundle.BriefSHA256, ExpectedDifficulty: authoring.input.ExpectedDifficulty,
			RequiredKnowledgePoints: authoring.input.RequiredKnowledgePoints})
		require.True(t, lint.Passed, "%+v", lint.Issues)
		authoring.bundle.LintReport = &lint
		authoring.statementDraftFixture.input.ExpectedSemanticSpecSHA256 = semanticSHA
		authoring.input.ExpectedSemanticSpecSHA256 = semanticSHA
	})
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusPass, verified.Gate.Status)
	require.Len(t, verified.MainRun.Batches, 2)
	require.Equal(t, 8, verified.MainRun.Batches[0].CaseCount)
	require.Equal(t, 4, verified.MainRun.Batches[1].CaseCount)
	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp",
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact,
		ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	require.Equal(t, qualitygate.GateStatusPass, sanitized.Gate.Status)
	return f, S3ManifestGateInputV1{PayloadVersion: S3QualityPayloadVersionV1,
		SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef,
		CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun,
		OracleReceiptArtifact: *verified.ReceiptArtifact, VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact,
		SanitizerReceiptArtifact: *sanitized.ReceiptArtifact}
}

func TestS3ManifestPreservesPresentEmptyBoundaryRefsThroughCoverageAndCAS(t *testing.T) {
	f, input := newS3ManifestInteriorCaseFixtureV1(t)
	plan, _, err := f.acts.loadS3CasePlanV1(context.Background(), input.CasePlanArtifact)
	require.NoError(t, err)
	result := executeS3BatchTestActivityV1[S3ManifestGateResultV1](t, f.acts.BuildS3TestManifestActivityV1, input)
	require.Equal(t, qualitygate.GateStatusPass, result.TestManifestGate.Status)
	require.Equal(t, qualitygate.GateStatusPass, result.BoundaryCoverageGate.Status)
	raw, err := f.store.Get(context.Background(), *result.TestManifestArtifact)
	require.NoError(t, err)
	manifest, err := ParseTestManifestV2JSON(raw)
	require.NoError(t, err)
	require.Equal(t, 12, manifest.TestCount)
	emptyCount := 0
	for i, item := range manifest.Cases {
		require.NotNil(t, item.BoundaryRefs, item.TestID)
		require.Equal(t, plan.Cases[i].BoundaryRefs, item.BoundaryRefs, "must preserve exactly the actual derived boundary hits")
		if i%4 == 2 {
			require.Empty(t, item.BoundaryRefs)
			emptyCount++
		} else {
			require.NotEmpty(t, item.BoundaryRefs)
		}
	}
	require.Equal(t, 3, emptyCount, "both sample and nonsample interior cases must remain valid")
	require.Equal(t, emptyCount, bytes.Count(raw, []byte(`"boundary_refs":[]`)))
	require.NotContains(t, string(raw), `"boundary_refs":null`)
	raw, err = f.store.Get(context.Background(), *result.BoundaryReceiptArtifact)
	require.NoError(t, err)
	var coverage IntegerBoundaryCoverageReceiptV1
	require.NoError(t, json.Unmarshal(raw, &coverage))
	require.Equal(t, []string{"a-0", "a-1", "a-3", "n-0", "n-1", "n-3"}, coverage.CoveredBoundaryRefs)
}

func TestS3ManifestStillRejectsInvalidBoundaryRefsAndMissingRealCoverage(t *testing.T) {
	f, input := newS3ManifestInteriorCaseFixtureV1(t)
	planBytes, err := f.store.Get(context.Background(), input.CasePlanArtifact)
	require.NoError(t, err)
	for _, test := range []struct {
		name, wantError string
		mutate          func(*S3CasePlanBundleV1)
	}{
		{"absent", "not canonical or complete", func(plan *S3CasePlanBundleV1) { plan.Cases[2].BoundaryRefs = nil }},
		{"duplicate", "invalid derived metadata", func(plan *S3CasePlanBundleV1) { plan.Cases[0].BoundaryRefs = []string{"a-0", "a-0"} }},
		{"unsorted", "invalid derived metadata", func(plan *S3CasePlanBundleV1) { plan.Cases[0].BoundaryRefs = []string{"n-0", "a-0"} }},
		{"false hit", "but its input does not hit it", func(plan *S3CasePlanBundleV1) { plan.Cases[2].BoundaryRefs = []string{"a-3"} }},
		{"missing required coverage", "is not covered", func(plan *S3CasePlanBundleV1) {
			for i := range plan.Cases {
				refs := make([]string, 0, len(plan.Cases[i].BoundaryRefs))
				for _, ref := range plan.Cases[i].BoundaryRefs {
					if ref != "a-3" {
						refs = append(refs, ref)
					}
				}
				plan.Cases[i].BoundaryRefs = refs
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var plan S3CasePlanBundleV1
			require.NoError(t, json.Unmarshal(planBytes, &plan))
			test.mutate(&plan)
			encoded, err := json.Marshal(plan)
			require.NoError(t, err)
			tampered := input
			tampered.CasePlanArtifact = s3BatchSeedArtifactV1(t, f.store, encoded, "application/json", "MaterializeS3CasePlanActivityV1")
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(f.acts.BuildS3TestManifestActivityV1)
			_, err = env.ExecuteActivity(f.acts.BuildS3TestManifestActivityV1, tampered)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}
