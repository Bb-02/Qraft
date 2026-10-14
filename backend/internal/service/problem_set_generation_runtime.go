package service

import (
	"context"
	"sync"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	problemSetRuntimeBudget       = 2 * time.Second
	problemSetRuntimeRPCTimeout   = 800 * time.Millisecond
	problemSetRuntimePollerWindow = 2 * time.Minute
	problemSetRuntimeChildLimit   = 4
)

// SetRuntimeWarningsResolver reports optional configured features without
// treating an omitted check as a failed generation or changing a run.
func (s *ProblemSetGenerationService) SetRuntimeWarningsResolver(resolve func(context.Context) ([]string, error)) {
	s.runtimeWarnings = resolve
}

func copySetRuntimeState(state *domain.ProblemSetGenerationState) *domain.ProblemSetGenerationState {
	copy := *state
	copy.Runtime = nil
	copy.Slots = append([]domain.ProblemSetGenerationSlot(nil), state.Slots...)
	for i := range copy.Slots {
		copy.Slots[i].Execution = nil
	}
	return &copy
}

func runtimeElapsed(now time.Time, started time.Time) int64 {
	if started.IsZero() || now.Before(started) {
		return 0
	}
	return int64(now.Sub(started) / time.Second)
}

func runtimeTimestamp(stamp *timestamppb.Timestamp) *time.Time {
	if stamp == nil || stamp.CheckValid() != nil {
		return nil
	}
	value := stamp.AsTime().UTC()
	return &value
}

func (s *ProblemSetGenerationService) addRuntimeWarnings(ctx context.Context, runtime *domain.ProblemSetGenerationRuntime) {
	if s.runtimeWarnings == nil {
		return
	}
	warningCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	warnings, err := s.runtimeWarnings(warningCtx)
	if err == nil {
		runtime.Warnings = append([]string(nil), warnings...)
	}
}

func (s *ProblemSetGenerationService) submittedSetRuntime(ctx context.Context, state *domain.ProblemSetGenerationState) *domain.ProblemSetGenerationState {
	copy := copySetRuntimeState(state)
	copy.Runtime = &domain.ProblemSetGenerationRuntime{State: state.Status, ElapsedSeconds: runtimeElapsed(time.Now(), state.StartedAt),
		Message: "题集任务已提交，等待执行进度更新；当前状态会持续刷新。"}
	warningCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	s.addRuntimeWarnings(warningCtx, copy.Runtime)
	return copy
}

func (s *ProblemSetGenerationService) unknownSetRuntime(ctx context.Context, state *domain.ProblemSetGenerationState, message string) *domain.ProblemSetGenerationState {
	copy := copySetRuntimeState(state)
	copy.Runtime = &domain.ProblemSetGenerationRuntime{State: "unknown", Message: message, ElapsedSeconds: runtimeElapsed(time.Now(), state.StartedAt)}
	for i := range copy.Slots {
		if copy.Slots[i].Status == "running" {
			copy.Slots[i].Execution = &domain.ProblemSetSlotExecution{State: "unknown", Stage: "状态暂时无法确认", Message: "暂时无法确认这道题的执行状态，任务记录会保留。"}
		}
	}
	s.addRuntimeWarnings(ctx, copy.Runtime)
	return copy
}

