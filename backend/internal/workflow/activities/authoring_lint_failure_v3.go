package activities

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
)

const maxAuthoringLintBlockingIssuesV3 = 16

// The explanation is derived exclusively from the server's lint report. The
// complete original report remains in the immutable authoring bundle.
func authoringLintFailureV3(report speccontract.LintReportV1) *domain.GenerationFailure {
	encoded, _ := json.Marshal(report)
	technical := []rune(string(encoded))
	if len(technical) > 6000 {
		technical = technical[:6000]
	}
	issues := make([]string, 0, maxAuthoringLintBlockingIssuesV3)
	seen := map[string]bool{}
	errors := 0
	for _, issue := range report.Issues {
		if issue.Severity == speccontract.LintSeverityAdvisory {
			continue
		}
		errors++
		explanation := authoringLintIssueExplanationV3(issue) + "（" + issue.Path + "）"
		if !seen[explanation] && len(issues) < maxAuthoringLintBlockingIssuesV3 {
			seen[explanation] = true
			issues = append(issues, explanation)
		}
	}
	retryable := false
	reason := fmt.Sprintf("题意、输入格式或约束有 %d 项结构检查未通过。", errors)
	if errors > len(issues) {
		reason += fmt.Sprintf("下方展示前 %d 项，其余见技术详情；完整报告保存在题目设计的原始检查记录中。", len(issues))
	}
	return &domain.GenerationFailure{
		Code: "spec_lint_failed", Category: "题意与约束检查未通过", Stage: "设计题意与约束",
		Message: "题目设计存在不一致，需要修正后重新生成", Reason: reason,
		Suggestion:  "查看具体未通过项，重新生成或修复题目设计。缺少参考程序和测试数据的候选尚不能人工批准；这项错误不表示目标难度不合法。",
		BlockedGate: "spec_lint", BlockingIssues: issues, TechnicalDetails: string(technical), Retryable: &retryable,
	}
}

