package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	ProblemGenerationAuthoringStatementPayloadVersionV1 = 1
	authoringStatementGateContractErrorTypeV1           = "AuthoringStatementGateContractError"
)

// ProblemGenerationAuthoringStatementResultV1 is a terminal internal-stage
// envelope. A rejected authoring result has no statement draft. An accepted
// result points to an internal presentation shell, not a final problem.
type ProblemGenerationAuthoringStatementResultV1 struct {
	PayloadVersion       int                                                  `json:"payload_version"`
	Decision             string                                               `json:"decision"`
	GateReasonCode       string                                               `json:"gate_reason_code,omitempty"`
	RejectionReason      string                                               `json:"rejection_reason,omitempty"`
	AuthoringResult      *activities.GenerateAuthoringPlanResult              `json:"authoring_result"`
	StatementDraftResult *activities.RenderStatementFromAuthoringBundleResult `json:"statement_draft_result,omitempty"`
}

// ProblemGenerationAuthoringStatementWorkflowV1 is a second, additive dormant
// workflow type. It does not change ProblemGenerationAuthoringWorkflowV1 or
// any legacy history. Production starters do not route to this type.
func ProblemGenerationAuthoringStatementWorkflowV1(
	ctx workflow.Context,
	in activities.GenerateAuthoringPlanInput,
) (*ProblemGenerationAuthoringStatementResultV1, error) {
	if err := validateAuthoringWorkflowInputV1(in); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			err.Error(), authoringInputContractErrorTypeV1, err,
		)
	}
	presentationLocale, err := authoringStatementLocaleV1(in.Params.Locale)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			err.Error(), authoringInputContractErrorTypeV1, err,
		)
	}

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Minute,
			MaximumAttempts:    3,
			NonRetryableErrorTypes: []string{
				"InvalidParameterError",
			},
		},
	}
	activityCtx := workflow.WithActivityOptions(ctx, activityOptions)

	var authoring activities.GenerateAuthoringPlanResult
	if err := workflow.ExecuteActivity(
		activityCtx,
		"GenerateAuthoringPlanActivity",
		in,
	).Get(ctx, &authoring); err != nil {
		return nil, fmt.Errorf("generate authoring plan: %w", err)
	}
	if err := validateAuthoringWorkflowResultV1(in, &authoring); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			err.Error(), authoringGateContractErrorTypeV1, err,
		)
	}

	terminal := &ProblemGenerationAuthoringStatementResultV1{
		PayloadVersion:  ProblemGenerationAuthoringStatementPayloadVersionV1,
		Decision:        authoring.Decision,
		GateReasonCode:  authoring.GateReasonCode,
		RejectionReason: authoring.RejectionReason,
		AuthoringResult: &authoring,
	}
	if authoring.Decision == activities.AuthoringPlanDecisionRejected {
		return terminal, nil
	}

	bundleRef := *authoring.BundleArtifact
	rendererInput := activities.RenderStatementFromAuthoringBundleInput{
		PayloadVersion:               activities.RenderStatementFromAuthoringBundlePayloadVersion,
		BundleArtifact:               bundleRef,
		ExpectedBundleSHA256:         authoring.BundleSHA256,
		ExpectedAuthoringInputSHA256: authoring.InputSHA256,
		ExpectedBriefSHA256:          authoring.BriefSHA256,
		ExpectedSemanticSpecSHA256:   authoring.SemanticSpecSHA256,
		ExpectedDifficulty:           in.Params.Difficulty,
		RequiredKnowledgePoints:      append([]string(nil), in.Params.Tags...),
		PresentationLocale:           presentationLocale,
		StatementRuntime:             copyAuthoringStatementRuntimeV1(in.Params.ProviderConfig),
	}

	var statement activities.RenderStatementFromAuthoringBundleResult
	if err := workflow.ExecuteActivity(
		activityCtx,
		"RenderStatementFromAuthoringBundleActivityV1",
		rendererInput,
	).Get(ctx, &statement); err != nil {
		return nil, fmt.Errorf("render statement from authoring bundle: %w", err)
	}
	if err := validateAuthoringStatementWorkflowResultV1(rendererInput, &statement); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			err.Error(), authoringStatementGateContractErrorTypeV1, err,
		)
	}
	terminal.StatementDraftResult = &statement
	return terminal, nil
}

func authoringStatementLocaleV1(value string) (string, error) {
	switch value {
	case "":
		return "en", nil
	case "en", "zh":
		return value, nil
	default:
		return "", fmt.Errorf("authoring statement locale must be empty, en, or zh")
	}
}

func copyAuthoringStatementRuntimeV1(config *domain.ProviderRuntimeConfig) *domain.LLMRuntimeConfig {
	if config == nil || config.Statement == nil {
		return nil
	}
	copyValue := *config.Statement
	return &copyValue
}

