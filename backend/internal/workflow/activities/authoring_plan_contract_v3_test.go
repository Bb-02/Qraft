package activities

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
)

func TestAuthoringPlanV3ContainsEnumsAndExecutableIntegerExample(t *testing.T) {
	system, err := authoringPlanSystemPromptV3()
	require.NoError(t, err)
	_, rawSchema, ok := strings.Cut(system, "Complete output JSON Schema:\n")
	require.True(t, ok)
	var schema struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(rawSchema), &schema))
	require.Equal(t, []string{domain.SemanticConstraintIntegerRange, domain.SemanticConstraintLengthRange, domain.SemanticConstraintCardinality, domain.SemanticConstraintElementRange}, schema.Definitions["SemanticConstraintV1"].Properties["kind"].Enum)
	require.Contains(t, schema.Definitions["SemanticBoundaryV1"].Properties["measure"].Enum, domain.SemanticBoundaryMeasureElementValue)
	require.NotContains(t, schema.Definitions["SemanticBoundaryV1"].Properties["measure"].Enum, domain.SemanticConstraintElementRange)
	require.Equal(t, []string{domain.SemanticBoundaryPolicyZeroOneRequired, domain.SemanticBoundaryPolicyExplicit, domain.SemanticBoundaryPolicyNotApplicable}, schema.Definitions["SemanticSymbolV1"].Properties["boundary_policy"].Enum)
	require.Equal(t, []string{domain.SemanticTopologyNone, domain.SemanticTopologyLinear, domain.SemanticTopologyCircular, domain.SemanticTopologyGraph, domain.SemanticTopologyTree, domain.SemanticTopologyGrid}, schema.Definitions["SemanticTopologyV1"].Properties["kind"].Enum)
	for _, definition := range []string{"SemanticObjectiveV1", "SemanticExpressionV1", "SemanticAnswerSemanticsV1", "SemanticGrammarFieldV1", "SemanticJudgeV1"} {
		found := false
		for _, field := range schema.Definitions[definition].Properties {
			found = found || len(field.Enum) > 0
		}
		require.True(t, found, "enum contract omitted %s", definition)
	}
	for _, rule := range []string{"time_limit_ms", "memory_limit_mb", "exactly two", "length(a)", "zero_one_required", "ALL to not_applicable", "floating_point_error_rule", "including grammar", "Never replace the user's task"} {
		require.Contains(t, system, rule)
	}
	output := authoringPlanDSLExampleV3()
	raw, err := marshalAuthoringPlanExampleV3(output)
	require.NoError(t, err)
	parsed, err := parseAuthoringPlanResponseV1(string(raw))
	require.NoError(t, err, "example must obey the strict model parser")
	brief := strings.Repeat("a", 64)
	parsed.SemanticSpec.SchemaVersion, parsed.SemanticSpec.BriefSHA256 = domain.SemanticSpecSchemaV1, brief
	parsed.AuthoringPlan.SchemaVersion, parsed.AuthoringPlan.BriefSHA256 = domain.AuthoringPlanSchemaV1, brief
	parsed.AuthoringPlan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(*parsed.SemanticSpec)
	report := speccontract.LintV1(speccontract.LintInputV1{SemanticSpec: *parsed.SemanticSpec, AuthoringPlan: *parsed.AuthoringPlan, ExpectedBriefSHA256: brief, ExpectedDifficulty: 400, RequiredKnowledgePoints: []string{"simulation"}})
	require.True(t, report.Passed, "synthetic example must pass real lint, issues=%+v", report.Issues)
	for _, sample := range parsed.SemanticSpec.SampleInputs {
		_, err := speccontract.ParseSampleInputV1(*parsed.SemanticSpec, sample)
		require.NoError(t, err, "sample must execute against the actual integer grammar")
	}
	require.Equal(t, 400, parsed.AuthoringPlan.TargetDifficulty)
	// Server-owned stamps were not present in the model's illustrative JSON.
	require.NotContains(t, string(raw), "schema_version")
	require.NotContains(t, string(raw), "brief_sha256")
}

