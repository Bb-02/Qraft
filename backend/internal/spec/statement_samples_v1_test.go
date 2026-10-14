package spec

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestStatementSamplesV1RoundTripsZeroOneAndManyDeterministically(t *testing.T) {
	variants := []struct {
		name    string
		samples []StatementSampleV1
	}{
		{name: "zero"},
		{name: "one", samples: []StatementSampleV1{{Input: "1\n5\n", Output: "25"}}},
		{name: "many", samples: []StatementSampleV1{
			{Input: "1\n-7\n", Output: "49"},
			{Input: "3\n1 2 3\n", Output: "6\n```\nstill output"},
			{Input: "4\n4 3 2 1\n", Output: "10"},
		}},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			draft := "before\n" + StatementSamplesPlaceholderV1 + "\nafter\n"
			first, err := RenderStatementSamplesV1(draft, variant.samples)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			second, err := RenderStatementSamplesV1(draft, variant.samples)
			if err != nil || second != first {
				t.Fatalf("render is not deterministic: err=%v", err)
			}
			if strings.Contains(first, StatementSamplesPlaceholderV1) {
				t.Fatal("render retained placeholder")
			}
			parsed, err := ParseStatementSamplesV1(first)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(parsed) != len(variant.samples) ||
				(len(parsed) > 0 && !reflect.DeepEqual(parsed, variant.samples)) {
				t.Fatalf("round trip mismatch: got=%+v want=%+v", parsed, variant.samples)
			}
		})
	}
}

func TestStatementSamplesV1RejectsManualEditOrderingAndMarkerDefects(t *testing.T) {
	draft := "before\n" + StatementSamplesPlaceholderV1 + "\nafter\n"
	markdown, err := RenderStatementSamplesV1(draft, []StatementSampleV1{
		{Input: "1\n5\n", Output: "25"},
		{Input: "1\n7\n", Output: "49"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reordered := strings.Replace(markdown, "#### input1", "#### input9", 1)
	cases := map[string]string{
		"manual output edit":  strings.Replace(markdown, "\n25\n```", "\n26\n```", 1),
		"reordered label":     reordered,
		"duplicate section":   markdown + "\n" + markdown,
		"placeholder remains": markdown + StatementSamplesPlaceholderV1,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStatementSamplesV1(value); err == nil {
				t.Fatal("expected fail-closed parse error")
			}
		})
	}
	if _, err := RenderStatementSamplesV1("no marker", nil); err == nil {
		t.Fatal("missing marker was accepted")
	}
	if _, err := RenderStatementSamplesV1(draft, []StatementSampleV1{{Input: "1\r\n", Output: "ok"}}); err == nil {
		t.Fatal("bare/CRLF input was accepted by the fenced-text contract")
	}
}

func TestValidateSampleInputProfileV1RejectsUnsupportedTypesEvenWithoutSamples(t *testing.T) {
	one := int64(1)
	base := domain.SemanticSpecV1{
		SchemaVersion: domain.SemanticSpecSchemaV1,
		Symbols: []domain.SemanticSymbolV1{{
			Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput,
		}},
		InputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines: []domain.SemanticGrammarLineV1{{
				ID:     "line_n",
				Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one},
				Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}},
			}},
		},
	}
	if err := ValidateSampleInputProfileV1(base); err != nil {
		t.Fatalf("integer profile rejected: %v", err)
	}
	unsupported := base
	unsupported.Symbols = append([]domain.SemanticSymbolV1(nil), base.Symbols...)
	unsupported.Symbols[0].Type = domain.SemanticSymbolGraph
	if err := ValidateSampleInputProfileV1(unsupported); err == nil || !strings.Contains(err.Error(), "unsupported parser type") {
		t.Fatalf("unsupported grammar did not fail closed: %v", err)
	}
}

