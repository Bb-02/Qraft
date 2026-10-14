package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/google/uuid"
)

type workflowTaskSetReader interface {
	GetWorkflowTaskSet(context.Context, uuid.UUID) (*domain.ProblemSet, error)
}

type workflowTaskProblemReader interface {
	GetByWorkflowID(context.Context, string) (*domain.Problem, error)
}

type workflowTaskOwnerReader interface {
	Owner(context.Context, string) (string, error)
}

// WorkflowTaskResolver reads business records only. Ownership lookup uses
// durable database rows, never an additional Temporal query per list item.
type WorkflowTaskResolver struct {
	sets     workflowTaskSetReader
	problems workflowTaskProblemReader
	owners   workflowTaskOwnerReader
}

func NewWorkflowTaskResolver(sets workflowTaskSetReader, problems workflowTaskProblemReader, owners workflowTaskOwnerReader) *WorkflowTaskResolver {
	return &WorkflowTaskResolver{sets: sets, problems: problems, owners: owners}
}

func (r *WorkflowTaskResolver) Resolve(ctx context.Context, workflowID, workflowType, parentWorkflowID string) (*domain.WorkflowTaskSummary, error) {
	kind, title := workflowTaskTypeLabel(workflowType)
	task := &domain.WorkflowTaskSummary{Kind: kind, Title: title, IsChild: parentWorkflowID != "", ParentWorkflowID: parentWorkflowID}
	identity, authenticated := access.FromContext(ctx)
	if r == nil || !authenticated {
		return task, nil
	}
	setID, runID, isSetRoot := parseProblemSetTaskID(workflowID)
	if isSetRoot {
		set, err := r.readVisibleSet(ctx, identity, setID)
		if err != nil || set == nil {
			return task, err
		}
		task.Kind, task.Title = "problem_set", set.Title
		task.SourceURL, task.ResultURL = "/problem-sets/"+set.ID.String(), "/problem-sets/"+set.ID.String()
		if canReadWorkflowTaskGeneration(identity, set) && set.Generation != nil && set.Generation.ID == runID {
			applyCurrentSetTask(task, set)
		} else if canReadWorkflowTaskGeneration(identity, set) && set.Generation != nil && set.Generation.ID != runID {
			task.Summary = "这是已保存题集的一次历史生成，任务状态以本次执行记录为准。"
		} else {
			task.Summary = "题集已保存，本次任务状态以执行记录为准。"
		}
		return task, nil
	}
	var problem *domain.Problem
	if r.problems != nil && (kind == "problem_generation" || kind == "problem_import" || kind == "problem_validation" || parentWorkflowID != "") {
		candidate, err := r.problems.GetByWorkflowID(ctx, workflowID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
			return task, err
		}
		if candidate != nil {
			allowed, err := r.canReadProblem(ctx, identity, candidate)
			if err != nil {
				return task, err
			}
			if allowed {
				problem = candidate
				task.Title, task.ResultURL = candidate.Title, "/problems/"+candidate.ID.String()
				task.Summary = "题目已保存，可以查看题面和检查记录。"
			}
		}
	}
	parentSetID, parentRun, isSetChild := parseProblemSetTaskID(parentWorkflowID)
	position := 0
	if !isSetChild && problem != nil {
		parentSetID, position = workflowTaskProblemSetContext(problem)
	}
	if parentSetID == uuid.Nil {
		return task, nil
	}
	set, err := r.readVisibleSet(ctx, identity, parentSetID)
	if err != nil || set == nil {
		return task, err
	}
	task.Kind, task.IsChild, task.SourceURL = "problem", true, "/problem-sets/"+set.ID.String()
	current := canReadWorkflowTaskGeneration(identity, set) && set.Generation != nil && ((isSetChild && set.Generation.ID == parentRun) || !isSetChild)
	var slot *domain.ProblemSetGenerationSlot
	if current {
		for i := range set.Generation.Slots {
			if set.Generation.Slots[i].ChildID == workflowID {
				slot = &set.Generation.Slots[i]
				position = slot.Position
				if !isSetChild {
					task.ParentWorkflowID = setWorkflowID(domain.ProblemSetGenerationRef{SetID: set.ID, RunID: set.Generation.ID})
				}
				break
			}
		}
	}
	if position == 0 && problem != nil {
		contextSet, contextPosition := workflowTaskProblemSetContext(problem)
		if contextSet == set.ID {
			position = contextPosition
		}
	}
	if position > 0 {
		task.Position = &position
		name := "生成题目"
		if problem != nil {
			name = problem.Title
		} else if slot != nil && strings.TrimSpace(slot.Title) != "" {
			name = slot.Title
		}
		task.Title = fmt.Sprintf("%s · 第 %d 题：%s", set.Title, position, name)
	} else {
		task.Title = set.Title + " · 逐题生成"
	}
	if slot != nil {
		task.Status = slot.Status
		task.Summary = workflowTaskSlotSummary(slot)
	} else if problem == nil {
		task.Summary = "这道题属于该题集的一次生成，进度以本次执行记录为准。"
	}
	return task, nil
}

