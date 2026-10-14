const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');

function load(relative, dependencies = {}) {
  const file = path.resolve(__dirname, relative);
  const loaded = new Module(file, module);
  loaded.filename = file;
  loaded.paths = module.paths;
  loaded.require = request => Object.hasOwn(dependencies, request) ? dependencies[request] : module.require(request);
  loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true },
  }).outputText, file);
  return loaded.exports;
}

const failures = load('./generation-failure.ts');
const generation = load('./problem-set-generation.ts');
const difficulty = load('./generation-difficulty.ts');
const progress = load('./generation-progress.ts', { '@/lib/generation-failure': failures });
const ProgressNotice = load('../components/problem-sets/GenerationProgressNotice.tsx', { '@/lib/generation-progress': progress }).default;
const DifficultyNotice = load('../components/problem-sets/GenerationDifficultyNotice.tsx', { '@/lib/generation-difficulty': difficulty }).default;
const Notice = load('../components/workflow/GenerationFailureNotice.tsx', { '@/lib/generation-failure': failures }).default;
const Slot = load('../components/problem-sets/GenerationSlotCard.tsx', {
  'next/link': ({ children, ...props }) => React.createElement('a', props, children),
  '@/components/workflow/GenerationFailureNotice': { __esModule: true, default: Notice },
  '@/components/problem-sets/GenerationDifficultyNotice': { __esModule: true, default: DifficultyNotice },
  '@/components/problem-sets/GenerationProgressNotice': { __esModule: true, default: ProgressNotice },
  '@/lib/problem-set-generation': generation,
  '@/lib/generation-progress': progress,
  '@/lib/generation-failure': failures,
}).default;

test('legacy nested Temporal errors have a readable cause and keep their diagnostics', () => {
  const errors = [
    ['child workflow execution error: activity error: llm unauthorized (401)', /访问凭据/, /API Key/],
    ['activity error: provider returned 429 rate limit', /额度不足|过于频繁/, /并发限制/],
    ['child workflow execution error: StartToClose timeout', /超时/, /超时阶段/],
    ['QualityNotMet: candidate did not satisfy all nine quality gates (retryable: false)', /质量检查/, /管理员.*人工审核/],
    ['activity error: CompilationError: compilation failed', /编译/, /修复编译错误/],
    ['activity error: independent oracle_differential output mismatch', /输出.*不一致/, /失败用例/],
    ['ProblemSetSlotRejected: dedup.duplicate', /重复/, /新题/],
    ['test_cases returned invalid count', /测试数据/, /补齐或修复/],
  ];
  for (const [raw, message, suggestion] of errors) {
    const summary = failures.generationFailureSummary(raw, undefined, 'set', 'saved');
    assert.match(summary.message, message);
    assert.match(summary.suggestion, suggestion);
    assert.equal(summary.technicalDetails, raw);
    assert.doesNotMatch(summary.message, /child workflow|retryable|activity error/);
  }
});

test('server-owned failure messages and suggestions take precedence over keyword guesses', () => {
  const failure = { code: 'quality_not_met', message: '评审对题面清晰度有疑问，候选已保留。', suggestion: '检查未通过项后可人工审核。', technical_details: 'synthetic diagnostic', blocking_issues: ['样例解释还需补充。'] };
  const summary = failures.generationFailureSummary('QualityNotMet', failure, 'set', 'saved');
  assert.equal(summary.message, failure.message);
  assert.equal(summary.suggestion, failure.suggestion);
  assert.equal(summary.technicalDetails, failure.technical_details);
  const html = renderToStaticMarkup(React.createElement(Notice, { error: 'QualityNotMet', failure, candidate: 'saved' }));
  assert.match(html, /样例解释还需补充/);
  assert.match(html, /<details[^>]*><summary[^>]*>技术详情/);
  assert.ok(html.indexOf(failure.message) < html.indexOf('<details'));
  assert.ok(html.indexOf(failure.technical_details) > html.indexOf('<details'));
});

test('a legacy task retry does not imply that updated model credentials will replace its saved configuration', () => {
  const summary = failures.generationFailureSummary('LLM unauthorized (401)', undefined, 'workflow');
  assert.match(summary.suggestion, /原任务重试沿用创建时的配置/);
  assert.match(summary.suggestion, /修改配置后请新建任务/);
});

test('simple Chinese status messages are not replaced by unrelated retry advice', () => {
  const summary = failures.generationFailureSummary('3 道题未完成，可重试这些题目');
  assert.equal(summary.message, '3 道题未完成，可重试这些题目');
  assert.equal(summary.suggestion, '');
  assert.equal(summary.technicalDetails, '');
});

