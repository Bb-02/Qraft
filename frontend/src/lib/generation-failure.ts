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
  duplicate_candidate: '题目内容重复', quality_review_required: '自动质量检查未通过', spec_lint_failed: '题意与约束检查未通过', verification_failed: '答案或数据验证问题',
  verification_unavailable: '执行服务连接问题', invalid_difficulty: '生成参数问题',
  verification_request_limit: '执行请求超过服务限制', testdata_generator_empty_output: '测试数据生成器输出为空',
  test_manifest_boundary_refs_invalid: '出题流程内部格式问题', case_plan_input_normalization_failed: '出题流程输入规范化问题',
  statement_presentation_rejected: '题面表达检查未通过',
  oracle_identity_not_independent: '验证来源不够独立',
  unsupported_judge_contract: '流程能力限制',
};

const QUALITY_GATE_FAILURES: Record<string, { category: string; stage: string; reason: string }> = {
  spec_lint: { category: '题意与约束检查未通过', stage: '设计题意与约束', reason: '结构化题意、变量范围或约束关联尚未通过一致性检查。' },
  sample_output_binding: { category: '样例输出检查未通过', stage: '核对样例输出', reason: '样例、题面与参考程序的输出尚未核对一致。' },
  oracle_differential: { category: '独立解法验证未通过', stage: '独立解法验证', reason: '参考解法对比或验证来源独立性尚未通过，请结合具体报告核对；这个状态本身不表示输出一定不一致。' },
  sanitizer: { category: '参考程序运行检查未通过', stage: '检查程序运行错误', reason: '参考程序尚未通过运行与内存安全检查，需要核对程序和失败用例。' },
  boundary_coverage: { category: '测试数据覆盖检查未通过', stage: '检查测试数据覆盖', reason: '测试数据的边界或特殊情况覆盖尚未通过检查。' },
  test_manifest: { category: '测试数据完整性检查未通过', stage: '检查测试数据与题目文件', reason: '测试数据清单及文件的完整性尚未通过校验。' },
  reviewer_schema_verdict: { category: '模型评审需要确认', stage: '自动评审题目', reason: '模型评审提出了需要核对的问题，尚未取得通过结论。' },
  dedup: { category: '题目去重检查未通过', stage: '检查题目重复', reason: '候选可能存在重复内容，或去重检查未能完成，需要核对具体报告。' },
  hidden_regression: { category: '额外验证尚未通过', stage: '运行额外验证', reason: '额外验证未能完成或结果需要检查；这项状态本身不代表题目已被证实有错。' },
};

const INCOMPLETE_CANDIDATE_SUGGESTION = '这道候选题需要重新生成。处理上述失败原因后，重试未完成题目；旧候选仍保留。';
const MANUAL_REVIEW_SUGGESTION = /人工|管理员.*(?:确认|批准|审核|通过)/;

