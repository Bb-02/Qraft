package generationapi

import (
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

// Only the typed preflight error authorizes reading its safe readiness report.
// Provider errors, endpoint addresses and key material are never mined for a
// user-facing explanation; the original diagnostic stays in TechnicalDetails.
func setGenerationPreflightFailure(application *temporal.ApplicationError) *domain.GenerationFailure {
	if application == nil || application.Type() != "SetGenerationPreflightError" {
		return nil
	}
	retryable := false
	failure := &domain.GenerationFailure{
		Code: "generation_configuration_not_ready", Category: "生成配置未就绪", Stage: "生成前检查",
		Message:    "题集生成前检查未通过",
		Reason:     "当前有效配置未满足题集生成条件，已在本批调用模型前停止。",
		Suggestion: "修正模型或生成服务配置后，重新检查生成条件，再重试未完成题目。",
		Retryable:  &retryable,
	}
	if !application.HasDetails() {
		return failure
	}
	var report domain.ProblemSetGenerationReadiness
	if application.Details(&report) != nil {
		return failure
	}
	seen := map[string]bool{}
	for _, blocker := range report.Blockers {
		message := boundedFailureText(blocker.Message, 800)
		if message == "" || seen[message] {
			continue
		}
		seen[message] = true
		failure.BlockingIssues = append(failure.BlockingIssues, message)
		if len(failure.BlockingIssues) == 8 {
			break
		}
	}
	if len(failure.BlockingIssues) > 0 {
		failure.Reason = boundedFailureText(strings.Join(failure.BlockingIssues, "；"), 4000)
	}
	return failure
}
