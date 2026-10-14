// Package spec implements deterministic validation for the versioned problem
// fact and authoring contracts. It does not infer semantics from Markdown.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const (
	SampleInputParserRuleVersionV1               = "algoforge.sample-input-parser.rules.v1"
	SampleInputParserTokenStringsRuleVersionV2   = "algoforge.sample-input-parser.token-strings.rules.v2"
	TestCaseInputParserRuleVersionV2             = "algoforge.testcase-input-parser.rules.v2"
	TestCaseInputParserTokenStringsRuleVersionV2 = "algoforge.testcase-input-parser.token-strings.rules.v2"

	SampleInputBindingIntegerV1         = "integer"
	SampleInputBindingIntegerSequenceV1 = "integer_sequence"
	SampleInputBindingStringV2          = "string"
	SampleInputBindingStringSequenceV2  = "string_sequence"

	maxSampleInputBytesV1         = 1 << 20
	maxSampleInputGrammarLinesV1  = 4096
	maxSampleInputPhysicalLinesV1 = 100000
	maxSampleInputTokensV1        = 250000
	maxSampleInputExpressionDepth = 16
	maxSampleInputExpressionBits  = 4096
)

// SampleInputBindingV1 is the typed result of consuming one input symbol from
// the authoritative token-line grammar. Exactly one value member is populated.
// Bindings retain grammar-field order so their JSON hash is deterministic.
type SampleInputBindingV1 struct {
	Symbol   string   `json:"symbol"`
	Kind     string   `json:"kind"`
	Integer  *int64   `json:"integer,omitempty"`
	Integers []int64  `json:"integers,omitempty"`
	String   string   `json:"string,omitempty"`
	Strings  []string `json:"strings,omitempty"`
}

func (binding SampleInputBindingV1) Length() (int64, bool) {
	switch binding.Kind {
	case SampleInputBindingIntegerSequenceV1:
		return int64(len(binding.Integers)), true
	case SampleInputBindingStringV2:
		return int64(len(binding.String)), true
	case SampleInputBindingStringSequenceV2:
		return int64(len(binding.Strings)), true
	default:
		return 0, false
	}
}

// Integer-only contracts retain their original bytes and rule identity. String
// contracts use nonempty ASCII tokens, with byte length equal to character length.
func SampleInputParserRuleVersionForSpecV1(spec domain.SemanticSpecV1) string {
	for _, symbol := range spec.Symbols {
		if symbol.Scope == domain.SemanticSymbolScopeInput && (symbol.Type == domain.SemanticSymbolString || symbol.Type == domain.SemanticSymbolStringSequence) {
			return SampleInputParserTokenStringsRuleVersionV2
		}
	}
	return SampleInputParserRuleVersionV1
}

// ParsedSampleInputV1 is an input-only receipt. It deliberately carries no
// expected output and makes no correctness claim about a solution program.
type ParsedSampleInputV1 struct {
	ParserRuleVersion  string                 `json:"parser_rule_version"`
	InputGrammarSHA256 string                 `json:"input_grammar_sha256"`
	CanonicalInput     string                 `json:"canonical_input"`
	CanonicalSHA256    string                 `json:"canonical_sha256"`
	BindingSHA256      string                 `json:"binding_sha256"`
	Bindings           []SampleInputBindingV1 `json:"bindings"`
}

type sampleInputParserV1 struct {
	spec             domain.SemanticSpecV1
	symbols          map[string]domain.SemanticSymbolV1
	bindings         map[string]*SampleInputBindingV1
	bindingOrder     []string
	physicalLines    []string
	lineIndex        int
	tokenCount       int
	canonical        []string
	maxPhysicalLines int
	maxTokens        int
}

type inputParserLimitsV1 struct{ bytes, physicalLines, tokens int }

var sampleInputLimitsV1 = inputParserLimitsV1{maxSampleInputBytesV1, maxSampleInputPhysicalLinesV1, maxSampleInputTokensV1}
var testCaseInputLimitsV2 = inputParserLimitsV1{8 << 20, 1000000, 1000000}

func TestCaseInputParserRuleVersionForSpecV2(spec domain.SemanticSpecV1) string {
	if SampleInputParserRuleVersionForSpecV1(spec) == SampleInputParserTokenStringsRuleVersionV2 {
		return TestCaseInputParserTokenStringsRuleVersionV2
	}
	return TestCaseInputParserRuleVersionV2
}

// InputGrammarSHA256V1 returns the JSON identity of the exact grammar used by
// ParseSampleInputV1. domain.SemanticGrammarV1 contains no map fields, so the
// standard JSON encoding is a stable canonical representation here.
func InputGrammarSHA256V1(grammar domain.SemanticGrammarV1) string {
	encoded, err := json.Marshal(grammar)
	if err != nil {
		panic(fmt.Sprintf("marshal deterministic input grammar: %v", err))
	}
	return sampleInputSHA256V1(encoded)
}

