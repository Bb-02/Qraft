package domain

// WorkflowTaskSummary describes the accessible business record behind an
// execution. It does not replace Temporal execution status or expose inputs.
type WorkflowTaskSummary struct {
	Kind             string `json:"kind"`
	Title            string `json:"title"`
	SourceURL        string `json:"source_url,omitempty"`
	ResultURL        string `json:"result_url,omitempty"`
	ParentWorkflowID string `json:"parent_workflow_id,omitempty"`
	Position         *int   `json:"position,omitempty"`
	IsChild          bool   `json:"is_child,omitempty"`
	Summary          string `json:"summary,omitempty"`
	Status           string `json:"status,omitempty"`
	CompletedCount   *int   `json:"completed_count,omitempty"`
	TotalCount       *int   `json:"total_count,omitempty"`
}
