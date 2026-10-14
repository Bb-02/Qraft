package domain

// GenerationFailure separates the user's next action from diagnostic details.
// It is also persisted with a candidate so a retry does not erase the evidence.
type GenerationFailure struct {
	Code             string   `json:"code"`
	Message          string   `json:"message"`
	Suggestion       string   `json:"suggestion"`
	TechnicalDetails string   `json:"technical_details,omitempty"`
	BlockedGate      string   `json:"blocked_gate,omitempty"`
	BlockingIssues   []string `json:"blocking_issues,omitempty"`
}

type GenerationDifficultyAssessment struct {
	Target      int    `json:"target"`
	Estimated   *int   `json:"estimated,omitempty"`
	Delta       *int   `json:"delta,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}
