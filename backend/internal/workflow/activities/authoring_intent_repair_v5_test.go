package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

func intentRepairFixtureV5() (GenerateAuthoringPlanInput, authoringPlanModelOutputV1, []domain.AuthoringTestIntentV1) {
	in := validAuthoringActivityInput()
	in.Params.Tags = []string{"simulation"}
	output := authoringPlanDSLExampleV3()
	output.AuthoringPlan.TargetDifficulty = in.Params.Difficulty
	output.SemanticSpec.Boundaries = append(output.SemanticSpec.Boundaries,
		domain.SemanticBoundaryV1{ID: "a-low", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: -1000000000, Allowed: true, Meaning: "minimum integer element"},
		domain.SemanticBoundaryV1{ID: "a-high", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: 1000000000, Allowed: true, Meaning: "maximum integer element"})
	output.AuthoringPlan.TestIntents = append(output.AuthoringPlan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "compare edge values", ConstraintRegion: "singleton alternatives", BoundaryRefs: []string{"a-one", "a-low", "a-high"}})
	corrected := append([]domain.AuthoringTestIntentV1(nil), output.AuthoringPlan.TestIntents[:1]...)
	corrected = append(corrected,
		domain.AuthoringTestIntentV1{Purpose: "single minimum", ConstraintRegion: "singleton alternatives", BoundaryRefs: []string{"a-one", "a-low"}},
		domain.AuthoringTestIntentV1{Purpose: "single maximum", ConstraintRegion: "singleton alternatives", BoundaryRefs: []string{"a-one", "a-high"}})
	return in, output, corrected
}

func intentRepairJSONV5(t *testing.T, intents []domain.AuthoringTestIntentV1) string {
	t.Helper()
	encoded, err := json.Marshal(authoringIntentRepairOutputV5{TestIntents: intents})
	require.NoError(t, err)
	return string(encoded)
}

func TestAuthoringIntentRepairV5CorrectsOnlyPlanAndRetainsBothSources(t *testing.T) {
	in, original, corrected := intentRepairFixtureV5()
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, original)), authoringLLMResponse(intentRepairJSONV5(t, corrected))}}
	acts, store, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, in)
	require.True(t, result.LintPassed)
	require.Equal(t, AuthoringPlanDecisionAccepted, result.Decision)
	require.Len(t, model.requests, 2)
	require.Equal(t, AuthoringPlanPromptIDV5, model.requests[0].PromptID)
	require.Equal(t, authoringIntentRepairPromptIDV5, model.requests[1].PromptID)
	require.NotContains(t, model.requests[1].System, "format correction")
	require.Contains(t, model.requests[0].System, "same input must hit EVERY")
	require.Len(t, result.SourceArtifacts, 3)
	var bundle AuthoringPlanBundleV1
	require.NoError(t, json.Unmarshal(store.objects[result.BundleSHA256], &bundle))
	require.Equal(t, result.SourceArtifacts[0].SHA256, bundle.SourceArtifactSHA256)
	require.Equal(t, result.SourceArtifacts[1].SHA256, bundle.TestIntentRepairArtifact.SHA256)
	require.Equal(t, bundle.TestIntentRepairArtifact, result.TestIntentRepairArtifact)
	require.Equal(t, bundle.OriginalTestIntentPlanSHA256, result.OriginalTestIntentPlanSHA256)
	original.SemanticSpec.SchemaVersion, original.SemanticSpec.BriefSHA256 = domain.SemanticSpecSchemaV1, in.CanonicalBriefSHA256
	original.AuthoringPlan.SchemaVersion, original.AuthoringPlan.BriefSHA256 = domain.AuthoringPlanSchemaV1, in.CanonicalBriefSHA256
	original.AuthoringPlan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(*original.SemanticSpec)
	expectedPlanSHA, err := canonicalJSONSHA256(*original.AuthoringPlan)
	require.NoError(t, err)
	require.Equal(t, expectedPlanSHA, bundle.OriginalTestIntentPlanSHA256)
	require.Equal(t, *original.SemanticSpec, *bundle.SemanticSpec, "the frozen task, grammar, bounds and samples must remain identical")
	original.AuthoringPlan.TestIntents = corrected
	require.Equal(t, *original.AuthoringPlan, *bundle.AuthoringPlan, "only the test-intent list can change")
	require.Empty(t, authoringIntentConflictsV5(*bundle.SemanticSpec, *bundle.AuthoringPlan))
	// Downstream readers recompute the original strict lint, including all new intents.
	err = validateAcceptedAuthoringBundleForStatementV1(&bundle, RenderStatementFromAuthoringBundleInput{ExpectedAuthoringInputSHA256: result.InputSHA256, ExpectedBriefSHA256: in.CanonicalBriefSHA256, ExpectedSemanticSpecSHA256: result.SemanticSpecSHA256, ExpectedDifficulty: in.Params.Difficulty, RequiredKnowledgePoints: in.Params.Tags})
	require.NoError(t, err)
}

