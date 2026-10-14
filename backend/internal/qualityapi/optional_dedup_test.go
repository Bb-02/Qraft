package qualityapi

import (
	"testing"

	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
)

func TestQualityReportAllowsOnlyV2UnconfiguredDedup(t *testing.T) {
	for _, policy := range []string{"", qualitygate.PolicyProblemSetTraining, qualitygate.PolicyProblemSetTrainingV2} {
		report := canonicalReportFixture(t)
		report.Policy = policy
		for index := range report.Gates {
			if report.Gates[index].Name == qualitygate.GateDedup {
				report.Gates[index].Status = qualitygate.GateStatusNotConfigured
			}
		}
		err := report.Validate()
		if (err == nil) != (policy == qualitygate.PolicyProblemSetTrainingV2) {
			t.Fatalf("policy %q optional dedup validation=%v", policy, err)
		}
	}
}
