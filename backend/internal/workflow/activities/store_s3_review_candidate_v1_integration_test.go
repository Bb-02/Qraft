//go:build integration

package activities

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStoreS3PartialReviewCandidatePersistsIdempotentlyIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_INTEGRATION_DISPOSABLE_DB") != "true" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("requires an explicitly disposable migrated database")
	}
	ctx := context.Background()
	pool := storeIntegrationPool(t)
	problems := repository.NewProblemRepository(pool)
	tests := repository.NewTestCaseRepository(pool)
	fixture := partialReviewCandidateFixtureV1(t)
	// Every test execution has its own identity and only synthetic CAS bytes.
	workflowID := generationapi.JobIDPrefix + strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	fixture.input.WorkflowID = workflowID
	for _, ref := range []*ArtifactRef{fixture.input.AuthoringBundleArtifact, fixture.input.StatementDraftArtifact,
		fixture.input.FinalStatementArtifact, fixture.input.MainProgramArtifact, fixture.input.OracleProgramArtifact,
		fixture.input.OutputArtifacts[0]} {
		ref.WorkflowID = workflowID
	}
	for i := range fixture.plan.Cases {
		fixture.plan.Cases[i].InputArtifact.WorkflowID = workflowID
	}
	fixture.replacePlan(t)
	fixture.acts.deps = &Dependencies{ProblemRepo: problems, ArtifactStore: fixture.store}
	var id uuid.UUID
	var originalMetadata json.RawMessage
	for attempt := 0; attempt < 3; attempt++ {
		result, err := fixture.acts.StoreS3ReviewCandidateActivityV1(ctx, fixture.input)
		require.NoError(t, err)
		require.Equal(t, domain.ProblemStatusQuarantined, result.Status)
		currentID, err := uuid.Parse(result.ProblemID)
		require.NoError(t, err)
		if attempt == 0 {
			id = currentID
		} else {
			require.Equal(t, id, currentID)
		}
		problem, err := problems.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, 400, problem.Difficulty)
		require.Equal(t, domain.ProblemStatusQuarantined, problem.Status)
		require.Equal(t, fixture.finalMarkdown, problem.Statement)
		require.Equal(t, workflowID, *problem.WorkflowID)
		if attempt == 0 {
			originalMetadata = append(json.RawMessage(nil), problem.MetadataJSON...)
		} else {
			require.JSONEq(t, string(originalMetadata), string(problem.MetadataJSON))
		}
		var metadata struct {
			Candidate struct {
				Complete bool                                `json:"complete"`
				Failure  *domain.GenerationFailure           `json:"failure"`
				Context  *domain.ProblemSetGenerationContext `json:"context"`
			} `json:"generation_candidate"`
			Difficulty *domain.GenerationDifficultyAssessment `json:"generation_difficulty"`
		}
		require.NoError(t, json.Unmarshal(problem.MetadataJSON, &metadata))
		require.False(t, metadata.Candidate.Complete)
		require.Equal(t, fixture.input.Failure, metadata.Candidate.Failure)
		require.Equal(t, fixture.input.Params.ProblemSetContext, metadata.Candidate.Context)
		require.Equal(t, fixture.input.DifficultyAssessment, metadata.Difficulty)
		solutions, err := problems.ListSolutionsByProblemID(ctx, id)
		require.NoError(t, err)
		require.Len(t, solutions, 2)
		for _, solution := range solutions {
			require.Equal(t, "pending", solution.CompileStatus)
			switch solution.SolutionType {
			case domain.SolutionTypeMain:
				require.Equal(t, fixture.mainCode, solution.SourceCode)
			case domain.SolutionTypeBrute:
				require.Equal(t, fixture.oracleCode, solution.SourceCode)
			default:
				t.Fatalf("unexpected persisted solution role %q", solution.SolutionType)
			}
		}
		cases, err := tests.GetByProblemID(ctx, id)
		require.NoError(t, err)
		require.Len(t, cases, 2)
		for i, testcase := range cases {
			require.Equal(t, i+1, testcase.TestIndex)
			require.Equal(t, fixture.plan.Cases[i].InputArtifact.Key, testcase.InputPath)
			require.Zero(t, testcase.Score)
			require.Zero(t, testcase.GroupID)
		}
		require.True(t, cases[0].IsSample)
		require.Equal(t, fixture.input.OutputArtifacts[0].Key, cases[0].OutputPath)
		require.False(t, cases[1].IsSample)
		require.Empty(t, cases[1].OutputPath, "missing expected output must not become a fabricated answer")
	}
	var problemCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM problems WHERE workflow_id=$1`, workflowID).Scan(&problemCount))
	require.Equal(t, 1, problemCount)
}
