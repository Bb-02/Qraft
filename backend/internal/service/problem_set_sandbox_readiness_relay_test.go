package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const syntheticSandboxReadyHealth = `{"status":"ok","ready":true,"nsjail_ready":true,"cgroup_v2_ready":true,"languages":{"cpp":{"ready":true}},"sandbox_revision":"synthetic-only"}`
const syntheticSandboxUnreadyHealth = `{"status":"unavailable","ready":false,"nsjail_ready":true,"cgroup_v2_ready":false,"languages":{"cpp":{"ready":true}},"error":"synthetic private sandbox diagnostic"}`

func TestSandboxReadinessRealHTTPChainPreservesKnownAndUnknown(t *testing.T) {
	for _, test := range []struct {
		name, body              string
		status                  int
		known, ready, wantError bool
	}{
		{"ready", syntheticSandboxReadyHealth, 200, true, true, false},
		{"reported unready", syntheticSandboxUnreadyHealth, 503, true, false, false},
		{"unexpected upstream status", syntheticSandboxReadyHealth, 500, false, false, true},
		{"missing health capabilities", `{"ready":true}`, 200, false, false, true},
		{"empty object", `{}`, 200, false, false, true},
		{"invalid JSON", `invalid synthetic health response`, 200, false, false, true},
		{"trailing JSON", syntheticSandboxReadyHealth + ` {"ready":true}`, 200, false, false, true},
		{"null readiness", `{"ready":null,"nsjail_ready":true,"cgroup_v2_ready":true,"languages":{"cpp":{"ready":true}}}`, 200, false, false, true},
		{"inconsistent status", syntheticSandboxReadyHealth, 503, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/health", r.URL.Path)
				require.Empty(t, r.URL.RawQuery)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			worker := httptest.NewServer(NewWorkerSandboxReadinessHealthHandler(nil, upstream.URL))
			defer worker.Close()
			response, err := http.Get(worker.URL + WorkerSandboxReadinessPath)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			var observation map[string]bool
			require.NoError(t, json.Unmarshal(body, &observation))
			require.Equal(t, map[string]bool{"known": test.known, "ready": test.ready}, observation)
			require.NotContains(t, string(body), "diagnostic")
			require.NotContains(t, string(body), "sandbox_revision")
			require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
			if test.ready {
				require.Equal(t, 200, response.StatusCode)
			} else {
				require.Equal(t, 503, response.StatusCode)
			}
			probe := NewProblemSetSandboxReadinessProbeForAPI(worker.URL+WorkerSandboxReadinessPath, "http://unreachable-direct.invalid")
			require.NotNil(t, probe)
			ready, err := probe(context.Background())
			require.Equal(t, test.ready, ready)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSandboxReadinessRelayDoesNotAddExecutionOrUserSelectedTargets(t *testing.T) {
	var sandboxRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sandboxRequests.Add(1)
		_, _ = io.WriteString(w, syntheticSandboxReadyHealth)
	}))
	defer upstream.Close()
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusTeapot)
			_, _ = io.WriteString(w, "existing worker health")
			return
		}
		http.NotFound(w, r)
	})
	worker := httptest.NewServer(NewWorkerSandboxReadinessHealthHandler(fallback, upstream.URL))
	defer worker.Close()
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, WorkerSandboxReadinessPath, 405},
		{http.MethodPut, WorkerSandboxReadinessPath, 405},
		{http.MethodGet, WorkerSandboxReadinessPath + "?target=http://forbidden.invalid/execute", 400},
		{http.MethodGet, WorkerSandboxReadinessPath + "?", 400},
		{http.MethodGet, WorkerSandboxReadinessPath + "/execute", 404},
	} {
		request, err := http.NewRequest(test.method, worker.URL+test.path, strings.NewReader(`{"url":"http://forbidden.invalid/execute","source_code":"synthetic"}`))
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, test.status, response.StatusCode)
	}
	require.Zero(t, sandboxRequests.Load(), "invalid requests must never contact any sandbox endpoint")
	response, err := http.Get(worker.URL + "/readyz")
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, response.StatusCode)
	require.Equal(t, "existing worker health", string(body))
}

