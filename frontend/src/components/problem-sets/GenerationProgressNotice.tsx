'use client';

import { useEffect, useRef, useState } from 'react';
import { generationElapsedSeconds, generationProgressStage, generationProgressTitle, generationWaitLabel, type GenerationExecutionProgress } from '@/lib/generation-progress';

function readableTime(value: string): string {
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', { hour12: false }) : '暂未记录';
}

export default function GenerationProgressNotice({ progress }: { progress?: GenerationExecutionProgress }) {
  const [now, setNow] = useState(Date.now);
  const receivedAt = useRef(now);
  useEffect(() => {
    receivedAt.current = Date.now();
    setNow(receivedAt.current);
    if (!progress) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [progress]);
  if (!progress) return null;
  const unknown = progress.state === 'unknown';
  const stage = generationProgressStage(progress);
  const waiting = unknown || progress.state === 'queued' || progress.state === 'waiting_for_worker';
  const attempts = progress.attempt && Number.isInteger(progress.attempt) && progress.attempt > 0 ? progress.attempt : undefined;
  const elapsed = unknown ? Math.max(0, Number.isFinite(progress.elapsed_seconds) ? progress.elapsed_seconds : 0) : generationElapsedSeconds(progress, now, receivedAt.current);
  return <section role="status" className={'space-y-2 rounded-lg border p-3 text-sm ' + (waiting ? 'border-warning-400/30 bg-warning-50 dark:bg-warning-500/10' : 'border-forge-300/40 bg-forge-50 dark:bg-forge-500/10')}>
    <p className="font-medium">{generationProgressTitle(progress)}</p>
    {progress.message && <p className="text-anvil-600 dark:text-anvil-300">{progress.message}</p>}
    {unknown && stage && <p className="text-xs text-anvil-500">上次确认步骤：{stage}</p>}
    <dl aria-live="off" className="flex flex-wrap gap-x-5 gap-y-2 text-xs text-anvil-500">
      {progress.activity_started_at && <div><dt>{unknown ? '上次步骤开始于' : '当前步骤开始于'}</dt><dd>{readableTime(progress.activity_started_at)}</dd></div>}
      <div><dt>{unknown ? '上次记录的等待时长' : '已等待'}</dt><dd>{generationWaitLabel(elapsed)}</dd></div>
      {attempts !== undefined && <div><dt>重试次数</dt><dd>{attempts - 1} 次（第 {attempts} 次尝试）</dd></div>}
      {progress.last_heartbeat_at && <div><dt>最近收到反馈</dt><dd>{readableTime(progress.last_heartbeat_at)}</dd></div>}
    </dl>
    {progress.warnings?.map((warning, index) => <p key={index} className="text-warning-700 dark:text-warning-400">{warning}</p>)}
  </section>;
}
