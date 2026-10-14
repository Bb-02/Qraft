package qualitygate

import (
	"bytes"
	"strings"
	"testing"
)

func TestTrainingPolicyReportsDifficultyWithoutBlockingCorrectness(t *testing.T) {
	evidence := passingEvidenceV1(t, false)
	evidence.Policy = PolicyProblemSetTraining
	evidence.Reviewer.Assessment.Dimensions.DifficultyCalibration.Score = ScoreV1(2)
	evidence.Reviewer.Assessment.Dimensions.DifficultyCalibration.Notes = "预估更容易，建议作为前段基础题。"
	evidence.Reviewer.Assessment.EstimatedDifficulty = ScoreV1(400)
	evidence.Reviewer.Assessment.DifficultyExplanation = "只需要直接模拟，没有额外观察。"
	evidence.Reviewer.Assessment.Blockers = []ReviewBlockerV1{fixtureBlockerV1("review.difficulty_mismatch", "cas://review")}
	evidence.HiddenRegression.Status = GateStatusNotConfigured
	bindReviewAssessmentV1(t, &evidence)
	audit, err := RecomputeVerdictV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Decision != DecisionPass || audit.Policy != PolicyProblemSetTraining || len(audit.BlockingIssues) != 0 || len(audit.Warnings) != 2 {
		t.Fatalf("training audit=%+v", audit)
	}
	if *evidence.Reviewer.Assessment.Dimensions.DifficultyCalibration.Score != 2 || *evidence.Reviewer.Assessment.EstimatedDifficulty != 400 {
		t.Fatal("advisory policy rewrote the model's real scores")
	}
	evidence.Reviewer.Assessment.Dimensions.Correctness.Score = ScoreV1(6)
	bindReviewAssessmentV1(t, &evidence)
	audit, err = RecomputeVerdictV1(evidence)
	if err != nil || audit.Decision != DecisionQuarantine || !hasIssueCodeV1(audit, "review.score_below_threshold.correctness") {
		t.Fatalf("a real correctness defect was bypassed: %+v err=%v", audit, err)
	}
}

func TestTrainingPolicyOnlyAllowsUnconfiguredHiddenRegression(t *testing.T) {
	evidence := passingEvidenceV1(t, true)
	evidence.HiddenRegression.Status = GateStatusNotConfigured
	if _, err := RecomputeVerdictV1(evidence); err == nil {
		t.Fatal("strict policy accepted an unexecuted hidden gate")
	}
	evidence.Policy = PolicyProblemSetTraining
	if audit, err := RecomputeVerdictV1(evidence); err != nil || audit.Decision != DecisionPass {
		t.Fatalf("training optional hidden gate audit=%+v err=%v", audit, err)
	}
	evidence.SpecLint.Status = GateStatusNotConfigured
	if _, err := RecomputeVerdictV1(evidence); err == nil {
		t.Fatal("an obligatory gate accepted not_configured")
	}
	evidence.SpecLint.Status = GateStatusPass
	evidence.HiddenRegression.Status = GateStatusCheckFailed
	if audit, err := RecomputeVerdictV1(evidence); err != nil || audit.Decision != DecisionQuarantine || !hasIssueCodeV1(audit, "hidden_regression.check_failed") {
		t.Fatalf("configured hidden failure was skipped: %+v err=%v", audit, err)
	}
}

func TestTrainingPolicyRuleIsSeparateAndLegacyFieldsRemainOmitted(t *testing.T) {
	strictRule, strictSHA, err := CanonicalRuleV1()
	if err != nil {
		t.Fatal(err)
	}
	trainingRule, trainingSHA, err := CanonicalRuleForPolicyV1(PolicyProblemSetTraining)
	if err != nil || strictSHA == trainingSHA || !bytes.Contains(trainingRule, []byte(`"difficulty_calibration_advisory":true`)) {
		t.Fatalf("training rule identity is not independent: %s err=%v", trainingRule, err)
	}
	if bytes.Contains(strictRule, []byte("policy")) || bytes.Contains(strictRule, []byte("unconfigured_hidden_optional")) {
		t.Fatalf("strict descriptor gained optional training fields: %s", strictRule)
	}
	evidence := passingEvidenceV1(t, true)
	input, _, err := CanonicalInputV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := RecomputeVerdictV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	auditBytes, _, err := CanonicalAuditV1(audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"policy"`, `"estimated_difficulty"`, `"difficulty_explanation"`, `"warnings"`} {
		if bytes.Contains(input, []byte(field)) || bytes.Contains(auditBytes, []byte(field)) {
			t.Fatalf("legacy canonical bytes gained %s", field)
		}
	}
	if _, _, err := CanonicalRuleForPolicyV1("untrusted_override"); err == nil {
		t.Fatal("an unknown policy was accepted")
	}
}

func TestTrainingReviewOptionalEstimateCannotDiscardCoreReview(t *testing.T) {
	for _, field := range []string{
		``,
		`,"estimated_difficulty":"easy","difficulty_explanation":12`,
		`,"estimated_difficulty":0`,
		`,"estimated_difficulty":10001`,
	} {
		raw := strings.TrimSuffix(validReviewJSONV1, "}") + field + "}"
		review, err := DecodeTrainingReviewAssessmentV1([]byte(raw))
		if err != nil || review.EstimatedDifficulty != nil || !strings.Contains(review.DifficultyExplanation, "预估未完成") || *review.Dimensions.Correctness.Score != 8 {
			t.Fatalf("optional estimate discarded valid review: %+v err=%v", review, err)
		}
	}
	raw := strings.TrimSuffix(validReviewJSONV1, "}") + `,"estimated_difficulty":125,"difficulty_explanation":"基础计算"}`
	review, err := DecodeTrainingReviewAssessmentV1([]byte(raw))
	if err != nil || review.EstimatedDifficulty == nil || *review.EstimatedDifficulty != 125 || review.DifficultyExplanation != "基础计算" {
		t.Fatalf("valid non-rounded estimate was changed: %+v err=%v", review, err)
	}
	for _, raw := range []string{
		strings.Replace(validReviewJSONV1, `"score":7`, `"score":7,"score":8`, 1),
		strings.Replace(validReviewJSONV1, `"approved":true`, `"approved":true,"unexpected":1`, 1),
		strings.Replace(validReviewJSONV1, `"correctness":{"score":8,"notes":"correct"},`, "", 1),
	} {
		if _, err := DecodeTrainingReviewAssessmentV1([]byte(raw)); err == nil {
			t.Fatal("training estimate normalization relaxed the core strict review contract")
		}
	}
}
