package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestS3OracleComparisonUsesOnlyFrozenTrailingWhitespaceNormalization(t *testing.T) {
	spec := domain.SemanticSpecV1{SchemaVersion: domain.SemanticSpecSchemaV1,
		Judge: domain.SemanticJudgeV1{Mode: domain.SemanticJudgeExactNormalized, ComparisonProfile: domain.SemanticComparisonTrimTrailingSpaceLFV1}}
	contract, err := s3OracleComparisonForSpecV2(spec)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, main, oracle string
		equal              bool
	}{
		{"identical", "42\n", "42\n", true},
		{"final newline", "42\n", "42", true},
		{"per-line trailing space and CRLF", "1 \t\r\n2\t\r\n\n", "1\n2", true},
		{"different integers", "42\n", "41\n", false},
		{"separate numeric tokens", "12\n", "1 2\n", false},
		{"internal spaces", "1  2\n", "1 2\n", false},
		{"internal tab", "1\t2\n", "1 2\n", false},
		{"leading space", " 42\n", "42\n", false},
		{"leading blank line", "\n42\n", "42\n", false},
		{"line count", "1\n2\n", "1 2\n", false},
		{"case-sensitive strings", "YES\n", "yes\n", false},
		{"numeric spelling", "01\n", "1\n", false},
		{"no float tolerance", "1.0\n", "1\n", false},
		{"unicode whitespace", "42\u00a0\n", "42\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.equal, contract.equal(tc.main, tc.oracle))
		})
	}
	require.False(t, (s3OracleComparisonContractV2{}).equal("42", "42"), "an unselected contract cannot authorize comparison")
}

func TestS3OracleComparisonRejectsUnknownProfileAndSpecialChecker(t *testing.T) {
	base := domain.SemanticSpecV1{SchemaVersion: domain.SemanticSpecSchemaV1,
		Judge: domain.SemanticJudgeV1{Mode: domain.SemanticJudgeExactNormalized, ComparisonProfile: domain.SemanticComparisonTrimTrailingSpaceLFV1}}
	for name, mutate := range map[string]func(*domain.SemanticSpecV1){
		"unknown profile": func(spec *domain.SemanticSpecV1) { spec.Judge.ComparisonProfile = "tokens" },
		"missing profile": func(spec *domain.SemanticSpecV1) { spec.Judge.ComparisonProfile = "" },
		"special checker": func(spec *domain.SemanticSpecV1) { spec.Judge.Mode = domain.SemanticJudgeSpecialChecker },
		"checker residue": func(spec *domain.SemanticSpecV1) { spec.Judge.CheckerReference = "cas://checker" },
		"any valid answer": func(spec *domain.SemanticSpecV1) {
			spec.AnswerSemantics.MultipleSolutionPolicy = domain.SemanticMultipleSolutionAnyValid
		},
		"unknown schema": func(spec *domain.SemanticSpecV1) { spec.SchemaVersion = "future" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := base
			mutate(&spec)
			_, err := s3OracleComparisonForSpecV2(spec)
			require.ErrorContains(t, err, "unsupported oracle")
		})
	}
	require.NoError(t, validateS3OracleReceiptComparisonV2(base, "", ""), "historical byte-exact receipts remain readable")
	require.NoError(t, validateS3OracleReceiptComparisonV2(base, base.Judge.Mode, base.Judge.ComparisonProfile))
	require.Error(t, validateS3OracleReceiptComparisonV2(base, base.Judge.Mode, "tokens"))
	require.Error(t, validateS3OracleReceiptComparisonV2(base, "", base.Judge.ComparisonProfile))
}

