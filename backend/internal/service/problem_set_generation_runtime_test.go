package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/mocks"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type runtimeSetStore struct {
	problemSetStore
	problemSetGenerationStore
	set    *domain.ProblemSet
	writes int
}

func (s *runtimeSetStore) GetByID(context.Context, uuid.UUID) (*domain.ProblemSet, error) {
	return s.set, nil
}
func (s *runtimeSetStore) ListItems(context.Context, uuid.UUID) ([]domain.ProblemSetItem, error) {
	return s.set.Items, nil
}
func (s *runtimeSetStore) ChangeGeneration(_ context.Context, _ domain.ProblemSetGenerationRef, change func(*domain.ProblemSetGenerationState) error) error {
	s.writes++
	return change(s.set.Generation)
}
func setRuntimeFixture(status string) (*ProblemSetGenerationService, *runtimeSetStore, *mocks.Client, *workflowservice.DescribeWorkflowExecutionResponse) {
	state := &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: status, StartedAt: time.Now().Add(-30 * time.Minute), Failure: &domain.GenerationFailure{Code: "retained", Message: "Do not rewrite durable evidence"}}
	repo := &runtimeSetStore{set: &domain.ProblemSet{ID: uuid.New(), Generation: state}}
	client := &mocks.Client{}
	svc := &ProblemSetGenerationService{sets: &ProblemSetService{repo: repo}, repo: repo, temporal: client, queue: "current-config-queue"}
	parent := &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, TaskQueue: "actual-submitted-queue"}}
	return svc, repo, client, parent
}
func mockParentRuntime(client *mocks.Client, repo *runtimeSetStore, response *workflowservice.DescribeWorkflowExecutionResponse) {
	client.On("DescribeWorkflowExecution", mock.Anything, setWorkflowID(domain.ProblemSetGenerationRef{SetID: repo.set.ID, RunID: repo.set.Generation.ID}), "").Return(response, nil).Once()
}

func TestSetRuntimeQueuedObservesWorkerWithoutChangingDurableRun(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pollerAge  *time.Duration
		probeError error
		pending    enumspb.PendingWorkflowTaskState
		want       string
	}{
		{name: "no recent poller", pending: enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED, want: "waiting_for_worker"},
		{name: "stale poller", pollerAge: durationPtr(3 * time.Minute), pending: enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED, want: "waiting_for_worker"},
		{name: "recent poller", pollerAge: durationPtr(time.Second), pending: enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED, want: "queued"},
		{name: "probe unavailable", probeError: fmt.Errorf("synthetic transport unavailable"), pending: enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED, want: "unknown"},
		{name: "workflow already started", pending: enumspb.PENDING_WORKFLOW_TASK_STATE_STARTED, want: "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, client, parent := setRuntimeFixture("queued")
			parent.PendingWorkflowTask = &workflowpb.PendingWorkflowTaskInfo{State: tc.pending}
			mockParentRuntime(client, repo, parent)
			if tc.pending == enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED {
				response := &workflowservice.DescribeTaskQueueResponse{}
				if tc.pollerAge != nil {
					response.Pollers = []*taskqueuepb.PollerInfo{{Identity: "synthetic-worker", LastAccessTime: timestamppb.New(time.Now().Add(-*tc.pollerAge))}}
				}
				client.On("DescribeTaskQueue", mock.Anything, "actual-submitted-queue", enumspb.TASK_QUEUE_TYPE_WORKFLOW).Return(response, tc.probeError).Once()
			}
			before, _ := json.Marshal(repo.set.Generation)
			result, err := svc.Status(context.Background(), repo.set.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, result.Runtime.State)
			require.GreaterOrEqual(t, result.Runtime.ElapsedSeconds, int64(1800))
			require.Equal(t, "queued", result.Status)
			require.Equal(t, repo.set.Generation.ID, result.ID)
			require.Equal(t, repo.set.Generation.Failure, result.Failure)
			if tc.want == "waiting_for_worker" {
				require.Contains(t, result.Runtime.Message, "尚未开始执行")
			}
			after, _ := json.Marshal(repo.set.Generation)
			require.Equal(t, string(before), string(after))
			require.Zero(t, repo.writes)
			client.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "CancelWorkflow", mock.Anything, mock.Anything, mock.Anything)
			client.AssertExpectations(t)
		})
	}
}
func durationPtr(value time.Duration) *time.Duration { return &value }

