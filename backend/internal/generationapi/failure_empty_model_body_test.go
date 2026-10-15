package generationapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestEmptyMainSolutionResponseExplainsMissingFinalBody(t *testing.T) {
	for _, diagnostic := range []string{
		"strict main-solution response: $: EOF",
		"strict main-solution response: EOF",
		"generate S3 main solution: strict main-solution response: \n\t$: EOF  \n",
		"activity error: strict main-solution response: $: EOF (S3QualityContractError)",
		"strict main-solution response: $: EOF (type: S3QualityContractError, retryable: false)",
	} {
		t.Run(diagnostic, func(t *testing.T) {
			got := DescribeFailure(errors.New(diagnostic))
			require.Equal(t, "model_output_empty", got.Code)
			require.Equal(t, "模型回复为空", got.Category)
			require.Equal(t, "生成参考程序", got.Stage)
			require.Equal(t, "模型未返回可用参考程序", got.Message)
			require.Equal(t, "当前接口适配器未取得可用最终正文。", got.Reason)
			require.Contains(t, got.Suggestion, "模型服务返回与兼容协议")
			require.Contains(t, got.Suggestion, "修复后重试")
			require.Equal(t, strings.TrimSpace(diagnostic), got.TechnicalDetails)
			require.NotNil(t, got.Retryable)
			require.False(t, *got.Retryable)
		})
	}
}

func TestEmptyMainSolutionResponseDoesNotReclassifyOtherFailures(t *testing.T) {
	for _, diagnostic := range []string{
		"EOF",
		"strict main-solution response: unexpected EOF",
		"strict main-solution response: $: unexpected EOF",
		"strict main-solution response: $: EOF is not a quality verdict",
		"strict main-solution response: oracle source is empty, oversized, or invalid UTF-8",
		"strict main-solution response: invalid character after object key:value pair",
		"main compilation failed: expected token before EOF",
		"authoring plan lint rejected after format correction: input boundary contradiction",
		"QualityNotMet: STRICT_INEQUALITY_MISMATCH, CASE_INTENT_MISMATCH",
		"reviewer correctness score 3: boundary witness mismatch",
	} {
		t.Run(diagnostic, func(t *testing.T) {
			require.Nil(t, emptyMainSolutionResponseFailure(diagnostic))
			require.NotEqual(t, "model_output_empty", DescribeFailure(errors.New(diagnostic)).Code)
		})
	}
	formatted := DescribeFailure(errors.New("AuthoringPlanFormatError: parsing authoring-plan response: $: EOF"))
	require.Equal(t, "model_output_invalid", formatted.Code)
	require.Equal(t, "设计题意与约束", formatted.Stage)
	nested := DescribeFailure(errors.New("AuthoringPlanFormatError: parsing authoring-plan response: strict main-solution response: $: EOF"))
	require.Equal(t, "model_output_invalid", nested.Code)
	require.Equal(t, "设计题意与约束", nested.Stage)
}

func TestEmptyMainSolutionResponseEnrichmentPreservesOriginalEvidence(t *testing.T) {
	const diagnostic = "strict main-solution response: $: EOF (type: S3QualityContractError, retryable: false)"
	for _, oldCode := range []string{"generation_failed", "verification_failed", "quality_review_required"} {
		t.Run(oldCode, func(t *testing.T) {
			original := domain.GenerationFailure{Code: oldCode, Message: "原记录", TechnicalDetails: diagnostic,
				BlockedGate: "oracle_differential", BlockingIssues: []string{"synthetic retained diagnostic"}}
			before := original
			got := EnrichFailure(&original)
			require.Equal(t, "model_output_empty", got.Code)
			require.Equal(t, diagnostic, got.TechnicalDetails)
			require.Equal(t, original.BlockedGate, got.BlockedGate)
			require.Equal(t, original.BlockingIssues, got.BlockingIssues)
			require.Equal(t, before, original)
			require.NotSame(t, &original, got)
			wrapped := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError(original.Message, oldCode, nil, original))
			require.Equal(t, got, DescribeFailure(wrapped))
			got.BlockingIssues[0] = "projection-only change"
			require.Equal(t, "synthetic retained diagnostic", original.BlockingIssues[0])
		})
	}
}

func TestMainSolutionTypedFailuresExplainTheActualOutputStage(t *testing.T) {
	for _, tc := range []struct{ typ, detail, code, category string }{
		{"MainSolutionEmptyResponseError", "no usable final body after bounded correction", "model_output_empty", "模型回复为空"},
		{"MainSolutionFormatError", "language does not match the requested language", "model_output_invalid", "模型回复格式"},
		{"MainSolutionTruncatedError", "stop_reason=max_tokens", "model_output_truncated", "模型回复不完整"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			for _, diagnostic := range []string{tc.typ + ": " + tc.detail, tc.detail + " (type: " + tc.typ + ", retryable: false)"} {
				got := DescribeFailure(errors.New(diagnostic))
				require.Equal(t, tc.code, got.Code)
				require.Equal(t, tc.category, got.Category)
				require.Equal(t, "生成参考程序", got.Stage)
				require.Equal(t, diagnostic, got.TechnicalDetails)
				require.Contains(t, got.Suggestion, "兼容协议")
				require.False(t, *got.Retryable)
				original := domain.GenerationFailure{Code: "quality_review_required", Message: "原记录", TechnicalDetails: diagnostic,
					BlockedGate: "synthetic retained gate", BlockingIssues: []string{"synthetic retained issue"}}
				projected := EnrichFailure(&original)
				require.Equal(t, got.Code, projected.Code)
				require.Equal(t, got.Stage, projected.Stage)
				require.Equal(t, got.TechnicalDetails, projected.TechnicalDetails)
				require.Equal(t, original.BlockedGate, projected.BlockedGate)
				require.Equal(t, original.BlockingIssues, projected.BlockingIssues)
			}
			wrapped := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError(tc.detail, tc.typ, nil))
			got := DescribeFailure(wrapped)
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, "生成参考程序", got.Stage)
		})
	}
}

func TestMainSolutionTypedFailureRequiresAnActualTypeContext(t *testing.T) {
	for _, diagnostic := range []string{
		"main compilation failed: MainSolutionFormatError appears in source code",
		"quality review failed: a title mentions MainSolutionEmptyResponseError",
		"MainSolutionFormatErrorSupplement: unrelated error",
		"MainSolutionTruncatedErrorSupplement: unrelated error",
	} {
		require.Nil(t, mainSolutionOutputFailure(diagnostic))
	}
	got := DescribeFailure(errors.New("main compilation failed: MainSolutionFormatError appears in source code"))
	require.Equal(t, "compile_failed", got.Code)
}
