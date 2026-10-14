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
    const summary = failures.generationFailureSummary(raw);
    assert.match(summary.message, message);
    assert.match(summary.suggestion, suggestion);
    assert.equal(summary.technicalDetails, raw);
    assert.doesNotMatch(summary.message, /child workflow|retryable|activity error/);
  }
});

test('server-owned failure messages and suggestions take precedence over keyword guesses', () => {
  const failure = { code: 'quality_not_met', message: '评审对题面清晰度有疑问，候选已保留。', suggestion: '检查未通过项后可人工审核。', technical_details: 'synthetic diagnostic', blocking_issues: ['样例解释还需补充。'] };
  const summary = failures.generationFailureSummary('QualityNotMet', failure);
  assert.equal(summary.message, failure.message);
  assert.equal(summary.suggestion, failure.suggestion);
  assert.equal(summary.technicalDetails, failure.technical_details);
  const html = renderToStaticMarkup(React.createElement(Notice, { error: 'QualityNotMet', failure }));
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
