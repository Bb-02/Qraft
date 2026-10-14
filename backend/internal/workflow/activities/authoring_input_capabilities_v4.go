package activities

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

const (
	AuthoringPlanPromptIDV4             = "authoring_plan_input_capabilities_v4"
	AuthoringPlanFormatRepairPromptIDV4 = "authoring_plan_input_capabilities_format_repair_v4"
	currentAuthoringStringInputPolicyV4 = "nonempty ASCII tokens without whitespace; string_sequence counts tokens; empty sequences are supported when the grammar and ranges allow them"
)

// This is server runtime capability metadata, independent of the user's frozen
// problem facts. One declaration feeds fresh briefs and resumed authoring calls.
type authoringInputCapabilitiesV4 struct {
	CapabilityVersion         string   `json:"capability_version"`
	InputGrammarProfile       string   `json:"input_grammar_profile"`
	SupportedInputSymbolTypes []string `json:"supported_input_symbol_types"`
	StringInputPolicy         string   `json:"string_input_policy"`
	SampleInputMaxBytes       int      `json:"sample_input_max_bytes"`
	TestCaseInputMaxBytes     int      `json:"test_case_input_max_bytes"`
}

func currentAuthoringInputCapabilitiesV4() authoringInputCapabilitiesV4 {
	return authoringInputCapabilitiesV4{
		CapabilityVersion:         "algoforge.authoring-input-capabilities.v4",
		InputGrammarProfile:       domain.SemanticGrammarTokenLinesV1,
		SupportedInputSymbolTypes: []string{domain.SemanticSymbolInteger, domain.SemanticSymbolIntegerSequence, domain.SemanticSymbolString, domain.SemanticSymbolStringSequence},
		StringInputPolicy:         currentAuthoringStringInputPolicyV4,
		SampleInputMaxBytes:       1 << 20,
		TestCaseInputMaxBytes:     8 << 20,
	}
}

func authoringPlanSystemPromptV4() (string, error) {
	v3, err := authoringPlanSystemPromptV3()
	if err != nil {
		return "", err
	}
	prefix, schema, ok := strings.Cut(v3, "Complete output JSON Schema:\n")
	if !ok {
		return "", fmt.Errorf("authoring output schema marker is missing")
	}
	capabilities, err := json.Marshal(currentAuthoringInputCapabilitiesV4())
	if err != nil {
		return "", err
	}
	return prefix + authoringRuntimeCapabilityInstructionsV4 + "\nCurrent server input capabilities (authoritative runtime metadata):\n" + string(capabilities) + "\n\nComplete output JSON Schema:\n" + schema, nil
}

func authoringPlanFormatRepairRequestV4(original *llm.Request, invalidOutput string, formatErr error) *llm.Request {
	repair := authoringPlanFormatRepairRequestV2(original, invalidOutput, formatErr)
	repair.PromptID, repair.PromptVersion = AuthoringPlanFormatRepairPromptIDV4, "v4"
	return repair
}

const authoringRuntimeCapabilityInstructionsV4 = `
Current capabilities override historical capability metadata only:
A canonical brief stored by an older run can contain
supported_input_symbol_types=[integer,integer_sequence]. That field describes
the old server capability, not an input restriction requested by the user.
Use the current server input capabilities below even when the historical brief
contains the older list. Do not reject a frozen task merely because it needs
nonempty ASCII token strings or string_sequences: those inputs are supported.
Preserve the original frozen_concept and required_facts, including their actual
input alphabet and length/count rules. Do not silently convert strings to
integers, add algorithmic complexity or change the requested difficulty.
The wider SemanticSpec enum is not the runtime support list: input graph/tree/
grid/set/multiset/boolean serialization remains unsupported by this pipeline.
String fields mean nonempty ASCII tokens without whitespace; string_sequence
length_range measures token count, not each token's character length. A scalar
string needs length_range min>=1; an empty string_sequence can have zero tokens
when the actual task, grammar and ranges permit it. Unicode, empty scalar
strings and whole-line text with spaces must receive an explicit unsupported
reason, without changing the user's problem to fit a different representation.

Boundary policy applies independently to EVERY symbol:
- zero_one_required is only for integers, requires an integer_range and two
  explicit value facts for 0 and 1 with allowed matching that range.
- explicit requires at least one meaningful boundary fact for that symbol,
  even when five or more facts already exist for other symbols. An input score
  integer with explicit needs its own value boundary (for example its actual
  minimum); a one-character input target marker string with explicit needs its
  own length=1 boundary. These are illustrative names, not extra task symbols.
- not_applicable forbids ALL boundary facts for that symbol, including facts
  with allowed=false. Do not attach forbidden boundaries to a not_applicable
  symbol to fill the five-fact minimum. Choose a policy consistent with the
  actual symbol and facts; do not remove or invent task requirements to pass.
Measures depend on type: integer/boolean=value; integer_sequence=length or
element_value; string/string_sequence/grid=length; set/multiset/graph/tree=
cardinality. A string_sequence's length counts tokens. Do not use element_range
as a boundary measure or value as a marker-string length measure.
Forbidden boundaries remain factual declarations in semantic_spec.boundaries,
but generated judge inputs must NOT hit them. Authoring test_intents.boundary_refs
reference only allowed=true boundaries of input-scoped symbols that a valid
test can actually hit, never forbidden boundaries or output/derived values.
Do not use an invalid input as a required ordinary judge testcase. For example,
if n=0 is forbidden, keep its allowed=false fact but do not reference that fact
in a normal testcase intent. A legal allowed edge belongs in the test intent.
`
