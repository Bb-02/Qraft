import { generationDifficultyDelta, generationDifficultyTone, parseGenerationDifficulty } from '@/lib/generation-difficulty';

const tones = {
  neutral: 'border-anvil-200 text-anvil-600 dark:border-anvil-700 dark:text-anvil-300',
  success: 'border-success-400/30 bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400',
  warning: 'border-warning-400/40 bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400',
  danger: 'border-danger-400/30 bg-danger-50 text-danger-600 dark:bg-danger-500/10 dark:text-danger-400',
};

export default function GenerationDifficultyNotice({ assessment }: { assessment: unknown }) {
  const difficulty = parseGenerationDifficulty(assessment);
  if (!difficulty) return null;
  const tone = generationDifficultyTone(difficulty.delta);
  return <div className={'space-y-2 rounded-lg border p-3 text-sm ' + tones[tone]}>
    <dl className="flex flex-wrap gap-x-6 gap-y-2">
      <div><dt className="text-xs opacity-80">目标难度</dt><dd className="font-medium">CF {difficulty.target}</dd></div>
      <div><dt className="text-xs opacity-80">独立预估</dt><dd className="font-medium">{difficulty.estimated === undefined ? '未评估' : 'CF ' + difficulty.estimated}</dd></div>
      <div><dt className="text-xs opacity-80">预估偏差</dt><dd className="font-medium">{difficulty.delta === undefined ? '暂无' : generationDifficultyDelta(difficulty.delta)}</dd></div>
    </dl>
    {difficulty.explanation && <p className="text-xs">{difficulty.explanation}</p>}
    <p className="text-xs opacity-80">难度预估用于校准；偏差仅作提示，可以据此保留、调整或重新生成。</p>
  </div>;
}
