'use client';

import Link from 'next/link';
import { CheckCircle2, Loader2, RefreshCw } from 'lucide-react';
import { useAuth } from '@/components/auth/AuthProvider';
import type { ProblemSetGenerationReadiness } from '@/lib/types';

interface Props {
  readiness?: ProblemSetGenerationReadiness;
  loading: boolean;
  error: string;
  onRefresh: () => void;
}

export default function GenerationReadinessNotice({ readiness, loading, error, onRefresh }: Props) {
  const { isAdmin } = useAuth();
  const settingsURL = readiness?.blockers.find((issue) => issue.settings_url?.startsWith('/settings?') || issue.settings_url === '/settings')?.settings_url;
  return <section aria-label="生成前检查" className="space-y-3 rounded-lg border border-[var(--dl)] p-4 text-sm">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="font-semibold">生成前检查</h3>
      <button type="button" className="af-link inline-flex items-center gap-1" disabled={loading} onClick={onRefresh}>
        {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}重新检查
      </button>
    </div>
    {loading && <p role="status">正在检查模型与执行服务…</p>}
    {error && <p role="alert" className="text-danger-600">{error}</p>}
    {!loading && !error && readiness && <>
      <p className={readiness.ready ? 'text-success-600' : 'font-medium text-danger-600'}>
        {readiness.ready ? <><CheckCircle2 className="mr-1 inline h-4 w-4" />当前配置已就绪</> : '尚不能开始生成，请先处理以下问题'}
      </p>
      {readiness.effective_roles.length > 0 && <dl className="space-y-1 text-xs text-anvil-500">
        {readiness.effective_roles.map((role) => <div key={role.role} className="flex flex-wrap justify-between gap-1"><dt>{role.label}</dt><dd className="break-all">{role.configured ? role.model || '模型身份待核验' : '尚未配置'}</dd></div>)}
      </dl>}
      {readiness.checks.some((check) => check.status === 'ready') && <ul className="space-y-1 text-xs text-anvil-500">
        {readiness.checks.filter((check) => check.status === 'ready').map((check) => <li key={check.code}><CheckCircle2 className="mr-1 inline h-3 w-3 text-success-600" />{check.message}</li>)}
      </ul>}
      {readiness.blockers.length > 0 && <ul className="list-disc space-y-2 pl-4 text-danger-600">
        {readiness.blockers.map((issue) => <li key={issue.code}>{issue.message}</li>)}
      </ul>}
      {settingsURL && (isAdmin ? <Link className="af-link inline-block" href={settingsURL}>打开模型配置</Link> : <p>请联系管理员完成模型配置。</p>)}
      {readiness.warnings.length > 0 && <ul className="list-disc space-y-2 pl-4 text-warning-700 dark:text-warning-300">
        {readiness.warnings.map((issue) => <li key={issue.code}>{issue.message}</li>)}
      </ul>}
      <p className="af-hint">这里只检查当前配置与服务状态，不调用模型生成题目。实际返回的模型身份和答案仍会在生成时验证。</p>
    </>}
  </section>;
}
