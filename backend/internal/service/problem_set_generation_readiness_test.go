package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func readinessProvidersFixture() *domain.ProviderRuntimeConfig {
	g := &domain.LLMRuntimeConfig{Model: "synthetic-generator", BaseURL: "https://generator.invalid/private-path", Provider: "fixture", APIKey: "synthetic-generator-secret"}
	v := &domain.LLMRuntimeConfig{Model: "synthetic-verifier", BaseURL: "https://verifier.invalid/private-path", Provider: "fixture", APIKey: "synthetic-verifier-secret"}
	return &domain.ProviderRuntimeConfig{Statement: g, Verification: v, Review: v}
}

func readyPollersFixture() *workflowservice.DescribeTaskQueueResponse {
	return &workflowservice.DescribeTaskQueueResponse{Pollers: []*taskqueuepb.PollerInfo{{LastAccessTime: timestamppb.Now()}}}
}

func mockReadinessWorkers(c *mocks.Client, queue string, response *workflowservice.DescribeTaskQueueResponse, err error) {
	for _, typ := range []enumspb.TaskQueueType{enumspb.TASK_QUEUE_TYPE_WORKFLOW, enumspb.TASK_QUEUE_TYPE_ACTIVITY} {
		c.On("DescribeTaskQueue", mock.Anything, queue, typ).Return(response, err)
	}
}

func readinessServiceFixture(config *domain.ProviderRuntimeConfig) (*ProblemSetGenerationService, *mocks.Client, *int) {
	c := &mocks.Client{}
	paidResolverCalls := new(int)
	s := NewProblemSetGenerationService(nil, nil, nil, nil, c, "synthetic-readiness-queue", func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		(*paidResolverCalls)++
		return config, nil
	})
	s.SetGenerationReadiness(ProblemSetGenerationReadinessOptions{
		Providers: func(context.Context, bool) (*domain.ProviderRuntimeConfig, error) { return config, nil },
		Sandbox:   func(context.Context) (bool, error) { return true, nil },
	})
	return s, c, paidResolverCalls
}

func TestSetGenerationReadinessUsesActualInheritedRolesWithoutRegisteringKeys(t *testing.T) {
	g := readinessProvidersFixture().Statement
	keys := &setKeysFixture{}
	resolver := NewProblemSetReadinessProviderResolver(setSettingsFixture{g: g})
	config, err := resolver(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, g.Model, config.Verification.Model)
	require.Equal(t, g.BaseURL, config.Verification.BaseURL)
	require.Equal(t, "synthetic-generator-secret", config.Verification.APIKey)
	require.Empty(t, keys.values)
	svc, client, paidCalls := readinessServiceFixture(config)
	svc.readiness.Providers = resolver
	report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "programming", ProgrammingCount: 12})
	require.NoError(t, err)
	require.False(t, report.Ready)
	require.Len(t, report.Blockers, 2)
	require.Equal(t, "oracle_model_independence", report.Blockers[0].Code)
	require.Equal(t, "oracle_endpoint_independence", report.Blockers[1].Code)
	require.Contains(t, report.Blockers[0].Message, g.Model)
	require.Equal(t, generationModelSettingsURL, report.Blockers[0].SettingsURL)
	require.Equal(t, "configured_routes", report.Scope)
	require.Len(t, report.EffectiveRoles, 3)
	require.Zero(t, *paidCalls)
	client.AssertNotCalled(t, "DescribeTaskQueue", mock.Anything, mock.Anything, mock.Anything)
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	for _, private := range []string{g.APIKey, g.BaseURL, "private-path", "api_key", "api_key_ref", "endpoint_id", "provider"} {
		require.NotContains(t, string(encoded), private)
	}
}

