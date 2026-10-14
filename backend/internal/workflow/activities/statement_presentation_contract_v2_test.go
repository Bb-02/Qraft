package activities

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func presentationActivitiesV2(model *authoringFormatLLM, fixture *statementDraftFixture) *Activities {
	return &Activities{deps: &Dependencies{LLM: model, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: fixture.store}
}

func TestPresentationV2PromptHasCompleteActualForbiddenTerms(t *testing.T) {
	system, err := statementNarrativeSystemPromptV2()
	require.NoError(t, err)
	_, raw, ok := strings.Cut(system, "Exact server presentation constraints:\n")
	require.True(t, ok)
	var constraints struct {
		Words   []string `json:"forbidden_ascii_words"`
		Phrases []string `json:"forbidden_ascii_phrases"`
		CJK     []string `json:"forbidden_cjk_substrings"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &constraints))
	require.Equal(t, statementNarrativeNormativeWordTermsV1, constraints.Words)
	require.Equal(t, statementNarrativeNormativePhraseTermsV1, constraints.Phrases)
	require.Equal(t, statementNarrativeNormativeCJKTermsV1, constraints.CJK)
	require.Contains(t, system, "single character 图 is forbidden")
	require.Contains(t, system, "任务 is forbidden")
	model, err := parseStatementNarrativeResponseV1(`{"title":"雨后的街角","narrative":"晨光穿过窗帘，人们迎来平静的日常。"}`)
	require.NoError(t, err)
	require.NoError(t, lintStatementNarrativeAgainstFactsV1(*model, StatementFactManifestV1{}))
}

func TestPresentationV2CorrectsFormatOrWordsWithoutChangingNormativeFacts(t *testing.T) {
	good := `{"title":"灯火街角","narrative":"朋友们在雨后相聚。"}`
	for name, bad := range map[string]string{
		"task CJK":               `{"title":"灯火","narrative":"朋友们迎来了新的任务。"}`,
		"single graph character": `{"title":"街角地图","narrative":"朋友们在雨后相聚。"}`,
		"protected symbol":       `{"title":"Lantern n","narrative":"Friends gather beneath warm lanterns."}`,
		"numeric fact":           `{"title":"灯火","narrative":"朋友们相聚了两天，共有7人。"}`,
		"JSON format":            `{"title":"灯火","narrative":`,
		"unknown JSON field":     `{"title":"灯火","narrative":"朋友们相聚。","facts":"untrusted"}`,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newStatementDraftFixture(t, []string{"PRIVATE_SAMPLE_DO_NOT_SEND\n"})
			model := &authoringFormatLLM{responses: []*llm.Response{statementDraftLLMResponse(bad), statementDraftLLMResponse(good)}}
			acts := presentationActivitiesV2(model, fixture)
			result, err := executeStatementDraftActivity(t, acts, fixture.input)
			require.NoError(t, err)
			require.Len(t, model.requests, 2)
			require.Len(t, result.SourceArtifacts, 4)
			require.Equal(t, AuthoringStatementPresentationPromptIDV2, result.SourceArtifacts[1].LLMCallReceipt.PromptID)
			require.Equal(t, AuthoringStatementPresentationRepairPromptIDV2, result.SourceArtifacts[2].LLMCallReceipt.PromptID)
			require.NotEqual(t, result.SourceArtifacts[1].LLMCallReceipt.RequestSHA256, result.SourceArtifacts[2].LLMCallReceipt.RequestSHA256)
			var draft StatementDraftBundleV1
			require.NoError(t, json.Unmarshal(fixture.store.objects[result.StatementDraftSHA256], &draft))
			require.Equal(t, result.SourceArtifacts[2].SHA256, draft.RendererSourceArtifactSHA256)
			require.Equal(t, result.SourceArtifacts[2].LLMCallReceipt.RequestSHA256, draft.RendererSourceRequestSHA256)
			require.Equal(t, statementFactManifestFromSemanticSpecV1(*fixture.bundle.SemanticSpec), draft.FactManifest)
			require.Equal(t, fixture.bundle.SemanticSpec.ProblemDefinition, draft.FactManifest.ProblemDefinition)
			require.True(t, reflect.DeepEqual(fixture.bundle.SemanticSpec.InputGrammar, draft.FactManifest.InputGrammar))
			require.Contains(t, draft.Markdown, fixture.bundle.SemanticSpec.ProblemDefinition)
			require.Equal(t, fixture.bundleBytes, fixture.store.objects[fixture.input.BundleArtifact.SHA256])
			parsed, err := parseStatementNarrativeResponseV1(good)
			require.NoError(t, err)
			expectedSHA, err := canonicalJSONSHA256(parsed)
			require.NoError(t, err)
			require.Equal(t, expectedSHA, draft.ModelOutputSHA256)
			var original recordedLLMResponse
			require.NoError(t, json.Unmarshal(fixture.store.objects[result.SourceArtifacts[1].SHA256], &original))
			require.Equal(t, bad, original.Response.Text())
			for _, req := range model.requests {
				require.NotContains(t, req.Messages[0].Content, "PRIVATE_SAMPLE_DO_NOT_SEND")
				require.NotContains(t, req.Messages[0].Content, "problem_definition")
				require.NotContains(t, req.Messages[0].Content, "failure_modes")
			}
			require.Contains(t, model.requests[1].System, "untrusted data")
		})
	}
}

func TestPresentationV2RemainsStrictAfterOneCorrectionAndSkipsTruncationRepair(t *testing.T) {
	bad := `{"title":"灯火","narrative":"这是一个任务。"}`
	fixture := newStatementDraftFixture(t, nil)
	model := &authoringFormatLLM{responses: []*llm.Response{statementDraftLLMResponse(bad), statementDraftLLMResponse(bad)}}
	_, err := executeStatementDraftActivity(t, presentationActivitiesV2(model, fixture), fixture.input)
	var applicationErr *temporal.ApplicationError
	require.True(t, errors.As(err, &applicationErr))
	require.True(t, applicationErr.NonRetryable())
	require.Equal(t, statementDraftContractErrorTypeV1, applicationErr.Type())
	require.Len(t, model.requests, 2)
	for _, stop := range []string{"max_tokens", "length", "max_output_tokens", "token_limit"} {
		fixture := newStatementDraftFixture(t, nil)
		response := statementDraftLLMResponse(`{"title":"灯火"`)
		response.StopReason = stop
		model := &authoringFormatLLM{responses: []*llm.Response{response}}
		_, err := executeStatementDraftActivity(t, presentationActivitiesV2(model, fixture), fixture.input)
		require.ErrorContains(t, err, "truncated")
		require.Len(t, model.requests, 1)
	}
}

func TestPresentationV2ReplayUsesIndependentKeysAndKeepsOldCachedResponseUnused(t *testing.T) {
	fixture := newStatementDraftFixture(t, nil)
	model := &authoringFormatLLM{responses: []*llm.Response{statementDraftLLMResponse(`{"title":"灯火","narrative":"这是一个任务。"}`), statementDraftLLMResponse(`{"title":"灯火","narrative":"朋友们在雨后相聚。"}`)}}
	acts := presentationActivitiesV2(model, fixture)
	cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	var legacyKey string
	run := func() *RenderStatementFromAuthoringBundleResult {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		wrapper := func(ctx context.Context, in RenderStatementFromAuthoringBundleInput) (*RenderStatementFromAuthoringBundleResult, error) {
			var err error
			legacyKey, err = temporalProviderEffectKey(ctx, "llm:authoring_statement_v1")
			if err != nil {
				return nil, err
			}
			cache.requests[legacyKey] = "synthetic-legacy-request-hash"
			cache.results[legacyKey] = json.RawMessage(`{"content":[{"type":"text","text":"old cached invalid presentation"}]}`)
			return acts.RenderStatementFromAuthoringBundleActivityV1(ctx, in)
		}
		env.RegisterActivityWithOptions(wrapper, activity.RegisterOptions{Name: "RenderStatementFromAuthoringBundleActivityV1"})
		value, err := env.ExecuteActivity("RenderStatementFromAuthoringBundleActivityV1", fixture.input)
		require.NoError(t, err)
		var result RenderStatementFromAuthoringBundleResult
		require.NoError(t, value.Get(&result))
		return &result
	}
	first, second := run(), run()
	require.Len(t, model.requests, 2)
	require.Len(t, cache.acquired, 4)
	require.NotEqual(t, legacyKey, cache.acquired[0])
	require.NotEqual(t, legacyKey, cache.acquired[1])
	require.NotEqual(t, cache.acquired[0], cache.acquired[1])
	require.Equal(t, cache.acquired[:2], cache.acquired[2:])
	require.Equal(t, first.StatementDraftSHA256, second.StatementDraftSHA256)
}
