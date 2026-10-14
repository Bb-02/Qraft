package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func mixedSetFixture() *domain.ProblemSet {
	return &domain.ProblemSet{ID: uuid.New(), Title: "期末题集", DesiredItemCount: 4,
		GenerationConfig: &domain.ProblemSetGenerationConfig{Mode: "mixed", Requirements: "覆盖不同概念", Distribution: []domain.ProblemSetTypeQuota{
			{Type: domain.QuizTypeProgramming, Count: 1, Score: 100},
			{Type: domain.QuizTypeChoice, Count: 1, Score: 2},
			{Type: domain.QuizTypeFillBlank, Count: 1, Score: 5},
			{Type: domain.QuizTypeJudge, Count: 1, Score: 2},
		}},
	}
}

func TestSetGenerationExactQuotas(t *testing.T) {
	s := mixedSetFixture()
	require.NoError(t, s.GenerationConfig.Validate(4))
	state, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.Len(t, state.Slots, 4)
	for i, q := range s.GenerationConfig.Distribution {
		require.Equal(t, i+1, state.Slots[i].Position)
		require.Equal(t, q.Type, state.Slots[i].Type)
		require.Equal(t, q.Score, state.Slots[i].Score)
	}
	for _, test := range []struct {
		name string
		edit func(*domain.ProblemSetGenerationConfig)
	}{
		{"pure rejects objective", func(c *domain.ProblemSetGenerationConfig) { c.Mode = "programming" }},
		{"count drift", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[0].Count++ }},
		{"duplicate type", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[1].Type = c.Distribution[0].Type }},
		{"unknown type", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[1].Type = "essay" }},
		{"negative score", func(c *domain.ProblemSetGenerationConfig) { c.Distribution[1].Score = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := mixedSetFixture().GenerationConfig
			test.edit(c)
			require.Error(t, c.Validate(4))
		})
	}
	for _, n := range []int{1, 1000} {
		c := &domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: n, Score: 100}}}
		require.NoError(t, c.Validate(n))
	}
}

func TestSetGenerationRetryPreservesSuccessAndRecoversInterruptedStore(t *testing.T) {
	s := mixedSetFixture()
	state, err := buildSetGenerationState(s)
	require.NoError(t, err)
	state.Brief = "已保存整套规划"
	state.Status = "partial"
	retained := uuid.New()
	s.Items = []domain.ProblemSetItem{{Position: 1, Score: 100, ProblemID: &retained, Problem: &domain.Problem{ID: retained, Title: "已完成", Statement: "existing"}}}
	recovered := uuid.New()
	state.Slots[0].Status = "succeeded"
	state.Slots[1].Status = "failed"
	state.Slots[1].ChildID = "old-child"
	state.Slots[1].QuizID = &recovered
	state.Slots[1].Brief = "保留的分题任务"
	state.Slots[2].Status = "failed"
	state.Slots[2].Regenerate = true
	state.Slots[2].QuizID = &recovered
	state.Slots[2].ChildID = "rejected-child"
	s.Generation = state
	next, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.NotEqual(t, state.ID, next.ID)
	require.Equal(t, state.Brief, next.Brief)
	require.Equal(t, "succeeded", next.Slots[0].Status)
	require.Equal(t, &retained, next.Slots[0].ProblemID)
	require.Equal(t, &recovered, next.Slots[1].QuizID)
	require.Equal(t, "old-child", next.Slots[1].ChildID)
	require.Equal(t, 2, next.Slots[1].Attempt)
	require.Equal(t, "pending", next.Slots[1].Status)
	require.Nil(t, next.Slots[2].QuizID)
	require.Empty(t, next.Slots[2].ChildID)
	s.Items = nil
	replacement, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.Nil(t, replacement.Slots[0].ProblemID, "a removed successful item must not be resurrected")
	s.GenerationConfig.Requirements = "新的需求"
	changed, err := buildSetGenerationState(s)
	require.NoError(t, err)
	require.Empty(t, changed.Brief)
	require.Nil(t, changed.Slots[1].QuizID)
}