func validateAuthoringStatementWorkflowResultV1(
	in activities.RenderStatementFromAuthoringBundleInput,
	result *activities.RenderStatementFromAuthoringBundleResult,
) error {
	if result == nil {
		return fmt.Errorf("statement draft activity result is required")
	}
	if result.PayloadVersion != activities.RenderStatementFromAuthoringBundlePayloadVersion {
		return fmt.Errorf("unsupported statement draft result payload version %d", result.PayloadVersion)
	}
	rendererInputSHA, err := authoringStatementRendererInputSHA256V1(in)
	if err != nil {
		return fmt.Errorf("hash statement renderer input: %w", err)
	}
	if result.RendererInputSHA256 != rendererInputSHA ||
		result.AuthoringBundleSHA256 != in.ExpectedBundleSHA256 ||
		result.AuthoringInputSHA256 != in.ExpectedAuthoringInputSHA256 ||
		result.BriefSHA256 != in.ExpectedBriefSHA256 ||
		result.SemanticSpecSHA256 != in.ExpectedSemanticSpecSHA256 {
		return fmt.Errorf("statement draft result input binding mismatch")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "fact_manifest_sha256", value: result.FactManifestSHA256},
		{name: "markdown_sha256", value: result.MarkdownSHA256},
		{name: "statement_draft_sha256", value: result.StatementDraftSHA256},
	} {
		if !authoringWorkflowIsSHA256V1(field.value) {
			return fmt.Errorf("statement draft result %s is invalid", field.name)
		}
	}
	if result.StatementDraftArtifact == nil ||
		result.StatementDraftArtifact.SHA256 != result.StatementDraftSHA256 ||
		result.StatementDraftArtifact.ContentType != "application/json" {
		return fmt.Errorf("statement draft artifact identity mismatch")
	}
	if err := result.StatementDraftArtifact.Validate(result.StatementDraftArtifact.Bucket); err != nil {
		return fmt.Errorf("invalid statement draft artifact ref: %w", err)
	}
	if (len(result.SourceArtifacts) != 3 && len(result.SourceArtifacts) != 4) || result.SourceArtifacts[0] == nil {
		return fmt.Errorf("statement draft result requires bundle, raw response, optional one presentation correction, and draft artifacts")
	}
	if !reflect.DeepEqual(result.SourceArtifacts[0], &in.BundleArtifact) {
		return fmt.Errorf("statement draft ancestry does not begin with the exact authoring bundle ref")
	}
	last := len(result.SourceArtifacts) - 1
	for _, source := range result.SourceArtifacts[1:last] {
		if source == nil || !authoringWorkflowIsSHA256V1(source.SHA256) || source.LLMCallReceipt == nil || !authoringWorkflowIsSHA256V1(source.LLMCallReceipt.RequestSHA256) {
			return fmt.Errorf("statement draft raw source lacks immutable request identity")
		}
	}
	if !reflect.DeepEqual(result.SourceArtifacts[last], result.StatementDraftArtifact) {
		return fmt.Errorf("statement draft ancestry does not end with the exact draft artifact ref")
	}
	if last == 3 {
		for _, source := range result.SourceArtifacts[1:last] {
			if err := source.Validate(result.StatementDraftArtifact.Bucket); err != nil {
				return fmt.Errorf("statement presentation correction source ref is invalid: %w", err)
			}
			if source.WorkflowID != result.StatementDraftArtifact.WorkflowID || source.Producer != result.StatementDraftArtifact.Producer {
				return fmt.Errorf("statement presentation correction belongs to a different workflow or producer")
			}
			receipt := source.LLMCallReceipt
			if receipt.SchemaVersion != 1 || strings.TrimSpace(receipt.RequestedModel) == "" || receipt.Provider != source.Provider || !authoringWorkflowIsSHA256V1(receipt.EndpointID) || !authoringWorkflowIsSHA256V1(receipt.PromptHash) {
				return fmt.Errorf("statement presentation correction source receipt is incomplete")
			}
		}
		original, final := result.SourceArtifacts[1].LLMCallReceipt, result.SourceArtifacts[2].LLMCallReceipt
		if original.PromptID != activities.AuthoringStatementPresentationPromptIDV2 || final.PromptID != activities.AuthoringStatementPresentationRepairPromptIDV2 || original.PromptVersion != "v2" || final.PromptVersion != "v2" || original.RequestSHA256 == final.RequestSHA256 {
			return fmt.Errorf("statement presentation correction prompt identities are invalid")
		}
		if original.RequestedModel != final.RequestedModel || original.Provider != final.Provider || original.EndpointID != final.EndpointID {
			return fmt.Errorf("statement presentation correction changed provider route")
		}
	}
	return nil
}

func authoringStatementRendererInputSHA256V1(in activities.RenderStatementFromAuthoringBundleInput) (string, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return authoringWorkflowSHA256HexV1(encoded), nil
}
