package activities

import (
	"fmt"
	"strings"
)

// The immutable bundle is authoritative. Optional result summaries never
// replace this guard when a consumer reopens the actual CAS payload.
func validateAuthoringIntentBindingV5(bundle *AuthoringPlanBundleV1) error {
	if (bundle.TestIntentRepairArtifact == nil) != (bundle.OriginalTestIntentPlanSHA256 == "") {
		return fmt.Errorf("authoring bundle test-intent repair binding is incomplete")
	}
	if bundle.TestIntentRepairArtifact == nil {
		return nil
	}
	ref := bundle.TestIntentRepairArtifact
	if err := ref.Validate(ref.Bucket); err != nil || ref.Producer != "GenerateAuthoringPlanActivity" || ref.ContentType != "application/json" || ref.LLMCallReceipt == nil {
		return fmt.Errorf("authoring bundle test-intent repair artifact identity is invalid")
	}
	receipt := ref.LLMCallReceipt
	if receipt.SchemaVersion != 1 || receipt.PromptID != AuthoringPlanIntentRepairPromptIDV5 || receipt.PromptVersion != "v5" || strings.TrimSpace(receipt.RequestedModel) == "" || receipt.Provider != ref.Provider ||
		!statementDraftIsSHA256V1(receipt.EndpointID) || !statementDraftIsSHA256V1(receipt.PromptHash) || !statementDraftIsSHA256V1(receipt.RequestSHA256) ||
		!statementDraftIsSHA256V1(bundle.OriginalTestIntentPlanSHA256) || !statementDraftIsSHA256V1(bundle.SourceArtifactSHA256) || !statementDraftIsSHA256V1(bundle.SourceRequestSHA256) ||
		ref.SHA256 == bundle.SourceArtifactSHA256 || receipt.RequestSHA256 == bundle.SourceRequestSHA256 {
		return fmt.Errorf("authoring bundle test-intent repair source receipt identity is invalid")
	}
	if bundle.ModelDecision != AuthoringPlanDecisionAccepted || bundle.SemanticSpec == nil || bundle.AuthoringPlan == nil {
		return fmt.Errorf("authoring bundle test-intent repair requires the preserved accepted candidate")
	}
	currentPlanSHA, err := canonicalJSONSHA256(*bundle.AuthoringPlan)
	if err != nil || currentPlanSHA == bundle.OriginalTestIntentPlanSHA256 {
		return fmt.Errorf("authoring bundle test-intent repair did not create a different plan")
	}
	return nil
}
