package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func manualS3ExportFixture(t *testing.T) *qg15AS3ExportFixtureV1 {
	t.Helper()
	f := qg15AS3ExportFixture(t)
	p := f.request.Problem
	p.Status = domain.ProblemStatusPublished
	p.UpdatedAt = time.Now().UTC()
	var audit qualitygate.AuditV1
	require.NoError(t, json.Unmarshal(f.reader.data[f.evidence.AuditArtifact.Key], &audit))
	audit.Decision = qualitygate.DecisionQuarantine
	audit.DeterministicGatesPassed = false
	audit.GateResults[8].Status = qualitygate.GateStatusCheckFailed
	audit.BlockingIssues = []qualitygate.BlockingIssueV1{{Source: qualitygate.IssueSourceDeterministic, Gate: qualitygate.GateHiddenRegression, Code: "hidden_regression.check_failed"}}
	data, _, err := qualitygate.CanonicalAuditV1(audit)
	require.NoError(t, err)
	f.evidence.Decision = qualitygate.DecisionQuarantine
	f.evidence.AuditArtifact = qg15AS3ArtifactRef(data, f.evidence.AuditArtifact.Bucket, *p.WorkflowID, f.evidence.AuditArtifact.Producer)
	f.reader.data[f.evidence.AuditArtifact.Key] = data
	qg15ARefreshS3ExportMetadata(t, f, activities.StoreProblemS3QualityDraftPayloadVersion)
	return f
}

func TestS3HumanApprovalPreservesOriginalFailureAndRequiresCurrentRecord(t *testing.T) {
	ctx := context.Background()
	f := manualS3ExportFixture(t)
	p := f.request.Problem
	original := append([]byte(nil), f.reader.data[f.evidence.AuditArtifact.Key]...)
	approved := &s3ManualReleaseApprovalV1{problemID: p.ID, updatedAt: p.UpdatedAt, approved: true}
	binding, err := loadS3ExportBindingV1(ctx, p, f.request.TestCases, f.reader, approved)
	require.NoError(t, err)
	require.Equal(t, qualitygate.DecisionQuarantine, binding.QualityAuditV1.Decision)
	require.Equal(t, qualitygate.GateStatusCheckFailed, binding.QualityAuditV1.GateResults[8].Status)
	require.True(t, bytes.Equal(original, binding.QualityAuditBytes))
	for _, invalid := range []*s3ManualReleaseApprovalV1{nil,
		{problemID: p.ID, updatedAt: p.UpdatedAt, approved: false},
		{problemID: uuid.New(), updatedAt: p.UpdatedAt, approved: true},
		{problemID: p.ID, updatedAt: p.UpdatedAt.Add(-time.Second), approved: true},
	} {
		_, err := loadS3ExportBindingV1(ctx, p, f.request.TestCases, f.reader, invalid)
		require.ErrorIs(t, err, ErrConflict)
	}
	qg15ASetRootMetadataField(t, p, "manual_release_approval", map[string]interface{}{"approval_id": uuid.NewString(), "approved_by": "forged"})
	_, err = loadS3ExportBindingV1(ctx, p, f.request.TestCases, f.reader)
	require.ErrorIs(t, err, ErrConflict, "editable summary must never grant approval")
}

func TestS3HumanApprovalCannotOverrideMissingOrChangedAssets(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*qg15AS3ExportFixtureV1)
	}{
		{"missing program", func(f *qg15AS3ExportFixtureV1) { delete(f.reader.data, f.evidence.MainProgramArtifact.Key) }},
		{"changed oracle source", func(f *qg15AS3ExportFixtureV1) {
			f.reader.data[f.evidence.OracleProgramArtifact.Key] = []byte("changed")
		}},
		{"missing oracle receipt", func(f *qg15AS3ExportFixtureV1) { delete(f.reader.data, f.evidence.OracleReceiptArtifact.Key) }},
		{"changed statement", func(f *qg15AS3ExportFixtureV1) { f.request.Problem.Statement += "edited" }},
		{"missing testcase", func(f *qg15AS3ExportFixtureV1) { delete(f.reader.data, f.request.TestCases[1].InputPath) }},
		{"changed testcase output", func(f *qg15AS3ExportFixtureV1) { f.reader.data[f.request.TestCases[1].OutputPath] = []byte("wrong\n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := manualS3ExportFixture(t)
			p := f.request.Problem
			tc.edit(f)
			_, err := loadS3ExportBindingV1(context.Background(), p, f.request.TestCases, f.reader, &s3ManualReleaseApprovalV1{problemID: p.ID, updatedAt: p.UpdatedAt, approved: true})
			require.ErrorIs(t, err, ErrConflict)
		})
	}
}

type manualS3ProblemSource struct {
	qg15AHydroProblemSource
	approved  bool
	solutions []*domain.Solution
}

func (s *manualS3ProblemSource) HasCurrentManualReleaseApproval(_ context.Context, id uuid.UUID, updated time.Time) (bool, error) {
	return s.approved && id == s.problem.ID && updated.Equal(s.problem.UpdatedAt), nil
}
func (s *manualS3ProblemSource) GetSolutions(context.Context, uuid.UUID) ([]*domain.Solution, error) {
	return s.solutions, nil
}

