package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
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
			require.Equal(t, "v4", request.PromptVersion)
			require.Equal(t, "s3_strict_reviewer_v4", request.PromptID)
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

func TestS3ReviewerBudgetV3StillRejectsExhaustedReasoningAfterOneBudgetRetry(t *testing.T) {
	response := reviewerFormatResponseV2("", "max_tokens")
	response.Usage = llm.Usage{InputTokens: 1200, OutputTokens: 32768}
	expanded := reviewerFormatResponseV2("", "max_tokens")
	expanded.Usage = llm.Usage{InputTokens: 1200, OutputTokens: 65536}
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{response, expanded}}
	acts, store, in := reviewerFormatFixtureV2(t, provider)
	in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Protocol: "openai-chat", ReasoningEffort: "high"}
	_, err := executeReviewerFormatV2(t, acts, in)
	require.Error(t, err)
	var applicationErr *temporal.ApplicationError
	require.ErrorAs(t, err, &applicationErr)
	require.Equal(t, "ReviewerTruncatedError", applicationErr.Type())
	require.True(t, applicationErr.NonRetryable())
	require.Len(t, provider.requests, 2, "a second exhausted budget must stop without a format correction or another expansion")
	require.Equal(t, 32768, provider.requests[0].MaxTokens)
	require.Equal(t, 65536, provider.requests[1].MaxTokens)
	require.Equal(t, "high", provider.requests[0].Runtime.ReasoningEffort)
	require.Equal(t, "high", provider.requests[1].Runtime.ReasoningEffort)
	retained := 0
	for _, data := range store.objects {
		var recorded recordedLLMResponse
		if json.Unmarshal(data, &recorded) == nil && recorded.Response != nil && recorded.Response.StopReason == "max_tokens" {
			retained++
			require.Contains(t, []int{32768, 65536}, recorded.Response.Usage.OutputTokens)
			require.Empty(t, recorded.Response.Text())
			require.Equal(t, "v4", recorded.CallReceipt.PromptVersion)
		}
	}
	require.Equal(t, 2, retained, "both failed responses remain available as provenance")
}

func executeReviewerBudgetRedeliveryV3(t *testing.T, acts *Activities, in S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, *S3StrictReviewerResultV1) {
	t.Helper()
	var first *S3StrictReviewerResultV1
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		result, err := acts.S3StrictReviewerActivityV1(ctx, input)
		if err == nil && first == nil {
			first = result
			return nil, fmt.Errorf("synthetic redelivery after successful provider effects")
		}
		return result, err
	}, activity.RegisterOptions{Name: "syntheticReviewerExpandedRedelivery"})
	env.ExecuteWorkflow(func(ctx temporalworkflow.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2}})
		var result S3StrictReviewerResultV1
		err := temporalworkflow.ExecuteActivity(ctx, "syntheticReviewerExpandedRedelivery", input).Get(ctx, &result)
		return &result, err
	}, in)
	require.NoError(t, env.GetWorkflowError())
	var result S3StrictReviewerResultV1
	require.NoError(t, env.GetWorkflowResult(&result))
	return first, &result
}

