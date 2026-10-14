package domain

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSetGenerationTargetsAreIndependentOfContentLevel(t *testing.T) {
	setID := uuid.New()
	for _, level := range []ProblemLevel{LevelSyntax, LevelAlgorithm} {
		for _, difficulty := range []int{1, 125, 400, 1200, 3501, 10000} {
			params := DefaultProblemGenParams()
			params.Level, params.Difficulty = level, difficulty
			params.ProblemSetContext = &ProblemSetGenerationContext{SetID: setID, Position: 1}
			if err := params.Validate(); err != nil {
				t.Errorf("set level=%s difficulty=%d: %v", level, difficulty, err)
			}
		}
	}
	params := DefaultProblemGenParams()
	params.ProblemSetContext = &ProblemSetGenerationContext{SetID: setID, Position: 1}
	for _, difficulty := range []int{-1, 0, 10001} {
		params.Difficulty = difficulty
		if params.Validate() == nil {
			t.Errorf("invalid set target %d was accepted", difficulty)
		}
	}
}

func TestSetGenerationContextIsValidatedAndSurvivesSerialization(t *testing.T) {
	params := DefaultProblemGenParams()
	params.Difficulty = 125
	params.ProblemSetContext = &ProblemSetGenerationContext{SetID: uuid.New(), Position: 2}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var restored ProblemGenParams
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatalf("serialized set target lost its policy: %v", err)
	}
	if !restored.IsProblemSetGeneration() || *restored.ProblemSetContext != *params.ProblemSetContext {
		t.Fatal("set identity was not preserved")
	}
	for _, context := range []ProblemSetGenerationContext{
		{Position: 1},
		{SetID: uuid.New(), Position: 0},
		{SetID: uuid.New(), Position: MaxProblemSetItemCount + 1},
	} {
		params.ProblemSetContext = &context
		if params.Validate() == nil {
			t.Errorf("invalid set context %+v was accepted", context)
		}
	}
	params.ProblemSetContext = nil
	if params.Validate() == nil {
		t.Fatal("single-problem target validation was relaxed")
	}
}
