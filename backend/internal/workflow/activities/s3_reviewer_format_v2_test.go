package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

type reviewerFormatLLMV2 struct {
	responses []*llm.Response
	requests  []*llm.Request
}

func (f *reviewerFormatLLMV2) CompleteWithRetry(_ context.Context, req *llm.Request, _ int) (*llm.Response, error) {
	copy := *req
	copy.Messages = append([]llm.Message(nil), req.Messages...)
	f.requests = append(f.requests, &copy)
	if len(f.requests) > len(f.responses) {
		return nil, fmt.Errorf("synthetic fixture exhausted")
	}
	return f.responses[len(f.requests)-1], nil
}

func reviewerFormatResponseV2(text, stop string) *llm.Response {
	return &llm.Response{Model: "synthetic-reviewer", ModelObserved: true, StopReason: stop, Content: []llm.ContentBlock{{Type: "text", Text: text}}}
}

func reviewerAssessmentJSONV2(t *testing.T, correctness int) string {
	t.Helper()
	dimension := func(score int) *qualitygate.ReviewDimensionV1 {
		return &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(score), Notes: "Synthetic review observation."}
	}
	value := qualitygate.ReviewAssessmentV1{SchemaVersion: qualitygate.ReviewAssessmentSchemaVersionV1, Approved: qualitygate.BoolV1(false), Blockers: []qualitygate.ReviewBlockerV1{}, Dimensions: &qualitygate.ReviewDimensionsV1{Clarity: dimension(8), Correctness: dimension(correctness), TestCoverage: dimension(8), DifficultyCalibration: dimension(4), TagAccuracy: dimension(8)}}
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func reviewerFormatFixtureV2(t *testing.T, provider *reviewerFormatLLMV2) (*Activities, *oracleMemoryArtifactStore, S3StrictReviewerInputV1) {
	t.Helper()
	store := newOracleMemoryArtifactStore()
	_, _, manifest, _ := validIntegerCoverageFixtureV1(t)
	encoded, _, err := CanonicalTestManifestV2JSON(manifest)
	require.NoError(t, err)
	in := S3StrictReviewerInputV1{PayloadVersion: S3QualityPayloadVersionV1, SubjectID: "synthetic-reviewer-format", SubjectRevision: strings.Repeat("b", 64), TestManifestArtifact: s3SeedArtifactForTest(store, encoded, "application/json", "BuildS3TestManifestActivityV1")}
	for _, role := range []string{"semantic_spec", "final_statement", "oracle_promotion", "sanitizer", "boundary_coverage"} {
		data, _ := json.Marshal(map[string]string{"synthetic_visible_role": role})
		ref := s3SeedArtifactForTest(store, data, "application/json", "fixture")
		in.VisibleAssets = append(in.VisibleAssets, S3ReviewerAssetV1{Role: role, Artifact: ref, ExpectedSHA256: ref.SHA256})
	}
	return New(&Dependencies{LLM: provider, LLMProvider: "fixture-provider", LLMModel: "synthetic-reviewer", ArtifactStore: store, ProvenanceRecorder: &captureProvenanceRecorder{}}), store, in
}

func executeReviewerFormatV2(t *testing.T, acts *Activities, in S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.S3StrictReviewerActivityV1)
	value, err := env.ExecuteActivity(acts.S3StrictReviewerActivityV1, in)
	if err != nil {
		return nil, err
	}
	var result S3StrictReviewerResultV1
	require.NoError(t, value.Get(&result))
	return &result, nil
}

func TestS3ReviewerFormatV2RepairsOneMalformedReplyAndRetainsBothSources(t *testing.T) {
	valid := reviewerAssessmentJSONV2(t, 5)
	bad := strings.TrimSuffix(valid, "}") + `,"invented_confidence":0.9}`
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(bad, "end_turn"), reviewerFormatResponseV2(valid, "end_turn")}}
	acts, store, in := reviewerFormatFixtureV2(t, provider)
	result, err := executeReviewerFormatV2(t, acts, in)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2)
	require.Len(t, result.SourceArtifacts, 2)
	require.Equal(t, result.SourceArtifacts[1], result.ProviderArtifact)
	require.NotEqual(t, result.SourceArtifacts[0].SHA256, result.SourceArtifacts[1].SHA256)
	require.False(t, *result.Reviewer.Assessment.Approved)
	require.Equal(t, 5, *result.Reviewer.Assessment.Dimensions.Correctness.Score)
	for i, source := range result.SourceArtifacts {
		var recorded recordedLLMResponse
		require.NoError(t, json.Unmarshal(store.objects[source.SHA256], &recorded))
		require.Equal(t, []string{bad, valid}[i], recorded.Response.Text())
		require.Equal(t, "v3", recorded.CallReceipt.PromptVersion)
		require.Equal(t, []string{s3ReviewerPromptIDV3, s3ReviewerFormatRepairPromptIDV3}[i], recorded.CallReceipt.PromptID)
	}
	require.NotEqual(t, result.SourceArtifacts[0].LLMCallReceipt.RequestSHA256, result.SourceArtifacts[1].LLMCallReceipt.RequestSHA256)
	require.Contains(t, provider.requests[0].System, `"additionalProperties":false`)
	require.Contains(t, provider.requests[0].System, `"ReviewDimensionsV1"`)
	require.Contains(t, provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Content, "never raise scores")
}