test('unknown legacy errors remain accessible without showing technical text as the main message', () => {
  const summary = failures.generationFailureSummary('unexpected immutable binding: revision abc123');
  assert.match(summary.message, /暂未完成/);
  assert.equal(summary.technicalDetails, 'unexpected immutable binding: revision abc123');
});

const slot = { position: 3, type: 'programming', score: 100, title: '合成入门题', status: 'awaiting_review', problem_id: 'synthetic-problem', child_id: 'synthetic-job', error: 'QualityNotMet' };
const renderSlot = (value, isAdmin = true, busy = false) => renderToStaticMarkup(React.createElement(Slot, { slot: value, isAdmin, busy, onReview() {} }));

test('a retained candidate has a review state and links to the candidate and its task', () => {
  const html = renderSlot(slot);
  assert.match(html, /等待人工审核/);
  assert.match(html, /href="\/problems\/synthetic-problem"/);
  assert.match(html, /href="\/workflows\/synthetic-job"/);
  assert.match(html, /人工审核并恢复原题位/);
  assert.equal(generation.isSetGenerationActive('awaiting_review'), false);
});

test('only administrators can review retained candidates and active operations disable the action', () => {
  assert.doesNotMatch(renderSlot(slot, false), /人工审核并恢复原题位/);
  assert.doesNotMatch(renderSlot({ ...slot, status: 'succeeded' }), /人工审核并恢复原题位/);
  assert.doesNotMatch(renderSlot({ ...slot, problem_id: undefined }), /人工审核并恢复原题位/);
  assert.match(renderSlot(slot, true, true), /<button[^>]*disabled=""/);
});

test('a difficulty mismatch remains a reference on a successful slot', () => {
  const html = renderSlot({ ...slot, status: 'succeeded', error: undefined, difficulty_assessment: { target: 800, estimated: 1200, delta: 400 } });
  assert.match(html, /已完成/);
  assert.match(html, /CF 800/);
  assert.match(html, /CF 1200/);
  assert.match(html, /\+400/);
  assert.match(html, /偏差仅作提示/);
  assert.doesNotMatch(html, /生成失败|人工审核并恢复原题位/);
});

test('a retained candidate requiring regeneration stays viewable and cannot be manually resumed', () => {
  const html = renderSlot({ ...slot, status: 'failed', regenerate: true });
  assert.match(html, /href="\/problems\/synthetic-problem"/);
  assert.match(html, /候选题需要重新生成/);
  assert.match(html, /旧候选仍保留/);
  assert.doesNotMatch(html, /人工审核并恢复原题位/);
  assert.match(renderSlot({ ...slot, regenerate: false }), /人工审核并恢复原题位/);
});

test('a disconnected running slot shows its last confirmed stage without claiming it is still running', () => {
  const html = renderToStaticMarkup(React.createElement(Slot, { slot: { ...slot, status: 'running', execution: { state: 'running', stage: '生成参考程序', message: '正在执行', elapsed_seconds: 20, attempt: 2 } }, isAdmin: true, busy: true, connectionUnknown: true, onReview() {} }));
  assert.match(html, /暂时无法确认执行状态/);
  assert.match(html, /上次确认步骤：生成参考程序/);
  assert.match(html, /1 次（第 2 次尝试）/);
  assert.doesNotMatch(html, />正在执行</);
  assert.doesNotMatch(html, /生成与校验中/);
});

test('quality activity names are readable in older task event streams', () => {
  assert.equal(failures.generationStepLabel('s_3_strict_reviewer_activity_v1'), '自动评审题目');
  assert.equal(failures.generationStepLabel('BuildS3TestManifestActivityV1'), '检查测试数据覆盖');
  assert.equal(failures.generationStepLabel('StoreS3ReviewCandidateActivityV1'), '保存未完成候选题');
  assert.equal(failures.generationStepLabel('store_s_3_review_candidate_activity_v1'), '保存未完成候选题');
  assert.equal(failures.generationStepLabel('future_unknown_step'), 'future_unknown_step');
});

