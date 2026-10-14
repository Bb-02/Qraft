package activities

import (
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/google/uuid"
)

func TestS3TrainingMaterializationKeepsStrictPassContract(t *testing.T) {
	audit := trainingMaterializationAuditFixtureV1(t, "", 8)
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, false); err != nil {
		t.Fatalf("old strict PASS contract was rejected: %v", err)
	}
	if audit.Policy != "" || len(audit.Warnings) != 0 {
		t.Fatalf("strict audit acquired training semantics: %+v", audit)
	}
	for index := range audit.GateResults {
		if audit.GateResults[index].Gate == qualitygate.GateHiddenRegression {
			audit.GateResults[index].Status = qualitygate.GateStatusNotConfigured
		}
	}
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, false); err == nil {
		t.Fatal("strict materialization accepted an unexecuted hidden gate")
	}
}

func TestS3TrainingMaterializationAcceptsAdvisoryDifficultyAndUnconfiguredHidden(t *testing.T) {
	audit := trainingMaterializationAuditFixtureV1(t, qualitygate.PolicyProblemSetTraining, 8)
	if audit.Decision != qualitygate.DecisionPass || len(audit.Warnings) != 1 {
		t.Fatalf("training audit was not an actual server-computed PASS: %+v", audit)
	}
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, false); err != nil {
		t.Fatalf("training PASS was rejected: %v", err)
	}
	for index := range audit.GateResults {
		if audit.GateResults[index].Gate == qualitygate.GateSpecLint {
			audit.GateResults[index].Status = qualitygate.GateStatusNotConfigured
		}
	}
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, false); err == nil {
		t.Fatal("training materialization skipped an obligatory spec gate")
	}
}

func TestS3TrainingMaterializationPreservesQuarantineUntilExplicitReview(t *testing.T) {
	audit := trainingMaterializationAuditFixtureV1(t, qualitygate.PolicyProblemSetTraining, 6)
	if audit.Decision != qualitygate.DecisionQuarantine || len(audit.BlockingIssues) != 1 {
		t.Fatalf("real correctness failure lost its quarantine: %+v", audit)
	}
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, false); err == nil {
		t.Fatal("quarantined audit was accepted as an automatic PASS")
	}
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, true); err != nil {
		t.Fatalf("complete review candidate could not be saved with its original audit: %v", err)
	}
	audit.GateResults = audit.GateResults[:len(audit.GateResults)-1]
	if err := validateS3MaterializationAuditV1(audit, audit.SubjectID, audit.SubjectRevision, true); err == nil {
		t.Fatal("an incomplete review audit was accepted")
	}
}

func TestS3TrainingMaterializationRejectsMixedPolicyAndRuleHashes(t *testing.T) {
	strict := trainingMaterializationAuditFixtureV1(t, "", 8)
	training := trainingMaterializationAuditFixtureV1(t, qualitygate.PolicyProblemSetTraining, 8)
	training.RuleSHA256 = strict.RuleSHA256
	if err := validateS3MaterializationAuditV1(training, training.SubjectID, training.SubjectRevision, false); err == nil {
		t.Fatal("training policy accepted a strict rule hash")
	}
	training = trainingMaterializationAuditFixtureV1(t, qualitygate.PolicyProblemSetTraining, 6)
	training.RuleSHA256 = strict.RuleSHA256
	if err := validateS3MaterializationAuditV1(training, training.SubjectID, training.SubjectRevision, true); err == nil {
		t.Fatal("review candidate bypassed its policy/rule binding")
	}
	strict.RuleSHA256 = trainingMaterializationAuditFixtureV1(t, qualitygate.PolicyProblemSetTraining, 8).RuleSHA256
	if err := validateS3MaterializationAuditV1(strict, strict.SubjectID, strict.SubjectRevision, false); err == nil {
		t.Fatal("strict policy accepted a training rule hash")
	}
}

