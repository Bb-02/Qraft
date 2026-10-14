package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
)

func TestCanonicalBriefAndAuthoringV4DeclareActualTokenStringCapabilities(t *testing.T) {
	input := BuildCanonicalAuthoringBriefInput{PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion, FrozenConcept: "Read nonempty ASCII tokens and count their characters.", RequiredFacts: []CanonicalAuthoringBriefFactV1{{Key: "alphabet", Value: "nonempty ASCII without whitespace"}}}
	result, err := (&Activities{}).BuildCanonicalAuthoringBriefActivityV1(context.Background(), input)
	require.NoError(t, err)
	var brief CanonicalAuthoringBriefV1
	require.NoError(t, json.Unmarshal([]byte(result.CanonicalBrief), &brief))
	require.Equal(t, []string{domain.SemanticSymbolInteger, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolString, domain.SemanticSymbolStringSequence}, brief.SupportedInputSymbolTypes)
	require.Equal(t, input.FrozenConcept, brief.FrozenConcept)
	require.Equal(t, input.RequiredFacts, brief.RequiredFacts)
	require.Contains(t, brief.StringInputPolicy, "nonempty ASCII")
	require.Equal(t, sha256Hex([]byte(result.CanonicalBrief)), result.CanonicalBriefSHA256)
	system, err := authoringPlanSystemPromptV4()
	require.NoError(t, err)
	for _, requirement := range []string{"old server capability, not an input restriction", "nonempty ASCII token strings", "empty string_sequence", "explicit requires at least one", "ALL boundary facts", "only allowed=true boundaries", "never forbidden boundaries", "including facts\n  with allowed=false"} {
		require.Contains(t, system, requirement)
	}
	capabilities := currentAuthoringInputCapabilitiesV4()
	require.Equal(t, 1<<20, capabilities.SampleInputMaxBytes)
	require.Equal(t, 8<<20, capabilities.TestCaseInputMaxBytes)
}

func TestAuthoringV4ResumesLegacyBriefWithoutChangingFrozenFactsOrHash(t *testing.T) {
	legacy := CanonicalAuthoringBriefV1{SchemaVersion: CanonicalAuthoringBriefSchemaV1, FrozenConcept: "Read n nonempty ASCII tokens and print their total character count.", RequiredFacts: []CanonicalAuthoringBriefFactV1{{Key: "alphabet", Value: "nonempty ASCII without whitespace"}}, InputGrammarProfile: domain.SemanticGrammarTokenLinesV1, SupportedInputSymbolTypes: []string{domain.SemanticSymbolInteger, domain.SemanticSymbolIntegerSequence}}
	encoded, err := json.Marshal(legacy)
	require.NoError(t, err)
	in := validAuthoringActivityInput()
	in.CanonicalBrief, in.CanonicalBriefSHA256 = string(encoded), sha256Hex(encoded)
	in.Params.Tags = []string{"simulation"}
	output := authoringPlanDSLExampleV3()
	output.SemanticSpec.ProblemDefinition = legacy.FrozenConcept
	output.SemanticSpec.Symbols[1].Type, output.SemanticSpec.Symbols[1].Definition = domain.SemanticSymbolStringSequence, "n nonempty ASCII token strings"
	output.SemanticSpec.Symbols[2].Definition = "total ASCII character count of all tokens"
	output.SemanticSpec.Constraints = output.SemanticSpec.Constraints[:2]
	output.SemanticSpec.Boundaries = output.SemanticSpec.Boundaries[:5]
	output.SemanticSpec.SampleInputs = []string{"3\na bb c\n"}
	output.AuthoringPlan.TargetDifficulty = in.Params.Difficulty
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(authoringFormatValidJSON(t, output))}}
	acts, store, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, in)
	require.True(t, result.LintPassed)
	require.Len(t, model.requests, 1)
	require.Equal(t, in.CanonicalBriefSHA256, result.BriefSHA256)
	require.Equal(t, AuthoringPlanPromptIDV4, model.requests[0].PromptID)
	require.Equal(t, "v4", model.requests[0].PromptVersion)
	userPrompt := model.requests[0].Messages[0].Content
	require.Contains(t, userPrompt, string(encoded), "legacy immutable brief and original task facts must remain in the request")
	_, tail, ok := strings.Cut(userPrompt, "Current server input capabilities (authoritative;")
	require.True(t, ok)
	require.Contains(t, tail, `"supported_input_symbol_types":["integer","integer_sequence","string","string_sequence"]`)
	require.Contains(t, tail, "never its frozen task facts")
	require.Equal(t, string(encoded), in.CanonicalBrief)
	var bundle AuthoringPlanBundleV1
	require.NoError(t, json.Unmarshal(store.objects[result.BundleSHA256], &bundle))
	_, err = speccontract.ParseSampleInputV1(*bundle.SemanticSpec, bundle.SemanticSpec.SampleInputs[0])
	require.NoError(t, err, "resumed legacy brief must produce an actually supported token-string input")
}