test('legacy authoring JSON errors identify format and stage before incidental rating or status numbers', () => {
  const cases = [
    ['json: cannot unmarshal number into Go struct field brief of type string; rating 401', /字段类型/],
    ['json: unknown field "difficulty_401"', /不接受的字段/],
    ['invalid character after object key', /不是合法的 JSON/],
    ['unexpected end of JSON input', /输出前结束/],
  ];
  for (const [cause, reason] of cases) {
    const technical = 'child workflow execution error: activity error: parsing authoring-plan response: ' + cause;
    const summary = failures.generationFailureSummary('这道题未能完成生成', { code: 'generation_failed', message: '这道题未能完成生成', suggestion: '查看任务后重试', technical_details: technical }, 'set', 'none');
    assert.equal(summary.category, '模型输出格式错误');
    assert.equal(summary.stage, '设计题意与约束');
    assert.match(summary.reason, reason);
    assert.doesNotMatch(summary.message, /凭据|难度.*无法/);
    assert.equal(summary.technicalDetails, technical);
    assert.doesNotMatch(summary.suggestion, /人工审核|人工批准/);
  }
  for (const raw of ['workflow synthetic-401 failed', 'difficulty target 401 rejected', 'sample ID 429 was invalid']) {
    assert.notEqual(failures.generationFailureSummary(raw).category, '模型访问配置问题');
    assert.notEqual(failures.generationFailureSummary(raw).category, '模型额度或限流');
  }
});

test('failure display has four plain-language fields, retry advice and accurate candidate facts', () => {
  const failure = { code: 'internal_hidden_code', message: '作者回复无法读取', category: '模型输出格式错误', stage: '设计题意与约束', reason: '字段应为文本，实际返回数字。', suggestion: '修正输出格式后重试。', retryable: false, technical_details: 'synthetic raw diagnostic' };
  const html = renderToStaticMarkup(React.createElement(Notice, { error: '', failure, candidate: 'none' }));
  for (const label of ['失败类型', '发生阶段', '具体原因', '下一步']) assert.ok(html.includes(label));
  assert.match(html, /直接重复重试通常无效/);
  assert.match(html, /尚未保存可供审核的候选题/);
  assert.doesNotMatch(html, /internal_hidden_code|管理员可以人工审核/);
  assert.ok(html.indexOf('synthetic raw diagnostic') > html.indexOf('<details'));
  const partial = renderToStaticMarkup(React.createElement(Notice, { error: 'QualityNotMet', candidate: 'incomplete' }));
  assert.match(partial, /仅保存了未完成候选/);
  assert.doesNotMatch(partial, /管理员可以人工审核/);
});

test('new author and reviewer format exceptions have readable stages and preserve original evidence', () => {
  const cases = [
    ['AuthoringPlanFormatError: unknown or non-canonical JSON key "extra_401"', '设计题意与约束', /不接受的字段名/],
    ['AuthoringPlanFormatError: problem_definition: expected scalar, got object', '设计题意与约束', /字段类型/],
    ['AuthoringPlanTruncatedError: stop_reason=length', '设计题意与约束', /输出前结束/],
    ['ReviewerFormatError: expected scalar for assessment', '评审题目', /字段类型/],
    ['ReviewerTruncatedError: max_tokens=401', '评审题目', /输出前结束/],
  ];
  for (const [cause, stage, reason] of cases) {
    const raw = 'activity error: ' + cause;
    const summary = failures.generationFailureSummary(raw);
    assert.equal(summary.category, '模型输出格式错误');
    assert.equal(summary.stage, stage);
    assert.match(summary.reason, reason);
    assert.equal(summary.retryable, false);
    assert.equal(summary.technicalDetails, raw);
    assert.doesNotMatch(summary.message, /凭据|难度不合法/);
  }
});

test('twelve same authoring failures share a cause without merging unrelated or unknown failures', () => {
  const repeated = Array.from({ length: 12 }, () => ({ status: 'failed', error: 'parsing authoring-plan response: json: cannot unmarshal number into field of type string' }));
  const shared = failures.commonGenerationFailure(repeated);
  assert.equal(shared.count, 12);
  assert.equal(shared.total, 12);
  assert.equal(shared.summary.category, '模型输出格式错误');
  assert.equal(failures.commonGenerationFailure([{ status: 'failed', error: 'unknown a' }, { status: 'failed', error: 'unknown b' }]), undefined);
  assert.equal(failures.commonGenerationFailure([{ status: 'failed', error: 'HTTP 401 unauthorized' }, { status: 'failed', error: 'parsing authoring-plan response: unknown field "extra"' }]), undefined);
});

