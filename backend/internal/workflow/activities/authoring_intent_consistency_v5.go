package activities

import (
	"fmt"
	"sort"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

type authoringIntentBindingV5 struct {
	typeName string
	extent   *domain.SemanticExpressionV1
}

type authoringIntentExactV5 struct {
	values map[int64][]string
}

type authoringIntentCapacityV5 struct {
	known  bool
	value  int64
	reason string
	refs   []string
}

// authoringIntentConflictsV5 proves only elementary contradictions inside one
// intent's AND of boundary refs. It is not a general satisfiability checker.
// Unsupported or ambiguous grammar bindings are left to the existing parser
// and lint, never guessed to be one scalar or one local sequence per case.
func authoringIntentConflictsV5(spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1) []string {
	bindings := authoringIntentBindingsV5(spec)
	if len(bindings) == 0 {
		return nil
	}
	boundaries := make(map[string]domain.SemanticBoundaryV1, len(spec.Boundaries))
	duplicateIDs := make(map[string]bool)
	for _, boundary := range spec.Boundaries {
		if _, exists := boundaries[boundary.ID]; exists {
			duplicateIDs[boundary.ID] = true
		}
		boundaries[boundary.ID] = boundary
	}
	conflicts := make([]string, 0)
	for index, intent := range plan.TestIntents {
		bySymbol := make(map[string][]domain.SemanticBoundaryV1)
		for _, ref := range intent.BoundaryRefs {
			boundary, exists := boundaries[ref]
			binding, bound := bindings[boundary.Symbol]
			if !exists || duplicateIDs[ref] || !boundary.Allowed || !bound {
				continue
			}
			if (binding.typeName == domain.SemanticSymbolInteger && boundary.Measure == domain.SemanticBoundaryMeasureValue) ||
				(binding.typeName != domain.SemanticSymbolInteger && boundary.Measure == domain.SemanticBoundaryMeasureLength) ||
				(binding.typeName == domain.SemanticSymbolIntegerSequence && boundary.Measure == domain.SemanticBoundaryMeasureElementValue) {
				bySymbol[boundary.Symbol] = append(bySymbol[boundary.Symbol], boundary)
			}
		}
		integerExacts := make(map[string]authoringIntentExactV5)
		for symbol, refs := range bySymbol {
			if bindings[symbol].typeName == domain.SemanticSymbolInteger {
				integerExacts[symbol] = authoringIntentExactsV5(refs, domain.SemanticBoundaryMeasureValue)
			}
		}
		names := make([]string, 0, len(bySymbol))
		for name := range bySymbol {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			binding, refs := bindings[name], bySymbol[name]
			measure := domain.SemanticBoundaryMeasureLength
			if binding.typeName == domain.SemanticSymbolInteger {
				measure = domain.SemanticBoundaryMeasureValue
			}
			exact := authoringIntentExactsV5(refs, measure)
			if len(exact.values) > 1 {
				conflicts = append(conflicts, fmt.Sprintf("intent[%d] region=%q symbol=%q boundary_refs=%q: one whole-case %s binding requires different exact %s values %v", index, intent.ConstraintRegion, name, authoringIntentExactRefsV5(exact), binding.typeName, measure, authoringIntentExactValuesV5(exact)))
				continue
			}
			if binding.typeName != domain.SemanticSymbolIntegerSequence {
				continue
			}
			elements := authoringIntentExactsV5(refs, domain.SemanticBoundaryMeasureElementValue)
			if len(elements.values) == 0 {
				continue
			}
			capacity := authoringIntentCapacityV5{}
			for _, value := range authoringIntentExactValuesV5(exact) {
				capacity.add(value, "whole-case length boundary", exact.values[value])
			}
			for _, constraint := range spec.Constraints {
				if constraint.Subject == name && constraint.Kind == domain.SemanticConstraintLengthRange && constraint.Max != nil {
					capacity.add(*constraint.Max, "whole-case input length_range.max", nil)
				}
			}
			if binding.extent != nil {
				if maximum, source, sourceRefs, known := authoringIntentExtentMaximumV5(*binding.extent, spec, bindings, integerExacts); known {
					capacity.add(maximum, source, sourceRefs)
				}
			}
			if capacity.known && capacity.value < int64(len(elements.values)) {
				proofRefs := append(authoringIntentExactRefsV5(elements), capacity.refs...)
				proofRefs = authoringIntentSortedRefsV5(proofRefs)
				conflicts = append(conflicts, fmt.Sprintf("intent[%d] region=%q symbol=%q boundary_refs=%q: one whole-case sequence has capacity at most %d (%s), but must contain %d distinct element_value values %v", index, intent.ConstraintRegion, name, proofRefs, capacity.value, capacity.reason, len(elements.values), authoringIntentExactValuesV5(elements)))
			}
		}
	}
	return conflicts
}

// token-lines.v1 aggregates every element_per_repeat value into the same
// whole-case binding. A sequence field inside a repeated record is deliberately
// not interpreted as one global sequence: that local/nested shape is outside
// this parser contract and cannot establish a total capacity here.
func authoringIntentBindingsV5(spec domain.SemanticSpecV1) map[string]authoringIntentBindingV5 {
	result := make(map[string]authoringIntentBindingV5)
	if spec.InputGrammar.Profile != domain.SemanticGrammarTokenLinesV1 {
		return result
	}
	symbols := make(map[string]domain.SemanticSymbolV1, len(spec.Symbols))
	ambiguous := make(map[string]bool)
	for _, symbol := range spec.Symbols {
		if _, exists := symbols[symbol.Name]; exists {
			ambiguous[symbol.Name] = true
		}
		symbols[symbol.Name] = symbol
	}
	seen := make(map[string]bool)
	for _, line := range spec.InputGrammar.Lines {
		for _, field := range line.Fields {
			if seen[field.Symbol] {
				ambiguous[field.Symbol] = true
			}
			seen[field.Symbol] = true
			symbol, exists := symbols[field.Symbol]
			if !exists || symbol.Scope != domain.SemanticSymbolScopeInput || ambiguous[field.Symbol] {
				continue
			}
			binding := authoringIntentBindingV5{typeName: symbol.Type}
			switch field.Mode {
			case domain.SemanticGrammarFieldScalar:
				if field.Count != nil || !sampleInputLiteralOneForIntentV5(line.Repeat) || (symbol.Type != domain.SemanticSymbolInteger && symbol.Type != domain.SemanticSymbolString) {
					continue
				}
			case domain.SemanticGrammarFieldSequence:
				if field.Count == nil || !sampleInputLiteralOneForIntentV5(line.Repeat) || (symbol.Type != domain.SemanticSymbolIntegerSequence && symbol.Type != domain.SemanticSymbolStringSequence) {
					continue
				}
				binding.extent = field.Count
			case domain.SemanticGrammarFieldElementPerRepeat:
				if field.Count != nil || (symbol.Type != domain.SemanticSymbolIntegerSequence && symbol.Type != domain.SemanticSymbolStringSequence) {
					continue
				}
				repeat := line.Repeat
				binding.extent = &repeat
			default:
				continue
			}
			result[field.Symbol] = binding
		}
	}
	for name := range ambiguous {
		delete(result, name)
	}
	return result
}

func sampleInputLiteralOneForIntentV5(expression domain.SemanticExpressionV1) bool {
	return expression.Kind == domain.SemanticExpressionInteger && expression.Value != nil && *expression.Value == 1 && expression.Symbol == "" && len(expression.Args) == 0
}

func authoringIntentExtentMaximumV5(expression domain.SemanticExpressionV1, spec domain.SemanticSpecV1, bindings map[string]authoringIntentBindingV5, exacts map[string]authoringIntentExactV5) (int64, string, []string, bool) {
	if expression.Kind == domain.SemanticExpressionInteger && expression.Value != nil && expression.Symbol == "" && len(expression.Args) == 0 && *expression.Value >= 0 {
		return *expression.Value, "whole-case grammar count/repeat literal", nil, true
	}
	if expression.Kind != domain.SemanticExpressionSymbol || expression.Value != nil || len(expression.Args) != 0 || bindings[expression.Symbol].typeName != domain.SemanticSymbolInteger {
		return 0, "", nil, false
	}
	capacity := authoringIntentCapacityV5{}
	for _, value := range authoringIntentExactValuesV5(exacts[expression.Symbol]) {
		capacity.add(value, fmt.Sprintf("whole-case grammar count/repeat symbol %q exact boundary", expression.Symbol), exacts[expression.Symbol].values[value])
	}
	for _, constraint := range spec.Constraints {
		if constraint.Subject == expression.Symbol && constraint.Kind == domain.SemanticConstraintIntegerRange && constraint.Max != nil {
			capacity.add(*constraint.Max, fmt.Sprintf("whole-case grammar count/repeat symbol %q integer_range.max", expression.Symbol), nil)
		}
	}
	return capacity.value, capacity.reason, capacity.refs, capacity.known
}

func (capacity *authoringIntentCapacityV5) add(value int64, reason string, refs []string) {
	if value >= 0 && (!capacity.known || value < capacity.value) {
		capacity.known, capacity.value, capacity.reason = true, value, reason
		capacity.refs = append([]string(nil), refs...)
	}
}

func authoringIntentExactsV5(refs []domain.SemanticBoundaryV1, measure string) authoringIntentExactV5 {
	result := authoringIntentExactV5{values: make(map[int64][]string)}
	for _, ref := range refs {
		if ref.Measure == measure {
			result.values[ref.Value] = append(result.values[ref.Value], ref.ID)
		}
	}
	return result
}

func authoringIntentExactValuesV5(exact authoringIntentExactV5) []int64 {
	values := make([]int64, 0, len(exact.values))
	for value := range exact.values {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func authoringIntentExactRefsV5(exact authoringIntentExactV5) []string {
	refs := make([]string, 0)
	for _, ids := range exact.values {
		refs = append(refs, ids...)
	}
	return authoringIntentSortedRefsV5(refs)
}

func authoringIntentSortedRefsV5(refs []string) []string {
	sort.Strings(refs)
	if len(refs) < 2 {
		return refs
	}
	result := refs[:1]
	for _, ref := range refs[1:] {
		if ref != result[len(result)-1] {
			result = append(result, ref)
		}
	}
	return result
}
