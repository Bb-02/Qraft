export interface GenerationFailure {
  code: string;
  message: string;
  suggestion?: string;
  technical_details?: string;
  blocked_gate?: string;
  blocking_issues?: string[];
}

export interface GenerationFailureSummary {
  message: string;
  suggestion: string;
  technicalDetails: string;
}

// Old executions only have a nested Temporal error string. Keep them readable
// while new executions provide a server-owned message and a concrete next step.
export function generationFailureSummary(error: string, failure?: GenerationFailure, context: 'set' | 'workflow' = 'set'): GenerationFailureSummary {
  const original = error.trim();
  const details = (failure?.technical_details || original).trim();
  let message = '这道题暂未完成。';
  let suggestion = '打开生成任务查看失败阶段，再重试未完成的题目；已经完成的题目会保留。';

  if (/\b401\b|\b403\b|unauthorized|authentication_error|invalid[_ ](?:api[_ ])?(?:key|token)|鉴权|认证失败/i.test(original)) {
    message = '模型服务没有接受当前访问凭据。';
    suggestion = context === 'workflow'
      ? '请管理员检查模型配置中的服务地址、API Key 和访问权限。原任务重试沿用创建时的配置，修改配置后请新建任务。'
      : '请管理员检查模型配置中的服务地址、API Key 和访问权限，再重试这道题。';
  } else if (/\b429\b|rate.?limit|insufficient_quota|quota.?exceeded|余额|额度|限流/i.test(original)) {
    message = '模型服务的额度不足或请求过于频繁。';
    suggestion = '检查模型服务余额和并发限制，稍后重试未完成的题目。';
  } else if (/timeout|timed out|deadline exceeded|超时/i.test(original)) {
    message = '生成或验证等待超时。';
    suggestion = '查看生成任务确认超时阶段，检查对应服务后重试；已经保存的结果会保留。';
  } else if (/CompilationError|compilation failed|compile.*(?:failed|error)|编译失败/i.test(original)) {
    message = '参考程序未能编译通过。';
    suggestion = '打开候选题目检查参考程序；修复编译错误后重新验证，或重新生成这道题。';
  } else if (/DuplicateProblem|ProblemSetSlotRejected|dedup\.duplicate|重复题|重复内容|内容重复/i.test(original)) {
    message = '这道题与已有内容重复，尚未加入题集。';
    suggestion = '查看候选并调整题意，或重试生成一道新题。';
  } else if (/output mismatch|oracle_differential|ValidationFailed|答案.*不一致|输出.*不一致/i.test(original)) {
    message = '参考程序的输出与校验答案不一致。';
    suggestion = '打开候选题目核对题意、参考解法和失败用例，修复后重新验证。';
  } else if (/testdata|test[_ -]cases?|TestManifest|测试数据|资产.*校验/i.test(original)) {
    message = '测试数据或题目文件尚未通过完整性检查。';
    suggestion = '打开候选题目补齐或修复测试数据，再重新验证并加入题集。';
  } else if (/QualityNotMet|quality gates?|质量校验|质量门禁|未通过.*复核|QuizQualityNotMet/i.test(original)) {
    message = '自动质量检查暂未通过。';
    suggestion = '查看候选题目和未通过项。确认内容可用后，管理员可以人工审核；有错误时先修复或重试。';
  } else if (/unavailable|connection refused|connect.*failed|network|fetch failed|ECONN|服务.*不可用|无法连接/i.test(original)) {
    message = '生成所需的服务暂时无法连接。';
    suggestion = '检查模型、去重和沙箱服务的连接状态，再重试未完成的题目。';
  } else if (/[\u3400-\u9fff]/.test(original) && !/workflow execution|activity error|application error|type:|retryable:/i.test(original)) {
    message = original;
    suggestion = '';
  }

  if (failure?.message?.trim()) message = failure.message.trim();
  if (failure?.suggestion?.trim()) suggestion = failure.suggestion.trim();
  return { message, suggestion, technicalDetails: details === message ? '' : details };
}

const GENERATION_STEP_LABELS: Record<string, string> = {
  resolves3repairpolicyactivityv1: '读取自动修复配置',
  buildcanonicalauthoringbriefactivityv1: '整理出题需求',
  generateauthoringplan: '设计题意与约束',
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