test('old and enriched spec lint failures explain constraints and prevent manual approval of incomplete candidates', () => {
  const base = { code: 'quality_review_required', blocked_gate: 'spec_lint', message: '题意与约束描述尚未通过检查', suggestion: '查看检查结果，修复或由管理员人工确认后继续入集。', technical_details: 'deterministic spec lint failed' };
  const enriched = { ...base, code: 'spec_lint_failed', category: '题意与约束检查未通过', stage: '设计题意与约束', reason: '合成变量的取值范围与约束关联不一致。', blocking_issues: ['合成边界定义需要补充。'] };
  for (const failure of [base, enriched]) {
    const summary = failures.generationFailureSummary(failure.message, failure, 'set', 'incomplete');
    assert.equal(summary.category, '题意与约束检查未通过');
    assert.equal(summary.stage, '设计题意与约束');
    assert.match(summary.suggestion, /需要重新生成/);
    assert.match(summary.suggestion, /旧候选仍保留/);
    assert.doesNotMatch(summary.suggestion, /人工确认|管理员|人工审核/);
    assert.equal(summary.technicalDetails, failure.technical_details);
    const html = renderSlot({ ...slot, status: 'failed', regenerate: true, failure, error: failure.message });
    assert.match(html, /题意与约束检查未通过/);
    assert.match(html, /仅保存了未完成候选/);
    assert.doesNotMatch(html, /人工确认|人工审核并恢复原题位/);
  }
  assert.equal(failures.generationFailureSummary('', base).reason, '结构化题意、变量范围或约束关联尚未通过一致性检查。');
  assert.equal(failures.generationFailureSummary('', enriched).reason, enriched.reason);
  const issuesOnly = { ...base, blocking_issues: ['合成输入长度与数组约束不一致。'] };
  assert.equal(failures.generationFailureSummary('', issuesOnly).reason, issuesOnly.blocking_issues[0]);
  assert.equal(failures.generationFailureSummary('deterministic spec lint failed').category, '题意与约束检查未通过');
  assert.doesNotMatch(failures.generationFailureSummary('', base, 'set', 'none').suggestion, /人工确认|管理员/);
  assert.doesNotMatch(failures.generationFailureSummary('', base).suggestion, /人工确认|管理员/);
  assert.match(failures.generationFailureSummary('', base, 'set', 'saved').suggestion, /管理员人工确认/);
  const repeated = Array.from({ length: 12 }, () => ({ status: 'failed', problem_id: 'synthetic-retained', regenerate: true, failure: base }));
  const common = failures.commonGenerationFailure(repeated);
  assert.equal(common.count, 12);
  assert.equal(common.summary.category, '题意与约束检查未通过');
  assert.match(common.summary.suggestion, /需要重新生成/);
  assert.doesNotMatch(common.summary.suggestion, /人工确认|管理员/);
  const mixed = failures.commonGenerationFailure([{ ...repeated[0], regenerate: false }, repeated[1]]);
  assert.match(mixed.summary.suggestion, /根据对应题位/);
  assert.doesNotMatch(mixed.summary.suggestion, /人工确认|管理员/);
});

test('each known structured quality gate has a readable classification and stage', () => {
  const gates = {
    spec_lint: '设计题意与约束', sample_output_binding: '核对样例输出', oracle_differential: '独立解法验证',
    sanitizer: '检查程序运行错误', boundary_coverage: '检查测试数据覆盖', test_manifest: '检查测试数据与题目文件',
    reviewer_schema_verdict: '自动评审题目', dedup: '检查题目重复', hidden_regression: '运行额外验证',
  };
  for (const [gate, stage] of Object.entries(gates)) {
    const summary = failures.generationFailureSummary('', { code: 'quality_review_required', blocked_gate: gate, message: '合成候选需要处理', technical_details: 'synthetic gate evidence' }, 'set', 'incomplete');
    assert.notEqual(summary.category, '生成服务错误');
    assert.equal(summary.stage, stage);
    assert.doesNotMatch(summary.reason, /具体原因暂时无法归纳/);
    assert.match(summary.suggestion, /重新生成/);
  }
});

