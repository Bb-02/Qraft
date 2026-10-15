package activities

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
)

func reviewerFactsFixtureV4(t *testing.T) (*Activities, *oracleMemoryArtifactStore, *reviewerFormatLLMV2, S3StrictReviewerInputV1) {
	t.Helper()
	provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(reviewerAssessmentJSONV2(t, 5), "stop")}}
	acts, store, in := reviewerFormatFixtureV2(t, provider)
	spec, _, manifest, _ := validIntegerCoverageFixtureV1(t)
	specData, err := json.Marshal(spec)
	require.NoError(t, err)
	specRef := s3SeedArtifactForTest(store, specData, "application/json", "ExtractSemanticSpecArtifactActivityV1")
	main := s3SeedArtifactForTest(store, []byte("// 主解法\nint main() { return 0; }\n"), "text/x-c++src", "GenerateMainSolutionActivityV1")
	oracle := s3SeedArtifactForTest(store, []byte("// independent synthetic solution\nint main() { return 0; }\n"), "text/x-c++src", "GenerateOracleCandidateActivityV1")
	receipt := S3OracleGateReceiptV1{SchemaVersion: S3OracleGateReceiptSchemaV1, Status: "pass", SemanticSpecSHA256: specRef.SHA256, CandidateSHA256: main.SHA256, Promotion: &OraclePromotionReceiptV1{SchemaVersion: OraclePromotionSchemaV1, SemanticSpecSHA256: specRef.SHA256, CandidateSourceSHA256: oracle.SHA256, Promoted: true}}
	receiptData, err := json.Marshal(receipt)
	require.NoError(t, err)
	receiptRef := s3SeedArtifactForTest(store, receiptData, "application/json", "VerifyProgramAgainstIndependentOracleActivityV1")
	for i := range in.VisibleAssets {
		switch in.VisibleAssets[i].Role {
		case "semantic_spec":
			in.VisibleAssets[i].Artifact, in.VisibleAssets[i].ExpectedSHA256 = specRef, specRef.SHA256
		case "oracle_promotion":
			in.VisibleAssets[i].Artifact, in.VisibleAssets[i].ExpectedSHA256 = receiptRef, receiptRef.SHA256
		}
	}
	in.VisibleAssets = append(in.VisibleAssets, S3ReviewerAssetV1{Role: "main_solution", Artifact: main, ExpectedSHA256: main.SHA256}, S3ReviewerAssetV1{Role: "oracle_solution", Artifact: oracle, ExpectedSHA256: oracle.SHA256})
	in.RequestedTags = []string{"arithmetic", "grammar"}
	manifest.SemanticSpecSHA256, manifest.OraclePromotionReceiptSHA256 = specRef.SHA256, receiptRef.SHA256
	setReviewerManifestV4(t, store, &in, manifest)
	return acts, store, provider, in
}

func setReviewerManifestV4(t *testing.T, store *oracleMemoryArtifactStore, in *S3StrictReviewerInputV1, manifest TestManifestV2) {
	t.Helper()
	data, _, err := CanonicalTestManifestV2JSON(manifest)
	require.NoError(t, err)
	in.TestManifestArtifact = s3SeedArtifactForTest(store, data, "application/json", "BuildS3TestManifestActivityV1")
}

func reviewerFactsRequestV4(t *testing.T, provider *reviewerFormatLLMV2) s3ReviewerFactsV4 {
	t.Helper()
	require.Len(t, provider.requests, 1)
	var facts s3ReviewerFactsV4
	require.NoError(t, json.Unmarshal([]byte(provider.requests[0].Messages[0].Content), &facts))
	return facts
}

func TestS3ReviewerFactsV4ContainsVerifiedReadableProgramsTagsAndCompleteCases(t *testing.T) {
	acts, store, provider, in := reviewerFactsFixtureV4(t)
	result, err := executeReviewerFormatV2(t, acts, in)
	require.NoError(t, err)
	facts := reviewerFactsRequestV4(t, provider)
	require.Equal(t, "algoforge.s3-review-facts.v4", facts.SchemaVersion)
	require.Equal(t, "verified", facts.ProgramSourcesStatus)
	require.True(t, facts.RequestedTagsProvided)
	require.Equal(t, in.RequestedTags, facts.RequestedTags)
	require.Len(t, facts.VisibleAssets, 7)
	require.Len(t, facts.Cases, 3)
	for _, asset := range facts.VisibleAssets {
		require.NotNil(t, asset.Content)
		require.False(t, asset.Truncated)
		require.Equal(t, string(store.objects[asset.SHA256]), *asset.Content)
		require.Equal(t, len(*asset.Content), asset.VisibleBytes)
	}
	for _, item := range facts.Cases {
		for _, view := range []s3ReviewerTextV4{item.Input, item.Output} {
			require.NotNil(t, view.Content)
			require.False(t, view.Truncated)
			require.Equal(t, string(store.objects[view.SHA256]), *view.Content)
			require.Equal(t, int64(len(*view.Content)), view.OriginalBytes)
		}
	}
	require.NotContains(t, provider.requests[0].Messages[0].Content, "content_base64")
	require.Contains(t, provider.requests[0].System, "not an executable predicate")
	require.Contains(t, provider.requests[0].System, "Do not claim an unseen value or test is absent")
	require.Equal(t, "s3_strict_reviewer_v4", provider.requests[0].PromptID)
	require.Equal(t, "v4", provider.requests[0].PromptVersion)
	require.False(t, *result.Reviewer.Assessment.Approved)
}

