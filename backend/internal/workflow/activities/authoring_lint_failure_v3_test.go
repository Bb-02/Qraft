package activities

import (
	"testing"

	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
)

func TestAuthoringLintBoundaryExplanationMatchesObservedRule(t *testing.T) {
	for _, tc := range []struct{ message, expected, forbidden string }{
		{"symbol synthetic_value requires at least one explicit boundary fact", "变量 synthetic_value 至少需要一项明确的边界事实", "五项"},
		{"symbol synthetic_marker requires at least one explicit boundary fact", "变量 synthetic_marker 至少需要一项明确的边界事实", "零和一"},
		{"symbol synthetic_count requires an explicit 0 boundary fact", "变量 synthetic_count 缺少取值为零的边界事实", "取值为一"},
		{"symbol synthetic_count requires an explicit 1 boundary fact", "变量 synthetic_count 缺少取值为一的边界事实", "取值为零"},
		{"at least five high-risk boundary facts are required", "缺少至少五项", "zero_one_required"},
		{"boundary id is required", "缺少标识", "五项"},
		{"boundary meaning is required", "缺少用途或语义说明", "五项"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			issue := speccontract.LintIssueV1{Severity: speccontract.LintSeverityError, Code: speccontract.CodeSpecBoundaryFactMissing, Path: "semantic_spec.boundaries", Message: tc.message}
			text := authoringLintIssueExplanationV3(issue)
			require.Contains(t, text, tc.expected)
			require.NotContains(t, text, tc.forbidden)
			failure := authoringLintFailureV3(speccontract.LintReportV1{Issues: []speccontract.LintIssueV1{issue}})
			require.Contains(t, failure.BlockingIssues[0], tc.expected)
			require.Contains(t, failure.TechnicalDetails, tc.message)
		})
	}
}
