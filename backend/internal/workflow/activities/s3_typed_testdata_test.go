package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestEnrichS3TestDataParamsIncludesTypedContract(t *testing.T) {
	params := domain.ProblemGenParams{CustomPrompt: "operator preference"}
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	plan.FailureModes = []domain.AuthoringFailureModeV1{{ID: "overflow", Description: "overflow", WitnessIntent: "max"}}
	got, err := enrichS3TestDataParamsV1(params, spec, plan)
	if err != nil {
		t.Fatalf("enrichS3TestDataParamsV1: %v", err)
	}
	for _, want := range []string{"operator preference", "SERVER-AUTHORED TYPED TEST-DATA CONTRACT", "semantic_spec", "minimum values", "overflow", "derived_case_plan", "derived_case_plan_sha256", "allowed_boundary:a-element-max", "range_edge:a:element_range:max", "ONE complete input case to hit ALL boundary_refs together"} {
		if !strings.Contains(got.CustomPrompt, want) {
			t.Fatalf("typed test-data context missing %q: %s", want, got.CustomPrompt)
		}
	}
}
