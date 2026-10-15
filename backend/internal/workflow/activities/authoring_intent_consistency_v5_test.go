package activities

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
)

func authoringRepeatedIntentFixtureV5() (domain.SemanticSpecV1, domain.AuthoringPlanV1) {
	one, two, three, low, high := int64(1), int64(2), int64(3), int64(-7), int64(11)
	literalOne := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}
	spec := domain.SemanticSpecV1{
		SchemaVersion: domain.SemanticSpecSchemaV1,
		Symbols: []domain.SemanticSymbolV1{
			{Name: "records", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput},
			{Name: "values", Type: domain.SemanticSymbolIntegerSequence, Scope: domain.SemanticSymbolScopeInput},
		},
		InputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{
			{ID: "count", Repeat: literalOne, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "records", Mode: domain.SemanticGrammarFieldScalar}}},
			{ID: "record-lines", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "records"}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "values", Mode: domain.SemanticGrammarFieldElementPerRepeat}}},
		}},
		Constraints: []domain.SemanticConstraintV1{
			{Subject: "records", Kind: domain.SemanticConstraintIntegerRange, Min: &one, Max: &three},
			{Subject: "values", Kind: domain.SemanticConstraintLengthRange, Min: &one, Max: &three},
			{Subject: "values", Kind: domain.SemanticConstraintElementRange, Min: &low, Max: &high},
		},
		Relations: []domain.SemanticRelationV1{{ID: "count-equals-total-length", Left: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "values"}, Operator: domain.SemanticRelationEqual, Right: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "records"}}},
		Boundaries: []domain.SemanticBoundaryV1{
			{ID: "records-one", Symbol: "records", Measure: domain.SemanticBoundaryMeasureValue, Value: one, Allowed: true},
			{ID: "records-three", Symbol: "records", Measure: domain.SemanticBoundaryMeasureValue, Value: three, Allowed: true},
			{ID: "values-one", Symbol: "values", Measure: domain.SemanticBoundaryMeasureLength, Value: one, Allowed: true},
			{ID: "values-two", Symbol: "values", Measure: domain.SemanticBoundaryMeasureLength, Value: two, Allowed: true},
			{ID: "element-low", Symbol: "values", Measure: domain.SemanticBoundaryMeasureElementValue, Value: low, Allowed: true},
			{ID: "element-high", Symbol: "values", Measure: domain.SemanticBoundaryMeasureElementValue, Value: high, Allowed: true},
		},
	}
	plan := domain.AuthoringPlanV1{SchemaVersion: domain.AuthoringPlanSchemaV1, TestIntents: []domain.AuthoringTestIntentV1{{Purpose: "vary record values", ConstraintRegion: "low and high records", BoundaryRefs: []string{"element-low", "element-high"}}}}
	return spec, plan
}

func TestAuthoringIntentConsistencyV5UsesWholeCaseRepeatedSequenceCapacity(t *testing.T) {
	spec, plan := authoringRepeatedIntentFixtureV5()
	parsed, err := speccontract.ParseTestCaseInputV2(spec, "2\n-7\n11\n")
	require.NoError(t, err)
	bindings := integerCoverageBindingMapV1(parsed.Bindings)
	length, ok := bindings["values"].Length()
	require.True(t, ok)
	require.EqualValues(t, 2, length, "one element per record is aggregated across all records")
	require.Empty(t, authoringIntentConflictsV5(spec, plan), "two records can satisfy the two element boundaries")
	plan.TestIntents[0].BoundaryRefs = append(plan.TestIntents[0].BoundaryRefs, "values-one")
	conflicts := authoringIntentConflictsV5(spec, plan)
	require.Len(t, conflicts, 1)
	for _, expected := range []string{"intent[0]", `region="low and high records"`, "element-low", "element-high", "values-one", "capacity at most 1", "2 distinct"} {
		require.Contains(t, conflicts[0], expected)
	}
	// The intended boundaries are individually reachable. Only their AND is
	// impossible: one global value cannot equal two distinct integers.
	one, err := speccontract.ParseTestCaseInputV2(spec, "1\n-7\n")
	require.NoError(t, err)
	oneBindings := integerCoverageBindingMapV1(one.Bindings)
	require.True(t, integerBoundaryHitV1(spec.Boundaries[2], oneBindings))
	require.False(t, integerBoundaryHitV1(spec.Boundaries[5], oneBindings))
}

