package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/temporal"
)

const (
	AuthoringPlanPromptIDV5             = "authoring_plan_input_coverage_v5"
	AuthoringPlanFormatRepairPromptIDV5 = "authoring_plan_input_coverage_format_repair_v5"
	AuthoringPlanIntentRepairPromptIDV5 = "authoring_plan_test_intent_repair_v5_1"
	authoringIntentRepairPromptIDV5     = AuthoringPlanIntentRepairPromptIDV5
	authoringIntentConflictErrorTypeV5  = "AuthoringPlanIntentConflictError"
)

const authoringJointCoverageInstructionsV5 = `
Each test_intent describes ONE complete input case. Its boundary_refs are an
AND: that same input must hit EVERY referenced boundary simultaneously.
Never combine alternative scalar values, different exact lengths of the same
sequence, or more distinct required element values than that sequence can hold.
The parser aggregates element_per_repeat fields across record_lines into one
sequence: its length is the TOTAL number of elements in the complete input.
For example, length=1 cannot coexist with two different element_value edges.
Split alternative edges into separate test_intents, retaining every required
boundary. A comparison between two inputs is not a single-case conjunction.
Preserve the frozen problem facts, target difficulty, and all declared bounds.
`

func authoringPlanSystemPromptV5() (string, error) {
	v4, err := authoringPlanSystemPromptV4()
	if err != nil {
		return "", err
	}
	prefix, schema, ok := strings.Cut(v4, "Complete output JSON Schema:\n")
	if !ok {
		return "", fmt.Errorf("authoring output schema marker is missing")
	}
	return prefix + authoringJointCoverageInstructionsV5 + "\nComplete output JSON Schema:\n" + schema, nil
}

func authoringPlanFormatRepairRequestV5(original *llm.Request, invalid string, formatErr error) *llm.Request {
	req := authoringPlanFormatRepairRequestV2(original, invalid, formatErr)
	req.PromptID, req.PromptVersion = AuthoringPlanFormatRepairPromptIDV5, "v5"
	return req
}

type authoringIntentRepairOutputV5 struct {
	TestIntents []domain.AuthoringTestIntentV1 `json:"test_intents"`
}

// Only the test plan can be corrected. The frozen spec and every other plan
// field stay in server memory, never replaced by model-authored alternatives.
// One separately cached effect bounds cost and survives activity redelivery.
func (a *Activities) repairAuthoringIntentsV5(ctx context.Context, request *llm.Request, spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1, conflicts []string) ([]domain.AuthoringTestIntentV1, *ArtifactRef, error) {
	schema, err := closedModelJSONSchemaV2(reflect.TypeOf(authoringIntentRepairOutputV5{}), nil)
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(struct {
		Spec      domain.SemanticSpecV1  `json:"frozen_semantic_spec"`
		Plan      domain.AuthoringPlanV1 `json:"frozen_authoring_plan"`
		Conflicts []string               `json:"proven_conflicts"`
	}{spec, plan, conflicts})
	if err != nil {
		return nil, nil, err
	}
	req := *request
	req.PromptID, req.PromptVersion = authoringIntentRepairPromptIDV5, "v5"
	req.System = "Correct ONLY the incompatible test_intents. Return one complete JSON object following this contract:\n" + schema + "\n" + authoringJointCoverageInstructionsV5 +
		"Preserve every already compatible test_intent exactly. Replace incompatible intents with satisfiable intents under the SAME constraint_region and retain ALL their original boundary_refs across the replacements. Do not remove requirements, invent new boundaries, change the semantic spec, algorithms, facts or difficulty. No extra fields, Markdown or commentary."
	req.Messages = []llm.Message{{Role: "user", Content: string(payload)}}
	stop := heartbeatWhile(ctx, "correcting incompatible test-intent boundary combinations", 15*time.Second)
	response, source, err := a.completeLLMWithProvenance(ctx, authoringIntentRepairPromptIDV5, &req, 2)
	stop()
	if err != nil {
		return nil, source, wrapRequiredProviderEffectError("correct authoring test intents", err)
	}
	if err := validateAuthoringProviderResponseV2(response, source); err != nil {
		return nil, source, err
	}
	fail := func(err error) ([]domain.AuthoringTestIntentV1, *ArtifactRef, error) {
		return nil, source, temporal.NewNonRetryableApplicationError("test-intent correction failed: "+err.Error(), authoringIntentConflictErrorTypeV5, err)
	}
	text := strings.TrimSpace(response.Text())
	if len(text) > maxAuthoringFormatRepairBytes {
		return fail(fmt.Errorf("response exceeds the test-intent correction size limit"))
	}
	if err := validateExactJSONKeysV1(text, reflect.TypeOf(authoringIntentRepairOutputV5{})); err != nil {
		return fail(err)
	}
	var corrected authoringIntentRepairOutputV5
	if err := json.Unmarshal([]byte(text), &corrected); err != nil {
		return fail(err)
	}
	if len(corrected.TestIntents) == 0 {
		return fail(fmt.Errorf("test_intents is missing or empty"))
	}
	if err := preserveAuthoringIntentRequirementsV5(spec, plan, corrected.TestIntents); err != nil {
		return fail(err)
	}
	return corrected.TestIntents, source, nil
}

