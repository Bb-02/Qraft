package generationapi

import (
	"errors"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestCoverageFailuresExplainMissingEvidenceWithoutGuessingItsCause(t *testing.T) {
	for _, tc := range []struct{ name, diagnostic, code, category string }{
		{"allowed boundary", `validate automatic boundary coverage: derived allowed boundary obligation "allowed_boundary:synthetic-limit" is not covered`, "test_boundary_coverage_incomplete", "测试边界覆盖不足"},
		{"range edge", `build S3 TestManifest v2: derived range-edge obligation "range_edge:synthetic-value:max" is not covered`, "test_boundary_coverage_incomplete", "测试边界覆盖不足"},
		{"historical region", `build S3 TestManifest v2: derived constraint-region obligation "constraint_region:synthetic-combination" has no single matching case`, "test_constraint_region_coverage_incomplete", "测试约束组合覆盖不足"},
		{"multi-region context", `derived constraint-region obligation "constraint_region:synthetic-combination" has no single matching case (region="synthetic-region", boundary_refs=[synthetic-a synthetic-b])`, "test_constraint_region_coverage_incomplete", "测试约束组合覆盖不足"},
		{"early coverage check", `S3 test-data coverage preflight failed: missing obligation synthetic-required-case`, "test_coverage_preflight_failed", "测试覆盖不足"},
		{"early region check", `S3 test-data coverage preflight failed: derived constraint-region obligation "constraint_region:synthetic-combination" has no single matching case (region="synthetic-region", boundary_refs=[synthetic-a synthetic-b])`, "test_coverage_preflight_failed", "测试覆盖不足"},
		{"typed coverage check", `activity error (type: S3TestDataCoverageError): synthetic missing coverage`, "test_coverage_preflight_failed", "测试覆盖不足"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribeFailure(errors.New(tc.diagnostic))
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, tc.category, got.Category)
			require.Equal(t, "检查测试数据覆盖", got.Stage)
			require.Equal(t, tc.diagnostic, got.TechnicalDetails)
			require.Contains(t, got.Reason, "覆盖")
			require.NotContains(t, got.Reason, "测试数据或题目文件缺失，或尚未通过完整性与有效性检查")
			if tc.code == "test_coverage_preflight_failed" {
				require.Contains(t, got.Reason, "根据实际输入检查")
				require.Contains(t, got.Suggestion, "重新检查覆盖")
			} else {
				require.Contains(t, got.Suggestion, "核对并修正覆盖记录")
			}
			require.NotContains(t, got.Suggestion, "模型")
			require.NotContains(t, got.Suggestion, "难度")
			require.NotContains(t, got.Suggestion, "人工")
			require.NotNil(t, got.Retryable)
			require.False(t, *got.Retryable)
			if tc.code == "test_constraint_region_coverage_incomplete" {
				require.Contains(t, got.Reason, "同一个用例")
				require.Contains(t, got.Reason, "也可能已有用例的区域标记")
			}
			for _, oldCode := range []string{"generation_failed", "verification_failed", "quality_review_required"} {
				legacy := domain.GenerationFailure{Code: oldCode, Message: "参考解法或测试数据需要检查", Suggestion: "打开候选补齐文件", TechnicalDetails: tc.diagnostic}
				before := legacy
				projected := EnrichFailure(&legacy)
				require.Equal(t, got, projected)
				require.Equal(t, before, legacy, "historical evidence must not be rewritten")
				wrapped := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError(legacy.Message, oldCode, nil, legacy))
				require.Equal(t, projected, DescribeFailure(wrapped))
			}
		})
	}
}

func TestCoverageFailureClassificationRequiresARealCoverageDiagnostic(t *testing.T) {
	for _, diagnostic := range []string{"coverage check failed", "BuildS3TestManifestActivityV1 failed", `derived allowed boundary obligation "synthetic" is covered`, "constraint_region appears in a task title", "S3TestDataCoverageError appears in a task title"} {
		require.Nil(t, testCoverageFailure(diagnostic))
	}
	for _, diagnostic := range []string{
		`parsing authoring-plan response: unknown field "derived allowed boundary obligation \"synthetic\" is not covered"`,
		`TestDataFormatError: unknown field "S3TestDataCoverageError"`,
		`ReviewerFormatError: unknown field "S3 test-data coverage preflight failed:"`,
	} {
		require.Equal(t, "model_output_invalid", DescribeFailure(errors.New(diagnostic)).Code)
		require.Equal(t, "model_output_invalid", EnrichFailure(&domain.GenerationFailure{Code: "verification_failed", Message: "旧错误", TechnicalDetails: diagnostic}).Code)
	}
}
