package service

import (
	"net/url"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/google/uuid"
)

// Import pages reopen a workflow; rating pages accept the immutable assessment
// UUID encoded by RatingRepository.CreateAssessment as rating-<UUID>.
func workflowTaskBusinessSourceURL(identity access.Principal, workflowID, workflowType string) string {
	switch workflowType {
	case "ProblemImportWorkflow":
		if workflowID != "" {
			return "/problems/import?workflow=" + url.QueryEscape(workflowID)
		}
	case "RatingWorkflow":
		// Assessment reads are administrator-only, matching RatingHandler.
		if !identity.IsAdmin() || !strings.HasPrefix(workflowID, "rating-") {
			return ""
		}
		value := strings.TrimPrefix(workflowID, "rating-")
		id, err := uuid.Parse(value)
		if err == nil && id != uuid.Nil && id.String() == value {
			return "/rating?assessment=" + url.QueryEscape(id.String())
		}
	}
	return ""
}
