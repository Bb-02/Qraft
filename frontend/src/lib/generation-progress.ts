export interface GenerationExecutionProgress {
  state?: 'queued' | 'waiting_for_worker' | 'planning' | 'generating' | 'running' | 'unknown';
  stage?: string;
  message: string;
  elapsed_seconds: number;
  activity?: string;
  activity_started_at?: string;
  attempt?: number;
  last_heartbeat_at?: string;
  warnings?: string[];
}

export function generationProgressTitle(progress: GenerationExecutionProgress): string {
  if (progress.state === 'unknown') return '暂时无法确认执行状态';
  if (progress.state === 'queued') return '已排队，尚未开始执行';
  if (progress.state === 'waiting_for_worker') return '等待执行服务接单';
  const stage = generationProgressStage(progress);
  if (stage) return stage;
  if (progress.state === 'planning') return '正在规划题集';
  if (progress.state === 'generating') return '正在生成与检查';
  return '正在等待执行服务报告进度';
}

export function generationProgressStage(progress: GenerationExecutionProgress): string {
  if (progress.stage && /[\u3400-\u9fff]/.test(progress.stage)) return progress.stage;
  if (!progress.activity) return '';
  if (progress.activity === 'PrepareProblemSetBatchActivity') return '规划整套题目与题位';
  const label = generationStepLabel(progress.activity);
  return /[\u3400-\u9fff]/.test(label) ? label : '';
}

export function generationElapsedSeconds(progress: GenerationExecutionProgress, now: number, receivedAt: number): number {
  const started = progress.activity_started_at ? Date.parse(progress.activity_started_at) : NaN;
  if (Number.isFinite(started)) return Math.max(0, Math.floor((now - started) / 1000));
  const elapsed = Number.isFinite(progress.elapsed_seconds) ? Math.max(0, progress.elapsed_seconds) : 0;
  return Math.floor(elapsed + Math.max(0, now - receivedAt) / 1000);
}

export function generationWaitLabel(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds));
  if (safe < 60) return safe + ' 秒';
  if (safe < 3600) return Math.floor(safe / 60) + ' 分 ' + safe % 60 + ' 秒';
  return Math.floor(safe / 3600) + ' 小时 ' + Math.floor(safe % 3600 / 60) + ' 分';
}

export interface GenerationStartNotice { message: string; suggestion: string }

// Save only a short classification. Provider errors can include private URLs or
// credentials, so neither the original error nor instance settings enter storage.
export function generationStartNotice(error: string): GenerationStartNotice {
  if (/无法确认.*(?:启动|执行|状态)|(?:执行|服务).*未知|readiness unknown|status unknown/i.test(error)) {
    return { message: '题集已保存，暂时无法确认生成是否已启动。', suggestion: '查看这个题集的生成进度，服务恢复后继续启动；无需重新创建题集。' };
  }
  if (/worker|poller|执行服务|接单|生成服务.*(?:未启动|未就绪|未准备)/i.test(error)) {
    return { message: '题集已保存，但生成执行服务尚未准备好。', suggestion: '请管理员启动生成执行服务，再从这个题集继续启动。' };
  }
  if (/401|403|unauthorized|api[_ ]?key|authentication|模型|密钥/i.test(error)) {
    return { message: '题集已保存，但模型配置尚未通过检查。', suggestion: '检查模型服务地址、访问凭据和使用权限，再从这个题集继续启动。' };
  }
  if (/timeout|deadline|超时|连接|unavailable|network/i.test(error)) {
    return { message: '题集已保存，暂时无法确认生成是否已启动。', suggestion: '查看这个题集的生成进度，服务恢复后继续启动；无需重新创建题集。' };
  }
  return { message: '题集已保存，但自动启动未成功。', suggestion: '查看生成进度中的服务提示，处理后从这个题集继续启动。' };
}

function startNoticeKey(id: string): string { return 'qraft:generation-start-notice:' + id; }

export function saveGenerationStartNotice(storage: Pick<Storage, 'setItem'>, id: string, error: string): void {
  try { storage.setItem(startNoticeKey(id), JSON.stringify(generationStartNotice(error))); } catch { /* The redirect still retains the saved set ID. */ }
}

export function consumeGenerationStartNotice(storage: Pick<Storage, 'getItem' | 'removeItem'>, id: string): GenerationStartNotice {
  try {
    const raw = storage.getItem(startNoticeKey(id));
    storage.removeItem(startNoticeKey(id));
    const value = raw ? JSON.parse(raw) as Partial<GenerationStartNotice> : null;
    if (typeof value?.message === 'string' && typeof value.suggestion === 'string' && value.message.length <= 200 && value.suggestion.length <= 300) return { message: value.message, suggestion: value.suggestion };
  } catch { /* Older services and restricted browser storage use the generic notice. */ }
  return generationStartNotice('');
}
import { generationStepLabel } from '@/lib/generation-failure';
