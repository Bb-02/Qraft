package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/google/uuid"
)

// An incomplete candidate is an editable review record, never a quality PASS.
// Keep every produced asset accessible without inventing missing outputs.
type StoreS3ReviewCandidateInputV1 struct {
	PayloadVersion          int                                    `json:"payload_version"`
	WorkflowID              string                                 `json:"workflow_id"`
	FrozenConcept           string                                 `json:"frozen_concept"`
	Language                string                                 `json:"language"`
	Params                  domain.ProblemGenParams                `json:"params"`
	Failure                 *domain.GenerationFailure              `json:"failure"`
	AuthoringBundleArtifact *ArtifactRef                           `json:"authoring_bundle_artifact,omitempty"`
	StatementDraftArtifact  *ArtifactRef                           `json:"statement_draft_artifact,omitempty"`
	FinalStatementArtifact  *ArtifactRef                           `json:"final_statement_artifact,omitempty"`
	MainProgramArtifact     *ArtifactRef                           `json:"main_program_artifact,omitempty"`
	OracleProgramArtifact   *ArtifactRef                           `json:"oracle_program_artifact,omitempty"`
	CasePlanArtifact        *ArtifactRef                           `json:"case_plan_artifact,omitempty"`
	OutputArtifacts         []*ArtifactRef                         `json:"output_artifacts,omitempty"`
	DifficultyAssessment    *domain.GenerationDifficultyAssessment `json:"difficulty_assessment,omitempty"`
}