function qualityFailureSuggestion(candidate?: GenerationCandidateState): string {
  return candidate === 'none' ? '当前没有可供人工审核的候选题。修正生成设置后重试未完成题目。'
    : candidate === 'incomplete' ? INCOMPLETE_CANDIDATE_SUGGESTION
      : candidate === 'saved' ? '查看未通过项并检查候选题，先修复存在的问题；完整候选可以由管理员人工审核。'
        : '查看未通过项与生成结果，按对应题位的提示修复或重新生成。';
}

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
  const gate = failure?.code === 'spec_lint_failed' ? 'spec_lint' : failure?.blocked_gate || (/deterministic spec[_ ]lint failed|spec_lint_failed/i.test(evidence) ? 'spec_lint' : undefined);
  const gateFailure = gate ? QUALITY_GATE_FAILURES[gate] : undefined;
  const blockingReasons = failure?.blocking_issues?.filter(issue => /[\u3400-\u9fff]/.test(issue)).map(issue => issue.trim()).filter(Boolean);
  const authoringFormat = /pars(?:e|ing).*authoring[-_ ]plan response|authoring[-_ ]plan.*(?:unmarshal|invalid json|schema)|InvalidAuthoringPlanResponse|AuthoringPlan(?:Format|Truncated)Error/i.test(evidence);
  const reviewerFormat = /Reviewer(?:Format|Truncated)Error|decode (?:strict reviewer|review assessment)/i.test(evidence);
  const testDataFormat = /parsing test[-_ ]?data response|TestData(?:Format|Truncated)Error|test[- ]data response was truncated|test-data JSON format is still invalid after one correction/i.test(evidence);
  const statementFormat = /parse statement narrative response/i.test(evidence);
  const requestLimit = failure?.code === 'verification_request_limit' || /sandbox HTTP\s+(?:400|413)\s+\(invalid_request\):\s*(?:declared batch output budget exceeds \d+ bytes|combined inputs exceed \d+ bytes|input \d+ exceeds \d+ bytes|source exceeds \d+ bytes)/i.test(evidence);
  const emptyGenerator = failure?.code === 'testdata_generator_empty_output' || /generator case \d+ produced empty input/i.test(evidence);
  const presentationRejected = failure?.code === 'statement_presentation_rejected' || /(?:title|narrative) (?:contains normative or meta term|must not introduce normative operators)/i.test(evidence);
  const unsupportedJudge = failure?.code === 'unsupported_judge_contract' || /unsupported oracle judge contract|a special checker cannot use ordinary output comparison/i.test(evidence);
  const manifestBoundaryRefs = failure?.code === 'test_manifest_boundary_refs_invalid' || /invalid TestManifest v2:\s*case "[^"\r\n]+" boundary_refs must be a present sorted unique array/i.test(evidence);
  const casePlanNormalization = failure?.code === 'case_plan_input_normalization_failed' || /materialize S3 case plan|MaterializeS3CasePlanActivityV1/i.test(evidence) && /case "[^"\r\n]+" is not canonical SemanticSpec input:\s*<nil>/i.test(evidence);
  const internalDataContract = (manifestBoundaryRefs || casePlanNormalization) && !authoringFormat && !reviewerFormat;

  if (failure?.code === 'oracle_identity_not_independent') {
    category = '验证来源不够独立'; stage = '独立解法验证';
    message = '独立校验未满足模型与来源独立性要求。';
    reason = '两路验证来源尚未满足当前独立性规则，测试输出一致不能代替这项要求。';
    suggestion = '在模型配置中为独立验证设置第二个实际不同的模型和服务地址，再重试未完成题目。';
    retryable ??= false;
  } else if (testDataFormat || statementFormat) {
    const model = testDataFormat ? '测试数据模型' : '题面模型';
    const structure = testDataFormat ? '测试用例或生成器' : '标题或叙述';
    category = '模型输出格式错误'; stage = testDataFormat ? '生成测试数据' : '编写题面';
    message = model + '的回复不符合所需格式。';
    reason = model + '没有返回约定的完整 JSON 对象，' + structure + '尚未形成有效数据。';
    if (/TestDataTruncatedError|test[- ]data response was truncated|stop_reason\s*=\s*length/i.test(evidence)) reason = '模型输出在结束前被截断，系统无法得到完整的测试数据与生成器结构。';
    else if (/cannot unmarshal|type mismatch|expected.*(?:string|array|object|scalar)/i.test(evidence)) reason = model + '把字段写成了另一种类型，系统无法读取' + structure + '。';
    else if (/unknown (?:field|or non-canonical)|unrecognized field/i.test(evidence)) reason = model + '使用了当前格式不接受的字段名，系统无法读取' + structure + '。';
    else if (/invalid character|invalid json|not a complete json object|unexpected end|unexpected eof/i.test(evidence)) reason = model + '没有返回单个完整、合法的 JSON 对象，可能混入额外文字、缺少引号或括号。';
    suggestion = '检查' + model + '的结构化输出能力。格式纠正后仍失败时，换用更稳定的模型或精简输出后重试未完成题目；无需修改目标难度。';
    retryable ??= false;
  } else if (internalDataContract) {
    category = manifestBoundaryRefs ? '出题流程内部格式问题' : '出题流程输入规范化问题';
    stage = manifestBoundaryRefs ? '检查测试数据覆盖' : '整理验证用例';
    message = manifestBoundaryRefs ? '测试数据清单的内部格式未通过校验。' : '验证用例的输入规范化流程未能完成。';
    reason = manifestBoundaryRefs
      ? '流程没有按内部约定保存测试用例的边界覆盖列表，列表必须始终存在、排序且不含重复项。这项错误不能证明题目文件缺失或答案有误。'
      : '用例输入在转换成约定格式时被流程的一致性检查拦住，但记录中没有实际解析错误。这是输入规范化流程需要修复的问题，不能据此认定题目有误或测试文件缺失。';
    suggestion = manifestBoundaryRefs
      ? '管理员需修复测试清单的列表保存与校验流程，再重试未完成题目；无需因此修改题目需求或目标难度。'
      : '管理员需修复用例输入的规范化与一致性检查，再重试未完成题目；无需因此修改模型、题目需求或目标难度。';
    retryable = false;
  } else if (requestLimit) {
    category = '执行请求超过服务限制'; stage = '提交程序执行请求';
    message = '出题流程提交的执行请求超过服务限制。';
    reason = /declared batch output budget/i.test(evidence) ? '单批测试数量与每例输出额度合计超过执行服务的总量限制，服务拒绝了请求，尚未运行这一批程序。' : '提交的程序或数据量超过执行服务允许的请求范围，服务已拒绝本次提交，尚未完成程序执行。';
    suggestion = '管理员需修正出题流程的执行批次划分、数据规模或输出额度，让请求落在服务限制内，再重试未完成题目；无需修改目标难度。';
    retryable ??= false;
  } else if (emptyGenerator) {
    category = '测试数据生成器输出为空'; stage = '生成测试数据';
    message = '测试数据生成器没有输出有效输入。';
    reason = '某个用例的生成器执行后返回了空白数据，系统无法取得完整输入，测试数据尚未生成成功。';
    suggestion = '检查生成器是否对每个用例编号都输出完整输入，修复提前退出或遗漏的分支后重新生成此题。';
    retryable ??= false;
  } else if (unsupportedJudge && !authoringFormat && !reviewerFormat) {
    category = '流程能力限制'; stage = '独立解法验证';
    message = '当前验证流程不支持这道题的判题方式。';
    reason = '该题的判题规则需要尚未支持的比较方式或专用检查器，不能用普通输出比较代替。';
    suggestion = '核对题目的实际判题规则；需要专用检查器或其他比较方式时，应先补齐对应验证能力，再重试未完成题目。';
    retryable ??= false;
  } else if (presentationRejected) {
    category = '题面表达检查未通过'; stage = '编写题面';
    message = '题面叙述与当前表达检查规则冲突。';
    reason = '题目标题或叙述触发了当前表现层的用语限制，题面检查未通过；这项结果不表示题意、算法或目标难度有误。';
    const term = evidence.match(/(?:title|narrative) contains normative or meta term "([^"]+)"/i)?.[1];
    if (term) reason = '题目标题或叙述使用了当前表现层检查禁止的词语「' + term + '」，题面检查未通过；这项结果不表示题意、算法或目标难度有误。';
    suggestion = '检查标题与叙述，调整表现层文案，或修正过严的表达检查后重试未完成题目；无需修改目标难度。';
    retryable ??= false;
  } else if (gateFailure) {
    ({ category, stage, reason } = gateFailure);
    if (blockingReasons?.length) reason = blockingReasons.join('；');
    message = category + '。';
    suggestion = qualityFailureSuggestion(candidate);
  } else if (failure?.code === 'quality_review_required' || failure?.code === 'quality_not_met') {
    category = '自动质量检查未通过'; stage = '自动评审与质量检查';
    reason = blockingReasons?.length ? blockingReasons.join('；') : '至少一项自动检查尚未通过，需要结合具体报告核对。';
    message = '自动质量检查暂未通过。';
    suggestion = qualityFailureSuggestion(candidate);
  } else if (authoringFormat || reviewerFormat) {
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
    suggestion = qualityFailureSuggestion(candidate);
  } else if (/unavailable|connection refused|connect.*failed|network|fetch failed|ECONN|服务.*不可用|无法连接/i.test(evidence)) {
    category = '执行服务连接问题'; stage = '连接生成或验证服务'; reason = '当前步骤所需的服务没有正常响应。';
    message = '生成所需的服务暂时无法连接。';
    suggestion = '检查模型、去重和沙箱服务的连接状态，再重试未完成的题目。';
  } else if (/[\u3400-\u9fff]/.test(original) && !/workflow execution|activity error|application error|type:|retryable:/i.test(original)) {
    message = original;
    reason = original;
    suggestion = '';
  }

  const legacyClassification = failure && !failure.category && !failure.stage && !failure.reason;
  const generic = legacyClassification && (failure.code === 'generation_failed' || (testDataFormat || statementFormat || internalDataContract || requestLimit || emptyGenerator || presentationRejected || unsupportedJudge) && ['verification_unavailable', 'verification_failed'].includes(failure.code));
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
  if (candidate === 'incomplete' && MANUAL_REVIEW_SUGGESTION.test(suggestion)) suggestion = INCOMPLETE_CANDIDATE_SUGGESTION;
  if (candidate === undefined && MANUAL_REVIEW_SUGGESTION.test(suggestion)) suggestion = '查看未通过项与生成结果，确认候选完整性后，按对应题位的提示处理。';
  return { message, suggestion, category, stage, reason, retryable, technicalDetails: details === message ? '' : details };
}