func (s *ProblemSetGenerationService) observeSetRuntime(ctx context.Context, state *domain.ProblemSetGenerationState, parent *workflowservice.DescribeWorkflowExecutionResponse) *domain.ProblemSetGenerationState {
	copy := copySetRuntimeState(state)
	now := time.Now().UTC()
	runtime := &domain.ProblemSetGenerationRuntime{State: state.Status, ElapsedSeconds: runtimeElapsed(now, state.StartedAt)}
	copy.Runtime = runtime
	switch state.Status {
	case "queued":
		runtime.Message = "题集任务已提交，正在等待开始执行。"
	case "planning":
		runtime.Message = "正在规划整套题目，规划结果会持续保存。"
	case "generating":
		runtime.Message = "正在逐题生成和检查题目，已完成的结果会保留。"
	}
	if pending := currentPendingActivity(parent.PendingActivities); pending != nil {
		execution := pendingActivityExecution(pending, now)
		runtime.Activity, runtime.ActivityStartedAt, runtime.Attempt, runtime.LastHeartbeatAt = execution.Activity, execution.ActivityStartedAt, execution.Attempt, execution.LastHeartbeatAt
		runtime.Message = execution.Message
	} else if state.Status == "queued" && parent.PendingWorkflowTask != nil && parent.PendingWorkflowTask.State == enumspb.PENDING_WORKFLOW_TASK_STATE_STARTED {
		runtime.Message = "执行服务已接手任务，正在准备下一步。"
	} else if state.Status == "queued" && parent.PendingWorkflowTask != nil && parent.PendingWorkflowTask.State == enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED {
		queue := parent.WorkflowExecutionInfo.TaskQueue
		if queue == "" {
			queue = s.queue
		}
		probeCtx, cancel := context.WithTimeout(ctx, problemSetRuntimeRPCTimeout)
		response, err := s.temporal.DescribeTaskQueue(probeCtx, queue, enumspb.TASK_QUEUE_TYPE_WORKFLOW)
		cancel()
		if err != nil || response == nil {
			runtime.State, runtime.Message = "unknown", "任务仍在等待开始，但暂时无法确认是否有执行服务接单；原任务会保留。"
		} else {
			recent := false
			for _, poller := range response.Pollers {
				if poller == nil {
					continue
				}
				access := runtimeTimestamp(poller.LastAccessTime)
				if access != nil && !access.After(now.Add(time.Minute)) && now.Sub(*access) <= problemSetRuntimePollerWindow {
					recent = true
					break
				}
			}
			if !recent {
				runtime.State = "waiting_for_worker"
				runtime.Message = "题集尚未开始执行：最近未发现执行服务领取任务，可能尚未启动、连接中断或繁忙。原任务已保留，服务恢复后会继续。"
			}
		}
	}
	if state.Status == "generating" {
		s.observeRunningSetSlots(ctx, copy, now)
	}
	s.addRuntimeWarnings(ctx, runtime)
	return copy
}

func (s *ProblemSetGenerationService) observeRunningSetSlots(ctx context.Context, state *domain.ProblemSetGenerationState, now time.Time) {
	var done sync.WaitGroup
	checked := 0
	for i := range state.Slots {
		slot := &state.Slots[i]
		if slot.Status != "running" {
			continue
		}
		slot.Execution = &domain.ProblemSetSlotExecution{State: "unknown", Stage: "等待执行进度", Message: "这道题的执行进度尚未确认，稍后刷新查看。"}
		if slot.ChildID == "" || checked == problemSetRuntimeChildLimit {
			continue
		}
		checked++
		done.Add(1)
		go func(slot *domain.ProblemSetGenerationSlot) {
			defer done.Done()
			probeCtx, cancel := context.WithTimeout(ctx, problemSetRuntimeRPCTimeout)
			defer cancel()
			child, err := s.temporal.DescribeWorkflowExecution(probeCtx, slot.ChildID, "")
			if err != nil || child == nil || child.WorkflowExecutionInfo == nil {
				slot.Execution.Message = "暂时无法确认这道题的执行状态，任务可能仍在继续；稍后刷新查看。"
				return
			}
			if pending := currentPendingActivity(child.PendingActivities); pending != nil {
				slot.Execution = pendingActivityExecution(pending, now)
				return
			}
			if child.PendingWorkflowTask != nil && child.PendingWorkflowTask.State == enumspb.PENDING_WORKFLOW_TASK_STATE_SCHEDULED {
				slot.Execution = &domain.ProblemSetSlotExecution{State: "queued", Stage: "等待下一步开始", Message: "这道题的下一步已排队，尚未开始执行。"}
				return
			}
			if child.WorkflowExecutionInfo.Status == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
				slot.Execution = &domain.ProblemSetSlotExecution{State: "running", Stage: "推进生成步骤", Message: "正在推进生成步骤，等待下一次进度更新。"}
			}
		}(slot)
	}
	done.Wait()
}

