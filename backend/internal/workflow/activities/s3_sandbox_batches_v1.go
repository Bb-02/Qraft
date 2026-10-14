package activities

import (
	"fmt"
	"strings"

	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
)

// A common environment is not a combined execution receipt. Batched results
// keep their immutable request identities only in Batches; the returned value
// deliberately omits run/manifest identity for that case.
func s3SandboxRunIdentityV1(run SandboxResult, count int) (SandboxAuditMetadata, error) {
	if run.PayloadVersion != ActivityPayloadVersion || len(run.Outputs) != count || count < 0 {
		return SandboxAuditMetadata{}, fmt.Errorf("sandbox run does not cover the expected cases")
	}
	if len(run.Batches) == 0 {
		if err := validateS3SandboxRequestIdentityV1(run.Audit); err != nil {
			return SandboxAuditMetadata{}, err
		}
		return run.Audit, nil
	}
	if run.Audit != (SandboxAuditMetadata{}) {
		return SandboxAuditMetadata{}, fmt.Errorf("batched sandbox run also claims a single request identity")
	}
	if err := validateTestManifestExecution("S3", TestManifestSandboxIdentity{}, run.Batches, count); err != nil {
		return SandboxAuditMetadata{}, err
	}
	common := run.Batches[0].Audit
	for index, batch := range run.Batches {
		if err := validateS3SandboxRequestIdentityV1(batch.Audit); err != nil {
			return SandboxAuditMetadata{}, fmt.Errorf("sandbox batch %d: %w", index+1, err)
		}
		if !sameS3SandboxIdentityV1(common, batch.Audit) || common.Seed != batch.Audit.Seed {
			return SandboxAuditMetadata{}, fmt.Errorf("sandbox batches ran under different environments, limits, profiles, or seeds")
		}
	}
	common.RunID = ""
	common.ManifestDigest = ""
	return common, nil
}

func validateS3SandboxRequestIdentityV1(audit SandboxAuditMetadata) error {
	if audit.RunID == "" || audit.RunID != strings.TrimSpace(audit.RunID) ||
		audit.ManifestDigest == "" || audit.ManifestDigest != strings.TrimSpace(audit.ManifestDigest) {
		return fmt.Errorf("sandbox run lacks immutable run/manifest identity")
	}
	if !sameS3SandboxIdentityV1(audit, audit) {
		return fmt.Errorf("sandbox run lacks its environment or execution limit identity")
	}
	return nil
}

func validateS3SanitizerExecutionIdentityV1(execution S3SanitizerExecutionV1, verified VerifiedProgramReceiptV1, count int) error {
	if len(execution.CaseIDs) != count || execution.Profile != remotesandbox.SanitizerCAndCPPProfileV1 {
		return fmt.Errorf("sanitizer execution case count or requested profile differs")
	}
	identity, err := s3SandboxRunIdentityV1(SandboxResult{
		PayloadVersion: ActivityPayloadVersion, Outputs: make([]string, count), Audit: execution.Audit, Batches: execution.Batches,
	}, count)
	if err != nil {
		return err
	}
	if identity.Profile != remotesandbox.SanitizerCAndCPPProfileV1 || !strings.HasSuffix(identity.LimitProfile, ",profile="+remotesandbox.SanitizerCAndCPPProfileV1) ||
		identity.ImageDigest != verified.SandboxImageDigest || identity.ToolchainManifestDigest != verified.ToolchainManifestDigest || identity.SeccompPolicyDigest != verified.SeccompPolicyDigest {
		return fmt.Errorf("sanitizer execution does not bind the verified sandbox environment and profile")
	}
	return nil
}
