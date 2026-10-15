package generationapi

import (
	"errors"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestInternalDataContractFailuresExplainNewAndHistoricalExecutions(t *testing.T) {
	for _, tc := range []struct{ name, diagnostic, code, category, stage string }{
		{"boundary list", `build S3 TestManifest v2: activity error (type: BuildS3TestManifestActivityV1): validate automatic boundary coverage: invalid TestManifest v2: case "synthetic-case-a" boundary_refs must be a present sorted unique array`, "test_manifest_boundary_refs_invalid", "出题流程内部格式问题", "检查测试数据覆盖"},
		{"input normalization", `materialize S3 case plan: activity error (type: MaterializeS3CasePlanActivityV1): case "synthetic-case-b" is not canonical SemanticSpec input: <nil>`, "case_plan_input_normalization_failed", "出题流程输入规范化问题", "整理验证用例"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribeFailure(errors.New(tc.diagnostic))
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, tc.category, got.Category)
			require.Equal(t, tc.stage, got.Stage)
			require.Equal(t, tc.diagnostic, got.TechnicalDetails)
			require.NotNil(t, got.Retryable)
			require.False(t, *got.Retryable)
			require.Contains(t, got.Suggestion, "修复")
			require.Contains(t, got.Suggestion, "重试未完成题目")
			require.NotContains(t, got.Suggestion, "人工")
			require.NotContains(t, got.Suggestion, "补齐")
			for _, oldCode := range []string{"generation_failed", "verification_failed"} {
				legacy := domain.GenerationFailure{Code: oldCode, Message: "参考解法或测试数据需要检查", Suggestion: "查看候选题后修复", TechnicalDetails: tc.diagnostic}
				before := legacy
				projected := EnrichFailure(&legacy)
				require.Equal(t, tc.code, projected.Code)
				require.Equal(t, tc.diagnostic, projected.TechnicalDetails)
				require.Equal(t, before, legacy, "read projection must not rewrite persisted evidence")
				wrapped := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError(legacy.Message, legacy.Code, nil, legacy))
				require.Equal(t, projected, DescribeFailure(wrapped), "legacy structured Temporal details must use the same projection")
			}
		})
	}
}

func TestInternalDataContractClassificationRequiresItsOwnPreciseDiagnostic(t *testing.T) {
	for _, diagnostic := range []string{
		`BuildS3TestManifestActivityV1 failed`,
		`case "synthetic-case" is not canonical SemanticSpec input: invalid integer`,
		`materialize S3 case plan: case "synthetic-case" is not canonical SemanticSpec input: unexpected EOF`,
		`unrelated log: case "synthetic-case" is not canonical SemanticSpec input: <nil>`,
	} {
		require.Nil(t, internalDataContractFailure(diagnostic))
	}
	diagnostic := `parsing authoring-plan response: unknown field "quoted": invalid TestManifest v2: case "synthetic-case" boundary_refs must be a present sorted unique array`
	require.Equal(t, "model_output_invalid", DescribeFailure(errors.New(diagnostic)).Code)
	require.Equal(t, "model_output_invalid", EnrichFailure(&domain.GenerationFailure{Code: "verification_failed", Message: "旧提示", TechnicalDetails: diagnostic}).Code)
}
