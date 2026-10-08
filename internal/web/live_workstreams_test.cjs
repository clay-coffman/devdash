const { test } = require('node:test');
const assert = require('node:assert/strict');
const { app, get, run, all } = require('./dom_support.cjs');
const at = () => new Date().toISOString();
function work(agents = [], streams = [], overrides = {}) {
  const time = at(), ready = { state: 'ready', has_data: true, stale: false, last_success: time };
  const declarations = streams.flatMap(s => (s.derived?.tasks || []).flatMap(t => (t.checkouts || []).filter(c => !c.observer_only).map(c => ({s,t,c}))));
  const memberships = declarations.filter(({c}) => declarations.filter(other => other.c.path === c.path).length === 1).map(({s,t,c}) =>
    ({checkout:c.path,stream_key:s.key,task_key:t.key,panes:agents.filter(a => a.checkout === c.path).map(a => a.pane),board_panes:[]}));
  return { version: 1, now: time, sweep_at: time, board: { ...ready }, herdr: { ...ready },
    agents, workstreams: streams, memberships, activity: [], board_agents: [], ...overrides };
}
const agent = (pane, checkout, status = 'working', extras = {}) => ({ pane, checkout, cwd: checkout || '/scratch/session', status, name: pane, workspace: 'w1', ...extras });
function stream(id, path, panes = [], overrides = {}) {
  const time = at();
  return { key: id, id, name: 'Stream ' + id, repo: '/repository', stage: 'Needs reconciliation', live_panes: panes, prs: [],
    derived: { version: 1, section: 'Now', runtime_known: true, runtime_observed_at: time, fact_max_age: '5m',
      tasks: [{ key: id + '|task', id: id + '-TASK', intent: 'active', live_panes: panes, checkouts: [{ path }] }] }, ...overrides };
}
function co(path, port = 0) { return { path, display: path, branch: 'branch', agents: [], stacks: [], processes: port ?
  [{ pid: port, name: 'node', cmd: 'vite', cwd: path + '/web', ports: [port], rss: 1, cpu: 0 }] : [], proc_bytes: 1, total_bytes: 1 }; }
function state(checkouts = [], errors = []) { return { now: at(), checkouts, errors, docker: { available: !errors.some(e => e.startsWith('docker:')) } }; }
function setup(initial = {}) {
  const c = app(initial); c.document.querySelectorAll = selector => selector === '[data-key]' ? all(get(c, 'work-list')).filter(n => n.hasAttribute('data-key')) : [];
  run(c, 'let fixtureClock=100;globalThis.performance={now:()=>fixtureClock}');
  get(c, 'view-work').onclick(); return c;
}
function ingest(c, w, s) { c.receiveWork(w); c.receiveState(s); c.renderOverview(); }
function titles(c) { return all(get(c, 'work-list')).filter(n => n.className === 'work-row-title' && n.parentNode?.className === 'work-row' && !n.parentNode.getAttribute('data-key')?.includes('|task')).map(n => n.textContent); }
function links(c) { return all(get(c, 'work-list')).filter(n => n.tagName === 'A' && n.className === 'view'); }

test('saved Workstreams view boots after shared freshness script and defaults to Live', () => {
  const c=app({'devdash.view':'workstreams','devdash.work.scope':'unrecognized'});
  c.receiveWork(work([agent('p','/repo')],[]));
  assert.equal(get(c,'work-view').hidden,false);assert.match(get(c,'work-list').textContent,/Ungrouped activity/);
  assert.equal(get(c,'work-scope-live').getAttribute('aria-pressed'),'true');
});

test('24 stale declarations stay out of Live; All registered retains every one with honest collapsed sections', () => {
  const c = setup(), streams = Array.from({ length: 24 }, (_, i) => stream('s' + i, '/repo/' + i));
  for (let i = 19; i < 24; i++) streams[i].derived.section = i < 22 ? 'Parked' : 'Completed/canceled';
  const w = work([agent('p1', '/repo/0')], streams); w.board.stale = true;
  ingest(c, w, state([co('/repo/0', 5173)]));
  assert.equal(titles(c).length, 1); assert.match(get(c, 'work-list').textContent, /Ungrouped activity/);
  assert.doesNotMatch(get(c, 'work-list').textContent, /\bNow\b/);
  assert.equal(links(c).length, 1); assert.match(get(c, 'work-notice').textContent, /Grouping outdated/);
  get(c, 'work-scope-all').onclick(); assert.equal(titles(c).length, 24);
  assert.match(get(c, 'work-list').textContent, /Activity unverified/); assert.doesNotMatch(get(c, 'work-list').textContent, /\bNow\b|Needs input/);
  assert.equal(c.storage.get('devdash.work.scope'), 'all'); assert.equal(get(c, 'work-list').firstChild.open, false);
});

test('fresh exact membership groups active agents and app-only; inactive, non-Git and observer-only stay honest', () => {
  const c = setup(), a = stream('active', '/repo/active', ['p1']), appOnly = stream('app', '/repo/app'),
    observer = stream('observer', '/repo/observer'); observer.derived.tasks[0].checkouts[0].observer_only = true;
  const parked = stream('parked', '/repo/parked', ['p2']); parked.derived.section = 'Parked';
  ingest(c, work([agent('p1','/repo/active'), agent('p2','/repo/parked','blocked'),
    agent('p3','/scratch','working',{checkout:'',cwd:'/scratch'}), agent('p4','/repo/quiet','idle')],
  [a, appOnly, observer, parked, stream('quiet','/repo/quiet')]),
  state([co('/repo/active',5173),co('/repo/app',4000),co('/repo/observer',6000),co('/repo/parked'),co('/repo/quiet')]));
  assert.deepEqual(titles(c), ['Stream active','Stream parked','Stream app','scratch','observer']);
  assert.equal(links(c).length,3); assert.match(get(c,'work-list').textContent,/declared parked \(unchanged\)/);
  assert.doesNotMatch(get(c,'work-list').textContent,/Stream quiet/);
  assert.equal(all(get(c,'work-list')).filter(n=>n.textContent === 'Stream active').length >= 1,true);
  c.receiveWork(work([agent('p1','/repo/active')],[a,appOnly,stream('duplicate','/repo/active')],{activity:[]}));
  assert.match(get(c,'work-list').textContent,/Ungrouped activity/); assert.doesNotMatch(get(c,'work-list').textContent,/Stream active/);
});

