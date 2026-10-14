package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// TrainingDifficultyMin and TrainingDifficultyMax bound an author-requested
// training target. These targets are not measured or official CF ratings.
const TrainingDifficultyMin = 1
const TrainingDifficultyMax = 10000

func ValidateTrainingDifficulty(difficulty int) error {
	if difficulty < TrainingDifficultyMin || difficulty > TrainingDifficultyMax {
		return fmt.Errorf("训练目标难度须为 %d 至 %d 之间的整数", TrainingDifficultyMin, TrainingDifficultyMax)
	}
	return nil
}

// ProblemSetGenerationContext is authored by the set service, never by a
// public single-problem request. It survives Temporal serialization so every
// activity applies the same set-specific difficulty policy.
type ProblemSetGenerationContext struct {
	SetID    uuid.UUID `json:"set_id"`
	Position int       `json:"position"`
}

func (p *ProblemGenParams) IsProblemSetGeneration() bool {
	return p != nil && p.ProblemSetContext != nil
}

func (c *ProblemSetGenerationContext) Validate() error {
	if c == nil {
		return nil
	}
	if c.SetID == uuid.Nil || c.Position < 1 || c.Position > MaxProblemSetItemCount {
		return fmt.Errorf("题集生成上下文缺少有效的题集或题位")
	}
	return nil
}
