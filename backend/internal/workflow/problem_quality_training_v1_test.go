package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
)

func setTrainingProductInput(h *fixed20Harness, target int) {
	h.input.SubjectID = generationapi.JobIDPrefix + strings.Repeat("a", 64)
	h.input.Params.ProblemSetContext = &domain.ProblemSetGenerationContext{SetID: uuid.New(), Position: 1}
	h.input.Params.Difficulty = target
	h.llm.targetDifficulty = target
}

func TestProblemSetQualityPreservesTargetAndEstimatesWithoutDifficultyFailure(t *testing.T) {
	h := newFixed20HarnessWithOptions(t, 81, fixed20Scenario{}, fixed20HarnessOptions{noHiddenSuite: true})
	setTrainingProductInput(h, 400)
	response, err := fixed20ReviewResponse(false)
	require.NoError(t, err)
	var review qualitygate.ReviewAssessmentV1
	require.NoError(t, json.Unmarshal([]byte(response), &review))
	review.Dimensions.DifficultyCalibration.Score = qualitygate.ScoreV1(2)
	review.Dimensions.DifficultyCalibration.Notes = "目标400，推理负担更接近600；其余检查通过。"
	review.Blockers = []qualitygate.ReviewBlockerV1{{Code: "difficulty.target_mismatch", ResponsibleAsset: "final_statement", Witness: qualitygate.ExecutableWitnessV1{Runner: "reviewer", FixtureRef: "cas://synthetic-review", Assertion: "目标与估分相差200"}}}
	estimate := 600
	review.EstimatedDifficulty = &estimate
	review.DifficultyExplanation = "依据最简单解法的建模负担预估；仅作为训练参考。"
	raw, err := json.Marshal(review)
	require.NoError(t, err)
	h.llm.reviewResponse = string(raw)
	stored := 0
	h.env.RegisterActivityWithOptions(func(_ context.Context, input activities.StoreS3QualityDraftInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
		stored++
		require.Equal(t, 400, input.Params.Difficulty)
		require.Nil(t, input.Failure)
		require.NotNil(t, input.DifficultyAssessment)
		require.Equal(t, 400, input.DifficultyAssessment.Target)
		require.Equal(t, 600, *input.DifficultyAssessment.Estimated)
		require.Equal(t, 200, *input.DifficultyAssessment.Delta)
		return &activities.StoreS3QualityDraftResultV1{PayloadVersion: activities.StoreS3QualityDraftPayloadVersionV1, ProblemID: uuid.NewString(), Status: domain.ProblemStatusDraft, AuditSHA256: input.AuditArtifact.SHA256, TestManifestSHA256: input.TestManifestArtifact.SHA256}, nil
	}, activity.RegisterOptions{Name: "StoreS3QualityDraftActivityV1"})
	result, err := h.execute(t)
	require.NoError(t, err)
	require.Equal(t, 1, stored)
	require.Equal(t, qualitygate.DecisionPass, result.Decision)
	require.Equal(t, qualitygate.PolicyProblemSetTrainingV2, result.Audit.Policy)
	require.NotEmpty(t, result.Audit.Warnings)
	require.Empty(t, result.Audit.BlockingIssues)
	require.Equal(t, qualitygate.GateStatusNotConfigured, result.Audit.GateResults[8].Status)
	require.False(t, result.HiddenInvoked)
	require.Equal(t, 0, h.hidden.requestCount())
	require.Equal(t, 1, h.trace.count("GenerateAuthoringPlanActivity"), "difficulty mismatch must not trigger repeated generation")
}

