package domain

import "fmt"

// Readiness is a read-only observation of configured routes and local services.
// It cannot attest the identities returned by a future model request.
type ProblemSetGenerationReadinessRequest struct {
	Mode             string `json:"mode"`
	ProgrammingCount int    `json:"programming_count"`
}

func (r ProblemSetGenerationReadinessRequest) Validate() error {
	if r.Mode != "programming" && r.Mode != "mixed" {
		return fmt.Errorf("请选择纯编程比赛或混合题型")
	}
	if r.ProgrammingCount < 0 || r.ProgrammingCount > 1000 || (r.Mode == "programming" && r.ProgrammingCount == 0) {
		return fmt.Errorf("编程题数量必须为 0-1000，纯编程比赛至少需要一道编程题")
	}
	return nil
}

type GenerationReadinessNotice struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	SettingsURL string `json:"settings_url,omitempty"`
}

type GenerationReadinessCheck struct {
	Code    string `json:"code"`
	Status  string `json:"status"` // ready, blocked, warning
	Message string `json:"message"`
}

type GenerationReadinessRole struct {
	Role       string `json:"role"`
	Label      string `json:"label"`
	Model      string `json:"model"`
	Configured bool   `json:"configured"`
}

type ProblemSetGenerationReadiness struct {
	Ready          bool                        `json:"ready"`
	Scope          string                      `json:"scope"`
	Blockers       []GenerationReadinessNotice `json:"blockers"`
	Warnings       []GenerationReadinessNotice `json:"warnings"`
	Checks         []GenerationReadinessCheck  `json:"checks"`
	EffectiveRoles []GenerationReadinessRole   `json:"effective_roles"`
}