func TestSetGenerationReadinessDetectsEveryConfiguredIndependenceConflict(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		edit       func(*domain.ProviderRuntimeConfig)
	}{
		{"same model different endpoints", "oracle_model_independence", func(c *domain.ProviderRuntimeConfig) { c.Verification.Model = strings.ToUpper(c.Statement.Model) }},
		{"different models same endpoint", "oracle_endpoint_independence", func(c *domain.ProviderRuntimeConfig) {
			c.Verification.BaseURL = "HTTPS://GENERATOR.INVALID/private-path/"
		}},
		{"provider and protocol do not distinguish deployments", "oracle_endpoint_independence", func(c *domain.ProviderRuntimeConfig) {
			c.Verification.BaseURL = c.Statement.BaseURL
			c.Verification.Provider, c.Verification.Protocol = "other-label", "openai-chat"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := readinessProvidersFixture()
			tc.edit(config)
			svc, client, calls := readinessServiceFixture(config)
			report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "programming", ProgrammingCount: 1})
			require.NoError(t, err)
			require.False(t, report.Ready)
			require.Equal(t, tc.code, report.Blockers[0].Code)
			require.Zero(t, *calls)
			client.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestSetGenerationReadinessWarningDoesNotBlockValidProgrammingConfiguration(t *testing.T) {
	svc, client, calls := readinessServiceFixture(readinessProvidersFixture())
	mockReadinessWorkers(client, svc.queue, readyPollersFixture(), nil)
	report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "mixed", ProgrammingCount: 1})
	require.NoError(t, err)
	require.True(t, report.Ready)
	require.Empty(t, report.Blockers)
	require.Len(t, report.Warnings, 2)
	require.Equal(t, "returned_model_identity", report.Warnings[0].Code)
	require.Equal(t, "similarity_dedup", report.Warnings[1].Code)
	require.Contains(t, report.Warnings[1].Message, "不会做相似题去重")
	for _, check := range report.Checks {
		require.Contains(t, []string{"ready", "warning"}, check.Status)
	}
	require.Zero(t, *calls)
	client.AssertExpectations(t)
}

type readinessSettingsReader struct{ verificationReads, reviewReads int }

func (*readinessSettingsReader) EffectiveRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, error) {
	return readinessProvidersFixture().Statement, nil
}
func (s *readinessSettingsReader) SavedRuntimeConfig(_ context.Context, role string) (*domain.LLMRuntimeConfig, bool, error) {
	if role == "review" {
		s.reviewReads++
		return nil, false, errors.New("synthetic programming reviewer is unavailable")
	}
	s.verificationReads++
	return nil, false, nil
}

func TestSetGenerationReadinessObjectiveOnlyDoesNotRequireOracleSandboxOrEmbedding(t *testing.T) {
	settings := &readinessSettingsReader{}
	svc, client, _ := readinessServiceFixture(readinessProvidersFixture())
	svc.readiness.Providers = NewProblemSetReadinessProviderResolver(settings)
	svc.readiness.Sandbox = func(context.Context) (bool, error) {
		t.Fatal("objective-only must not probe sandbox")
		return false, nil
	}
	mockReadinessWorkers(client, svc.queue, readyPollersFixture(), nil)
	report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "mixed", ProgrammingCount: 0})
	require.NoError(t, err)
	require.True(t, report.Ready)
	require.Empty(t, report.Warnings)
	require.Len(t, report.EffectiveRoles, 2)
	require.Equal(t, report.EffectiveRoles[0].Model, report.EffectiveRoles[1].Model)
	require.Equal(t, "客观题答案复核", report.EffectiveRoles[1].Label)
	require.Equal(t, 1, settings.verificationReads)
	require.Zero(t, settings.reviewReads)
	for _, check := range report.Checks {
		require.NotContains(t, check.Code, "oracle")
		require.NotEqual(t, "sandbox", check.Code)
		require.NotEqual(t, "similarity_dedup", check.Code)
	}
	client.AssertExpectations(t)
}

