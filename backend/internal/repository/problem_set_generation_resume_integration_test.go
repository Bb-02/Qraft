//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProblemSetReviewedSlotAtomicRecoveryIntegration(t *testing.T) {
	pool, problems, p := manualReleaseFixture(t)
	ctx := context.Background()
	sets := repository.NewProblemSetRepository(pool)
	set := &domain.ProblemSet{ID: uuid.New(), Code: "REVIEW-" + uuid.NewString(), Title: "Synthetic review recovery", Kind: domain.ProblemSetKindContest, Visibility: domain.ProblemSetVisibilityPrivate, Status: domain.ProblemSetStatusDraft, DesiredItemCount: 2,
		GenerationConfig: &domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 2, Score: 100}}}}
	require.NoError(t, set.NormalizeProblemSet())
	require.NoError(t, sets.Create(ctx, set))
	set, err := sets.GetByID(ctx, set.ID)
	require.NoError(t, err)
	slots := []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming, Score: 100, ChildID: "synthetic-candidate", ProblemID: &p.ID, Status: "awaiting_review", Failure: &domain.GenerationFailure{Code: "quality_not_met", Message: "original finding"}}, {Position: 2, Type: domain.QuizTypeProgramming, Score: 100, ChildID: "unrelated-child", Status: "failed", Error: "Unrelated failure"}}
	state, err := sets.ReserveGeneration(ctx, set.ID, set.UpdatedAt, &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "partial", Slots: slots})
	require.NoError(t, err)
	ref := domain.ProblemSetGenerationRef{SetID: set.ID, RunID: state.ID}
	slots[0].Fingerprint = "synthetic-statement-fingerprint"
	require.ErrorIs(t, sets.CompleteReviewedGenerationSlot(ctx, ref, slots[0], p.UpdatedAt), repository.ErrProblemSetGenerationChanged, "unapproved candidate cannot enter a set")
	approveManualFixture(t, problems, p.ID)
	p, err = problems.GetByID(ctx, p.ID)
	require.NoError(t, err)
	approved, err := problems.HasCurrentManualReleaseApproval(ctx, p.ID, p.UpdatedAt)
	require.NoError(t, err)
	require.True(t, approved)
	approved, err = problems.HasCurrentManualReleaseApproval(ctx, p.ID, p.UpdatedAt.Add(-time.Second))
	require.NoError(t, err)
	require.False(t, approved)
	replaced := slots[0]
	otherID := uuid.New()
	replaced.ProblemID = &otherID
	require.ErrorIs(t, sets.CompleteReviewedGenerationSlot(ctx, ref, replaced, p.UpdatedAt), repository.ErrProblemSetGenerationChanged)
	require.ErrorIs(t, sets.CompleteReviewedGenerationSlot(ctx, domain.ProblemSetGenerationRef{SetID: set.ID, RunID: "old-run"}, slots[0], p.UpdatedAt), repository.ErrProblemSetGenerationChanged)
	require.NoError(t, sets.ChangeGeneration(ctx, ref, func(s *domain.ProblemSetGenerationState) error { s.Status = "generating"; return nil }))
	require.ErrorIs(t, sets.CompleteReviewedGenerationSlot(ctx, ref, slots[0], p.UpdatedAt), repository.ErrProblemSetGenerationActive)
	require.NoError(t, sets.ChangeGeneration(ctx, ref, func(s *domain.ProblemSetGenerationState) error { s.Status = "partial"; return nil }))
	// A service precheck is insufficient: the attach transaction must re-read
	// the real status and timestamp under its own row lock.
	_, err = pool.Exec(ctx, `UPDATE problems SET status='quarantined' WHERE id=$1`, p.ID)
	require.NoError(t, err)
	require.ErrorIs(t, sets.CompleteReviewedGenerationSlot(ctx, ref, slots[0], p.UpdatedAt), repository.ErrProblemSetGenerationChanged)
	_, _, err = problems.ApplyPublicReleaseGate(ctx, p.ID, "restore-reviewed-fixture/"+uuid.NewString())
	require.NoError(t, err)
	p, err = problems.GetByID(ctx, p.ID)
	require.NoError(t, err)
	oldTimestamp := p.UpdatedAt
	_, err = pool.Exec(ctx, `UPDATE problems SET updated_at=updated_at+INTERVAL '1 microsecond' WHERE id=$1`, p.ID)
	require.NoError(t, err)
	require.ErrorIs(t, sets.CompleteReviewedGenerationSlot(ctx, ref, slots[0], oldTimestamp), repository.ErrProblemSetGenerationChanged)
	p, err = problems.GetByID(ctx, p.ID)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		require.NoError(t, sets.CompleteReviewedGenerationSlot(ctx, ref, slots[0], p.UpdatedAt))
	}
	got, err := sets.GetByID(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Equal(t, 100, got.TotalScore)
	require.Equal(t, "partial", got.Generation.Status)
	require.Equal(t, "succeeded", got.Generation.Slots[0].Status)
	require.Nil(t, got.Generation.Slots[0].Failure)
	require.Equal(t, slots[1], got.Generation.Slots[1], "single-slot resume must preserve another failure")
	// A second approval restores the remaining slot and completes the same run.
	second := *p
	second.ID = uuid.New()
	second.Title = "Second synthetic candidate"
	second.Statement = "A distinct second statement"
	second.Status = domain.ProblemStatusQuarantined
	second.CreatedAt = time.Now().UTC()
	second.UpdatedAt = second.CreatedAt
	require.NoError(t, problems.Create(ctx, &second))
	approveManualFixture(t, problems, second.ID)
	current, err := problems.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.NoError(t, sets.ChangeGeneration(ctx, ref, func(s *domain.ProblemSetGenerationState) error { s.Slots[1].ProblemID = &second.ID; return nil }))
	result := got.Generation.Slots[1]
	result.ProblemID = &second.ID
	result.Fingerprint = "distinct-second-fingerprint"
	require.NoError(t, sets.CompleteReviewedGenerationSlot(ctx, ref, result, current.UpdatedAt))
	got, err = sets.GetByID(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.Equal(t, "completed", got.Generation.Status)
	require.Empty(t, got.Generation.Error)
}
