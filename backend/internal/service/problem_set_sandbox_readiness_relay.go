package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const WorkerSandboxReadinessPath = "/generation/sandbox-readiness"

type workerSandboxReadiness struct {
	Known bool `json:"known"`
	Ready bool `json:"ready"`
}

// NewWorkerSandboxReadinessHealthHandler adds only a fixed, read-only observation
// to the worker's existing internal health listener. The sandbox target belongs
// to process configuration; no request can select a URL or execute code.
func NewWorkerSandboxReadinessHealthHandler(existing http.Handler, sandboxBaseURL string) http.Handler {
	probe := NewProblemSetSandboxReadinessProbe(sandboxBaseURL)
	if existing == nil {
		existing = http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	mux.Handle(WorkerSandboxReadinessPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		result := workerSandboxReadiness{}
		status := http.StatusServiceUnavailable
		switch {
		case r.Method != http.MethodGet:
			w.Header().Set("Allow", http.MethodGet)
			status = http.StatusMethodNotAllowed
		case r.URL.RawQuery != "" || r.URL.ForceQuery:
			status = http.StatusBadRequest
		case probe != nil:
			probeCtx, cancel := context.WithTimeout(r.Context(), generationReadinessProbeTimeout)
			ready, err := probe(probeCtx)
			cancel()
			if err == nil {
				result.Known, result.Ready = true, ready
				if ready {
					status = http.StatusOK
				}
			}
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	}))
	mux.Handle("/", existing)
	return mux
}

// A configured relay is authoritative. Failure never falls back across the
// execution-network boundary or turns an unknown observation into readiness.
func NewProblemSetSandboxReadinessProbeForAPI(workerReadinessURL, sandboxBaseURL string) func(context.Context) (bool, error) {
	if strings.TrimSpace(workerReadinessURL) == "" {
		return NewProblemSetSandboxReadinessProbe(sandboxBaseURL)
	}
	return NewWorkerSandboxReadinessProbe(workerReadinessURL)
}

func NewWorkerSandboxReadinessProbe(workerReadinessURL string) func(context.Context) (bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(workerReadinessURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.EscapedPath() != WorkerSandboxReadinessPath {
		return nil
	}
	httpClient := &http.Client{Timeout: generationReadinessProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context) (bool, error) {
		unknown := func() (bool, error) { return false, fmt.Errorf("sandbox readiness observation unavailable") }
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return unknown()
		}
		response, err := httpClient.Do(request)
		if err != nil {
			return unknown()
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable {
			return unknown()
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<10)+1))
		if err != nil || len(raw) > 4<<10 {
			return unknown()
		}
		var observation struct {
			Known *bool `json:"known"`
			Ready *bool `json:"ready"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&observation) != nil || decoder.Decode(new(any)) != io.EOF || observation.Known == nil || observation.Ready == nil || !*observation.Known {
			return unknown()
		}
		if response.StatusCode == http.StatusOK && *observation.Ready {
			return true, nil
		}
		if response.StatusCode == http.StatusServiceUnavailable && !*observation.Ready {
			return false, nil
		}
		return unknown()
	}
}
