package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

// ResumeReviewedSlot uses the existing human decision and restores one result;
// it never starts a model call or a new generation run.
func (s *ProblemSetGenerationService) ResumeReviewedSlot(ctx context.Context, id uuid.UUID, position int) (*domain.ProblemSetGenerationState, error) {
	if s == nil || s.sets == nil || s.repo == nil || s.sets.repo == nil || s.sets.problems == nil {
		return nil, fmt.Errorf("problem-set generation is unavailable")
	}
	if principal, ok := access.FromContext(ctx); ok && !principal.IsAdmin() {
		return nil, ErrNotFound
	}
	if err := s.sets.requireSetAccess(ctx, id, true); err != nil {
		return nil, err
	}
	set, err := s.sets.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	state := set.Generation
	if state == nil {
		return nil, fmt.Errorf("%w: 此题集没有可恢复的生成记录", ErrConflict)
	}
	if state.Active() {
		return nil, repository.ErrProblemSetGenerationActive
	}
	if state.ConfigFingerprint != setConfigFingerprint(set) {
		return nil, fmt.Errorf("%w: 题集需求已变化，请刷新后重新安排此题", ErrConflict)
	}
	slot := state.FindSlot(position)
	if slot == nil || slot.Type != domain.QuizTypeProgramming || slot.ProblemID == nil || slot.Regenerate {
		return nil, fmt.Errorf("%w: 此题位没有可恢复的编程题候选", ErrConflict)
	}
	p, err := s.sets.problems.GetProblem(ctx, *slot.ProblemID)
	if err != nil {
		return nil, err
	}
	reader, ok := s.sets.problems.(manualReleaseApprovalReader)
	if !ok {
		return nil, fmt.Errorf("manual release approval service is unavailable")
	}
	approved, err := reader.HasCurrentManualReleaseApproval(ctx, p.ID, p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !approved || p.Status != domain.ProblemStatusPublished {
		return nil, fmt.Errorf("%w: 请先核对当前候选题并完成管理员人工审核", ErrConflict)
	}
	validated, err := s.validateSetSlotAssets(ctx, *slot)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(interface {
		CompleteReviewedGenerationSlot(context.Context, domain.ProblemSetGenerationRef, domain.ProblemSetGenerationSlot, time.Time) error
	})
	if !ok {
		return nil, fmt.Errorf("reviewed slot recovery is unavailable")
	}
	err = repo.CompleteReviewedGenerationSlot(ctx, domain.ProblemSetGenerationRef{SetID: id, RunID: state.ID}, validated, p.UpdatedAt)
	if err != nil {
		if errors.Is(err, repository.ErrProblemSetDuplicateContent) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, err)
		}
		return nil, err
	}
	set, err = s.sets.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return set.Generation, nil
}
