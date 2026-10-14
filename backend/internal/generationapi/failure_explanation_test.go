package generationapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestModelOutputFailureExplainsStageAndPreservesEvidence(t *testing.T) {
	for _, tc := range []struct{ diagnostic, stage, reason, code string }{
		{"parsing authoring-plan response: $.semantic_spec.problem_definition: expected scalar JSON value", "设计题意与约束", "一段文字", "model_output_invalid"},
		{"parsing authoring-plan response: $.semantic_spec.input_constraints: unknown or non-canonical JSON key", "设计题意与约束", "字段名", "model_output_invalid"},
		{"parsing authoring-plan response: $.semantic_spec: invalid character after object key:value pair", "设计题意与约束", "合法的 JSON", "model_output_invalid"},
		{"AuthoringPlanFormatError: schema mismatch difficulty 401", "设计题意与约束", "数据结构", "model_output_invalid"},
		{"parsing authoring-plan response: $.unsupported parser type: unknown or non-canonical JSON key", "设计题意与约束", "字段名", "model_output_invalid"},
		{"ReviewerFormatError: unknown field", "评审题目", "字段名", "model_output_invalid"},
		{"AuthoringPlanTruncatedError: stop_reason=length", "设计题意与约束", "截断", "model_output_truncated"},
	} {
		t.Run(tc.diagnostic, func(t *testing.T) {
			got := DescribeFailure(errors.New(tc.diagnostic))
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, tc.stage, got.Stage)
			require.Contains(t, got.Reason, tc.reason)
			require.Equal(t, tc.diagnostic, got.TechnicalDetails)
			require.NotEmpty(t, got.Category)
			require.NotEmpty(t, got.Suggestion)
		})
	}
}

func TestHistoricalFailureEnrichmentDoesNotRewritePersistedRecord(t *testing.T) {
	original := &domain.GenerationFailure{Code: "generation_failed", Message: "原记录", TechnicalDetails: "parsing authoring-plan response: expected scalar JSON value"}
	got := EnrichFailure(original)
	require.Equal(t, "model_output_invalid", got.Code)
	require.Equal(t, "generation_failed", original.Code)
	require.Equal(t, "原记录", original.Message)
	require.NotSame(t, original, got)
	require.Nil(t, EnrichFailure(nil))
}

func TestHTTPDigitsInWorkflowAndJSONPathsAreNotAuthenticationEvidence(t *testing.T) {
	for _, diagnostic := range []string{"synthetic workflow-401-failed", "unrecognized field $.items[403]", "problem rating 429 failed", "input violated forbidden boundary zero"} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "generation_failed", got.Code)
	}
	got := DescribeFailure(errors.New("provider response status code: 403"))
	require.Equal(t, "model_authentication", got.Code)
	require.False(t, strings.Contains(got.Suggestion, "难度"))
}

func TestUnsupportedInputFormatHasCapabilityExplanation(t *testing.T) {
	for _, diagnostic := range []string{`input symbol "s" has unsupported parser type "graph"`, `unsupported input grammar profile "line-text-v1"`, "token string requires non-empty ASCII without whitespace", "sample input contains unsupported whitespace U+00A0"} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "unsupported_input_format", got.Code)
		require.Equal(t, "流程能力限制", got.Category)
		require.Equal(t, "样例与测试数据检查", got.Stage)
		require.Equal(t, diagnostic, got.TechnicalDetails)
		require.NotContains(t, got.Suggestion, "人工确认")
	}
}

func TestDeclaredInputSymbolRestrictionIsExplainedAsHistoricalPipelineCapability(t *testing.T) {
	for _, diagnostic := range []string{
		"The synthetic task needs a string. Generation parameters restrict supported_input_symbol_types to only [integer,integer_sequence].",
		"The synthetic task needs a string. supported_input_symbol_types is [integer,integer_sequence]. The string type is not available as an input symbol in this pipeline configuration.",
	} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "unsupported_input_format", got.Code)
		require.Equal(t, "设计题意与约束", got.Stage)
		require.Contains(t, got.Reason, "当时支持范围")
		require.Equal(t, diagnostic, got.TechnicalDetails)
		original := &domain.GenerationFailure{Code: "quality_review_required", BlockedGate: "spec_lint", Message: "合成旧分类", TechnicalDetails: diagnostic}
		require.Equal(t, got.Code, EnrichFailure(original).Code)
		require.Equal(t, "quality_review_required", original.Code)
		require.Equal(t, diagnostic, original.TechnicalDetails)
	}
	got := DescribeFailure(errors.New(`parsing authoring-plan response: unknown field "supported_input_symbol_types restrict to only integer"`))
	require.Equal(t, "model_output_invalid", got.Code, "malformed JSON remains a formatter failure")
}

func TestUnsupportedOracleJudgeContractDoesNotBlameProgramOutputs(t *testing.T) {
	for _, diagnostic := range []string{"unsupported oracle judge contract: mode special_judge, comparison profile synthetic", "a special checker cannot use ordinary output comparison"} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "unsupported_judge_contract", got.Code)
		require.Equal(t, "独立解法验证", got.Stage)
		require.Contains(t, got.Reason, "不能用普通输出比较代替")
		require.NotContains(t, got.Suggestion, "人工")
	}
	got := DescribeFailure(errors.New(`parsing authoring-plan response: unknown field "unsupported oracle judge contract"`))
	require.Equal(t, "model_output_invalid", got.Code)
}