func TestS3ReviewerFactsV4PreservesEveryCaseWithinGlobalPreviewBudget(t *testing.T) {
	acts, store, provider, in := reviewerFactsFixtureV4(t)
	var manifest TestManifestV2
	require.NoError(t, json.Unmarshal(store.objects[in.TestManifestArtifact.SHA256], &manifest))
	base := manifest.Cases[0]
	manifest.Cases = nil
	for i := 0; i < 32; i++ {
		item := base
		item.TestID, item.TestIndex = fmt.Sprintf("case-%06d", i), i
		input := "10000\n" + strings.Repeat("0 ", 9999) + fmt.Sprintf("%d\n", i)
		inputRef := s3SeedArtifactForTest(store, []byte(input), "text/plain", "MaterializeS3CasePlanActivityV1")
		outputRef := s3SeedArtifactForTest(store, []byte("NO\n"), "text/plain", "BuildS3TestManifestActivityV1")
		item.InputArtifact, item.InputSHA256, item.OutputArtifact, item.OutputSHA256 = &inputRef, inputRef.SHA256, &outputRef, outputRef.SHA256
		manifest.Cases = append(manifest.Cases, item)
	}
	manifest.TestCount = len(manifest.Cases)
	setReviewerManifestV4(t, store, &in, manifest)
	_, err := executeReviewerFormatV2(t, acts, in)
	require.NoError(t, err)
	facts := reviewerFactsRequestV4(t, provider)
	require.Len(t, facts.Cases, 32)
	visible := 0
	for i, item := range facts.Cases {
		require.Equal(t, manifest.Cases[i].TestID, item.TestID)
		require.Nil(t, item.Input.Content, "a preview must never be presented as a complete input")
		require.True(t, item.Input.Truncated)
		require.Equal(t, manifest.Cases[i].InputSHA256, item.Input.SHA256)
		require.LessOrEqual(t, item.Input.VisibleBytes, 8<<10)
		require.Less(t, item.Input.VisibleBytes, int(item.Input.OriginalBytes))
		require.True(t, utf8.ValidString(item.Input.PreviewHead+item.Input.PreviewTail))
		require.NotNil(t, item.Output.Content, "short expected outputs keep their reserved full budget")
		require.Equal(t, "NO\n", *item.Output.Content)
		visible += item.Input.VisibleBytes + item.Output.VisibleBytes
	}
	require.Equal(t, visible, facts.CaseTextVisible)
	require.LessOrEqual(t, visible, 256<<10)
	require.LessOrEqual(t, len(provider.requests[0].Messages[0].Content), 2<<20)
}

func TestS3ReviewerFactsV4RejectsHiddenInventedAndMismatchedSourcesBeforeModel(t *testing.T) {
	for _, kind := range []string{"hidden", "incomplete pair", "unverified code", "changed bytes", "missing case", "invalid UTF8", "encoded budget"} {
		t.Run(kind, func(t *testing.T) {
			acts, store, provider, in := reviewerFactsFixtureV4(t)
			switch kind {
			case "hidden":
				ref := s3SeedArtifactForTest(store, []byte("synthetic-hidden-body"), "text/plain", "fixture")
				in.VisibleAssets = append(in.VisibleAssets, S3ReviewerAssetV1{Role: "hidden_regression", Artifact: ref, ExpectedSHA256: ref.SHA256})
			case "incomplete pair":
				in.VisibleAssets = in.VisibleAssets[:len(in.VisibleAssets)-1]
			case "unverified code", "changed bytes":
				for i, asset := range in.VisibleAssets {
					if asset.Role == "main_solution" {
						if kind == "changed bytes" {
							store.objects[asset.Artifact.SHA256] = []byte("tampered synthetic code")
						} else {
							ref := s3SeedArtifactForTest(store, []byte("different synthetic code"), asset.Artifact.ContentType, asset.Artifact.Producer)
							in.VisibleAssets[i].Artifact, in.VisibleAssets[i].ExpectedSHA256 = ref, ref.SHA256
						}
					}
				}
			case "missing case":
				var manifest TestManifestV2
				require.NoError(t, json.Unmarshal(store.objects[in.TestManifestArtifact.SHA256], &manifest))
				delete(store.objects, manifest.Cases[0].InputSHA256)
			case "invalid UTF8", "encoded budget":
				data := []byte{0xff}
				if kind == "encoded budget" {
					data = []byte(`"` + strings.Repeat(`\\`, 600000) + `"`)
				}
				ref := s3SeedArtifactForTest(store, data, "application/json", "fixture")
				for i, asset := range in.VisibleAssets {
					if asset.Role == "final_statement" {
						in.VisibleAssets[i].Artifact, in.VisibleAssets[i].ExpectedSHA256 = ref, ref.SHA256
					}
				}
			}
			_, err := executeReviewerFormatV2(t, acts, in)
			require.Error(t, err)
			require.Empty(t, provider.requests)
		})
	}
}

