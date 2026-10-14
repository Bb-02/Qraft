package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"go.temporal.io/sdk/testsuite"
)

func TestS3OptionalDedupDistinguishesDisabledFromConfiguredFailures(t *testing.T) {
	for _, test := range []struct {
		name, policy, want string
		enabled            bool
	}{
		{name: "training v2 disabled", policy: qualitygate.PolicyProblemSetTrainingV2, want: qualitygate.GateStatusNotConfigured},
		{name: "training v2 configured dependencies missing", policy: qualitygate.PolicyProblemSetTrainingV2, enabled: true, want: qualitygate.GateStatusCheckFailed},
		{name: "original training disabled remains strict", policy: qualitygate.PolicyProblemSetTraining, want: qualitygate.GateStatusCheckFailed},
		{name: "ordinary strict disabled", want: qualitygate.GateStatusCheckFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newOracleMemoryArtifactStore()
			putJSON := func(value interface{}, producer string) ArtifactRef {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				ref, err := store.Put(context.Background(), data, "application/json", ArtifactMetadata{Producer: producer, WorkflowID: "synthetic-optional-dedup"})
				if err != nil {
					t.Fatal(err)
				}
				return ref
			}
			draft := StatementDraftBundleV1{SchemaVersion: StatementDraftBundleSchemaV1, DocumentStatus: statementDraftDocumentStatusV1, Title: "Synthetic introductory task", Markdown: "Read an integer and double it."}
			draft.MarkdownSHA256 = sha256Hex([]byte(draft.Markdown))
			draftRef := putJSON(draft, "RenderStatementFromAuthoringBundleActivityV1")
			final := FinalAuthoringStatementSamplesBundleV1{SchemaVersion: FinalAuthoringStatementSamplesSchemaV1, Status: FinalAuthoringStatementSamplesStatusV1, StatementDraftSHA256: draftRef.SHA256, Markdown: draft.Markdown, MarkdownSHA256: draft.MarkdownSHA256}
			finalRef := putJSON(final, "FinalizeAuthoringStatementSamplesActivityV1")
			acts := New(&Dependencies{ArtifactStore: store, EmbeddingEnabled: test.enabled, ProvenanceRecorder: &captureProvenanceRecorder{}})
			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(acts.S3DedupGateActivityV1)
			result, err := env.ExecuteActivity(acts.S3DedupGateActivityV1, S3DedupGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, Policy: test.policy, FinalStatementArtifact: finalRef, ExpectedFinalStatementSHA256: finalRef.SHA256, StatementDraftArtifact: draftRef, ExpectedStatementDraftSHA256: draftRef.SHA256, ExpectedMarkdownSHA256: final.MarkdownSHA256})
			if err != nil {
				t.Fatal(err)
			}
			var gate S3DedupGateResultV1
			if err := result.Get(&gate); err != nil {
				t.Fatal(err)
			}
			if gate.Gate.Status != test.want || gate.Gate.NeighborCount != nil {
				t.Fatalf("gate misrepresented configuration/execution: %+v", gate)
			}
			data, err := store.Get(context.Background(), *gate.ReceiptArtifact)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == qualitygate.GateStatusNotConfigured && (!strings.Contains(string(data), "未进行相似题去重") || strings.Contains(string(data), "neighbor_count")) {
				t.Fatalf("receipt fabricated execution: %s", data)
			}
		})
	}
}