func authoringLintIssueExplanationV3(issue speccontract.LintIssueV1) string {
	message := issue.Message
	switch issue.Code {
	case speccontract.CodeSpecSectionMissing:
		return "题意、输入、输出或约束的必填说明缺失"
	case speccontract.CodeSpecInputGrammarInvalid:
		switch {
		case strings.Contains(message, "profile"):
			return "输入或输出格式必须使用规定的 token-lines 格式版本"
		case strings.Contains(message, "literal repeat=1"):
			return "普通标量或整行序列只能读取一次，重复次数必须写成整数 1；逐行序列应使用 element_per_repeat"
		case strings.Contains(message, "bounded interval"):
			return "读取次数无法由已有整数范围确定，需要补全计数变量的范围或表达式"
		case strings.Contains(message, "before it is read"):
			return "输入格式先使用了尚未读取的计数变量，需要调整读取顺序"
		case strings.Contains(message, "negative"):
			return "读取次数的取值范围包含负数"
		case strings.Contains(message, "escape"):
			return "输入的元素数量可能超出序列声明的长度范围"
		case strings.Contains(message, "wrong scope"):
			return "输入或输出格式引用了作用域不匹配的变量"
		case strings.Contains(message, "duplicated"):
			return "输入或输出格式重复声明了同一变量或行标识"
		case strings.Contains(message, "scalar field"):
			return "标量字段必须对应标量变量，并且不能填写元素数量"
		case strings.Contains(message, "sequence field"):
			return "序列字段必须对应序列变量，并提供合法元素数量表达式"
		case strings.Contains(message, "element_per_repeat"):
			return "逐行元素字段必须对应序列变量，并且不能同时填写 count"
		default:
			return "输入或输出格式的字段、行说明或读取模式不符合规定"
		}
	case speccontract.CodeSpecObjectiveInvalid:
		if strings.Contains(message, "kind") {
			return "计算目标类型只能使用 compute、count、decision、minimize、maximize 或 construct"
		}
		return "计算目标缺少说明，或没有引用实际输出变量"
	case speccontract.CodeSpecSymbolInvalid:
		switch {
		case strings.Contains(message, "boundary policy"):
			return "变量边界策略只能使用 zero_one_required、explicit 或 not_applicable，且零和一策略仅适用于整数"
		case strings.Contains(message, "symbol type"):
			return "变量类型使用了程序不支持的名称，需要使用契约中的类型"
		case strings.Contains(message, "scope"):
			return "变量作用域只能使用 input、output 或 derived，并与输入格式对应"
		case strings.Contains(message, "ASCII"):
			return "变量名必须是以英文字母开头的固定英文标识符"
		default:
			return "变量缺少必要的用途或定义说明"
		}
	case speccontract.CodeSpecSymbolDuplicate:
		return "题目重复定义了同名变量"
	case speccontract.CodeSpecSymbolUndefined:
		return "结构中引用了没有定义的变量或边界标识"
	case speccontract.CodeSpecSymbolUnreferenced:
		return "已定义或使用的变量没有完整列入对应输入、输出或约束引用"
	case speccontract.CodeSpecConstraintMissing:
		return "输入变量缺少必要的数值、长度或元素范围，计数变量也必须声明整数范围"
	case speccontract.CodeSpecRangeInvalid:
		return "约束上下界缺失、顺序错误、数量为负数或相互矛盾；上下界应为明确整数"
	case speccontract.CodeSpecRangeTypeMismatch:
		return "约束类型必须使用 integer_range、length_range、cardinality 或 element_range，并匹配变量类型"
	case speccontract.CodeSpecRelationInvalid:
		switch {
		case strings.Contains(message, "length expression"):
			return "长度表达式应直接填写序列变量 symbol，不能嵌套 args 或 value"
		case strings.Contains(message, "cardinality expression"):
			return "集合大小表达式应直接填写集合变量 symbol，不能嵌套 args 或 value"
		case strings.Contains(message, "exactly two"):
			return "加减乘除表达式必须恰好有两个参数，且不能同时带 symbol 或 value"
		case strings.Contains(message, "arithmetic symbol"):
			return "算术表达式只能直接引用整数变量，序列需要先取长度或集合大小"
		case strings.Contains(message, "division"), strings.Contains(message, "denominator"):
			return "整除表达式的除数必须有明确范围，并排除零"
		case strings.Contains(message, "conflict"), strings.Contains(message, "impossible"):
			return "变量之间的关系与声明的范围或常量事实矛盾"
		case strings.Contains(message, "symbol expression"):
			return "变量表达式只能填写 symbol，不能同时带 value 或 args"
		case strings.Contains(message, "integer expression"):
			return "整数常量表达式只能填写 value，不能同时带 symbol 或 args"
		default:
			return "变量关系表达式的操作类型、标识或引用不符合规定"
		}
	case speccontract.CodeSpecTopologyUnsupported:
		return "数据结构类型或拓扑属性使用了未支持的值，需要使用契约中的枚举"
	case speccontract.CodeSpecTopologyConflict:
		if strings.Contains(message, "kind none") {
			return "无拓扑结构时，方向、环、连通性、动态性、自环和重边都必须写为 not_applicable"
		}
		return "数据结构类型与方向、环、连通性、计数变量或集合大小关系不一致"
	case speccontract.CodeSpecBoundaryFactMissing:
		switch {
		case strings.Contains(message, "at least five high-risk boundary facts"):
			return "题目设计缺少至少五项独立、有意义的高风险边界事实"
		case strings.HasPrefix(message, "symbol ") && strings.HasSuffix(message, " requires at least one explicit boundary fact"):
			symbol := strings.TrimSuffix(strings.TrimPrefix(message, "symbol "), " requires at least one explicit boundary fact")
			return "变量 " + symbol + " 至少需要一项明确的边界事实"
		case strings.HasPrefix(message, "symbol ") && strings.HasSuffix(message, " requires an explicit 0 boundary fact"):
			symbol := strings.TrimSuffix(strings.TrimPrefix(message, "symbol "), " requires an explicit 0 boundary fact")
			return "变量 " + symbol + " 缺少取值为零的边界事实，需要说明零是否允许"
		case strings.HasPrefix(message, "symbol ") && strings.HasSuffix(message, " requires an explicit 1 boundary fact"):
			symbol := strings.TrimSuffix(strings.TrimPrefix(message, "symbol "), " requires an explicit 1 boundary fact")
			return "变量 " + symbol + " 缺少取值为一的边界事实，需要说明一是否允许"
		case strings.Contains(message, "boundary id is required"):
			return "边界事实缺少标识"
		case strings.Contains(message, "boundary meaning is required"):
			return "边界事实缺少用途或语义说明"
		default:
			return "必要的边界事实尚未完整声明，请核对对应变量与字段"
		}
	case speccontract.CodeSpecBoundaryFactConflict:
		switch {
		case strings.Contains(message, "measure"):
			return "边界衡量方式与变量类型不符：整数用 value，整数序列用 length 或 element_value"
		case strings.Contains(message, "allowance"):
			return "边界是否允许与声明的上下界矛盾"
		case strings.Contains(message, "forbidden"):
			return "变量声明无需边界事实，却又为它添加了边界"
		default:
			return "边界标识或同一变量的同值边界被重复声明"
		}
	case speccontract.CodeSpecNoSolutionSemanticsMissing:
		return "无解规则只能使用 impossible、token 或 empty_output；只有 token 规则填写无解输出标记"
	case speccontract.CodeSpecMultipleSolutionSemanticsMissing:
		return "答案规则缺失或冲突：unique 不带额外接受规则，any_valid 需要专用检查器，canonical 需要确定的规范化规则"
	case speccontract.CodeSpecJudgeContractInvalid:
		if strings.Contains(message, "floating-point") {
			return "当前题目语义版本不支持浮点误差规则，不能填写 floating_point_error_rule"
		}
		return "判题方式与比较规则或专用检查器不一致；精确比较使用 trim-trailing-space-lf-v1"
	case speccontract.CodeAuthoringKnowledgePointMissing:
		return "设计计划缺少请求的知识点，或重复定义了知识点；slug 必须与请求的标签完全相同"
	case speccontract.CodeAuthoringRequiredFieldMissing:
		return "设计计划缺少解法、复杂度、教学目标、错误解法或测试意图等必填内容"
	case speccontract.CodeSpecSchemaUnsupported, speccontract.CodeSpecBriefHashMismatch, speccontract.CodeAuthoringSchemaUnsupported, speccontract.CodeAuthoringBriefHashMismatch, speccontract.CodeAuthoringSpecHashMismatch:
		return "题目设计与服务器保存的任务或版本绑定不一致"
	default:
		return "题目设计未通过结构一致性检查，请核对对应字段"
	}
}
