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
	if copy.Code == "generation_failed" || copy.Code == "model_output_invalid" || copy.Code == "model_output_truncated" || copy.Code == "verification_unavailable" || copy.Code == "verification_failed" || copy.Code == "quality_review_required" {
		if explained := modelOutputFailure(copy.TechnicalDetails); explained != nil {
			explained.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return explained
		}
		if internal := internalDataContractFailure(copy.TechnicalDetails); internal != nil {
			internal.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return internal
		}
		if coverage := testCoverageFailure(copy.TechnicalDetails); coverage != nil {
			coverage.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return coverage
		}
		if runtime := testDataExecutionFailure(copy.TechnicalDetails); runtime != nil {
			runtime.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return runtime
		}
		if presentation := statementPresentationFailure(copy.TechnicalDetails); presentation != nil {
			presentation.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return presentation
		}
		if unsupported := unsupportedInputFailure(copy.TechnicalDetails); unsupported != nil {
			unsupported.TechnicalDetails = boundedFailureText(copy.TechnicalDetails, 6000)
			return unsupported
		}
	}
	return &copy
}

func unsupportedInputFailure(diagnostic string) *domain.GenerationFailure {
	text := strings.ToLower(diagnostic)
	if strings.Contains(text, "unsupported oracle judge contract") || strings.Contains(text, "a special checker cannot use ordinary output comparison") {
		retryable := false
		return &domain.GenerationFailure{Code: "unsupported_judge_contract", Category: "流程能力限制", Stage: "独立解法验证", Message: "当前验证流程不支持这道题的判题方式",
			Reason:     "该题的判题规则需要尚未支持的比较方式或专用检查器，不能用普通输出比较代替。",
			Suggestion: "核对题目的实际判题规则；需要专用检查器或其他比较方式时，应先补齐对应验证能力，再重试未完成题目。", Retryable: &retryable}
	}
	declaredInputSupport := strings.HasPrefix(text, "supportedinput") || strings.Contains(text, " supportedinput") || strings.HasPrefix(text, "supported input") || strings.Contains(text, " supported input") || strings.Contains(text, "supported_input_symbol_types")
	if (strings.Contains(text, "current pipeline") || strings.Contains(text, "current authoring pipeline") || declaredInputSupport) &&
		(strings.Contains(text, "unsupported") || strings.Contains(text, "only support") || strings.Contains(text, "supports only") || strings.Contains(text, "not support") || strings.Contains(text, "limited to") ||
			(strings.Contains(text, "restrict") && strings.Contains(text, "to only")) || strings.Contains(text, "not available as an input symbol")) {
		retryable := false
		return &domain.GenerationFailure{Code: "unsupported_input_format", Category: "流程能力限制", Stage: "设计题意与约束", Message: "这次题目设计因输入格式支持范围被拒绝",
			Reason:     "该次任务的出题流程或模型将所需输入格式判断为超出当时支持范围；这项结果不表示目标难度不合法或题目质量差。",
			Suggestion: "核对这次任务采用的输入格式支持范围。流程能力已经更新时，重试未完成题目让其使用新版本；需要保留原输入表示时，应先补齐对应解析与数据验证能力。", Retryable: &retryable}
	}
	if !strings.Contains(text, "unsupported parser type") && !strings.Contains(text, "unsupported input grammar profile") && !strings.Contains(text, "token string requires non-empty ascii") && !strings.Contains(text, "unsupported whitespace u+") {
		return nil
	}
	retryable := false
	reason := "当前样例与测试数据流程还不支持这道题使用的输入表示，无法完整校验数据。"
	if strings.Contains(text, "token string requires non-empty ascii") {
		reason = "当前字符串输入格式只支持非空、无空白的 ASCII 文本，尚不支持空字符串、含空格整行文本或 Unicode 文本。"
	} else if strings.Contains(text, "unsupported whitespace u+") {
		reason = "输入含有当前格式不支持的空白字符，无法按约定读取。"
	}
	return &domain.GenerationFailure{Code: "unsupported_input_format", Category: "流程能力限制", Stage: "样例与测试数据检查", Message: "当前流程无法校验这道题的输入格式", Reason: reason,
		Suggestion: "核对输入表示是否在当前流程的支持范围内；需要原格式时，应先补齐对应解析与数据验证能力。这项错误不表示目标难度太低或题目质量差。", Retryable: &retryable}
}

