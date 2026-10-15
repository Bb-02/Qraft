package activities

import (
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
)

const (
	IntegerInputBoundaryCasePlanRuleVersionV3     = "algoforge.integer-input-boundary-case-plan.rules.v3"
	TokenStringInputBoundaryCasePlanRuleVersionV3 = "algoforge.token-string-input-boundary-case-plan.rules.v3"
)

type inputBoundaryCoverageScopeV3 struct {
	Constraints []domain.SemanticConstraintV1
	Boundaries  []domain.SemanticBoundaryV1
}

// Input coverage can prove only symbols bound by the actual input grammar.
// Retain the complete spec for identity and all other generation/verification
// stages; this selection never edits its output or derived-symbol contracts.
func deriveInputBoundaryCoverageScopeV3(spec domain.SemanticSpecV1) (inputBoundaryCoverageScopeV3, error) {
	result := inputBoundaryCoverageScopeV3{Constraints: []domain.SemanticConstraintV1{}, Boundaries: []domain.SemanticBoundaryV1{}}
	symbols := make(map[string]domain.SemanticSymbolV1, len(spec.Symbols))
	for _, symbol := range spec.Symbols {
		if symbol.Name == "" || symbol.Name != strings.TrimSpace(symbol.Name) {
			return result, fmt.Errorf("input coverage symbol name is not canonical")
		}
		if _, exists := symbols[symbol.Name]; exists {
			return result, fmt.Errorf("input coverage symbol %q is duplicated", symbol.Name)
		}
		if symbol.Scope != domain.SemanticSymbolScopeInput && symbol.Scope != domain.SemanticSymbolScopeOutput && symbol.Scope != domain.SemanticSymbolScopeDerived {
			return result, fmt.Errorf("input coverage symbol %q has unknown or empty scope %q", symbol.Name, symbol.Scope)
		}
		symbols[symbol.Name] = symbol
	}
	bound := make(map[string]bool)
	for _, line := range spec.InputGrammar.Lines {
		for _, field := range line.Fields {
			symbol, exists := symbols[field.Symbol]
			if !exists || symbol.Scope != domain.SemanticSymbolScopeInput {
				return result, fmt.Errorf("input coverage grammar field %q is unknown or not input-scoped", field.Symbol)
			}
			bound[field.Symbol] = true
		}
	}
	for _, symbol := range spec.Symbols {
		if symbol.Scope == domain.SemanticSymbolScopeInput && !bound[symbol.Name] {
			return result, fmt.Errorf("input coverage symbol %q is absent from the input grammar", symbol.Name)
		}
	}
	for _, constraint := range spec.Constraints {
		symbol, exists := symbols[constraint.Subject]
		if !exists {
			return result, fmt.Errorf("input coverage constraint references undefined symbol %q", constraint.Subject)
		}
		if symbol.Scope == domain.SemanticSymbolScopeInput {
			result.Constraints = append(result.Constraints, constraint)
		}
	}
	boundaryIDs := make(map[string]bool)
	for _, boundary := range spec.Boundaries {
		if boundary.ID == "" || boundary.ID != strings.TrimSpace(boundary.ID) || boundaryIDs[boundary.ID] {
			return result, fmt.Errorf("input coverage boundary id %q is empty, duplicated, or non-canonical", boundary.ID)
		}
		boundaryIDs[boundary.ID] = true
		symbol, exists := symbols[boundary.Symbol]
		if !exists {
			return result, fmt.Errorf("input coverage boundary %q references undefined symbol %q", boundary.ID, boundary.Symbol)
		}
		if symbol.Scope == domain.SemanticSymbolScopeInput {
			result.Boundaries = append(result.Boundaries, boundary)
		}
	}
	return result, nil
}

func inputBoundaryCasePlanRuleForSpecV3(spec domain.SemanticSpecV1) string {
	if speccontract.SampleInputParserRuleVersionForSpecV1(spec) == speccontract.SampleInputParserTokenStringsRuleVersionV2 {
		return TokenStringInputBoundaryCasePlanRuleVersionV3
	}
	return IntegerInputBoundaryCasePlanRuleVersionV3
}

func DeriveInputBoundaryCasePlanV3(spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1) (*IntegerBoundaryCasePlanV1, error) {
	scope, err := deriveInputBoundaryCoverageScopeV3(spec)
	if err != nil {
		return nil, err
	}
	scopes := make(map[string]string, len(spec.Symbols))
	for _, symbol := range spec.Symbols {
		scopes[symbol.Name] = symbol.Scope
	}
	nonInputBoundaries := make(map[string]string)
	for _, boundary := range spec.Boundaries {
		if scopes[boundary.Symbol] != domain.SemanticSymbolScopeInput {
			nonInputBoundaries[boundary.ID] = scopes[boundary.Symbol]
		}
	}
	for _, intent := range plan.TestIntents {
		for _, ref := range intent.BoundaryRefs {
			if scope, exists := nonInputBoundaries[ref]; exists {
				return nil, fmt.Errorf("input coverage cannot prove test intent boundary %q on a %s-scoped symbol", ref, scope)
			}
		}
	}
	return deriveBoundaryCasePlanWithScopeV1(spec, plan, scope.Constraints, scope.Boundaries, inputBoundaryCasePlanRuleForSpecV3(spec))
}
