package service

import (
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
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
