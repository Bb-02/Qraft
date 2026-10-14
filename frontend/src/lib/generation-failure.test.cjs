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
const DifficultyNotice = load('../components/problem-sets/GenerationDifficultyNotice.tsx', { '@/lib/generation-difficulty': difficulty }).default;
const Notice = load('../components/workflow/GenerationFailureNotice.tsx', { '@/lib/generation-failure': failures }).default;
const Slot = load('../components/problem-sets/GenerationSlotCard.tsx', {
  'next/link': ({ children, ...props }) => React.createElement('a', props, children),
  '@/components/workflow/GenerationFailureNotice': { __esModule: true, default: Notice },
  '@/components/problem-sets/GenerationDifficultyNotice': { __esModule: true, default: DifficultyNotice },
  '@/lib/problem-set-generation': generation,
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

test('quality activity names are readable in older task event streams', () => {
  assert.equal(failures.generationStepLabel('s_3_strict_reviewer_activity_v1'), '自动评审题目');
  assert.equal(failures.generationStepLabel('BuildS3TestManifestActivityV1'), '检查测试数据覆盖');
  assert.equal(failures.generationStepLabel('StoreS3ReviewCandidateActivityV1'), '保存未完成候选题');
  assert.equal(failures.generationStepLabel('store_s_3_review_candidate_activity_v1'), '保存未完成候选题');
  assert.equal(failures.generationStepLabel('future_unknown_step'), 'future_unknown_step');
});