func TestSetGenerationExistingItemsCannotOverfillOrOverlap(t *testing.T) {
	s := mixedSetFixture()
	qid := uuid.New()
	item := domain.ProblemSetItem{Position: 2, QuizID: &qid, Quiz: &domain.QuizProblem{Type: domain.QuizTypeChoice, Statement: "choice"}}
	s.Items = []domain.ProblemSetItem{item, item}
	_, err := buildSetGenerationState(s)
	require.Error(t, err)
	s.Items[1].Position = 3
	_, err = buildSetGenerationState(s)
	require.ErrorContains(t, err, "超过配额")
	s.Items = []domain.ProblemSetItem{item}
	s.Items[0].Position = 5
	_, err = buildSetGenerationState(s)
	require.ErrorContains(t, err, "排序")
}

func TestSetBatchPlannerRejectsModelTypeAndCoverageDrift(t *testing.T) {
	targets := []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming}, {Position: 2, Type: domain.QuizTypeJudge}}
	catalog := []domain.TagCategory{{TagName: "binary_search", Level: domain.LevelAlgorithm, MinDifficulty: 1000, MaxDifficulty: 2000}}
	valid := []setPlanEntry{
		{Position: 1, Type: domain.QuizTypeProgramming, Title: "边界定位", Brief: "为单调判定设计一个需要推导边界的题目，必须辨析临界状态而非机械套用模板。", Tags: []string{"binary_search"}, Level: domain.LevelAlgorithm, Difficulty: 1500},
		{Position: 2, Type: domain.QuizTypeJudge, Title: "不变量", Brief: "围绕循环不变量编写判断题，给出完整前提以保证结论唯一，并解释典型误区。", Tags: []string{"不变量"}, QuizDifficulty: domain.QuizDifficultyMedium},
	}
	require.NoError(t, validateSetBatchPlan(valid, targets, nil, catalog))
	for _, tc := range []struct {
		name string
		edit func([]setPlanEntry)
	}{
		{"wrong type", func(p []setPlanEntry) { p[1].Type = domain.QuizTypeChoice }},
		{"duplicate position", func(p []setPlanEntry) { p[1].Position = 1 }},
		{"duplicate task", func(p []setPlanEntry) { p[1].Brief = p[0].Brief }},
		{"invented tag", func(p []setPlanEntry) { p[0].Tags = []string{"invented"} }},
		{"invalid training target", func(p []setPlanEntry) { p[0].Difficulty = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans := append([]setPlanEntry(nil), valid...)
			tc.edit(plans)
			require.Error(t, validateSetBatchPlan(plans, targets, nil, catalog))
		})
	}
	require.Error(t, validateSetBatchPlan(valid[:1], targets, nil, catalog))
}

func TestSetBatchPlansAllowIntroductoryAndIntermediateTargets(t *testing.T) {
	catalog := []domain.TagCategory{
		{TagName: "grammar", Level: domain.LevelSyntax, MinDifficulty: 800, MaxDifficulty: 1200},
		{TagName: "simulation", Level: domain.LevelAlgorithm, MinDifficulty: 800, MaxDifficulty: 1400},
	}
	targets := []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming}, {Position: 2, Type: domain.QuizTypeProgramming}}
	plans := []setPlanEntry{
		{Position: 1, Type: domain.QuizTypeProgramming, Title: "基础计算", Brief: "按输入直接计算一个整数结果，学习读取变量和准确输出，不需要额外算法技巧。", Tags: []string{"grammar"}, Level: domain.LevelSyntax, Difficulty: 125},
		{Position: 2, Type: domain.QuizTypeProgramming, Title: "思维观察", Brief: "根据完整规则找出一个简短的不变量，直接模拟即可，不为了分类而提高难度。", Tags: []string{"simulation"}, Level: domain.LevelAlgorithm, Difficulty: 400},
	}
	require.NoError(t, validateSetBatchPlan(plans, targets, nil, catalog))
	// A tag's suggested upper range does not cap the user's training target.
	plans[1].Difficulty = 1601
	require.NoError(t, validateSetBatchPlan(plans, targets, nil, catalog))
	params := setProblemGenParams(domain.ProblemSetGenerationRef{SetID: uuid.New()}, domain.ProblemSetGenerationSlot{Position: 1, Level: domain.LevelAlgorithm, Difficulty: 125, Tags: []string{"simulation"}}, nil, "独立基础任务")
	require.NoError(t, params.Validate())
	quality, err := generationJobQualityInput(generationapi.JobIDPrefix+strings.Repeat("a", 64), params)
	require.NoError(t, err)
	require.Equal(t, 125, quality.Params.Difficulty)
	require.Equal(t, params.ProblemSetContext, quality.Params.ProblemSetContext)
}

