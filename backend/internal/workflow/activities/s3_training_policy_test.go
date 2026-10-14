package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
)

type unavailableTrainingHiddenResolver struct{}

func (unavailableTrainingHiddenResolver) ResolveHiddenSuiteV1(context.Context, HiddenSuiteResolutionRequestV1) (HiddenSuiteRefV1, error) {
	return HiddenSuiteRefV1{}, errors.New("synthetic configured registry failure")
}

func TestTrainingHiddenGateDistinguishesNoConfigurationFromConfiguredFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		resolver HiddenSuiteResolverV1
		want     string
	}{
		{name: "unconfigured", want: qualitygate.GateStatusNotConfigured},
		{name: "configured registry failed", resolver: unavailableTrainingHiddenResolver{}, want: qualitygate.GateStatusCheckFailed},
		{name: "configured executor absent", resolver: s3TestHiddenResolver{}, want: qualitygate.GateStatusCheckFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newOracleMemoryArtifactStore()
			acts := New(&Dependencies{ArtifactStore: store, HiddenSuiteResolver: test.resolver})
			resolution, err := acts.ResolveS3HiddenSuiteActivityV1(context.Background(), ResolveS3HiddenSuiteInputV1{PayloadVersion: S3QualityPayloadVersionV1, SubjectID: "synthetic-training", SemanticSpecSHA256: strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
			candidate := s3SeedArtifactForTest(store, []byte("synthetic candidate"), "text/plain", "GenerateMainSolutionActivityV1")
			gate, err := acts.S3HiddenRegressionActivityV1(context.Background(), S3HiddenGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, Resolution: *resolution, CandidateArtifact: candidate, Policy: qualitygate.PolicyProblemSetTraining})
			if err != nil || gate.Gate.Status != test.want {
				t.Fatalf("gate=%+v err=%v want=%s", gate, err, test.want)
			}
			data, err := store.Get(context.Background(), *gate.ReceiptArtifact)
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				NotConfigured bool                                `json:"not_configured"`
				Response      HiddenRegressionExecutionResponseV1 `json:"response"`
			}
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.NotConfigured != (test.want == qualitygate.GateStatusNotConfigured) || receipt.Response.Passed || receipt.Response.ExecutedCount != 0 {
				t.Fatalf("hidden receipt misrepresented execution: %+v", receipt)
			}
		})
	}
}

func TestVerdictActivityRejectsAnEvidenceSuppliedPolicy(t *testing.T) {
	acts := &Activities{}
	_, err := acts.RecomputeS3VerdictActivityV1(context.Background(), S3VerdictInputV1{PayloadVersion: S3QualityPayloadVersionV1, Evidence: qualitygate.S3EvidenceV1{Policy: qualitygate.PolicyProblemSetTraining}})
	if err == nil || !strings.Contains(err.Error(), "server-owned policy") {
		t.Fatalf("evidence bypassed the server's policy: %v", err)
	}
}
