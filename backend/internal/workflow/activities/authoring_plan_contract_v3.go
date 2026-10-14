package activities

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

const (
	AuthoringPlanPromptIDV3             = "authoring_plan_dsl_v3"
	AuthoringPlanFormatRepairPromptIDV3 = "authoring_plan_dsl_format_repair_v3"
)

// Types alone cannot describe the DSL: these values come from the actual
// domain constants accepted by spec.LintV1, rather than model-chosen aliases.
func authoringPlanEnumFieldsV3() map[reflect.Type]map[string][]string {
	return map[reflect.Type]map[string][]string{
		reflect.TypeOf(authoringPlanModelOutputV1{}):    {"decision": {AuthoringPlanDecisionAccepted, AuthoringPlanDecisionRejected}},
		reflect.TypeOf(domain.SemanticGrammarV1{}):      {"profile": {domain.SemanticGrammarTokenLinesV1}},
		reflect.TypeOf(domain.SemanticGrammarFieldV1{}): {"mode": {domain.SemanticGrammarFieldScalar, domain.SemanticGrammarFieldSequence, domain.SemanticGrammarFieldElementPerRepeat}},
		reflect.TypeOf(domain.SemanticObjectiveV1{}):    {"kind": {domain.SemanticObjectiveCompute, domain.SemanticObjectiveCount, domain.SemanticObjectiveDecision, domain.SemanticObjectiveMinimize, domain.SemanticObjectiveMaximize, domain.SemanticObjectiveConstruct}},
		reflect.TypeOf(domain.SemanticSymbolV1{}): {
			"type":            {domain.SemanticSymbolInteger, domain.SemanticSymbolString, domain.SemanticSymbolBoolean, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolStringSequence, domain.SemanticSymbolSet, domain.SemanticSymbolMultiset, domain.SemanticSymbolGraph, domain.SemanticSymbolTree, domain.SemanticSymbolGrid},
			"scope":           {domain.SemanticSymbolScopeInput, domain.SemanticSymbolScopeOutput, domain.SemanticSymbolScopeDerived},
			"boundary_policy": {domain.SemanticBoundaryPolicyZeroOneRequired, domain.SemanticBoundaryPolicyExplicit, domain.SemanticBoundaryPolicyNotApplicable},
		},
		reflect.TypeOf(domain.SemanticConstraintV1{}): {"kind": {domain.SemanticConstraintIntegerRange, domain.SemanticConstraintLengthRange, domain.SemanticConstraintCardinality, domain.SemanticConstraintElementRange}},
		reflect.TypeOf(domain.SemanticRelationV1{}):   {"operator": {domain.SemanticRelationEqual, domain.SemanticRelationLessThan, domain.SemanticRelationLessThanOrEqual, domain.SemanticRelationGreaterThan, domain.SemanticRelationGreaterThanOrEqual}},
		reflect.TypeOf(domain.SemanticExpressionV1{}): {"kind": {domain.SemanticExpressionSymbol, domain.SemanticExpressionInteger, domain.SemanticExpressionLength, domain.SemanticExpressionCardinality, domain.SemanticExpressionAdd, domain.SemanticExpressionSubtract, domain.SemanticExpressionMultiply, domain.SemanticExpressionFloorDivide}},
		reflect.TypeOf(domain.SemanticTopologyV1{}): {
			"kind":         {domain.SemanticTopologyNone, domain.SemanticTopologyLinear, domain.SemanticTopologyCircular, domain.SemanticTopologyGraph, domain.SemanticTopologyTree, domain.SemanticTopologyGrid},
			"direction":    {domain.SemanticTopologyNotApplicable, domain.SemanticTopologyDirected, domain.SemanticTopologyUndirected},
			"cycle_policy": {domain.SemanticTopologyNotApplicable, domain.SemanticTopologyAcyclic, domain.SemanticTopologyCyclic, domain.SemanticTopologyCyclesAllowed},
			"connectivity": {domain.SemanticTopologyNotApplicable, domain.SemanticTopologyConnected, domain.SemanticTopologyDisconnected},
			"dynamics":     {domain.SemanticTopologyNotApplicable, domain.SemanticTopologyStatic, domain.SemanticTopologyDynamic},
			"self_loops":   {domain.SemanticTopologyNotApplicable, domain.SemanticTopologyAllowed, domain.SemanticTopologyForbidden},
			"multi_edges":  {domain.SemanticTopologyNotApplicable, domain.SemanticTopologyAllowed, domain.SemanticTopologyForbidden},
		},
		reflect.TypeOf(domain.SemanticBoundaryV1{}): {"measure": {domain.SemanticBoundaryMeasureValue, domain.SemanticBoundaryMeasureLength, domain.SemanticBoundaryMeasureCardinality, domain.SemanticBoundaryMeasureElementValue}},
		reflect.TypeOf(domain.SemanticAnswerSemanticsV1{}): {
			"no_solution_policy":       {domain.SemanticNoSolutionImpossible, domain.SemanticNoSolutionToken, domain.SemanticNoSolutionEmptyOutput},
			"multiple_solution_policy": {domain.SemanticMultipleSolutionUnique, domain.SemanticMultipleSolutionAnyValid, domain.SemanticMultipleSolutionCanonical},
		},
		reflect.TypeOf(domain.SemanticJudgeV1{}): {"mode": {domain.SemanticJudgeExactNormalized, domain.SemanticJudgeSpecialChecker}, "comparison_profile": {domain.SemanticComparisonTrimTrailingSpaceLFV1}},
	}
}