func TestSetProblemGenParamsCarriesKnowledgePointContractIntoQualityInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
		mode string
		want []string
	}{
		{name: "single", tags: []string{"simulation"}, mode: domain.KnowledgePointCombinationSingle, want: []string{"simulation"}},
		{name: "set", tags: []string{"simulation", "greedy"}, mode: domain.KnowledgePointCombinationSet, want: []string{"greedy", "simulation"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot := domain.ProblemSetGenerationSlot{Position: 1, Level: domain.LevelAlgorithm, Difficulty: 400, Tags: append([]string(nil), tc.tags...)}
			params := setProblemGenParams(domain.ProblemSetGenerationRef{SetID: uuid.New()}, slot, nil, "合成基础训练题")
			require.Equal(t, tc.tags, slot.Tags, "canonical ordering must not mutate the stored slot")
			require.Equal(t, tc.want, params.Tags)
			require.ElementsMatch(t, tc.tags, params.Tags, "the requested knowledge points must remain unchanged")
			require.Equal(t, &domain.KnowledgePointCombinationContract{SchemaVersion: domain.KnowledgePointCombinationSchemaV1, Mode: tc.mode, MaxConcepts: len(tc.tags)}, params.KnowledgePointCombination)
			require.NoError(t, params.Validate())
			quality, err := generationJobQualityInput(generationapi.JobIDPrefix+strings.Repeat("b", 64), params)
			require.NoError(t, err)
			require.Equal(t, params.KnowledgePointCombination, quality.Params.KnowledgePointCombination)
			require.Equal(t, tc.want, quality.Params.Tags)
			require.NoError(t, quality.Params.KnowledgePointCombination.Validate(quality.Params.Tags))
		})
	}
}

func TestSetProblemGenParamsDoesNotHideInvalidKnowledgePointSlugs(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
		want string
	}{
		{name: "duplicate", tags: []string{"simulation", "simulation"}, want: "duplicate"},
		{name: "normalized duplicate", tags: []string{"simulation", " Simulation "}, want: "duplicate"},
		{name: "empty value", tags: []string{"simulation", ""}, want: "empty value"},
		{name: "missing knowledge points", tags: nil, want: "at least one slug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := setProblemGenParams(domain.ProblemSetGenerationRef{SetID: uuid.New()}, domain.ProblemSetGenerationSlot{Position: 1, Level: domain.LevelAlgorithm, Difficulty: 400, Tags: tc.tags}, nil, "合成基础训练题")
			require.Equal(t, tc.tags, params.Tags)
			require.Equal(t, len(tc.tags), params.KnowledgePointCombination.MaxConcepts)
			require.ErrorContains(t, params.Validate(), tc.want)
			_, err := generationJobQualityInput(generationapi.JobIDPrefix+strings.Repeat("c", 64), params)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSetPlanningFailuresLeaveValidNeighborsAndRetryReplansOnlyFailures(t *testing.T) {
	catalog := []domain.TagCategory{{TagName: "simulation", Level: domain.LevelAlgorithm, MinDifficulty: 800, MaxDifficulty: 1400}}
	plans := []setPlanEntry{
		{Position: 1, Type: domain.QuizTypeProgramming, Title: "坏目标", Brief: "这是完整的合成题目任务，用于检查一个不合法难度不会阻断其他题位。", Tags: []string{"simulation"}, Level: domain.LevelAlgorithm, Difficulty: 0},
		{Position: 2, Type: domain.QuizTypeProgramming, Title: "有效题位", Brief: "这是另一个完整的合成题目任务，保持基础模拟并设置有效的低难度目标。", Tags: []string{"simulation"}, Level: domain.LevelAlgorithm, Difficulty: 400},
	}
	targets := []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming}, {Position: 2, Type: domain.QuizTypeProgramming}}
	require.NoError(t, validateSetBatchPlanStructure(plans, targets))
	errorsByPosition := setBatchPlanEntryErrors(plans, nil, catalog)
	require.Len(t, errorsByPosition, 1)
	require.Contains(t, errorsByPosition[1].Error(), "训练目标难度")
	require.Nil(t, errorsByPosition[2])
	slot := domain.ProblemSetGenerationSlot{Position: 1, Type: domain.QuizTypeProgramming, Brief: plans[0].Brief}
	failSetPlanningSlot(&slot, errorsByPosition[1].Error())
	require.Equal(t, "failed", slot.Status)
	require.Empty(t, slot.Brief, "a retry must not reuse the invalid task")
	require.True(t, slot.Regenerate)
	set := &domain.ProblemSet{ID: uuid.New(), Title: "合成题集", DesiredItemCount: 2, GenerationConfig: &domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 2, Score: 100}}}}
	set.Generation = &domain.ProblemSetGenerationState{ConfigFingerprint: setConfigFingerprint(set), Slots: []domain.ProblemSetGenerationSlot{slot, {Position: 2, Type: domain.QuizTypeProgramming, Status: "failed", Brief: plans[1].Brief}}}
	state, err := buildSetGenerationState(set)
	require.NoError(t, err)
	require.Equal(t, "pending", state.Slots[0].Status)
	require.Empty(t, state.Slots[0].Brief)
	require.Equal(t, plans[1].Brief, state.Slots[1].Brief)
}

