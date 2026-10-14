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
  loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true } }).outputText, file);
  return loaded.exports;
}
const failures = load('./generation-failure.ts');
const progress = load('./generation-progress.ts', { '@/lib/generation-failure': failures });
const Notice = load('../components/problem-sets/GenerationProgressNotice.tsx', { '@/lib/generation-progress': progress }).default;
const render = value => renderToStaticMarkup(React.createElement(Notice, { progress: value }));

test('queued, missing executor and unknown states do not claim generation has begun', () => {
  const states = [['queued', /已排队，尚未开始执行/], ['waiting_for_worker', /等待执行服务接单/], ['unknown', /暂时无法确认执行状态/]];
  for (const [state, title] of states) {
    const html = render({ state, message: '', elapsed_seconds: 12 });
    assert.match(html, title);
    assert.doesNotMatch(html, /正在生成|预计|剩余|[0-9]+%|Temporal|poller/);
  }
  const unknown = render({ state: 'unknown', message: '', elapsed_seconds: 12, activity_started_at: '2026-10-14T12:00:00Z' });
  assert.match(unknown, /上次记录的等待时长/);
  assert.match(unknown, /12 秒/);
});

test('elapsed time comes from the recorded activity start and safely handles missing timestamps', () => {
  const now = Date.parse('2026-10-14T12:30:00Z');
  assert.equal(progress.generationElapsedSeconds({ activity_started_at: '2026-10-14T12:00:00Z', elapsed_seconds: 1 }, now, now), 1800);
  assert.equal(progress.generationElapsedSeconds({ activity_started_at: 'bad', elapsed_seconds: 90 }, now, now - 5000), 95);
  assert.equal(progress.generationElapsedSeconds({ activity_started_at: '2026-10-14T13:00:00Z', elapsed_seconds: 1 }, now, now), 0);
  assert.equal(progress.generationWaitLabel(1800), '30 分 0 秒');
  assert.equal(progress.generationWaitLabel(3665), '1 小时 1 分');
});

test('observed planning stage, retries and unavailable similarity check are visible', () => {
  const html = render({ state: 'planning', stage: '规划题位', message: '生成整套规划', elapsed_seconds: 120, attempt: 3, activity_started_at: '2026-10-14T12:00:00Z', last_heartbeat_at: '2026-10-14T12:01:00Z', warnings: ['向量服务未配置，本次未执行相似题去重。'] });
  assert.match(html, /规划题位/);
  assert.match(html, /当前步骤开始于/);
  assert.match(html, /2 次（第 3 次尝试）/);
  assert.match(html, /最近收到反馈/);
  assert.match(html, /未执行相似题去重/);
  assert.doesNotMatch(html, /预计|剩余|[0-9]+%/);
  const planning = render({ state: 'planning', activity: 'PrepareProblemSetBatchActivity', message: '', elapsed_seconds: 20 });
  assert.match(planning, /规划整套题目与题位/);
  assert.doesNotMatch(planning, /PrepareProblemSetBatchActivity/);
});

test('start failure storage retains a safe reason without raw provider errors or settings', () => {
  const values = new Map();
  const storage = { setItem: (key, value) => values.set(key, value), getItem: key => values.get(key) ?? null, removeItem: key => values.delete(key) };
  const raw = 'worker unavailable: https://private.example.invalid/config?api_key=synthetic-secret';
  progress.saveGenerationStartNotice(storage, 'synthetic-set', raw);
  assert.equal(values.size, 1);
  for (const value of values.values()) assert.doesNotMatch(value, /private\.example|synthetic-secret|api_key|worker/);
  const notice = progress.consumeGenerationStartNotice(storage, 'synthetic-set');
  assert.match(notice.message, /执行服务尚未准备好/);
  assert.match(notice.suggestion, /这个题集继续启动/);
  assert.equal(values.size, 0);
  assert.doesNotMatch(progress.consumeGenerationStartNotice(storage, 'synthetic-set').message, /模型/);
});

test('unavailable browser storage does not turn a saved set into a submission failure', () => {
  const unavailable = { setItem() { throw new Error('unavailable'); }, getItem() { throw new Error('unavailable'); }, removeItem() { throw new Error('unavailable'); } };
  assert.doesNotThrow(() => progress.saveGenerationStartNotice(unavailable, 'synthetic-set', 'worker unavailable'));
  assert.match(progress.consumeGenerationStartNotice(unavailable, 'synthetic-set').message, /题集已保存/);
});
