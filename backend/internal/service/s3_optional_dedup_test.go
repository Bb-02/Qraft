package service

import (
	"strings"
	"testing"

	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
)

func TestS3ExportAllowsOptionalDedupOnlyUnderItsV2Rule(t *testing.T) {
	for _, policy := range []string{"", qualitygate.PolicyProblemSetTraining, qualitygate.PolicyProblemSetTrainingV2} {
		_, ruleSHA, err := qualitygate.CanonicalRuleForPolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{qualitygate.GateSpecLint, qualitygate.GateSampleOutputBinding, qualitygate.GateOracleDifferential, qualitygate.GateSanitizer, qualitygate.GateBoundaryCoverage, qualitygate.GateTestManifest, qualitygate.GateReviewerSchemaVerdict, qualitygate.GateDedup, qualitygate.GateHiddenRegression}
		audit := qualitygate.AuditV1{Policy: policy, RuleSHA256: ruleSHA, SubjectID: "synthetic-optional-dedup-export", SubjectRevision: strings.Repeat("a", 64), Decision: qualitygate.DecisionPass, DeterministicGatesPassed: true, ReviewerAdvisoryApproved: true, BlockingIssues: []qualitygate.BlockingIssueV1{}}
		for _, name := range names {
			status := qualitygate.GateStatusPass
			if name == qualitygate.GateDedup {
				status = qualitygate.GateStatusNotConfigured
			}
			audit.GateResults = append(audit.GateResults, qualitygate.GateResultV1{Gate: name, Status: status})
		}
		err = validateS3ExportAuditPassV1(audit, audit.SubjectID, audit.SubjectRevision)
		if (err == nil) != (policy == qualitygate.PolicyProblemSetTrainingV2) {
			t.Fatalf("policy %q optional dedup export validation=%v", policy, err)
		}
	}
}