func TestS3ReviewerFormatV2RejectsHiddenAssetsBeforeAnyModelCall(t *testing.T) {
	provider := &reviewerFormatLLMV2{}
	acts, store, in := reviewerFormatFixtureV2(t, provider)
	ref := s3SeedArtifactForTest(store, []byte("synthetic-hidden-secret"), "text/plain", "fixture")
	in.VisibleAssets = append(in.VisibleAssets, S3ReviewerAssetV1{Role: "hidden_regression", Artifact: ref, ExpectedSHA256: ref.SHA256})
	_, err := executeReviewerFormatV2(t, acts, in)
	require.ErrorContains(t, err, "reviewer asset role")
	require.Empty(t, provider.requests)
}

func TestS3ReviewerFormatV2StopsAfterOneCorrectionAndRejectsUnknownKeys(t *testing.T) {
	bad := strings.TrimSuffix(reviewerAssessmentJSONV2(t, 8), "}") + `,"unknown":true}`
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(bad, "end_turn"), reviewerFormatResponseV2(bad, "end_turn")}}
	acts, _, in := reviewerFormatFixtureV2(t, provider)
	_, err := executeReviewerFormatV2(t, acts, in)
	require.Error(t, err)
	var applicationErr *temporal.ApplicationError
	require.ErrorAs(t, err, &applicationErr)
	require.Equal(t, "ReviewerFormatError", applicationErr.Type())
	require.True(t, applicationErr.NonRetryable())
	require.Len(t, provider.requests, 2)
}

func TestS3ReviewerFormatV2CannotChangeKnownConclusionsDuringCorrection(t *testing.T) {
	valid := reviewerAssessmentJSONV2(t, 5)
	bad := strings.TrimSuffix(valid, "}") + `,"unsupported":true}`
	for _, tc := range []struct {
		name      string
		corrected string
	}{
		{name: "approved", corrected: strings.Replace(valid, `"approved":false`, `"approved":true`, 1)},
		{name: "correctness score", corrected: strings.Replace(valid, `"correctness":{"score":5`, `"correctness":{"score":8`, 1)},
		{name: "blocker evidence", corrected: strings.Replace(valid, `"blockers":[]`, `"blockers":[{"code":"synthetic.counterexample","responsible_asset":"cas://fixture/visible","witness":{"runner":"synthetic-runner","fixture_ref":"cas://fixture/input","assertion":"Known synthetic counterexample."}}]`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(bad, "end_turn"), reviewerFormatResponseV2(tc.corrected, "end_turn")}}
			acts, _, in := reviewerFormatFixtureV2(t, provider)
			_, err := executeReviewerFormatV2(t, acts, in)
			require.ErrorContains(t, err, "changed review conclusions")
			require.Len(t, provider.requests, 2)
		})
	}
}

func TestS3ReviewerFormatV2DoesNotRegradeShapedReviewOrRepairTruncation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		stop    string
		errType string
	}{
		{name: "out of range score", text: reviewerAssessmentJSONV2(t, 11), stop: "end_turn", errType: s3QualityContractErrorTypeV1},
		{name: "empty notes", text: strings.Replace(reviewerAssessmentJSONV2(t, 8), `"notes":"Synthetic review observation."`, `"notes":""`, 1), stop: "end_turn", errType: s3QualityContractErrorTypeV1},
		{name: "invalid witness", text: strings.Replace(reviewerAssessmentJSONV2(t, 8), `"blockers":[]`, `"blockers":[{"code":"synthetic.counterexample","responsible_asset":"cas://fixture/visible","witness":{"runner":"invalid runner","fixture_ref":"cas://fixture/input","assertion":"Synthetic assertion."}}]`, 1), stop: "end_turn", errType: s3QualityContractErrorTypeV1},
		{name: "truncated", text: `{"schema_version":`, stop: "max_tokens", errType: "ReviewerTruncatedError"},
		{name: "openai length", text: `{"schema_version":`, stop: "length", errType: "ReviewerTruncatedError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(tc.text, tc.stop)}}
			acts, _, in := reviewerFormatFixtureV2(t, provider)
			_, err := executeReviewerFormatV2(t, acts, in)
			require.Error(t, err)
			var applicationErr *temporal.ApplicationError
			require.ErrorAs(t, err, &applicationErr)
			require.Equal(t, tc.errType, applicationErr.Type())
			require.True(t, applicationErr.NonRetryable())
			require.Len(t, provider.requests, 1)
		})
	}
}

