package generationapi

import (
	"errors"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestTestDataFailureCausesHaveSpecificActions(t *testing.T) {
	for _, tc := range []struct{ name, diagnostic, code, stage, reason string }{
		{"request limit", "remote generator execution failed closed: sandbox HTTP 400 (invalid_request): declared batch output budget exceeds 12345 bytes", "verification_request_limit", "提交程序执行请求", "单批测试数量"},
		{"hidden request limit", "hidden validation: sandbox HTTP 400 (invalid_request): declared batch output budget exceeds 12345 bytes", "verification_request_limit", "提交程序执行请求", "尚未运行"},
		{"empty generator", "remote generator execution failed closed: generator case 3 produced empty input", "testdata_generator_empty_output", "生成测试数据", "空白数据"},
		{"legacy malformed JSON", "parsing test data response: model response is not a complete JSON object", "model_output_invalid", "生成测试数据", "合法的 JSON"},
		{"new malformed JSON", "test-data JSON format is still invalid after one correction (type: TestDataFormatError)", "model_output_invalid", "生成测试数据", "完整 JSON 对象"},
		{"new truncated JSON", "test-data response was truncated (type: TestDataTruncatedError)", "model_output_truncated", "生成测试数据", "截断"},
		{"legacy truncated JSON", "test data response was truncated at max_tokens", "model_output_truncated", "生成测试数据", "截断"},
		{"unmarshal", "parsing testdata response: cannot unmarshal string into field of type int", "model_output_invalid", "生成测试数据", "另一种类型"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribeFailure(syntheticChildFailure(t, temporal.NewNonRetryableApplicationError(tc.diagnostic, "SyntheticTestDataFailure", nil)))
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, tc.stage, got.Stage)
			require.Contains(t, got.Reason, tc.reason)
			require.Contains(t, got.TechnicalDetails, tc.diagnostic)
			require.NotContains(t, got.Suggestion, "人工确认")
			require.NotContains(t, got.Suggestion, "worker")
			require.NotContains(t, got.Suggestion, "提高难度")
			require.NotNil(t, got.Retryable)
			require.False(t, *got.Retryable)
		})
	}
}

func TestTestDataFormattingTakesPrecedenceOverDiagnosticKeywords(t *testing.T) {
	for _, diagnostic := range []string{
		`parsing test data response: unknown field "sandbox HTTP 401 unauthorized"`,
		`parsing test data response: unknown field "sandbox HTTP 400 (invalid_request): declared batch output budget exceeds 12345 bytes"`,
		`parsing test data response: unknown field "generator case 3 produced empty input"`,
		`parsing test data response: unknown field "unsupported parser type"`,
		`parsing test data response: unknown field "max_tokens"`,
	} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "model_output_invalid", got.Code)
		require.Equal(t, "生成测试数据", got.Stage)
		require.Equal(t, diagnostic, got.TechnicalDetails)
	}
}

func TestHistoricalTestDataFailuresAreReclassifiedWithoutMutation(t *testing.T) {
	for _, tc := range []struct{ oldCode, diagnostic, code string }{
		{"verification_unavailable", "sandbox HTTP 400 (invalid_request): declared batch output budget exceeds 12345 bytes", "verification_request_limit"},
		{"generation_failed", "remote generator execution failed closed: generator case 3 produced empty input", "testdata_generator_empty_output"},
		{"verification_failed", "parsing test data response: model response is not a complete JSON object", "model_output_invalid"},
		{"quality_review_required", "The current pipeline supports only integer input; string input is unsupported", "unsupported_input_format"},
	} {
		original := &domain.GenerationFailure{Code: tc.oldCode, Message: "合成历史记录", TechnicalDetails: tc.diagnostic}
		got := EnrichFailure(original)
		require.Equal(t, tc.code, got.Code)
		require.Equal(t, tc.oldCode, original.Code)
		require.Equal(t, "合成历史记录", original.Message)
		require.Equal(t, tc.diagnostic, got.TechnicalDetails)
		require.NotEmpty(t, got.Category)
		require.NotEmpty(t, got.Stage)
		require.NotEmpty(t, got.Reason)
	}
}

func TestStatementPresentationFailureHasConcreteStageWithoutInventingProblemDefects(t *testing.T) {
	for _, diagnostic := range []string{`narrative contains normative or meta term "合成规范词"`, "title must not introduce normative operators"} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "statement_presentation_rejected", got.Code)
		require.Equal(t, "编写题面", got.Stage)
		require.Contains(t, got.Reason, "表现层")
		require.Contains(t, got.Reason, "不表示题意、算法或目标难度有误")
		require.NotContains(t, got.Suggestion, "人工确认")
		require.Equal(t, diagnostic, got.TechnicalDetails)
		original := &domain.GenerationFailure{Code: "generation_failed", Message: "旧的合成未知错误", TechnicalDetails: diagnostic}
		require.Equal(t, got.Code, EnrichFailure(original).Code)
		require.Equal(t, "generation_failed", original.Code)
	}
	got := DescribeFailure(errors.New(`parse statement narrative response: unknown field "narrative contains normative or meta term"`))
	require.Equal(t, "model_output_invalid", got.Code)
	require.Equal(t, "编写题面", got.Stage)
}