func TestS3TrainingStoreParametersRetainRequestedTargetAndContext(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	if policy := s3QualityPolicyForParamsV1(params); policy != "" {
		t.Fatalf("ordinary authoring gained policy %q", policy)
	}
	params.Difficulty = 125
	params.ProblemSetContext = &domain.ProblemSetGenerationContext{SetID: uuid.New(), Position: 1}
	if policy := s3QualityPolicyForParamsV1(params); policy != qualitygate.PolicyProblemSetTraining {
		t.Fatalf("set authoring lost its explicit policy: %q", policy)
	}
	stored := s3QualityDraftStoreParamsV1(params, generationapi.EvidenceMinimal)
	if stored.Difficulty != 125 || !stored.IsProblemSetGeneration() || *stored.ProblemSetContext != *params.ProblemSetContext {
		t.Fatalf("materialization changed the requested target/context: %+v", stored)
	}
	if err := stored.Validate(); err != nil {
		t.Fatalf("stored introductory target cannot traverse later activities: %v", err)
	}
}

func trainingMaterializationAuditFixtureV1(t *testing.T, policy string, correctness int) qualitygate.AuditV1 {
	t.Helper()
	asset := func(role string) qualitygate.EvidenceAssetV1 {
		return qualitygate.EvidenceAssetV1{Ref: "cas://synthetic-materialization/" + role, SHA256: strings.Repeat("a", 64)}
	}
	gate := func(role string) qualitygate.GateEvidenceV1 {
		return qualitygate.GateEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: asset(role), Blockers: []qualitygate.ReviewBlockerV1{}}
	}
	dimension := func(score int, note string) *qualitygate.ReviewDimensionV1 {
		return &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(score), Notes: note}
	}
	difficulty := 8
	if policy == qualitygate.PolicyProblemSetTraining {
		difficulty = 2
	}
	assessment := qualitygate.ReviewAssessmentV1{SchemaVersion: qualitygate.ReviewAssessmentSchemaVersionV1, Approved: qualitygate.BoolV1(true), Blockers: []qualitygate.ReviewBlockerV1{}, Dimensions: &qualitygate.ReviewDimensionsV1{
		Clarity: dimension(8, "clear"), Correctness: dimension(correctness, "actual correctness evidence"), TestCoverage: dimension(8, "covered"), DifficultyCalibration: dimension(difficulty, "difficulty differs from the requested target"), TagAccuracy: dimension(8, "accurate"),
	}}
	_, assessmentSHA, err := qualitygate.CanonicalReviewAssessmentV1(assessment)
	if err != nil {
		t.Fatal(err)
	}
	neighbors := 0
	evidence := qualitygate.S3EvidenceV1{SchemaVersion: qualitygate.S3EvidenceSchemaVersionV1, Policy: policy, SubjectID: generationapi.JobIDPrefix + strings.Repeat("a", 64), SubjectRevision: strings.Repeat("b", 64),
		SpecLint: gate("spec"), SampleOutputBinding: gate("sample"), OracleDifferential: gate("oracle"), Sanitizer: gate("sanitizer"), BoundaryCoverage: gate("boundary"), TestManifest: gate("manifest"),
		Reviewer:         qualitygate.ReviewerEvidenceV1{Evidence: asset("reviewer"), AssessmentSHA256: assessmentSHA, Assessment: assessment},
		Dedup:            qualitygate.DedupEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: asset("dedup"), NeighborCount: &neighbors, Blockers: []qualitygate.ReviewBlockerV1{}},
		HiddenRegression: qualitygate.HiddenRegressionEvidenceV1{Status: qualitygate.GateStatusPass, Evidence: asset("hidden"), Blockers: []qualitygate.ReviewBlockerV1{}},
	}
	if policy == qualitygate.PolicyProblemSetTraining {
		evidence.HiddenRegression.Status = qualitygate.GateStatusNotConfigured
	}
	audit, err := qualitygate.RecomputeVerdictV1(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return audit
}
