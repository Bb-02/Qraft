package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	enumspb "go.temporal.io/api/enums/v1"
)

const generationModelSettingsURL = "/settings?section=verification"
const generationReadinessProbeTimeout = 1500 * time.Millisecond

type ProblemSetGenerationReadinessOptions struct {
	Providers        ProblemSetReadinessProviderResolver
	Sandbox          func(context.Context) (bool, error)
	EmbeddingEnabled bool
	FallbackBaseURL  string
}

func (s *ProblemSetGenerationService) SetGenerationReadiness(options ProblemSetGenerationReadinessOptions) {
	s.readiness = &options
}

func (s *ProblemSetGenerationService) SetGenerationProviderResolver(resolver ProblemSetReadinessProviderResolver) {
	s.resolveForMode = resolver
}

func setHasProgrammingQuestions(config *domain.ProblemSetGenerationConfig) bool {
	if config != nil {
		for _, quota := range config.Distribution {
			if quota.Type == domain.QuizTypeProgramming && quota.Count > 0 {
				return true
			}
		}
	}
	return false
}

func (s *ProblemSetGenerationService) resolveSetGenerationProviders(ctx context.Context, config *domain.ProblemSetGenerationConfig) (*domain.ProviderRuntimeConfig, error) {
	if s.resolveForMode != nil {
		return s.resolveForMode(ctx, setHasProgrammingQuestions(config))
	}
	return s.resolve(ctx)
}

type ProblemSetGenerationNotReadyError struct {
	Readiness *domain.ProblemSetGenerationReadiness
}

func (e *ProblemSetGenerationNotReadyError) Error() string {
	if e.Readiness != nil && len(e.Readiness.Blockers) > 0 {
		return e.Readiness.Blockers[0].Message
	}
	return "题集生成条件尚未满足，请检查配置后重新检查"
}

func newSetGenerationReadiness() *domain.ProblemSetGenerationReadiness {
	return &domain.ProblemSetGenerationReadiness{Ready: true, Scope: "configured_routes",
		Blockers: []domain.GenerationReadinessNotice{}, Warnings: []domain.GenerationReadinessNotice{},
		Checks: []domain.GenerationReadinessCheck{}, EffectiveRoles: []domain.GenerationReadinessRole{}}
}

func addSetReadinessCheck(report *domain.ProblemSetGenerationReadiness, code, status, message, settingsURL string) {
	report.Checks = append(report.Checks, domain.GenerationReadinessCheck{Code: code, Status: status, Message: message})
	notice := domain.GenerationReadinessNotice{Code: code, Message: message, SettingsURL: settingsURL}
	switch status {
	case "blocked":
		report.Ready = false
		report.Blockers = append(report.Blockers, notice)
	case "warning":
		report.Warnings = append(report.Warnings, notice)
	}
}

// GenerationReadiness does not resolve runtime keys, write a reservation, create
// a workflow, or contact a model. Services are observed through read-only probes.
func (s *ProblemSetGenerationService) GenerationReadiness(ctx context.Context, request domain.ProblemSetGenerationReadinessRequest) (*domain.ProblemSetGenerationReadiness, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	report := newSetGenerationReadiness()
	if s == nil || s.temporal == nil || s.resolve == nil || s.readiness == nil || s.readiness.Providers == nil {
		addSetReadinessCheck(report, "generation_available", "blocked", "题集自动生成尚未就绪，请联系管理员启用生成服务后重新检查。", "")
		return report, nil
	}
	programming := request.ProgrammingCount > 0
	settingsCtx, cancelSettings := context.WithTimeout(ctx, problemSetRuntimeBudget)
	config, err := s.readiness.Providers(settingsCtx, programming)
	cancelSettings()
	if err != nil {
		config = nil
	}
	report = s.assessSetGenerationConfiguredProviders(config, programming)
	// A known route conflict needs no further probes. It must be fixed before
	// any generation work, so do not obscure it with secondary runtime failures.
	if !report.Ready {
		return report, nil
	}
	for _, check := range s.probeSetGenerationRuntime(ctx, programming) {
		addSetReadinessCheck(report, check.Code, check.Status, check.Message, "")
	}
	return report, nil
}

