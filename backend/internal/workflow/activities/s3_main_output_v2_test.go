package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

type mainOutputResultV2 struct {
	Parsed  *oracleCandidateModelOutputV1
	Final   *ArtifactRef
	Sources []*ArtifactRef
}

func mainOutputJSONV2(t *testing.T, source, language string) string {
	t.Helper()
	data, err := json.Marshal(oracleCandidateModelOutputV1{SourceCode: source, Language: language})
	require.NoError(t, err)
	return string(data)
}

func mainOutputFixtureV2(provider *reviewerFormatLLMV2) *Activities {
	return New(&Dependencies{LLM: provider, LLMProvider: "synthetic-provider", LLMModel: "synthetic-main", ArtifactStore: newOracleMemoryArtifactStore(), ProvenanceRecorder: &captureProvenanceRecorder{}})
}

func mainOutputRequestV2() *llm.Request {
	return &llm.Request{Model: "synthetic-main", MaxTokens: 16000, System: s3MainSolutionSystemPromptV2, Messages: []llm.Message{{Role: "user", Content: `{"frozen_task":"synthetic"}`}}, Runtime: &llm.RuntimeConfig{Provider: "synthetic-provider", ReasoningEffort: "high"}}
}

func executeMainOutputV2(t *testing.T, acts *Activities, req *llm.Request) (*mainOutputResultV2, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context) (*mainOutputResultV2, error) {
		parsed, final, sources, err := acts.completeS3MainOutputV2(ctx, req, "cpp")
		return &mainOutputResultV2{Parsed: parsed, Final: final, Sources: sources}, err
	}, activity.RegisterOptions{Name: "syntheticMainOutputV2"})
	value, err := env.ExecuteActivity("syntheticMainOutputV2")
	if err != nil {
		return nil, err
	}
	var result mainOutputResultV2
	require.NoError(t, value.Get(&result))
	return &result, nil
}

func TestS3MainOutputV2PreservesDecodedSourceExactly(t *testing.T) {
	for _, source := range []string{
		"#include <iostream>\nint main(){std::cout << '\\n';}\n",
		// JSON-valid but invalid C++ must remain invalid: the compiler is the gate.
		"#include <iostream>\nint main(){std::cout << '\n';}\n",
	} {
		provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(mainOutputJSONV2(t, source, "cpp"), "stop")}}
		result, err := executeMainOutputV2(t, mainOutputFixtureV2(provider), mainOutputRequestV2())
		require.NoError(t, err)
		require.Equal(t, source, result.Parsed.SourceCode)
		require.Len(t, provider.requests, 1)
		require.Len(t, result.Sources, 1)
		require.Equal(t, "v2", result.Final.LLMCallReceipt.PromptVersion)
		require.Equal(t, s3MainSolutionPromptIDV2, result.Final.LLMCallReceipt.PromptID)
	}
	require.Contains(t, s3MainSolutionSystemPromptV2, `C++ '\n'`)
	require.Contains(t, s3MainSolutionSystemPromptV2, `'\\n' in the outer JSON`)
}

func TestS3MainOutputV2CorrectsEmptyOrMalformedReplyOnce(t *testing.T) {
	source := "int main(){return 0;}\n"
	for _, first := range []string{"", "{\"source_code\":", strings.TrimSuffix(mainOutputJSONV2(t, source, "cpp"), "}") + `,"extra":1}`} {
		provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(first, "stop"), reviewerFormatResponseV2(mainOutputJSONV2(t, source, "cpp"), "stop")}}
		acts := mainOutputFixtureV2(provider)
		request := mainOutputRequestV2()
		result, err := executeMainOutputV2(t, acts, request)
		require.NoError(t, err)
		require.Len(t, result.Sources, 2)
		require.Equal(t, result.Sources[1], result.Final)
		require.Len(t, provider.requests, 2)
		require.Equal(t, request.Messages[0], provider.requests[1].Messages[0])
		require.Equal(t, request.Runtime, provider.requests[1].Runtime)
		require.Equal(t, request.MaxTokens, provider.requests[1].MaxTokens)
		require.Len(t, request.Messages, 1, "format repair must not mutate frozen request")
		for index, ref := range result.Sources {
			var recorded recordedLLMResponse
			bytes, err := acts.artifacts.Get(context.Background(), *ref)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(bytes, &recorded))
			require.Equal(t, []string{first, mainOutputJSONV2(t, source, "cpp")}[index], recorded.Response.Text())
			require.Equal(t, []string{s3MainSolutionPromptIDV2, s3MainSolutionFormatRepairPromptIDV2}[index], ref.LLMCallReceipt.PromptID)
		}
	}
}

