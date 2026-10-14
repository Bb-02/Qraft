import Link from 'next/link';
import { ExternalLink, ShieldCheck } from 'lucide-react';
import GenerationFailureNotice from '@/components/workflow/GenerationFailureNotice';
import GenerationDifficultyNotice from '@/components/problem-sets/GenerationDifficultyNotice';
import GenerationProgressNotice from '@/components/problem-sets/GenerationProgressNotice';
import { SET_QUESTION_TYPES, setGenerationStatusLabel } from '@/lib/problem-set-generation';
import type { ProblemSetGenerationSlot } from '@/lib/types';
import { generationProgressTitle } from '@/lib/generation-progress';

export default function GenerationSlotCard({ slot, isAdmin, busy, connectionUnknown, onReview }: {
  slot: ProblemSetGenerationSlot;
  isAdmin: boolean;
  busy: boolean;
  connectionUnknown?: boolean;
  onReview: (slot: ProblemSetGenerationSlot) => void;
}) {
  const needsReview = slot.status === 'failed' || slot.status === 'awaiting_review';
  const statusLabel = slot.status === 'running' && connectionUnknown ? '执行状态待确认' : slot.status === 'running' && slot.execution ? generationProgressTitle(slot.execution) : setGenerationStatusLabel(slot.status);
  const statusTone = slot.status === 'failed' ? 'text-danger-600' : slot.status === 'awaiting_review' ? 'text-warning-700' : slot.status === 'succeeded' ? 'text-success-700' : 'text-anvil-500';
  return <div className="rounded-lg border border-anvil-200 p-3 text-sm dark:border-anvil-700">
    <div className="flex flex-wrap justify-between gap-2"><span>#{slot.position} · {SET_QUESTION_TYPES.find(type => type.type === slot.type)?.label} · {slot.title || '等待规划'}</span><span className={statusTone}>{statusLabel}</span></div>
    {slot.type === 'programming' && <div className="mt-2"><GenerationDifficultyNotice assessment={slot.difficulty_assessment || { target: slot.difficulty }} /></div>}
    {slot.execution && <div className="mt-2"><GenerationProgressNotice progress={connectionUnknown ? { ...slot.execution, state: 'unknown', message: '暂时无法同步这道题的执行状态，正在重新连接。' } : slot.execution} /></div>}
    {(slot.error || slot.failure) && needsReview && <div className="mt-2 text-danger-600"><GenerationFailureNotice error={slot.error || ''} failure={slot.failure} /></div>}
    {slot.regenerate && <p className="mt-2 text-sm text-warning-700">这道候选题需要重新生成，修正需求后重试未完成题目；旧候选仍保留。</p>}
    <div className="mt-2 flex flex-wrap gap-3">
      {slot.problem_id && <Link href={'/problems/' + encodeURIComponent(slot.problem_id)} className="af-link inline-flex items-center gap-1"><ExternalLink size={14} />{slot.status === 'succeeded' ? '查看题目' : '查看和修复候选'}</Link>}
      {slot.quiz_id && <Link href={'/quizzes/' + encodeURIComponent(slot.quiz_id)} className="af-link inline-flex items-center gap-1"><ExternalLink size={14} />查看客观题</Link>}
      {slot.child_id && <Link href={'/workflows/' + encodeURIComponent(slot.child_id)} className="af-link inline-flex items-center gap-1"><ExternalLink size={14} />查看生成任务</Link>}
      {isAdmin && slot.problem_id && needsReview && !slot.regenerate && <button className="af-link inline-flex items-center gap-1 disabled:opacity-50" disabled={busy} onClick={() => onReview(slot)}><ShieldCheck size={14} />人工审核并恢复原题位</button>}
    </div>
  </div>;
}
