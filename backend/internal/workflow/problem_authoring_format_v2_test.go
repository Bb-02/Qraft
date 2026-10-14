package workflow

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/stretchr/testify/mock"
)

func correctedAuthoringWorkflowResultV2(t *testing.T, input activities.GenerateAuthoringPlanInput) *activities.GenerateAuthoringPlanResult {
	t.Helper()
	result := acceptedAuthoringWorkflowResult(t, input)
	fill := func(ref *activities.ArtifactRef) {
		ref.SchemaVersion = activities.ArtifactRefSchemaVersion
		ref.PayloadVersion = activities.ActivityPayloadVersion
		ref.Bucket = "fixture"
		ref.Key = "workflow-artifacts/v1/sha256/" + ref.SHA256[:2] + "/" + ref.SHA256
		ref.SizeBytes = 1
		ref.ContentType = "application/json"
		ref.Producer = "GenerateAuthoringPlanActivity"
		ref.Provider = "fixture-provider"
		ref.Model = "fixture-model"
		ref.ModelRevision = "fixture-model-revision"
		ref.WorkflowID = "synthetic-authoring-workflow"
	}
	original := result.SourceArtifacts[0]
	fill(original)
	*original.LLMCallReceipt = activities.LLMCallReceipt{
		SchemaVersion:  1,
		PromptID:       activities.AuthoringPlanPromptIDV2,
		PromptVersion:  "v2",
		RequestedModel: "fixture-model",
		Provider:       "fixture-provider",
		EndpointID:     strings.Repeat("1", 64),
		PromptHash:     strings.Repeat("2", 64),
		RequestSHA256:  strings.Repeat("d", 64),
	}
	corrected := *original
	corrected.SHA256 = strings.Repeat("e", 64)
	fill(&corrected)
	receipt := *original.LLMCallReceipt
	receipt.PromptID = activities.AuthoringPlanFormatRepairPromptIDV2
	receipt.PromptHash = strings.Repeat("3", 64)
	receipt.RequestSHA256 = strings.Repeat("f", 64)
	corrected.LLMCallReceipt = &receipt
	fill(result.BundleArtifact)
	result.SourceArtifacts = []*activities.ArtifactRef{original, &corrected, result.BundleArtifact}
	return result
}

func TestAuthoringResultAllowsOneAuditableFormatCorrectionV2(t *testing.T) {
	_, _, input := newAuthoringWorkflowEnvironment(t)
	if err := validateAuthoringWorkflowResultV1(input, acceptedAuthoringWorkflowResult(t, input)); err != nil {
		t.Fatalf("old two-reference contract is no longer supported: %v", err)
	}
	result := correctedAuthoringWorkflowResultV2(t, input)
	if err := validateAuthoringWorkflowResultV1(input, result); err != nil {
		t.Fatalf("corrected result rejected: %v", err)
	}
	// An extra original raw response cannot substitute for lint or mutate the
	// gate outcome. Both model rejection and actual lint rejection remain valid.
	result.Decision = activities.AuthoringPlanDecisionRejected
	result.GateReasonCode = activities.AuthoringPlanGateReasonSpecLintFailed
	result.RejectionReason = "deterministic spec lint failed"
	result.LintPassed = false
	result.LintErrorCount = 1
	if err := validateAuthoringWorkflowResultV1(input, result); err != nil {
		t.Fatalf("corrected but lint-rejected result rejected: %v", err)
	}
}

func TestAuthoringResultAcceptsV3CorrectionAndKeepsHistoricalV2(t *testing.T) {
	_, _, input := newAuthoringWorkflowEnvironment(t)
	v2 := correctedAuthoringWorkflowResultV2(t, input)
	if err := validateAuthoringWorkflowResultV1(input, v2); err != nil {
		t.Fatalf("historical v2 rejected: %v", err)
	}
	v3 := correctedAuthoringWorkflowResultV2(t, input)
	v3.SourceArtifacts[0].LLMCallReceipt.PromptID = activities.AuthoringPlanPromptIDV3
	v3.SourceArtifacts[1].LLMCallReceipt.PromptID = activities.AuthoringPlanFormatRepairPromptIDV3
	v3.SourceArtifacts[0].LLMCallReceipt.PromptVersion, v3.SourceArtifacts[1].LLMCallReceipt.PromptVersion = "v3", "v3"
	if err := validateAuthoringWorkflowResultV1(input, v3); err != nil {
		t.Fatalf("v3 rejected: %v", err)
	}
	v3.SourceArtifacts[0].LLMCallReceipt.PromptVersion = "v2"
	if err := validateAuthoringWorkflowResultV1(input, v3); err == nil {
		t.Fatal("mixed prompt versions accepted")
	}
}

