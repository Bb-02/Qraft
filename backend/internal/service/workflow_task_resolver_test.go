package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type workflowTaskFixture struct {
	set     *domain.ProblemSet
	problem *domain.Problem
	owner   string
	reads   int
	err     error
}

func (f *workflowTaskFixture) GetWorkflowTaskSet(_ context.Context, id uuid.UUID) (*domain.ProblemSet, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	if f.set == nil || f.set.ID != id {
		return nil, sql.ErrNoRows
	}
	return f.set, nil
}
func (f *workflowTaskFixture) GetByWorkflowID(_ context.Context, id string) (*domain.Problem, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	if f.problem == nil || f.problem.WorkflowID == nil || *f.problem.WorkflowID != id {
		return nil, sql.ErrNoRows
	}
	return f.problem, nil
}
func (f *workflowTaskFixture) Owner(context.Context, string) (string, error) {
	f.reads++
	return f.owner, f.err
}

func newWorkflowTaskFixture(t *testing.T) (context.Context, *workflowTaskFixture, *WorkflowTaskResolver, string, string) {
	t.Helper()
	child := generationapi.JobIDPrefix + fmt.Sprintf("%064x", 1)
	set := &domain.ProblemSet{ID: uuid.New(), Title: "合成基础训练赛", Visibility: domain.ProblemSetVisibilityPrivate, OwnerUserID: "synthetic-member", DesiredItemCount: 2,
		Generation: &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "partial", Slots: []domain.ProblemSetGenerationSlot{
			{Position: 1, Status: "succeeded"}, {Position: 2, Status: "failed", Title: "基础模拟", ChildID: child, Failure: &domain.GenerationFailure{Code: "model_output_invalid", Message: "模型返回格式需要修正"}},
		}},
	}
	fixture := &workflowTaskFixture{set: set, owner: "synthetic-member"}
	root := setWorkflowID(domain.ProblemSetGenerationRef{SetID: set.ID, RunID: set.Generation.ID})
	return access.WithPrincipal(context.Background(), access.Principal{UserID: "synthetic-member", Role: "member"}), fixture, NewWorkflowTaskResolver(fixture, fixture, fixture), root, child
}

func TestWorkflowTaskResolverCurrentAndHistoricalSetRuns(t *testing.T) {
	ctx, fixture, resolver, root, _ := newWorkflowTaskFixture(t)
	task, err := resolver.Resolve(ctx, root, "ProblemSetGenerationWorkflow", "")
	require.NoError(t, err)
	require.Equal(t, fixture.set.Title, task.Title)
	require.Equal(t, "partial", task.Status)
	require.Equal(t, 1, *task.CompletedCount)
	require.Equal(t, 2, *task.TotalCount)
	require.Equal(t, "/problem-sets/"+fixture.set.ID.String(), task.ResultURL)
	require.Equal(t, 1, fixture.reads, "root summary needs one lightweight database read")
	old := setWorkflowID(domain.ProblemSetGenerationRef{SetID: fixture.set.ID, RunID: uuid.NewString()})
	task, err = resolver.Resolve(ctx, old, "ProblemSetGenerationWorkflow", "")
	require.NoError(t, err)
	require.Equal(t, fixture.set.Title, task.Title)
	require.Empty(t, task.Status)
	require.Nil(t, task.CompletedCount)
	require.Nil(t, task.TotalCount)
	require.Contains(t, task.Summary, "历史生成")
	require.Contains(t, task.Summary, "执行已结束不代表所有题目成功，请在题集查看结果")
}

func TestWorkflowTaskResolverCurrentChildHasRealSetTitleAndPosition(t *testing.T) {
	ctx, fixture, resolver, root, child := newWorkflowTaskFixture(t)
	task, err := resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, root)
	require.NoError(t, err)
	require.Equal(t, "problem", task.Kind)
	require.True(t, task.IsChild)
	require.Equal(t, root, task.ParentWorkflowID)
	require.Equal(t, 2, *task.Position)
	require.Equal(t, "合成基础训练赛 · 第 2 题：基础模拟", task.Title)
	require.Equal(t, "failed", task.Status)
	require.Equal(t, fixture.set.Generation.Slots[1].Failure.Message, task.Summary)
	require.Empty(t, task.ResultURL)
	require.Equal(t, "/problem-sets/"+fixture.set.ID.String(), task.SourceURL)
}