func currentPendingActivity(pending []*workflowpb.PendingActivityInfo) *workflowpb.PendingActivityInfo {
	var chosen *workflowpb.PendingActivityInfo
	for _, activity := range pending {
		if activity == nil {
			continue
		}
		if chosen == nil || (chosen.State != enumspb.PENDING_ACTIVITY_STATE_STARTED && activity.State == enumspb.PENDING_ACTIVITY_STATE_STARTED) {
			chosen = activity
		}
	}
	return chosen
}

func pendingActivityExecution(pending *workflowpb.PendingActivityInfo, now time.Time) *domain.ProblemSetSlotExecution {
	activity := ""
	if pending.ActivityType != nil {
		activity = pending.ActivityType.Name
	}
	stage := problemSetActivityStage(activity)
	execution := &domain.ProblemSetSlotExecution{State: "queued", Stage: stage, Message: stage + "的步骤正在等待执行。", Activity: activity, Attempt: pending.Attempt, LastHeartbeatAt: runtimeTimestamp(pending.LastHeartbeatTime)}
	if pending.State == enumspb.PENDING_ACTIVITY_STATE_STARTED || pending.State == enumspb.PENDING_ACTIVITY_STATE_CANCEL_REQUESTED {
		execution.State, execution.Message = "running", "正在"+stage+"。"
		execution.ActivityStartedAt = runtimeTimestamp(pending.LastStartedTime)
		if execution.ActivityStartedAt != nil {
			execution.ElapsedSeconds = runtimeElapsed(now, *execution.ActivityStartedAt)
		}
	}
	return execution
}

func problemSetActivityStage(activity string) string {
	stages := map[string]string{
		"PrepareProblemSetBatchActivity":                  "规划整套题目",
		"ResolveS3RepairPolicyActivityV1":                 "准备检查流程",
		"BuildCanonicalAuthoringBriefActivityV1":          "整理出题需求",
		"GenerateAuthoringPlanActivity":                   "设计题意与解法",
		"BuildS3SpecLintGateActivityV1":                   "检查题意与解法设计",
		"RenderStatementFromAuthoringBundleActivityV1":    "编写题面",
		"ValidateAuthoringSampleCandidatesActivityV1":     "检查样例输入",
		"ExtractSemanticSpecArtifactActivityV1":           "准备独立验算",
		"GenerateMainSolutionActivityV1":                  "编写参考程序",
		"GenerateOracleCandidateActivityV1":               "编写独立验算程序",
		"GenerateS3TestDataActivityV1":                    "生成测试数据",
		"MaterializeS3CasePlanActivityV1":                 "整理测试数据",
		"RunS3OracleDifferentialActivityV1":               "运行程序并进行对拍验证",
		"VerifyProgramAgainstIndependentOracleActivityV1": "运行程序并进行对拍验证",
		"RunVerifiedAuthoringSamplesActivityV1":           "运行样例并核对输出",
		"StageAuthoringStatementSamplesActivityV1":        "核对样例输出",
		"FinalizeAuthoringStatementSamplesActivityV1":     "核对样例输出",
		"BuildS3SampleOutputGateActivityV1":               "核对样例输出",
		"RunSanitizerGateActivityV1":                      "检查程序运行",
		"BuildS3TestManifestActivityV1":                   "检查测试数据",
		"S3DedupGateActivityV1":                           "检查相似题",
		"S3StrictReviewerActivityV1":                      "独立审核题目",
		"ResolveS3HiddenSuiteActivityV1":                  "准备附加验证",
		"S3HiddenRegressionActivityV1":                    "运行附加测试",
		"RecomputeS3VerdictActivityV1":                    "整理检查结果",
		"StoreS3QualityDraftActivityV1":                   "保存候选题与检查记录",
		"StoreS3ReviewCandidateActivityV1":                "保存候选题与检查记录",
		"RecordProblemSetSlotActivity":                    "保存逐题进度",
		"AttachProblemSetSlotActivity":                    "将题目加入题集",
		"FinishProblemSetGenerationActivity":              "汇总题集结果",
		"GenerateQuizActivity":                            "编写客观题",
		"ReviewProblemSetQuizActivity":                    "复核客观题",
		"StoreQuizActivity":                               "保存客观题",
	}
	if stage := stages[activity]; stage != "" {
		return stage
	}
	return "执行当前生成步骤"
}
