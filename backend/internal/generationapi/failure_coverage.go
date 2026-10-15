package generationapi

import (
	"regexp"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

var uncoveredBoundaryObligationPattern = regexp.MustCompile(`(?i)derived (?:allowed boundary|range-edge) obligation "[^"\r\n]+" is not covered`)
var uncoveredConstraintRegionPattern = regexp.MustCompile(`(?i)derived constraint-region obligation "[^"\r\n]+" has no single matching case`)
var coveragePreflightErrorPattern = regexp.MustCompile(`(?i)S3 test-data coverage preflight failed:|\btype:\s*S3TestDataCoverageError\b`)

// A missing coverage receipt can mean missing inputs or an incorrect coverage
// label. It is not evidence that files disappeared or an answer is wrong.
func testCoverageFailure(diagnostic string) *domain.GenerationFailure {
	retryable := false
	if coveragePreflightErrorPattern.MatchString(diagnostic) {
		return &domain.GenerationFailure{Code: "test_coverage_preflight_failed", Category: "测试覆盖不足", Stage: "检查测试数据覆盖",
			Message:    "测试数据未满足必测边界与组合覆盖要求",
			Reason:     "根据实际输入检查，生成的测试用例尚未覆盖全部必测边界或约束组合。覆盖预检未通过，尚未进入后续独立验算；这项检查不判断参考答案是否正确。",
			Suggestion: "生成流程需补齐缺少的测试用例并重新检查覆盖，再重试未完成题目。", Retryable: &retryable}
	}
	if uncoveredConstraintRegionPattern.MatchString(diagnostic) {
		return &domain.GenerationFailure{Code: "test_constraint_region_coverage_incomplete", Category: "测试约束组合覆盖不足", Stage: "检查测试数据覆盖",
			Message:    "某项约束组合尚未取得有效覆盖记录",
			Reason:     "某个计划测试区域尚未确认由同一个用例同时覆盖所需条件。可能缺少这种组合用例，也可能已有用例的区域标记没有被正确计入，需要核对实际输入与覆盖记录。",
			Suggestion: "生成流程需补齐缺少的组合用例，或核对并修正覆盖记录后，再重试未完成题目。", Retryable: &retryable}
	}
	if uncoveredBoundaryObligationPattern.MatchString(diagnostic) {
		return &domain.GenerationFailure{Code: "test_boundary_coverage_incomplete", Category: "测试边界覆盖不足", Stage: "检查测试数据覆盖",
			Message:    "所需边界情况尚未取得有效覆盖记录",
			Reason:     "测试计划要求的某个取值边界或值域端点尚未确认被覆盖。需要核对实际输入是否命中该边界，以及覆盖标记是否完整；这项结果本身不表示答案错误或文件缺失。",
			Suggestion: "生成流程需补齐缺少的边界用例，或核对并修正覆盖记录后，再重试未完成题目。", Retryable: &retryable}
	}
	return nil
}
