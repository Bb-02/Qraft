package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

type manualReleaseApprovalReader interface {
	HasCurrentManualReleaseApproval(context.Context, uuid.UUID, time.Time) (bool, error)
}

type s3ManualReleaseApprovalV1 struct {
	problemID uuid.UUID
	updatedAt time.Time
	approved  bool
}

func (s *ProblemService) HasCurrentManualReleaseApproval(ctx context.Context, id uuid.UUID, expectedUpdatedAt time.Time) (bool, error) {
	if s == nil || s.problemRepo == nil {
		return false, fmt.Errorf("manual release approval service is unavailable")
	}
	return s.problemRepo.HasCurrentManualReleaseApproval(ctx, id, expectedUpdatedAt)
}

func problemS3Decision(p *domain.Problem) string {
	if p == nil {
		return ""
	}
	var metadata map[string]json.RawMessage
	var evidence activities.S3QualityPassDraftEvidenceV1
	if json.Unmarshal(p.MetadataJSON, &metadata) != nil || json.Unmarshal(metadata[generationapi.S3QualityMaterializationMetadataKey], &evidence) != nil {
		return ""
	}
	return evidence.Decision
}

func ensureCompleteGenerationCandidate(p *domain.Problem) error {
	if p == nil {
		return fmt.Errorf("%w: 题目身份不完整", ErrConflict)
	}
	var metadata struct {
		Candidate *struct {
			Complete *bool `json:"complete"`
		} `json:"generation_candidate"`
	}
	if len(p.MetadataJSON) > 0 && json.Unmarshal(p.MetadataJSON, &metadata) != nil {
		return fmt.Errorf("%w: 题目生成记录损坏，请重新验证", ErrConflict)
	}
	if metadata.Candidate != nil && (metadata.Candidate.Complete == nil || !*metadata.Candidate.Complete) {
		return fmt.Errorf("%w: 此候选题尚未生成完整，请先补齐测试数据并完成验证；人工审核不能补齐缺失的文件", ErrConflict)
	}
	return nil
}

func loadProductS3ExportBindingV1(ctx context.Context, source hydroProblemSource, p *domain.Problem, tests []*domain.TestCase, objects HydroObjectReader) (*s3ExportBindingV1, error) {
	if err := ensureCompleteGenerationCandidate(p); err != nil {
		return nil, err
	}
	var approval *s3ManualReleaseApprovalV1
	if problemS3Decision(p) == qualitygate.DecisionQuarantine {
		reader, ok := source.(manualReleaseApprovalReader)
		if !ok {
			return nil, fmt.Errorf("%w: 无法核对当前题目的人工审核记录", ErrConflict)
		}
		approved, err := reader.HasCurrentManualReleaseApproval(ctx, p.ID, p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		approval = &s3ManualReleaseApprovalV1{problemID: p.ID, updatedAt: p.UpdatedAt, approved: approved}
		if !approved {
			return nil, fmt.Errorf("%w: 此题尚未通过当前版本的人工审核，请先核对题目并审核通过", ErrConflict)
		}
	}
	binding, err := loadS3ExportBindingV1(ctx, p, tests, objects, approval)
	if err != nil {
		return nil, err
	}
	if approval != nil {
		// Approval binds the persisted programs too. Verify their actual source
		// against the original CAS so edits cannot borrow an older approval.
		reader, ok := source.(interface {
			GetSolutions(context.Context, uuid.UUID) ([]*domain.Solution, error)
		})
		if !ok {
			return nil, fmt.Errorf("%w: 无法核对已审核程序的源代码", ErrConflict)
		}
		solutions, err := reader.GetSolutions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for role, expected := range map[domain.SolutionType]string{domain.SolutionTypeMain: binding.MainProgramSHA256, domain.SolutionTypeBrute: binding.OracleProgramSHA256} {
			matched := false
			for _, solution := range solutions {
				if solution != nil && solution.ProblemID == p.ID && solution.SolutionType == role && sha256Hex([]byte(solution.SourceCode)) == expected {
					matched = true
					break
				}
			}
			if !matched {
				return nil, fmt.Errorf("%w: 已保存的 %s 程序与审核时的源代码不一致，请重新验证", ErrConflict, role)
			}
		}
		stillApproved, err := source.(manualReleaseApprovalReader).HasCurrentManualReleaseApproval(ctx, p.ID, p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if !stillApproved {
			return nil, fmt.Errorf("%w: 题目内容或审核证据已变化，请刷新并重新审核", ErrConflict)
		}
	}
	return binding, nil
}