func TestAuthoringIntentConsistencyV5ProvesOnlyKnownCapacityBounds(t *testing.T) {
	for _, test := range []struct {
		name         string
		mutate       func(*domain.SemanticSpecV1, *domain.AuthoringPlanV1)
		wantConflict bool
	}{
		{"repeat symbol fixed to one by same intent", func(_ *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			plan.TestIntents[0].BoundaryRefs = append(plan.TestIntents[0].BoundaryRefs, "records-one")
		}, true},
		{"whole-case length max one", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			value := int64(1)
			spec.Constraints[1].Max = &value
		}, true},
		{"whole-case record repeat max one", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			value := int64(1)
			spec.Constraints[0].Max = &value
		}, true},
		{"whole-case repeat literal one", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			value := int64(1)
			spec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
		}, true},
		{"whole-case sequence count literal one", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			value := int64(1)
			spec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
			spec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldSequence
			spec.InputGrammar.Lines[1].Fields[0].Count = &domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
		}, true},
		{"two values fit exactly", func(_ *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			plan.TestIntents[0].BoundaryRefs = append(plan.TestIntents[0].BoundaryRefs, "values-two")
		}, false},
		{"local per-record count is not total capacity", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			value := int64(1)
			spec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldSequence
			spec.InputGrammar.Lines[1].Fields[0].Count = &domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
			spec.Constraints[1].Max = &value
		}, false},
		{"unknown grammar profile", func(spec *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			spec.InputGrammar.Profile = "hypothetical.local-records.v1"
			plan.TestIntents[0].BoundaryRefs = append(plan.TestIntents[0].BoundaryRefs, "values-one")
		}, false},
		{"unknown count capacity", func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.Constraints[0].Max = nil
			spec.Constraints[1].Max = nil
		}, false},
		{"ambiguous multiple sequence bindings", func(spec *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			spec.InputGrammar.Lines = append(spec.InputGrammar.Lines, spec.InputGrammar.Lines[1])
			plan.TestIntents[0].BoundaryRefs = append(plan.TestIntents[0].BoundaryRefs, "values-one")
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec, plan := authoringRepeatedIntentFixtureV5()
			test.mutate(&spec, &plan)
			conflicts := authoringIntentConflictsV5(spec, plan)
			if test.wantConflict {
				require.NotEmpty(t, conflicts)
			} else {
				require.Empty(t, conflicts)
			}
		})
	}
}

func TestAuthoringIntentConsistencyV5ScalarLengthAndDistinctValueRules(t *testing.T) {
	for _, test := range []struct {
		name         string
		refs         []string
		wantConflict bool
	}{
		{"same scalar has two values", []string{"records-one", "records-three"}, true},
		{"same sequence has two total lengths", []string{"values-one", "values-two"}, true},
		{"duplicate scalar refs do not conflict", []string{"records-one", "records-one"}, false},
		{"one slot with repeated equal element values", []string{"values-one", "element-low", "element-low-alias"}, false},
		{"different symbols do not conflict", []string{"records-one", "values-one", "element-low"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec, plan := authoringRepeatedIntentFixtureV5()
			alias := spec.Boundaries[4]
			alias.ID = "element-low-alias"
			spec.Boundaries = append(spec.Boundaries, alias)
			plan.TestIntents[0].BoundaryRefs = test.refs
			conflicts := authoringIntentConflictsV5(spec, plan)
			if test.wantConflict {
				require.Len(t, conflicts, 1)
			} else {
				require.Empty(t, conflicts)
			}
		})
	}
	spec, plan := authoringRepeatedIntentFixtureV5()
	spec.Symbols[1].Type = domain.SemanticSymbolString
	value := int64(1)
	spec.InputGrammar.Lines[1].Repeat = domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &value}
	spec.InputGrammar.Lines[1].Fields[0].Mode = domain.SemanticGrammarFieldScalar
	plan.TestIntents[0].BoundaryRefs = []string{"values-one", "values-two"}
	require.Len(t, authoringIntentConflictsV5(spec, plan), 1, "one scalar string cannot have two exact lengths")
}

func TestAuthoringIntentConsistencyV5IsStableAndDoesNotMutateFullContracts(t *testing.T) {
	spec, plan := authoringRepeatedIntentFixtureV5()
	plan.TestIntents = []domain.AuthoringTestIntentV1{
		{Purpose: "scalar conflict", ConstraintRegion: "first", BoundaryRefs: []string{"records-three", "records-one"}},
		{Purpose: "sequence conflict", ConstraintRegion: "second", BoundaryRefs: []string{"element-low", "values-one", "element-high"}},
	}
	before, err := json.Marshal(struct {
		Spec domain.SemanticSpecV1
		Plan domain.AuthoringPlanV1
	}{spec, plan})
	require.NoError(t, err)
	want := authoringIntentConflictsV5(spec, plan)
	require.Len(t, want, 2)
	require.True(t, strings.HasPrefix(want[0], "intent[0]"))
	require.True(t, strings.HasPrefix(want[1], "intent[1]"))
	for i := 0; i < 8; i++ {
		require.Equal(t, want, authoringIntentConflictsV5(spec, plan))
	}
	after, err := json.Marshal(struct {
		Spec domain.SemanticSpecV1
		Plan domain.AuthoringPlanV1
	}{spec, plan})
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	for i := range plan.TestIntents {
		refs := plan.TestIntents[i].BoundaryRefs
		for left, right := 0, len(refs)-1; left < right; left, right = left+1, right-1 {
			refs[left], refs[right] = refs[right], refs[left]
		}
	}
	require.Equal(t, want, authoringIntentConflictsV5(spec, plan), "ref input order must not affect proof order")
}