export function commonGenerationFailure(slots: { status: string; error?: string; failure?: GenerationFailure; problem_id?: string; quiz_id?: string; regenerate?: boolean }[]): { count: number; total: number; summary: GenerationFailureSummary } | undefined {
  const failed = slots.filter(slot => slot.status === 'failed' || slot.status === 'awaiting_review');
  const groups = new Map<string, { count: number; total: number; summary: GenerationFailureSummary; candidate: GenerationCandidateState }>();
  for (const slot of failed) {
    const candidate = generationCandidateState(slot);
    const summary = generationFailureSummary(slot.error || '', slot.failure, 'set', candidate);
    if (summary.category === '生成服务错误' || summary.reason.includes('具体原因暂时无法归纳')) continue;
    const key = JSON.stringify([summary.category, summary.stage, summary.reason]);
    const group = groups.get(key);
    if (group) {
      group.count++;
      if (group.candidate !== candidate) group.summary = { ...group.summary, suggestion: '逐题查看未通过项与已保存结果，根据对应题位的提示修复或重新生成。' };
    } else groups.set(key, { count: 1, total: failed.length, summary, candidate });
  }
  const group = [...groups.values()].filter(group => group.count >= 2).sort((a, b) => b.count - a.count)[0];
  return group ? { count: group.count, total: group.total, summary: group.summary } : undefined;
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
