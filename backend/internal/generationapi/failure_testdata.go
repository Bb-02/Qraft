package generationapi

import (
	"regexp"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

var sandboxRequestLimitPattern = regexp.MustCompile(`(?i)\bsandbox HTTP\s+(?:400|413)\s+\(invalid_request\):\s*(declared batch output budget exceeds \d+ bytes|combined inputs exceed \d+ bytes|input \d+ exceeds \d+ bytes|source exceeds \d+ bytes)`)
var generatorEmptyInputPattern = regexp.MustCompile(`(?i)\bgenerator case \d+ produced empty input\b`)

// An accepted request that produces no data differs from a request rejected by
// the runner's size limits. Neither means that the runner is disconnected.
func testDataExecutionFailure(diagnostic string) *domain.GenerationFailure {
	retryable := false
	if sandboxRequestLimitPattern.MatchString(diagnostic) {
		reason := "提交的程序或数据量超过执行服务允许的请求范围，服务已拒绝本次提交，尚未完成程序执行。"
		if strings.Contains(strings.ToLower(diagnostic), "declared batch output budget") {
			reason = "单批测试数量与每例输出额度合计超过执行服务的总量限制，服务拒绝了请求，尚未运行这一批程序。"
		}
		return &domain.GenerationFailure{Code: "verification_request_limit", Category: "执行请求超过服务限制", Stage: "提交程序执行请求", Message: "出题流程提交的执行请求超过服务限制", Reason: reason,
			Suggestion: "管理员需修正出题流程的执行批次划分、数据规模或输出额度，让请求落在服务限制内，再重试未完成题目；无需修改目标难度。", Retryable: &retryable}
	}
	if generatorEmptyInputPattern.MatchString(diagnostic) {
		return &domain.GenerationFailure{Code: "testdata_generator_empty_output", Category: "测试数据生成器输出为空", Stage: "生成测试数据", Message: "测试数据生成器没有输出有效输入",
			Reason:     "某个用例的生成器执行后返回了空白数据，系统无法取得完整输入，测试数据尚未生成成功。",
			Suggestion: "检查生成器是否对每个用例编号都输出完整输入，修复提前退出或遗漏的分支后重新生成此题。", Retryable: &retryable}
	}
	return nil
}