func TestProblemSetQualityRetainsCompleteRejectedCandidateAndOriginalAudit(t *testing.T) {
	h := newFixed20Harness(t, 82, fixed20Scenario{falseReviewer: true})
	setTrainingProductInput(h, 1200)
	response, err := fixed20ReviewResponse(false)
	require.NoError(t, err)
	var review qualitygate.ReviewAssessmentV1
	require.NoError(t, json.Unmarshal([]byte(response), &review))
	review.Dimensions.Correctness.Score = qualitygate.ScoreV1(6)
	review.Dimensions.Correctness.Notes = "合成评审发现题意存在正确性疑点，需要人工审核。"
	raw, err := json.Marshal(review)
	require.NoError(t, err)
	h.llm.reviewResponse = string(raw)
	stored := 0
	h.env.RegisterActivityWithOptions(func(_ context.Context, input activities.StoreS3QualityDraftInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
		stored++
		require.NotNil(t, input.Failure)
		require.Equal(t, qualitygate.GateReviewerSchemaVerdict, input.Failure.BlockedGate)
		require.NotEmpty(t, input.Failure.BlockingIssues)
		require.Equal(t, 1200, input.Params.Difficulty)
		require.NotEmpty(t, input.MainProgramArtifact.SHA256)
		require.NotEmpty(t, input.OracleProgramArtifact.SHA256)
		require.NotEmpty(t, input.TestManifestArtifact.SHA256)
		return &activities.StoreS3QualityDraftResultV1{PayloadVersion: activities.StoreS3QualityDraftPayloadVersionV1, ProblemID: uuid.NewString(), Status: domain.ProblemStatusQuarantined, AuditSHA256: input.AuditArtifact.SHA256, TestManifestSHA256: input.TestManifestArtifact.SHA256}, nil
	}, activity.RegisterOptions{Name: "StoreS3QualityDraftActivityV1"})
	result, err := h.execute(t)
	require.NoError(t, err)
	require.Equal(t, 1, stored)
	require.True(t, problemQualityHasCompleteAssetsV1(result))
	require.Equal(t, qualitygate.DecisionQuarantine, result.Audit.Decision)
	require.Equal(t, domain.ProblemStatusQuarantined, result.StoredProblemStatus)
	require.NotEmpty(t, result.StoredProblemID)
	require.NotNil(t, result.Failure)
	require.Nil(t, result.DifficultyAssessment.Estimated, "missing model estimate must never be replaced by the target")
}

func TestProblemSetEarlyVerificationFailureRetainsPartialAssetsForRegeneration(t *testing.T) {
	h := newFixed20Harness(t, 83, fixed20Scenario{numericMismatch: true})
	setTrainingProductInput(h, 1000)
	stored := 0
	h.env.RegisterActivityWithOptions(func(_ context.Context, input activities.StoreS3ReviewCandidateInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
		stored++
		require.NotNil(t, input.AuthoringBundleArtifact)
		require.NotNil(t, input.StatementDraftArtifact)
		require.NotNil(t, input.MainProgramArtifact)
		require.NotNil(t, input.OracleProgramArtifact)
		require.NotNil(t, input.CasePlanArtifact)
		require.NotNil(t, input.Failure)
		require.Equal(t, qualitygate.GateOracleDifferential, input.Failure.BlockedGate)
		return &activities.StoreS3QualityDraftResultV1{PayloadVersion: activities.StoreS3QualityDraftPayloadVersionV1, ProblemID: uuid.NewString(), Status: domain.ProblemStatusQuarantined}, nil
	}, activity.RegisterOptions{Name: "StoreS3ReviewCandidateActivityV1"})
	result, err := h.execute(t)
	require.NoError(t, err)
	require.Equal(t, 1, stored)
	require.False(t, problemQualityHasCompleteAssetsV1(result))
	require.NotEmpty(t, result.StoredProblemID)
	require.Equal(t, domain.ProblemStatusQuarantined, result.StoredProblemStatus)
	require.Equal(t, 0, h.trace.count("S3StrictReviewerActivityV1"))
}

func TestProblemSetUnconfiguredEmbeddingStillStoresCompleteCandidateWithWarning(t *testing.T) {
	h := newFixed20HarnessWithOptions(t, 84, fixed20Scenario{}, fixed20HarnessOptions{noEmbedding: true, noHiddenSuite: true})
	setTrainingProductInput(h, 800)
	stored := 0
	h.env.RegisterActivityWithOptions(func(_ context.Context, input activities.StoreS3QualityDraftInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
		stored++
		require.Nil(t, input.Failure)
		return &activities.StoreS3QualityDraftResultV1{PayloadVersion: activities.StoreS3QualityDraftPayloadVersionV1, ProblemID: uuid.NewString(), Status: domain.ProblemStatusDraft, AuditSHA256: input.AuditArtifact.SHA256, TestManifestSHA256: input.TestManifestArtifact.SHA256}, nil
	}, activity.RegisterOptions{Name: "StoreS3QualityDraftActivityV1"})
	result, err := h.execute(t)
	require.NoError(t, err)
	require.Equal(t, 1, stored)
	require.True(t, problemQualityHasCompleteAssetsV1(result))
	require.True(t, result.ReviewerInvoked)
	require.Equal(t, qualitygate.DecisionPass, result.Audit.Decision)
	require.Equal(t, qualitygate.GateStatusNotConfigured, result.Audit.GateResults[7].Status)
	require.Empty(t, result.Audit.BlockingIssues)
	require.NotEmpty(t, result.Audit.Warnings, "not doing similarity checks must remain visible")
}
