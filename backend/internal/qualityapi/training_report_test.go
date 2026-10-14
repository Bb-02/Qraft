package qualityapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQualityReportTrainingAllowsOnlyOptionalHiddenAndIntegerTarget(t *testing.T) {
	report := canonicalReportFixture(t)
	report.Policy = "problem_set_training"
	report.Difficulty.GenerationTarget = 425
	report.Gates[8].Status = "not_configured"
	report.Warnings = []QualityFindingV1{{Source: "reviewer", Gate: "reviewer_schema_verdict", Code: "review.difficulty_mismatch"}}
	_, _, err := CanonicalProblemQualityReportV1(report)
	require.NoError(t, err)
	for _, mutate := range []func(*ProblemQualityReportV1){
		func(r *ProblemQualityReportV1) { r.Policy = "" },
		func(r *ProblemQualityReportV1) { r.Gates[3].Status = "not_configured" },
		func(r *ProblemQualityReportV1) { r.Gates[8].Status = "check_failed" },
		func(r *ProblemQualityReportV1) { r.Policy = "unknown" },
	} {
		copy := report
		copy.Gates = append([]QualityGateV1(nil), report.Gates...)
		mutate(&copy)
		_, _, err := CanonicalProblemQualityReportV1(copy)
		require.Error(t, err)
	}
}

func TestQualityReportHumanPublicationKeepsAutomaticQuarantine(t *testing.T) {
	report := canonicalReportFixture(t)
	report.Decision = "quarantine"
	report.HumanApproved = true
	report.Gates[8].Status = "check_failed"
	report.BlockingIssues = []QualityFindingV1{{Source: "deterministic", Gate: "hidden_regression", Code: "hidden_regression.check_failed"}}
	report.Layers[0].Status = LayerStatusQuarantined
	report.Layers[2].Status = LayerStatusReady
	report.Layers[2].Basis = "current_human_approval_and_assets_revalidated"
	_, _, err := CanonicalProblemQualityReportV1(report)
	require.NoError(t, err)
	for _, mutate := range []func(*ProblemQualityReportV1){
		func(r *ProblemQualityReportV1) { r.HumanApproved = false },
		func(r *ProblemQualityReportV1) { r.Layers[0].Status = LayerStatusPass },
		func(r *ProblemQualityReportV1) { r.BlockingIssues = nil },
		func(r *ProblemQualityReportV1) { r.Decision = "pass" },
		func(r *ProblemQualityReportV1) { r.Layers[2].Basis = "automatic pass" },
	} {
		copy := report
		copy.Layers = append([]QualityLayerV1(nil), report.Layers...)
		mutate(&copy)
		_, _, err := CanonicalProblemQualityReportV1(copy)
		require.Error(t, err)
	}
}