func TestS3ReviewerFactsV4RejectsUntrustedReceiptContractsBeforeModel(t *testing.T) {
	for _, kind := range []string{"missing gate schema", "wrong gate schema", "missing promotion schema", "wrong promotion schema", "wrong receipt producer"} {
		t.Run(kind, func(t *testing.T) {
			acts, store, provider, in := reviewerFactsFixtureV4(t)
			var manifest TestManifestV2
			require.NoError(t, json.Unmarshal(store.objects[in.TestManifestArtifact.SHA256], &manifest))
			for index, asset := range in.VisibleAssets {
				if asset.Role != "oracle_promotion" {
					continue
				}
				var receipt S3OracleGateReceiptV1
				require.NoError(t, json.Unmarshal(store.objects[asset.Artifact.SHA256], &receipt))
				producer := asset.Artifact.Producer
				switch kind {
				case "missing gate schema":
					receipt.SchemaVersion = ""
				case "wrong gate schema":
					receipt.SchemaVersion = "synthetic.untrusted-gate.v1"
				case "missing promotion schema":
					receipt.Promotion.SchemaVersion = ""
				case "wrong promotion schema":
					receipt.Promotion.SchemaVersion = "synthetic.untrusted-promotion.v1"
				case "wrong receipt producer":
					producer = "SyntheticUntrustedActivity"
				}
				data, err := json.Marshal(receipt)
				require.NoError(t, err)
				ref := s3SeedArtifactForTest(store, data, asset.Artifact.ContentType, producer)
				in.VisibleAssets[index].Artifact, in.VisibleAssets[index].ExpectedSHA256 = ref, ref.SHA256
				manifest.OraclePromotionReceiptSHA256 = ref.SHA256
			}
			// Keep all content hashes and source bindings consistent so rejection
			// specifically exercises the receipt schema and producer contract.
			setReviewerManifestV4(t, store, &in, manifest)
			_, err := executeReviewerFormatV2(t, acts, in)
			require.ErrorContains(t, err, "source pair does not match the manifest's verified oracle receipt")
			require.Empty(t, provider.requests)
		})
	}
}

func TestS3ReviewerFactsV4LegacyAbsentTagsAndExplicitEmptyTagsStayDistinct(t *testing.T) {
	for _, provided := range []bool{false, true} {
		t.Run(fmt.Sprint(provided), func(t *testing.T) {
			provider := &reviewerFormatLLMV2{responses: []*llm.Response{reviewerFormatResponseV2(reviewerAssessmentJSONV2(t, 5), "stop")}}
			acts, _, in := reviewerFormatFixtureV2(t, provider)
			data, err := json.Marshal(in)
			require.NoError(t, err)
			var legacy map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &legacy))
			delete(legacy, "requested_tags")
			data, err = json.Marshal(legacy)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &in))
			if provided {
				in.RequestedTags = []string{}
			}
			_, err = executeReviewerFormatV2(t, acts, in)
			require.NoError(t, err)
			facts := reviewerFactsRequestV4(t, provider)
			require.Equal(t, provided, facts.RequestedTagsProvided)
			require.Empty(t, facts.RequestedTags)
			require.Equal(t, "not_provided", facts.ProgramSourcesStatus)
		})
	}
}

func TestReviewerTextViewV4KeepsUnicodePreviewsValidAndHonest(t *testing.T) {
	data := []byte(strings.Repeat("界", 100))
	ref := manifestV2FixtureArtifact(string(data), "text/plain")
	view, err := reviewerTextViewV4(ref, data, 17)
	require.NoError(t, err)
	require.True(t, view.Truncated)
	require.Nil(t, view.Content)
	require.True(t, utf8.ValidString(view.PreviewHead))
	require.True(t, utf8.ValidString(view.PreviewTail))
	require.LessOrEqual(t, view.VisibleBytes, 17)
	view, err = reviewerTextViewV4(ref, data, 0)
	require.NoError(t, err)
	require.True(t, view.Truncated)
	require.Equal(t, "case_text_budget_exhausted", view.OmittedReason)
}
