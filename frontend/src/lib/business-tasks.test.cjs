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
const statuses = load('./status.ts');
const tasks = load('./business-tasks.ts', { './status': statuses });
const failures = load('./generation-failure.ts');
const parent = { workflow_id: 'synthetic-parent', status: 'Completed', task: { kind: 'problem_set', title: '合成入门比赛', source_url: '/problem-sets/synthetic-set', result_url: '/problem-sets/synthetic-set', status: 'partial', completed_count: 0, total_count: 12, summary: '12 道题未完成，需要处理共同原因。' } };
const children = Array.from({ length: 12 }, (_, i) => ({ workflow_id: 'synthetic-child-' + i, status: 'Failed', task: { kind: 'problem', title: '合成子题 ' + (i + 1), parent_workflow_id: parent.workflow_id, is_child: true, position: i + 1, result_url: '/problems/synthetic-' + i } }));

test('a set and twelve children are one business group with ordered child entries', () => {
  const groups = tasks.groupBusinessTasks([...children.slice().reverse(), parent]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].main, parent);
  assert.equal(groups[0].children.length, 12);
  assert.deepEqual(groups[0].children.map(child => child.task.position), Array.from({ length: 12 }, (_, i) => i + 1));
  assert.equal(tasks.businessTaskStatusLabel(parent), '需要处理');
  assert.notEqual(tasks.businessTaskStatusLabel(parent), '已完成');
  assert.equal(tasks.problemSetRecoveryURL(parent), '/problem-sets/synthetic-set');
  assert.equal(tasks.problemSetRecoveryURL(children[0]), undefined);
});

test('a paginated orphan child preserves a missing parent without inventing a title', () => {
  const groups = tasks.groupBusinessTasks([children[0]]);
  assert.equal(groups[0].main, undefined);
  assert.equal(groups[0].parentID, parent.workflow_id);
  const legacy = { workflow_id: 'problem-set-generation-synthetic', status: 'Running' };
  assert.equal(tasks.businessTaskTitle(legacy), '未识别任务');
  assert.equal(tasks.businessTaskKind(legacy), '未识别任务');
  const outerParent = tasks.groupBusinessTasks([{ workflow_id: 'legacy-child', status: 'Failed', parent_workflow_id: 'legacy-parent' }]);
  assert.equal(outerParent[0].parentID, 'legacy-parent');
});

test('source and result navigation accepts only local business pages', () => {
  for (const value of ['/problem-sets/synthetic', '/problems/synthetic', '/quizzes/synthetic', '/rating?assessment=synthetic']) assert.equal(tasks.safeBusinessTaskURL(value), value);
  for (const value of ['https://example.invalid', '//example.invalid', '/api/v1/secrets', '/problems-extra', '/problems\\evil', 'javascript:evil', undefined]) assert.equal(tasks.safeBusinessTaskURL(value), undefined);
});

let records = [];
let listError = null;
let requestParams;
const noop = () => {};
const Page = load('../app/workflows/page.tsx', {
  'next/link': ({ children, ...props }) => React.createElement('a', props, children),
  '@/components/auth/AuthProvider': { useAuth: () => ({ isAdmin: true }) },
  '@/components/RefreshButton': { __esModule: true, default: () => React.createElement('button', null, '刷新') },
  '@/components/ui/Workspace': { PageHeader: ({ title, description, actions }) => React.createElement('header', null, title, description, actions), EmptyState: ({ title }) => React.createElement('p', null, title) },
  '@/hooks/useWorkflow': { useWorkflows: params => { requestParams = params; return { workflows: records, loading: false, error: listError, nextCursor: '', refresh: noop, setParams: noop }; } },
  '@/lib/utils': { cn: (...values) => values.join(' '), formatDate: value => value, formatRelativeTime: value => value, statusBadgeColor: () => '' },
  '@/lib/constants': { DEFAULT_PAGE_SIZE: 20 },
  '@/lib/status': statuses,
  '@/lib/business-tasks': tasks,
  '@/lib/generation-failure': failures,
}).default;
const render = value => { records = value; return renderToStaticMarkup(React.createElement(Page)); };

test('task center prioritizes business names and links, defaults to mine and collapses child logs', () => {
  const html = render([parent, ...children]);
  assert.match(html, /合成入门比赛/);
  assert.match(html, /href="\/problem-sets\/synthetic-set"/);
  assert.match(html, /实际完成 0 \/ 12/);
  assert.match(html, /处理未完成题目/);
  assert.match(html, /查看 12 条子任务记录/);
  assert.match(html, /<details[^>]*>/);
  assert.doesNotMatch(html, /<details[^>]*open/);
  assert.match(html, /日志与技术详情/);
  assert.match(html, /可访问的任务（含历史记录）/);
  assert.equal(requestParams.scope, 'mine');
  assert.equal(requestParams.include_children, false);
});