type setStateOnlyStore struct {
	problemSetGenerationStore
	state *domain.ProblemSetGenerationState
}

func (s *setStateOnlyStore) ChangeGeneration(_ context.Context, _ domain.ProblemSetGenerationRef, f func(*domain.ProblemSetGenerationState) error) error {
	return f(s.state)
}

func TestSetGenerationFinishesPartialAndPreservesCompletedOnCancel(t *testing.T) {
	for _, status := range []string{"failed", "pending", "succeeded"} {
		state := &domain.ProblemSetGenerationState{Status: "generating", Slots: []domain.ProblemSetGenerationSlot{{Status: "succeeded"}, {Status: status}}}
		svc := &ProblemSetGenerationService{repo: &setStateOnlyStore{state: state}}
		more, err := svc.FinishProblemSetGenerationActivity(context.Background(), algoworkflow.ProblemSetFinishInput{})
		require.NoError(t, err)
		require.Equal(t, status == "pending", more)
		require.Equal(t, map[string]string{"failed": "partial", "pending": "planning", "succeeded": "completed"}[status], state.Status)
	}
	state := &domain.ProblemSetGenerationState{Status: "generating", Slots: []domain.ProblemSetGenerationSlot{{Status: "succeeded"}, {Status: "running"}}}
	svc := &ProblemSetGenerationService{repo: &setStateOnlyStore{state: state}}
	_, err := svc.FinishProblemSetGenerationActivity(context.Background(), algoworkflow.ProblemSetFinishInput{Cancelled: true})
	require.NoError(t, err)
	require.Equal(t, "cancelled", state.Status)
	require.Equal(t, "succeeded", state.Slots[0].Status)
}

type setSettingsFixture struct{ g, v, r *domain.LLMRuntimeConfig }

func (s setSettingsFixture) EffectiveRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, error) {
	return s.g, nil
}
func (s setSettingsFixture) SavedRuntimeConfig(_ context.Context, p string) (*domain.LLMRuntimeConfig, bool, error) {
	x := s.v
	if p == "review" {
		x = s.r
	}
	return x, x != nil, nil
}

type setKeysFixture struct{ values []string }

func (s *setKeysFixture) Put(_ context.Context, k string) (string, error) {
	s.values = append(s.values, k)
	return fmt.Sprintf("runtime:test-key-%d", len(s.values)), nil
}
func TestSetProviderFallbackDoesNotPutRawKeysInWorkflow(t *testing.T) {
	g := &domain.LLMRuntimeConfig{Model: "generator", Provider: "openai", Protocol: "openai-chat", BaseURL: "https://example.test/v1", APIKey: "test-secret"}
	keys := &setKeysFixture{}
	cfg, err := NewProblemSetProviderResolver(setSettingsFixture{g: g}, keys)(context.Background())
	require.NoError(t, err)
	for _, r := range []*domain.LLMRuntimeConfig{cfg.Statement, cfg.Verification, cfg.Review} {
		require.Empty(t, r.APIKey)
		require.NotEmpty(t, r.APIKeyRef)
		require.Equal(t, g.Model, r.Model)
	}
	require.Equal(t, "test-secret", g.APIKey)
}