func TestObjectiveSetProviderResolutionMatchesReadinessAndSkipsUnusedReviewer(t *testing.T) {
	settings := &readinessSettingsReader{}
	keys := &setKeysFixture{}
	providers, err := NewProblemSetProviderResolverForMode(settings, keys)(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, settings.verificationReads)
	require.Zero(t, settings.reviewReads)
	require.Equal(t, providers.Statement.Model, providers.Verification.Model)
	require.Nil(t, providers.Review)
	require.NotEmpty(t, providers.Statement.APIKeyRef)
	require.NotEmpty(t, providers.Verification.APIKeyRef)
	require.Empty(t, providers.Statement.APIKey)
	require.Empty(t, providers.Verification.APIKey)
	require.Len(t, keys.values, 2)
}

func TestProgrammingSetProviderResolverRechecksCurrentSnapshotBeforeRegisteringKeys(t *testing.T) {
	for _, conflict := range []string{"model", "endpoint"} {
		t.Run(conflict, func(t *testing.T) {
			providers := readinessProvidersFixture()
			if conflict == "model" {
				providers.Verification.Model = providers.Statement.Model
			} else {
				providers.Verification.BaseURL = providers.Statement.BaseURL
			}
			keys := &setKeysFixture{}
			resolver := NewProblemSetProviderResolverForMode(setSettingsFixture{g: providers.Statement, v: providers.Verification, r: providers.Review}, keys)
			_, err := resolver(context.Background(), true)
			var notReady *ProblemSetGenerationNotReadyError
			require.ErrorAs(t, err, &notReady)
			require.False(t, notReady.Readiness.Ready)
			require.Zero(t, len(keys.values))
		})
	}
}

func TestSetGenerationStartRechecksChangedSettingsBeforeKeysAndReservation(t *testing.T) {
	svc, repo, client, _ := setRuntimeFixture("partial")
	repo.set.DesiredItemCount, repo.set.GenerationConfig = 4, mixedSetFixture().GenerationConfig
	svc.resolve = func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		t.Fatal("legacy resolver is not used")
		return nil, nil
	}
	svc.SetGenerationReadiness(ProblemSetGenerationReadinessOptions{
		Providers: func(context.Context, bool) (*domain.ProviderRuntimeConfig, error) {
			return readinessProvidersFixture(), nil
		},
		Sandbox: func(context.Context) (bool, error) { return true, nil },
	})
	mockReadinessWorkers(client, svc.queue, readyPollersFixture(), nil)
	changedG := readinessProvidersFixture().Statement
	keys := &setKeysFixture{}
	svc.SetGenerationProviderResolver(NewProblemSetProviderResolverForMode(setSettingsFixture{g: changedG}, keys))
	_, err := svc.Start(context.Background(), repo.set.ID)
	var notReady *ProblemSetGenerationNotReadyError
	require.ErrorAs(t, err, &notReady)
	require.Equal(t, "oracle_model_independence", notReady.Readiness.Blockers[0].Code)
	require.Empty(t, keys.values)
	require.Zero(t, repo.writes)
	client.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

type mustNotPlanReadinessFixture struct{ calls int }

func (f *mustNotPlanReadinessFixture) CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error) {
	f.calls++
	return nil, errors.New("planning must not call a model for known-correlated providers")
}

