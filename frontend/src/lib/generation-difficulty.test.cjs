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

const difficulty = load('./generation-difficulty.ts');
const Notice = load('../components/problem-sets/GenerationDifficultyNotice.tsx', { '@/lib/generation-difficulty': difficulty }).default;
const render = assessment => renderToStaticMarkup(React.createElement(Notice, { assessment }));

test('an absent or invalid estimate is never filled with the target rating', () => {
  for (const estimated of [undefined, null, 0, NaN, '800']) {
    const assessment = difficulty.parseGenerationDifficulty({ target: 800, estimated, delta: 0 });
    assert.equal(assessment.estimated, undefined);
    assert.equal(assessment.delta, undefined);
    const html = render({ target: 800, estimated, delta: 0 });
    assert.match(html, /未评估/);
    assert.match(html, /暂无/);
    assert.equal((html.match(/CF 800/g) || []).length, 1);
    assert.doesNotMatch(html, /bg-success/);
  }
});

test('signed deviation comes from the independent estimate and target', () => {
  assert.equal(difficulty.parseGenerationDifficulty({ target: 800, estimated: 1100, delta: -200 }).delta, 300);
  assert.equal(difficulty.generationDifficultyDelta(300), '+300');
  assert.equal(difficulty.generationDifficultyDelta(-100), '-100');
  assert.equal(difficulty.generationDifficultyDelta(0), '0');
  assert.match(render({ target: 800, estimated: 1100 }), /\+300/);
  assert.match(render({ target: 800, estimated: 700 }), />-100</);
});

test('both sides of each difficulty color boundary have the intended tone', () => {
  for (const delta of [-100, 0, 100]) assert.equal(difficulty.generationDifficultyTone(delta), 'success');
  for (const delta of [-300, -200, -101, 101, 200, 300]) assert.equal(difficulty.generationDifficultyTone(delta), 'warning');
  for (const delta of [-301, 301, 800]) assert.equal(difficulty.generationDifficultyTone(delta), 'danger');
  for (const delta of [undefined, NaN]) assert.equal(difficulty.generationDifficultyTone(delta), 'neutral');
});

test('invalid metadata stays hidden and assessment explanations are plain text', () => {
  for (const assessment of [null, [], {}, { target: 0 }, { target: '800' }]) assert.equal(render(assessment), '');
  const html = render({ target: 800, estimated: 1000, explanation: '<script>synthetic</script>' });
  assert.match(html, /&lt;script&gt;synthetic&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script>/);
});
