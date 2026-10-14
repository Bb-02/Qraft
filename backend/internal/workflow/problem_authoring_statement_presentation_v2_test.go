package workflow

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/require"
)

func correctedStatementWorkflowResultV2(t *testing.T, in activities.RenderStatementFromAuthoringBundleInput) *activities.RenderStatementFromAuthoringBundleResult {
	t.Helper()
	result := acceptedAuthoringStatementWorkflowResultV1(t, in)
	fill := func(ref *activities.ArtifactRef) {
		ref.SchemaVersion, ref.PayloadVersion = activities.ArtifactRefSchemaVersion, activities.ActivityPayloadVersion
		ref.Bucket = result.StatementDraftArtifact.Bucket
		ref.Key = "workflow-artifacts/v1/sha256/" + ref.SHA256[:2] + "/" + ref.SHA256
		ref.SizeBytes, ref.ContentType = 1, "application/json"
		ref.Producer, ref.WorkflowID = result.StatementDraftArtifact.Producer, result.StatementDraftArtifact.WorkflowID
		ref.Provider, ref.Model, ref.ModelRevision = "fixture-provider", "fixture-model", "fixture-revision"
	}
	original := result.SourceArtifacts[1]
	fill(original)
	*original.LLMCallReceipt = activities.LLMCallReceipt{SchemaVersion: 1, PromptID: activities.AuthoringStatementPresentationPromptIDV2, PromptVersion: "v2", RequestedModel: "fixture-model", Provider: "fixture-provider", EndpointID: strings.Repeat("1", 64), PromptHash: strings.Repeat("2", 64), RequestSHA256: strings.Repeat("3", 64)}
	corrected := *original
	corrected.SHA256 = strings.Repeat("b", 64)
	fill(&corrected)
	receipt := *original.LLMCallReceipt
	receipt.PromptID, receipt.PromptHash, receipt.RequestSHA256 = activities.AuthoringStatementPresentationRepairPromptIDV2, strings.Repeat("4", 64), strings.Repeat("5", 64)
	corrected.LLMCallReceipt = &receipt
	result.SourceArtifacts = []*activities.ArtifactRef{result.SourceArtifacts[0], original, &corrected, result.StatementDraftArtifact}
	return result
}

func TestStatementWorkflowAllowsLegacyThreeRefsOrOneBoundPresentationCorrection(t *testing.T) {
	_, _, input := newAuthoringStatementWorkflowEnvironment(t)
	authoring := acceptedAuthoringWorkflowResult(t, input)
	renderInput := expectedAuthoringStatementRendererInputV1(input, *authoring)
	require.NoError(t, validateAuthoringStatementWorkflowResultV1(renderInput, acceptedAuthoringStatementWorkflowResultV1(t, renderInput)))
	require.NoError(t, validateAuthoringStatementWorkflowResultV1(renderInput, correctedStatementWorkflowResultV2(t, renderInput)))
}

func TestStatementWorkflowRejectsUnboundOrRepeatedPresentationCorrections(t *testing.T) {
	_, _, input := newAuthoringStatementWorkflowEnvironment(t)
	authoring := acceptedAuthoringWorkflowResult(t, input)
	renderInput := expectedAuthoringStatementRendererInputV1(input, *authoring)
	for name, mutate := range map[string]func(*activities.RenderStatementFromAuthoringBundleResult){
		"foreign workflow": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].WorkflowID = "foreign"
		},
		"foreign producer": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].Producer = "OtherActivity"
		},
		"missing receipt": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].LLMCallReceipt = nil
		},
		"wrong version": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].LLMCallReceipt.PromptVersion = "v1"
		},
		"unknown repair prompt": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].LLMCallReceipt.PromptID = "untrusted"
		},
		"same request": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].LLMCallReceipt.RequestSHA256 = result.SourceArtifacts[1].LLMCallReceipt.RequestSHA256
		},
		"different route": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2].LLMCallReceipt.RequestedModel = "different"
		},
		"provider refs reversed": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[1], result.SourceArtifacts[2] = result.SourceArtifacts[2], result.SourceArtifacts[1]
		},
		"draft is not last": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts[2], result.SourceArtifacts[3] = result.SourceArtifacts[3], result.SourceArtifacts[2]
		},
		"second correction": func(result *activities.RenderStatementFromAuthoringBundleResult) {
			result.SourceArtifacts = append(result.SourceArtifacts[:3], result.SourceArtifacts[2], result.StatementDraftArtifact)
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := correctedStatementWorkflowResultV2(t, renderInput)
			mutate(result)
			require.Error(t, validateAuthoringStatementWorkflowResultV1(renderInput, result))
		})
	}
}
