package activities

import (
	"context"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

// The judge contract is selected from the CAS-bound SemanticSpec, never from
// a caller's requested normalization. Raw sandbox bytes remain the evidence.
type s3OracleComparisonContractV2 struct {
	JudgeMode         string
	ComparisonProfile string
}

func s3OracleComparisonForSpecV2(spec domain.SemanticSpecV1) (s3OracleComparisonContractV2, error) {
	if spec.SchemaVersion != domain.SemanticSpecSchemaV1 {
		return s3OracleComparisonContractV2{}, fmt.Errorf("unsupported oracle comparison SemanticSpec schema %q", spec.SchemaVersion)
	}
	judge := spec.Judge
	if judge.Mode != domain.SemanticJudgeExactNormalized || judge.ComparisonProfile != domain.SemanticComparisonTrimTrailingSpaceLFV1 ||
		judge.CheckerReference != "" || judge.CheckerSHA256 != "" || judge.CheckerRuleVersion != "" ||
		spec.AnswerSemantics.MultipleSolutionPolicy == domain.SemanticMultipleSolutionAnyValid {
		return s3OracleComparisonContractV2{}, fmt.Errorf("unsupported oracle judge contract: mode %q, comparison profile %q; a special checker cannot use ordinary output comparison", judge.Mode, judge.ComparisonProfile)
	}
	return s3OracleComparisonContractV2{JudgeMode: judge.Mode, ComparisonProfile: judge.ComparisonProfile}, nil
}

func (contract s3OracleComparisonContractV2) equal(main, oracle string) bool {
	if contract.JudgeMode != domain.SemanticJudgeExactNormalized || contract.ComparisonProfile != domain.SemanticComparisonTrimTrailingSpaceLFV1 {
		return false
	}
	// This is the existing frozen trim-trailing-space-lf-v1 implementation:
	// no token folding, leading-space trimming, numeric conversion or tolerance.
	return normalizeOutput(main) == normalizeOutput(oracle)
}

func (a *Activities) loadS3OracleComparisonV2(ctx context.Context, in S3OracleGateInputV1) (s3OracleComparisonContractV2, error) {
	if err := validateOracleCandidateResultV1(in.OracleCandidate, in.ExpectedSemanticSpecSHA256); err != nil {
		return s3OracleComparisonContractV2{}, fmt.Errorf("oracle comparison source binding: %w", err)
	}
	ref := in.OracleCandidate.SourceArtifacts[0]
	if ref.SHA256 != in.ExpectedSemanticSpecSHA256 || ref.SHA256 != in.OracleCandidate.SourceBinding.SemanticSpecArtifactSHA256 ||
		ref.Producer != "ExtractSemanticSpecArtifactActivityV1" {
		return s3OracleComparisonContractV2{}, fmt.Errorf("oracle comparison SemanticSpec artifact is not the bound standalone specification")
	}
	data, err := a.readS3ArtifactV1(ctx, *ref, in.ExpectedSemanticSpecSHA256, "application/json", maxS3SemanticSpecBytesV1)
	if err != nil {
		return s3OracleComparisonContractV2{}, err
	}
	var spec domain.SemanticSpecV1
	if err := decodeCanonicalSampleClosureJSONV1(data, &spec); err != nil {
		return s3OracleComparisonContractV2{}, fmt.Errorf("oracle comparison SemanticSpec canonical decode: %w", err)
	}
	return s3OracleComparisonForSpecV2(spec)
}

// Missing fields retain the historical byte-exact receipt's meaning. A new
// receipt must state exactly the comparison contract frozen in its real spec.
func validateS3OracleReceiptComparisonV2(spec domain.SemanticSpecV1, judgeMode, profile string) error {
	if judgeMode == "" && profile == "" {
		return nil
	}
	contract, err := s3OracleComparisonForSpecV2(spec)
	if err != nil {
		return err
	}
	if judgeMode != contract.JudgeMode || profile != contract.ComparisonProfile {
		return fmt.Errorf("oracle receipt comparison contract does not match its SemanticSpec")
	}
	return nil
}
