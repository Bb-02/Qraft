package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

func TestS3ReviewerBudgetV3PreservesSelectedModelAndReasoning(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runtime   bool
		effort    string
		maxTokens int
	}{
		{name: "historical runtime absent", maxTokens: 8192},
		{name: "no explicit reasoning", runtime: true, maxTokens: 8192},
		{name: "reasoning disabled", runtime: true, effort: "none", maxTokens: 8192},
		{name: "minimal", runtime: true, effort: "minimal", maxTokens: 32768},
		{name: "low", runtime: true, effort: "low", maxTokens: 32768},
		{name: "medium", runtime: true, effort: "medium", maxTokens: 32768},
		{name: "high", runtime: true, effort: "high", maxTokens: 32768},
		{name: "xhigh", runtime: true, effort: "xhigh", maxTokens: 32768},
		{name: "max", runtime: true, effort: "max", maxTokens: 32768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(reviewerAssessmentJSONV2(t, 5), "end_turn")}}
			acts, _, in := reviewerFormatFixtureV2(t, provider)
			if tc.runtime {
				in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Provider: "fixture-provider", Protocol: "openai-chat", ReasoningEffort: tc.effort}
			}
			result, err := executeReviewerFormatV2(t, acts, in)
			require.NoError(t, err)
			require.Len(t, provider.requests, 1)
			request := provider.requests[0]
			require.Equal(t, tc.maxTokens, request.MaxTokens)
			require.Equal(t, "v3", request.PromptVersion)
			require.Equal(t, "s3_strict_reviewer_v3", request.PromptID)
			if tc.runtime {
				require.Equal(t, "synthetic-selected-reviewer", request.Model)
				require.Equal(t, "openai-chat", request.Runtime.Protocol)
				require.Equal(t, tc.effort, request.Runtime.ReasoningEffort)
				require.Equal(t, tc.effort, in.ReviewRuntime.ReasoningEffort)
			} else {
				require.Equal(t, "synthetic-reviewer", request.Model)
				require.Nil(t, request.Runtime)
			}
			// A larger budget never turns a completed negative assessment into approval.
			require.False(t, *result.Reviewer.Assessment.Approved)
			require.Equal(t, 5, *result.Reviewer.Assessment.Dimensions.Correctness.Score)
		})
	}
}

func TestS3ReviewerBudgetV3StillRejectsExhaustedReasoningWithoutFormatRepair(t *testing.T) {
	response := reviewerFormatResponseV2("", "max_tokens")
	response.Usage = llm.Usage{InputTokens: 1200, OutputTokens: 32768}
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{response}}
	acts, store, in := reviewerFormatFixtureV2(t, provider)
	in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Protocol: "openai-chat", ReasoningEffort: "high"}
	_, err := executeReviewerFormatV2(t, acts, in)
	require.Error(t, err)
	var applicationErr *temporal.ApplicationError
	require.ErrorAs(t, err, &applicationErr)
	require.Equal(t, "ReviewerTruncatedError", applicationErr.Type())
	require.True(t, applicationErr.NonRetryable())
	require.Len(t, provider.requests, 1, "an exhausted reasoning budget must not trigger a new paid format correction")
	require.Equal(t, 32768, provider.requests[0].MaxTokens)
	require.Equal(t, "high", provider.requests[0].Runtime.ReasoningEffort)
	retained := false
	for _, data := range store.objects {
		var recorded recordedLLMResponse
		if json.Unmarshal(data, &recorded) == nil && recorded.Response != nil && recorded.Response.StopReason == "max_tokens" {
			retained = true
			require.Equal(t, response.Usage, recorded.Response.Usage)
			require.Empty(t, recorded.Response.Text())
			require.Equal(t, "v3", recorded.CallReceipt.PromptVersion)
		}
	}
	require.True(t, retained, "the failed response remains available as provenance")
}

func TestS3ReviewerBudgetV3RedeliveryReusesPaidCallsAndKeepsLegacyEffects(t *testing.T) {
	valid := reviewerAssessmentJSONV2(t, 5)
	bad := valid[:len(valid)-1] + `,"unexpected":true}`
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(bad, "end_turn"), reviewerFormatResponseV2(valid, "end_turn")}}
	acts, _, in := reviewerFormatFixtureV2(t, provider)
	in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Protocol: "openai-chat", ReasoningEffort: "high"}
	cache := make(reviewerFormatCacheV2)
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	legacy := make(reviewerFormatCacheV2)
	var first *S3StrictReviewerResultV1
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		if len(legacy) == 0 {
			for _, step := range []string{"s3_strict_reviewer_v2", "s3_strict_reviewer_v2_format_repair_1"} {
				key, err := temporalProviderEffectKey(ctx, "llm:"+step)
				require.NoError(t, err)
				entry := reviewerFormatCachedCallV2{request: "synthetic-legacy-v2-request", result: json.RawMessage(`{"synthetic_legacy":true}`)}
				cache[key], legacy[key] = entry, entry
			}
		}
		result, err := acts.S3StrictReviewerActivityV1(ctx, input)
		if err == nil && first == nil {
			first = result
			return nil, fmt.Errorf("synthetic activity redelivery after successful provider calls")
		}
		return result, err
	}, activity.RegisterOptions{Name: "syntheticReviewerBudgetRedelivery"})
	env.ExecuteWorkflow(func(ctx temporalworkflow.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2}})
		var result S3StrictReviewerResultV1
		err := temporalworkflow.ExecuteActivity(ctx, "syntheticReviewerBudgetRedelivery", input).Get(ctx, &result)
		return &result, err
	}, in)
	require.NoError(t, env.GetWorkflowError())
	var result S3StrictReviewerResultV1
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Len(t, provider.requests, 2, "redelivery must reuse both paid effects")
	require.Len(t, cache, 4, "v3 initial and correction effects must not collide with v2")
	for key, entry := range legacy {
		require.Equal(t, entry, cache[key], "existing v2 evidence must remain unchanged")
	}
	for i, request := range provider.requests {
		require.Equal(t, 32768, request.MaxTokens)
		require.Equal(t, "high", request.Runtime.ReasoningEffort)
		require.Equal(t, "synthetic-selected-reviewer", request.Model)
		require.Equal(t, "v3", request.PromptVersion)
		require.Equal(t, []string{"s3_strict_reviewer_v3", "s3_strict_reviewer_v3_format_repair_1"}[i], request.PromptID)
		require.Equal(t, first.SourceArtifacts[i].SHA256, result.SourceArtifacts[i].SHA256)
	}
	require.False(t, *result.Reviewer.Assessment.Approved)
	require.Equal(t, 5, *result.Reviewer.Assessment.Dimensions.Correctness.Score)
}
