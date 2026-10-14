package generationapi

import (
	"errors"
	"regexp"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

func DescribeFailure(err error) *domain.GenerationFailure {
	if err == nil {
		return nil
	}
	var application *temporal.ApplicationError
	if errors.As(err, &application) && application.HasDetails() {
		var failure domain.GenerationFailure
		if application.Details(&failure) == nil && failure.Code != "" && failure.Message != "" {
			failure.TechnicalDetails = boundedFailureText(failure.TechnicalDetails, 6000)
			return EnrichFailure(&failure)
		}
	}
	failure := &domain.GenerationFailure{Code: "generation_failed", Message: "这道题未能完成生成", Suggestion: "查看生成任务了解失败阶段，修正需求或服务配置后重试。", TechnicalDetails: boundedFailureText(err.Error(), 6000)}
	if formatted := modelOutputFailure(err.Error()); formatted != nil {
		formatted.TechnicalDetails = failure.TechnicalDetails
		return formatted
	}
	if runtime := testDataExecutionFailure(err.Error()); runtime != nil {
		runtime.TechnicalDetails = failure.TechnicalDetails
		return runtime
	}
	if presentation := statementPresentationFailure(err.Error()); presentation != nil {
		presentation.TechnicalDetails = failure.TechnicalDetails
		return presentation
	}
	if unsupported := unsupportedInputFailure(err.Error()); unsupported != nil {
		unsupported.TechnicalDetails = failure.TechnicalDetails
		return unsupported
	}
	message := err.Error()
	if application != nil {
		message = application.Message()
	}
	text := strings.ToLower(message)
	switch {
	case failureHTTPStatus(text, "401|403") || strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication") || strings.Contains(text, "invalid_api_key") || strings.Contains(text, "invalid api key") || strings.Contains(text, "invalid token"):
		failure.Code, failure.Message, failure.Suggestion = "model_authentication", "模型服务未通过身份验证", "检查模型配置中的 API 密钥、服务地址和模型访问权限。"
	case failureHTTPStatus(text, "429") || strings.Contains(text, "quota") || strings.Contains(text, "rate limit"):
		failure.Code, failure.Message, failure.Suggestion = "model_rate_limit", "模型服务限流或额度不足", "检查模型额度，稍后重试；已完成题目会保留。"
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
		failure.Code, failure.Message, failure.Suggestion = "service_timeout", "生成或验证服务响应超时", "检查服务连接后重试此题，必要时减少测试数据规模。"
	case strings.Contains(text, "hidden"):
		failure.Code, failure.Message, failure.Suggestion = "verification_unavailable", "隐藏验证服务未配置或暂时不可用", "这不是题目已被证实有错。可以检查验证服务配置，或审核已保存的候选题后人工确认。"
	case strings.Contains(text, "difficulty") || strings.Contains(text, "难度"):
		failure.Code, failure.Message, failure.Suggestion = "invalid_difficulty", "这道题的难度参数无法使用", "检查题位规划中的难度值。题集分类与标签的建议范围不会限制目标难度。"
	case strings.Contains(text, "compile") || strings.Contains(text, "compilation"):
		failure.Code, failure.Message, failure.Suggestion = "compile_failed", "参考程序未能编译", "打开候选题检查程序，修复后重新验证。"
	case strings.Contains(text, "dedup") || strings.Contains(text, "duplicate") || strings.Contains(text, "重复"):
		failure.Code, failure.Message, failure.Suggestion = "duplicate_candidate", "候选题与已有内容重复或去重检查未完成", "查看具体重复记录，调整题意后重试，或确认是否需要保留。"
	case strings.Contains(text, "quality") || strings.Contains(text, "qualitynotmet"):
		failure.Code, failure.Message, failure.Suggestion = "quality_review_required", "候选题需要进一步审核", "查看未通过的检查项和候选题，修复或由管理员人工确认后继续入集。"
	case strings.Contains(text, "sandbox") || strings.Contains(text, "connection refused") || strings.Contains(text, "unavailable"):
		failure.Code, failure.Message, failure.Suggestion = "verification_unavailable", "生成或验证服务尚未就绪", "检查 worker、代码沙箱和模型服务连接后重试。"
	case strings.Contains(text, "test") || strings.Contains(text, "oracle") || strings.Contains(text, "sample"):
		failure.Code, failure.Message, failure.Suggestion = "verification_failed", "参考解法或测试数据需要检查", "查看候选题的样例、参考程序和测试结果，修复后重新验证。"
	}
	return failure
}

// Array indices, ratings, and workflow IDs can contain HTTP-like digits.
// Only an actual provider status context is evidence of an HTTP failure.
func failureHTTPStatus(text, codes string) bool {
	return regexp.MustCompile(`(?i)(http(?: status)?|status(?: code)?|response code)\s*[:=]?\s*(` + codes + `)\b`).MatchString(text)
}

func boundedFailureText(value string, limit int) string {
	text := []rune(strings.TrimSpace(value))
	if len(text) > limit {
		text = text[:limit]
	}
	return string(text)
}
