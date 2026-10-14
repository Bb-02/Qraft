import type { WorkflowState } from './types';
import { normalizeStatus } from './status';

const KIND_LABELS: Record<string, string> = {
  problem_set: '题集生成', problem_set_generation: '题集生成', problem_set_question: '题集子题', problem: '题目生成', problem_generation: '题目生成', quiz: '客观题生成', quiz_generation: '客观题生成',
  problem_import: '题目导入', import: '题目导入', rating: '难度评估', validation: '题目验证', problem_validation: '题目验证',
  gplt_batch: '批量题目生成', batch_generation: '批量题目生成', quiz_batch: '批量客观题生成', task: '其他任务',
};

export function businessTaskKind(workflow: WorkflowState): string {
  return KIND_LABELS[workflow.task?.kind || ''] || '未识别任务';
}

export function businessTaskTitle(workflow: WorkflowState): string {
  return workflow.task?.title?.trim() || (KIND_LABELS[workflow.task?.kind || ''] ? '未提供任务名称' : '未识别任务');
}

export function businessTaskStatus(workflow: WorkflowState): string {
  return normalizeStatus(workflow.task?.status || workflow.status);
}

export function businessTaskStatusLabel(workflow: WorkflowState): string {
  const labels: Record<string, string> = {
    queued: '已排队，尚未开始', pending: '等待处理', planning: '正在规划', generating: '正在生成与检查',
    running: '已提交 / 处理中', waiting_review: '等待审核', awaiting_review: '等待审核', partial: '需要处理',
    completed: '已完成', approved: '已完成', succeeded: '已完成', failed: '失败', cancelled: '已停止', rejected_quarantined: '待人工复核',
  };
  return labels[businessTaskStatus(workflow)] || '状态暂未识别';
}

export function safeBusinessTaskURL(value?: string): string | undefined {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(value)) return undefined;
  return /^\/(?:problem-sets|problems|quizzes|rating)(?:[/?]|$)/.test(value) ? value : undefined;
}

export function isProblemSetTask(workflow: WorkflowState): boolean {
  return workflow.task?.kind === 'problem_set' || workflow.task?.kind === 'problem_set_generation';
}

export function problemSetRecoveryURL(workflow: WorkflowState): string | undefined {
  return isProblemSetTask(workflow) && ['partial', 'failed', 'awaiting_review'].includes(businessTaskStatus(workflow))
    ? safeBusinessTaskURL(workflow.task?.source_url) : undefined;
}

export interface BusinessTaskGroup { key: string; main?: WorkflowState; children: WorkflowState[]; parentID?: string }

// Relationships come from the server. A paginated response may contain a child
// without its parent; preserve that fact rather than inventing a parent title.
export function groupBusinessTasks(workflows: WorkflowState[]): BusinessTaskGroup[] {
  const byID = new Map(workflows.map(workflow => [workflow.workflow_id, workflow]));
  const groups = new Map<string, BusinessTaskGroup>();
  for (const workflow of workflows) {
    const parentID = workflow.task?.parent_workflow_id || workflow.parent_workflow_id;
    if (parentID || workflow.task?.is_child) {
      const key = parentID || 'children-without-parent';
      let group = groups.get(key);
      if (!group) {
        group = { key, main: parentID ? byID.get(parentID) : undefined, children: [], parentID };
        groups.set(key, group);
      }
      group.children.push(workflow);
    } else {
      const existing = groups.get(workflow.workflow_id);
      if (existing) existing.main = workflow;
      else groups.set(workflow.workflow_id, { key: workflow.workflow_id, main: workflow, children: [] });
    }
  }
  for (const group of groups.values()) group.children.sort((a, b) => (a.task?.position || Number.MAX_SAFE_INTEGER) - (b.task?.position || Number.MAX_SAFE_INTEGER));
  return [...groups.values()];
}
