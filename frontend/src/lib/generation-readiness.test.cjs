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
  loaded.filename = file; loaded.paths = module.paths;
  loaded.require = request => Object.hasOwn(dependencies, request) ? dependencies[request] : module.require(request);
  loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true },
  }).outputText, file);
  return loaded.exports;
}
let admin = true;
const auth = { useAuth: () => ({ isAdmin: admin, session: { user: { id: 'synthetic-user' } } }) };
const link = ({ children, ...props }) => React.createElement('a', props, children);
const Notice = load('../components/problem-sets/GenerationReadinessNotice.tsx', {
  'next/link': link, '@/components/auth/AuthProvider': auth,
}).default;
const blocked = {
  ready: false, scope: 'configured_routes',
  blockers: [{ code: 'same_model', message: '生成与独立验算使用了相同的模型，开始前需要调整。', settings_url: '/settings?section=verification' }],
  warnings: [{ code: 'embedding_missing', message: '未配置向量服务，本次不会执行相似题去重；这不阻止开始生成。' }],
  checks: [], effective_roles: [{ role: 'generation', label: '生成模型', model: 'synthetic-model', configured: true }, { role: 'verification', label: '独立验算', model: 'synthetic-model', configured: true }],
};
const renderNotice = props => renderToStaticMarkup(React.createElement(Notice, { onRefresh() {}, error: '', loading: false, ...props }));

test('readiness exposes a hidden model conflict and an actual configuration link before generation', () => {
  admin = true;
  const html = renderNotice({ readiness: blocked });
  assert.match(html, /尚不能开始生成/);
  assert.match(html, /相同的模型/);
  assert.match(html, /独立验算/);
  assert.match(html, /href="\/settings\?section=verification"/);
  assert.match(html, /不会执行相似题去重/);
  assert.match(html, /不调用模型生成题目/);
  assert.doesNotMatch(html, /API Key|runtime:|endpoint_id|服务已确认.*独立/);
});

test('optional dedup warning does not turn ready configuration into a failure and members get appropriate guidance', () => {
  const html = renderNotice({ readiness: { ...blocked, ready: true, blockers: [] } });
  assert.match(html, /当前配置已就绪/);
  assert.match(html, /未配置向量服务/);
  assert.doesNotMatch(html, /尚不能开始生成/);
  admin = false;
  const member = renderNotice({ readiness: blocked });
  assert.match(member, /请联系管理员/);
  assert.doesNotMatch(member, /href="\/settings/);
  admin = true;
});

test('failed or pending checks do not advertise readiness', () => {
  const pending = renderNotice({ readiness: blocked, loading: true });
  assert.match(pending, /正在检查模型与执行服务/);
  assert.doesNotMatch(pending, /当前配置已就绪/);
  const unknown = renderNotice({ readiness: { ...blocked, ready: true }, error: '无法确认服务状态，请重新检查。' });
  assert.match(unknown, /role="alert"/);
  assert.doesNotMatch(unknown, /当前配置已就绪/);
});

test('new set cannot start under failed readiness but can still be saved without generation', () => {
  const generation = load('./problem-set-generation.ts');
  const deps = {
    'next/link': link, 'next/navigation': { useRouter: () => ({ push() {} }) },
    '@/components/auth/AuthProvider': auth,
    '@/components/ui/Workspace': { PageHeader: ({ title }) => React.createElement('h1', null, title), FormSection: ({ children }) => React.createElement('section', null, children) },
    '@/components/problem-sets/GenerationConfigFields': { __esModule: true, default: () => null },
    '@/components/problem-sets/GenerationReadinessNotice': { __esModule: true, default: Notice },
    '@/components/workflow/GenerationFailureNotice': { __esModule: true, default: () => null },
    '@/lib/problem-set-generation': generation,
    '@/lib/api': { createProblemSet() { throw new Error('unexpected generation call'); } },
    '@/lib/generation-progress': { saveGenerationStartNotice() {} },
    '@/lib/use-generation-readiness': { useGenerationReadiness: () => ({ readiness: blocked, loading: false, error: '', canStart: false, refresh() {} }) },
  };
  const Page = load('../app/problem-sets/new/page.tsx', deps).default;
  const html = renderToStaticMarkup(React.createElement(Page));
  assert.match(html, /<button[^>]*type="submit"[^>]*disabled=""[^>]*>[\s\S]*?创建并开始生成<\/button>/);
  assert.match(html, /关闭后仅保存需求/);
  const SavePage = load('../app/problem-sets/new/page.tsx', { ...deps, react: { ...React, useState: initial => {
    const value = typeof initial === 'function' ? initial() : initial;
    return [value === true ? false : value, () => {}];
  } } }).default;
  const save = renderToStaticMarkup(React.createElement(SavePage));
  assert.match(save, /<button[^>]*type="submit"(?![^>]*disabled)[^>]*>[\s\S]*?保存题集<\/button>/);
});