func TestSetRuntimePlanningReportsActualAttemptStartAndHeartbeat(t *testing.T) {
	svc, repo, client, parent := setRuntimeFixture("planning")
	started := time.Now().UTC().Add(-12 * time.Second)
	heartbeat := time.Now().UTC().Add(-time.Second)
	parent.PendingActivities = []*workflowpb.PendingActivityInfo{{ActivityType: &commonpb.ActivityType{Name: "PrepareProblemSetBatchActivity"}, State: enumspb.PENDING_ACTIVITY_STATE_STARTED, LastStartedTime: timestamppb.New(started), LastHeartbeatTime: timestamppb.New(heartbeat), Attempt: 2}}
	mockParentRuntime(client, repo, parent)
	svc.SetRuntimeWarningsResolver(func(context.Context) ([]string, error) { return []string{"未执行相似题去重"}, nil })
	result, err := svc.Status(context.Background(), repo.set.ID)
	require.NoError(t, err)
	require.Equal(t, "planning", result.Runtime.State)
	require.Equal(t, "PrepareProblemSetBatchActivity", result.Runtime.Activity)
	require.Equal(t, int32(2), result.Runtime.Attempt)
	require.True(t, started.Equal(*result.Runtime.ActivityStartedAt))
	require.True(t, heartbeat.Equal(*result.Runtime.LastHeartbeatAt))
	require.Equal(t, []string{"未执行相似题去重"}, result.Runtime.Warnings)
	require.Contains(t, result.Runtime.Message, "正在规划")
	require.Nil(t, repo.set.Generation.Runtime)
	require.Zero(t, repo.writes)
	client.AssertExpectations(t)
}

func TestSetRuntimeChildMonitoringIsBoundedAndKeepsOtherSlots(t *testing.T) {
	svc, repo, client, parent := setRuntimeFixture("generating")
	mockParentRuntime(client, repo, parent)
	started := time.Now().UTC().Add(-10 * time.Second)
	heartbeat := time.Now().UTC().Add(-2 * time.Second)
	for i := 0; i < 6; i++ {
		childID := fmt.Sprintf("synthetic-child-%d", i)
		repo.set.Generation.Slots = append(repo.set.Generation.Slots, domain.ProblemSetGenerationSlot{Position: i + 1, Status: "running", ChildID: childID})
		if i < 4 {
			response := &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING}, PendingActivities: []*workflowpb.PendingActivityInfo{{ActivityType: &commonpb.ActivityType{Name: "GenerateMainSolutionActivityV1"}, State: enumspb.PENDING_ACTIVITY_STATE_STARTED, LastStartedTime: timestamppb.New(started), LastHeartbeatTime: timestamppb.New(heartbeat), Attempt: 3}}}
			if i == 1 {
				client.On("DescribeWorkflowExecution", mock.Anything, childID, "").Return(nil, fmt.Errorf("synthetic child probe unavailable")).Once()
			} else {
				client.On("DescribeWorkflowExecution", mock.Anything, childID, "").Return(response, nil).Once()
			}
		}
	}
	before, _ := json.Marshal(repo.set.Generation)
	result, err := svc.Status(context.Background(), repo.set.ID)
	require.NoError(t, err)
	require.Equal(t, "running", result.Slots[0].Execution.State)
	require.Equal(t, "编写参考程序", result.Slots[0].Execution.Stage)
	require.GreaterOrEqual(t, result.Slots[0].Execution.ElapsedSeconds, int64(10))
	require.Equal(t, int32(3), result.Slots[0].Execution.Attempt)
	require.Equal(t, "unknown", result.Slots[1].Execution.State)
	require.Equal(t, "unknown", result.Slots[4].Execution.State)
	after, _ := json.Marshal(repo.set.Generation)
	require.Equal(t, string(before), string(after))
	require.Zero(t, repo.writes)
	client.AssertNotCalled(t, "DescribeWorkflowExecution", mock.Anything, "synthetic-child-4", "")
	client.AssertNotCalled(t, "DescribeTaskQueue", mock.Anything, mock.Anything, mock.Anything)
	client.AssertExpectations(t)
}