func TestS3MainOutputV2FailsClosedWithoutProgramRepair(t *testing.T) {
	source := "int main(){return 0;}"
	for _, tc := range []struct {
		name, first, second, stop, errType string
		calls                              int
	}{
		{name: "empty twice", first: "", second: "", stop: "stop", errType: mainSolutionEmptyResponseErrorType, calls: 2},
		{name: "malformed twice", first: "{", second: "{", stop: "stop", errType: mainSolutionFormatErrorType, calls: 2},
		{name: "truncated", first: "{", stop: "length", errType: mainSolutionTruncatedErrorType, calls: 1},
		{name: "wrong language", first: mainOutputJSONV2(t, source, "python"), stop: "stop", errType: mainSolutionFormatErrorType, calls: 1},
		{name: "oversized malformed reply", first: strings.Repeat("x", maxS3MainFormatRepairBytesV2+1), stop: "stop", errType: mainSolutionFormatErrorType, calls: 1},
		{name: "known source changed", first: strings.TrimSuffix(mainOutputJSONV2(t, source, "cpp"), "}") + `,"extra":1}`, second: mainOutputJSONV2(t, "int main(){return 1;}", "cpp"), stop: "stop", errType: mainSolutionFormatErrorType, calls: 2},
		{name: "known language changed", first: `{"source_code":"","language":"python"}`, second: mainOutputJSONV2(t, source, "cpp"), stop: "stop", errType: mainSolutionFormatErrorType, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(tc.first, tc.stop), reviewerFormatResponseV2(tc.second, "stop")}}
			_, err := executeMainOutputV2(t, mainOutputFixtureV2(provider), mainOutputRequestV2())
			require.Error(t, err)
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, tc.errType, appErr.Type())
			require.True(t, appErr.NonRetryable())
			require.Len(t, provider.requests, tc.calls)
		})
	}
}

func TestS3MainOutputV2RedeliveryReusesIndependentEffects(t *testing.T) {
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2("legacy empty reply", "stop"), reviewerFormatResponseV2("", "stop"), reviewerFormatResponseV2(mainOutputJSONV2(t, "int main(){return 0;}", "cpp"), "stop")}}
	acts := mainOutputFixtureV2(provider)
	cache := make(reviewerFormatCacheV2)
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	var first *mainOutputResultV2
	var legacySHA string
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context) (*mainOutputResultV2, error) {
		legacy := mainOutputRequestV2()
		legacy.System = s3MainSolutionSystemPromptV1
		response, oldRef, oldErr := acts.completeLLMWithProvenance(ctx, "s3_main_solution_v1", legacy, 2)
		if oldErr != nil {
			return nil, oldErr
		}
		require.Equal(t, "legacy empty reply", response.Text())
		if legacySHA == "" {
			legacySHA = oldRef.SHA256
		} else {
			require.Equal(t, legacySHA, oldRef.SHA256)
		}
		parsed, final, sources, err := acts.completeS3MainOutputV2(ctx, mainOutputRequestV2(), "cpp")
		result := &mainOutputResultV2{Parsed: parsed, Final: final, Sources: sources}
		if err == nil && first == nil {
			first = result
			return nil, fmt.Errorf("synthetic redelivery after cached calls")
		}
		return result, err
	}, activity.RegisterOptions{Name: "syntheticMainRedeliveryV2"})
	env.ExecuteWorkflow(func(ctx temporalworkflow.Context) (*mainOutputResultV2, error) {
		ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2}})
		var result mainOutputResultV2
		err := temporalworkflow.ExecuteActivity(ctx, "syntheticMainRedeliveryV2").Get(ctx, &result)
		return &result, err
	})
	require.NoError(t, env.GetWorkflowError())
	var result mainOutputResultV2
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Len(t, provider.requests, 3)
	require.Len(t, cache, 3, "legacy, versioned initial and correction effects must be distinct and stable")
	require.NotEqual(t, legacySHA, result.Sources[0].SHA256)
	require.Equal(t, first.Sources[0].SHA256, result.Sources[0].SHA256)
	require.Equal(t, first.Sources[1].SHA256, result.Sources[1].SHA256)
}