// ParseSampleInputV1 parses one untrusted candidate against the input grammar.
// Integer and integer-sequence contracts retain rule v1. ASCII token strings
// and their sequences use rule v2; other input types remain unsupported.
func ParseSampleInputV1(spec domain.SemanticSpecV1, raw string) (ParsedSampleInputV1, error) {
	return parseInputReceiptV1(spec, raw, sampleInputLimitsV1, SampleInputParserRuleVersionForSpecV1(spec))
}

// ParseTestCaseInputV2 validates full test-case files under the existing CAS
// budget. It deliberately does not enlarge the sample-input v1 contract.
func ParseTestCaseInputV2(spec domain.SemanticSpecV1, raw string) (ParsedSampleInputV1, error) {
	return parseInputReceiptV1(spec, raw, testCaseInputLimitsV2, TestCaseInputParserRuleVersionForSpecV2(spec))
}

func parseInputReceiptV1(spec domain.SemanticSpecV1, raw string, limits inputParserLimitsV1, rule string) (ParsedSampleInputV1, error) {
	canonical, bindings, err := parseInputCandidateWithLimitsV1(spec, raw, limits)
	if err != nil {
		return ParsedSampleInputV1{}, err
	}

	// A canonical receipt must itself be accepted and byte-stable. Keep this
	// check inside the parser so downstream callers cannot accidentally omit it.
	reparsedCanonical, reparsedBindings, err := parseInputCandidateWithLimitsV1(spec, canonical, limits)
	if err != nil {
		return ParsedSampleInputV1{}, fmt.Errorf("canonical sample input did not reparse: %w", err)
	}
	if reparsedCanonical != canonical || !sampleInputBindingsEqualV1(bindings, reparsedBindings) {
		return ParsedSampleInputV1{}, fmt.Errorf("canonical sample input is not byte-stable")
	}

	bindingBytes, err := json.Marshal(bindings)
	if err != nil {
		return ParsedSampleInputV1{}, fmt.Errorf("marshal deterministic sample bindings: %w", err)
	}
	return ParsedSampleInputV1{
		ParserRuleVersion:  rule,
		InputGrammarSHA256: InputGrammarSHA256V1(spec.InputGrammar),
		CanonicalInput:     canonical,
		CanonicalSHA256:    sampleInputSHA256V1([]byte(canonical)),
		BindingSHA256:      sampleInputSHA256V1(bindingBytes),
		Bindings:           bindings,
	}, nil
}

func parseSampleInputCandidateV1(spec domain.SemanticSpecV1, raw string) (string, []SampleInputBindingV1, error) {
	return parseInputCandidateWithLimitsV1(spec, raw, sampleInputLimitsV1)
}

func parseInputCandidateWithLimitsV1(spec domain.SemanticSpecV1, raw string, limits inputParserLimitsV1) (string, []SampleInputBindingV1, error) {
	if len(raw) > limits.bytes {
		return "", nil, fmt.Errorf("sample input exceeds %d bytes", limits.bytes)
	}
	lines, err := splitInputLinesWithLimitV1(raw, limits.physicalLines)
	if err != nil {
		return "", nil, err
	}
	parser := &sampleInputParserV1{
		spec:             spec,
		bindings:         make(map[string]*SampleInputBindingV1),
		physicalLines:    lines,
		maxPhysicalLines: limits.physicalLines,
		maxTokens:        limits.tokens,
	}
	if err := parser.validateContract(); err != nil {
		return "", nil, err
	}
	if err := parser.parseGrammar(); err != nil {
		return "", nil, err
	}
	if parser.lineIndex != len(parser.physicalLines) {
		return "", nil, fmt.Errorf("sample input has %d extra physical line(s)", len(parser.physicalLines)-parser.lineIndex)
	}
	if err := parser.validateConstraints(); err != nil {
		return "", nil, err
	}
	if err := parser.validateInputOnlyRelations(); err != nil {
		return "", nil, err
	}
	if err := parser.validateForbiddenBoundaries(); err != nil {
		return "", nil, err
	}

	bindings := make([]SampleInputBindingV1, 0, len(parser.bindingOrder))
	for _, symbol := range parser.bindingOrder {
		bindings = append(bindings, cloneSampleInputBindingV1(*parser.bindings[symbol]))
	}
	return strings.Join(parser.canonical, "\n") + sampleInputFinalLFV1(parser.canonical), bindings, nil
}

func splitSampleInputLinesV1(raw string) ([]string, error) {
	return splitInputLinesWithLimitV1(raw, maxSampleInputPhysicalLinesV1)
}

