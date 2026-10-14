package llm

import (
	"net/url"
	"strings"
)

// NormalizeEndpointIdentity keeps the exact deployment identity used by model
// call receipts. Credentials and query strings never identify a deployment.
func NormalizeEndpointIdentity(baseURL, fallbackBaseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = strings.TrimSpace(fallbackBaseURL)
	}
	if raw == "" {
		return "https://api.anthropic.com/v1/messages"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}