test('test data format, empty generator output and rejected execution budgets have distinct actions', () => {
  const cases = [
    ['verification_unavailable', 'sandbox HTTP 400 (invalid_request): declared batch output budget exceeds 12345 bytes', '执行请求超过服务限制', '提交程序执行请求', /批次划分/],
    ['generation_failed', 'remote generator execution failed closed: generator case 3 produced empty input', '测试数据生成器输出为空', '生成测试数据', /每个用例编号/],
    ['verification_failed', 'parsing test data response: model response is not a complete JSON object', '模型输出格式错误', '生成测试数据', /结构化输出/],
    ['verification_failed', 'test-data JSON format is still invalid after one correction (type: TestDataFormatError)', '模型输出格式错误', '生成测试数据', /格式纠正后仍失败/],
  ];
  for (const [code, technical, category, stage, suggestion] of cases) {
    const failure = { code, message: '合成旧分类：检查执行服务', suggestion: '检查 worker 和服务连接', technical_details: technical };
    const summary = failures.generationFailureSummary('', failure, 'set', 'incomplete');
    assert.equal(summary.category, category);
    assert.equal(summary.stage, stage);
    assert.match(summary.suggestion, suggestion);
    assert.equal(summary.technicalDetails, technical);
    assert.doesNotMatch(summary.message, /旧分类/);
    assert.doesNotMatch(summary.suggestion, /worker|人工|提高难度/);
    assert.equal(summary.retryable, false);
    const html = renderSlot({ ...slot, status: 'failed', regenerate: true, failure, error: '' });
    assert.match(html, /需要重新生成/);
    assert.doesNotMatch(html, /人工审核并恢复原题位|检查 worker/);
  }
  for (const keyword of ['sandbox HTTP 401 unauthorized', 'sandbox HTTP 400 (invalid_request): declared batch output budget exceeds 12345 bytes', 'generator case 3 produced empty input', 'max_tokens']) {
    const summary = failures.generationFailureSummary('parsing test data response: unknown field "' + keyword + '"');
    assert.equal(summary.category, '模型输出格式错误');
    assert.equal(summary.stage, '生成测试数据');
    assert.doesNotMatch(summary.reason, /截断/);
  }
  const truncated = failures.generationFailureSummary('test-data response was truncated (type: TestDataTruncatedError)');
  assert.equal(truncated.stage, '生成测试数据');
  assert.match(truncated.reason, /截断/);
  assert.doesNotMatch(failures.generationFailureSummary('TestDataFormatError: not a complete JSON object').reason, /截断/);
});

test('presentation wording failures identify the statement stage and keep parser failures separate', () => {
  const raw = 'narrative contains normative or meta term "合成规范词"';
  const summary = failures.generationFailureSummary(raw, undefined, 'set', 'incomplete');
  assert.equal(summary.category, '题面表达检查未通过');
  assert.equal(summary.stage, '编写题面');
  assert.match(summary.reason, /「合成规范词」/);
  assert.match(summary.reason, /不表示题意、算法或目标难度有误/);
  assert.match(summary.suggestion, /过严的表达检查/);
  assert.doesNotMatch(summary.suggestion, /人工|提高难度/);
  assert.equal(summary.technicalDetails, raw);
  const parse = failures.generationFailureSummary('parse statement narrative response: unknown field "narrative contains normative or meta term"');
  assert.equal(parse.category, '模型输出格式错误');
  assert.equal(parse.stage, '编写题面');
});

test('structured oracle identity failures do not invent an output mismatch or manual approval path', () => {
  const failure = { code: 'oracle_identity_not_independent', blocked_gate: 'oracle_differential', message: '独立校验未满足模型与来源独立性要求', reason: '两路实际返回相同模型，即使测试输出一致，也未满足当前规则。', technical_details: 'synthetic same-returned-model decision' };
  const summary = failures.generationFailureSummary('', failure, 'set', 'incomplete');
  assert.equal(summary.category, '验证来源不够独立');
  assert.equal(summary.stage, '独立解法验证');
  assert.equal(summary.reason, failure.reason);
  assert.match(summary.suggestion, /第二个实际不同的模型和服务地址/);
  assert.doesNotMatch(summary.suggestion, /人工|输出.*不一致/);
  const html = renderSlot({ ...slot, status: 'failed', regenerate: true, failure, error: '' });
  assert.match(html, /验证来源不够独立/);
  assert.doesNotMatch(html, /人工审核并恢复原题位|参考程序的输出.*不一致/);
  const historical = failures.generationFailureSummary('', { code: 'quality_review_required', blocked_gate: 'oracle_differential', message: '合成旧检查', technical_details: 'oracle.promotion_failed' }, 'set', 'incomplete');
  assert.equal(historical.category, '独立解法验证未通过');
  assert.doesNotMatch(historical.reason, /两路实际返回相同模型/);
  const judging = failures.generationFailureSummary('unsupported oracle judge contract: mode special_judge, comparison profile synthetic');
  assert.equal(judging.category, '流程能力限制');
  assert.match(judging.reason, /不能用普通输出比较代替/);
  assert.doesNotMatch(judging.suggestion, /人工/);
});