func authoringPlanSystemPromptV3() (string, error) {
	v2, err := authoringPlanSystemPromptV2()
	if err != nil {
		return "", err
	}
	prefix, rawSchema, ok := strings.Cut(v2, "Complete output JSON Schema:\n")
	if !ok {
		return "", fmt.Errorf("authoring output schema marker is missing")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(rawSchema), &schema); err != nil {
		return "", err
	}
	definitions, ok := schema["$defs"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("authoring output schema definitions are missing")
	}
	for typ, fields := range authoringPlanEnumFieldsV3() {
		definition, ok := definitions[typ.Name()].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("authoring enum type %s is missing", typ.Name())
		}
		properties, ok := definition["properties"].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("authoring enum properties %s are missing", typ.Name())
		}
		for name, values := range fields {
			property, ok := properties[name].(map[string]interface{})
			if !ok || property["type"] != "string" {
				return "", fmt.Errorf("authoring enum field %s.%s is not a string", typ.Name(), name)
			}
			property["enum"] = values
		}
	}
	encodedSchema, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	example, err := marshalAuthoringPlanExampleV3(authoringPlanDSLExampleV3())
	if err != nil {
		return "", err
	}
	return prefix + authoringPlanConsistencyInstructionsV3 + "\n\nIllustrative valid integer-sequence contract (synthetic example, not the user's task):\n" + string(example) + "\n\nComplete output JSON Schema:\n" + string(encodedSchema), nil
}

func authoringPlanFormatRepairRequestV3(original *llm.Request, invalidOutput string, formatErr error) *llm.Request {
	repair := authoringPlanFormatRepairRequestV2(original, invalidOutput, formatErr)
	repair.PromptID, repair.PromptVersion = AuthoringPlanFormatRepairPromptIDV3, "v3"
	return repair
}

