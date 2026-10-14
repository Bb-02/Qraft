package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type readinessControllerFixture struct {
	requests    []domain.ProblemSetGenerationReadinessRequest
	configCalls int
	report      *domain.ProblemSetGenerationReadiness
}

func (*readinessControllerFixture) Start(context.Context, uuid.UUID) (*domain.ProblemSetGenerationState, error) {
	return nil, nil
}
func (*readinessControllerFixture) Status(context.Context, uuid.UUID) (*domain.ProblemSetGenerationState, error) {
	return nil, nil
}
func (*readinessControllerFixture) Cancel(context.Context, uuid.UUID) error { return nil }
func (f *readinessControllerFixture) GenerationReadiness(_ context.Context, request domain.ProblemSetGenerationReadinessRequest) (*domain.ProblemSetGenerationReadiness, error) {
	f.requests = append(f.requests, request)
	return f.report, nil
}
func (f *readinessControllerFixture) RequireGenerationReadiness(context.Context, *domain.ProblemSetGenerationConfig, int) (*domain.ProblemSetGenerationReadiness, error) {
	f.configCalls++
	if !f.report.Ready {
		return f.report, &service.ProblemSetGenerationNotReadyError{Readiness: f.report}
	}
	return f.report, nil
}

func handlerReadinessReportFixture() *domain.ProblemSetGenerationReadiness {
	return &domain.ProblemSetGenerationReadiness{Ready: false, Scope: "configured_routes",
		Blockers:       []domain.GenerationReadinessNotice{{Code: "oracle_model_independence", Message: "出题与验算不能使用同一个模型", SettingsURL: "/settings?section=verification"}},
		Warnings:       []domain.GenerationReadinessNotice{{Code: "similarity_dedup", Message: "未执行相似题去重"}},
		Checks:         []domain.GenerationReadinessCheck{{Code: "oracle_model_independence", Status: "blocked", Message: "出题与验算不能使用同一个模型"}},
		EffectiveRoles: []domain.GenerationReadinessRole{{Role: "generation", Label: "出题与参考程序", Model: "synthetic-model", Configured: true}}}
}

func TestProblemSetReadinessStaticRouteAndMixedObjectiveCount(t *testing.T) {
	fixture := &readinessControllerFixture{report: handlerReadinessReportFixture()}
	h := NewProblemSetHandler(nil)
	h.SetGenerationService(fixture)
	e := echo.New()
	RegisterProblemSetRoutes(e.Group("/api/v1"), h)
	for _, tc := range []struct {
		query    string
		wantCode int
		mode     string
		count    int
	}{
		{"", 200, "programming", 1},
		{"?mode=programming&programming_count=12", 200, "programming", 12},
		{"?mode=mixed&programming_count=0", 200, "mixed", 0},
		{"?mode=mixed", 400, "", 0},
		{"?mode=mixed&programming_count=oops", 400, "", 0},
		{"?mode=programming&programming_count=0", 400, "", 0},
		{"?mode=programming&programming_count=1001", 400, "", 0},
		{"?mode=unknown", 400, "", 0},
	} {
		out := httptest.NewRecorder()
		e.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/problem-sets/generation-readiness"+tc.query, nil))
		require.Equal(t, tc.wantCode, out.Code, out.Body.String())
		if tc.wantCode == 200 {
			require.Equal(t, domain.ProblemSetGenerationReadinessRequest{Mode: tc.mode, ProgrammingCount: tc.count}, fixture.requests[len(fixture.requests)-1])
			var response struct {
				Success bool                                 `json:"success"`
				Data    domain.ProblemSetGenerationReadiness `json:"data"`
			}
			require.NoError(t, json.Unmarshal(out.Body.Bytes(), &response))
			require.True(t, response.Success)
			require.False(t, response.Data.Ready)
			require.Equal(t, "blocked", response.Data.Checks[0].Status)
		}
	}
	require.Zero(t, fixture.configCalls)
}

func TestProblemSetCreateAndStartPreflightBlocksBeforeAnySetCreation(t *testing.T) {
	fixture := &readinessControllerFixture{report: handlerReadinessReportFixture()}
	// A nil set service would panic if the handler attempted even one write.
	h := NewProblemSetHandler(nil)
	h.SetGenerationService(fixture)
	e := echo.New()
	RegisterProblemSetRoutes(e.Group("/api/v1"), h)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/problem-sets", strings.NewReader(`{"title":"Synthetic preflight","desired_item_count":1,"start_generation":true,"generation_config":{"mode":"programming","distribution":[{"type":"programming","count":1,"score":100}]}}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	out := httptest.NewRecorder()
	e.ServeHTTP(out, request)
	require.Equal(t, http.StatusConflict, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), "GENERATION_NOT_READY")
	require.Contains(t, out.Body.String(), "oracle_model_independence")
	require.Equal(t, 1, fixture.configCalls)
}

func TestProblemSetReadinessFeatureDisabledStillReturnsUsefulReport(t *testing.T) {
	e := echo.New()
	RegisterProblemSetRoutes(e.Group("/api/v1"), NewProblemSetHandler(nil))
	out := httptest.NewRecorder()
	e.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/problem-sets/generation-readiness", nil))
	require.Equal(t, http.StatusOK, out.Code)
	require.Contains(t, out.Body.String(), `"ready":false`)
	require.Contains(t, out.Body.String(), `"warnings":[]`)
	require.Contains(t, out.Body.String(), `"generation_available"`)
}

func TestProblemSetReadinessRequiresMemberAndIsNotAnAnonymousCapability(t *testing.T) {
	permission, known := authmw.PermissionForRoute(http.MethodGet, "/api/v1/problem-sets/generation-readiness")
	require.True(t, known)
	require.Equal(t, authmw.PermissionMember, permission)
	e := echo.New()
	e.Use(authmw.RouteAuthorization())
	RegisterProblemSetRoutes(e.Group("/api/v1"), NewProblemSetHandler(nil))
	out := httptest.NewRecorder()
	e.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/problem-sets/generation-readiness", nil))
	require.Equal(t, http.StatusUnauthorized, out.Code)
}