func TestWorkflowTaskResolverStoredOldCandidateDoesNotInheritNewRunStatus(t *testing.T) {
	ctx, fixture, resolver, _, child := newWorkflowTaskFixture(t)
	oldRoot := setWorkflowID(domain.ProblemSetGenerationRef{SetID: fixture.set.ID, RunID: uuid.NewString()})
	metadata, err := json.Marshal(map[string]interface{}{"generation_candidate": map[string]interface{}{"context": domain.ProblemSetGenerationContext{SetID: fixture.set.ID, Position: 2}}})
	require.NoError(t, err)
	fixture.problem = &domain.Problem{ID: uuid.New(), Title: "真实合成候选标题", WorkflowID: &child, Status: domain.ProblemStatusQuarantined, MetadataJSON: metadata}
	task, err := resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, oldRoot)
	require.NoError(t, err)
	require.Contains(t, task.Title, fixture.problem.Title)
	require.Equal(t, oldRoot, task.ParentWorkflowID)
	require.Equal(t, "/problems/"+fixture.problem.ID.String(), task.ResultURL)
	require.Equal(t, 2, *task.Position)
	require.Empty(t, task.Status, "old child must not display current slot status even if a stale slot retained its ID")
	// With no Temporal parent, only a precise current slot ID may infer a parent.
	task, err = resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, "")
	require.NoError(t, err)
	require.NotEmpty(t, task.ParentWorkflowID)
	require.Equal(t, "failed", task.Status)
	fixture.set.Generation.Slots[1].ChildID = "different-current-child"
	task, err = resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, "")
	require.NoError(t, err)
	require.Empty(t, task.ParentWorkflowID)
	require.Empty(t, task.Status)
	require.Contains(t, task.Title, fixture.problem.Title)
}

func TestWorkflowTaskResolverRechecksCurrentEntityVisibility(t *testing.T) {
	for _, tc := range []struct {
		name         string
		visibility   domain.ProblemSetVisibility
		canReadTitle bool
	}{
		{name: "set became private to another user", visibility: domain.ProblemSetVisibilityPrivate},
		{name: "public set hides private generation state", visibility: domain.ProblemSetVisibilityPublic, canReadTitle: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, fixture, resolver, root, _ := newWorkflowTaskFixture(t)
			fixture.set.OwnerUserID, fixture.set.Visibility = "different-member", tc.visibility
			task, err := resolver.Resolve(ctx, root, "ProblemSetGenerationWorkflow", "")
			require.NoError(t, err)
			require.Empty(t, task.Status)
			require.Nil(t, task.CompletedCount)
			if tc.canReadTitle {
				require.Equal(t, fixture.set.Title, task.Title)
				require.NotEmpty(t, task.SourceURL)
			} else {
				require.Equal(t, "生成题集", task.Title)
				require.Empty(t, task.SourceURL)
				require.Empty(t, task.ResultURL)
			}
		})
	}
	ctx, fixture, resolver, _, child := newWorkflowTaskFixture(t)
	fixture.problem = &domain.Problem{ID: uuid.New(), Title: "不可访问的私有候选", WorkflowID: &child, Status: domain.ProblemStatusDraft}
	fixture.owner = "different-member"
	task, err := resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, "")
	require.NoError(t, err)
	require.Equal(t, "生成编程题", task.Title)
	require.Empty(t, task.ResultURL)
	fixture.problem.Status = domain.ProblemStatusPublished
	task, err = resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, "")
	require.NoError(t, err)
	require.Equal(t, fixture.problem.Title, task.Title)
	require.NotEmpty(t, task.ResultURL)
}

func TestWorkflowTaskResolverNeverUsesUnavailableEntityContext(t *testing.T) {
	ctx, fixture, resolver, root, child := newWorkflowTaskFixture(t)
	task, err := resolver.Resolve(context.Background(), root, "ProblemSetGenerationWorkflow", "")
	require.NoError(t, err)
	require.Equal(t, "生成题集", task.Title)
	require.Zero(t, fixture.reads, "missing identity must not read business data")
	fixture.set = nil
	task, err = resolver.Resolve(ctx, root, "ProblemSetGenerationWorkflow", "")
	require.NoError(t, err)
	require.Equal(t, "生成题集", task.Title)
	require.Empty(t, task.SourceURL)
	fixture.err = fmt.Errorf("synthetic database unavailable")
	task, err = resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, "")
	require.Error(t, err)
	require.Equal(t, "生成编程题", task.Title)
	require.Empty(t, task.ResultURL)
}