// The sample is deliberately a small, ordinary integer-only task. It exists to
// explain serialization and consistency, never to replace a frozen concept.
func authoringPlanDSLExampleV3() authoringPlanModelOutputV1 {
	minimum, maximum, lower, upper, one := int64(1), int64(200000), int64(-1000000000), int64(1000000000), int64(1)
	count := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	return authoringPlanModelOutputV1{
		Decision: AuthoringPlanDecisionAccepted,
		SemanticSpec: &domain.SemanticSpecV1{
			ProblemDefinition: "Read n integers and print their sum.",
			Sections: domain.SemanticSpecSectionsV1{
				Input:       &domain.SemanticSectionV1{Summary: "Read n, followed by n integers a.", SymbolRefs: []string{"n", "a"}},
				Output:      &domain.SemanticSectionV1{Summary: "Print the unique integer sum answer.", SymbolRefs: []string{"answer"}},
				Constraints: &domain.SemanticSectionV1{Summary: "1 <= n <= 200000; a has length n and bounded integer elements.", SymbolRefs: []string{"n", "a"}},
			},
			InputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{
				{ID: "size", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "read size once"},
				{ID: "values", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &count}}, Meaning: "read n values on one line"},
			}},
			OutputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{{ID: "answer", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "answer", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "print sum once"}}},
			Objective:     domain.SemanticObjectiveV1{Kind: domain.SemanticObjectiveCompute, Summary: "Compute the sum of a.", SymbolRefs: []string{"a", "answer"}},
			Symbols: []domain.SemanticSymbolV1{
				{Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput, Role: "size", Definition: "number of values", BoundaryPolicy: domain.SemanticBoundaryPolicyZeroOneRequired},
				{Name: "a", Type: domain.SemanticSymbolIntegerSequence, Scope: domain.SemanticSymbolScopeInput, Role: "values", Definition: "sequence of n integers", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit},
				{Name: "answer", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeOutput, Role: "sum", Definition: "sum of every input value", BoundaryPolicy: domain.SemanticBoundaryPolicyNotApplicable},
			},
			Constraints: []domain.SemanticConstraintV1{
				{Subject: "n", Kind: domain.SemanticConstraintIntegerRange, Min: &minimum, Max: &maximum, Meaning: "number of values"},
				{Subject: "a", Kind: domain.SemanticConstraintLengthRange, Min: &minimum, Max: &maximum, Meaning: "sequence length"},
				{Subject: "a", Kind: domain.SemanticConstraintElementRange, Min: &lower, Max: &upper, Meaning: "each integer element"},
			},
			Relations: []domain.SemanticRelationV1{{ID: "length", Left: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "a"}, Operator: domain.SemanticRelationEqual, Right: count, Meaning: "length(a) equals n"}},
			Topology:  domain.SemanticTopologyV1{Kind: domain.SemanticTopologyLinear, Subject: "a", Direction: domain.SemanticTopologyNotApplicable, CyclePolicy: domain.SemanticTopologyAcyclic, Connectivity: domain.SemanticTopologyNotApplicable, Dynamics: domain.SemanticTopologyStatic, SelfLoops: domain.SemanticTopologyNotApplicable, MultiEdges: domain.SemanticTopologyNotApplicable},
			Boundaries: []domain.SemanticBoundaryV1{
				{ID: "n-zero", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: false, Meaning: "empty input forbidden"},
				{ID: "n-one", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 1, Allowed: true, Meaning: "smallest size"},
				{ID: "n-max", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: maximum, Allowed: true, Meaning: "maximum size"},
				{ID: "a-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 0, Allowed: false, Meaning: "empty sequence forbidden"},
				{ID: "a-one", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 1, Allowed: true, Meaning: "single element sequence"},
				{ID: "a-element-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: 0, Allowed: true, Meaning: "zero-valued element allowed"},
			},
			AnswerSemantics: domain.SemanticAnswerSemanticsV1{NoSolutionPolicy: domain.SemanticNoSolutionImpossible, MultipleSolutionPolicy: domain.SemanticMultipleSolutionUnique},
			Judge:           domain.SemanticJudgeV1{Mode: domain.SemanticJudgeExactNormalized, ComparisonProfile: domain.SemanticComparisonTrimTrailingSpaceLFV1},
			SampleInputs:    []string{"3\n1 2 3\n"},
		},
		AuthoringPlan: &domain.AuthoringPlanV1{
			CoreIdea: "Accumulate each value once.", ConceptRoles: []domain.AuthoringConceptRoleV1{{Slug: "simulation", Role: "main method", Necessity: "follow the input sequence and accumulate its elements"}},
			IntendedSolution:        domain.AuthoringAlgorithmPlanV1{Summary: "single-pass integer sum", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
			BruteForceBaseline:      domain.AuthoringAlgorithmPlanV1{Summary: "direct sum of a tiny list", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
			OracleCandidateStrategy: domain.AuthoringAlgorithmPlanV1{Summary: "independent checked accumulation", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
			FailureModes:            []domain.AuthoringFailureModeV1{{ID: "narrow-sum", Description: "32-bit accumulator can overflow", WitnessIntent: "large same-sign values"}},
			TestIntents:             []domain.AuthoringTestIntentV1{{Purpose: "single element", ConstraintRegion: "n=1", BoundaryRefs: []string{"n-one", "a-one"}}},
			TargetDifficulty:        400, TeachingObjectives: []string{"read and accumulate integers"}, CreativeIntent: "Keep the task suitable for beginners.",
		},
	}
}

func marshalAuthoringPlanExampleV3(example authoringPlanModelOutputV1) ([]byte, error) {
	encoded, err := json.Marshal(example)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		return nil, err
	}
	for _, name := range []string{"semantic_spec", "authoring_plan"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(top[name], &fields); err != nil {
			return nil, err
		}
		for _, key := range []string{"schema_version", "brief_sha256", "semantic_spec_sha256"} {
			delete(fields, key)
		}
		top[name], err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(top)
}

const authoringPlanConsistencyInstructionsV3 = `
DSL consistency rules (checked deterministically, not optional quality advice):
1. Choose only the exact enum values in the schema. Do not invent synonyms such
as int, array, numeric_range, interval, minimum, required, optional or undefined.
Symbol names are trimmed ASCII identifiers matching ^[A-Za-z][A-Za-z0-9_]*$.
Only actual task inputs, outputs and derived facts belong in symbols. Runtime
configuration such as time_limit_ms, memory_limit_mb, difficulty, requested
sample/test counts and tags must not become problem symbols or constraints.
Every symbol ref and constraint subject names an existing symbol. Input section
refs and input grammar fields contain only input-scoped symbols. Every input
and output symbol appears in its respective section and grammar exactly once;
derived symbols appear in a section or objective. The constraints section refs
cover every constraint subject and every symbol referenced by a relation.
The objective has a nonempty summary and at least one output-scoped symbol ref.
2. min/max are explicit integer constants with min<=max, never expressions or
strings. Input integer symbols need integer_range; integer_sequence needs both
length_range and element_range; string/string_sequence/grid need length_range;
set/multiset/graph/tree need cardinality. Length/cardinality min is nonnegative.
Repeated ranges for the same subject must have a nonempty intersection.
The current token-lines string pipeline supports only nonempty ASCII tokens
without whitespace, including every string_sequence element. If the frozen
task requires empty strings, text containing spaces or Unicode input, do not
silently change its facts or encode it as integers: return rejected with the
specific unsupported input requirement. This is a serialization limitation,
not a restriction on the training difficulty.
3. Expression leaves have exactly the relevant member: integer uses value only;
symbol uses symbol naming an integer only; length uses symbol naming a string,
sequence or grid only; cardinality uses symbol naming a collection/graph/tree.
Leaves have no args. length(a) is {"kind":"length","symbol":"a"}, never an
args wrapper. add/subtract/multiply/floor_divide use exactly two expression args
and no symbol/value. Maximum nesting depth is 16. floor_divide's denominator has
a known bounded interval excluding zero. Relations cannot contradict ranges.
4. Grammar lines have unique trimmed ids and nonempty meaning. scalar fields
have scalar symbols and no count. sequence fields have sequence-like symbols
and a required count expression. If a line has any scalar or sequence field,
repeat must be exactly {"kind":"integer","value":1}. To serialize a sequence
across repeated lines, use element_per_repeat with no count and put its size in
repeat. All repeat/count intervals are known, bounded and nonnegative. Input
expressions can reference only input symbols already read earlier (including
earlier fields on the same line); never self-reference or use later symbols.
Counts stay inside the collection's declared length/cardinality range.
5. Every boundary has a unique id, known symbol, correct measure and nonempty
meaning. Integer/boolean use value; integer_sequence uses length or
element_value; string/string_sequence/grid use length; set/multiset/graph/tree
use cardinality. Do not use element_range as a boundary measure (it is a
constraint kind). A (symbol,measure,value) tuple occurs once. allowed agrees
with the declared range, including explicit forbidden values outside it.
zero_one_required applies only to integer symbols and requires an integer_range
plus explicit value=0 and value=1 facts, even when either is forbidden. explicit
requires at least one boundary for that symbol. not_applicable forbids boundary
facts for that symbol. Provide at least five distinct meaningful boundary facts.
6. Topology none omits subject/count variables and sets direction, cycle_policy,
connectivity, dynamics, self_loops and multi_edges ALL to not_applicable (not
static or false). linear/circular use an input-scoped string/sequence subject,
direction/connectivity/self_loops/multi_edges=not_applicable, dynamics=static or
dynamic, cycle_policy=acyclic for linear or cyclic for circular; omit graph
count variables. graph/tree use a matching input-scoped subject, explicit
directed/undirected, static/dynamic and connectivity/cycle/loop/edge policies.
graph requires input integer vertex_count_variable and edge_count_variable,
both zero_one_required; tree requires input integer vertex_count_variable with
zero_one_required and, if edge_count_variable is present, integer scope.
Bind cardinality(subject)=vertex_count_variable with an equal relation. tree
requires acyclic/connected/forbidden loops/forbidden multi_edges; if it declares
an edge count, bind it exactly to vertex_count_variable-1. grid uses an input
grid subject, static/dynamic, no graph counts, and no allowed loops/multi_edges.
7. no_solution_policy is impossible, token or empty_output. token requires
no_solution_token; the other two omit it. multiple_solution_policy is unique,
any_valid or canonical: unique omits canonicalization_rule and
custom_acceptance_semantics; any_valid requires custom_acceptance_semantics,
omits canonicalization_rule and requires a configured immutable special checker;
canonical requires canonicalization_rule and omits custom_acceptance_semantics.
Always omit floating_point_error_rule: floating-point acceptance is unsupported
by this v1 DSL. exact_normalized uses trim-trailing-space-lf-v1 and no checker
fields; special_checker omits comparison_profile and requires a real immutable
cas:// checker reference, lowercase SHA-256 and nonempty checker_rule_version.
Do not invent a checker reference merely to make the contract appear complete.
8. Authoring algorithm plans have nonempty summary/time_complexity/
space_complexity. Every requested tag has an exact matching unique concept_roles
slug (including grammar when requested), role and necessity; do not rename it
or add complexity to justify a beginner task. Nonempty teaching objectives,
creative intent, failure modes and test intents are required. Failure ids are
unique; test boundary_refs name actual boundary ids. Keep target_difficulty at
the supplied training target. Difficulty estimates never redefine that target.
The illustrative example below demonstrates serialization only: preserve the
user's frozen mechanism, facts and requested concepts, and adjust every bound,
symbol and sample accordingly. Never replace the user's task with this example.
`
