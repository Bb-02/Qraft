package workflow

import (
	"context"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
)

func TestProblemSetAuthoringRejectionPreservesStructuredServerLintExplanationV3(t *testing.T) {
	for _, withFailure := range []bool{true, false} {
		t.Run(map[bool]string{true: "structured v3", false: "historical nil"}[withFailure], func(t *testing.T) {
			h := newFixed20Harness(t, 85, fixed20Scenario{})
			setTrainingProductInput(h, 400)
			var expected *domain.GenerationFailure
			stored := 0
			h.env.RegisterActivityWithOptions(func(_ context.Context, input activities.StoreS3ReviewCandidateInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
				stored++
				require.NotNil(t, input.AuthoringBundleArtifact)
				require.Nil(t, input.StatementDraftArtifact)
				require.Nil(t, input.MainProgramArtifact)
				require.Nil(t, input.OracleProgramArtifact)
				if withFailure {
					require.Equal(t, expected, input.Failure)
				} else {
					require.NotNil(t, input.Failure)
					require.Equal(t, "deterministic spec lint failed", input.Failure.TechnicalDetails)
				}
				return &activities.StoreS3QualityDraftResultV1{PayloadVersion: activities.StoreS3QualityDraftPayloadVersionV1, ProblemID: uuid.NewString(), Status: domain.ProblemStatusQuarantined}, nil
			}, activity.RegisterOptions{Name: "StoreS3ReviewCandidateActivityV1"})
			h.env.OnActivity(h.acts.GenerateAuthoringPlanActivity, mock.Anything, mock.Anything).Return(func(_ context.Context, input activities.GenerateAuthoringPlanInput) (*activities.GenerateAuthoringPlanResult, error) {
				result := lintRejectedAuthoringWorkflowResult(t, input)
				if withFailure {
					expected = &domain.GenerationFailure{Code: "spec_lint_failed", Category: "题意与约束检查未通过", Stage: "设计题意与约束", Message: "题目设计存在不一致，需要修正后重新生成", BlockedGate: "spec_lint", BlockingIssues: []string{"整数序列缺少元素范围（semantic_spec.constraints）"}, TechnicalDetails: `{"passed":false,"issues":[{"code":"spec_constraint_missing"}]}`}
					result.Failure = expected
					result.RejectionReason = expected.Message
				}
				return result, nil
			}).Once()
			result, err := h.execute(t)
			require.NoError(t, err)
			require.Equal(t, 1, stored)
			require.Equal(t, qualitygate.DecisionQuarantine, result.Decision)
			require.Equal(t, qualitygate.GateSpecLint, result.StoppedGate)
			require.NotEmpty(t, result.StoredProblemID)
			require.False(t, problemQualityHasCompleteAssetsV1(result))
			if withFailure {
				require.Equal(t, expected, result.Failure)
			}
			require.Zero(t, h.trace.count("S3StrictReviewerActivityV1"))
			require.Zero(t, h.trace.count("RenderStatementFromAuthoringBundleActivityV1"))
		})
	}
}
