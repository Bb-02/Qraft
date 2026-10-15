const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');
function load(relative, dependencies = {}, globals = {}) {
 const filename=path.resolve(__dirname, relative);
 const code=ts.transpileModule(fs.readFileSync(filename,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,jsx:ts.JsxEmit.React,esModuleInterop:true}}).outputText;
 const m={exports:{}};
 vm.runInNewContext(code,{module:m,exports:m.exports,React,Error,URL,URLSearchParams,...globals,require:name=>name in dependencies?dependencies[name]:require(name)},{filename});
 return m.exports;
}
const locations=load('./problem-list-location.ts',{'./constants':{DEFAULT_PAGE_SIZE:20}});
const providerErrors=load('./provider-settings-error.ts');
test('model connection 404 explains a proven protocol conflict and preserves redacted diagnostics',()=>{
 const diagnostic='provider connection check: openai-chat api error (status 404, type "provider_error"): Not Found';
 const conflict=providerErrors.providerConnectionFailureSummary(diagnostic,'https://synthetic.example/anthropic/');
 assert.match(conflict.message,/协议与地址不匹配/);
 assert.match(conflict.message,/Anthropic/);
 assert.match(conflict.message,/本次配置尚未保存/);
 assert.equal(conflict.details,diagnostic);
 for(const endpoint of ['https://synthetic.example/v1','https://anthropic.synthetic.example/v1','https://synthetic.example/v1?path=/anthropic']) {
  const unknown=providerErrors.providerConnectionFailureSummary(diagnostic,endpoint);
  assert.match(unknown.message,/请求接口或模型/);
  assert.doesNotMatch(unknown.message,/协议与地址不匹配/);
 }
 const actualAnthropic=providerErrors.providerConnectionFailureSummary(diagnostic.replace('openai-chat','anthropic-messages'),'https://synthetic.example/anthropic');
 assert.doesNotMatch(actualAnthropic.message,/协议与地址不匹配/);
 for(const text of [diagnostic.replace('status 404','status 401'),diagnostic.replace('status 404','status 429'), 'provider connection check: invalid response: '+diagnostic, '中文保存错误']) {
  assert.equal(providerErrors.providerConnectionFailureSummary(text,'https://synthetic.example/anthropic'),undefined);
 }
});

