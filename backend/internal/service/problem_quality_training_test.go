package service

import (
	"context"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualityapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/stretchr/testify/require"
)

func TestProblemQualityTrainingReportRetainsRealWarningsAndOptionalHidden(t *testing.T) {
	f := trainingS3ExportFixture(t)
	source := &qg15AHydroProblemSource{problem: f.request.Problem, testCases: f.request.TestCases}
	quality := NewProblemQualityService(source, f.reader)
	report, err := quality.GetProblemQuality(context.Background(), source.problem.ID)
	require.NoError(t, err)
	require.Equal(t, qualitygate.PolicyProblemSetTraining, report.Report.Policy)
	require.False(t, report.Report.HumanApproved)
	require.Equal(t, qualitygate.DecisionPass, report.Report.Decision)
	require.Equal(t, qualitygate.GateStatusNotConfigured, report.Report.Gates[8].Status)
	require.Equal(t, "canonical_problem_set_training_policy_pass", report.Report.Layers[0].Basis)
	require.Equal(t, "review.score_below_threshold.difficulty_calibration", report.Report.Warnings[0].Code)
	audit, err := quality.GetProblemQualityAudit(context.Background(), source.problem.ID)
	require.NoError(t, err)
	require.Equal(t, f.reader.data[f.evidence.AuditArtifact.Key], audit.Bytes)
}

func TestProblemQualityHumanReportKeepsFailureAndSeparatePublicationBasis(t *testing.T) {
	f := manualS3ExportFixture(t)
	p := f.request.Problem
	source := &manualS3ProblemSource{qg15AHydroProblemSource: qg15AHydroProblemSource{problem: p, testCases: f.request.TestCases}, approved: true,
		solutions: []*domain.Solution{{ProblemID: p.ID, SolutionType: domain.SolutionTypeMain, SourceCode: "main source"}, {ProblemID: p.ID, SolutionType: domain.SolutionTypeBrute, SourceCode: "oracle source"}}}
	quality := NewProblemQualityService(source, f.reader)
	report, err := quality.GetProblemQuality(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, qualitygate.DecisionQuarantine, report.Report.Decision)
	require.True(t, report.Report.HumanApproved)
	require.Equal(t, qualityapi.LayerStatusQuarantined, report.Report.Layers[0].Status)
	require.Equal(t, qualityapi.LayerStatusReady, report.Report.Layers[2].Status)
	require.Equal(t, "current_human_approval_and_assets_revalidated", report.Report.Layers[2].Basis)
	require.Equal(t, qualitygate.GateStatusCheckFailed, report.Report.Gates[8].Status)
	require.Equal(t, "hidden_regression.check_failed", report.Report.BlockingIssues[0].Code)
	audit, err := quality.GetProblemQualityAudit(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, f.reader.data[f.evidence.AuditArtifact.Key], audit.Bytes)
	source.approved = false
	_, err = quality.GetProblemQuality(context.Background(), p.ID)
	require.ErrorIs(t, err, ErrConflict)
}
