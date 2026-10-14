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
	for _, diagnostic := range []string{"synthetic workflow-401-failed", "unrecognized field $.items[403]", "problem rating 429 failed"} {
		got := DescribeFailure(errors.New(diagnostic))
		require.Equal(t, "generation_failed", got.Code)
	}
	got := DescribeFailure(errors.New("provider response status code: 403"))
	require.Equal(t, "model_authentication", got.Code)
	require.False(t, strings.Contains(got.Suggestion, "难度"))
}
