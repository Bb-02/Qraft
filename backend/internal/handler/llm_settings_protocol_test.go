package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestVerificationSettingsAutoAnthropicUsesRealProbeBeforeSaving(t *testing.T) {
	for _, suffix := range []string{"/anthropic/", "/anthropic/v1", "/anthropic/v1/messages"} {
		t.Run(suffix, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "/anthropic/v1/messages", r.URL.Path)
				require.Equal(t, "synthetic-settings-key", r.Header.Get("x-api-key"))
				var body struct {
					Model     string `json:"model"`
					MaxTokens int    `json:"max_tokens"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "deepseek-flash", body.Model)
				require.Equal(t, 1, body.MaxTokens)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"synthetic-probe","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer server.Close()
			store := &fakeLLMSettingsStore{}
			handler := NewLLMSettingsHandler(store, &fakeSettingsCipher{}, config.AnthropicConfig{APIKey: "synthetic-settings-key"})
			payload, err := json.Marshal(map[string]any{"model": "deepseek-flash", "base_url": server.URL + suffix, "use_environment_key": true})
			require.NoError(t, err)
			result := invokeLLMSettingsUpdateBody(t, handler, repository.LLMProviderPurposeVerification, string(payload))
			require.Equal(t, http.StatusOK, result.Code, result.Body.String())
			require.Equal(t, 1, calls)
			require.Equal(t, 1, store.upsertCalls)
			record := store.records[repository.LLMProviderPurposeVerification]
			require.Equal(t, "anthropic-messages", record.Protocol)
			require.Equal(t, strings.TrimRight(server.URL+suffix, "/"), record.BaseURL)
			require.Equal(t, "llm-endpoint:"+record.BaseURL, record.Provider)
			require.NotContains(t, result.Body.String(), "synthetic-settings-key")
		})
	}
}