let detailWorkflow;
const DetailPage = load('../app/workflows/[id]/page.tsx', {
  'next/link': ({ children, ...props }) => React.createElement('a', props, children),
  'next/navigation': { useParams: () => ({ id: 'synthetic-parent' }) },
  '@/components/RefreshButton': { __esModule: true, default: () => React.createElement('button', null, '刷新') },
  '@/components/ui/Workspace': { PageHeader: ({ title, description, actions }) => React.createElement('header', null, title, description, actions), SectionHeading: ({ title }) => React.createElement('h2', null, title) },
  '@/components/workflow/SimilarityPanel': { __esModule: true, default: () => null },
  '@/components/workflow/GenerationFailureNotice': { __esModule: true, default: () => null },
  '@/hooks/useWorkflow': {
    useWorkflow: () => ({ workflow: detailWorkflow, loading: false, error: null, refresh: noop }),
    useWorkflowEvents: () => ({ events: [], latestEvent: null, connected: false, error: null }),
    useWorkflowActions: () => ({ approve: noop, reject: noop, retry: noop, cancel: noop, loading: false, error: null }),
  },
  '@/lib/utils': { cn: (...values) => values.join(' '), formatDate: value => value, formatRelativeTime: value => value, statusBadgeColor: () => '' },
  '@/lib/status': statuses,
  '@/lib/business-tasks': tasks,
  '@/lib/generation-failure': failures,
}).default;

test('unfinished sets link to their set recovery flow instead of generic workflow reset', () => {
  for (const status of ['Completed', 'Failed']) {
    detailWorkflow = { ...parent, status };
    const html = renderToStaticMarkup(React.createElement(DetailPage));
    assert.match(html, /合成入门比赛/);
    assert.match(html, /href="\/problem-sets\/synthetic-set"[^>]*>.*?处理未完成题目/);
    assert.match(html, /实际完成 0 \/ 12/);
    assert.match(html, /任务需要处理/);
    assert.doesNotMatch(html, /这次执行已完成|>重试<|>通过审核</);
  }
  detailWorkflow = { workflow_id: 'legacy-completed', status: 'Completed' };
  const html = renderToStaticMarkup(React.createElement(DetailPage));
  assert.match(html, /这次执行已完成/);
  assert.doesNotMatch(html, />通过审核</);
});

test('completed historical set executions without a business snapshot are neutral and remain terminal', () => {
  for (const status of ['Completed', 'WORKFLOW_EXECUTION_STATUS_COMPLETED']) {
    const historical = { ...parent, status, task: { kind: 'problem_set', title: '合成历史题集', source_url: '/problem-sets/synthetic-set' } };
    assert.equal(tasks.businessTaskStatus(historical), 'ended');
    assert.equal(tasks.businessTaskStatusLabel(historical), '已结束，结果请查看题集');
    assert.equal(tasks.problemSetRecoveryURL(historical), undefined);
    assert.match(render([historical]), /已结束，结果请查看题集/);
    detailWorkflow = historical;
    const html = renderToStaticMarkup(React.createElement(DetailPage));
    assert.match(html, /已结束，结果请查看题集/);
    assert.match(html, /href="\/problem-sets\/synthetic-set"/);
    assert.doesNotMatch(html, /这次执行已完成|正在重连|实时更新|>取消<|>重试<|实际完成/);
  }
  const problem = { workflow_id: 'synthetic-completed-problem', status: 'Completed', task: { kind: 'problem', title: '合成单题' } };
  assert.equal(tasks.businessTaskStatus(problem), 'approved');
  assert.equal(tasks.businessTaskStatusLabel(problem), '已完成');
  assert.equal(tasks.businessTaskStatus({ ...parent, task: { ...parent.task, status: 'completed' } }), 'completed');
});

test('old metadata and disconnected lists remain understandable without fabricated task facts', () => {
  assert.match(render([{ workflow_id: 'legacy-opaque-id', status: 'Running' }]), /未识别任务/);
  assert.match(render([children[0]]), /主任务不在本页/);
  listError = 'synthetic technical connection error';
  const html = render([parent]);
  assert.match(html, /上次确认：需要处理/);
  assert.match(html, /暂时无法读取任务的最新状态/);
  listError = null;
});
