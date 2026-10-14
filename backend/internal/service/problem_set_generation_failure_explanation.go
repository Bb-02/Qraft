package service

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

func explainSetGenerationFailures(state *domain.ProblemSetGenerationState) *domain.ProblemSetGenerationState {
	if state == nil {
		return nil
	}
	copy := *state
	copy.Failure = generationapi.EnrichFailure(state.Failure)
	copy.Slots = append([]domain.ProblemSetGenerationSlot(nil), state.Slots...)
	for i := range copy.Slots {
		copy.Slots[i].Failure = generationapi.EnrichFailure(copy.Slots[i].Failure)
		if copy.Slots[i].Failure != nil && copy.Slots[i].Status == "failed" {
			copy.Slots[i].Error = copy.Slots[i].Failure.Message
		}
	}
	return &copy
}
