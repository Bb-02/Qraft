'use client';

import { useAuth } from '@/components/auth/AuthProvider';
import { useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { Activity, AlertCircle, ArrowUpRight, CheckCircle2, Clock, Loader2, Pause, Plus, XCircle } from 'lucide-react';

import RefreshButton from '@/components/RefreshButton';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import { useWorkflows } from '@/hooks/useWorkflow';
import { cn, formatDate, formatRelativeTime, statusBadgeColor } from '@/lib/utils';
import { DEFAULT_PAGE_SIZE } from '@/lib/constants';
import { normalizeStatus } from '@/lib/status';
import { businessTaskKind, businessTaskStatus, businessTaskStatusLabel, businessTaskTitle, groupBusinessTasks, problemSetRecoveryURL, safeBusinessTaskURL } from '@/lib/business-tasks';
import { generationFailureSummary } from '@/lib/generation-failure';
import type { WorkflowState } from '@/lib/types';

const STATUS_TABS = [
  { value: '', label: '全部任务' },
  { value: 'running', label: '已提交 / 处理中' },
  { value: 'completed', label: '已结束' },
  { value: 'failed', label: '失败' },
  { value: 'cancelled', label: '已取消' },
];

function StatusIcon({ status }: { status: string }) {
  switch (normalizeStatus(status)) {
    case 'running': case 'planning': case 'generating': return <Loader2 className="h-4 w-4 animate-spin text-cyan-500" />;
    case 'queued':
    case 'pending': return <Clock className="h-4 w-4 text-[var(--dm)]" />;
    case 'waiting_review': return <Pause className="h-4 w-4 text-blue-500" />;
    case 'rejected_quarantined': return <Pause className="h-4 w-4 text-amber-500" />;
    case 'approved': return <CheckCircle2 className="h-4 w-4 text-success-500" />;
    case 'failed': return <AlertCircle className="h-4 w-4 text-danger-500" />;
    case 'cancelled': return <XCircle className="h-4 w-4 text-[var(--dm)]" />;
    default: return <Activity className="h-4 w-4 text-[var(--dm)]" />;
  }
}

function TaskEntry({ workflow, nested = false, stale = false }: { workflow: WorkflowState; nested?: boolean; stale?: boolean }) {
  const status = businessTaskStatus(workflow);
  const source = safeBusinessTaskURL(workflow.task?.source_url);
  const result = safeBusinessTaskURL(workflow.task?.result_url);
  const titleURL = result || source;
  const recoveryURL = problemSetRecoveryURL(workflow);
  const failure = workflow.failure_reason || workflow.failure ? generationFailureSummary(workflow.failure_reason || '', workflow.failure, 'workflow') : undefined;
  const position = workflow.task?.position;
  return <article className={'space-y-3 px-5 py-4 ' + (nested ? 'border-t border-[var(--dl)] bg-[var(--dss)]' : '')}>
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-[var(--dm)]"><span>{businessTaskKind(workflow)}</span>{nested && position !== undefined && position > 0 && <span>第 {position} 题</span>}<StatusIcon status={status} /><span className={cn('forge-badge', status === 'partial' ? 'bg-warning-100 text-warning-700' : statusBadgeColor(status))}>{stale ? '上次确认：' : ''}{businessTaskStatusLabel(workflow)}</span></div>
        <h2 className="break-words text-base font-semibold">{titleURL ? <Link className="af-link" href={titleURL}>{businessTaskTitle(workflow)}</Link> : businessTaskTitle(workflow)}</h2>
      </div>
      <div className="flex flex-wrap gap-3 text-sm">
        {source && source !== result && <Link className="af-link inline-flex items-center gap-1" href={source}>打开来源<ArrowUpRight size={14} /></Link>}
        {recoveryURL ? <Link className="af-link inline-flex items-center gap-1" href={recoveryURL}>处理未完成题目<ArrowUpRight size={14} /></Link> : result && <Link className="af-link inline-flex items-center gap-1" href={result}>打开结果<ArrowUpRight size={14} /></Link>}
        <Link className="af-link" href={'/workflows/' + encodeURIComponent(workflow.workflow_id)}>日志与技术详情</Link>
      </div>
    </div>
    {workflow.task?.summary ? <p className="text-sm text-[var(--dm)]">{workflow.task.summary}</p> : !workflow.task && <p className="text-sm text-[var(--dm)]">这条记录没有业务名称与来源信息，可打开日志与技术详情核对。</p>}
    {workflow.task?.completed_count !== undefined && workflow.task.total_count !== undefined && <p className="text-sm text-[var(--dm)]">实际完成 {workflow.task.completed_count} / {workflow.task.total_count}</p>}
    {failure && <p className="line-clamp-2 text-sm text-danger-600">{failure.category}：{failure.reason}</p>}
    <div className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-[var(--dm)]">
      {workflow.start_time && <time dateTime={workflow.start_time} title={formatDate(workflow.start_time)}>提交于 {formatRelativeTime(workflow.start_time)}</time>}
      {workflow.close_time && <time dateTime={workflow.close_time} title={formatDate(workflow.close_time)}>结束于 {formatRelativeTime(workflow.close_time)}</time>}
    </div>
  </article>;
}

export default function WorkflowsPage() {
  const { isAdmin } = useAuth();
  const [statusFilter, setStatusFilter] = useState('');
  const [includeChildren, setIncludeChildren] = useState(false);
  const [scope, setScope] = useState<'mine' | 'accessible'>('mine');
  const [cursors, setCursors] = useState<string[]>(['']);
  const page = cursors.length;
  const cursor = cursors[page - 1];
  const [size, setSize] = useState(DEFAULT_PAGE_SIZE);
  const { workflows, nextCursor, loading, error, refresh, setParams } = useWorkflows({
    cursor, size, status: statusFilter || undefined, scope, include_children: includeChildren,
  });

  useEffect(() => {
    setParams({ cursor, size, status: statusFilter || undefined, scope, include_children: includeChildren });
  }, [cursor, size, statusFilter, scope, includeChildren, setParams]);

  const hasRunning = workflows.some(workflow => ['running', 'queued', 'pending', 'planning', 'generating'].includes(businessTaskStatus(workflow)));
  const taskGroups = groupBusinessTasks(workflows);
  const refreshIntervalRef = useRef<ReturnType<typeof setInterval>>();

  useEffect(() => {
    if (hasRunning) refreshIntervalRef.current = setInterval(() => refresh(), 5000);
    return () => {
      if (refreshIntervalRef.current) clearInterval(refreshIntervalRef.current);
    };
  }, [hasRunning, refresh]);

  return (
    <div className="af-page">
      <PageHeader eyebrow="工作空间" title={isAdmin ? "任务中心" : "我的任务"} description="按题集、题目、导入与评估查看有权限访问的任务，直接返回业务来源和生成结果。"
        actions={<div className="flex gap-2"><Link href="/problem-sets/new" className="forge-btn-primary"><Plus className="h-4 w-4" />生成题集</Link><Link href="/problems/new" className="forge-btn-secondary">创建题目</Link></div>}
      />

      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
        暂时无法读取任务的最新状态，请检查服务连接后重新加载。<button className="ml-3 underline" onClick={refresh}>重新加载</button><details className="mt-2"><summary className="cursor-pointer text-xs">错误详情</summary><pre className="mt-2 whitespace-pre-wrap break-words text-xs">{error}</pre></details>
      </div>}

      <section className="af-table-panel" aria-label="生成任务列表" aria-busy={loading}>
        <div className="af-toolbar justify-between">
          <div className="af-segmented" role="group" aria-label="按任务状态筛选">
            {STATUS_TABS.map((tab) => <button key={tab.value} aria-pressed={statusFilter === tab.value}
              className={statusFilter === tab.value ? 'is-active' : ''}
              onClick={() => { setStatusFilter(tab.value); setCursors(['']); }}>{tab.label}</button>)}
          </div>
          <div className="flex flex-wrap items-center gap-4"><select aria-label="任务查看范围" className="forge-input w-auto" value={scope} onChange={event => { setScope(event.target.value as 'mine' | 'accessible'); setCursors(['']); }}><option value="mine">我发起的任务</option><option value="accessible">可访问的任务（含历史记录）</option></select><label className="flex items-center gap-2 text-sm text-[var(--dm)]"><input type="checkbox" checked={includeChildren} onChange={event => { setIncludeChildren(event.target.checked); setCursors(['']); }} />显示子任务记录</label><RefreshButton onClick={refresh} loading={loading} /></div>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--dl)] px-5 py-3 text-sm text-[var(--dm)]">
          <span>{loading && workflows.length === 0 ? '正在读取任务…' : error ? '暂时无法读取任务数量' : <>本页 <strong className="font-medium text-[var(--dt)]">{taskGroups.length}</strong> 组任务，{workflows.length} 条执行记录</>}</span>
          {hasRunning ? <span className="inline-flex items-center gap-2"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--da)]" />本页任务每 5 秒刷新</span> : <span>选择任务，查看执行与审核详情</span>}
        </div>

        <div className="divide-y divide-[var(--dl)]">
              {loading && workflows.length === 0 ? Array.from({ length: 5 }).map((_, row) => <div key={row} className="p-5"><div className="forge-skeleton h-16 w-full" /></div>)
                : workflows.length === 0 ? <EmptyState icon={Activity}
                  title={error ? '暂时无法查看任务' : statusFilter ? '没有这个状态的任务' : '你的创作任务会显示在这里'}
                  description={error ? '恢复服务连接后，可以继续查看执行记录。' : statusFilter ? '切换状态查看其他任务，或创建一道新题。' : '创建题目后，生成、验算和审核进度都会保存在任务记录里。'}
                  action={error ? <button className="forge-btn-secondary" onClick={refresh}>重新加载</button> : statusFilter ? <button className="forge-btn-secondary" onClick={() => { setStatusFilter(''); setCursors(['']); }}>查看全部任务</button> : <Link href="/problems/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建题目</Link>}
                />
                : taskGroups.map(group => <section key={group.key}>
                  {group.main ? <TaskEntry workflow={group.main} stale={Boolean(error)} /> : <div className="space-y-2 px-5 py-4"><h2 className="font-semibold">子任务记录</h2><p className="text-sm text-[var(--dm)]">{group.parentID ? '主任务不在本页，可打开主任务记录或切换分页查看。' : '这批子任务没有提供主任务信息，请在详情中核对。'}</p>{group.parentID && <Link className="af-link text-sm" href={'/workflows/' + encodeURIComponent(group.parentID)}>打开主任务记录</Link>}</div>}
                  {group.children.length > 0 && <details className="border-t border-[var(--dl)]"><summary className="cursor-pointer px-5 py-3 text-sm text-[var(--dm)]">查看 {group.children.length} 条子任务记录</summary><div>{group.children.map(child => <TaskEntry key={child.workflow_id} workflow={child} nested stale={Boolean(error)} />)}</div></details>}
                </section>)}
        </div>
        <nav className="af-pagination flex flex-wrap items-center justify-between gap-3" aria-label="任务分页">
          <label className="flex items-center gap-2 text-sm text-[var(--dm)]">每页
            <select className="forge-input w-auto" aria-label="每页任务数量" value={size} disabled={loading} onChange={event => { setSize(Number(event.target.value)); setCursors(['']); }}>
              {[10, 20, 50, 100].map(value => <option key={value} value={value}>{value} 条</option>)}
            </select>
          </label>
          <div className="flex items-center gap-3">
            <button className="forge-btn-secondary" disabled={loading || page === 1} onClick={() => setCursors(current => current.slice(0, -1))}>上一页</button>
            <span className="text-sm" aria-live="polite">第 {page} 页</span>
            <button className="forge-btn-secondary" disabled={loading || !!error || !nextCursor} onClick={() => setCursors(current => [...current, nextCursor])}>下一页</button>
          </div>
        </nav>
      </section>
    </div>
  );
}