func modelOutputFailure(diagnostic string) *domain.GenerationFailure {
	text := strings.ToLower(diagnostic)
	stage := ""
	switch {
	case strings.Contains(text, "authoringplanformaterror"), strings.Contains(text, "authoringplantruncatederror"), strings.Contains(text, "parsing authoring-plan response"):
		stage = "设计题意与约束"
	case strings.Contains(text, "reviewerformaterror"), strings.Contains(text, "reviewertruncatederror"), strings.Contains(text, "decode strict reviewer"), strings.Contains(text, "decode review assessment"):
		stage = "评审题目"
	case strings.Contains(text, "testdataformaterror"), strings.Contains(text, "testdatatruncatederror"), strings.Contains(text, "parsing test data response"), strings.Contains(text, "parsing testdata response"), strings.Contains(text, "test data response was truncated"), strings.Contains(text, "test-data response was truncated"), strings.Contains(text, "test-data json format is still invalid after one correction"):
		stage = "生成测试数据"
	case strings.Contains(text, "parse statement narrative response"):
		stage = "编写题面"
	}
	if stage == "" {
		return nil
	}
	retryable := false
	failure := &domain.GenerationFailure{Code: "model_output_invalid", Category: "模型回复格式", Stage: stage,
		Message: "模型回复的格式不符合要求，系统无法读取", Reason: "模型返回的字段或数据结构与出题流程要求不一致。",
		Suggestion: "出题流程会提供完整格式要求，并尝试一次格式纠正。仍然失败时，可换用结构化输出更稳定的模型后重试未完成题目；无需因此降低目标难度。", Retryable: &retryable}
	if stage == "生成测试数据" {
		failure.Message = "测试数据模型回复的格式不符合要求，系统无法读取"
		failure.Reason = "测试数据模型没有返回约定的完整 JSON 对象，测试用例与生成器尚未形成有效数据。"
		failure.Suggestion = "检查测试数据模型的结构化输出能力。格式纠正后仍失败时，换用更稳定的模型或精简输出后重试未完成题目；无需修改目标难度。"
	}
	if stage == "编写题面" {
		failure.Message = "题面模型的回复格式不符合要求，系统无法读取"
		failure.Reason = "题面模型没有返回约定的标题与叙述结构，题面尚未完成编写。"
		failure.Suggestion = "检查题面模型的结构化输出能力，调整模型或输出设置后重试未完成题目；无需修改目标难度。"
	}
	switch {
	case strings.Contains(text, "truncatederror"), strings.Contains(text, "output truncated"), strings.Contains(text, "response was truncated"), strings.Contains(text, "stop_reason=length"):
		failure.Code, failure.Category = "model_output_truncated", "模型回复不完整"
		failure.Message = "模型回复达到长度上限，没有返回完整内容"
		failure.Reason = "输出在结束前被截断，系统无法得到完整的题目结构。"
		failure.Suggestion = "增加模型可用输出长度，或精简本题需求后重试；这项错误不表示目标难度不合法。"
		if stage == "生成测试数据" {
			failure.Message = "测试数据模型回复达到长度上限，没有返回完整内容"
			failure.Reason = "输出在结束前被截断，系统无法得到完整的测试数据与生成器结构。"
		}
	case strings.Contains(text, "problem_definition") && strings.Contains(text, "expected scalar"):
		failure.Reason = "题意说明应是一段文字，模型却返回了其他数据结构。"
	case strings.Contains(text, "unknown or non-canonical json key"), strings.Contains(text, "unknown field"):
		failure.Reason = "模型使用了程序不认识的字段名，无法按约定读取题意、输入格式或评审内容。"
		if stage == "生成测试数据" {
			failure.Reason = "模型使用了程序不认识的字段名，无法按约定读取测试用例或生成器。"
		}
	case strings.Contains(text, "invalid character"), strings.Contains(text, "unexpected eof"), strings.Contains(text, "unexpected end"), strings.Contains(text, "trailing json"), strings.Contains(text, "not a complete json object"):
		failure.Reason = "模型回复不是完整、合法的 JSON，可能含有未转义的引号、多余文字或缺失的标点。"
	case strings.Contains(text, "expected scalar"), strings.Contains(text, "cannot unmarshal"), strings.Contains(text, "expected object"):
		failure.Reason = "模型把应为文字、数字或对象的字段写成了另一种类型，程序无法读取。"
	}
	return failure
}