test('settings save displays a readable connection failure with collapsed secret-free technical details',async()=>{
 const states=[],effects=[],calls=[]; let cursor=0,started=false;
 const initial={base_url:'https://synthetic.example/anthropic',model:'synthetic-chat',api_key_configured:false,override_configured:true};
 const api={
  async getPermanentLLMSettings(){return {data:{statement:initial,verification:initial,review:initial}};},
  async getEmbeddingRuntimeSettings(){return {data:{base_url:'',model:'',api_key_configured:false}};},
  async updatePermanentLLMSetting(purpose,payload){
   calls.push({purpose,payload});
   throw new Error('provider connection check: openai-chat api error (status 404, type "provider_error"): Not Found synthetic-test-secret');
  },
 };
 const Page=load('../app/settings/page.tsx',{
  react:{...React,
   useState(initial){const index=cursor++;if(!(index in states))states[index]=typeof initial==='function'?initial():initial;return [states[index],value=>{states[index]=typeof value==='function'?value(states[index]):value;}];},
   useCallback:callback=>callback,
   useEffect:callback=>{if(!started)effects.push(callback);},
  },
  'next/link':({children,...props})=>React.createElement('a',props,children),
  '@/components/ui/Workspace':{PageHeader:()=>null},
  '@/components/ApiHealthBanner':()=>null,
  '@/lib/api':api,
  '@/lib/provider-settings-error':providerErrors,
 },{window:{location:{search:'?section=verification'}}}).default;
 const render=()=>{cursor=0;return Page();};
 function find(node,predicate){
  if(Array.isArray(node)){for(const child of node){const result=find(child,predicate);if(result)return result;}}
  else if(React.isValidElement(node)){if(predicate(node))return node;return find(node.props.children,predicate);}
 }
 render();started=true;effects.forEach(effect=>effect());
 await new Promise(resolve=>setImmediate(resolve));
 let card=find(render(),node=>node.props.purpose==='verification');
 assert.ok(card);
 card.props.onChange({...card.props.form,api_key:'synthetic-test-secret'});
 card=find(render(),node=>node.props.purpose==='verification');
 card.props.onSave();
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(calls.length,1);
 assert.equal(calls[0].purpose,'verification');
 assert.equal(calls[0].payload.base_url,initial.base_url);
 assert.equal(calls[0].payload.protocol,undefined);
 const alert=find(render(),node=>node.props.role==='alert');
 assert.ok(alert);
 const html=renderToStaticMarkup(alert);
 assert.match(html,/协议与地址不匹配/);
 assert.match(html,/本次配置尚未保存/);
 assert.match(html,/<details><summary[^>]*>技术详情<\/summary>/);
 assert.match(html,/provider connection check: openai-chat api error/);
 assert.match(html,/\[已隐藏\]/);
 assert.doesNotMatch(html,/synthetic-test-secret/);
});
test('list/detail/edit round trip retains pagination, filters, sort and destination set',()=>{
 const filter={page:7,size:50,search:'sum & difference',level:'algorithm',status:'draft',difficulty_min:800,difficulty_max:1200,tags:['dp','tree'],sort_by:'difficulty',sort_order:'asc'};
 const list=locations.problemListHref(filter,'synthetic-set');
 const detail=locations.problemDetailHref('synthetic-id',list);
 const edit=locations.problemDetailHref('synthetic-id',list,true);
 for (const link of [detail,edit]) {
  const back=locations.problemListReturnTo(new URL(link,'https://test.invalid').searchParams.get('returnTo'));
  assert.equal(back,list);
  assert.equal(new URL(back,'https://test.invalid').searchParams.get('set'),'synthetic-set');
  assert.equal(JSON.stringify(locations.problemFilterFromQuery(back.split('?')[1])),JSON.stringify(filter));
 }
});
test('direct visit defaults and invalid return locations are safe',()=>{
 assert.equal(locations.problemFilterFromQuery('page=-1&size=500&level=bogus').page,1);
 assert.equal(locations.problemFilterFromQuery('page=NaN').size,20);
 for(const value of [null,'//example.com','https://example.com','/problems-extra','/problems\\?page=3']) assert.equal(locations.problemListReturnTo(value),'/problems');
});
const images=load('./markdown-image.ts');
const Renderer=load('../components/MarkdownRenderer.tsx',{'@/lib/markdown-image':images,'next/dynamic':()=>()=>null}).default;
const render=content=>renderToStaticMarkup(React.createElement(Renderer,{content}));
test('Markdown pictures render in text, lists and tables, including empty alt and URL parentheses',()=>{
 for(const content of ['![diagram](https://example.test/plot(a).png "A plot")','- ![](https://example.test/a.png)','| a | b |\n|---|---|\n| ![x](./a.png) | text |']) {
  assert.match(render(content),/<img /);
 }
 const html=render('Before ![figure](https://example.test/a.png "Title") after');
 assert.match(html,/alt="figure"/);assert.match(html,/title="Title"/);assert.match(html,/Before /);assert.match(html,/ after/);
});
test('HTML images retain safe attributes without forwarding scripts or unsafe sources',()=>{
 const html=render('<img src="https://example.test/a.png?x=1&amp;y=2" alt="figure" onerror="alert(1)">');
 assert.match(html,/<img /);assert.doesNotMatch(html,/onerror|alert\(/);assert.match(html,/x=1&amp;y=2/);
 for(const source of ['javascript:alert(1)','data:text/html,hello','file:///tmp/secret','java\nscript:alert(1)','\\\\example.test/a']) assert.equal(images.safeImageSource(source),null);
 assert.doesNotMatch(render('<img src="javascript:evil" onerror="evil">'),/<img /);
});
test('code examples containing image syntax remain code',()=>{
 assert.doesNotMatch(render('```md\n![alt](https://example.test/a.png)\n```'),/<img /);
 assert.doesNotMatch(render('`![alt](https://example.test/a.png)`'),/<img /);
});

test('longer code fences preserve shorter backtick tokens and the following heading',()=>{
 for (const [opening, literal, closing] of [['````text','```','````'],['`````text','````','`````'],['````text','```','`````']]) {
  const body=literal+'\n```text\n`````text\n# literal heading\n![literal](https://example.test/a.png)\n$x$';
  const html=render(opening+'\n'+body+'\n  '+closing+'  \n\n## 样例解释\n后续说明');
  assert.match(html,/<code data-lang="text">/);
  assert.ok(html.includes('<code data-lang="text">'+body+'</code>'));
  assert.match(html,/<\/pre><h2[^>]*>样例解释<\/h2>/);
  assert.match(html,/后续说明<\/p>/);
  assert.equal((html.match(/<pre\b/g)||[]).length,1);
  assert.doesNotMatch(html,/<img |<h1/);
 }
});

test('ordinary cpp fences keep their language and require a pure closing fence',()=>{
 const body='int main() { return 0; }\n```text\n``';
 const html=render('```cpp\n'+body+'\n```\n## 解法\n![figure](https://example.test/a.png)');
 assert.ok(html.includes('<code data-lang="cpp">'+body+'</code>'));
 assert.match(html,/<\/pre><h2[^>]*>解法<\/h2>/);
 assert.match(html,/<img /);
 assert.doesNotMatch(html,/data-lang="`/);
});
