package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type reviewedSlotSetStore struct {
	problemSetStore
	problemSetGenerationStore
	set       *domain.ProblemSet
	completed []domain.ProblemSetGenerationSlot
}

func (s *reviewedSlotSetStore) GetByID(context.Context, uuid.UUID) (*domain.ProblemSet, error) {
	return s.set, nil
}
func (s *reviewedSlotSetStore) CompleteReviewedGenerationSlot(_ context.Context, ref domain.ProblemSetGenerationRef, slot domain.ProblemSetGenerationSlot, _ time.Time) error {
	if ref.SetID != s.set.ID || ref.RunID != s.set.Generation.ID {
		return repository.ErrProblemSetGenerationChanged
	}
	s.completed = append(s.completed, slot)
	s.set.Generation.FindSlot(slot.Position).Status = "succeeded"
	return nil
}

type reviewedSlotPackages struct {
	ids []uuid.UUID
	err error
}

func (s *reviewedSlotPackages) BuildProblemPackage(_ context.Context, id uuid.UUID) (*HydroPackage, error) {
	s.ids = append(s.ids, id)
	return &HydroPackage{}, s.err
}
func reviewedSlotFixture(t *testing.T) (*ProblemSetGenerationService, *reviewedSlotSetStore, *manualS3ProblemSource, *reviewedSlotPackages) {
	t.Helper()
	id := uuid.New()
	p := &domain.Problem{ID: id, Status: domain.ProblemStatusPublished, Statement: "Synthetic distinct statement", UpdatedAt: time.Now().UTC()}
	source := &manualS3ProblemSource{qg15AHydroProblemSource: qg15AHydroProblemSource{problem: p}, approved: true}
	set := &domain.ProblemSet{ID: uuid.New(), Title: "Synthetic contest", DesiredItemCount: 2,
		GenerationConfig: &domain.ProblemSetGenerationConfig{Mode: "programming", Distribution: []domain.ProblemSetTypeQuota{{Type: domain.QuizTypeProgramming, Count: 2, Score: 100}}}}
	set.Generation = &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "partial", ConfigFingerprint: setConfigFingerprint(set), Slots: []domain.ProblemSetGenerationSlot{
		{Position: 1, Type: domain.QuizTypeProgramming, ProblemID: &id, ChildID: "current-candidate", Status: "awaiting_review", Error: "Original finding"},
		{Position: 2, Type: domain.QuizTypeProgramming, ChildID: "other-candidate", Status: "failed", Error: "Keep this failure"},
	}}
	repo := &reviewedSlotSetStore{set: set}
	packages := &reviewedSlotPackages{}
	sets := NewProblemSetServiceWithDeps(repo, source, nil, packages, nil)
	return &ProblemSetGenerationService{sets: sets, repo: repo}, repo, source, packages
}
func TestReviewedSlotResumeRestoresOnlySelectedCandidateWithoutModels(t *testing.T) {
	svc, repo, source, packages := reviewedSlotFixture(t)
	other := repo.set.Generation.Slots[1]
	state, err := svc.ResumeReviewedSlot(context.Background(), repo.set.ID, 1)
	require.NoError(t, err)
	require.Equal(t, "succeeded", state.Slots[0].Status)
	require.Equal(t, other, state.Slots[1])
	require.Equal(t, []uuid.UUID{source.problem.ID}, packages.ids)
	require.Len(t, repo.completed, 1)
	require.Equal(t, setContentFingerprint(source.problem.Statement), repo.completed[0].Fingerprint)
}
func TestReviewedSlotResumeRejectsUnapprovedStaleActiveAndBrokenCandidates(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ProblemSetGenerationService, *reviewedSlotSetStore, *manualS3ProblemSource, *reviewedSlotPackages)
	}{
		{"no current approval", func(_ *ProblemSetGenerationService, _ *reviewedSlotSetStore, s *manualS3ProblemSource, _ *reviewedSlotPackages) {
			s.approved = false
		}},
		{"still quarantined", func(_ *ProblemSetGenerationService, _ *reviewedSlotSetStore, s *manualS3ProblemSource, _ *reviewedSlotPackages) {
			s.problem.Status = domain.ProblemStatusQuarantined
		}},
		{"active generation", func(_ *ProblemSetGenerationService, r *reviewedSlotSetStore, _ *manualS3ProblemSource, _ *reviewedSlotPackages) {
			r.set.Generation.Status = "generating"
		}},
		{"changed requirements", func(_ *ProblemSetGenerationService, r *reviewedSlotSetStore, _ *manualS3ProblemSource, _ *reviewedSlotPackages) {
			r.set.Title = "Changed"
		}},
		{"missing source", func(_ *ProblemSetGenerationService, r *reviewedSlotSetStore, _ *manualS3ProblemSource, _ *reviewedSlotPackages) {
			r.set.Generation.Slots[0].ProblemID = nil
		}},
		{"rejected duplicate", func(_ *ProblemSetGenerationService, r *reviewedSlotSetStore, _ *manualS3ProblemSource, _ *reviewedSlotPackages) {
			r.set.Generation.Slots[0].Regenerate = true
		}},
		{"broken assets", func(_ *ProblemSetGenerationService, _ *reviewedSlotSetStore, _ *manualS3ProblemSource, p *reviewedSlotPackages) {
			p.err = fmt.Errorf("missing actual testcase file")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, source, packages := reviewedSlotFixture(t)
			tc.edit(svc, repo, source, packages)
			_, err := svc.ResumeReviewedSlot(context.Background(), repo.set.ID, 1)
			require.Error(t, err)
			require.Empty(t, repo.completed)
		})
	}
	svc, repo, _, _ := reviewedSlotFixture(t)
	_, err := svc.ResumeReviewedSlot(access.WithPrincipal(context.Background(), access.Principal{UserID: "synthetic-member", Role: "member"}), repo.set.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)
}
func TestSetSlotRecordsStructuredPendingReview(t *testing.T) {
	id := uuid.New()
	failure := &domain.GenerationFailure{Code: "quality_not_met", Message: "请人工核对这道题"}
	estimated := 800
	assessment := &domain.GenerationDifficultyAssessment{Target: 400, Estimated: &estimated}
	state := &domain.ProblemSetGenerationState{Status: "generating", Slots: []domain.ProblemSetGenerationSlot{{Position: 1, Type: domain.QuizTypeProgramming, ChildID: "child"}}}
	svc := &ProblemSetGenerationService{repo: &setStateOnlyStore{state: state}}
	err := svc.RecordProblemSetSlotActivity(context.Background(), algoworkflow.ProblemSetSlotInput{Slot: domain.ProblemSetGenerationSlot{Position: 1, Type: domain.QuizTypeProgramming, ChildID: "child", Status: "awaiting_review", ProblemID: &id, Failure: failure, DifficultyAssessment: assessment}})
	require.NoError(t, err)
	require.Equal(t, failure, state.Slots[0].Failure)
	require.Equal(t, &id, state.Slots[0].ProblemID)
	require.Equal(t, assessment, state.Slots[0].DifficultyAssessment)
}
