import { generationFailureSummary, type GenerationFailure } from '@/lib/generation-failure';

export default function GenerationFailureNotice({ error, failure, context = 'set' }: { error: string; failure?: GenerationFailure; context?: 'set' | 'workflow' }) {
  const summary = generationFailureSummary(error, failure, context);
  return <div className="space-y-2 text-sm">
    <p className="font-medium">{summary.message}</p>
    {summary.suggestion && <p className="text-anvil-600 dark:text-anvil-300">{summary.suggestion}</p>}
    {failure?.blocking_issues && failure.blocking_issues.length > 0 && <ul className="list-disc space-y-1 pl-5">
      {failure.blocking_issues.map((issue, index) => <li key={index}>{issue}</li>)}
    </ul>}
    {summary.technicalDetails && <details className="text-anvil-500 dark:text-anvil-400">
      <summary className="cursor-pointer text-xs">技术详情（排查时使用）</summary>
      <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-anvil-50 p-3 font-mono text-xs dark:bg-anvil-950">{summary.technicalDetails}</pre>
    </details>}
  </div>;
}
