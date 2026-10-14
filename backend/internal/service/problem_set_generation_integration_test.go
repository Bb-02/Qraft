//go:build integration

package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

type setPlanningLLMFixture struct {
	calls int
	t     *testing.T
}

type setIntroductoryPlanningFixture struct{ calls int }

func (f *setIntroductoryPlanningFixture) CompleteWithRetry(_ context.Context, _ *llm.Request, _ int) (*llm.Response, error) {
	f.calls++
	text := "按要求生成基础模拟题，保持低难度；每道题分别检查，不阻止其他合格题位。"
	if f.calls > 1 {
		raw, _ := json.Marshal([]setPlanEntry{
			{Position: 1, Type: domain.QuizTypeProgramming, Title: "无效合成目标", Brief: "这是一个完整的合成规划任务，故意填入不合法难度以检查其他题位仍可继续。", Tags: []string{"simulation"}, Level: domain.LevelAlgorithm, Difficulty: 0},
			{Position: 2, Type: domain.QuizTypeProgramming, Title: "基础合成模拟", Brief: "给出完整的整数操作规则，通过直接模拟得到输出，保留用户要求的低难度目标。", Tags: []string{"simulation"}, Level: domain.LevelAlgorithm, Difficulty: 400},
		})
		text = string(raw)
	}
	return &llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: text}}}, nil
}

func TestProblemSetPreparationIsolatesInvalidTargetsIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires a disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	repo := repository.NewProblemSetRepository(pool)
	tags := repository.NewTagRepository(pool)
	problems := NewProblemService(nil, nil, tags, nil, nil, "")
	model := &setIntroductoryPlanningFixture{}
	sets := NewProblemSetServiceWithDeps(repo, nil, nil, &HydroExportService{}, model)
	config := &domain.ProblemSetGenerationConfig{Mode: "programming", Requirements: "CF 风格训练目标400，前段基础实现，不增加创新性门槛。", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 2, Score: 100}}}
	set, err := sets.Create(ctx, ProblemSetCreateRequest{Title: "Synthetic introductory set", DesiredItemCount: 2, GenerationConfig: config})
	require.NoError(t, err)
	state, err := buildSetGenerationState(set)
	require.NoError(t, err)
	state, err = repo.ReserveGeneration(ctx, set.ID, set.UpdatedAt, state)
	require.NoError(t, err)
	runtime := &domain.LLMRuntimeConfig{Model: "fixture", APIKeyRef: "runtime:synthetic-set", BaseURL: "https://example.test", Provider: "fixture", Protocol: "openai-chat"}
	svc := NewProblemSetGenerationService(sets, repo, problems, tags, nil, "", func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{Statement: runtime, Verification: runtime, Review: runtime}, nil
	})
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(svc.PrepareProblemSetBatchActivity)
	result, err := env.ExecuteActivity(svc.PrepareProblemSetBatchActivity, domain.ProblemSetGenerationRef{SetID: set.ID, RunID: state.ID})
	require.NoError(t, err)
	var batch algoworkflow.ProblemSetBatch
	require.NoError(t, result.Get(&batch))
	require.Len(t, batch.Slots, 1, "invalid target must not discard the valid neighboring slot")
	require.Equal(t, 2, batch.Slots[0].Slot.Position)
	require.Equal(t, 400, batch.Slots[0].Programming.Params.Difficulty)
	require.Equal(t, set.ID, batch.Slots[0].Programming.Params.ProblemSetContext.SetID)
	require.NoError(t, batch.Slots[0].Programming.Params.Validate())
	stored, err := sets.Get(ctx, set.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", stored.Generation.Slots[0].Status)
	require.Contains(t, stored.Generation.Slots[0].Error, "训练目标难度")
	require.Empty(t, stored.Generation.Slots[0].Brief, "retry must replan invalid slot")
	require.Equal(t, "ready", stored.Generation.Slots[1].Status)
	require.Equal(t, 2, model.calls, "content failure must not regenerate the complete batch")
}

func (f *setPlanningLLMFixture) CompleteWithRetry(_ context.Context, r *llm.Request, _ int) (*llm.Response, error) {
	f.calls++
	require.Equal(f.t, "runtime:set-fixture", r.Runtime.APIKeyRef)
	text := "整套覆盖图与排序，先辨析前提再应用，不互相泄露答案。"
	if f.calls > 1 {
		require.Contains(f.t, r.Messages[0].Content, "judge")
		require.Contains(f.t, r.Messages[0].Content, "fill_blank")
		raw, _ := json.Marshal([]setPlanEntry{
			{Position: 1, Type: domain.QuizTypeJudge, Title: "树的性质", Brief: "设计一个澄清树与一般图差异的判断题，必须给出图的完整前提，避免混淆连通性和边数条件。", Tags: []string{"树"}, QuizDifficulty: domain.QuizDifficultyMedium},
			{Position: 2, Type: domain.QuizTypeFillBlank, Title: "排序过程", Brief: "根据明确的输入和排序规则考查关键中间状态，明确稳定性条件，答案是唯一的数值。", Tags: []string{"排序"}, QuizDifficulty: domain.QuizDifficultyMedium},
		})
		text = string(raw)
	}
	return &llm.Response{Content: []llm.ContentBlock{{Type: "text", Text: text}}}, nil
}

