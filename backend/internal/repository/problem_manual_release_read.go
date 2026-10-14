package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// HasCurrentManualReleaseApproval checks immutable approval history, rather
// than the editable metadata summary. The timestamp binds the caller's read.
func (r *ProblemRepository) HasCurrentManualReleaseApproval(ctx context.Context, id uuid.UUID, expectedUpdatedAt time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("manual release approval database is unavailable")
	}
	var approved bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM problems p WHERE p.id=$1 AND p.updated_at=$2
		AND p.status='published' AND problem_manual_release_approved(p)
	)`, id, expectedUpdatedAt).Scan(&approved)
	return approved, err
}