func TestSandboxReadinessRelayUnknownWhenSandboxUnavailableOrNotConfigured(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	for _, sandboxURL := range []string{url, "", "https://user:synthetic-secret@invalid.example"} {
		worker := httptest.NewServer(NewWorkerSandboxReadinessHealthHandler(nil, sandboxURL))
		probe := NewWorkerSandboxReadinessProbe(worker.URL + WorkerSandboxReadinessPath)
		ready, err := probe(context.Background())
		require.False(t, ready)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "synthetic-secret")
		worker.Close()
	}
}

func TestSandboxReadinessRelayRejectsRedirectsOnBothNetworkHops(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, syntheticSandboxReadyHealth)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	worker := httptest.NewServer(NewWorkerSandboxReadinessHealthHandler(nil, redirect.URL))
	defer worker.Close()
	for _, relayURL := range []string{worker.URL + WorkerSandboxReadinessPath, redirect.URL + WorkerSandboxReadinessPath} {
		probe := NewWorkerSandboxReadinessProbe(relayURL)
		ready, err := probe(context.Background())
		require.False(t, ready)
		require.Error(t, err)
	}
	require.Zero(t, redirected.Load())
}

func TestSandboxReadinessRelayStrictResponseNeverPromotesUnknownToReady(t *testing.T) {
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"known":false,"ready":true}`, 200},
		{`{"known":false,"ready":false}`, 503},
		{`{"known":true,"ready":true}`, 503},
		{`{"known":true,"ready":false}`, 200},
		{`{"known":true}`, 200},
		{`{"known":null,"ready":true}`, 200},
		{`{"known":true,"ready":"true"}`, 200},
		{`{"known":true,"ready":true,"private":"synthetic"}`, 200},
		{`{"known":true,"ready":true} {"known":true,"ready":true}`, 200},
		{`invalid`, 200},
		{`{"known":true,"ready":true}`, 500},
		{strings.Repeat(" ", 4<<10) + `{"known":true,"ready":true}`, 200},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(test.status)
			_, _ = io.WriteString(w, test.body)
		}))
		probe := NewWorkerSandboxReadinessProbe(server.URL + WorkerSandboxReadinessPath)
		ready, err := probe(context.Background())
		require.False(t, ready, test.body)
		require.Error(t, err)
		server.Close()
	}
}

func TestSandboxReadinessAPIUsesDirectOnlyWhenRelayIsNotConfigured(t *testing.T) {
	var directRequests atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directRequests.Add(1)
		_, _ = io.WriteString(w, syntheticSandboxReadyHealth)
	}))
	defer direct.Close()
	probe := NewProblemSetSandboxReadinessProbeForAPI("", direct.URL)
	require.NotNil(t, probe)
	ready, err := probe(context.Background())
	require.True(t, ready)
	require.NoError(t, err)
	require.Equal(t, int32(1), directRequests.Load())
	worker := httptest.NewServer(http.NotFoundHandler())
	workerURL := worker.URL
	worker.Close()
	probe = NewProblemSetSandboxReadinessProbeForAPI(workerURL+WorkerSandboxReadinessPath, direct.URL)
	ready, err = probe(context.Background())
	require.False(t, ready)
	require.Error(t, err)
	require.Equal(t, int32(1), directRequests.Load(), "a failed configured relay must not fall back to direct sandbox access")
	for _, invalid := range []string{"file:///generation/sandbox-readiness", "https://user:secret@worker.invalid" + WorkerSandboxReadinessPath, "http://worker.invalid/readyz", "http://worker.invalid" + WorkerSandboxReadinessPath + "?target=synthetic", "http://worker.invalid" + WorkerSandboxReadinessPath + "?", "http://worker.invalid" + WorkerSandboxReadinessPath + "#fragment"} {
		require.Nil(t, NewProblemSetSandboxReadinessProbeForAPI(invalid, direct.URL))
	}
	require.Equal(t, int32(1), directRequests.Load())
}