func TestProblemSetPlanCheckpointAttachAndExportIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" {
		t.Skip("requires a disposable migrated DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()
	repo := repository.NewProblemSetRepository(pool)
	quizzes := NewQuizService(repository.NewQuizRepository(pool), nil, nil, "")
	model := &setPlanningLLMFixture{t: t}
	sets := NewProblemSetServiceWithDeps(repo, nil, quizzes, &HydroExportService{}, model)
	config := &domain.ProblemSetGenerationConfig{Mode: "mixed", Requirements: "先概念后应用", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeJudge, Count: 1, Score: 2}, {Type: domain.QuizTypeFillBlank, Count: 1, Score: 5}}}
	set, err := sets.Create(ctx, ProblemSetCreateRequest{Title: "Full set fixture", DesiredItemCount: 2, GenerationConfig: config})
	require.NoError(t, err)
	state, err := buildSetGenerationState(set)
	require.NoError(t, err)
	state, err = repo.ReserveGeneration(ctx, set.ID, set.UpdatedAt, state)
	require.NoError(t, err)
	runtime := &domain.LLMRuntimeConfig{Model: "fixture", APIKeyRef: "runtime:set-fixture", BaseURL: "https://example.test", Provider: "fixture", Protocol: "openai-chat"}
	svc := NewProblemSetGenerationService(sets, repo, nil, nil, nil, "", func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{Statement: runtime, Verification: runtime, Review: runtime}, nil
	})
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(svc.PrepareProblemSetBatchActivity)
	ref := domain.ProblemSetGenerationRef{SetID: set.ID, RunID: state.ID}
	for i := 0; i < 2; i++ {
		result, err := env.ExecuteActivity(svc.PrepareProblemSetBatchActivity, ref)
		require.NoError(t, err)
		var batch algoworkflow.ProblemSetBatch
		require.NoError(t, result.Get(&batch))
		require.Len(t, batch.Slots, 2)
		require.Nil(t, batch.Slots[0].Programming)
		require.Equal(t, 1, batch.Slots[0].Quiz.Count)
		require.Contains(t, batch.Slots[0].Quiz.CustomPrompt, "先概念后应用")
		require.Equal(t, runtime, batch.Slots[0].ReviewRuntime)
	}
	require.Equal(t, 2, model.calls, "retrying preparation must reuse the persisted global and per-slot plans")
	set, err = sets.Get(ctx, set.ID)
	require.NoError(t, err)
	for _, slot := range set.Generation.Slots {
		q := &domain.QuizProblem{ID: uuid.New(), Code: slot.Type.CodePrefix() + uuid.NewString()[:8], Title: slot.Title, Statement: "单顶点无边的无向图是一棵树。", Type: slot.Type, Answers: []string{"对"}, Explanation: "满足树的连通且无环定义。", Difficulty: domain.QuizDifficultyMedium, Visibility: domain.QuizVisibilityPrivate, Subject: domain.QuizSubjectDataStructureAlgorithm, Tags: slot.Tags}
		if slot.Type == domain.QuizTypeFillBlank {
			q.Statement = "将序列 3,1,2 升序排序后，第一个元素为 ___。"
			q.Answers = []string{"1"}
			q.Explanation = "最小元素是 1。"
		}
		// Use the normal allocator and validation rather than inventing an export code.
		q.Code, err = quizzes.quizRepo.NextCodeForType(ctx, q.Type)
		require.NoError(t, err)
		_, err = quizzes.Create(ctx, q)
		require.NoError(t, err)
		slot.QuizID = &q.ID
		slot.Status = "generated"
		input := algoworkflow.ProblemSetSlotInput{Ref: ref, Slot: slot}
		require.NoError(t, svc.RecordProblemSetSlotActivity(ctx, input))
		require.NoError(t, svc.AttachProblemSetSlotActivity(ctx, input))
	}
	more, err := svc.FinishProblemSetGenerationActivity(ctx, algoworkflow.ProblemSetFinishInput{Ref: ref})
	require.NoError(t, err)
	require.False(t, more)
	set, err = sets.Get(ctx, set.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", set.Generation.Status)
	require.Len(t, set.Items, 2)
	require.Equal(t, 7, set.TotalScore)
	require.True(t, set.Quality.ReadyForExport)
	exported, err := sets.Export(ctx, set.ID, false)
	require.NoError(t, err)
	require.NotEmpty(t, exported.Package.Content)
}
