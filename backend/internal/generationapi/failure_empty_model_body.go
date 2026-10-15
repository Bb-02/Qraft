package generationapi

import (
	"regexp"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

// Only an EOF at the start of the main-response JSON decoder establishes an
// absent final body. Unexpected EOF, invalid source code and quality verdicts
// do not establish that the adapter received an empty body.
var emptyMainSolutionResponsePattern = regexp.MustCompile(`(?i)\bstrict main-solution response:\s*(?:\$:\s*)?EOF(?:\s*$|\s+\((?:type:\s*)?S3QualityContractError\b)`)
var mainSolutionOutputErrorTypePattern = regexp.MustCompile(`(?i)(?:^|\btype:\s*)(MainSolutionEmptyResponseError|MainSolutionFormatError|MainSolutionTruncatedError)\b`)

func mainSolutionOutputFailure(diagnostic string) *domain.GenerationFailure {
	matched := mainSolutionOutputErrorTypePattern.FindStringSubmatch(diagnostic)
	if len(matched) == 0 {
		return emptyMainSolutionResponseFailure(diagnostic)
	}
	if strings.EqualFold(matched[1], "MainSolutionEmptyResponseError") {
		return emptyMainSolutionFailure()
	}
	retryable := false
	failure := &domain.GenerationFailure{
		Code: "model_output_invalid", Category: "模型回复格式", Stage: "生成参考程序",
		Message:    "参考程序模型的回复格式不符合要求，系统无法读取",
		Reason:     "模型回复未符合约定的程序源码与语言结构，无法形成可验证的参考程序。",
		Suggestion: "检查模型服务返回与兼容协议，修复后重试未完成题目。",
		Retryable:  &retryable,
	}
	if strings.EqualFold(matched[1], "MainSolutionTruncatedError") {
		failure.Code, failure.Category = "model_output_truncated", "模型回复不完整"
		failure.Message = "参考程序模型回复被截断，程序尚未生成完整"
		failure.Reason = "当前接口适配器未取得完整的参考程序正文。"
		failure.Suggestion = "检查模型输出长度限制与兼容协议，修复后重试未完成题目。"
	}
	return failure
}

func emptyMainSolutionResponseFailure(diagnostic string) *domain.GenerationFailure {
	if !emptyMainSolutionResponsePattern.MatchString(diagnostic) {
		return nil
	}
	return emptyMainSolutionFailure()
}

func emptyMainSolutionFailure() *domain.GenerationFailure {
	retryable := false
	return &domain.GenerationFailure{
		Code: "model_output_empty", Category: "模型回复为空", Stage: "生成参考程序",
		Message:    "模型未返回可用参考程序",
		Reason:     "当前接口适配器未取得可用最终正文。",
		Suggestion: "检查模型服务返回与兼容协议，修复后重试未完成题目。",
		Retryable:  &retryable,
	}
}
