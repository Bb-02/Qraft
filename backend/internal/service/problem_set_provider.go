package service

import (
	"context"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"strings"
)

type setSavedProviderSettings interface {
	EffectiveRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, error)
	SavedRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, bool, error)
}
type setRuntimeKeys interface {
	Put(context.Context, string) (string, error)
}
type ProblemSetProviderResolver func(context.Context) (*domain.ProviderRuntimeConfig, error)

type ProblemSetReadinessProviderResolver func(context.Context, bool) (*domain.ProviderRuntimeConfig, error)

// This resolver only reads saved settings. It never registers runtime keys,
// probes a provider or calls a model.
func NewProblemSetReadinessProviderResolver(settings setSavedProviderSettings) ProblemSetReadinessProviderResolver {
	return func(ctx context.Context, programming bool) (*domain.ProviderRuntimeConfig, error) {
		return loadProblemSetProviders(ctx, settings, programming)
	}
}

func loadProblemSetProviders(ctx context.Context, settings setSavedProviderSettings, programming bool) (*domain.ProviderRuntimeConfig, error) {
	if settings == nil {
		return nil, fmt.Errorf("not configured: 请先配置出题模型")
	}
	g, err := settings.EffectiveRuntimeConfig(ctx, "statement")
	if err != nil {
		return nil, err
	}
	if g == nil || strings.TrimSpace(g.Model) == "" {
		return nil, fmt.Errorf("not configured: 请先配置出题模型")
	}
	v, saved, err := settings.SavedRuntimeConfig(ctx, "verification")
	if err != nil {
		return nil, err
	}
	if !saved {
		v = g
	}
	if !programming {
		return &domain.ProviderRuntimeConfig{Statement: g, Verification: v}, nil
	}
	r, saved, err := settings.SavedRuntimeConfig(ctx, "review")
	if err != nil {
		return nil, err
	}
	if !saved {
		r = v
	}
	return &domain.ProviderRuntimeConfig{Statement: g, Verification: v, Review: r}, nil
}

// Resolve per batch so large sets do not retain expired runtime references.
func NewProblemSetProviderResolver(settings setSavedProviderSettings, keys setRuntimeKeys) ProblemSetProviderResolver {
	return func(ctx context.Context) (*domain.ProviderRuntimeConfig, error) {
		return newProblemSetProviderResolverForMode(settings, keys, ProblemSetGenerationReadinessOptions{}, false)(ctx, true)
	}
}

// Objective questions also need V for answer review, but do not use the
// programming-only R reviewer or require cross-model oracle independence.
func NewProblemSetProviderResolverForMode(settings setSavedProviderSettings, keys setRuntimeKeys, options ...ProblemSetGenerationReadinessOptions) ProblemSetReadinessProviderResolver {
	var readinessOptions ProblemSetGenerationReadinessOptions
	if len(options) > 0 {
		readinessOptions = options[0]
	}
	return newProblemSetProviderResolverForMode(settings, keys, readinessOptions, true)
}

func newProblemSetProviderResolverForMode(settings setSavedProviderSettings, keys setRuntimeKeys, options ProblemSetGenerationReadinessOptions, checkIndependence bool) ProblemSetReadinessProviderResolver {
	return func(ctx context.Context, programming bool) (*domain.ProviderRuntimeConfig, error) {
		loaded, err := loadProblemSetProviders(ctx, settings, programming)
		if err != nil {
			return nil, err
		}
		if checkIndependence {
			// Check this exact saved snapshot before registering any runtime keys.
			// API readiness may have observed older settings while a run queued.
			checker := &ProblemSetGenerationService{readiness: &options}
			report := checker.assessSetGenerationConfiguredProviders(loaded, programming)
			if !report.Ready {
				return nil, &ProblemSetGenerationNotReadyError{Readiness: report}
			}
		}
		prepare := func(in *domain.LLMRuntimeConfig) (*domain.LLMRuntimeConfig, error) {
			if in == nil {
				return nil, fmt.Errorf("not configured: 模型配置不完整")
			}
			out := *in
			if strings.TrimSpace(out.APIKey) != "" {
				if out.APIKeyRef != "" || keys == nil {
					return nil, fmt.Errorf("模型密钥配置冲突")
				}
				ref, err := keys.Put(ctx, out.APIKey)
				if err != nil {
					return nil, err
				}
				out.APIKey = ""
				out.APIKeyRef = ref
			}
			return &out, nil
		}
		g, err := prepare(loaded.Statement)
		if err != nil {
			return nil, err
		}
		v, err := prepare(loaded.Verification)
		if err != nil {
			return nil, err
		}
		var r *domain.LLMRuntimeConfig
		if programming {
			r, err = prepare(loaded.Review)
			if err != nil {
				return nil, err
			}
		}
		out := &domain.ProviderRuntimeConfig{Statement: g, Verification: v, Review: r}
		if err = out.Validate(); err != nil {
			return nil, err
		}
		return out, nil
	}
}
