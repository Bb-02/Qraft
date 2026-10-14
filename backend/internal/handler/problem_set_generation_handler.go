package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type problemSetGenerationController interface {
	Start(context.Context, uuid.UUID) (*domain.ProblemSetGenerationState, error)
	Status(context.Context, uuid.UUID) (*domain.ProblemSetGenerationState, error)
	Cancel(context.Context, uuid.UUID) error
}

func (h *ProblemSetHandler) SetGenerationService(g problemSetGenerationController) { h.generation = g }
func (h *ProblemSetHandler) HandleStartGeneration(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	state, err := h.generation.Start(c.Request().Context(), id)
	if err != nil {
		return handleProblemSetError(c, err, "failed to start set generation")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: state})
}
func (h *ProblemSetHandler) HandleGenerationStatus(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	state, err := h.generation.Status(c.Request().Context(), id)
	if err != nil {
		return handleProblemSetError(c, err, "failed to read set generation")
	}
	return ok(c, state)
}
func (h *ProblemSetHandler) HandleCancelGeneration(c echo.Context) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	if err = h.generation.Cancel(c.Request().Context(), id); err != nil {
		return handleProblemSetError(c, err, "failed to stop set generation")
	}
	return c.JSON(http.StatusAccepted, APIResponse{Success: true, Data: map[string]bool{"stop_requested": true}})
}

func (h *ProblemSetHandler) HandleResumeReviewedSlot(c echo.Context) error {
	if _, allowed := publicReleaseApprover(c); !allowed {
		return forbidden(c, "administrator role is required")
	}
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	position, err := strconv.Atoi(c.Param("position"))
	if err != nil || position < 1 || position > 1000 {
		return badRequest(c, "INVALID_PARAMS", "题位必须为 1-1000")
	}
	controller, available := h.generation.(interface {
		ResumeReviewedSlot(context.Context, uuid.UUID, int) (*domain.ProblemSetGenerationState, error)
	})
	if !available {
		return serviceUnavailable(c, "审核后恢复题位暂不可用")
	}
	state, err := controller.ResumeReviewedSlot(c.Request().Context(), id, position)
	if err != nil {
		return handleProblemSetError(c, err, "恢复已审核候选题失败")
	}
	return ok(c, state)
}
