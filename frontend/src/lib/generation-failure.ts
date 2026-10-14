export interface GenerationFailure {
  code: string;
  message: string;
  suggestion?: string;
  technical_details?: string;
  blocked_gate?: string;
  blocking_issues?: string[];
  category?: string;
  stage?: string;
  reason?: string;
  retryable?: boolean;
}

export interface GenerationFailureSummary {
  message: string;
  suggestion: string;
  technicalDetails: string;
  category: string;
  stage: string;
  reason: string;
  retryable?: boolean;
}

export type GenerationCandidateState = 'saved' | 'incomplete' | 'none';

const FAILURE_CATEGORY_LABELS: Record<string, string> = {
  model_output_format: '模型输出格式错误', model_response_format: '模型输出格式错误', model_authentication: '模型访问配置问题',
  model_rate_limit: '模型额度或限流', service_timeout: '服务响应超时', compile_failed: '参考程序编译错误',
  duplicate_candidate: '题目内容重复', quality_review_required: '自动质量检查未通过', verification_failed: '答案或数据验证问题',
  verification_unavailable: '执行服务连接问题', invalid_difficulty: '生成参数问题',
};

export function generationCandidateState(slot: { problem_id?: string; quiz_id?: string; regenerate?: boolean }): GenerationCandidateState {
  if (!slot.problem_id && !slot.quiz_id) return 'none';
  return slot.regenerate ? 'incomplete' : 'saved';
}

