package activities

import (
	"errors"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
)

// This typed cause is emitted only after binding, compilation, case identity
// and differential checks passed. Classify the actual identity decision rather
// than guessing the promotion failure from a diagnostic string.
type oracleIdentityNotIndependentErrorV1 struct{ Receipt *OracleIndependenceReceipt }

func (e *oracleIdentityNotIndependentErrorV1) Error() string {
	if e.Receipt == nil {
		return "oracle identity independence receipt is missing"
	}
	return "oracle identity is not independent: " + e.Receipt.Reason
}

func boundedOracleValidationReasonV1(err error) string {
	if err == nil {
		return ""
	}
	text := []rune(strings.TrimSpace(err.Error()))
	if len(text) > 6000 {
		text = text[:6000]
	}
	return string(text)
}

func oraclePromotionFailureV1(err error) *domain.GenerationFailure {
	var identity *oracleIdentityNotIndependentErrorV1
	if !errors.As(err, &identity) {
		return nil
	}
	reason := "模型调用记录缺少实际返回模型或服务来源信息，无法证明两路验证来源相互独立。"
	if identity.Receipt != nil {
		switch identity.Receipt.Reason {
		case "same_returned_model_and_endpoint":
			reason = "两路实际返回相同模型，且服务地址也相同。即使本次测试输出一致，仍不满足当前跨模型独立验证规则。"
		case "same_returned_model":
			reason = "两路实际返回相同模型。即使服务地址不同、本次测试输出一致，仍不满足当前跨模型独立验证规则。"
		case "same_endpoint":
			reason = "两路模型虽然不同，但来自同一服务地址。当前规则要求实际返回模型与服务来源均不同，测试输出一致也不能代替这项要求。"
		}
	}
	retryable := false
	return &domain.GenerationFailure{Code: "oracle_identity_not_independent", Category: "验证来源不够独立", Stage: "独立解法验证", Message: "独立校验未满足模型与来源独立性要求", Reason: reason,
		Suggestion:  "在模型配置中为独立验证设置第二个实际不同的模型和服务地址，并确保调用记录可确认这些身份后，重试未完成题目。",
		BlockedGate: qualitygate.GateOracleDifferential, TechnicalDetails: boundedOracleValidationReasonV1(err), Retryable: &retryable}
}