func TestSetPreparationRechecksActualProvidersBeforeAnyPlanningModelCall(t *testing.T) {
	for _, fromModeResolver := range []bool{false, true} {
		t.Run(fmt.Sprintf("mode resolver=%t", fromModeResolver), func(t *testing.T) {
			svc, repo, client, _ := setRuntimeFixture("queued")
			repo.set.DesiredItemCount, repo.set.GenerationConfig = 4, mixedSetFixture().GenerationConfig
			repo.set.Generation.Slots = []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming, Status: "pending"}}
			model := &mustNotPlanReadinessFixture{}
			svc.sets.llm = model
			g := readinessProvidersFixture().Statement
			svc.resolve = func(context.Context) (*domain.ProviderRuntimeConfig, error) {
				return &domain.ProviderRuntimeConfig{Statement: g, Verification: g, Review: g}, nil
			}
			keys := &setKeysFixture{}
			if fromModeResolver {
				svc.SetGenerationProviderResolver(NewProblemSetProviderResolverForMode(setSettingsFixture{g: g}, keys))
			}
			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(svc.PrepareProblemSetBatchActivity)
			_, err := env.ExecuteActivity(svc.PrepareProblemSetBatchActivity, domain.ProblemSetGenerationRef{SetID: repo.set.ID, RunID: repo.set.Generation.ID})
			var applicationError *temporal.ApplicationError
			require.ErrorAs(t, err, &applicationError)
			require.True(t, applicationError.NonRetryable())
			require.Equal(t, "SetGenerationPreflightError", applicationError.Type())
			require.Contains(t, applicationError.Message(), "独立验算需要与出题不同的模型")
			var report domain.ProblemSetGenerationReadiness
			require.NoError(t, applicationError.Details(&report))
			require.False(t, report.Ready)
			require.Len(t, report.EffectiveRoles, 3)
			require.Zero(t, model.calls)
			require.Empty(t, keys.values)
			require.Empty(t, repo.set.Generation.Brief)
			client.AssertNotCalled(t, "DescribeTaskQueue", mock.Anything, mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestSetGenerationReadinessRuntimeUnknownAndStalePollersPreventStarting(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		response      *workflowservice.DescribeTaskQueueResponse
		err           error
	}{
		{"unknown", "暂时无法确认", nil, errors.New("synthetic connection error with secret")},
		{"no pollers", "最近未观察到", &workflowservice.DescribeTaskQueueResponse{}, nil},
		{"stale poller", "最近未观察到", &workflowservice.DescribeTaskQueueResponse{Pollers: []*taskqueuepb.PollerInfo{{LastAccessTime: timestamppb.New(time.Now().Add(-3 * time.Minute))}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, client, _ := readinessServiceFixture(readinessProvidersFixture())
			mockReadinessWorkers(client, svc.queue, tc.response, tc.err)
			report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "programming", ProgrammingCount: 1})
			require.NoError(t, err)
			require.False(t, report.Ready)
			require.Len(t, report.Blockers, 2)
			require.Contains(t, report.Blockers[0].Message, tc.message)
			require.NotContains(t, report.Blockers[0].Message, "secret")
			client.AssertExpectations(t)
		})
	}
}

func TestSetGenerationReadinessSandboxFailureIsObservedWithoutLeakingDetails(t *testing.T) {
	for _, tc := range []struct {
		ready   bool
		err     error
		message string
	}{
		{false, errors.New("https://user:secret@private.example/config"), "暂时无法确认"},
		{false, nil, "报告尚未就绪"},
	} {
		svc, client, _ := readinessServiceFixture(readinessProvidersFixture())
		mockReadinessWorkers(client, svc.queue, readyPollersFixture(), nil)
		svc.readiness.Sandbox = func(context.Context) (bool, error) { return tc.ready, tc.err }
		report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "programming", ProgrammingCount: 1})
		require.NoError(t, err)
		require.False(t, report.Ready)
		require.Equal(t, "sandbox", report.Blockers[0].Code)
		require.Contains(t, report.Blockers[0].Message, tc.message)
		encoded, _ := json.Marshal(report)
		require.NotContains(t, string(encoded), "secret")
	}
}

