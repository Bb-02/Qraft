package service

import (
	"encoding/json"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSetFailureExplanationIsReadOnlyAndKeepsSlotIdentity(t *testing.T) {
	failure := &domain.GenerationFailure{Code: "generation_failed", Message: "原错误", TechnicalDetails: "parsing authoring-plan response: unknown or non-canonical JSON key"}
	state := &domain.ProblemSetGenerationState{ID: "synthetic-run", Status: "partial", Slots: []domain.ProblemSetGenerationSlot{{Position: 1, ChildID: "synthetic-child", Status: "failed", Error: "原错误", Failure: failure}}}
	got := explainSetGenerationFailures(state)
	require.Equal(t, state.ID, got.ID)
	require.Equal(t, state.Status, got.Status)
	require.Equal(t, state.Slots[0].ChildID, got.Slots[0].ChildID)
	require.Equal(t, "model_output_invalid", got.Slots[0].Failure.Code)
	require.NotEqual(t, "原错误", got.Slots[0].Error)
	require.Equal(t, "原错误", state.Slots[0].Error)
	require.Equal(t, "generation_failed", state.Slots[0].Failure.Code)
	require.Nil(t, explainSetGenerationFailures(nil))
}

func TestProblemSetGetExplainsInactiveFailuresWithoutChangingStoredEvidence(t *testing.T) {
	failure := &domain.GenerationFailure{Code: "quality_review_required", BlockedGate: "spec_lint", Message: "合成旧分类", TechnicalDetails: "The synthetic task needs a string. Generation parameters restrict supported_input_symbol_types to only [integer,integer_sequence]."}
	stored := &domain.ProblemSet{ID: uuid.New(), Title: "合成停止后的题集", OwnerUserID: "synthetic-owner", Visibility: domain.ProblemSetVisibilityPrivate,
		Generation: &domain.ProblemSetGenerationState{ID: "synthetic-ended-run", Status: "partial", Slots: []domain.ProblemSetGenerationSlot{{Position: 1, ChildID: "synthetic-child", Status: "failed", Regenerate: true, Error: "合成旧分类", Failure: failure}}}}
	repo := &runtimeSetStore{set: stored}
	svc := NewProblemSetServiceWithDeps(repo, nil, nil, nil, nil)
	svc.SetWorkflowAccess(NewWorkflowAccess(nil, nil))
	before, err := json.Marshal(stored)
	require.NoError(t, err)
	got, err := svc.Get(ownerContext("synthetic-owner", "member"), stored.ID)
	require.NoError(t, err)
	require.NotSame(t, stored, got)
	require.NotSame(t, stored.Generation, got.Generation)
	require.Equal(t, "partial", got.Generation.Status)
	require.Equal(t, "synthetic-ended-run", got.Generation.ID)
	require.Equal(t, "synthetic-child", got.Generation.Slots[0].ChildID)
	require.True(t, got.Generation.Slots[0].Regenerate)
	require.Equal(t, "unsupported_input_format", got.Generation.Slots[0].Failure.Code)
	require.Equal(t, "流程能力限制", got.Generation.Slots[0].Failure.Category)
	require.Equal(t, got.Generation.Slots[0].Failure.Message, got.Generation.Slots[0].Error)
	require.Equal(t, failure.TechnicalDetails, got.Generation.Slots[0].Failure.TechnicalDetails)
	after, err := json.Marshal(stored)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.Zero(t, repo.writes)
}

func TestProblemSetGetDoesNotExposeOtherOwnersGenerationWhenPublic(t *testing.T) {
	stored := &domain.ProblemSet{ID: uuid.New(), Title: "合成公开题集", OwnerUserID: "synthetic-owner", Visibility: domain.ProblemSetVisibilityPublic, GenerationError: "synthetic private generation error",
		Generation: &domain.ProblemSetGenerationState{ID: "synthetic-private-run", Status: "partial", Slots: []domain.ProblemSetGenerationSlot{{Position: 1, Status: "failed", Failure: &domain.GenerationFailure{Code: "generation_failed", Message: "合成私有失败", TechnicalDetails: "parsing test data response: unknown field"}}}}}
	repo := &runtimeSetStore{set: stored}
	svc := NewProblemSetServiceWithDeps(repo, nil, nil, nil, nil)
	svc.SetWorkflowAccess(NewWorkflowAccess(nil, nil))
	before, err := json.Marshal(stored)
	require.NoError(t, err)
	got, err := svc.Get(ownerContext("synthetic-other-member", "member"), stored.ID)
	require.NoError(t, err)
	require.Equal(t, stored.Title, got.Title)
	require.Nil(t, got.Generation)
	require.Empty(t, got.GenerationError)
	after, err := json.Marshal(stored)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.NotNil(t, stored.Generation)
	require.Zero(t, repo.writes)
	stored.Visibility = domain.ProblemSetVisibilityPrivate
	_, err = svc.Get(ownerContext("synthetic-other-member", "member"), stored.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