func TestS3ReviewerFormatV2SeparatesStrictAndTrainingEstimateGuidance(t *testing.T) {
	strict, err := s3ReviewerOutputPromptV2(false)
	require.NoError(t, err)
	training, err := s3ReviewerOutputPromptV2(true)
	require.NoError(t, err)
	require.NotContains(t, strict, `"estimated_difficulty"`)
	require.Contains(t, training, `"estimated_difficulty"`)
	valid := strings.TrimSuffix(reviewerAssessmentJSONV2(t, 8), "}") + `,"estimated_difficulty":"unavailable","difficulty_explanation":null}`
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(valid, "end_turn")}}
	acts, _, in := reviewerFormatFixtureV2(t, provider)
	in.TargetDifficulty = 400
	result, err := executeReviewerFormatV2(t, acts, in)
	require.NoError(t, err)
	require.Nil(t, result.Reviewer.Assessment.EstimatedDifficulty)
	require.False(t, *result.Reviewer.Assessment.Approved)
	require.Len(t, provider.requests, 1, "optional estimate failure must not re-run a valid core review")
	require.Contains(t, provider.requests[0].System, s3TrainingReviewerGuidanceV1)
}

type reviewerFormatCachedCallV2 struct {
	request string
	result  json.RawMessage
}

type reviewerFormatCacheV2 map[string]reviewerFormatCachedCallV2

func (cache reviewerFormatCacheV2) Acquire(_ context.Context, key, _ string, request string, _ time.Duration) (repository.ProviderEffectClaim, error) {
	if cached, ok := cache[key]; ok {
		if cached.request != request {
			return repository.ProviderEffectClaim{}, fmt.Errorf("synthetic request mismatch")
		}
		return repository.ProviderEffectClaim{State: repository.ProviderEffectCompleted, ResultJSON: cached.result}, nil
	}
	cache[key] = reviewerFormatCachedCallV2{request: request}
	return repository.ProviderEffectClaim{State: repository.ProviderEffectAcquired, LeaseToken: uuid.New()}, nil
}

func (cache reviewerFormatCacheV2) Complete(_ context.Context, key, _ string, request string, _ uuid.UUID, result json.RawMessage) error {
	cache[key] = reviewerFormatCachedCallV2{request: request, result: append(json.RawMessage(nil), result...)}
	return nil
}

func (cache reviewerFormatCacheV2) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	return nil
}

func TestS3ReviewerFormatV2RedeliveryReusesBothIndependentProviderEffects(t *testing.T) {
	valid := reviewerAssessmentJSONV2(t, 5)
	bad := strings.TrimSuffix(valid, "}") + `,"unexpected":true}`
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(bad, "end_turn"), reviewerFormatResponseV2(valid, "end_turn")}}
	acts, _, in := reviewerFormatFixtureV2(t, provider)
	cache := make(reviewerFormatCacheV2)
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	var first *S3StrictReviewerResultV1
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		result, err := acts.S3StrictReviewerActivityV1(ctx, input)
		if err == nil && first == nil {
			first = result
			return nil, fmt.Errorf("synthetic activity redelivery after successful provider calls")
		}
		return result, err
	}, activity.RegisterOptions{Name: "syntheticReviewerRedelivery"})
	env.ExecuteWorkflow(func(ctx temporalworkflow.Context, input S3StrictReviewerInputV1) (*S3StrictReviewerResultV1, error) {
		ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2}})
		var result S3StrictReviewerResultV1
		err := temporalworkflow.ExecuteActivity(ctx, "syntheticReviewerRedelivery", input).Get(ctx, &result)
		return &result, err
	}, in)
	require.NoError(t, env.GetWorkflowError())
	var result S3StrictReviewerResultV1
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Len(t, provider.requests, 2, "redelivery must not repeat billable format corrections")
	require.Len(t, cache, 2, "first attempt and format correction must use distinct stable effects")
	require.Equal(t, first.SourceArtifacts[0].SHA256, result.SourceArtifacts[0].SHA256)
	require.Equal(t, first.SourceArtifacts[1].SHA256, result.SourceArtifacts[1].SHA256)
}