func preserveAuthoringIntentRequirementsV5(spec domain.SemanticSpecV1, original domain.AuthoringPlanV1, corrected []domain.AuthoringTestIntentV1) error {
	regions := map[string]map[string]bool{}
	for _, intent := range original.TestIntents {
		if regions[intent.ConstraintRegion] == nil {
			regions[intent.ConstraintRegion] = map[string]bool{}
		}
		for _, ref := range intent.BoundaryRefs {
			regions[intent.ConstraintRegion][ref] = true
		}
	}
	allowed := map[string]bool{}
	scopes := map[string]string{}
	for _, symbol := range spec.Symbols {
		scopes[symbol.Name] = symbol.Scope
	}
	for _, boundary := range spec.Boundaries {
		allowed[boundary.ID] = boundary.Allowed && scopes[boundary.Symbol] == domain.SemanticSymbolScopeInput
	}
	for _, intent := range corrected {
		refs, exists := regions[intent.ConstraintRegion]
		if !exists {
			return fmt.Errorf("correction added a new test region: %q", intent.ConstraintRegion)
		}
		for _, ref := range intent.BoundaryRefs {
			if !refs[ref] || !allowed[ref] {
				return fmt.Errorf("correction added an undeclared, forbidden, non-input or unrelated boundary %q in region %q", ref, intent.ConstraintRegion)
			}
		}
	}
	for _, intent := range original.TestIntents {
		single := original
		single.TestIntents = []domain.AuthoringTestIntentV1{intent}
		if len(authoringIntentConflictsV5(spec, single)) == 0 {
			found := false
			for _, next := range corrected {
				if reflect.DeepEqual(intent, next) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("a compatible test intent was changed or removed (region=%q)", intent.ConstraintRegion)
			}
			continue
		}
		refs := map[string]bool{}
		found := false
		for _, next := range corrected {
			if next.ConstraintRegion != intent.ConstraintRegion {
				continue
			}
			found = true
			for _, ref := range next.BoundaryRefs {
				refs[ref] = true
			}
		}
		if !found {
			return fmt.Errorf("a conflicting test region was removed: %q", intent.ConstraintRegion)
		}
		for _, ref := range intent.BoundaryRefs {
			if !refs[ref] {
				return fmt.Errorf("required boundary %q was removed from region %q", ref, intent.ConstraintRegion)
			}
		}
	}
	next := original
	next.TestIntents = corrected
	if conflicts := authoringIntentConflictsV5(spec, next); len(conflicts) > 0 {
		return fmt.Errorf("incompatible requirements remain after one correction: %s", strings.Join(conflicts, "; "))
	}
	return nil
}