func (a *Activities) StoreS3ReviewCandidateActivityV1(ctx context.Context, in StoreS3ReviewCandidateInputV1) (*StoreS3QualityDraftResultV1, error) {
	if a == nil || a.deps == nil || a.deps.ProblemRepo == nil {
		return nil, fmt.Errorf("candidate repository is unavailable")
	}
	problem, solutions, cases, err := a.buildS3ReviewCandidateV1(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := a.persistWorkflowOwner(ctx, in.WorkflowID); err != nil {
		return nil, err
	}
	existing, err := a.deps.ProblemRepo.GetByID(ctx, problem.ID)
	if err == nil {
		if existing.WorkflowID == nil || *existing.WorkflowID != in.WorkflowID {
			return nil, fmt.Errorf("candidate identity is already used by another workflow")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if err := a.deps.ProblemRepo.Create(ctx, problem); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	for _, solution := range solutions {
		err := a.deps.ProblemRepo.ExecRaw(ctx, `INSERT INTO solutions(id,problem_id,solution_type,language,source_code,compile_status,created_at) VALUES($1,$2,$3,$4,$5,'pending',$6) ON CONFLICT(id) DO NOTHING`, solution.ID, problem.ID, solution.SolutionType, solution.Language, solution.SourceCode, solution.CreatedAt)
		if err != nil {
			return nil, err
		}
	}
	for _, tc := range cases {
		err := a.deps.ProblemRepo.ExecRaw(ctx, `INSERT INTO testcases(id,problem_id,input_path,output_path,description,is_sample,test_index,group_id,score,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,0,0,$8) ON CONFLICT(id) DO NOTHING`, tc.ID, problem.ID, tc.InputPath, tc.OutputPath, tc.Description, tc.IsSample, tc.TestIndex, tc.CreatedAt)
		if err != nil {
			return nil, err
		}
	}
	return &StoreS3QualityDraftResultV1{PayloadVersion: StoreS3QualityDraftPayloadVersionV1, ProblemID: problem.ID.String(), Status: domain.ProblemStatusQuarantined}, nil
}

func (a *Activities) buildS3ReviewCandidateV1(ctx context.Context, in StoreS3ReviewCandidateInputV1) (*domain.Problem, []domain.Solution, []domain.TestCase, error) {
	if a == nil || a.artifacts == nil || in.PayloadVersion != S3QualityPayloadVersionV1 || !generationapi.IsJobID(in.WorkflowID) || !in.Params.IsProblemSetGeneration() || in.Failure == nil || in.Failure.Message == "" {
		return nil, nil, nil, fmt.Errorf("invalid problem-set review candidate")
	}
	if err := in.Params.Validate(); err != nil {
		return nil, nil, nil, err
	}
	title := strings.TrimSpace(in.FrozenConcept)
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	if title == "" {
		title = "尚未完成的候选题"
	}
	statement := "题面尚未生成完整。请查看生成任务和检查记录，修改需求后重新生成。"
	missing := []string{}
	if in.FinalStatementArtifact != nil {
		data, err := a.reviewCandidateAssetV1(ctx, in.WorkflowID, "final statement", "FinalizeAuthoringStatementSamplesActivityV1", in.FinalStatementArtifact)
		if err != nil {
			return nil, nil, nil, err
		}
		var final FinalAuthoringStatementSamplesBundleV1
		if err := decodeCanonicalSampleClosureJSONV1(data, &final); err != nil {
			return nil, nil, nil, err
		}
		statement = final.Markdown
	}
	if in.StatementDraftArtifact != nil {
		data, err := a.reviewCandidateAssetV1(ctx, in.WorkflowID, "statement draft", "RenderStatementFromAuthoringBundleActivityV1", in.StatementDraftArtifact)
		if err != nil {
			return nil, nil, nil, err
		}
		var draft StatementDraftBundleV1
		if err := decodeCanonicalSampleClosureJSONV1(data, &draft); err != nil {
			return nil, nil, nil, err
		}
		title = draft.Title
		if in.FinalStatementArtifact == nil {
			statement = draft.Markdown
		}
	} else {
		missing = append(missing, "完整题面")
	}
	if in.AuthoringBundleArtifact != nil {
		if _, err := a.reviewCandidateAssetV1(ctx, in.WorkflowID, "authoring bundle", "GenerateAuthoringPlanActivity", in.AuthoringBundleArtifact); err != nil {
			return nil, nil, nil, err
		}
	}
	now := time.Now().UTC()
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("algoforge:quality-review-candidate:"+in.WorkflowID))
	problem := &domain.Problem{ID: id, Title: title, Statement: statement, Level: in.Params.Level, Difficulty: in.Params.Difficulty, Tags: append([]string(nil), in.Params.Tags...), TimeLimit: in.Params.TimeLimit, MemoryLimit: in.Params.MemoryLimit, Source: "algoforge_workflow", Status: domain.ProblemStatusQuarantined, WorkflowID: &in.WorkflowID, CreatedAt: now, UpdatedAt: now}
	solutions := []domain.Solution{}
	for _, item := range []struct {
		typ      domain.SolutionType
		producer string
		ref      *ArtifactRef
	}{{domain.SolutionTypeMain, "GenerateMainSolutionActivityV1", in.MainProgramArtifact}, {domain.SolutionTypeBrute, "GenerateOracleCandidateActivityV1", in.OracleProgramArtifact}} {
		if item.ref == nil {
			missing = append(missing, string(item.typ)+"参考程序")
			continue
		}
		source, err := a.reviewCandidateAssetV1(ctx, in.WorkflowID, string(item.typ)+" program", item.producer, item.ref)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(source) == 0 || len(source) > maxS3QualityDraftSourceBytesV1 {
			return nil, nil, nil, fmt.Errorf("candidate program is empty or too large")
		}
		solutions = append(solutions, domain.Solution{ID: uuid.NewSHA1(id, []byte(string(item.typ))), ProblemID: id, SolutionType: item.typ, Language: in.Language, SourceCode: string(source), CompileStatus: "pending", CreatedAt: now})
	}
	cases := []domain.TestCase{}
	if in.CasePlanArtifact != nil {
		data, err := a.reviewCandidateAssetV1(ctx, in.WorkflowID, "case plan", "MaterializeS3CasePlanActivityV1", in.CasePlanArtifact)
		if err != nil {
			return nil, nil, nil, err
		}
		var plan S3CasePlanBundleV1
		if err := decodeCanonicalSampleClosureJSONV1(data, &plan); err != nil {
			return nil, nil, nil, err
		}
		for index, item := range plan.Cases {
			if err := item.InputArtifact.Validate(item.InputArtifact.Bucket); err != nil {
				return nil, nil, nil, err
			}
			if item.InputArtifact.WorkflowID != in.WorkflowID {
				return nil, nil, nil, fmt.Errorf("candidate input belongs to another workflow")
			}
			if _, err := a.artifacts.Get(ctx, item.InputArtifact); err != nil {
				return nil, nil, nil, err
			}
			output := ""
			if index < len(in.OutputArtifacts) && in.OutputArtifacts[index] != nil {
				ref := in.OutputArtifacts[index]
				if err := ref.Validate(ref.Bucket); err != nil {
					return nil, nil, nil, err
				}
				if ref.WorkflowID != in.WorkflowID {
					return nil, nil, nil, fmt.Errorf("candidate output belongs to another workflow")
				}
				if _, err := a.artifacts.Get(ctx, *ref); err != nil {
					return nil, nil, nil, err
				}
				output = ref.Key
			}
			cases = append(cases, domain.TestCase{ID: uuid.NewSHA1(id, []byte(fmt.Sprintf("case:%d", index))), ProblemID: id, InputPath: item.InputArtifact.Key, OutputPath: output, Description: "待验证：" + item.Purpose, IsSample: item.Purpose == TestManifestPurposeSample, TestIndex: index + 1, CreatedAt: now})
		}
	}
	missing = append(missing, "完整的自动验证报告")
	assets := map[string]interface{}{"authoring_bundle": in.AuthoringBundleArtifact, "statement_draft": in.StatementDraftArtifact, "final_statement": in.FinalStatementArtifact, "main_program": in.MainProgramArtifact, "oracle_program": in.OracleProgramArtifact, "case_plan": in.CasePlanArtifact, "outputs": in.OutputArtifacts}
	meta := map[string]interface{}{"generation_candidate": map[string]interface{}{"complete": false, "failure": in.Failure, "missing_assets": missing, "context": in.Params.ProblemSetContext, "assets": assets}, "review_quarantine": map[string]interface{}{"reason": in.Failure.Message}}
	if in.DifficultyAssessment != nil {
		meta["generation_difficulty"] = in.DifficultyAssessment
	}
	problem.MetadataJSON, _ = json.Marshal(meta)
	if err := problem.Validate(); err != nil {
		return nil, nil, nil, err
	}
	return problem, solutions, cases, nil
}

func (a *Activities) reviewCandidateAssetV1(ctx context.Context, workflowID, name, producer string, ref *ArtifactRef) ([]byte, error) {
	if err := validateS3QualityDraftArtifactRefV1(name, producer, workflowID, *ref); err != nil {
		return nil, err
	}
	return a.artifacts.Get(ctx, *ref)
}
