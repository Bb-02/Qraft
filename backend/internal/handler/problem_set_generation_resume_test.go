package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type reviewedSlotController struct {
	problemSetGenerationController
	calls    int
	id       uuid.UUID
	position int
}

func (s *reviewedSlotController) ResumeReviewedSlot(_ context.Context, id uuid.UUID, position int) (*domain.ProblemSetGenerationState, error) {
	s.calls++
	s.id = id
	s.position = position
	return &domain.ProblemSetGenerationState{Status: "completed"}, nil
}
func TestProblemSetReviewedSlotResumeHTTPRequiresAdminAndValidPosition(t *testing.T) {
	for _, tc := range []struct {
		role, position string
		want           int
	}{{"admin", "3", http.StatusOK}, {"member", "3", http.StatusForbidden}, {"admin", "0", http.StatusBadRequest}, {"admin", "1001", http.StatusBadRequest}, {"admin", "wrong", http.StatusBadRequest}} {
		t.Run(tc.role+tc.position, func(t *testing.T) {
			controller := &reviewedSlotController{}
			h := NewProblemSetHandler(nil)
			h.SetGenerationService(controller)
			e := echo.New()
			e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					c.Set("user", &authmw.JWTClaims{UserID: "synthetic-user", Role: tc.role})
					return next(c)
				}
			})
			RegisterProblemSetRoutes(e.Group("/api/v1"), h)
			id := uuid.New()
			res := httptest.NewRecorder()
			e.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/problem-sets/"+id.String()+"/generation/slots/"+tc.position+"/resume", nil))
			require.Equal(t, tc.want, res.Code, res.Body.String())
			if tc.want == http.StatusOK {
				require.Equal(t, 1, controller.calls)
				require.Equal(t, id, controller.id)
				require.Equal(t, 3, controller.position)
			} else {
				require.Zero(t, controller.calls)
			}
		})
	}
}
