package activities

import qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"

// Only a server-authored V2 set candidate may be stored without a vector index.
// This does not bypass its CAS, code, sample or test-manifest checks.
func (a *Activities) optionalSetEmbeddingV2(input StoreInput) bool {
	return a != nil && a.deps != nil && !a.deps.EmbeddingEnabled &&
		input.PayloadVersion == StoreProblemS3QualityDraftPayloadVersion &&
		input.Params.IsProblemSetGeneration() && input.QualityPassDraft != nil &&
		input.QualityPassDraft.Policy == qualitygate.PolicyProblemSetTrainingV2
}
