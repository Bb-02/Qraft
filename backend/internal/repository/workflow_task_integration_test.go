//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWorkflowTaskSetReadIsLightweightAndPreservesRunIntegration(t *testing.T) {
	_, repo, set := setGenerationDB(t)
	ctx := context.Background()
	read, err := repo.GetWorkflowTaskSet(ctx, set.ID)
	require.NoError(t, err)
	require.Equal(t, set.Title, read.Title)
	require.Nil(t, read.Generation)
	require.Nil(t, read.Items)
	require.Nil(t, read.GenerationConfig)
	state := &domain.ProblemSetGenerationState{ID: uuid.NewString(), Status: "queued", StartedAt: time.Now().UTC(), Slots: []domain.ProblemSetGenerationSlot{{Position: 1, Status: "pending", ChildID: "synthetic-summary-child"}}}
	state, err = repo.ReserveGeneration(ctx, set.ID, set.UpdatedAt, state)
	require.NoError(t, err)
	before, err := repo.GetByID(ctx, set.ID)
	require.NoError(t, err)
	read, err = repo.GetWorkflowTaskSet(ctx, set.ID)
	require.NoError(t, err)
	require.Equal(t, state, read.Generation)
	require.Nil(t, read.Items)
	after, err := repo.GetByID(ctx, set.ID)
	require.NoError(t, err)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt)
	require.Equal(t, before.Generation, after.Generation)
	_, err = repo.GetWorkflowTaskSet(ctx, uuid.New())
	require.ErrorIs(t, err, sql.ErrNoRows)
}