func TestAuthoringIntentRepairV5RejectsIncompleteOrRequirementDroppingRepliesWithoutLoop(t *testing.T) {
	in, original, corrected := intentRepairFixtureV5()
	for _, tc := range []struct{ name, reply string }{
		{"invalid JSON", "not JSON"},
		{"empty", `{"test_intents":[]}`},
		{"cannot change spec", strings.TrimSuffix(intentRepairJSONV5(t, corrected), "}") + `,"semantic_spec":{}}`},
		{"cannot drop compatible intent", intentRepairJSONV5(t, corrected[1:])},
		{"cannot drop required maximum", intentRepairJSONV5(t, corrected[:2])},
		{"cannot keep impossible combination", intentRepairJSONV5(t, original.AuthoringPlan.TestIntents)},
		{"cannot add unrelated region", intentRepairJSONV5(t, append(append([]domain.AuthoringTestIntentV1(nil), corrected...), domain.AuthoringTestIntentV1{Purpose: "unrelated", ConstraintRegion: "invented", BoundaryRefs: []string{"a-one"}}))},
		{"cannot add unrelated boundary", intentRepairJSONV5(t, append(append([]domain.AuthoringTestIntentV1(nil), corrected...), domain.AuthoringTestIntentV1{Purpose: "extra edge", ConstraintRegion: "singleton alternatives", BoundaryRefs: []string{"n-max"}}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, original)), authoringLLMResponse(tc.reply)}}
			acts, _, _ := authoringFormatActivities(model)
			_, err := executeAuthoringActivityRaw(acts, in)
			require.Error(t, err)
			var applicationErr *temporal.ApplicationError
			require.ErrorAs(t, err, &applicationErr)
			require.Equal(t, authoringIntentConflictErrorTypeV5, applicationErr.Type())
			require.True(t, applicationErr.NonRetryable())
			require.Len(t, model.requests, 2, "no nested semantic or format correction loop")
		})
	}
}

func TestAuthoringIntentRepairV5StillRunsFullLint(t *testing.T) {
	in, original, corrected := intentRepairFixtureV5()
	corrected[1].Purpose = ""
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, original)), authoringLLMResponse(intentRepairJSONV5(t, corrected))}}
	acts, _, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, in)
	require.False(t, result.LintPassed)
	require.Equal(t, AuthoringPlanDecisionRejected, result.Decision)
	require.Equal(t, AuthoringPlanGateReasonSpecLintFailed, result.GateReasonCode)
	require.Len(t, model.requests, 2)
}

func TestAuthoringIntentRepairV5RedeliveryReusesBothPaidCalls(t *testing.T) {
	in, original, corrected := intentRepairFixtureV5()
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, original)), authoringLLMResponse(intentRepairJSONV5(t, corrected))}}
	acts, _, _ := authoringFormatActivities(model)
	cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	var first *GenerateAuthoringPlanResult
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context, input GenerateAuthoringPlanInput) (*GenerateAuthoringPlanResult, error) {
		result, err := acts.GenerateAuthoringPlanActivity(ctx, input)
		if err == nil && first == nil {
			first = result
			return nil, fmt.Errorf("synthetic delivery failure after both cached calls")
		}
		return result, err
	}, activity.RegisterOptions{Name: "syntheticIntentRepairRedelivery"})
	env.ExecuteWorkflow(func(ctx temporalworkflow.Context, input GenerateAuthoringPlanInput) (*GenerateAuthoringPlanResult, error) {
		ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2}})
		var result GenerateAuthoringPlanResult
		err := temporalworkflow.ExecuteActivity(ctx, "syntheticIntentRepairRedelivery", input).Get(ctx, &result)
		return &result, err
	}, in)
	require.NoError(t, env.GetWorkflowError())
	var result GenerateAuthoringPlanResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Len(t, model.requests, 2)
	require.Len(t, cache.results, 2)
	require.Equal(t, first.BundleSHA256, result.BundleSHA256)
	for i := range first.SourceArtifacts {
		require.Equal(t, first.SourceArtifacts[i].SHA256, result.SourceArtifacts[i].SHA256)
	}
}
