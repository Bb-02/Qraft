export interface GenerationDifficultyAssessment {
  target: number;
  estimated?: number;
  delta?: number;
  explanation?: string;
}

export function parseGenerationDifficulty(value: unknown): GenerationDifficultyAssessment | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const fields = value as Record<string, unknown>;
  if (typeof fields.target !== 'number' || !Number.isFinite(fields.target) || fields.target <= 0) return undefined;
  const estimated = typeof fields.estimated === 'number' && Number.isFinite(fields.estimated) && fields.estimated > 0 ? fields.estimated : undefined;
  return {
    target: fields.target,
    estimated,
    delta: estimated === undefined ? undefined : estimated - fields.target,
    explanation: typeof fields.explanation === 'string' ? fields.explanation.trim() : undefined,
  };
}

export function generationDifficultyTone(delta?: number): 'neutral' | 'success' | 'warning' | 'danger' {
  if (delta === undefined || !Number.isFinite(delta)) return 'neutral';
  if (Math.abs(delta) <= 100) return 'success';
  if (Math.abs(delta) <= 300) return 'warning';
  return 'danger';
}

export function generationDifficultyDelta(delta: number): string {
  return delta > 0 ? '+' + delta : String(delta);
}