func TestS3OracleComparisonLoadsTheActualHashBoundStandaloneSpec(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	contract, err := f.acts.loadS3OracleComparisonV2(context.Background(), f.oracleInput)
	require.NoError(t, err)
	require.Equal(t, domain.SemanticJudgeExactNormalized, contract.JudgeMode)
	require.Equal(t, domain.SemanticComparisonTrimTrailingSpaceLFV1, contract.ComparisonProfile)

	input := f.oracleInput
	input.OracleCandidate.SourceArtifacts = append([]*ArtifactRef(nil), input.OracleCandidate.SourceArtifacts...)
	wrongRef := *input.OracleCandidate.SourceArtifacts[0]
	wrongRef.Producer = "CallerSuggestedJudgeActivity"
	input.OracleCandidate.SourceArtifacts[0] = &wrongRef
	_, err = f.acts.loadS3OracleComparisonV2(context.Background(), input)
	require.ErrorContains(t, err, "bound standalone specification")

	for name, rewrite := range map[string]func([]byte) []byte{
		"unknown property": func(data []byte) []byte {
			return append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"normalization":"tokens"}`)...)
		},
		"noncanonical bytes": func(data []byte) []byte { return append(data, '\n') },
		"unsupported real judge": func(data []byte) []byte {
			var spec domain.SemanticSpecV1
			require.NoError(t, json.Unmarshal(data, &spec))
			spec.Judge.ComparisonProfile = "tokens"
			encoded, err := json.Marshal(spec)
			require.NoError(t, err)
			return encoded
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := f.store.Get(context.Background(), f.semanticRef)
			require.NoError(t, err)
			ref := s3BatchSeedArtifactV1(t, f.store, rewrite(data), "application/json", "ExtractSemanticSpecArtifactActivityV1")
			input := f.oracleInput
			input.ExpectedSemanticSpecSHA256 = ref.SHA256
			input.OracleCandidate.SourceBinding.SemanticSpecSHA256 = ref.SHA256
			input.OracleCandidate.SourceBinding.SemanticSpecArtifactSHA256 = ref.SHA256
			input.OracleCandidate.SourceArtifacts = append([]*ArtifactRef(nil), input.OracleCandidate.SourceArtifacts...)
			input.OracleCandidate.SourceArtifacts[0] = &ref
			_, err = f.acts.loadS3OracleComparisonV2(context.Background(), input)
			require.Error(t, err)
		})
	}
	input = f.oracleInput
	input.ExpectedSemanticSpecSHA256 = strings.Repeat("e", 64)
	_, err = f.acts.loadS3OracleComparisonV2(context.Background(), input)
	require.ErrorContains(t, err, "binding")
}

func TestS3ManifestRejectsAReceiptClaimingAComparisonOutsideItsRealSpec(t *testing.T) {
	f := newS3BatchGateFixtureV1(t)
	verified := executeS3BatchTestActivityV1[S3OracleGateResultV1](t, f.acts.VerifyProgramAgainstIndependentOracleActivityV1, f.oracleInput)
	sanitized := executeS3BatchTestActivityV1[S3SanitizerGateResultV1](t, f.acts.RunSanitizerGateActivityV1, S3SanitizerGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, CandidateArtifact: *f.oracleInput.MainCandidate.CandidateSourceArtifact, Language: "cpp",
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, CasePlanArtifact: f.oracleInput.CasePlanArtifact,
		ExpectedSmallSuiteSHA256: f.materialized.SanitizerSuiteSHA256, Limits: f.oracleInput.Limits})
	data, err := f.store.Get(context.Background(), *verified.ReceiptArtifact)
	require.NoError(t, err)
	var receipt S3OracleGateReceiptV1
	require.NoError(t, decodeCanonicalSampleClosureJSONV1(data, &receipt))
	receipt.ComparisonProfile = "tokens"
	data, err = json.Marshal(receipt)
	require.NoError(t, err)
	wrongRef := s3BatchSeedArtifactV1(t, f.store, data, "application/json", VerifiedProgramReceiptProducerV1)
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(f.acts.BuildS3TestManifestActivityV1)
	_, err = env.ExecuteActivity(f.acts.BuildS3TestManifestActivityV1, S3ManifestGateInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, SemanticSpecArtifact: f.semanticRef, AuthoringBundleArtifact: f.authoringRef,
		CasePlanArtifact: f.oracleInput.CasePlanArtifact, MainRun: *verified.MainRun, OracleReceiptArtifact: wrongRef,
		VerifiedProgramReceiptArtifact: *verified.VerifiedProgramReceiptArtifact, SanitizerReceiptArtifact: *sanitized.ReceiptArtifact})
	require.ErrorContains(t, err, "oracle receipt comparison contract")
}