func TestAuthoringPlanV3SemanticErrorsRemainRejectedWithReadableIssues(t *testing.T) {
	output := validAuthoringModelOutputV1()
	output.SemanticSpec.Constraints[0].Kind = "numeric_range"
	output.SemanticSpec.Objective.Kind = "sum"
	output.SemanticSpec.Symbols[0].BoundaryPolicy = "required"
	output.SemanticSpec.AnswerSemantics.FloatingPointErrorRule = "absolute epsilon"
	output.SemanticSpec.Topology.Kind = domain.SemanticTopologyNone
	output.SemanticSpec.Relations[0].Left = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Args: []domain.SemanticExpressionV1{{Kind: domain.SemanticExpressionSymbol, Symbol: "a"}}}
	output.AuthoringPlan.ConceptRoles = []domain.AuthoringConceptRoleV1{}
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, output))}}
	acts, store, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, validAuthoringActivityInput())
	require.False(t, result.LintPassed)
	require.Equal(t, AuthoringPlanDecisionRejected, result.Decision)
	require.Len(t, model.requests, 1, "semantic failures must not become implicit automatic rewrites")
	require.NotNil(t, result.Failure)
	require.Equal(t, "spec_lint_failed", result.Failure.Code)
	require.Equal(t, "题意与约束检查未通过", result.Failure.Category)
	require.Equal(t, "设计题意与约束", result.Failure.Stage)
	require.Equal(t, "spec_lint", result.Failure.BlockedGate)
	require.NotEmpty(t, result.Failure.BlockingIssues)
	require.LessOrEqual(t, len(result.Failure.BlockingIssues), maxAuthoringLintBlockingIssuesV3)
	issues := strings.Join(result.Failure.BlockingIssues, "\n")
	for _, explanation := range []string{"知识点", "浮点", "约束类型", "长度表达式", "边界策略", "无拓扑"} {
		require.Contains(t, issues, explanation)
	}
	require.NotContains(t, result.Failure.Suggestion, "降低")
	require.Contains(t, result.Failure.Suggestion, "尚不能人工批准")
	require.Equal(t, result.Failure.Message, result.RejectionReason)
	var bundle AuthoringPlanBundleV1
	require.NoError(t, json.Unmarshal(store.objects[result.BundleSHA256], &bundle))
	require.NotNil(t, bundle.LintReport)
	require.False(t, bundle.LintReport.Passed)
	require.Equal(t, result.LintErrorCount, len(bundle.LintReport.Issues))
	var originalReport speccontract.LintReportV1
	require.NoError(t, json.Unmarshal([]byte(result.Failure.TechnicalDetails), &originalReport))
	require.True(t, reflect.DeepEqual(*bundle.LintReport, originalReport), "original lint evidence must be retained")
}

func TestAuthoringLintFailureV3BoundsEvidenceAndDoesNotBlockAdvisoryDifficulty(t *testing.T) {
	report := speccontract.LintReportV1{Issues: []speccontract.LintIssueV1{{Severity: speccontract.LintSeverityAdvisory, Code: speccontract.CodeAuthoringDifficultyMismatch, Path: "authoring_plan.target_difficulty", Message: "target mismatch advisory"}}}
	for i := 0; i < 30; i++ {
		report.Issues = append(report.Issues, speccontract.LintIssueV1{Severity: speccontract.LintSeverityError, Code: speccontract.CodeSpecSymbolUndefined, Path: fmt.Sprintf("semantic_spec.constraints[%d]", i), Message: strings.Repeat("合成未定义变量", 300)})
	}
	failure := authoringLintFailureV3(report)
	require.Len(t, failure.BlockingIssues, maxAuthoringLintBlockingIssuesV3)
	require.NotContains(t, strings.Join(failure.BlockingIssues, "\n"), "target_difficulty")
	require.Contains(t, failure.Reason, "30")
	require.Contains(t, failure.Reason, "前 16 项")
	require.LessOrEqual(t, len([]rune(failure.TechnicalDetails)), 6000)
	require.True(t, utf8.ValidString(failure.TechnicalDetails))
	output := validAuthoringModelOutputV1()
	output.AuthoringPlan.TargetDifficulty = 400
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, output))}}
	acts, _, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, validAuthoringActivityInput())
	require.True(t, result.LintPassed)
	require.Positive(t, result.LintAdvisoryCount)
	require.Nil(t, result.Failure)
}