func TestS3ReviewerBudgetV3UsesOneExpansionAndPreservesCachedCalls(t *testing.T) {
	valid := reviewerAssessmentJSONV2(t, 5)
	bad := valid[:len(valid)-1] + `,"unexpected":true}`
	for _, tc := range []struct {
		name      string
		responses []*llm.Response
		ids       []string
		budgets   []int
	}{
		{name: "complete initial response remains one paid call", responses: []*llm.Response{reviewerFormatResponseV2(valid, "stop")}, ids: []string{"s3_strict_reviewer_v4"}, budgets: []int{32768}},
		{name: "initial expansion", responses: []*llm.Response{reviewerFormatResponseV2("", "max_tokens"), reviewerFormatResponseV2(valid, "stop")}, ids: []string{"s3_strict_reviewer_v4", "s3_strict_reviewer_v4_reasoning_budget_retry_1"}, budgets: []int{32768, 65536}},
		{name: "format after initial expansion", responses: []*llm.Response{reviewerFormatResponseV2("", "max_tokens"), reviewerFormatResponseV2(bad, "stop"), reviewerFormatResponseV2(valid, "stop")}, ids: []string{"s3_strict_reviewer_v4", "s3_strict_reviewer_v4_reasoning_budget_retry_1", "s3_strict_reviewer_v4_reasoning_budget_retry_1_format_repair_1"}, budgets: []int{32768, 65536, 65536}},
		{name: "expansion during format correction", responses: []*llm.Response{reviewerFormatResponseV2(bad, "stop"), reviewerFormatResponseV2("", "length"), reviewerFormatResponseV2(valid, "stop")}, ids: []string{"s3_strict_reviewer_v4", "s3_strict_reviewer_v4_format_repair_1", "s3_strict_reviewer_v4_format_repair_1_reasoning_budget_retry_1"}, budgets: []int{32768, 32768, 65536}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &reviewerFormatLLMV2{responses: tc.responses}
			acts, store, in := reviewerFormatFixtureV2(t, provider)
			in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Protocol: "openai-chat", ReasoningEffort: "high"}
			cache := make(reviewerFormatCacheV2)
			acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
			first, result := executeReviewerBudgetRedeliveryV3(t, acts, in)
			require.Len(t, provider.requests, len(tc.responses), "redelivery reuses every paid response")
			require.LessOrEqual(t, len(provider.requests), 3)
			require.Len(t, cache, len(tc.responses))
			require.Len(t, result.SourceArtifacts, len(tc.responses))
			for i, request := range provider.requests {
				require.Equal(t, tc.budgets[i], request.MaxTokens)
				require.Equal(t, tc.ids[i], request.PromptID)
				require.Equal(t, "v4", request.PromptVersion)
				require.Equal(t, "high", request.Runtime.ReasoningEffort)
				require.Equal(t, "synthetic-selected-reviewer", request.Model)
				require.Equal(t, provider.requests[0].System, request.System)
				require.Equal(t, first.SourceArtifacts[i].SHA256, result.SourceArtifacts[i].SHA256)
				var recorded recordedLLMResponse
				require.NoError(t, json.Unmarshal(store.objects[result.SourceArtifacts[i].SHA256], &recorded))
				require.Equal(t, tc.ids[i], recorded.CallReceipt.PromptID)
				require.Equal(t, tc.responses[i].StopReason, recorded.Response.StopReason)
				if i > 0 && (tc.responses[i-1].StopReason == "max_tokens" || tc.responses[i-1].StopReason == "length") {
					require.Equal(t, provider.requests[i-1].Messages, request.Messages, "budget retry must not change the semantic request")
				}
			}
			require.Equal(t, result.SourceArtifacts[len(tc.responses)-1], result.ProviderArtifact)
			require.False(t, *result.Reviewer.Assessment.Approved)
			require.Equal(t, 5, *result.Reviewer.Assessment.Dimensions.Correctness.Score)
		})
	}
}

func TestS3ReviewerBudgetV3CannotEraseKnownNegativeConclusions(t *testing.T) {
	negative := reviewerAssessmentJSONV2(t, 5)
	positive := reviewerAssessmentJSONV2(t, 8)
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(negative, "max_tokens"), reviewerFormatResponseV2(positive, "stop")}}
	acts, _, in := reviewerFormatFixtureV2(t, provider)
	in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Protocol: "openai-chat", ReasoningEffort: "high"}
	_, err := executeReviewerFormatV2(t, acts, in)
	require.ErrorContains(t, err, "budget retry changed review conclusions")
	require.Len(t, provider.requests, 2)
}

func TestS3ReviewerBudgetV3DoesNotExpandOrdinaryModelTruncation(t *testing.T) {
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2("", "max_tokens")}}
	acts, _, in := reviewerFormatFixtureV2(t, provider)
	in.ReviewRuntime = &domain.LLMRuntimeConfig{Model: "synthetic-selected-reviewer", Protocol: "openai-chat", ReasoningEffort: "none"}
	_, err := executeReviewerFormatV2(t, acts, in)
	var applicationErr *temporal.ApplicationError
	require.ErrorAs(t, err, &applicationErr)
	require.Equal(t, "ReviewerTruncatedError", applicationErr.Type())
	require.Len(t, provider.requests, 1)
	require.Equal(t, 8192, provider.requests[0].MaxTokens)
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
			for _, step := range []string{"s3_strict_reviewer_v2", "s3_strict_reviewer_v2_format_repair_1", "s3_strict_reviewer_v3", "s3_strict_reviewer_v3_format_repair_1", "s3_strict_reviewer_v3_reasoning_budget_retry_1", "s3_strict_reviewer_v3_reasoning_budget_retry_1_format_repair_1", "s3_strict_reviewer_v3_format_repair_1_reasoning_budget_retry_1"} {
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
	require.Len(t, cache, len(legacy)+2, "v4 initial and correction effects must not collide with older contexts")
	for key, entry := range legacy {
		require.Equal(t, entry, cache[key], "existing v2 evidence must remain unchanged")
	}
	for i, request := range provider.requests {
		require.Equal(t, 32768, request.MaxTokens)
		require.Equal(t, "high", request.Runtime.ReasoningEffort)
		require.Equal(t, "synthetic-selected-reviewer", request.Model)
		require.Equal(t, "v4", request.PromptVersion)
		require.Equal(t, []string{"s3_strict_reviewer_v4", "s3_strict_reviewer_v4_format_repair_1"}[i], request.PromptID)
		require.Equal(t, first.SourceArtifacts[i].SHA256, result.SourceArtifacts[i].SHA256)
	}
	require.False(t, *result.Reviewer.Assessment.Approved)
	require.Equal(t, 5, *result.Reviewer.Assessment.Dimensions.Correctness.Score)
}