func TestS3ProductExportUsesDatabaseApprovalAndChecksPersistedPrograms(t *testing.T) {
	f := manualS3ExportFixture(t)
	p := f.request.Problem
	source := &manualS3ProblemSource{qg15AHydroProblemSource: qg15AHydroProblemSource{problem: p, testCases: f.request.TestCases}, approved: true,
		solutions: []*domain.Solution{{ProblemID: p.ID, SolutionType: domain.SolutionTypeMain, SourceCode: "main source"}, {ProblemID: p.ID, SolutionType: domain.SolutionTypeBrute, SourceCode: "oracle source"}}}
	svc := &HydroExportService{problems: source, objects: f.reader}
	_, err := svc.BuildProblemPackage(context.Background(), p.ID)
	require.NoError(t, err)
	source.approved = false
	_, err = svc.BuildProblemPackage(context.Background(), p.ID)
	require.ErrorContains(t, err, "当前版本的人工审核")
	source.approved = true
	source.solutions[0].SourceCode = "edited program"
	_, err = svc.BuildProblemPackage(context.Background(), p.ID)
	require.ErrorContains(t, err, "源代码不一致")
}

func TestIncompleteGenerationCandidateCannotExportAfterHumanApproval(t *testing.T) {
	f := manualS3ExportFixture(t)
	p := f.request.Problem
	qg15ASetRootMetadataField(t, p, "generation_candidate", map[string]interface{}{"complete": false})
	source := &manualS3ProblemSource{qg15AHydroProblemSource: qg15AHydroProblemSource{problem: p, testCases: f.request.TestCases}, approved: true}
	for _, legacy := range []bool{false, true} {
		svc := &HydroExportService{problems: source, objects: f.reader, legacyOnly: legacy}
		_, err := svc.BuildProblemPackage(context.Background(), p.ID)
		require.ErrorContains(t, err, "补齐测试数据")
		_, err = svc.loadTestingProblem(context.Background(), p.ID)
		require.ErrorContains(t, err, "补齐测试数据")
	}
}

func trainingS3ExportFixture(t *testing.T) *qg15AS3ExportFixtureV1 {
	t.Helper()
	f := qg15AS3ExportFixture(t)
	var audit qualitygate.AuditV1
	require.NoError(t, json.Unmarshal(f.reader.data[f.evidence.AuditArtifact.Key], &audit))
	audit.Policy = qualitygate.PolicyProblemSetTraining
	_, ruleSHA, err := qualitygate.CanonicalRuleForPolicyV1(audit.Policy)
	require.NoError(t, err)
	audit.RuleSHA256 = ruleSHA
	audit.GateResults[8].Status = qualitygate.GateStatusNotConfigured
	audit.Warnings = []qualitygate.BlockingIssueV1{{Source: qualitygate.IssueSourceReviewer, Gate: qualitygate.GateReviewerSchemaVerdict, Code: "review.score_below_threshold.difficulty_calibration"}}
	data, _, err := qualitygate.CanonicalAuditV1(audit)
	require.NoError(t, err)
	f.evidence.Policy = audit.Policy
	f.evidence.AuditArtifact = qg15AS3ArtifactRef(data, f.evidence.AuditArtifact.Bucket, *f.request.Problem.WorkflowID, f.evidence.AuditArtifact.Producer)
	f.reader.data[f.evidence.AuditArtifact.Key] = data
	qg15ARefreshS3ExportMetadata(t, f, activities.StoreProblemS3QualityDraftPayloadVersion)
	return f
}

func TestS3TrainingExportPreservesOptionalHiddenStateAndDifficultyWarning(t *testing.T) {
	f := trainingS3ExportFixture(t)
	binding, err := loadS3ExportBindingV1(context.Background(), f.request.Problem, f.request.TestCases, f.reader)
	require.NoError(t, err)
	require.Equal(t, qualitygate.GateStatusNotConfigured, binding.QualityAuditV1.GateResults[8].Status)
	require.Equal(t, "review.score_below_threshold.difficulty_calibration", binding.QualityAuditV1.Warnings[0].Code)
	require.Equal(t, f.reader.data[f.evidence.AuditArtifact.Key], binding.QualityAuditBytes)
	f.evidence.Policy = ""
	qg15ARefreshS3ExportMetadata(t, f, activities.StoreProblemS3QualityDraftPayloadVersion)
	_, err = loadS3ExportBindingV1(context.Background(), f.request.Problem, f.request.TestCases, f.reader)
	require.ErrorContains(t, err, "policy")
}

func TestS3TrainingOptionalHiddenDoesNotRelaxOtherGatesOrStrictPolicy(t *testing.T) {
	f := trainingS3ExportFixture(t)
	var audit qualitygate.AuditV1
	require.NoError(t, json.Unmarshal(f.reader.data[f.evidence.AuditArtifact.Key], &audit))
	for _, index := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		bad := audit
		bad.GateResults = append([]qualitygate.GateResultV1(nil), audit.GateResults...)
		bad.GateResults[index].Status = qualitygate.GateStatusNotConfigured
		require.Error(t, validateS3ExportAuditPassV1(bad, audit.SubjectID, audit.SubjectRevision))
	}
	bad := audit
	bad.Policy = ""
	_, bad.RuleSHA256, _ = qualitygate.CanonicalRuleV1()
	require.Error(t, validateS3ExportAuditPassV1(bad, audit.SubjectID, audit.SubjectRevision))
	bad = audit
	bad.GateResults = append([]qualitygate.GateResultV1(nil), audit.GateResults...)
	bad.GateResults[8].Status = qualitygate.GateStatusCheckFailed
	require.Error(t, validateS3ExportAuditPassV1(bad, audit.SubjectID, audit.SubjectRevision))
}