func TestWorkflowTaskResolverRejectsMalformedSetWorkflowIDs(t *testing.T) {
	ctx, fixture, resolver, root, _ := newWorkflowTaskFixture(t)
	for _, id := range []string{"problem-set-generation-x", root + "-suffix", "problem-set-generation-" + uuid.Nil.String() + "-" + uuid.NewString()} {
		task, err := resolver.Resolve(ctx, id, "ProblemSetGenerationWorkflow", "")
		require.NoError(t, err)
		require.Equal(t, "生成题集", task.Title)
		require.Empty(t, task.SourceURL)
	}
	require.Zero(t, fixture.reads)
}

func TestWorkflowTaskResolverExplainsHistoricalFailuresWithoutMutatingEvidence(t *testing.T) {
	ctx, fixture, resolver, root, child := newWorkflowTaskFixture(t)
	slot := &fixture.set.Generation.Slots[1]
	slot.Failure = &domain.GenerationFailure{Code: "generation_failed", Message: "这道题未能完成生成", TechnicalDetails: "generate S3 authoring plan: parsing authoring-plan response: $.semantic_spec.problem_definition: expected scalar JSON value"}
	before, err := json.Marshal(slot)
	require.NoError(t, err)
	task, err := resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, root)
	require.NoError(t, err)
	require.Equal(t, "模型回复的格式不符合要求，系统无法读取", task.Summary)
	after, err := json.Marshal(slot)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	slot.Failure = nil
	slot.Error = "opaque synthetic internal details"
	task, err = resolver.Resolve(ctx, child, generationapi.QualityWorkflowTypeV1, root)
	require.NoError(t, err)
	require.NotContains(t, task.Summary, slot.Error)
	require.Equal(t, "这道题未完成，可以回到题集查看情况并重试。", task.Summary)
}

func TestWorkflowTaskResolverStandaloneQuarantineIsNotReportedAsSuccessfulGeneration(t *testing.T) {
	for _, typ := range []string{generationapi.QualityWorkflowTypeV1, "ProblemGenerationWorkflow", "ProblemImportWorkflow", "ImportedProblemWorkflow", "ProblemValidationWorkflow"} {
		t.Run(typ, func(t *testing.T) {
			ctx, fixture, resolver, _, child := newWorkflowTaskFixture(t)
			fixture.problem = &domain.Problem{ID: uuid.New(), Title: "合成隔离候选", WorkflowID: &child, Status: domain.ProblemStatusQuarantined}
			task, err := resolver.Resolve(ctx, child, typ, "")
			require.NoError(t, err)
			if typ == "ProblemValidationWorkflow" {
				require.Empty(t, task.Status, "validation execution retains its existing status behavior")
			} else {
				require.Equal(t, string(domain.WorkflowStatusRejectedQuarantined), task.Status)
				require.Contains(t, task.Summary, "人工检查")
			}
			require.Equal(t, "/problems/"+fixture.problem.ID.String(), task.ResultURL)
			fixture.problem.Status = domain.ProblemStatusPublished
			task, err = resolver.Resolve(ctx, child, typ, "")
			require.NoError(t, err)
			require.Empty(t, task.Status, "other persisted statuses retain existing behavior")
		})
	}
}

func TestWorkflowTaskResolverBusinessEntrypointsUseExistingImportAndRatingContracts(t *testing.T) {
	ctx, fixture, resolver, _, _ := newWorkflowTaskFixture(t)
	workflowID := "problem-import-synthetic space/&?extra=1"
	task, err := resolver.Resolve(ctx, workflowID, "ProblemImportWorkflow", "")
	require.NoError(t, err)
	entry, err := url.Parse(task.SourceURL)
	require.NoError(t, err)
	require.Equal(t, "/problems/import", entry.Path)
	require.Equal(t, workflowID, entry.Query().Get("workflow"))
	require.Len(t, entry.Query(), 1)
	assessment := uuid.New()
	admin := access.WithPrincipal(context.Background(), access.Principal{UserID: "synthetic-admin", Role: "admin"})
	before := fixture.reads
	task, err = resolver.Resolve(admin, "rating-"+assessment.String(), "RatingWorkflow", "")
	require.NoError(t, err)
	require.Equal(t, "/rating?assessment="+assessment.String(), task.SourceURL)
	require.Equal(t, before, fixture.reads, "confirmed identifier derivation needs no database or Temporal query")
	for _, id := range []string{"rating-unknown", "rating-" + uuid.Nil.String(), "rating-" + assessment.String() + "-suffix"} {
		task, err = resolver.Resolve(admin, id, "RatingWorkflow", "")
		require.NoError(t, err)
		require.Empty(t, task.SourceURL)
	}
	task, err = resolver.Resolve(ctx, "rating-"+assessment.String(), "RatingWorkflow", "")
	require.NoError(t, err)
	require.Empty(t, task.SourceURL, "members cannot read administrator-only assessments")
}
