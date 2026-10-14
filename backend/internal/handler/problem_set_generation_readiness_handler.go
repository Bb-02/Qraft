package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
)

type problemSetGenerationReadinessController interface {
	GenerationReadiness(context.Context, domain.ProblemSetGenerationReadinessRequest) (*domain.ProblemSetGenerationReadiness, error)
	RequireGenerationReadiness(context.Context, *domain.ProblemSetGenerationConfig, int) (*domain.ProblemSetGenerationReadiness, error)
}

func (h *ProblemSetHandler) HandleGenerationReadiness(c echo.Context) error {
	request := domain.ProblemSetGenerationReadinessRequest{Mode: strings.TrimSpace(c.QueryParam("mode")), ProgrammingCount: 1}
	if request.Mode == "" {
		request.Mode = "programming"
	}
	rawCount := strings.TrimSpace(c.QueryParam("programming_count"))
	if rawCount == "" && request.Mode == "mixed" {
		return badRequest(c, "INVALID_PARAMS", "混合题型需要明确编程题数量 programming_count")
	}
	if rawCount != "" {
		count, err := strconv.Atoi(rawCount)
		if err != nil {
			return badRequest(c, "INVALID_PARAMS", "编程题数量必须为 0-1000 的整数")
		}
		request.ProgrammingCount = count
	}
	if err := request.Validate(); err != nil {
		return badRequest(c, "INVALID_PARAMS", err.Error())
	}
	controller, available := h.generation.(problemSetGenerationReadinessController)
	if !available {
		return ok(c, &domain.ProblemSetGenerationReadiness{Ready: false, Scope: "configured_routes",
			Blockers: []domain.GenerationReadinessNotice{{Code: "generation_available", Message: "题集自动生成未启用，请联系管理员启用后重新检查。"}},
			Warnings: []domain.GenerationReadinessNotice{}, EffectiveRoles: []domain.GenerationReadinessRole{},
			Checks: []domain.GenerationReadinessCheck{{Code: "generation_available", Status: "blocked", Message: "题集自动生成未启用，请联系管理员启用后重新检查。"}}})
	}
	result, err := controller.GenerationReadiness(c.Request().Context(), request)
	if err != nil {
		return handleProblemSetError(c, err, "检查题集生成条件失败")
	}
	return ok(c, result)
}

func (h *ProblemSetHandler) requireCreateGenerationReadiness(ctx context.Context, request service.ProblemSetCreateRequest) error {
	controller, available := h.generation.(problemSetGenerationReadinessController)
	if !available {
		return &service.ProblemSetGenerationNotReadyError{Readiness: &domain.ProblemSetGenerationReadiness{Ready: false, Scope: "configured_routes", Blockers: []domain.GenerationReadinessNotice{{Code: "generation_available", Message: "题集生成条件检查尚未就绪，请联系管理员恢复服务。"}}}}
	}
	_, err := controller.RequireGenerationReadiness(ctx, request.GenerationConfig, request.DesiredItemCount)
	return err
}

func generationNotReadyResponse(c echo.Context, err *service.ProblemSetGenerationNotReadyError) error {
	return c.JSON(http.StatusConflict, APIResponse{Success: false, Data: err.Readiness, Error: &APIError{Code: "GENERATION_NOT_READY", Message: err.Error()}})
}