// Old executions only have a nested Temporal error string. Keep them readable
// while new executions provide a server-owned message and a concrete next step.
export function generationFailureSummary(error: string, failure?: GenerationFailure, context: 'set' | 'workflow' = 'set', candidate?: GenerationCandidateState): GenerationFailureSummary {
  const original = error.trim();
  const details = (failure?.technical_details || original).trim();
  const evidence = [original, details, failure?.reason || ''].join('\n');
  let message = '这道题暂未完成。';
  let suggestion = '打开生成任务查看失败阶段，再重试未完成的题目；已经完成的题目会保留。';
  let category = '生成服务错误';
  let stage = '生成流程';
  let reason = '本次生成未完成，具体原因暂时无法归纳；原始记录保留在技术详情中。';
  let retryable = failure?.retryable;
  const authoringFormat = /pars(?:e|ing).*authoring[-_ ]plan response|authoring[-_ ]plan.*(?:unmarshal|invalid json|schema)|InvalidAuthoringPlanResponse|AuthoringPlan(?:Format|Truncated)Error/i.test(evidence);
  const reviewerFormat = /Reviewer(?:Format|Truncated)Error|decode (?:strict reviewer|review assessment)/i.test(evidence);

  if (authoringFormat || reviewerFormat) {
    category = '模型输出格式错误'; stage = authoringFormat ? '设计题意与约束' : '评审题目';
    message = authoringFormat ? '作者模型的回复不符合题目设计所需的格式。' : '评审模型的回复不符合质量评审所需的格式。';
    reason = '模型回复无法按约定的数据结构读取，当前步骤尚未完成。';
    if (/unexpected end|unexpected eof|truncat|max_tokens|(?:finish_reason|stop_reason).*length|回复.*截断/i.test(evidence)) reason = '模型回复在完整数据输出前结束，可能受到输出长度限制或被中途截断。';
    else if (/cannot unmarshal|type mismatch|expected.*(?:string|array|object|scalar)|字段.*类型/i.test(evidence)) reason = '模型回复中的字段类型与要求不一致，例如把文本写成数字，或把列表写成单个值。';
    else if (/unknown (?:field|or non-canonical)|unrecognized field|未知字段/i.test(evidence)) reason = '模型回复包含当前步骤不接受的字段名，无法按约定读取内容。';
    else if (/invalid character|invalid json|malformed json|syntax error|非法.*json/i.test(evidence)) reason = '模型回复不是合法的 JSON 数据，例如混入解释文字、缺少引号或括号。';
    suggestion = '检查' + (authoringFormat ? '出题' : '评审') + '模型是否支持稳定的结构化输出，调整模型或输出长度设置后重试未完成题目；不要仅因这个错误修改目标难度。';
    retryable ??= false;
  } else if (/unauthorized|authentication_error|authentication failed|invalid[_ ](?:api[_ ])?(?:key|token)|(?:http|status(?: code)?|response code)\s*[:=]?\s*(?:401|403)\b|鉴权|认证失败/i.test(evidence)) {
    category = '模型访问配置问题'; stage = '连接模型服务'; reason = '模型服务拒绝了访问凭据或当前模型的使用权限。';
    message = '模型服务没有接受当前访问凭据。';
    suggestion = context === 'workflow'
      ? '请管理员检查模型配置中的服务地址、API Key 和访问权限。原任务重试沿用创建时的配置，修改配置后请新建任务。'
      : '请管理员检查模型配置中的服务地址、API Key 和访问权限，再重试这道题。';
  } else if (/rate.?limit|insufficient_quota|quota.?exceeded|(?:http|status(?: code)?)\s*[:=]?\s*429\b|余额不足|额度不足|限流/i.test(evidence)) {
    category = '模型额度或限流'; stage = '请求模型服务'; reason = '模型服务额度不足，或暂时无法接受这么多请求。';
    message = '模型服务的额度不足或请求过于频繁。';
    suggestion = '检查模型服务余额和并发限制，稍后重试未完成的题目。';
  } else if (/timeout|timed out|deadline exceeded|超时/i.test(evidence)) {
    category = '服务响应超时'; stage = '生成或验证'; reason = '当前步骤超过了允许的等待时间，尚未取得有效结果。';
    message = '生成或验证等待超时。';
    suggestion = '查看生成任务确认超时阶段，检查对应服务后重试；已经保存的结果会保留。';
  } else if (/CompilationError|compilation failed|compile.*(?:failed|error)|编译失败/i.test(evidence)) {
    category = '参考程序编译错误'; stage = '编译参考程序'; reason = '参考程序存在编译错误，还不能用于验证答案。';
    message = '参考程序未能编译通过。';
    suggestion = '打开候选题目检查参考程序；修复编译错误后重新验证，或重新生成这道题。';
  } else if (/DuplicateProblem|ProblemSetSlotRejected|dedup\.duplicate|重复题|重复内容|内容重复/i.test(evidence)) {
    category = '题目内容重复'; stage = '检查题目重复'; reason = '候选题与已有内容重复，尚未加入题集。';
    message = '这道题与已有内容重复，尚未加入题集。';
    suggestion = '查看候选并调整题意，或重试生成一道新题。';
  } else if (/output mismatch|oracle_differential|ValidationFailed|答案.*不一致|输出.*不一致/i.test(evidence)) {
    category = '答案校验不一致'; stage = '比对参考答案'; reason = '参考程序与独立校验程序或预期答案产生了不同输出。';
    message = '参考程序的输出与校验答案不一致。';
    suggestion = '打开候选题目核对题意、参考解法和失败用例，修复后重新验证。';
  } else if (/testdata|test[_ -]cases?|TestManifest|测试数据|资产.*校验/i.test(evidence)) {
    category = '题目文件或测试数据问题'; stage = '检查测试数据与题目文件'; reason = '测试数据或题目文件缺失，或尚未通过完整性与有效性检查。';
    message = '测试数据或题目文件尚未通过完整性检查。';
    suggestion = '打开候选题目补齐或修复测试数据，再重新验证并加入题集。';
  } else if (/QualityNotMet|quality gates?|质量校验|质量门禁|未通过.*复核|QuizQualityNotMet/i.test(evidence)) {
    category = '自动质量检查未通过'; stage = '自动评审与质量检查'; reason = '至少一项自动检查尚未通过，需要结合具体报告核对。';
    message = '自动质量检查暂未通过。';
    suggestion = candidate === 'none' ? '当前没有可供人工审核的候选题。修正生成设置后重试未完成题目。' : candidate === 'incomplete' ? '已保存的候选尚未完整，请先检查并修正，再重新生成。' : '查看未通过项。候选已保存时，管理员可以人工审核；有错误时先修复或重试。';
  } else if (/unavailable|connection refused|connect.*failed|network|fetch failed|ECONN|服务.*不可用|无法连接/i.test(evidence)) {
    category = '执行服务连接问题'; stage = '连接生成或验证服务'; reason = '当前步骤所需的服务没有正常响应。';
    message = '生成所需的服务暂时无法连接。';
    suggestion = '检查模型、去重和沙箱服务的连接状态，再重试未完成的题目。';
  } else if (/[\u3400-\u9fff]/.test(original) && !/workflow execution|activity error|application error|type:|retryable:/i.test(original)) {
    message = original;
    reason = original;
    suggestion = '';
  }

  const generic = failure?.code === 'generation_failed' && !failure.category && !failure.stage && !failure.reason;
  if (!generic && failure?.message?.trim() && /[\u3400-\u9fff]/.test(failure.message) && !/workflow execution|activity error|application error|retryable:/i.test(failure.message)) message = failure.message.trim();
  if (!generic && failure?.suggestion?.trim()) suggestion = failure.suggestion.trim();
  if (failure?.category) category = /[\u3400-\u9fff]/.test(failure.category) ? failure.category.trim() : FAILURE_CATEGORY_LABELS[failure.category] || category;
  if (failure?.stage) {
    const translated = generationStepLabel(failure.stage);
    if (/[\u3400-\u9fff]/.test(translated)) stage = translated;
    else if (/authoring[-_ ]plan|authoring/i.test(failure.stage)) stage = '设计题意与约束';
  }
  if (failure?.reason && /[\u3400-\u9fff]/.test(failure.reason)) reason = failure.reason.trim();
  if (candidate === 'none' && /人工|候选/.test(suggestion) && !authoringFormat && !reviewerFormat) suggestion = '当前没有可供人工审核的候选题。先处理上述原因，再重试未完成题目。';
  return { message, suggestion, category, stage, reason, retryable, technicalDetails: details === message ? '' : details };
}