func TestStatementSamplesV1UsesSafeFencesForValidBacktickTokens(t *testing.T) {
	one, four := int64(1), int64(4)
	semanticSpec := domain.SemanticSpecV1{
		SchemaVersion: domain.SemanticSpecSchemaV1,
		Symbols:       []domain.SemanticSymbolV1{{Name: "s", Type: domain.SemanticSymbolString, Scope: domain.SemanticSymbolScopeInput}},
		InputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{{
			ID: "token", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one},
			Fields: []domain.SemanticGrammarFieldV1{{Symbol: "s", Mode: domain.SemanticGrammarFieldScalar}},
		}}},
		Constraints: []domain.SemanticConstraintV1{{Subject: "s", Kind: domain.SemanticConstraintLengthRange, Min: &one, Max: &four}},
	}
	samples := []StatementSampleV1{{Input: "```\n", Output: "````\n"}, {Input: "````\n", Output: "ok\n"}}
	for _, sample := range samples {
		parsed, err := ParseSampleInputV1(semanticSpec, sample.Input)
		if err != nil || parsed.Bindings[0].String != strings.TrimSuffix(sample.Input, "\n") {
			t.Fatalf("valid ASCII backtick token rejected: parsed=%+v err=%v", parsed, err)
		}
	}
	markdown, err := RenderStatementSamplesV1(StatementSamplesPlaceholderV1, samples)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown, "\n````text\n```\n````\n") ||
		!strings.Contains(markdown, "\n`````text\n````\n`````\n") {
		t.Fatal("rendered fences do not enclose the real backtick tokens safely")
	}
	parsed, err := ParseStatementSamplesV1(markdown)
	if err != nil || !reflect.DeepEqual(parsed, samples) {
		t.Fatalf("safe fence round trip failed: parsed=%+v err=%v", parsed, err)
	}
	for name, changed := range map[string]string{
		"overlong fence":        strings.Replace(markdown, "\n````text\n```\n````\n", "\n`````text\n```\n`````\n", 1),
		"closing mismatch":      strings.Replace(markdown, "\n````text\n```\n````\n", "\n````text\n```\n```\n", 1),
		"mixed legacy and safe": strings.Replace(markdown, "\n`````text\n````\n`````\n", "\n```text\n````\n```\n", 1),
		"edited actual input":   strings.Replace(markdown, "\n````text\n```\n````\n", "\n````text\n`a`\n````\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStatementSamplesV1(changed); err == nil {
				t.Fatal("noncanonical or edited fence section accepted")
			}
		})
	}
}

func TestStatementSamplesV1ReadsExactLegacyEmbeddedFenceRecord(t *testing.T) {
	const legacy = "<!-- ALGOFORGE_SAMPLES_V1_BEGIN count=1 -->\n### Samples\n" +
		"#### input1 bytes=4 sha256=47c06c561d5c327343685268a46b3391f3998eee890d2be51f0d21cc4a53ae6d\n```text\n```\n```\n" +
		"#### output1 bytes=3 sha256=dc51b8c96c2d745df3bd5590d990230a482fd247123599548e0632fdbf97fc22\n```text\nok\n```\n" +
		"<!-- ALGOFORGE_SAMPLES_V1_END -->"
	parsed, err := ParseStatementSamplesV1(legacy)
	if err != nil || !reflect.DeepEqual(parsed, []StatementSampleV1{{Input: "```\n", Output: "ok\n"}}) {
		t.Fatalf("exact historical bytes became unreadable: parsed=%+v err=%v", parsed, err)
	}
	newMarkdown, err := RenderStatementSamplesV1(StatementSamplesPlaceholderV1, parsed)
	if err != nil || newMarkdown == legacy || !strings.Contains(newMarkdown, "\n````text\n```\n````\n") {
		t.Fatalf("new render did not use a safe fence: err=%v", err)
	}
	if _, err := ParseStatementSamplesV1(strings.Replace(legacy, "\n```text\n```\n```\n", "\n```text\n`a`\n```\n", 1)); err == nil {
		t.Fatal("legacy compatibility bypassed the actual input digest")
	}
}

func TestStatementSamplesV1PreservesOrdinaryIntegerBytesAndHashes(t *testing.T) {
	const expected = "<!-- ALGOFORGE_SAMPLES_V1_BEGIN count=1 -->\n### Samples\n" +
		"#### input1 bytes=4 sha256=ffdb2b471864a2f3bbb7753ece957f6e8f0915538d88a495ac6f28d2583c44f1\n```text\n1\n5\n```\n" +
		"#### output1 bytes=2 sha256=b7a56873cd771f2c446d369b649430b65a756ba278ff97ec81bb6f55b2e73569\n```text\n25\n```\n" +
		"<!-- ALGOFORGE_SAMPLES_V1_END -->"
	actual, err := RenderStatementSamplesV1(StatementSamplesPlaceholderV1, []StatementSampleV1{{Input: "1\n5\n", Output: "25"}})
	if err != nil || actual != expected {
		t.Fatalf("ordinary integer statement bytes changed: got=%q err=%v", actual, err)
	}
}
