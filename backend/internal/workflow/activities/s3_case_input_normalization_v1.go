package activities

import (
	"context"
	"encoding/json"
	"fmt"

	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
)

const S3CaseInputNormalizationRuleV1 = "algoforge.s3-case-input-normalization.rules.v1"

// The caller must first obtain this receipt by parsing the original bytes with
// the authoritative SemanticSpec. The parser reparses its canonical bytes and
// requires identical typed bindings; this step never repairs invalid input.
func (a *Activities) storeS3CanonicalCaseInputV1(ctx context.Context, testID, semanticSHA, testDataSHA string, original ArtifactRef, parsed speccontract.ParsedSampleInputV1) (*ArtifactRef, error) {
	canonical := []byte(parsed.CanonicalInput)
	if !isManifestSHA256(parsed.BindingSHA256) || !isManifestSHA256(parsed.InputGrammarSHA256) || parsed.CanonicalSHA256 != sha256Hex(canonical) || parsed.CanonicalSHA256 == original.SHA256 {
		return nil, s3ContractErrorV1("case %q normalization receipt is inconsistent", testID)
	}
	metadata := artifactMetadataFromActivity(ctx)
	metadata.ArtifactType = "qg06_test_input"
	metadata.SourceType = "semantic_spec_input_canonicalization_v1"
	metadata.RetentionClass = "workflow_cas_unreviewed"
	// Distinct source spellings can share a canonical CAS hash. Give each
	// source/case binding its own durable provenance role instead of overwriting
	// another normalization record for that same canonical object.
	metadata.ArtifactRole = "qg06_canonical_input:" + testID + ":" + original.SHA256
	var err error
	metadata.ProvenanceMetadata, err = json.Marshal(map[string]interface{}{
		"normalization_rule_version": S3CaseInputNormalizationRuleV1,
		"test_id":                    testID, "semantic_spec_sha256": semanticSHA,
		"test_data_identity_sha256": testDataSHA,
		"original_input_artifact":   original,
		"canonical_input_sha256":    parsed.CanonicalSHA256,
		"input_parser_rule_version": parsed.ParserRuleVersion,
		"input_grammar_sha256":      parsed.InputGrammarSHA256,
		"binding_sha256":            parsed.BindingSHA256,
	})
	if err != nil {
		return nil, fmt.Errorf("encode case %q normalization provenance: %w", testID, err)
	}
	ref, err := a.putArtifactWithMetadata(ctx, canonical, "text/plain", metadata)
	if err != nil {
		return nil, fmt.Errorf("store case %q canonical input: %w", testID, err)
	}
	if ref.SHA256 != parsed.CanonicalSHA256 || ref.SizeBytes != int64(len(canonical)) {
		return nil, s3ContractErrorV1("case %q canonical input CAS identity mismatch", testID)
	}
	return ref, nil
}

func validateS3CaseInputNormalizationV1(item S3ManifestCasePlanV1) error {
	if item.OriginalInputArtifact == nil {
		if item.InputNormalizationRuleVersion != "" {
			return fmt.Errorf("normalization rule lacks its original input artifact")
		}
		return nil
	}
	if item.InputNormalizationRuleVersion != S3CaseInputNormalizationRuleV1 || item.OriginalInputArtifact.SHA256 == item.InputArtifact.SHA256 || item.InputArtifact.Producer != "MaterializeS3CasePlanActivityV1" || item.InputArtifact.LLMCallReceipt != nil || item.InputArtifact.ContentType != "text/plain" {
		return fmt.Errorf("normalized input lacks a valid server-owned source binding")
	}
	if err := item.OriginalInputArtifact.Validate(item.InputArtifact.Bucket); err != nil {
		return fmt.Errorf("original input artifact is invalid: %w", err)
	}
	return nil
}
