import { generationFailureSummary, type GenerationCandidateState, type GenerationFailure, type GenerationFailureSummary } from '@/lib/generation-failure';

export default function GenerationFailureNotice({ error, failure, context = 'set', candidate, summary: suppliedSummary }: { error: string; failure?: GenerationFailure; context?: 'set' | 'workflow'; candidate?: GenerationCandidateState; summary?: GenerationFailureSummary }) {
  const summary = suppliedSummary || generationFailureSummary(error, failure, context, candidate);
  return <div className="space-y-2 text-sm">
    <p className="font-medium">{summary.message}</p>
    <dl className="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-anvil-600 dark:text-anvil-300">
      <dt className="text-anvil-500">失败类型</dt><dd>{summary.category}</dd>
      <dt className="text-anvil-500">发生阶段</dt><dd>{summary.stage}</dd>
      <dt className="text-anvil-500">具体原因</dt><dd>{summary.reason}</dd>
      {summary.suggestion && <><dt className="text-anvil-500">下一步</dt><dd>{summary.suggestion}</dd></>}
    </dl>
    {summary.retryable !== undefined && <p className="text-xs text-anvil-500">{summary.retryable ? '可以重试；若反复出现同类错误，先调整相关设置。' : '直接重复重试通常无效，请先处理上面的具体原因。'}</p>}
    {candidate && <p className="text-xs text-anvil-500">{candidate === 'none' ? '尚未保存可供审核的候选题，需要重新生成。' : candidate === 'incomplete' ? '仅保存了未完成候选，需要检查后重新生成。' : '已保存候选题，可以打开检查；人工通过前仍需核对题意、解法和数据。'}</p>}
    {failure?.blocking_issues && failure.blocking_issues.length > 0 && <ul className="list-disc space-y-1 pl-5">
      {failure.blocking_issues.map((issue, index) => <li key={index}>{issue}</li>)}
    </ul>}
    {summary.technicalDetails && <details className="text-anvil-500 dark:text-anvil-400">
      <summary className="cursor-pointer text-xs">技术详情（排查时使用）</summary>
      <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-anvil-50 p-3 font-mono text-xs dark:bg-anvil-950">{summary.technicalDetails}</pre>
    </details>}
  </div>;
}
