package generationapi

import (
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestStructuredOracleIdentityFailureRetainsCauseInsteadOfGuessingMismatch(t *testing.T) {
	retryable := false
	failure := domain.GenerationFailure{Code: "oracle_identity_not_independent", Category: "验证来源不够独立", Stage: "独立解法验证", Message: "独立校验未满足来源独立性要求", Reason: "两路实际返回同一模型，输出一致也不满足当前跨模型规则。", Suggestion: "配置另一个模型和服务地址后重试未完成题目。", BlockedGate: "oracle_differential", TechnicalDetails: "synthetic identity decision", Retryable: &retryable}
	err := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError("oracle verification was blocked", "QualityNotMet", nil, failure))
	require.Equal(t, &failure, DescribeFailure(err))
	require.Equal(t, &failure, EnrichFailure(&failure))
}