func TestSetGenerationReadinessModelNamesAndSettingsFailuresAreSafe(t *testing.T) {
	for _, model := range []string{"https://user:secret@host.invalid/model", "sk-synthetic-secret", "runtime:synthetic-token", "env:PRIVATE_KEY", "synthetic-generator-secret", "a\nprivate"} {
		config := readinessProvidersFixture()
		config.Statement.Model = model
		require.Equal(t, "已配置模型（名称不公开）", safeReadinessModelName(config.Statement))
	}
	svc, _, _ := readinessServiceFixture(nil)
	svc.readiness.Providers = func(context.Context, bool) (*domain.ProviderRuntimeConfig, error) {
		return nil, errors.New("secret decrypted private config")
	}
	report, err := svc.GenerationReadiness(context.Background(), domain.ProblemSetGenerationReadinessRequest{Mode: "programming", ProgrammingCount: 1})
	require.NoError(t, err)
	require.False(t, report.Ready)
	encoded, _ := json.Marshal(report)
	require.NotContains(t, string(encoded), "decrypted")
	require.NotContains(t, string(encoded), "secret")
}

func TestSetGenerationStartReadinessBlocksBeforeReservationAndRuntimeKeyResolution(t *testing.T) {
	svc, repo, client, _ := setRuntimeFixture("partial")
	repo.set.DesiredItemCount, repo.set.GenerationConfig = 4, mixedSetFixture().GenerationConfig
	keysResolved := 0
	svc.resolve = func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		keysResolved++
		return nil, fmt.Errorf("must not resolve runtime keys")
	}
	g := readinessProvidersFixture().Statement
	svc.SetGenerationReadiness(ProblemSetGenerationReadinessOptions{Providers: NewProblemSetReadinessProviderResolver(setSettingsFixture{g: g})})
	old, _ := json.Marshal(repo.set)
	_, err := svc.Start(context.Background(), repo.set.ID)
	var notReady *ProblemSetGenerationNotReadyError
	require.ErrorAs(t, err, &notReady)
	require.Equal(t, "oracle_model_independence", notReady.Readiness.Blockers[0].Code)
	require.Zero(t, keysResolved)
	require.Zero(t, repo.writes)
	after, _ := json.Marshal(repo.set)
	require.JSONEq(t, string(old), string(after))
	client.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestSetGenerationStartChecksOwnerBeforeReadingSharedConfiguration(t *testing.T) {
	svc, repo, _, _ := setRuntimeFixture("partial")
	repo.set.OwnerUserID = uuid.NewString()
	svc.sets.permissions = &WorkflowAccess{}
	svc.readiness = &ProblemSetGenerationReadinessOptions{Providers: func(context.Context, bool) (*domain.ProviderRuntimeConfig, error) {
		t.Fatal("unauthorized user must not read configuration")
		return nil, nil
	}}
	ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: uuid.NewString(), Role: "member"})
	_, err := svc.Start(ctx, repo.set.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.Zero(t, repo.writes)
}

func TestSandboxReadinessHealthProbeOnlyGetsServerHealthAndRequiresCPPCapability(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		want       bool
		wantError  bool
	}{
		{"ready", `{"ready":true,"nsjail_ready":true,"cgroup_v2_ready":true,"languages":{"cpp":{"ready":true}}}`, 200, true, false},
		{"missing cpp", `{"ready":true,"nsjail_ready":true,"cgroup_v2_ready":true,"languages":{}}`, 200, false, true},
		{"not ready", `{"ready":false,"nsjail_ready":true,"cgroup_v2_ready":false,"languages":{"cpp":{"ready":true}},"error":"private sandbox diagnostics"}`, 503, false, false},
		{"bad response", `{"error":"synthetic private diagnostic"}`, 500, false, true},
		{"malformed", `not json`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/health", r.URL.Path)
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			probe := NewProblemSetSandboxReadinessProbe(server.URL)
			require.NotNil(t, probe)
			ready, err := probe(context.Background())
			require.Equal(t, tc.want, ready)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	for _, invalid := range []string{"", "file:///tmp/health", "https://user:secret@server.invalid", "https://server.invalid/?token=secret"} {
		require.Nil(t, NewProblemSetSandboxReadinessProbe(invalid))
	}
}