func TestAuthoringResultAcceptsV4CapabilitiesCorrectionWithoutRelabelingV3(t *testing.T) {
	_, _, input := newAuthoringWorkflowEnvironment(t)
	v4 := correctedAuthoringWorkflowResultV2(t, input)
	v4.SourceArtifacts[0].LLMCallReceipt.PromptID = activities.AuthoringPlanPromptIDV4
	v4.SourceArtifacts[1].LLMCallReceipt.PromptID = activities.AuthoringPlanFormatRepairPromptIDV4
	v4.SourceArtifacts[0].LLMCallReceipt.PromptVersion, v4.SourceArtifacts[1].LLMCallReceipt.PromptVersion = "v4", "v4"
	if err := validateAuthoringWorkflowResultV1(input, v4); err != nil {
		t.Fatalf("v4 rejected: %v", err)
	}
	v4.SourceArtifacts[1].LLMCallReceipt.PromptID = activities.AuthoringPlanFormatRepairPromptIDV3
	if err := validateAuthoringWorkflowResultV1(input, v4); err == nil {
		t.Fatal("mixed v4/v3 correction identities accepted")
	}
}

func TestAuthoringResultRejectsLintExplanationOnAcceptedOutcome(t *testing.T) {
	_, _, input := newAuthoringWorkflowEnvironment(t)
	result := acceptedAuthoringWorkflowResult(t, input)
	result.Failure = &domain.GenerationFailure{Code: "spec_lint_failed", Message: "合成错误", BlockedGate: "spec_lint", BlockingIssues: []string{"缺少整数范围"}}
	if err := validateAuthoringWorkflowResultV1(input, result); err == nil {
		t.Fatal("accepted result carried a conflicting server lint failure")
	}
}

func TestAuthoringResultRejectsUnboundFormatCorrectionV2(t *testing.T) {
	_, _, input := newAuthoringWorkflowEnvironment(t)
	mutations := map[string]func(*activities.GenerateAuthoringPlanResult){
		"original receipt missing":  func(result *activities.GenerateAuthoringPlanResult) { result.SourceArtifacts[0].LLMCallReceipt = nil },
		"corrected receipt missing": func(result *activities.GenerateAuthoringPlanResult) { result.SourceArtifacts[1].LLMCallReceipt = nil },
		"original foreign workflow": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[0].WorkflowID = "foreign-workflow"
		},
		"corrected foreign producer": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[1].Producer = "different-activity"
		},
		"corrected unknown prompt": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[1].LLMCallReceipt.PromptID = "unknown"
		},
		"same request reused": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[1].LLMCallReceipt.RequestSHA256 = result.SourceArtifacts[0].LLMCallReceipt.RequestSHA256
		},
		"route changed": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[1].LLMCallReceipt.RequestedModel = "other-model"
		},
		"provider order reversed": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[0], result.SourceArtifacts[1] = result.SourceArtifacts[1], result.SourceArtifacts[0]
		},
		"bundle order changed": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts[1], result.SourceArtifacts[2] = result.SourceArtifacts[2], result.SourceArtifacts[1]
		},
		"more than one correction": func(result *activities.GenerateAuthoringPlanResult) {
			result.SourceArtifacts = append(result.SourceArtifacts[:2], result.SourceArtifacts[1], result.BundleArtifact)
		},
		"correction fakes lint pass": func(result *activities.GenerateAuthoringPlanResult) { result.LintPassed = false },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			result := correctedAuthoringWorkflowResultV2(t, input)
			mutate(result)
			if err := validateAuthoringWorkflowResultV1(input, result); err == nil {
				t.Fatal("unbound correction or gate summary was accepted")
			}
		})
	}
}

func TestAuthoringWorkflowConsumesCorrectedResultWithoutAdditionalGeneration(t *testing.T) {
	env, acts, input := newAuthoringWorkflowEnvironment(t)
	want := correctedAuthoringWorkflowResultV2(t, input)
	env.OnActivity(acts.GenerateAuthoringPlanActivity, mock.Anything, input).Return(want, nil).Once()
	env.ExecuteWorkflow(ProblemGenerationAuthoringWorkflowV1, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("corrected authoring result failed workflow: %v", err)
	}
	var got activities.GenerateAuthoringPlanResult
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatal(err)
	}
	if got.BundleSHA256 != want.BundleSHA256 || len(got.SourceArtifacts) != 3 || !got.LintPassed {
		t.Fatalf("corrected evidence lost at workflow boundary: %+v", got)
	}
	assertAuthoringWorkflowCallsOnlyFormalizer(t, env)
}
