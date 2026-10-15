package generationapi

import (
	"regexp"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

var manifestBoundaryRefsContractPattern = regexp.MustCompile(`(?i)invalid TestManifest v2:\s*case "[^"\r\n]+" boundary_refs must be a present sorted unique array`)
var casePlanCanonicalNilPattern = regexp.MustCompile(`(?i)case "[^"\r\n]+" is not canonical SemanticSpec input:\s*<nil>`)

// These exact diagnostics identify internal representation checks. Merely
// mentioning test data or an activity does not prove missing question files.
func internalDataContractFailure(diagnostic string) *domain.GenerationFailure {
	retryable := false
	if manifestBoundaryRefsContractPattern.MatchString(diagnostic) {
		return &domain.GenerationFailure{Code: "test_manifest_boundary_refs_invalid", Category: "出题流程内部格式问题", Stage: "检查测试数据覆盖",
			Message:    "测试数据清单的内部格式未通过校验",
			Reason:     "流程没有按内部约定保存测试用例的边界覆盖列表，列表必须始终存在、排序且不含重复项。这项错误不能证明题目文件缺失或答案有误。",
			Suggestion: "管理员需修复测试清单的列表保存与校验流程，再重试未完成题目；无需因此修改题目需求或目标难度。", Retryable: &retryable}
	}
	text := strings.ToLower(diagnostic)
	if (strings.Contains(text, "materialize s3 case plan") || strings.Contains(text, "materializes3caseplanactivityv1")) && casePlanCanonicalNilPattern.MatchString(diagnostic) {
		return &domain.GenerationFailure{Code: "case_plan_input_normalization_failed", Category: "出题流程输入规范化问题", Stage: "整理验证用例",
			Message:    "验证用例的输入规范化流程未能完成",
			Reason:     "用例输入在转换成约定格式时被流程的一致性检查拦住，但记录中没有实际解析错误。这是输入规范化流程需要修复的问题，不能据此认定题目有误或测试文件缺失。",
			Suggestion: "管理员需修复用例输入的规范化与一致性检查，再重试未完成题目；无需因此修改模型、题目需求或目标难度。", Retryable: &retryable}
	}
	return nil
}