test('fresh success removes absent apps; Docker warnings do not; failures and aging retain last known and recover', async () => {
  const c = setup(), w = work([],[]);
  ingest(c,w,state([co('/repo/app',5173)])); assert.equal(links(c).length,1);
  c.receiveState(state([co('/repo/app',3000)],['docker stats: unavailable'])); c.renderOverview();
  assert.equal(links(c).length,1); assert.match(links(c)[0].getAttribute('href'),/:3000/);
  c.fetch=async url=>{ throw Error(url.includes('work') ? 'work down' : 'state down'); };
  await c.workTick(); await c.tick(); assert.equal(links(c).length,1); assert.match(get(c,'work-list').textContent,/apps last known/);
  run(c,'fixtureClock+=16000'); c.intervals.at(-1)();
  assert.match(get(c,'work-list').textContent,/apps last known/);
  c.receiveState(state([]));c.renderOverview();assert.equal(links(c).length,0);
  c.receiveWork(work([agent('p1','/scratch','blocked',{checkout:'',cwd:'/scratch'})],[]));
  assert.match(get(c,'work-list').textContent,/Needs input/);
  c.receiveWork(work([],[]));assert.doesNotMatch(get(c,'work-list').textContent,/Needs input/);
});

test('scope, mode, filters, inline focus and disclosures survive polling; hidden inspector never attaches to another row', () => {
  const c=setup(), s=stream('s','/repo/s',['p1']);
  ingest(c,work([agent('p1','/repo/s')],[s]),state([co('/repo/s',5173)]));
  c.chooseWork('s'); const panel=get(c,'work-detail'); assert.equal(panel.hidden,false);
  const slot=panel.parentNode, row=all(get(c,'work-list')).find(n=>n.getAttribute('data-key')==='s');row.focus();
  const section=get(c,'work-list').firstChild; section.open=true;
  get(c,'work-search').value='unrelated';get(c,'work-search').oninput();assert.equal(panel.hidden,true);
  assert.notStrictEqual(panel.parentNode, get(c,'work-list').firstChild);
  get(c,'work-search').value='';get(c,'work-search').oninput();assert.equal(panel.hidden,false);
  assert.equal(panel.parentNode.getAttribute('data-key'),'work-detail-slot|s');
  get(c,'work-repo').value='/other';get(c,'work-repo').onchange();assert.equal(panel.hidden,true);
  get(c,'work-repo').value='';get(c,'work-repo').onchange();
  c.selectDetailTab('resources');const detail=all(get(c,'work-detail-body')).find(n=>n.className.includes('card-resources'));detail.open=true;detail.dispatchEvent({type:'toggle'});
  const surviving=all(get(c,'work-list')).find(n=>n.getAttribute('data-key')==='s');surviving.focus();
  c.receiveState(state([co('/repo/s',3000)]));c.renderOverview();assert.strictEqual(all(get(c,'work-list')).find(n=>n.getAttribute('data-key')==='s'),surviving);
  assert.equal(surviving.focused,true);assert.equal(section.open,true);assert.equal(detail.open,true);assert.equal(get(c,'work-detail-body').textContent.includes('3000'),true);
  get(c,'view-overview').onclick();assert.equal(panel.hidden,true);get(c,'view-work').onclick();assert.equal(panel.hidden,false);
  get(c,'work-scope-all').onclick();assert.equal(panel.hidden,false);assert.equal(c.storage.get('devdash.work.scope'),'all');
  get(c,'work-scope-live').onclick();assert.equal(panel.hidden,false);assert.notStrictEqual(panel.parentNode,slot);
});

test('validated endpoint ingress, not work-only fake rendering, controls freshness and provider absence', async () => {
  const c=setup(), initial=work([agent('p','/repo')],[]), s=state([co('/repo',5173)]);
  // The harness's boot fetch intentionally never settles; clear that synthetic
  // in-flight flag before exercising the real workTick successful-response path.
  run(c,'workPolling=false');
  c.fetch=async url=>({ok:true,json:async()=>url==='/api/work'?initial:s});await c.workTick();c.receiveState(s);c.renderOverview();
  assert.equal(links(c).length,1);assert.match(get(c,'work-list').textContent,/Working/);
  const failed=work([],[],{board:{state:'missing',has_data:false,stale:true},herdr:{state:'error',has_data:false,stale:true}});
  c.fetch=async url=>({ok:true,json:async()=>url==='/api/work'?failed:{...s,errors:['procs: unavailable'],checkouts:[]}});
  await c.workTick();c.receiveState({...s,errors:['procs: unavailable'],checkouts:[]});c.renderOverview();assert.equal(links(c).length,1);assert.match(get(c,'work-list').textContent,/last known/);
  c.fetch=async url=>({ok:true,json:async()=>url==='/api/work'?work([],[]):state([])});
  await c.workTick();c.receiveState(state([]));c.renderOverview();assert.equal(links(c).length,0);assert.equal(titles(c).length,0);
});
