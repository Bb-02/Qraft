package activities

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
)

func intentBindingBundleFixtureV5(t *testing.T) *AuthoringPlanBundleV1 {
	t.Helper()
	in, original, corrected := intentRepairFixtureV5()
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, original)), authoringLLMResponse(intentRepairJSONV5(t, corrected))}}
	acts, store, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, in)
	bundle, err := decodeCanonicalAuthoringBundleV1(store.objects[result.BundleSHA256])
	require.NoError(t, err)
	return bundle
}

func TestAuthoringIntentBindingV5ActualBundleReadRejectsIncompleteOrForgedIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*AuthoringPlanBundleV1){
		"missing repair ref":    func(b *AuthoringPlanBundleV1) { b.TestIntentRepairArtifact = nil },
		"missing original hash": func(b *AuthoringPlanBundleV1) { b.OriginalTestIntentPlanSHA256 = "" },
		"invalid original hash": func(b *AuthoringPlanBundleV1) { b.OriginalTestIntentPlanSHA256 = "invalid" },
		"unchanged plan": func(b *AuthoringPlanBundleV1) {
			sha, err := canonicalJSONSHA256(*b.AuthoringPlan)
			require.NoError(t, err)
			b.OriginalTestIntentPlanSHA256 = sha
		},
		"wrong repair hash": func(b *AuthoringPlanBundleV1) { b.TestIntentRepairArtifact.SHA256 = strings.Repeat("4", 64) },
		"original response reused": func(b *AuthoringPlanBundleV1) {
			b.TestIntentRepairArtifact.SHA256 = b.SourceArtifactSHA256
			b.TestIntentRepairArtifact.Key = artifactKey(b.SourceArtifactSHA256)
		},
		"original request reused": func(b *AuthoringPlanBundleV1) {
			b.TestIntentRepairArtifact.LLMCallReceipt.RequestSHA256 = b.SourceRequestSHA256
		},
		"wrong prompt": func(b *AuthoringPlanBundleV1) {
			b.TestIntentRepairArtifact.LLMCallReceipt.PromptID = AuthoringPlanFormatRepairPromptIDV5
		},
		"wrong version":   func(b *AuthoringPlanBundleV1) { b.TestIntentRepairArtifact.LLMCallReceipt.PromptVersion = "v4" },
		"missing receipt": func(b *AuthoringPlanBundleV1) { b.TestIntentRepairArtifact.LLMCallReceipt = nil },
		"wrong producer":  func(b *AuthoringPlanBundleV1) { b.TestIntentRepairArtifact.Producer = "synthetic_other_activity" },
	} {
		t.Run(name, func(t *testing.T) {
			bundle := intentBindingBundleFixtureV5(t)
			mutate(bundle)
			data, err := json.Marshal(bundle)
			require.NoError(t, err)
			_, err = decodeCanonicalAuthoringBundleV1(data)
			require.Error(t, err)
		})
	}
}

func TestAuthoringIntentBindingV5KeepsLegacyBundleBytesAndAllFourActualActivityPaths(t *testing.T) {
	for _, format := range []bool{false, true} {
		for _, intent := range []bool{false, true} {
			in, original, corrected := intentRepairFixtureV5()
			if !intent {
				original.AuthoringPlan.TestIntents = corrected
			}
			responses := []*llm.Response{}
			if format {
				responses = append(responses, authoringLLMResponse("{"))
			}
			responses = append(responses, authoringLLMResponse(authoringFormatValidJSON(t, original)))
			if intent {
				responses = append(responses, authoringLLMResponse(intentRepairJSONV5(t, corrected)))
			}
			model := &authoringFormatLLM{responses: responses}
			acts, store, _ := authoringFormatActivities(model)
			result := executeAuthoringActivity(t, acts, in)
			data := store.objects[result.BundleSHA256]
			bundle, err := decodeCanonicalAuthoringBundleV1(data)
			require.NoError(t, err)
			require.Equal(t, bundle.TestIntentRepairArtifact, result.TestIntentRepairArtifact)
			require.Equal(t, bundle.OriginalTestIntentPlanSHA256, result.OriginalTestIntentPlanSHA256)
			require.Len(t, result.SourceArtifacts, len(responses)+1)
			require.Len(t, model.requests, len(responses))
			if !intent {
				require.NotContains(t, string(data), "test_intent_repair_artifact")
				require.NotContains(t, string(data), "original_test_intent_plan_sha256")
			}
		}
	}
}