type reviewerProviderFuncV3 func(context.Context, *llm.Request, int) (*llm.Response, error)

func (f reviewerProviderFuncV3) CompleteWithRetry(ctx context.Context, request *llm.Request, retries int) (*llm.Response, error) {
	return f(ctx, request, retries)
}

func TestS3ReviewerHeartbeatV3CoversSlowInitialCallAndFormatCorrection(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises two real 65-second provider waits across the 60-second heartbeat limit")
	}
	valid := reviewerAssessmentJSONV2(t, 5)
	bad := valid[:len(valid)-1] + `,"unexpected":true}`
	responses := []*llm.Response{reviewerFormatResponseV2(bad, "stop"), reviewerFormatResponseV2(valid, "stop")}
	acts, _, in := reviewerFormatFixtureV2(t, &reviewerFormatLLMV2{})
	var heartbeatCount atomic.Int32
	var lastHeartbeat atomic.Int64
	type observation struct {
		promptID   string
		elapsed    time.Duration
		heartbeats int32
		recent     time.Duration
	}
	var observed []observation
	acts.deps.LLM = reviewerProviderFuncV3(func(ctx context.Context, request *llm.Request, _ int) (*llm.Response, error) {
		index := len(observed)
		if index >= len(responses) {
			return nil, fmt.Errorf("synthetic slow reviewer exhausted")
		}
		started, before := time.Now(), heartbeatCount.Load()
		timer := time.NewTimer(65 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		current := observation{promptID: request.PromptID, elapsed: time.Since(started), heartbeats: heartbeatCount.Load() - before, recent: time.Since(time.Unix(0, lastHeartbeat.Load()))}
		observed = append(observed, current)
		// The test activity environment coalesces outgoing heartbeats at about
		// 30 seconds even though the activity records one every 15 seconds.
		// Two fresh renewals over a 65-second call prove it cannot rely on the
		// one initial heartbeat that caused the production timeout.
		if current.heartbeats < 2 || current.recent >= 30*time.Second {
			return nil, fmt.Errorf("synthetic 60-second heartbeat deadline would expire during %s: %d beats, last heartbeat %s ago", request.PromptID, current.heartbeats, current.recent)
		}
		return responses[index], nil
	})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.SetTestTimeout(3 * time.Minute)
	env.SetWorkerOptions(worker.Options{DefaultHeartbeatThrottleInterval: time.Millisecond, MaxHeartbeatThrottleInterval: time.Millisecond})
	env.SetOnActivityHeartbeatListener(func(_ *activity.Info, _ converter.EncodedValues) {
		lastHeartbeat.Store(time.Now().UnixNano())
		heartbeatCount.Add(1)
	})
	env.RegisterActivity(acts.S3StrictReviewerActivityV1)
	value, err := env.ExecuteActivity(acts.S3StrictReviewerActivityV1, in)
	require.NoError(t, err)
	var result S3StrictReviewerResultV1
	require.NoError(t, value.Get(&result))
	require.Len(t, observed, 2)
	for i, current := range observed {
		require.Equal(t, []string{"s3_strict_reviewer_v4", "s3_strict_reviewer_v4_format_repair_1"}[i], current.promptID)
		require.Greater(t, current.elapsed, time.Minute)
		require.GreaterOrEqual(t, current.heartbeats, int32(2))
		require.Less(t, current.recent, 30*time.Second)
	}
	require.Len(t, result.SourceArtifacts, 2)
	require.False(t, *result.Reviewer.Assessment.Approved)
}

func TestS3ReviewerHeartbeatV3CancellationReleasesSlowProvider(t *testing.T) {
	acts, _, in := reviewerFormatFixtureV2(t, &reviewerFormatLLMV2{})
	entered, exited := make(chan struct{}), make(chan struct{})
	acts.deps.LLM = reviewerProviderFuncV3(func(ctx context.Context, _ *llm.Request, _ int) (*llm.Response, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	cancelReady := make(chan context.CancelFunc, 1)
	env.RegisterActivityWithOptions(func(ctx context.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cancelReady <- cancel
		return acts.S3StrictReviewerActivityV1(callCtx, input)
	}, activity.RegisterOptions{Name: "syntheticReviewerHeartbeatCancellation"})
	done := make(chan error, 1)
	go func() {
		_, err := env.ExecuteActivity("syntheticReviewerHeartbeatCancellation", in)
		done <- err
	}()
	var cancel context.CancelFunc
	select {
	case cancel = <-cancelReady:
		defer cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic reviewer did not start")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic provider did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("reviewer did not return after cancellation")
	}
	select {
	case <-exited:
	default:
		t.Fatal("provider did not observe cancellation")
	}
}
