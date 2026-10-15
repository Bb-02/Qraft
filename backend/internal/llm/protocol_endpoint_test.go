package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAutoProtocolUsesRecognizableEndpointBeforeModel(t *testing.T) {
	for _, tc := range []struct {
		baseURL, model string
		want           Protocol
	}{
		{"https://api.deepseek.com/anthropic", "deepseek-flash", ProtocolAnthropic},
		{"https://gateway.invalid/vendor/anthropic/", "deepseek-flash", ProtocolAnthropic},
		{"https://gateway.invalid/anthropic/v1", "deepseek-flash", ProtocolAnthropic},
		{"https://gateway.invalid/anthropic/v1/messages", "deepseek-flash", ProtocolAnthropic},
		{"https://gateway.invalid/anthropic", "gpt-synthetic-model", ProtocolAnthropic},
		{"https://gateway.invalid/v1/chat/completions", "claude-synthetic-model", ProtocolOpenAIChat},
		{"https://gateway.invalid/v1/responses", "deepseek-flash", ProtocolOpenAIResponses},
	} {
		t.Run(tc.baseURL+"/"+tc.model, func(t *testing.T) {
			protocol, err := ResolveProtocol(&RuntimeConfig{BaseURL: tc.baseURL, Provider: "fixture", Protocol: "auto"}, tc.model)
			require.NoError(t, err)
			require.Equal(t, tc.want, protocol)
		})
	}
	// Explicit saved protocols must remain authoritative, including old jobs.
	protocol, err := ResolveProtocol(&RuntimeConfig{BaseURL: "https://gateway.invalid/anthropic", Protocol: "openai-chat"}, "deepseek-flash")
	require.NoError(t, err)
	require.Equal(t, ProtocolOpenAIChat, protocol)
}

func TestEndpointProtocolDoesNotGuessFromHostQueryOrPartialPath(t *testing.T) {
	for _, baseURL := range []string{"", "not-a-url", "https://anthropic.gateway.invalid/v1", "https://gateway.invalid/v1?next=/anthropic", "https://gateway.invalid/not-anthropic", "https://gateway.invalid/anthropic-proxy", "https://gateway.invalid/v1", "ftp://gateway.invalid/anthropic"} {
		_, known := ProtocolFromBaseURL(baseURL)
		require.False(t, known, baseURL)
	}
}

func TestAutoAnthropicEndpointUsesMessagesWithNonClaudeModel(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/anthropic/v1/messages", r.URL.Path)
		require.Equal(t, "synthetic-probe-key", r.Header.Get("x-api-key"))
		var payload Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "deepseek-flash", payload.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"synthetic-message","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	client := NewClient(config.AnthropicConfig{APIKey: "synthetic-probe-key"})
	response, err := client.Complete(context.Background(), &Request{
		Model: "deepseek-flash", MaxTokens: 1, Messages: []Message{{Role: "user", Content: "Reply OK"}},
		Runtime: &RuntimeConfig{BaseURL: server.URL + "/anthropic", Provider: "fixture", Protocol: "auto"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, string(ProtocolAnthropic), response.Protocol)
	require.Equal(t, "deepseek-flash", response.Model)
	require.True(t, response.ModelObserved)
}
