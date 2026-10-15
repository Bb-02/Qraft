package workflow

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func authoringSourceChainFixtureV5(t *testing.T, input activities.GenerateAuthoringPlanInput, format, intent bool) *activities.GenerateAuthoringPlanResult {
	t.Helper()
	result := correctedAuthoringWorkflowResultV5(t, input)
	first, correction, bundle := result.SourceArtifacts[0], result.SourceArtifacts[1], result.SourceArtifacts[2]
	result.SourceArtifacts = []*activities.ArtifactRef{first}
	if format {
		result.SourceArtifacts = append(result.SourceArtifacts, correction)
	}
	if intent {
		ref := *first
		ref.SHA256 = strings.Repeat("7", 64)
		ref.Key = "workflow-artifacts/v1/sha256/77/" + ref.SHA256
		receipt := *first.LLMCallReceipt
		receipt.PromptID, receipt.PromptHash, receipt.RequestSHA256 = activities.AuthoringPlanIntentRepairPromptIDV5, strings.Repeat("8", 64), strings.Repeat("9", 64)
		ref.LLMCallReceipt = &receipt
		result.TestIntentRepairArtifact, result.OriginalTestIntentPlanSHA256 = &ref, strings.Repeat("6", 64)
		result.SourceArtifacts = append(result.SourceArtifacts, &ref)
	}
	result.SourceArtifacts = append(result.SourceArtifacts, bundle)
	return result
}

func TestAuthoringWorkflowV5AcceptsExactlyTheFourBoundedSourceChains(t *testing.T) {
	for _, tc := range []struct {
		name           string
		format, intent bool
	}{
		{"initial", false, false},
		{"format", true, false},
		{"intent", false, true},
		{"format then intent", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, acts, input := newAuthoringWorkflowEnvironment(t)
			want := authoringSourceChainFixtureV5(t, input, tc.format, tc.intent)
			require.NoError(t, validateAuthoringWorkflowResultV1(input, want))
			env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).Return(want, nil).Once()
			env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
			require.NoError(t, env.GetWorkflowError())
			var got activities.GenerateAuthoringPlanResult
			require.NoError(t, env.GetWorkflowResult(&got))
			require.Equal(t, want, &got)
			assertAuthoringWorkflowCallsOnlyFormalizer(t, env)
			// Historical V5 activity histories did not contain the new summaries.
			want.TestIntentRepairArtifact, want.OriginalTestIntentPlanSHA256 = nil, ""
			require.NoError(t, validateAuthoringWorkflowResultV1(input, want))
		})
	}
}

func TestAuthoringWorkflowV5RejectsUnboundIntentCorrectionChains(t *testing.T) {
	_, _, input := newAuthoringWorkflowEnvironment(t)
	for name, mutate := range map[string]func(*activities.GenerateAuthoringPlanResult){
		"intent before format": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[1], r.SourceArtifacts[2] = r.SourceArtifacts[2], r.SourceArtifacts[1]
		},
		"old format identity": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[1].LLMCallReceipt.PromptID = activities.AuthoringPlanFormatRepairPromptIDV4
		},
		"old intent version": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2].LLMCallReceipt.PromptVersion = "v4"
		},
		"unknown intent role": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2].LLMCallReceipt.PromptID = "synthetic_unknown_repair"
		},
		"duplicate request": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2].LLMCallReceipt.RequestSHA256 = r.SourceArtifacts[0].LLMCallReceipt.RequestSHA256
		},
		"intent changes route": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2].LLMCallReceipt.EndpointID = strings.Repeat("5", 64)
		},
		"intent foreign workflow": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2].WorkflowID = "synthetic_foreign_workflow"
		},
		"intent foreign producer": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2].Producer = "synthetic_foreign_producer"
		},
		"summary ref missing":       func(r *activities.GenerateAuthoringPlanResult) { r.TestIntentRepairArtifact = nil },
		"summary plan hash missing": func(r *activities.GenerateAuthoringPlanResult) { r.OriginalTestIntentPlanSHA256 = "" },
		"summary plan hash invalid": func(r *activities.GenerateAuthoringPlanResult) { r.OriginalTestIntentPlanSHA256 = "not-a-sha256" },
		"summary ref wrong hash": func(r *activities.GenerateAuthoringPlanResult) {
			ref := *r.TestIntentRepairArtifact
			ref.SHA256 = strings.Repeat("4", 64)
			r.TestIntentRepairArtifact = &ref
		},
		"bundle moved": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts[2], r.SourceArtifacts[3] = r.SourceArtifacts[3], r.SourceArtifacts[2]
		},
		"repeated intent": func(r *activities.GenerateAuthoringPlanResult) { r.SourceArtifacts[1] = r.SourceArtifacts[2] },
		"extra correction": func(r *activities.GenerateAuthoringPlanResult) {
			r.SourceArtifacts = append(r.SourceArtifacts[:3], r.SourceArtifacts[2], r.BundleArtifact)
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := authoringSourceChainFixtureV5(t, input, true, true)
			mutate(result)
			require.Error(t, validateAuthoringWorkflowResultV1(input, result))
		})
	}
	result := authoringSourceChainFixtureV5(t, input, false, false)
	foreign := authoringSourceChainFixtureV5(t, input, false, true)
	result.TestIntentRepairArtifact, result.OriginalTestIntentPlanSHA256 = foreign.TestIntentRepairArtifact, foreign.OriginalTestIntentPlanSHA256
	require.Error(t, validateAuthoringWorkflowResultV1(input, result), "summary cannot invent a missing source role")
}