func TestSetRuntimeMonitoringTimeoutLeavesOriginalTaskRunning(t *testing.T) {
	svc, repo, client, _ := setRuntimeFixture("generating")
	client.On("DescribeWorkflowExecution", mock.Anything, mock.Anything, "").Return(func(ctx context.Context, _ string, _ string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}).Once()
	started := time.Now()
	result, err := svc.Status(context.Background(), repo.set.ID)
	require.NoError(t, err)
	require.Less(t, time.Since(started), 2*time.Second)
	require.Equal(t, "unknown", result.Runtime.State)
	require.Equal(t, "generating", result.Status)
	require.Equal(t, repo.set.Generation.ID, result.ID)
	require.Nil(t, repo.set.Generation.Runtime)
	require.Zero(t, repo.writes)
	client.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	client.AssertExpectations(t)
}

func TestSetRuntimeQueuedRetryDoesNotReusePreviousAttemptStart(t *testing.T) {
	pending := &workflowpb.PendingActivityInfo{ActivityType: &commonpb.ActivityType{Name: "S3StrictReviewerActivityV1"}, State: enumspb.PENDING_ACTIVITY_STATE_SCHEDULED, LastStartedTime: timestamppb.New(time.Now().Add(-time.Minute)), Attempt: 2}
	result := pendingActivityExecution(pending, time.Now())
	require.Equal(t, "queued", result.State)
	require.Nil(t, result.ActivityStartedAt)
	require.Zero(t, result.ElapsedSeconds)
	require.Equal(t, int32(2), result.Attempt)
}

func TestSetRuntimeStartReturnsSubmittedObservationWithoutRejectingQueue(t *testing.T) {
	svc, repo, client, _ := setRuntimeFixture("queued")
	repo.set.DesiredItemCount = 1
	repo.set.GenerationConfig = &domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 1, Score: 100}}}
	svc.resolve = func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{}, nil
	}
	svc.SetRuntimeWarningsResolver(func(context.Context) ([]string, error) { return []string{"当前未配置向量服务"}, nil })
	client.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	result, err := svc.Start(context.Background(), repo.set.ID)
	require.NoError(t, err)
	require.Equal(t, "queued", result.Runtime.State)
	require.Equal(t, []string{"当前未配置向量服务"}, result.Runtime.Warnings)
	require.Nil(t, repo.set.Generation.Runtime)
	require.Equal(t, repo.set.Generation.ID, result.ID)
	client.AssertNotCalled(t, "DescribeTaskQueue", mock.Anything, mock.Anything, mock.Anything)
	client.AssertExpectations(t)
}

func TestSetRuntimeTerminalDoesNotUseCurrentSettingsForHistoricalWarnings(t *testing.T) {
	svc, repo, client, _ := setRuntimeFixture("completed")
	called := false
	svc.SetRuntimeWarningsResolver(func(context.Context) ([]string, error) {
		called = true
		return []string{"当前未配置向量服务"}, nil
	})
	result, err := svc.Status(context.Background(), repo.set.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", result.Status)
	require.Nil(t, result.Runtime)
	require.False(t, called)
	require.Zero(t, repo.writes)
	client.AssertNotCalled(t, "DescribeWorkflowExecution", mock.Anything, mock.Anything, mock.Anything)
}

func TestSetRuntimeChildTimeoutsRunTogetherWithinMonitoringBudget(t *testing.T) {
	svc, repo, client, parent := setRuntimeFixture("generating")
	mockParentRuntime(client, repo, parent)
	for i := 0; i < problemSetRuntimeChildLimit; i++ {
		childID := fmt.Sprintf("synthetic-slow-child-%d", i)
		repo.set.Generation.Slots = append(repo.set.Generation.Slots, domain.ProblemSetGenerationSlot{Position: i + 1, Status: "running", ChildID: childID})
		client.On("DescribeWorkflowExecution", mock.Anything, childID, "").Return(func(ctx context.Context, _ string, _ string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}).Once()
	}
	started := time.Now()
	result, err := svc.Status(context.Background(), repo.set.ID)
	require.NoError(t, err)
	require.Less(t, time.Since(started), problemSetRuntimeBudget)
	for _, slot := range result.Slots {
		require.Equal(t, "unknown", slot.Execution.State)
	}
	require.Zero(t, repo.writes)
	client.AssertExpectations(t)
}
