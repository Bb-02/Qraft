package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

type taskListTemporal struct {
	client.Client
	query string
}

func (m *taskListTemporal) ListWorkflow(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	m.query = req.Query
	return &workflowservice.ListWorkflowExecutionsResponse{Executions: []*workflowpb.WorkflowExecutionInfo{
		{Execution: &commonpb.WorkflowExecution{WorkflowId: "mine"}, Type: &commonpb.WorkflowType{Name: generationapi.QualityWorkflowTypeV1}},
		{Execution: &commonpb.WorkflowExecution{WorkflowId: "child"}, Type: &commonpb.WorkflowType{Name: generationapi.QualityWorkflowTypeV1}, ParentExecution: &commonpb.WorkflowExecution{WorkflowId: "mine"}},
		{Execution: &commonpb.WorkflowExecution{WorkflowId: "teammate"}},
		{Execution: &commonpb.WorkflowExecution{WorkflowId: "ownerless"}},
	}}, nil
}

func TestTaskCenterPersonalScopeSeparatesAdminAccessFromOwnership(t *testing.T) {
	for _, tc := range []struct {
		query    string
		expected []string
		children bool
	}{
		{"scope=mine", []string{"mine"}, false},
		{"scope=mine&include_children=true", []string{"mine", "child"}, true},
		{"scope=accessible", []string{"mine", "teammate", "ownerless"}, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			raw := &taskListTemporal{}
			h := NewWorkflowHandler(raw, "synthetic")
			h.SetWorkflowAccess(service.NewWorkflowAccess(nil, handlerWorkflowOwners{"mine": "me", "child": "me", "teammate": "other"}))
			req := httptest.NewRequest("GET", "/workflows?"+tc.query, nil)
			req = req.WithContext(access.WithPrincipal(req.Context(), access.Principal{UserID: "me", Role: "admin"}))
			rec := httptest.NewRecorder()
			require.NoError(t, h.HandleList(echo.New().NewContext(req, rec)))
			var result struct{ Data []workflowSummary }
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
			var ids []string
			for _, row := range result.Data {
				ids = append(ids, row.WorkflowID)
			}
			require.Equal(t, tc.expected, ids)
			require.Contains(t, raw.query, generationapi.QualityWorkflowTypeV1)
			require.Contains(t, raw.query, "ProblemSetQuizGenerationWorkflow")
			require.Equal(t, !tc.children, strings.Contains(raw.query, "ParentWorkflowId IS NULL"))
			if tc.children {
				require.Equal(t, "mine", result.Data[1].ParentWorkflowID)
			}
		})
	}
}

func TestTaskCenterDoesNotShowInterruptedExecutionAsRunning(t *testing.T) {
	for _, terminal := range []enums.WorkflowExecutionStatus{enums.WORKFLOW_EXECUTION_STATUS_FAILED, enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, enums.WORKFLOW_EXECUTION_STATUS_TERMINATED, enums.WORKFLOW_EXECUTION_STATUS_CANCELED} {
		task := &domain.WorkflowTaskSummary{Status: "generating", Title: "Synthetic set"}
		reconcileTaskExecutionStatus(task, terminal)
		require.Contains(t, []string{"failed", "cancelled"}, task.Status)
		require.Equal(t, "Synthetic set", task.Title)
		require.Contains(t, task.Summary, "已保存的结果")
	}
	for _, businessStatus := range []string{"partial", "awaiting_review"} {
		task := &domain.WorkflowTaskSummary{Status: businessStatus}
		reconcileTaskExecutionStatus(task, enums.WORKFLOW_EXECUTION_STATUS_COMPLETED)
		require.Equal(t, businessStatus, task.Status)
	}
}

func TestTaskCenterRejectsInvalidStatusInsteadOfInterpolatingQuery(t *testing.T) {
	h := NewWorkflowHandler(nil, "synthetic")
	rec := httptest.NewRecorder()
	require.NoError(t, h.HandleList(echo.New().NewContext(httptest.NewRequest("GET", "/workflows?status=bogus", nil), rec)))
	require.Equal(t, 400, rec.Code)
}
