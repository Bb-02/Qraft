package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/google/uuid"
)

func TestS3PartialReviewCandidatePreservesProducedAssetsWithoutInventingOutputs(t *testing.T) {
	fixture := partialReviewCandidateFixtureV1(t)
	problem, solutions, cases, err := fixture.acts.buildS3ReviewCandidateV1(context.Background(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if problem.Status != domain.ProblemStatusQuarantined || problem.Difficulty != 400 || problem.Title != "合成待审题目" || problem.Statement != fixture.finalMarkdown {
		t.Fatalf("partial candidate lost its original statement/target/status: %+v", problem)
	}
	if len(solutions) != 2 || solutions[0].SolutionType != domain.SolutionTypeMain || solutions[1].SolutionType != domain.SolutionTypeBrute || solutions[0].SourceCode != fixture.mainCode || solutions[1].SourceCode != fixture.oracleCode {
		t.Fatalf("produced independent programs were lost: %+v", solutions)
	}
	if len(cases) != 2 || cases[0].InputPath != fixture.plan.Cases[0].InputArtifact.Key || cases[1].InputPath != fixture.plan.Cases[1].InputArtifact.Key {
		t.Fatalf("produced test inputs were lost: %+v", cases)
	}
	if cases[0].OutputPath != fixture.input.OutputArtifacts[0].Key || cases[1].OutputPath != "" || cases[0].Score != 0 || cases[1].Score != 0 {
		t.Fatalf("partial data fabricated a missing output or pass score: %+v", cases)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		t.Fatal(err)
	}
	var candidate struct {
		Complete bool                               `json:"complete"`
		Failure  domain.GenerationFailure           `json:"failure"`
		Context  domain.ProblemSetGenerationContext `json:"context"`
		Assets   map[string]json.RawMessage         `json:"assets"`
	}
	if err := json.Unmarshal(metadata["generation_candidate"], &candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.Complete || candidate.Failure.Message != fixture.input.Failure.Message || candidate.Context != *fixture.input.Params.ProblemSetContext {
		t.Fatalf("partial candidate was represented as complete or lost context: %+v", candidate)
	}
	for _, role := range []string{"authoring_bundle", "statement_draft", "final_statement", "main_program", "oracle_program", "case_plan", "outputs"} {
		if len(candidate.Assets[role]) == 0 {
			t.Fatalf("produced %s was not retained in candidate metadata", role)
		}
	}
	for _, field := range []string{generationapi.S3QualityMaterializationMetadataKey, "quality_pass", "generation_standard_evidence"} {
		if _, exists := metadata[field]; exists {
			t.Fatalf("incomplete candidate fabricated quality evidence %s", field)
		}
	}
	var difficulty domain.GenerationDifficultyAssessment
	if err := json.Unmarshal(metadata["generation_difficulty"], &difficulty); err != nil || difficulty.Target != 400 {
		t.Fatalf("requested target was lost from metadata: %+v err=%v", difficulty, err)
	}
	second, secondSolutions, secondCases, err := fixture.acts.buildS3ReviewCandidateV1(context.Background(), fixture.input)
	if err != nil || second.ID != problem.ID || secondSolutions[0].ID != solutions[0].ID || secondSolutions[1].ID != solutions[1].ID || secondCases[0].ID != cases[0].ID || secondCases[1].ID != cases[1].ID {
		t.Fatalf("retry changed stable candidate/program/test identities: err=%v", err)
	}
}

func TestS3PartialReviewCandidateRejectsForeignWorkflowAssets(t *testing.T) {
	for _, role := range []string{"draft", "main", "oracle", "case_plan", "output", "nested_input"} {
		t.Run(role, func(t *testing.T) {
			fixture := partialReviewCandidateFixtureV1(t)
			foreign := generationapi.JobIDPrefix + strings.Repeat("f", 64)
			switch role {
			case "draft":
				fixture.input.StatementDraftArtifact.WorkflowID = foreign
			case "main":
				fixture.input.MainProgramArtifact.WorkflowID = foreign
			case "oracle":
				fixture.input.OracleProgramArtifact.WorkflowID = foreign
			case "case_plan":
				fixture.input.CasePlanArtifact.WorkflowID = foreign
			case "output":
				fixture.input.OutputArtifacts[0].WorkflowID = foreign
			case "nested_input":
				fixture.plan.Cases[0].InputArtifact.WorkflowID = foreign
				fixture.replacePlan(t)
			}
			if _, _, _, err := fixture.acts.buildS3ReviewCandidateV1(context.Background(), fixture.input); err == nil || !strings.Contains(err.Error(), "workflow") {
				t.Fatalf("candidate did not reject a foreign %s with a workflow binding error: %v", role, err)
			}
		})
	}
}

func TestS3PartialReviewCandidateRequiresServerOwnedSetContext(t *testing.T) {
	fixture := partialReviewCandidateFixtureV1(t)
	fixture.input.Params.ProblemSetContext = nil
	if _, _, _, err := fixture.acts.buildS3ReviewCandidateV1(context.Background(), fixture.input); err == nil {
		t.Fatal("ordinary single-problem parameters entered partial set candidate storage")
	}
}

type partialReviewCandidateFixture struct {
	acts                                *Activities
	store                               *oracleMemoryArtifactStore
	input                               StoreS3ReviewCandidateInputV1
	plan                                S3CasePlanBundleV1
	finalMarkdown, mainCode, oracleCode string
}

func partialReviewCandidateFixtureV1(t *testing.T) *partialReviewCandidateFixture {
	t.Helper()
	store := newOracleMemoryArtifactStore()
	fixture := &partialReviewCandidateFixture{acts: &Activities{artifacts: store}, store: store, finalMarkdown: "# 合成待审题目\n\n读取整数并输出两倍的值。\n", mainCode: "int main(){ /* synthetic main */ }\n", oracleCode: "int main(){ /* synthetic independent oracle */ }\n"}
	workflowID := generationapi.JobIDPrefix + strings.Repeat("8", 64)
	params := domain.DefaultProblemGenParams()
	params.Difficulty = 400
	params.Tags = []string{"simulation"}
	params.ProblemSetContext = &domain.ProblemSetGenerationContext{SetID: uuid.New(), Position: 1}
	fixture.input = StoreS3ReviewCandidateInputV1{PayloadVersion: S3QualityPayloadVersionV1, WorkflowID: workflowID, FrozenConcept: "合成基础练习", Language: "cpp", Params: params, Failure: &domain.GenerationFailure{Code: "test_data_incomplete", Message: "部分测试输出尚未生成，需要检查。"}, DifficultyAssessment: &domain.GenerationDifficultyAssessment{Target: 400}}
	put := func(data []byte, contentType, producer string) *ArtifactRef {
		t.Helper()
		ref, err := store.Put(context.Background(), data, contentType, ArtifactMetadata{WorkflowID: workflowID, Producer: producer})
		if err != nil {
			t.Fatal(err)
		}
		return &ref
	}
	fixture.input.AuthoringBundleArtifact = put([]byte(`{"fixture":"synthetic partial authoring"}`), "application/json", "GenerateAuthoringPlanActivity")
	draftData, _ := json.Marshal(StatementDraftBundleV1{SchemaVersion: StatementDraftBundleSchemaV1, Title: "合成待审题目", Markdown: "合成题面的草稿。", FactManifest: statementFactManifestFromSemanticSpecV1(validOracleSemanticSpecV1(t))})
	fixture.input.StatementDraftArtifact = put(draftData, "application/json", "RenderStatementFromAuthoringBundleActivityV1")
	finalData, _ := json.Marshal(FinalAuthoringStatementSamplesBundleV1{SchemaVersion: FinalAuthoringStatementSamplesSchemaV1, Samples: []AuthoringStatementSampleRecordV1{}, Markdown: fixture.finalMarkdown})
	fixture.input.FinalStatementArtifact = put(finalData, "application/json", "FinalizeAuthoringStatementSamplesActivityV1")
	fixture.input.MainProgramArtifact = put([]byte(fixture.mainCode), "text/plain; charset=utf-8", "GenerateMainSolutionActivityV1")
	fixture.input.OracleProgramArtifact = put([]byte(fixture.oracleCode), "text/plain; charset=utf-8", "GenerateOracleCandidateActivityV1")
	fixture.plan = S3CasePlanBundleV1{SchemaVersion: S3CasePlanSchemaV1, SanitizerCaseIDs: []string{}, Cases: []S3ManifestCasePlanV1{
		{TestID: "case-0", Purpose: TestManifestPurposeSample, BoundaryRefs: []string{}, KilledWrongIDs: []string{}, InputArtifact: *put([]byte("1\n"), "text/plain; charset=utf-8", "GenerateTestDataActivity")},
		{TestID: "case-1", Purpose: TestManifestPurposeBoundary, BoundaryRefs: []string{}, KilledWrongIDs: []string{}, InputArtifact: *put([]byte("2\n"), "text/plain; charset=utf-8", "GenerateTestDataActivity")},
	}}
	fixture.input.OutputArtifacts = []*ArtifactRef{put([]byte("2\n"), "text/plain; charset=utf-8", "RunSandboxActivity"), nil}
	fixture.replacePlan(t)
	return fixture
}

func (f *partialReviewCandidateFixture) replacePlan(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(f.plan)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := f.store.Put(context.Background(), data, "application/json", ArtifactMetadata{WorkflowID: f.input.WorkflowID, Producer: "MaterializeS3CasePlanActivityV1"})
	if err != nil {
		t.Fatal(err)
	}
	f.input.CasePlanArtifact = &ref
}
