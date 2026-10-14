package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetWorkflowTaskSet reads only the fields needed for a task summary. The
// resolver separately checks present-day entity and generation visibility.
func (r *ProblemSetRepository) GetWorkflowTaskSet(ctx context.Context, id uuid.UUID) (*domain.ProblemSet, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("problem set database is required")
	}
	var set domain.ProblemSet
	var generation []byte
	err := r.db.QueryRow(ctx, `SELECT id,title,visibility,COALESCE(owner_user_id,''),desired_item_count,generation_state FROM problem_sets WHERE id=$1`, id).Scan(&set.ID, &set.Title, &set.Visibility, &set.OwnerUserID, &set.DesiredItemCount, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("read workflow task set: %w", err)
	}
	if len(generation) > 0 && string(generation) != "null" {
		if err := json.Unmarshal(generation, &set.Generation); err != nil {
			return nil, fmt.Errorf("decode workflow task generation: %w", err)
		}
	}
	return &set, nil
}
