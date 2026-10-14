package llm

import "testing"

func TestNormalizeEndpointIdentityMatchesHistoricalReceiptRules(t *testing.T) {
	for _, test := range []struct{ configured, fallback, want string }{
		{"", "", "https://api.anthropic.com/v1/messages"},
		{"", " https://Fallback.Invalid/v1/ ", "https://fallback.invalid/v1"},
		{"HTTPS://User:private@GENERATOR.INVALID/v1///?token=private#private", "", "https://generator.invalid/v1"},
		{"https://generator.invalid/v1/messages", "", "https://generator.invalid/v1/messages"},
		{"not-a-url", "https://fallback.invalid", "not-a-url"},
	} {
		if got := NormalizeEndpointIdentity(test.configured, test.fallback); got != test.want {
			t.Fatalf("normalization = %q, want %q", got, test.want)
		}
	}
}
