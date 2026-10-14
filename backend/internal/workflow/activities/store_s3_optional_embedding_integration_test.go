//go:build integration

package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.temporal.io/sdk/testsuite"
)

func TestStoreS3TrainingV2WithoutEmbeddingIntegration(t *testing.T) {
	pool := storeIntegrationPool(t)
	workflowID := generationapi.JobIDPrefix + sha256Hex([]byte(uuid.NewString()))
	fixture := newS3QualityDraftMaterializationFixture(t, workflowID)
	input := fixture.input
	input.Params.Difficulty = 400
	input.Params.ProblemSetContext = &domain.ProblemSetGenerationContext{SetID: uuid.New(), Position: 1}
	input.DifficultyAssessment = &domain.GenerationDifficultyAssessment{Target: 400}
	var audit qualitygate.AuditV1
	if err := json.Unmarshal(fixture.auditBytes, &audit); err != nil {
		t.Fatal(err)
	}
	audit.Policy = qualitygate.PolicyProblemSetTrainingV2
	_, ruleSHA, err := qualitygate.CanonicalRuleForPolicyV1(audit.Policy)
	if err != nil {
		t.Fatal(err)
	}
	audit.RuleSHA256 = ruleSHA
	for index := range audit.GateResults {
		if audit.GateResults[index].Gate == qualitygate.GateDedup {
			audit.GateResults[index].Status = qualitygate.GateStatusNotConfigured
		}
	}
	auditBytes, _, err := qualitygate.CanonicalAuditV1(audit)
	if err != nil {
		t.Fatal(err)
	}
	input.AuditArtifact = fixture.put(auditBytes, "application/json", "RecomputeS3VerdictActivityV1")
	const bucket = "fixture"
	server, recorder := newStoreS3Server(t, bucket)
	minioClient, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("synthetic-access", "synthetic-secret", ""), Secure: false, Region: "us-east-1", BucketLookup: minio.BucketLookupPath})
	if err != nil {
		t.Fatal(err)
	}
	acts := New(&Dependencies{ArtifactStore: fixture.store, EmbeddingEnabled: false, Embedding: nil, MinIO: minioClient, MinioBucket: bucket, ProblemRepo: repository.NewProblemRepository(pool), TestCaseRepo: repository.NewTestCaseRepository(pool), VectorRepo: repository.NewVectorRepository(pool), OperationLedger: repository.NewWorkflowOperationRepository(pool), ProviderEffects: repository.NewProviderEffectRepository(pool), ProviderEffectLease: time.Minute})
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.StoreS3QualityDraftActivityV1)
	execute := func() StoreS3QualityDraftResultV1 {
		t.Helper()
		result, err := env.ExecuteActivity(acts.StoreS3QualityDraftActivityV1, input)
		if err != nil {
			t.Fatal(err)
		}
		var stored StoreS3QualityDraftResultV1
		if err := result.Get(&stored); err != nil {
			t.Fatal(err)
		}
		return stored
	}
	first := execute()
	if first.Status != domain.ProblemStatusDraft || first.ProblemID == "" {
		t.Fatalf("optional embedding prevented a trusted draft: %+v", first)
	}
	problemID, err := uuid.Parse(first.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	var status, vectorStatus string
	var difficulty, solutions, cases, vectors, projection int
	if err := pool.QueryRow(context.Background(), `SELECT status,difficulty,metadata_json->'vector_index'->>'status',(SELECT count(*) FROM solutions WHERE problem_id=$1),(SELECT count(*) FROM testcases WHERE problem_id=$1),(SELECT count(*) FROM problem_embeddings WHERE problem_id=$1),COALESCE(vector_dims(embedding),0) FROM problems WHERE id=$1`, problemID).Scan(&status, &difficulty, &vectorStatus, &solutions, &cases, &vectors, &projection); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || difficulty != 400 || vectorStatus != "not_configured" || solutions != 2 || cases != 2 || vectors != 0 || projection != 0 {
		t.Fatalf("stored optional embedding result was incomplete: status=%s target=%d vectorStatus=%s programs=%d tests=%d vectors=%d projection=%d", status, difficulty, vectorStatus, solutions, cases, vectors, projection)
	}
	puts := recorder.putCount()
	second := execute()
	if second != first {
		t.Fatalf("repeat changed the stable result: first=%+v second=%+v", first, second)
	}
	var programsAfter, casesAfter, vectorsAfter int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM solutions WHERE problem_id=$1),(SELECT count(*) FROM testcases WHERE problem_id=$1),(SELECT count(*) FROM problem_embeddings WHERE problem_id=$1)`, problemID).Scan(&programsAfter, &casesAfter, &vectorsAfter); err != nil {
		t.Fatal(err)
	}
	if programsAfter != 2 || casesAfter != 2 || vectorsAfter != 0 || recorder.putCount() != puts {
		t.Fatalf("repeat duplicated side effects: programs=%d tests=%d vectors=%d puts=%d/%d", programsAfter, casesAfter, vectorsAfter, recorder.putCount(), puts)
	}
	for _, policy := range []string{"", qualitygate.PolicyProblemSetTraining} {
		strictFixture := newS3QualityDraftMaterializationFixture(t, generationapi.JobIDPrefix+sha256Hex([]byte(uuid.NewString())))
		strict := strictFixture.input
		if policy != "" {
			strict.Params.ProblemSetContext = input.Params.ProblemSetContext
		}
		var strictAudit qualitygate.AuditV1
		if err := json.Unmarshal(strictFixture.auditBytes, &strictAudit); err != nil {
			t.Fatal(err)
		}
		strictAudit.Policy = policy
		_, strictAudit.RuleSHA256, err = qualitygate.CanonicalRuleForPolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		strictBytes, _, err := qualitygate.CanonicalAuditV1(strictAudit)
		if err != nil {
			t.Fatal(err)
		}
		strict.AuditArtifact = strictFixture.put(strictBytes, "application/json", "RecomputeS3VerdictActivityV1")
		strictActs := New(&Dependencies{ArtifactStore: strictFixture.store, EmbeddingEnabled: false, Embedding: nil, MinIO: minioClient, MinioBucket: bucket, ProblemRepo: repository.NewProblemRepository(pool), TestCaseRepo: repository.NewTestCaseRepository(pool), VectorRepo: repository.NewVectorRepository(pool), OperationLedger: repository.NewWorkflowOperationRepository(pool)})
		strictEnv := suite.NewTestActivityEnvironment()
		strictEnv.RegisterActivity(strictActs.StoreS3QualityDraftActivityV1)
		if _, err := strictEnv.ExecuteActivity(strictActs.StoreS3QualityDraftActivityV1, strict); err == nil || !strings.Contains(err.Error(), "embedding") {
			t.Fatalf("policy %q bypassed required embedding for a fully valid PASS: %v", policy, err)
		}
	}
}
