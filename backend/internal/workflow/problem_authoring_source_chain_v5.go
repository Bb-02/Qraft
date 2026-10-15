package workflow

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
)

// Source roles are ordered and bounded: initial, optional format correction,
// optional V5 test-intent correction, then the server-created bundle.
func validateAuthoringSourceChainV5(result *activities.GenerateAuthoringPlanResult) error {
	if len(result.SourceArtifacts) < 2 || len(result.SourceArtifacts) > 4 {
		return fmt.Errorf("authoring result requires raw-response, optional one format correction, optional one test-intent correction, and bundle artifacts")
	}
	last := len(result.SourceArtifacts) - 1
	for _, source := range result.SourceArtifacts[:last] {
		if source == nil || !authoringWorkflowIsSHA256V1(source.SHA256) || source.LLMCallReceipt == nil || !authoringWorkflowIsSHA256V1(source.LLMCallReceipt.RequestSHA256) {
			return fmt.Errorf("authoring result raw source artifact lacks immutable request identity")
		}
	}
	if result.SourceArtifacts[last] == nil || result.SourceArtifacts[last].SHA256 != result.BundleSHA256 || !reflect.DeepEqual(result.SourceArtifacts[last], result.BundleArtifact) {
		return fmt.Errorf("authoring result source artifact list does not end with the bundle")
	}
	if (result.TestIntentRepairArtifact == nil) != (result.OriginalTestIntentPlanSHA256 == "") {
		return fmt.Errorf("authoring result test-intent repair binding is incomplete")
	}
	original := result.SourceArtifacts[0].LLMCallReceipt
	isV5 := original.PromptID == activities.AuthoringPlanPromptIDV5
	// Historical single-response results had no prompt-role summary and retain
	// their existing contract. V5 and all correction chains use full identities.
	if last == 1 && !isV5 {
		if result.TestIntentRepairArtifact != nil {
			return fmt.Errorf("legacy authoring result cannot claim a test-intent correction")
		}
		return nil
	}
	if err := result.BundleArtifact.Validate(result.BundleArtifact.Bucket); err != nil || result.BundleArtifact.LLMCallReceipt != nil {
		return fmt.Errorf("authoring result corrected bundle artifact is invalid")
	}
	requests, sources := map[string]bool{}, map[string]bool{}
	for _, source := range result.SourceArtifacts[:last] {
		if err := source.Validate(result.BundleArtifact.Bucket); err != nil {
			return fmt.Errorf("authoring result correction source artifact is invalid: %w", err)
		}
		if source.WorkflowID != result.BundleArtifact.WorkflowID || source.Producer != result.BundleArtifact.Producer {
			return fmt.Errorf("authoring result correction ancestry belongs to a different workflow or producer")
		}
		receipt := source.LLMCallReceipt
		if receipt.SchemaVersion != 1 || strings.TrimSpace(receipt.RequestedModel) == "" || receipt.Provider != source.Provider || !authoringWorkflowIsSHA256V1(receipt.EndpointID) || !authoringWorkflowIsSHA256V1(receipt.PromptHash) {
			return fmt.Errorf("authoring result correction source receipt lacks complete provider identity")
		}
		if receipt.RequestedModel != original.RequestedModel || receipt.Provider != original.Provider || receipt.EndpointID != original.EndpointID {
			return fmt.Errorf("authoring result correction changed its provider route")
		}
		if requests[receipt.RequestSHA256] || sources[source.SHA256] || source.SHA256 == result.BundleSHA256 {
			return fmt.Errorf("authoring result correction repeats a source or request identity")
		}
		requests[receipt.RequestSHA256], sources[source.SHA256] = true, true
	}
	if !isV5 {
		if last != 2 || result.TestIntentRepairArtifact != nil {
			return fmt.Errorf("legacy authoring result has an unsupported correction chain")
		}
		final := result.SourceArtifacts[1].LLMCallReceipt
		valid := (original.PromptID == activities.AuthoringPlanPromptIDV2 && final.PromptID == activities.AuthoringPlanFormatRepairPromptIDV2 && original.PromptVersion == "v2" && final.PromptVersion == "v2") ||
			(original.PromptID == activities.AuthoringPlanPromptIDV3 && final.PromptID == activities.AuthoringPlanFormatRepairPromptIDV3 && original.PromptVersion == "v3" && final.PromptVersion == "v3") ||
			(original.PromptID == activities.AuthoringPlanPromptIDV4 && final.PromptID == activities.AuthoringPlanFormatRepairPromptIDV4 && original.PromptVersion == "v4" && final.PromptVersion == "v4")
		if !valid {
			return fmt.Errorf("authoring result format-correction source receipt identity is invalid")
		}
		return nil
	}
	if original.PromptVersion != "v5" {
		return fmt.Errorf("authoring result V5 initial source receipt identity is invalid")
	}
	next := 1
	if next < last && result.SourceArtifacts[next].LLMCallReceipt.PromptID == activities.AuthoringPlanFormatRepairPromptIDV5 {
		if result.SourceArtifacts[next].LLMCallReceipt.PromptVersion != "v5" {
			return fmt.Errorf("authoring result V5 format-correction version is invalid")
		}
		next++
	}
	var intent *activities.ArtifactRef
	if next < last && result.SourceArtifacts[next].LLMCallReceipt.PromptID == activities.AuthoringPlanIntentRepairPromptIDV5 {
		intent = result.SourceArtifacts[next]
		if intent.LLMCallReceipt.PromptVersion != "v5" {
			return fmt.Errorf("authoring result V5 test-intent correction version is invalid")
		}
		next++
	}
	if next != last {
		return fmt.Errorf("authoring result V5 correction roles are unknown, repeated, or out of order")
	}
	if result.TestIntentRepairArtifact != nil {
		if intent == nil || !reflect.DeepEqual(intent, result.TestIntentRepairArtifact) {
			return fmt.Errorf("authoring result test-intent repair artifact does not match its source chain")
		}
		if !authoringWorkflowIsSHA256V1(result.OriginalTestIntentPlanSHA256) {
			return fmt.Errorf("authoring result original test-intent plan hash is invalid")
		}
	}
	// Both optional summary fields are absent in old activity histories. Their
	// source roles remain checkable; CAS readers validate the actual bundle's
	// test-intent repair binding before using its spec or plan.
	return nil
}
