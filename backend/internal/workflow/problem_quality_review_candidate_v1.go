package workflow

import (
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
)

func problemQualityHasCompleteAssetsV1(result *ProblemGenerationQualityResultV1) bool {
	return result != nil && result.Audit != nil && result.AuditArtifact != nil && result.AuthoringBundleArtifact != nil && result.StatementDraftArtifact != nil && result.FinalStatementArtifact != nil && result.MainProgramArtifact != nil && result.OracleProgramArtifact != nil && result.OracleReceiptArtifact != nil && result.TestManifestArtifact != nil
}

func problemQualityFailureV1(result *ProblemGenerationQualityResultV1) *domain.GenerationFailure {
	failure := &domain.GenerationFailure{Code: "quality_review_required", Message: "候选题需要人工审核", Suggestion: "查看具体检查结果，修复候选题或由管理员确认后继续入集。", BlockedGate: result.StoppedGate}
	labels := map[string]string{
		qualitygate.GateSpecLint:              "题意与约束描述尚未通过检查",
		qualitygate.GateSampleOutputBinding:   "样例与参考程序的输出尚未核对一致",
		qualitygate.GateOracleDifferential:    "主解与独立参考解的结果需要检查",
		qualitygate.GateSanitizer:             "参考程序的运行检查需要处理",
		qualitygate.GateBoundaryCoverage:      "测试数据的边界覆盖需要检查",
		qualitygate.GateTestManifest:          "测试数据清单尚未完成校验",
		qualitygate.GateReviewerSchemaVerdict: "模型评审提出了需要确认的问题",
		qualitygate.GateDedup:                 "重复内容或去重服务需要检查",
		qualitygate.GateHiddenRegression:      "隐藏验证服务或验证结果需要确认",
	}
	if label := labels[result.StoppedGate]; label != "" {
		failure.Message = label
	}
	if result.Audit != nil {
		for _, issue := range result.Audit.BlockingIssues {
			label := labels[issue.Gate]
			if label == "" {
				label = "自动检查需要确认"
			}
			failure.BlockingIssues = append(failure.BlockingIssues, label+"（"+issue.Code+"）")
			if issue.Gate == qualitygate.GateHiddenRegression && strings.Contains(issue.Code, "check_failed") {
				failure.Code, failure.Message = "verification_unavailable", "隐藏验证服务暂时不可用，未能完成检查"
				failure.Suggestion = "检查隐藏验证服务配置和连接，或检查已保存的候选题后人工确认。这项结果不代表题目已被证实有错。"
			}
		}
	}
	failure.TechnicalDetails = fmt.Sprintf("decision=%s; stopped_gate=%s", result.Decision, result.StoppedGate)
	return failure
}