// The worker repeats only the configured-route check against the exact resolved
// batch providers. Old queued runs and settings changes cannot bypass admission
// and spend model calls on a known-correlated oracle. This helper has no I/O.
func (s *ProblemSetGenerationService) assessSetGenerationConfiguredProviders(config *domain.ProviderRuntimeConfig, programming bool) *domain.ProblemSetGenerationReadiness {
	report := newSetGenerationReadiness()
	fallbackBaseURL, embeddingEnabled := "", false
	if s != nil && s.readiness != nil {
		fallbackBaseURL, embeddingEnabled = s.readiness.FallbackBaseURL, s.readiness.EmbeddingEnabled
	}
	if !validReadinessProviders(config, programming) {
		// Provider/storage/decryption errors may include private addresses or key
		// material. Only a fixed, actionable description crosses the public API.
		message := "无法读取完整的有效模型配置，请在模型配置页检查出题、验算和审核模型，然后重新检查。"
		if !programming {
			message = "无法读取完整的有效模型配置，请在模型配置页检查出题与客观题答案复核模型，然后重新检查。"
		}
		addSetReadinessCheck(report, "model_configuration", "blocked", message, generationModelSettingsURL)
		return report
	}
	generationLabel := "客观题出题"
	verificationLabel := "客观题答案复核"
	if programming {
		generationLabel = "出题与参考程序"
		verificationLabel = "独立验算"
	}
	roles := []struct {
		role, label string
		config      *domain.LLMRuntimeConfig
	}{{"generation", generationLabel, config.Statement}, {"verification", verificationLabel, config.Verification}}
	if programming {
		roles = append(roles, struct {
			role, label string
			config      *domain.LLMRuntimeConfig
		}{"review", "题目审核", config.Review})
	}
	for _, role := range roles {
		report.EffectiveRoles = append(report.EffectiveRoles, domain.GenerationReadinessRole{Role: role.role, Label: role.label, Model: safeReadinessModelName(role.config), Configured: true})
	}
	addSetReadinessCheck(report, "model_configuration", "ready", "已读取本次生成实际使用的模型配置。", "")
	if programming {
		g, v := config.Statement, config.Verification
		pair := fmt.Sprintf("出题与参考程序：%s；独立验算：%s。", safeReadinessModelName(g), safeReadinessModelName(v))
		if strings.EqualFold(strings.TrimSpace(g.Model), strings.TrimSpace(v.Model)) {
			addSetReadinessCheck(report, "oracle_model_independence", "blocked", "独立验算需要与出题不同的模型，当前配置使用同一个模型。"+pair+"请在模型配置页单独设置验算模型。", generationModelSettingsURL)
		} else {
			addSetReadinessCheck(report, "oracle_model_independence", "ready", "出题与独立验算配置了不同模型。", "")
		}
		if llm.NormalizeEndpointIdentity(g.BaseURL, fallbackBaseURL) == llm.NormalizeEndpointIdentity(v.BaseURL, fallbackBaseURL) {
			addSetReadinessCheck(report, "oracle_endpoint_independence", "blocked", "独立验算需要与出题不同的服务地址，当前两个角色使用同一个服务地址。"+pair+"请在模型配置页为验算模型设置另一个服务地址。", generationModelSettingsURL)
		} else {
			addSetReadinessCheck(report, "oracle_endpoint_independence", "ready", "出题与独立验算配置了不同服务地址。", "")
		}
		addSetReadinessCheck(report, "returned_model_identity", "warning", "以上只检查当前配置；模型服务实际返回的身份仍会在生成时核验。", "")
		if !embeddingEnabled {
			addSetReadinessCheck(report, "similarity_dedup", "warning", "当前未配置向量服务，新生成题目不会做相似题去重；题集内重复内容仍会检查。", "/settings?section=embedding")
		} else {
			addSetReadinessCheck(report, "similarity_dedup", "ready", "向量服务已启用；实际去重结果会在生成时检查。", "")
		}
	}
	return report
}

func (s *ProblemSetGenerationService) RequireGenerationReadiness(ctx context.Context, config *domain.ProblemSetGenerationConfig, total int) (*domain.ProblemSetGenerationReadiness, error) {
	if config == nil {
		return nil, fmt.Errorf("validation: 请先保存题型配额和整套需求")
	}
	if err := config.Validate(total); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}
	count := 0
	for _, quota := range config.Distribution {
		if quota.Type == domain.QuizTypeProgramming {
			count += quota.Count
		}
	}
	report, err := s.GenerationReadiness(ctx, domain.ProblemSetGenerationReadinessRequest{Mode: config.Mode, ProgrammingCount: count})
	if err != nil {
		return nil, err
	}
	if !report.Ready {
		return report, &ProblemSetGenerationNotReadyError{Readiness: report}
	}
	return report, nil
}

func validReadinessProviders(config *domain.ProviderRuntimeConfig, programming bool) bool {
	if config == nil {
		return false
	}
	roles := []*domain.LLMRuntimeConfig{config.Statement, config.Verification}
	if programming {
		roles = append(roles, config.Review)
	}
	for _, role := range roles {
		if role == nil || strings.TrimSpace(role.Model) == "" || (strings.TrimSpace(role.APIKey) != "" && strings.TrimSpace(role.APIKeyRef) != "") {
			return false
		}
		copy := *role
		if strings.TrimSpace(copy.APIKey) != "" {
			copy.APIKey, copy.APIKeyRef = "", "env:GENERATION_READINESS_PLACEHOLDER"
		}
		if copy.Validate("readiness") != nil {
			return false
		}
	}
	return true
}

var safeReadinessModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$`)

func safeReadinessModelName(config *domain.LLMRuntimeConfig) string {
	if config == nil {
		return "未配置"
	}
	model := strings.TrimSpace(config.Model)
	lower := strings.ToLower(model)
	if !safeReadinessModelPattern.MatchString(model) || strings.Contains(lower, "://") || strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "runtime:") || strings.HasPrefix(lower, "env:") || (config.APIKey != "" && strings.Contains(model, config.APIKey)) || (config.APIKeyRef != "" && strings.Contains(model, config.APIKeyRef)) {
		return "已配置模型（名称不公开）"
	}
	return model
}

func (s *ProblemSetGenerationService) probeSetGenerationRuntime(ctx context.Context, programming bool) []domain.GenerationReadinessCheck {
	checks := make([]domain.GenerationReadinessCheck, 2)
	if programming {
		checks = append(checks, domain.GenerationReadinessCheck{})
	}
	var done sync.WaitGroup
	for i, typ := range []enumspb.TaskQueueType{enumspb.TASK_QUEUE_TYPE_WORKFLOW, enumspb.TASK_QUEUE_TYPE_ACTIVITY} {
		done.Add(1)
		go func(i int, typ enumspb.TaskQueueType) {
			defer done.Done()
			code, label := "worker_workflow", "任务执行服务"
			if typ == enumspb.TASK_QUEUE_TYPE_ACTIVITY {
				code, label = "worker_activity", "生成步骤执行服务"
			}
			check := domain.GenerationReadinessCheck{Code: code, Status: "blocked"}
			if strings.TrimSpace(s.queue) == "" {
				check.Message = label + "未配置，请联系管理员设置执行队列后重新检查。"
				checks[i] = check
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, generationReadinessProbeTimeout)
			defer cancel()
			response, err := s.temporal.DescribeTaskQueue(probeCtx, s.queue, typ)
			if err != nil || response == nil {
				check.Message = "暂时无法确认" + label + "是否就绪；请稍后重新检查，本次尚未开始生成。"
			} else {
				now := time.Now().UTC()
				for _, poller := range response.Pollers {
					if poller != nil {
						access := runtimeTimestamp(poller.LastAccessTime)
						if access != nil && !access.After(now.Add(time.Minute)) && now.Sub(*access) <= problemSetRuntimePollerWindow {
							check.Status, check.Message = "ready", label+"最近已连接当前执行队列。"
							break
						}
					}
				}
				if check.Status == "blocked" {
					check.Message = "最近未观察到" + label + "连接，可能尚未启动、连接中断或繁忙；请启动或恢复服务后重新检查。"
				}
			}
			checks[i] = check
		}(i, typ)
	}
	if programming {
		done.Add(1)
		go func() {
			defer done.Done()
			check := domain.GenerationReadinessCheck{Code: "sandbox", Status: "blocked", Message: "程序运行沙箱未配置，请联系管理员配置后重新检查。"}
			if s.readiness.Sandbox != nil {
				probeCtx, cancel := context.WithTimeout(ctx, generationReadinessProbeTimeout)
				defer cancel()
				ready, err := s.readiness.Sandbox(probeCtx)
				switch {
				case err != nil:
					check.Message = "暂时无法确认程序运行沙箱是否就绪；请稍后重新检查，本次尚未开始生成。"
				case !ready:
					check.Message = "程序运行沙箱报告尚未就绪，请恢复沙箱及 C++ 运行环境后重新检查。"
				default:
					check.Status, check.Message = "ready", "程序运行沙箱及 C++ 运行环境已就绪。"
				}
			}
			checks[2] = check
		}()
	}
	done.Wait()
	return checks
}

// NewProblemSetSandboxReadinessProbe contacts only the deployment-owned health
// URL. It does not compile or run user code, and never returns private details.
func NewProblemSetSandboxReadinessProbe(baseURL string) func(context.Context) (bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil
	}
	healthURL := strings.TrimRight(parsed.String(), "/") + "/health"
	httpClient := &http.Client{Timeout: generationReadinessProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context) (bool, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return false, fmt.Errorf("sandbox observation unavailable")
		}
		response, err := httpClient.Do(request)
		if err != nil {
			return false, fmt.Errorf("sandbox observation unavailable")
		}
		defer response.Body.Close()
		var health struct {
			Ready         bool `json:"ready"`
			NsjailReady   bool `json:"nsjail_ready"`
			CgroupV2Ready bool `json:"cgroup_v2_ready"`
			Languages     map[string]struct {
				Ready bool `json:"ready"`
			} `json:"languages"`
		}
		if (response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable) || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&health) != nil {
			return false, fmt.Errorf("sandbox observation unavailable")
		}
		return response.StatusCode == http.StatusOK && health.Ready && health.NsjailReady && health.CgroupV2Ready && health.Languages["cpp"].Ready, nil
	}
}