func TestAuthoringV4BoundaryPoliciesRemainPerSymbolAndRejectForbiddenFacts(t *testing.T) {
	output := authoringPlanDSLExampleV3()
	zero, hundred, one := int64(0), int64(100), int64(1)
	output.SemanticSpec.Symbols = append(output.SemanticSpec.Symbols,
		domain.SemanticSymbolV1{Name: "score", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput, Role: "task input", Definition: "given score", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit},
		domain.SemanticSymbolV1{Name: "target_marker", Type: domain.SemanticSymbolString, Scope: domain.SemanticSymbolScopeInput, Role: "task input", Definition: "one ASCII marker token", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit})
	output.SemanticSpec.Sections.Input.SymbolRefs = append(output.SemanticSpec.Sections.Input.SymbolRefs, "score", "target_marker")
	output.SemanticSpec.Sections.Constraints.SymbolRefs = append(output.SemanticSpec.Sections.Constraints.SymbolRefs, "score", "target_marker")
	output.SemanticSpec.InputGrammar.Lines[0].Fields = append(output.SemanticSpec.InputGrammar.Lines[0].Fields, domain.SemanticGrammarFieldV1{Symbol: "score", Mode: domain.SemanticGrammarFieldScalar}, domain.SemanticGrammarFieldV1{Symbol: "target_marker", Mode: domain.SemanticGrammarFieldScalar})
	output.SemanticSpec.Constraints = append(output.SemanticSpec.Constraints, domain.SemanticConstraintV1{Subject: "score", Kind: domain.SemanticConstraintIntegerRange, Min: &zero, Max: &hundred, Meaning: "given score range"}, domain.SemanticConstraintV1{Subject: "target_marker", Kind: domain.SemanticConstraintLengthRange, Min: &one, Max: &one, Meaning: "one ASCII marker"})
	output.SemanticSpec.SampleInputs = []string{"3 0 X\n1 2 3\n"}
	brief := strings.Repeat("a", 64)
	output.SemanticSpec.SchemaVersion, output.SemanticSpec.BriefSHA256 = domain.SemanticSpecSchemaV1, brief
	output.AuthoringPlan.SchemaVersion, output.AuthoringPlan.BriefSHA256 = domain.AuthoringPlanSchemaV1, brief
	lint := func() speccontract.LintReportV1 {
		output.AuthoringPlan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(*output.SemanticSpec)
		return speccontract.LintV1(speccontract.LintInputV1{SemanticSpec: *output.SemanticSpec, AuthoringPlan: *output.AuthoringPlan, ExpectedBriefSHA256: brief, ExpectedDifficulty: 400, RequiredKnowledgePoints: []string{"simulation"}})
	}
	report := lint()
	require.False(t, report.Passed, "six boundaries on other symbols do not satisfy explicit policy for score/target_marker")
	var missing []string
	for _, issue := range report.Issues {
		if issue.Code == speccontract.CodeSpecBoundaryFactMissing {
			missing = append(missing, issue.Message)
		}
	}
	require.Contains(t, strings.Join(missing, "\n"), "score requires at least one")
	require.Contains(t, strings.Join(missing, "\n"), "target_marker requires at least one")
	output.SemanticSpec.Boundaries = append(output.SemanticSpec.Boundaries, domain.SemanticBoundaryV1{ID: "score-min", Symbol: "score", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: true, Meaning: "minimum input score"}, domain.SemanticBoundaryV1{ID: "marker-length", Symbol: "target_marker", Measure: domain.SemanticBoundaryMeasureLength, Value: 1, Allowed: true, Meaning: "single-character input marker"})
	report = lint()
	require.True(t, report.Passed, "%+v", report.Issues)
	_, err := speccontract.ParseSampleInputV1(*output.SemanticSpec, output.SemanticSpec.SampleInputs[0])
	require.NoError(t, err)
	output.SemanticSpec.Boundaries = append(output.SemanticSpec.Boundaries, domain.SemanticBoundaryV1{ID: "answer-forbidden-zero", Symbol: "answer", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: false, Meaning: "synthetic unsupported boundary on not_applicable output"})
	report = lint()
	require.False(t, report.Passed)
	found := false
	for _, issue := range report.Issues {
		found = found || (issue.Code == speccontract.CodeSpecBoundaryFactConflict && strings.Contains(issue.Message, "forbidden by the symbol boundary policy"))
	}
	require.True(t, found, "allowed=false is still forbidden on a not_applicable symbol")
}
