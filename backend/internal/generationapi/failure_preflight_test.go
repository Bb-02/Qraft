package generationapi

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestSetGenerationPreflightFailureExplainsEverySafeBlockerAndPreservesDiagnostic(t *testing.T) {
	report := domain.ProblemSetGenerationReadiness{Ready: false, Scope: "configured_routes",
		Blockers: []domain.GenerationReadinessNotice{
			{Code: "oracle_model_independence", Message: "独立验算需要不同模型，当前两路模型相同。", SettingsURL: "https://private.invalid/?secret=synthetic-key"},
			{Code: "oracle_endpoint_independence", Message: "独立验算需要不同的服务端点。"},
		},
		EffectiveRoles: []domain.GenerationReadinessRole{{Model: "synthetic-private-model"}},
		Warnings:       []domain.GenerationReadinessNotice{{Message: "synthetic-private-warning"}},
		Checks:         []domain.GenerationReadinessCheck{{Message: "synthetic-private-check"}},
	}
	err := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError("synthetic private endpoint/key diagnostic", "SetGenerationPreflightError", nil, report))
	failure := DescribeFailure(err)
	require.Equal(t, "generation_configuration_not_ready", failure.Code)
	require.Equal(t, "生成配置未就绪", failure.Category)
	require.Equal(t, "生成前检查", failure.Stage)
	require.Equal(t, []string{report.Blockers[0].Message, report.Blockers[1].Message}, failure.BlockingIssues)
	require.Contains(t, failure.Reason, report.Blockers[0].Message)
	require.Contains(t, failure.Reason, report.Blockers[1].Message)
	require.NotNil(t, failure.Retryable)
	require.False(t, *failure.Retryable)
	require.Contains(t, failure.Suggestion, "重新检查")
	require.Contains(t, failure.Suggestion, "重试未完成题目")
	require.Contains(t, failure.TechnicalDetails, "synthetic private endpoint/key diagnostic")
	for _, secret := range []string{"private.invalid", "synthetic-key", "synthetic-private-model", "synthetic-private-warning", "synthetic-private-check", "endpoint/key diagnostic"} {
		require.NotContains(t, failure.Message+failure.Reason+failure.Suggestion+strings.Join(failure.BlockingIssues, ""), secret)
	}
}

func TestSetGenerationPreflightFailureMissingOrMalformedDetailsUsesSafeFallback(t *testing.T) {
	for _, details := range [][]any{nil, {"synthetic malformed report"}, {domain.ProblemSetGenerationReadiness{Ready: false}}} {
		err := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError("raw https://user:synthetic-secret@private.invalid timed out", "SetGenerationPreflightError", nil, details...))
		failure := DescribeFailure(err)
		require.Equal(t, "generation_configuration_not_ready", failure.Code)
		require.Equal(t, "生成前检查", failure.Stage)
		require.Contains(t, failure.Reason, "当前有效配置")
		require.Contains(t, failure.Reason, "本批调用模型前停止")
		require.Empty(t, failure.BlockingIssues)
		require.NotContains(t, failure.Message+failure.Reason+failure.Suggestion, "synthetic-secret")
		require.NotContains(t, failure.Message+failure.Reason+failure.Suggestion, "private.invalid")
		require.Contains(t, failure.TechnicalDetails, "synthetic-secret")
	}
}

func TestSetGenerationPreflightFailureDoesNotInferTypeFromMessageOrReadinessShapedDetails(t *testing.T) {
	report := domain.ProblemSetGenerationReadiness{Blockers: []domain.GenerationReadinessNotice{{Message: "当前需要另一个验算模型。"}}}
	err := syntheticChildFailure(t, temporal.NewNonRetryableApplicationError("SetGenerationPreflightError mentioned by an unrelated source", "SyntheticUnrelatedFailure", nil, report))
	failure := DescribeFailure(err)
	require.Equal(t, "generation_failed", failure.Code)
	require.NotEqual(t, "生成配置未就绪", failure.Category)
	require.NotContains(t, failure.Reason, report.Blockers[0].Message)

	longDiagnostic := strings.Repeat("合成原始诊断", 1500)
	failure = DescribeFailure(temporal.NewNonRetryableApplicationError(longDiagnostic, "SetGenerationPreflightError", nil))
	require.Len(t, []rune(failure.TechnicalDetails), 6000)
}