func workflowTaskSlotSummary(slot *domain.ProblemSetGenerationSlot) string {
	failure := generationapi.EnrichFailure(slot.Failure)
	if (failure == nil || failure.Code == "generation_failed") && strings.TrimSpace(slot.Error) != "" {
		failure = generationapi.DescribeFailure(errors.New(slot.Error))
	}
	if failure != nil && failure.Code != "" && failure.Code != "generation_failed" && strings.TrimSpace(failure.Message) != "" {
		return failure.Message
	}
	switch slot.Status {
	case "failed":
		return "这道题未完成，可以回到题集查看情况并重试。"
	case "awaiting_review":
		return "候选题已保存，等待人工审核后继续入集。"
	case "succeeded":
		return "这道题已完成并加入题集。"
	default:
		return "这道题正在等待或推进生成，已保存的结果会保留。"
	}
}

func (r *WorkflowTaskResolver) readVisibleSet(ctx context.Context, identity access.Principal, id uuid.UUID) (*domain.ProblemSet, error) {
	if r.sets == nil {
		return nil, nil
	}
	set, err := r.sets.GetWorkflowTaskSet(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil || set == nil {
		return nil, err
	}
	if !identity.IsAdmin() && set.Visibility != domain.ProblemSetVisibilityPublic && set.OwnerUserID != identity.UserID {
		return nil, nil
	}
	return set, nil
}

func (r *WorkflowTaskResolver) canReadProblem(ctx context.Context, identity access.Principal, problem *domain.Problem) (bool, error) {
	if identity.IsAdmin() || problem.Status == domain.ProblemStatusPublished {
		return true, nil
	}
	if r.owners == nil || problem.WorkflowID == nil || *problem.WorkflowID == "" {
		return false, nil
	}
	owner, err := r.owners.Owner(ctx, *problem.WorkflowID)
	return err == nil && owner == identity.UserID, err
}

func canReadWorkflowTaskGeneration(identity access.Principal, set *domain.ProblemSet) bool {
	return identity.IsAdmin() || set.OwnerUserID == identity.UserID
}

func applyCurrentSetTask(task *domain.WorkflowTaskSummary, set *domain.ProblemSet) {
	completed, total := 0, len(set.Generation.Slots)
	for _, slot := range set.Generation.Slots {
		if slot.Status == "succeeded" {
			completed++
		}
	}
	if total == 0 {
		total = set.DesiredItemCount
	}
	task.Status, task.CompletedCount, task.TotalCount = set.Generation.Status, &completed, &total
	task.Summary = fmt.Sprintf("已完成 %d / %d 道题，结果保存在题集页面。", completed, total)
}

func parseProblemSetTaskID(workflowID string) (uuid.UUID, string, bool) {
	const prefix = "problem-set-generation-"
	if !strings.HasPrefix(workflowID, prefix) {
		return uuid.Nil, "", false
	}
	value := strings.TrimPrefix(workflowID, prefix)
	if len(value) != 73 || value[36] != '-' {
		return uuid.Nil, "", false
	}
	setID, err := uuid.Parse(value[:36])
	if err != nil || setID == uuid.Nil {
		return uuid.Nil, "", false
	}
	runID, err := uuid.Parse(value[37:])
	if err != nil || runID == uuid.Nil || setID.String() != value[:36] || runID.String() != value[37:] {
		return uuid.Nil, "", false
	}
	return setID, runID.String(), true
}

func workflowTaskProblemSetContext(problem *domain.Problem) (uuid.UUID, int) {
	var metadata struct {
		SetID     string `json:"problem_set_id"`
		Position  int    `json:"problem_set_position"`
		Candidate *struct {
			Context *domain.ProblemSetGenerationContext `json:"context"`
		} `json:"generation_candidate"`
	}
	if json.Unmarshal(problem.MetadataJSON, &metadata) != nil {
		return uuid.Nil, 0
	}
	if metadata.Candidate != nil && metadata.Candidate.Context != nil && metadata.Candidate.Context.Validate() == nil {
		return metadata.Candidate.Context.SetID, metadata.Candidate.Context.Position
	}
	id, err := uuid.Parse(metadata.SetID)
	if err != nil || id == uuid.Nil || metadata.Position < 1 {
		return uuid.Nil, 0
	}
	return id, metadata.Position
}

func workflowTaskTypeLabel(workflowType string) (string, string) {
	switch workflowType {
	case "ProblemSetGenerationWorkflow":
		return "problem_set", "生成题集"
	case generationapi.QualityWorkflowTypeV1, "ProblemGenerationWorkflow":
		return "problem_generation", "生成编程题"
	case "ProblemValidationWorkflow":
		return "problem_validation", "检查题目"
	case "ProblemImportWorkflow", "ImportedProblemWorkflow":
		return "problem_import", "导入题目"
	case "QuizGenerationWorkflow", "ProblemSetQuizGenerationWorkflow":
		return "quiz_generation", "生成客观题"
	case "GPLTBatchGenerationWorkflow", "S5MicroBatchWorkflowV1":
		return "batch_generation", "批量生成题目"
	case "RatingWorkflow":
		return "rating", "评估题目难度"
	default:
		return "task", "处理任务"
	}
}
