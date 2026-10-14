package qualitygate

import (
	"strings"
	"testing"
)

func TestTrainingV2DedupNotConfiguredIsAdvisoryWithoutFakeNeighbors(t *testing.T) {
	evidence := passingEvidenceV1(t, true)
	evidence.Policy = PolicyProblemSetTrainingV2
	evidence.Dedup.Status, evidence.Dedup.NeighborCount = GateStatusNotConfigured, nil
	evidence.HiddenRegression.Status = GateStatusNotConfigured
	audit, err := RecomputeVerdictV1(evidence)
	if err != nil || audit.Decision != DecisionPass || len(audit.Warnings) != 1 || audit.Warnings[0].Code != "dedup.not_configured" {
		t.Fatalf("optional dedup audit=%+v err=%v", audit, err)
	}
	if !strings.Contains(audit.Warnings[0].Witness.Assertion, "未进行相似题去重") {
		t.Fatal("unexecuted dedup was not clearly reported")
	}
	pre := PreReviewEvidenceFromS3V1(evidence)
	if shouldReview, err := ShouldInvokeReviewerV1(pre); err != nil || !shouldReview {
		t.Fatalf("optional dedup blocked the actual correctness reviewer: %v %v", shouldReview, err)
	}
	for _, policy := range []string{"", PolicyProblemSetTraining} {
		evidence.Policy = policy
		if _, err := RecomputeVerdictV1(evidence); err == nil {
			t.Fatalf("policy %q accepted an unconfigured dedup gate", policy)
		}
	}
	evidence.Policy = PolicyProblemSetTrainingV2
	evidence.Dedup.NeighborCount = ScoreV1(0)
	if _, err := RecomputeVerdictV1(evidence); err == nil {
		t.Fatal("unexecuted dedup claimed zero matching neighbors")
	}
	evidence.Dedup.Status, evidence.Dedup.NeighborCount = GateStatusCheckFailed, nil
	audit, err = RecomputeVerdictV1(evidence)
	if err != nil || audit.Decision != DecisionQuarantine || !hasIssueCodeV1(audit, "dedup.check_failed") {
		t.Fatalf("a configured dedup failure was skipped: %+v err=%v", audit, err)
	}
}

func TestTrainingV2RuleKeepsOriginalTrainingDescriptorImmutable(t *testing.T) {
	const originalTrainingRule = `{"rule_version":"algoforge.s3-quality-gate.v1","gate_order":["spec_lint","sample_output_binding","oracle_differential","sanitizer","boundary_coverage","test_manifest","reviewer_schema_verdict","dedup","hidden_regression"],"review_score_minimum":0,"review_score_maximum":10,"review_passing_score":7,"reviewer_approved_is_advisory":true,"check_failed_decision":"quarantine","hidden_exposure_decision":"quarantine","deterministic_blockers_take_priority":true,"max_repair_rounds":3,"policy":"problem_set_training","difficulty_calibration_advisory":true,"unconfigured_hidden_optional":true}`
	legacyBytes, legacySHA, err := CanonicalRuleForPolicyV1(PolicyProblemSetTraining)
	if err != nil || string(legacyBytes) != originalTrainingRule {
		t.Fatalf("saved training rule identity changed: %s err=%v", legacyBytes, err)
	}
	v2Bytes, v2SHA, err := CanonicalRuleForPolicyV1(PolicyProblemSetTrainingV2)
	if err != nil || v2SHA == legacySHA || !strings.Contains(string(v2Bytes), `"unconfigured_dedup_optional":true`) {
		t.Fatalf("v2 did not independently record its dedup exception: %s err=%v", v2Bytes, err)
	}
}
