package workflow

import (
	"context"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
)

func TestProblemSetOracleIdentityFailureIsPropagatedAndLegacyNilKeepsExistingPath(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "structured identity failure", true: "historical nil failure"}[legacy], func(t *testing.T) {
			h := newFixed20Harness(t, 92, fixed20Scenario{})
			setTrainingProductInput(h, 1000)
			h.input.Params.ProviderConfig.Verification.Model = h.input.Params.ProviderConfig.Statement.Model
			h.input.Params.ProviderConfig.Verification.BaseURL = h.input.Params.ProviderConfig.Statement.BaseURL
			if legacy {
				h.env.RegisterActivityWithOptions(func(ctx context.Context, in activities.S3OracleGateInputV1) (*activities.S3OracleGateResultV1, error) {
					result, err := h.acts.VerifyProgramAgainstIndependentOracleActivityV1(ctx, in)
					if result != nil {
						result.Failure = nil
					}
					return result, err
				}, activity.RegisterOptions{Name: "VerifyProgramAgainstIndependentOracleActivityV1"})
			}
			stored := 0
			h.env.RegisterActivityWithOptions(func(_ context.Context, in activities.StoreS3ReviewCandidateInputV1) (*activities.StoreS3QualityDraftResultV1, error) {
				stored++
				require.NotNil(t, in.Failure)
				require.Equal(t, qualitygate.GateOracleDifferential, in.Failure.BlockedGate)
				if legacy {
					require.Equal(t, "quality_review_required", in.Failure.Code)
				} else {
					require.Equal(t, "oracle_identity_not_independent", in.Failure.Code)
					require.Contains(t, in.Failure.Reason, "实际返回相同模型")
					require.NotContains(t, in.Failure.Suggestion, "人工")
				}
				return &activities.StoreS3QualityDraftResultV1{PayloadVersion: activities.S3QualityPayloadVersionV1, ProblemID: uuid.NewString(), Status: domain.ProblemStatusQuarantined}, nil
			}, activity.RegisterOptions{Name: "StoreS3ReviewCandidateActivityV1"})
			result, err := h.execute(t)
			require.NoError(t, err)
			require.Equal(t, 1, stored)
			require.False(t, problemQualityHasCompleteAssetsV1(result))
			require.Equal(t, qualitygate.GateOracleDifferential, result.StoppedGate)
			require.NotNil(t, result.Failure)
			require.Equal(t, 0, h.trace.count("S3StrictReviewerActivityV1"))
			require.Equal(t, 0, h.trace.count("ResolveS3HiddenSuiteActivityV1"))
			require.Equal(t, 1, h.trace.count("VerifyProgramAgainstIndependentOracleActivityV1"))
		})
	}
}