func splitInputLinesWithLimitV1(raw string, maxLines int) ([]string, error) {
	if !utf8.ValidString(raw) {
		return nil, fmt.Errorf("sample input is not valid UTF-8")
	}
	if strings.HasPrefix(raw, "\ufeff") {
		return nil, fmt.Errorf("sample input must not contain a UTF-8 BOM")
	}
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	for _, value := range normalized {
		if value == '\r' {
			return nil, fmt.Errorf("sample input contains a bare carriage return")
		}
		if value != ' ' && value != '\n' && unicode.IsSpace(value) {
			return nil, fmt.Errorf("sample input contains unsupported whitespace U+%04X", value)
		}
		if value != '\n' && unicode.IsControl(value) {
			return nil, fmt.Errorf("sample input contains forbidden control character U+%04X", value)
		}
	}
	if normalized == "" {
		return []string{}, nil
	}
	lines := strings.Split(normalized, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxLines {
		return nil, fmt.Errorf("sample input exceeds %d physical lines", maxLines)
	}
	return lines, nil
}

func (p *sampleInputParserV1) validateContract() error {
	if p.spec.SchemaVersion != domain.SemanticSpecSchemaV1 {
		return fmt.Errorf("unsupported SemanticSpec schema %q", p.spec.SchemaVersion)
	}
	if p.spec.InputGrammar.Profile != domain.SemanticGrammarTokenLinesV1 {
		return fmt.Errorf("unsupported input grammar profile %q", p.spec.InputGrammar.Profile)
	}
	if len(p.spec.InputGrammar.Lines) == 0 || len(p.spec.InputGrammar.Lines) > maxSampleInputGrammarLinesV1 {
		return fmt.Errorf("input grammar line count must be in [1,%d]", maxSampleInputGrammarLinesV1)
	}

	p.symbols = make(map[string]domain.SemanticSymbolV1, len(p.spec.Symbols))
	for _, symbol := range p.spec.Symbols {
		if symbol.Name == "" {
			return fmt.Errorf("SemanticSpec contains an unnamed symbol")
		}
		if _, duplicate := p.symbols[symbol.Name]; duplicate {
			return fmt.Errorf("SemanticSpec symbol %q is duplicated", symbol.Name)
		}
		p.symbols[symbol.Name] = symbol
		if symbol.Scope == domain.SemanticSymbolScopeInput &&
			symbol.Type != domain.SemanticSymbolInteger &&
			symbol.Type != domain.SemanticSymbolIntegerSequence &&
			symbol.Type != domain.SemanticSymbolString &&
			symbol.Type != domain.SemanticSymbolStringSequence {
			return fmt.Errorf("input symbol %q has unsupported parser type %q", symbol.Name, symbol.Type)
		}
		if symbol.Scope == domain.SemanticSymbolScopeInput && symbol.Type == domain.SemanticSymbolString {
			found, minimum := false, int64(0)
			for _, constraint := range p.spec.Constraints {
				if constraint.Subject == symbol.Name && constraint.Kind == domain.SemanticConstraintLengthRange && constraint.Min != nil {
					if !found || *constraint.Min > minimum {
						minimum = *constraint.Min
					}
					found = true
				}
			}
			if !found || minimum < 1 {
				return fmt.Errorf("token string requires non-empty ASCII: symbol %q needs length_range excluding empty strings; token-lines cannot express an empty string", symbol.Name)
			}
		}
	}

	lineIDs := make(map[string]struct{}, len(p.spec.InputGrammar.Lines))
	fieldSymbols := make(map[string]struct{})
	for lineIndex, line := range p.spec.InputGrammar.Lines {
		if strings.TrimSpace(line.ID) == "" || strings.TrimSpace(line.ID) != line.ID {
			return fmt.Errorf("input grammar line %d has an invalid id", lineIndex)
		}
		if _, duplicate := lineIDs[line.ID]; duplicate {
			return fmt.Errorf("input grammar line id %q is duplicated", line.ID)
		}
		lineIDs[line.ID] = struct{}{}
		if len(line.Fields) == 0 {
			return fmt.Errorf("input grammar line %q has no fields", line.ID)
		}
		for fieldIndex, field := range line.Fields {
			symbol, exists := p.symbols[field.Symbol]
			if !exists {
				return fmt.Errorf("input grammar line %q field %d references undefined symbol %q", line.ID, fieldIndex, field.Symbol)
			}
			if symbol.Scope != domain.SemanticSymbolScopeInput {
				return fmt.Errorf("input grammar field %q is not input-scoped", field.Symbol)
			}
			if _, duplicate := fieldSymbols[field.Symbol]; duplicate {
				return fmt.Errorf("input grammar field symbol %q is duplicated", field.Symbol)
			}
			fieldSymbols[field.Symbol] = struct{}{}
			switch field.Mode {
			case domain.SemanticGrammarFieldScalar:
				if (symbol.Type != domain.SemanticSymbolInteger && symbol.Type != domain.SemanticSymbolString) || field.Count != nil || !sampleInputLiteralOneV1(line.Repeat) {
					return fmt.Errorf("scalar field %q requires integer or token-string type, no count, and literal repeat=1", field.Symbol)
				}
			case domain.SemanticGrammarFieldSequence:
				if (symbol.Type != domain.SemanticSymbolIntegerSequence && symbol.Type != domain.SemanticSymbolStringSequence) || field.Count == nil || !sampleInputLiteralOneV1(line.Repeat) {
					return fmt.Errorf("sequence field %q requires integer_sequence or string_sequence type, count, and literal repeat=1", field.Symbol)
				}
			case domain.SemanticGrammarFieldElementPerRepeat:
				if (symbol.Type != domain.SemanticSymbolIntegerSequence && symbol.Type != domain.SemanticSymbolStringSequence) || field.Count != nil {
					return fmt.Errorf("element_per_repeat field %q requires integer_sequence or string_sequence type and no count", field.Symbol)
				}
			default:
				return fmt.Errorf("input grammar field %q has unsupported mode %q", field.Symbol, field.Mode)
			}
		}
	}
	for _, symbol := range p.spec.Symbols {
		if symbol.Scope != domain.SemanticSymbolScopeInput {
			continue
		}
		if _, covered := fieldSymbols[symbol.Name]; !covered {
			return fmt.Errorf("input symbol %q is absent from the input grammar", symbol.Name)
		}
	}
	return nil
}

func (p *sampleInputParserV1) parseGrammar() error {
	expandedLines := 0
	for _, line := range p.spec.InputGrammar.Lines {
		repeatValue, err := p.evalExpression(line.Repeat, 0)
		if err != nil {
			return fmt.Errorf("evaluate repeat for grammar line %q: %w", line.ID, err)
		}
		repeat, err := sampleInputExtentV1(repeatValue, p.maxPhysicalLines-expandedLines, "line repeat")
		if err != nil {
			return fmt.Errorf("grammar line %q: %w", line.ID, err)
		}
		expandedLines += repeat

		// element_per_repeat symbols exist even when repeat is zero.
		for _, field := range line.Fields {
			if field.Mode == domain.SemanticGrammarFieldElementPerRepeat {
				if err := p.addSequenceBinding(field.Symbol); err != nil {
					return err
				}
			}
		}

		for repetition := 0; repetition < repeat; repetition++ {
			if p.lineIndex >= len(p.physicalLines) {
				return fmt.Errorf("sample input is missing physical line %d for grammar line %q", p.lineIndex+1, line.ID)
			}
			tokens := strings.Fields(p.physicalLines[p.lineIndex])
			cursor := 0
			canonicalTokens := make([]string, 0, len(tokens))
			for _, field := range line.Fields {
				switch field.Mode {
				case domain.SemanticGrammarFieldScalar:
					if p.symbols[field.Symbol].Type == domain.SemanticSymbolString {
						value, next, err := consumeSampleStringV2(tokens, cursor)
						if err != nil {
							return fmt.Errorf("grammar line %q field %q: %w", line.ID, field.Symbol, err)
						}
						cursor = next
						canonicalTokens = append(canonicalTokens, value)
						if _, exists := p.bindings[field.Symbol]; exists {
							return fmt.Errorf("sample input symbol %q was bound more than once", field.Symbol)
						}
						p.bindings[field.Symbol] = &SampleInputBindingV1{Symbol: field.Symbol, Kind: SampleInputBindingStringV2, String: value}
						p.bindingOrder = append(p.bindingOrder, field.Symbol)
						continue
					}
					value, canonical, next, err := consumeSampleIntegerV1(tokens, cursor)
					if err != nil {
						return fmt.Errorf("grammar line %q field %q: %w", line.ID, field.Symbol, err)
					}
					cursor = next
					canonicalTokens = append(canonicalTokens, canonical)
					if err := p.addIntegerBinding(field.Symbol, value); err != nil {
						return err
					}
				case domain.SemanticGrammarFieldSequence:
					countValue, err := p.evalExpression(*field.Count, 0)
					if err != nil {
						return fmt.Errorf("evaluate count for field %q: %w", field.Symbol, err)
					}
					count, err := sampleInputExtentV1(countValue, p.maxTokens-p.tokenCount-cursor, "sequence count")
					if err != nil {
						return fmt.Errorf("field %q: %w", field.Symbol, err)
					}
					if p.symbols[field.Symbol].Type == domain.SemanticSymbolStringSequence {
						if err := p.addSequenceBinding(field.Symbol); err != nil {
							return err
						}
						for index := 0; index < count; index++ {
							value, next, err := consumeSampleStringV2(tokens, cursor)
							if err != nil {
								return fmt.Errorf("grammar line %q field %q element %d: %w", line.ID, field.Symbol, index, err)
							}
							cursor = next
							canonicalTokens = append(canonicalTokens, value)
							p.bindings[field.Symbol].Strings = append(p.bindings[field.Symbol].Strings, value)
						}
						continue
					}
					values := make([]int64, 0, count)
					for index := 0; index < count; index++ {
						value, canonical, next, err := consumeSampleIntegerV1(tokens, cursor)
						if err != nil {
							return fmt.Errorf("grammar line %q field %q element %d: %w", line.ID, field.Symbol, index, err)
						}
						cursor = next
						values = append(values, value)
						canonicalTokens = append(canonicalTokens, canonical)
					}
					if err := p.addSequenceBindingWithValues(field.Symbol, values); err != nil {
						return err
					}
				case domain.SemanticGrammarFieldElementPerRepeat:
					if p.symbols[field.Symbol].Type == domain.SemanticSymbolStringSequence {
						value, next, err := consumeSampleStringV2(tokens, cursor)
						if err != nil {
							return fmt.Errorf("grammar line %q field %q repetition %d: %w", line.ID, field.Symbol, repetition, err)
						}
						cursor = next
						canonicalTokens = append(canonicalTokens, value)
						p.bindings[field.Symbol].Strings = append(p.bindings[field.Symbol].Strings, value)
						continue
					}
					value, canonical, next, err := consumeSampleIntegerV1(tokens, cursor)
					if err != nil {
						return fmt.Errorf("grammar line %q field %q repetition %d: %w", line.ID, field.Symbol, repetition, err)
					}
					cursor = next
					canonicalTokens = append(canonicalTokens, canonical)
					p.bindings[field.Symbol].Integers = append(p.bindings[field.Symbol].Integers, value)
				}
			}
			if cursor != len(tokens) {
				if len(line.Fields) == 1 && line.Fields[0].Mode == domain.SemanticGrammarFieldScalar && p.symbols[line.Fields[0].Symbol].Type == domain.SemanticSymbolString {
					return fmt.Errorf("token string requires non-empty ASCII without whitespace: whole-line text with spaces is unsupported")
				}
				return fmt.Errorf("grammar line %q physical line %d has %d extra token(s)", line.ID, p.lineIndex+1, len(tokens)-cursor)
			}
			p.tokenCount += cursor
			if p.tokenCount > p.maxTokens {
				return fmt.Errorf("sample input exceeds %d tokens", p.maxTokens)
			}
			p.canonical = append(p.canonical, strings.Join(canonicalTokens, " "))
			p.lineIndex++
		}
	}
	return nil
}

func (p *sampleInputParserV1) addIntegerBinding(symbol string, value int64) error {
	if _, exists := p.bindings[symbol]; exists {
		return fmt.Errorf("sample input symbol %q was bound more than once", symbol)
	}
	copyValue := value
	p.bindings[symbol] = &SampleInputBindingV1{Symbol: symbol, Kind: SampleInputBindingIntegerV1, Integer: &copyValue}
	p.bindingOrder = append(p.bindingOrder, symbol)
	return nil
}

func (p *sampleInputParserV1) addSequenceBinding(symbol string) error {
	if _, exists := p.bindings[symbol]; exists {
		return fmt.Errorf("sample input symbol %q was bound more than once", symbol)
	}
	p.bindings[symbol] = &SampleInputBindingV1{Symbol: symbol, Kind: SampleInputBindingIntegerSequenceV1, Integers: []int64{}}
	if p.symbols[symbol].Type == domain.SemanticSymbolStringSequence {
		p.bindings[symbol] = &SampleInputBindingV1{Symbol: symbol, Kind: SampleInputBindingStringSequenceV2, Strings: []string{}}
	}
	p.bindingOrder = append(p.bindingOrder, symbol)
	return nil
}

func (p *sampleInputParserV1) addSequenceBindingWithValues(symbol string, values []int64) error {
	if err := p.addSequenceBinding(symbol); err != nil {
		return err
	}
	p.bindings[symbol].Integers = append([]int64(nil), values...)
	if len(values) == 0 {
		p.bindings[symbol].Integers = []int64{}
	}
	return nil
}

func consumeSampleIntegerV1(tokens []string, cursor int) (int64, string, int, error) {
	if cursor >= len(tokens) {
		return 0, "", cursor, fmt.Errorf("missing integer token")
	}
	token := tokens[cursor]
	if !sampleInputIntegerTokenV1(token) {
		return 0, "", cursor, fmt.Errorf("token %q is not an ASCII decimal integer", token)
	}
	value, err := strconv.ParseInt(token, 10, 64)
	if err != nil {
		return 0, "", cursor, fmt.Errorf("integer token %q is outside int64", token)
	}
	return value, strconv.FormatInt(value, 10), cursor + 1, nil
}

func consumeSampleStringV2(tokens []string, cursor int) (string, int, error) {
	if cursor >= len(tokens) || tokens[cursor] == "" {
		return "", cursor, fmt.Errorf("token string requires non-empty ASCII without whitespace; empty strings cannot be expressed by token-lines grammar")
	}
	for _, character := range tokens[cursor] {
		if character < 33 || character > 126 {
			return "", cursor, fmt.Errorf("token string requires non-empty ASCII without whitespace; found unsupported character U+%04X", character)
		}
	}
	return tokens[cursor], cursor + 1, nil
}

func sampleInputIntegerTokenV1(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '+' || value[0] == '-' {
		start = 1
	}
	if start == len(value) {
		return false
	}
	for index := start; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func sampleInputExtentV1(value *big.Int, maximum int, label string) (int, error) {
	if value.Sign() < 0 {
		return 0, fmt.Errorf("%s cannot be negative", label)
	}
	if !value.IsInt64() {
		return 0, fmt.Errorf("%s is outside int64", label)
	}
	integer := value.Int64()
	if integer > int64(maximum) {
		return 0, fmt.Errorf("%s exceeds remaining resource limit %d", label, maximum)
	}
	return int(integer), nil
}

func (p *sampleInputParserV1) evalExpression(expression domain.SemanticExpressionV1, depth int) (*big.Int, error) {
	if depth > maxSampleInputExpressionDepth {
		return nil, fmt.Errorf("expression exceeds depth %d", maxSampleInputExpressionDepth)
	}
	var value *big.Int
	switch expression.Kind {
	case domain.SemanticExpressionInteger:
		if expression.Value == nil || expression.Symbol != "" || len(expression.Args) != 0 {
			return nil, fmt.Errorf("malformed integer expression")
		}
		value = big.NewInt(*expression.Value)
	case domain.SemanticExpressionSymbol:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 {
			return nil, fmt.Errorf("malformed symbol expression")
		}
		binding, exists := p.bindings[expression.Symbol]
		if !exists || binding.Kind != SampleInputBindingIntegerV1 || binding.Integer == nil {
			return nil, fmt.Errorf("integer symbol %q is not bound", expression.Symbol)
		}
		value = big.NewInt(*binding.Integer)
	case domain.SemanticExpressionLength:
		if expression.Symbol == "" || expression.Value != nil || len(expression.Args) != 0 {
			return nil, fmt.Errorf("malformed length expression")
		}
		binding, exists := p.bindings[expression.Symbol]
		if !exists {
			return nil, fmt.Errorf("sequence symbol %q is not bound", expression.Symbol)
		}
		length, supported := binding.Length()
		if !supported {
			return nil, fmt.Errorf("length symbol %q is not a string or sequence binding", expression.Symbol)
		}
		value = big.NewInt(length)
	case domain.SemanticExpressionCardinality:
		return nil, fmt.Errorf("cardinality expressions are unsupported by sample parser v1")
	case domain.SemanticExpressionAdd, domain.SemanticExpressionSubtract, domain.SemanticExpressionMultiply, domain.SemanticExpressionFloorDivide:
		if expression.Symbol != "" || expression.Value != nil || len(expression.Args) != 2 {
			return nil, fmt.Errorf("malformed %s expression", expression.Kind)
		}
		left, err := p.evalExpression(expression.Args[0], depth+1)
		if err != nil {
			return nil, err
		}
		right, err := p.evalExpression(expression.Args[1], depth+1)
		if err != nil {
			return nil, err
		}
		switch expression.Kind {
		case domain.SemanticExpressionAdd:
			value = new(big.Int).Add(left, right)
		case domain.SemanticExpressionSubtract:
			value = new(big.Int).Sub(left, right)
		case domain.SemanticExpressionMultiply:
			value = new(big.Int).Mul(left, right)
		case domain.SemanticExpressionFloorDivide:
			if right.Sign() == 0 {
				return nil, fmt.Errorf("floor division by zero")
			}
			value = sampleInputFloorDivideV1(left, right)
		}
	default:
		return nil, fmt.Errorf("unsupported expression kind %q", expression.Kind)
	}
	if value.BitLen() > maxSampleInputExpressionBits {
		return nil, fmt.Errorf("expression result exceeds %d bits", maxSampleInputExpressionBits)
	}
	return value, nil
}

func sampleInputFloorDivideV1(numerator, denominator *big.Int) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() != 0 && numerator.Sign() != denominator.Sign() {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

type sampleInputRangeV1 struct {
	set bool
	min int64
	max int64
}

func (p *sampleInputParserV1) validateConstraints() error {
	ranges := make(map[string]sampleInputRangeV1)
	for index, constraint := range p.spec.Constraints {
		symbol, exists := p.symbols[constraint.Subject]
		if !exists {
			return fmt.Errorf("constraint %d references undefined symbol %q", index, constraint.Subject)
		}
		if symbol.Scope != domain.SemanticSymbolScopeInput {
			continue
		}
		measure, supported := sampleInputConstraintMeasureV1(symbol.Type, constraint.Kind)
		if !supported || constraint.Min == nil || constraint.Max == nil || *constraint.Min > *constraint.Max {
			return fmt.Errorf("constraint %d is unsupported or malformed for input symbol %q", index, constraint.Subject)
		}
		if measure == domain.SemanticBoundaryMeasureLength && *constraint.Min < 0 {
			return fmt.Errorf("constraint %d gives input sequence %q a negative minimum length", index, constraint.Subject)
		}
		key := sampleInputMeasureKeyV1(constraint.Subject, measure)
		current := ranges[key]
		if !current.set {
			current = sampleInputRangeV1{set: true, min: *constraint.Min, max: *constraint.Max}
		} else {
			if *constraint.Min > current.min {
				current.min = *constraint.Min
			}
			if *constraint.Max < current.max {
				current.max = *constraint.Max
			}
			if current.min > current.max {
				return fmt.Errorf("constraints for input symbol %q have an empty intersection", constraint.Subject)
			}
		}
		ranges[key] = current
	}

	for _, symbol := range p.spec.Symbols {
		if symbol.Scope != domain.SemanticSymbolScopeInput {
			continue
		}
		binding := p.bindings[symbol.Name]
		switch symbol.Type {
		case domain.SemanticSymbolInteger:
			valueRange, exists := ranges[sampleInputMeasureKeyV1(symbol.Name, domain.SemanticBoundaryMeasureValue)]
			if !exists {
				return fmt.Errorf("input integer %q lacks an integer_range constraint", symbol.Name)
			}
			if *binding.Integer < valueRange.min || *binding.Integer > valueRange.max {
				return fmt.Errorf("input integer %q=%d is outside [%d,%d]", symbol.Name, *binding.Integer, valueRange.min, valueRange.max)
			}
		case domain.SemanticSymbolIntegerSequence:
			lengthRange, lengthExists := ranges[sampleInputMeasureKeyV1(symbol.Name, domain.SemanticBoundaryMeasureLength)]
			elementRange, elementExists := ranges[sampleInputMeasureKeyV1(symbol.Name, domain.SemanticBoundaryMeasureElementValue)]
			if !lengthExists || !elementExists {
				return fmt.Errorf("input integer_sequence %q lacks length_range or element_range", symbol.Name)
			}
			length := int64(len(binding.Integers))
			if length < lengthRange.min || length > lengthRange.max {
				return fmt.Errorf("input sequence %q length %d is outside [%d,%d]", symbol.Name, length, lengthRange.min, lengthRange.max)
			}
			for index, value := range binding.Integers {
				if value < elementRange.min || value > elementRange.max {
					return fmt.Errorf("input sequence %q element %d=%d is outside [%d,%d]", symbol.Name, index, value, elementRange.min, elementRange.max)
				}
			}
		case domain.SemanticSymbolString, domain.SemanticSymbolStringSequence:
			lengthRange, exists := ranges[sampleInputMeasureKeyV1(symbol.Name, domain.SemanticBoundaryMeasureLength)]
			if !exists {
				return fmt.Errorf("input %s %q lacks length_range", symbol.Type, symbol.Name)
			}
			length, supported := binding.Length()
			if !supported || length < lengthRange.min || length > lengthRange.max {
				return fmt.Errorf("input %s %q length %d is outside [%d,%d]", symbol.Type, symbol.Name, length, lengthRange.min, lengthRange.max)
			}
		}
	}
	return nil
}

func sampleInputConstraintMeasureV1(symbolType, kind string) (string, bool) {
	switch {
	case symbolType == domain.SemanticSymbolInteger && kind == domain.SemanticConstraintIntegerRange:
		return domain.SemanticBoundaryMeasureValue, true
	case symbolType == domain.SemanticSymbolIntegerSequence && kind == domain.SemanticConstraintLengthRange:
		return domain.SemanticBoundaryMeasureLength, true
	case symbolType == domain.SemanticSymbolIntegerSequence && kind == domain.SemanticConstraintElementRange:
		return domain.SemanticBoundaryMeasureElementValue, true
	case (symbolType == domain.SemanticSymbolString || symbolType == domain.SemanticSymbolStringSequence) && kind == domain.SemanticConstraintLengthRange:
		return domain.SemanticBoundaryMeasureLength, true
	default:
		return "", false
	}
}

func (p *sampleInputParserV1) validateInputOnlyRelations() error {
	for index, relation := range p.spec.Relations {
		refs := append(sampleInputExpressionRefsV1(relation.Left), sampleInputExpressionRefsV1(relation.Right)...)
		inputOnly := true
		for _, ref := range refs {
			symbol, exists := p.symbols[ref]
			if !exists {
				return fmt.Errorf("relation %d references undefined symbol %q", index, ref)
			}
			if symbol.Scope != domain.SemanticSymbolScopeInput {
				inputOnly = false
			}
		}
		if !inputOnly {
			continue
		}
		left, err := p.evalExpression(relation.Left, 0)
		if err != nil {
			return fmt.Errorf("evaluate input-only relation %q left side: %w", relation.ID, err)
		}
		right, err := p.evalExpression(relation.Right, 0)
		if err != nil {
			return fmt.Errorf("evaluate input-only relation %q right side: %w", relation.ID, err)
		}
		valid, supported := sampleInputRelationHoldsV1(relation.Operator, left, right)
		if !supported {
			return fmt.Errorf("input-only relation %q has unsupported operator %q", relation.ID, relation.Operator)
		}
		if !valid {
			return fmt.Errorf("input-only relation %q is false for the sample candidate", relation.ID)
		}
	}
	return nil
}

func sampleInputRelationHoldsV1(operator string, left, right *big.Int) (bool, bool) {
	comparison := left.Cmp(right)
	switch operator {
	case domain.SemanticRelationEqual:
		return comparison == 0, true
	case domain.SemanticRelationLessThan:
		return comparison < 0, true
	case domain.SemanticRelationLessThanOrEqual:
		return comparison <= 0, true
	case domain.SemanticRelationGreaterThan:
		return comparison > 0, true
	case domain.SemanticRelationGreaterThanOrEqual:
		return comparison >= 0, true
	default:
		return false, false
	}
}

func (p *sampleInputParserV1) validateForbiddenBoundaries() error {
	for index, boundary := range p.spec.Boundaries {
		symbol, exists := p.symbols[boundary.Symbol]
		if !exists {
			return fmt.Errorf("boundary %d references undefined symbol %q", index, boundary.Symbol)
		}
		if symbol.Scope != domain.SemanticSymbolScopeInput {
			continue
		}
		supportedMeasure := (symbol.Type == domain.SemanticSymbolInteger && boundary.Measure == domain.SemanticBoundaryMeasureValue) ||
			(symbol.Type == domain.SemanticSymbolIntegerSequence &&
				(boundary.Measure == domain.SemanticBoundaryMeasureLength || boundary.Measure == domain.SemanticBoundaryMeasureElementValue)) ||
			((symbol.Type == domain.SemanticSymbolString || symbol.Type == domain.SemanticSymbolStringSequence) && boundary.Measure == domain.SemanticBoundaryMeasureLength)
		if !supportedMeasure {
			return fmt.Errorf("boundary %d has unsupported measure %q for input symbol %q", index, boundary.Measure, boundary.Symbol)
		}
		if boundary.Allowed {
			continue
		}
		binding := p.bindings[boundary.Symbol]
		forbidden := false
		switch {
		case symbol.Type == domain.SemanticSymbolInteger && boundary.Measure == domain.SemanticBoundaryMeasureValue:
			forbidden = *binding.Integer == boundary.Value
		case boundary.Measure == domain.SemanticBoundaryMeasureLength:
			length, supported := binding.Length()
			forbidden = supported && length == boundary.Value
		case symbol.Type == domain.SemanticSymbolIntegerSequence && boundary.Measure == domain.SemanticBoundaryMeasureElementValue:
			for _, value := range binding.Integers {
				if value == boundary.Value {
					forbidden = true
					break
				}
			}
		}
		if forbidden {
			return fmt.Errorf("sample candidate hits forbidden boundary %q", boundary.ID)
		}
	}
	return nil
}

func sampleInputExpressionRefsV1(expression domain.SemanticExpressionV1) []string {
	refs := []string{}
	if expression.Symbol != "" {
		refs = append(refs, expression.Symbol)
	}
	for _, argument := range expression.Args {
		refs = append(refs, sampleInputExpressionRefsV1(argument)...)
	}
	return refs
}

func sampleInputLiteralOneV1(expression domain.SemanticExpressionV1) bool {
	return expression.Kind == domain.SemanticExpressionInteger &&
		expression.Value != nil && *expression.Value == 1 &&
		expression.Symbol == "" && len(expression.Args) == 0
}

func sampleInputMeasureKeyV1(symbol, measure string) string {
	return symbol + "\x00" + measure
}

func sampleInputFinalLFV1(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return "\n"
}

func sampleInputSHA256V1(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func cloneSampleInputBindingV1(binding SampleInputBindingV1) SampleInputBindingV1 {
	cloned := binding
	if binding.Integer != nil {
		value := *binding.Integer
		cloned.Integer = &value
	}
	if binding.Integers != nil {
		cloned.Integers = append([]int64(nil), binding.Integers...)
		if len(binding.Integers) == 0 {
			cloned.Integers = []int64{}
		}
	}
	if binding.Strings != nil {
		cloned.Strings = append([]string{}, binding.Strings...)
	}
	return cloned
}

func sampleInputBindingsEqualV1(left, right []SampleInputBindingV1) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Symbol != right[index].Symbol || left[index].Kind != right[index].Kind {
			return false
		}
		if left[index].String != right[index].String || len(left[index].Strings) != len(right[index].Strings) {
			return false
		}
		for i := range left[index].Strings {
			if left[index].Strings[i] != right[index].Strings[i] {
				return false
			}
		}
		if (left[index].Integer == nil) != (right[index].Integer == nil) {
			return false
		}
		if left[index].Integer != nil && *left[index].Integer != *right[index].Integer {
			return false
		}
		if len(left[index].Integers) != len(right[index].Integers) {
			return false
		}
		for valueIndex := range left[index].Integers {
			if left[index].Integers[valueIndex] != right[index].Integers[valueIndex] {
				return false
			}
		}
	}
	return true
}