export function commonGenerationFailure(slots: { status: string; error?: string; failure?: GenerationFailure }[]): { count: number; total: number; summary: GenerationFailureSummary } | undefined {
  const failed = slots.filter(slot => slot.status === 'failed' || slot.status === 'awaiting_review');
  const groups = new Map<string, { count: number; total: number; summary: GenerationFailureSummary }>();
  for (const slot of failed) {
    const summary = generationFailureSummary(slot.error || '', slot.failure);
    if (summary.category === '生成服务错误' || summary.reason.includes('具体原因暂时无法归纳')) continue;
    const key = JSON.stringify([summary.category, summary.stage, summary.reason]);
    const group = groups.get(key);
    if (group) group.count++;
    else groups.set(key, { count: 1, total: failed.length, summary });
  }
  return [...groups.values()].filter(group => group.count >= 2).sort((a, b) => b.count - a.count)[0];
}

const GENERATION_STEP_LABELS: Record<string, string> = {
  resolves3repairpolicyactivityv1: '读取自动修复配置',
  buildcanonicalauthoringbriefactivityv1: '整理出题需求',
  generateauthoringplan: '设计题意与约束',
  generateauthoringplanactivity: '设计题意与约束',
  generates3testdataactivityv1: '生成测试数据',
  generatemainsolutionactivityv1: '生成参考解法',
  extractsemanticspecartifactactivityv1: '检查题意结构',
  materializes3caseplanactivityv1: '整理验证用例',
  builds3speclintgateactivityv1: '检查题意与约束',
  renderstatementfromauthoringbundleactivityv1: '生成题面',
  validateauthoringsamplecandidatesactivityv1: '检查样例',
  generateoraclecandidateactivityv1: '生成独立校验程序',
  verifyprogramagainstindependentoracleactivityv1: '比对参考答案',
  resolves3hiddensuiteactivityv1: '准备额外验证用例',
  runverifiedauthoringsamplesactivityv1: '执行样例验证',
  stageauthoringstatementsamplesactivityv1: '准备题面样例',
  finalizeauthoringstatementsamplesactivityv1: '保存已验证样例',
  builds3sampleoutputgateactivityv1: '核对样例输出',
  runsanitizergateactivityv1: '检查程序运行错误',
  builds3testmanifestactivityv1: '检查测试数据覆盖',
  s3dedupgateactivityv1: '检查题目重复',
  s3strictrevieweractivityv1: '自动评审题目',
  s3hiddenregressionactivityv1: '运行额外验证',
  recomputes3verdictactivityv1: '汇总质量检查',
  stores3qualitydraftactivityv1: '保存候选题目',
  stores3reviewcandidateactivityv1: '保存未完成候选题',
  s3repairrevisionactivityv1: '修复未通过项',
};

export function generationStepLabel(step: string): string {
  return GENERATION_STEP_LABELS[step.replace(/_/g, '').toLowerCase()] || step;
}
