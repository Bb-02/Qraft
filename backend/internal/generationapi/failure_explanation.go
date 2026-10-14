package generationapi

import (
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

// EnrichFailure explains historical generic failures without rewriting their
// persisted evidence. A formatter failure is separate from a quality verdict.
func EnrichFailure(failure *domain.GenerationFailure) *domain.GenerationFailure {
	if failure == nil {
		return nil
	}
	copy := *failure
	if copy.Code == "generation_failed" || copy.Code == "model_output_invalid" || copy.Code == "model_output_truncated" {
		if explained := modelOutputFailure(copy.TechnicalDetails); explained != nil {
			explained.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return explained
		}
	}
	return &copy
}

func modelOutputFailure(diagnostic string) *domain.GenerationFailure {
	text := strings.ToLower(diagnostic)
	stage := ""
	switch {
	case strings.Contains(text, "authoringplanformaterror"), strings.Contains(text, "authoringplantruncatederror"), strings.Contains(text, "parsing authoring-plan response"):
		stage = "设计题意与约束"
	case strings.Contains(text, "reviewerformaterror"), strings.Contains(text, "reviewertruncatederror"), strings.Contains(text, "decode strict reviewer"), strings.Contains(text, "decode review assessment"):
		stage = "评审题目"
	}
	if stage == "" {
		return nil
	}
	retryable := false
	failure := &domain.GenerationFailure{Code: "model_output_invalid", Category: "模型回复格式", Stage: stage,
		Message: "模型回复的格式不符合要求，系统无法读取", Reason: "模型返回的字段或数据结构与出题流程要求不一致。",
		Suggestion: "出题流程会提供完整格式要求，并尝试一次格式纠正。仍然失败时，可换用结构化输出更稳定的模型后重试未完成题目；无需因此降低目标难度。", Retryable: &retryable}
	switch {
	case strings.Contains(text, "truncatederror"), strings.Contains(text, "max_tokens"), strings.Contains(text, "output truncated"), strings.Contains(text, "stop_reason=length"):
		failure.Code, failure.Category = "model_output_truncated", "模型回复不完整"
		failure.Message = "模型回复达到长度上限，没有返回完整内容"
		failure.Reason = "输出在结束前被截断，系统无法得到完整的题目结构。"
		failure.Suggestion = "增加模型可用输出长度，或精简本题需求后重试；这项错误不表示目标难度不合法。"
	case strings.Contains(text, "problem_definition") && strings.Contains(text, "expected scalar"):
		failure.Reason = "题意说明应是一段文字，模型却返回了其他数据结构。"
	case strings.Contains(text, "unknown or non-canonical json key"), strings.Contains(text, "unknown field"):
		failure.Reason = "模型使用了程序不认识的字段名，无法按约定读取题意、输入格式或评审内容。"
	case strings.Contains(text, "invalid character"), strings.Contains(text, "unexpected eof"), strings.Contains(text, "unexpected end"), strings.Contains(text, "trailing json"):
		failure.Reason = "模型回复不是完整、合法的 JSON，可能含有未转义的引号、多余文字或缺失的标点。"
	case strings.Contains(text, "expected scalar"), strings.Contains(text, "cannot unmarshal"), strings.Contains(text, "expected object"):
		failure.Reason = "模型把应为文字、数字或对象的字段写成了另一种类型，程序无法读取。"
	}
	return failure
}
