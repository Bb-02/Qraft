package activities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/stretchr/testify/require"
)

func TestS3OracleIdentityFailurePreservesMatchingOutputsAndPromotionReason(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	main := f.oracleInput.MainCandidate.ProviderResponseArtifact.LLMCallReceipt
	oracle := f.oracleInput.OracleCandidate.ProviderResponseArtifact.LLMCallReceipt
	oracle.ReturnedModel, oracle.EndpointID = main.ReturnedModel, main.EndpointID
	result := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	require.Equal(t, qualitygate.GateStatusBlocked, result.Gate.Status)
	require.Equal(t, "oracle.promotion_failed", result.Gate.Blockers[0].Code)
	require.Equal(t, result.MainRun.Outputs, result.OracleRun.Outputs)
	require.Len(t, result.MainRun.Outputs, 12)
	require.Nil(t, result.VerifiedProgramReceiptArtifact)
	require.NotNil(t, result.Failure)
	require.Equal(t, "oracle_identity_not_independent", result.Failure.Code)
	require.Equal(t, "验证来源不够独立", result.Failure.Category)
	require.Equal(t, "独立解法验证", result.Failure.Stage)
	require.Contains(t, result.Failure.Reason, "两路实际返回相同模型")
	require.NotContains(t, result.Failure.Suggestion, "人工")
	data, err := f.store.Get(context.Background(), *result.ReceiptArtifact)
	require.NoError(t, err)
	var receipt S3OracleGateReceiptV1
	require.NoError(t, decodeCanonicalSampleClosureJSONV1(data, &receipt))
	require.Contains(t, receipt.ValidationReason, "same_returned_model_and_endpoint")
	require.Equal(t, result.Failure.TechnicalDetails, receipt.ValidationReason)
	require.Empty(t, receipt.FirstMismatchCaseID)
	require.Nil(t, receipt.Promotion)
}

func TestOraclePromotionClassifiesActualIdentityAndDoesNotGuessOtherFailures(t *testing.T) {
	for _, tc := range []struct{ name, reason, text string }{
		{"same model", "same_returned_model", "实际返回相同模型"},
		{"same endpoint", "same_endpoint", "来自同一服务地址"},
		{"missing identity", "missing_returned_identity", "缺少实际返回模型或服务来源"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validOraclePromotionInputV1(t)
			switch tc.reason {
			case "same_returned_model":
				input.Candidate.ProviderResponseArtifact.LLMCallReceipt.ReturnedModel = input.MainProviderArtifact.LLMCallReceipt.ReturnedModel
			case "same_endpoint":
				input.Candidate.ProviderResponseArtifact.LLMCallReceipt.EndpointID = input.MainProviderArtifact.LLMCallReceipt.EndpointID
			default:
				input.Candidate.ProviderResponseArtifact.LLMCallReceipt.ReturnedModel = ""
			}
			_, err := ValidateAndPromoteOracleCandidateV1(input)
			var cause *oracleIdentityNotIndependentErrorV1
			require.ErrorAs(t, err, &cause)
			require.Equal(t, tc.reason, cause.Receipt.Reason)
			failure := oraclePromotionFailureV1(err)
			require.Contains(t, failure.Reason, tc.text)
			require.NotContains(t, failure.Suggestion, "人工")
		})
	}
	for _, mismatch := range []bool{false, true} {
		input := validOraclePromotionInputV1(t)
		input.Candidate.ProviderResponseArtifact.LLMCallReceipt.ReturnedModel = input.MainProviderArtifact.LLMCallReceipt.ReturnedModel
		if mismatch {
			input.Mismatches = []OracleDifferentialMismatchV1{{CaseID: "case-a"}}
		} else {
			input.Candidate.SourceBinding.CandidateSourceSHA256 = strings.Repeat("0", 64)
		}
		_, err := ValidateAndPromoteOracleCandidateV1(input)
		require.Error(t, err)
		require.Nil(t, oraclePromotionFailureV1(err), "an earlier binding or output mismatch must not be called an identity failure")
	}
	require.Nil(t, oraclePromotionFailureV1(errors.New("oracle candidate is correlated: same_returned_model")), "a diagnostic alone does not prove an identity decision")
	long := boundedOracleValidationReasonV1(errors.New(strings.Repeat("合成校验记录", 2000)))
	require.Len(t, []rune(long), 6000)
	require.True(t, utf8.ValidString(long))
	require.Empty(t, boundedOracleValidationReasonV1(nil))
}
