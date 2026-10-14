'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { getProblemSetGenerationReadiness } from './api';
import type { ProblemSetGenerationConfig, ProblemSetGenerationReadiness } from './types';

export function useGenerationReadiness(config: ProblemSetGenerationConfig, enabled: boolean) {
  const [readiness, setReadiness] = useState<ProblemSetGenerationReadiness>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const sequence = useRef(0);
  const invalidate = useCallback(() => { sequence.current++; }, []);
  const mode = config.mode;
  const programmingCount = config.distribution.find((quota) => quota.type === 'programming')?.count ?? 0;

  const refresh = useCallback(async () => {
    if (!enabled) return undefined;
    const requestID = ++sequence.current;
    setLoading(true); setError('');
    try {
      const response = await getProblemSetGenerationReadiness(mode, programmingCount);
      if (!response.data) throw new Error('未取得生成前检查结果，请重新检查。');
      if (requestID === sequence.current) setReadiness(response.data);
      return response.data;
    } catch (cause) {
      if (requestID === sequence.current) {
        setReadiness(undefined);
        setError(cause instanceof Error ? cause.message : '暂时无法检查生成条件，请重新检查。');
      }
      return undefined;
    } finally {
      if (requestID === sequence.current) setLoading(false);
    }
  }, [enabled, mode, programmingCount]);

  useEffect(() => {
    setReadiness(undefined);
    if (enabled) void refresh();
    else { invalidate(); setLoading(false); setError(''); }
    return invalidate;
  }, [enabled, refresh, invalidate]);

  return { readiness, loading, error, refresh, canStart: Boolean(enabled && readiness?.ready && !loading && !error) };
}
