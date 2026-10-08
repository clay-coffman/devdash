const { test } = require('node:test');
const assert = require('node:assert/strict');
const { app, get, run, all } = require('./dom_support.cjs');
const GiB = 1024 ** 3;
const checkout = (path, ports = [], branch = 'main') => ({ path, display: path, branch, agents: [], processes: ports.map((port, i) => ({ pid: port, name: 'node', cmd: 'vite', cwd: path + '/' + (i ? 'admin-web' : 'web'), ports: [port], cpu: 1, rss: GiB })), stacks: [], total_bytes: GiB, proc_bytes: GiB, working: 0 });
function projection(agents = []) {
  const at = new Date().toISOString(), ready = { state: 'ready', available: true, has_data: true, stale: false, last_success: at };
  return { version: 1, now: at, sweep_at: at, board: { ...ready }, herdr: { ...ready }, agents, workstreams: [], activity: [], board_agents: [] };
}
function observe(c, w, checkouts = []) {
  c.receiveWork(w);
  c.receiveState({now:new Date().toISOString(),checkouts}); c.renderOverview();
}
const agent = (pane, path, status = 'working', extras = {}) => ({ pane, checkout: path, cwd: path, status, workspace: 'w1', name: pane, ...extras });
const rows = c => get(c,'overview-list').children;
const visible = c => rows(c).filter(n => !n.hasAttribute('hidden'));
const key = path => JSON.stringify(['checkout',path]);

test('old Work migrates to Overview; Resources and new Workstreams persist; polling never changes views', () => {
  const c = app({'devdash.view':'work'},true);
  assert.equal(c.storage.get('devdash.view'),'overview'); assert.equal(get(c,'overview-view').hidden,false);
  get(c,'view-resources').onclick(); observe(c,projection()); assert.equal(get(c,'resources-view').hidden,false);
  const remembered = app({'devdash.view':'resources'},true); assert.equal(get(remembered,'overview-view').hidden,true);
  get(c,'view-work').onclick(); assert.equal(c.storage.get('devdash.view'),'workstreams'); c.receiveWork(projection()); assert.equal(get(c,'work-view').hidden,false);
  const absent = app({},true), w = projection(); w.herdr = {state:'missing'}; w.board={state:'missing'}; observe(absent,w);
  assert.equal(get(absent,'overview-view').hidden,false); assert.match(get(absent,'overview-counts').textContent,/Working unknown/); assert.doesNotMatch(get(absent,'overview-counts').textContent,/0 working/);
});

test('initial response order cannot admit app rows before blocked/working priority is known', () => {
  const c=app({},true);
  c.receiveState({now:new Date().toISOString(),checkouts:[checkout('/app-first',[5173])]});c.renderOverview();
  assert.equal(visible(c).length,0,'wait for both endpoints to settle');
  c.receiveWork(projection([agent('blocked','/blocked','blocked'),agent('working','/working')]));
  assert.deepEqual(visible(c).map(n=>n.getAttribute('data-key')),[key('/blocked'),key('/working'),key('/app-first')]);
});

test('exact checkout joins, short duplicate labels, app-only rows, foreground location and non-Git identities', () => {
  const c = app({},true), w = projection([
    agent('p1','/one/repo'),agent('p2','/one/repo','blocked'),agent('p3','/two/repo','idle'),
    agent('scratch1','','working',{cwd:'/scratch',workspace:'w1'}),agent('scratch2','','working',{cwd:'/scratch',workspace:'w2'})]);
  observe(c,w,[checkout('/one/repo',[5173,3000]),checkout('/two/repo',[]),checkout('/app-only',[8080]),checkout('/one/repo-neighbor',[9000])]);
  assert.equal(visible(c).length,5); assert.equal(rows(c).length,6);
  const one=rows(c).find(n=>n.getAttribute('data-key')===key('/one/repo'));
  assert.match(one.textContent,/one\/repo/); assert.match(one.textContent,/Needs input/); assert.match(one.textContent,/Working/);
  const links=all(one).filter(n=>n.className==='view'); assert.equal(links[0].getAttribute('href'),'http://localhost:5173/'); assert.equal(links[1].getAttribute('href'),'http://localhost:3000/');
  assert.doesNotMatch(one.textContent,/9000/); assert.match(get(c,'overview-counts').textContent,/3 working/); assert.match(get(c,'overview-counts').textContent,/1 needs input/); assert.match(get(c,'overview-counts').textContent,/4 apps/);
  const nonGit=visible(c).filter(n=>n.getAttribute('data-key').includes('non-git'));assert.equal(nonGit.length,2);assert.notEqual(nonGit[0].getAttribute('data-key'),nonGit[1].getAttribute('data-key'));
  const co=checkout('/one/repo',[1111]);co.processes[0].cwd='/one/repo-neighbor/web';assert.equal(c.checkoutApps(co).views.length,0,'prefix neighbor must not join');
  assert.doesNotMatch(readSource(),/foreground_cwd/,'backend normalized cwd remains the sole fallback location');
});
function readSource(){return require('node:fs').readFileSync(`${__dirname}/static/overview.js`,'utf8').replace('// cwd is already normalized by the backend to foreground_cwd with fallback.','');}

test('initial blocked/working/apps priority freezes admission and order; new activity appends; Refresh folds quiet rows', () => {
  const c=app({},true), w=projection([agent('quiet','/quiet','done'),agent('working','/working'),agent('blocked','/blocked','blocked')]);
  const checkouts=[checkout('/quiet'),checkout('/apps',[5173])];observe(c,w,checkouts);
  assert.deepEqual(visible(c).map(n=>n.getAttribute('data-key')),[key('/blocked'),key('/working'),key('/apps')]);
  w.agents.find(a=>a.pane==='blocked').status='done';w.agents.find(a=>a.pane==='quiet').status='blocked';w.agents.reverse();observe(c,w,[...checkouts].reverse());
  assert.deepEqual(visible(c).map(n=>n.getAttribute('data-key')),[key('/blocked'),key('/working'),key('/apps'),key('/quiet')]);
  get(c,'overview-refresh').onclick();assert.deepEqual(visible(c).map(n=>n.getAttribute('data-key')),[key('/quiet'),key('/working'),key('/apps')]);
  assert.match(get(c,'overview-other').textContent,/\(1\)/);get(c,'overview-other').onclick();assert.equal(visible(c).length,4);assert.match(visible(c).at(-1).textContent,/Agent finished/);
});

test('stale/error/missing Herdr retains last known rows and unknown counts; only fresh successful absence removes them', () => {
  for(const state of ['ready','error','missing']) {
    const c=app({},true), w=projection([agent('p1','/repo','blocked')]);observe(c,w);
    const degraded={...w,herdr:{...w.herdr,state,stale:true,has_data:state!=='missing',error:'synthetic failed read'},agents:[]};c.receiveWork(degraded);
    assert.equal(visible(c).length,1);assert.match(all(visible(c)[0]).find(n=>n.className==='overview-agents').textContent,/^Last known/);
    assert.match(get(c,'overview-counts').textContent,/Input status unknown/);assert.match(get(c,'source-notice').textContent,/agent status unknown/);
    c.receiveWork(projection([]));assert.equal(rows(c).length,0);
  }
});

test('resource HTTP failure, process collector errors and aged receipts retain app-only rows; successful absence removes them', () => {
  const c=app({},true);observe(c,projection(),[checkout('/apps',[5173])]);
  run(c,"stateFetchError='HTTP 503';lastState={now:new Date().toISOString(),checkouts:[]};renderOverview();");
  assert.equal(visible(c).length,1);assert.match(get(c,'overview-counts').textContent,/1 last known/);assert.match(visible(c)[0].textContent,/Last known ports/);
  c.receiveState({now:new Date().toISOString(),checkouts:[],errors:['procs: failed']});c.renderOverview();assert.equal(visible(c).length,1);
  run(c,"stateReceivedAt-=60000;renderOverview();");assert.equal(visible(c).length,1);
  c.receiveState({now:new Date().toISOString(),checkouts:[]});c.renderOverview();assert.equal(rows(c).length,0);
});

test('search/repository selection, DOM focus, inline disclosure and scroll retain identity through polling', () => {
  const c=app({},true), w=projection([agent('p1','/repo','working',{repository:'/projects/repo'})]);observe(c,w,[checkout('/repo',[5173])]);
  const row=rows(c)[0],d=all(row).find(n=>n.getAttribute('data-key')==='details|'+key('/repo'));
  d.open=true;d.dispatchEvent({type:'toggle'});const summary=d.firstChild;summary.focus();get(c,'overview-list').scrollTop=45;
  get(c,'overview-search').value='p1';get(c,'overview-repo').value='/projects/repo';w.agents[0].status='idle';observe(c,w,[checkout('/repo',[5173])]);
  assert.strictEqual(rows(c)[0],row);assert.strictEqual(all(row).find(n=>n===d),d);assert.equal(d.open,true);assert.equal(summary.focused,true);assert.equal(get(c,'overview-list').scrollTop,45);
  assert.equal(get(c,'overview-search').value,'p1');assert.equal(get(c,'overview-repo').value,'/projects/repo');
  get(c,'overview-search').value='no match';get(c,'overview-search').oninput();assert.equal(visible(c).length,0);assert.strictEqual(rows(c)[0],row,'filters hide rather than destroy rows');
  get(c,'overview-search').value='5173';get(c,'overview-search').oninput();assert.equal(visible(c).length,1);assert.equal(d.open,true);
});

test('only fresh, exact and unambiguous API membership decorates checkout; duplicate task IDs/branches/workspaces never join', () => {
  const c=app({},true), w=projection([agent('pane','/one/repo','blocked')]);
  const task={key:'task1',id:'TASK-1',live_panes:['pane'],checkouts:[{path:'/one/repo'}],pr_references:[]};
  w.workstreams=[{key:'stream1',name:'First',repo:'/projects/one',derived:{version:1,tasks:[task]},prs:[]}];w.memberships=[{checkout:'/one/repo',stream_key:'stream1',task_key:'task1',panes:['pane'],board_panes:[]}];observe(c,w,[checkout('/one/repo',[5173]),checkout('/two/repo',[8080])]);
  const row=rows(c).find(n=>n.getAttribute('data-key')===key('/one/repo'));assert.match(row.textContent,/TASK-1/);assert.doesNotMatch(rows(c).find(n=>n.getAttribute('data-key')===key('/two/repo')).textContent,/TASK-1/);
  w.workstreams.push({...w.workstreams[0],key:'stream2',name:'Second',derived:{version:1,tasks:[{...task,key:'task2'}]}});w.memberships=[];c.receiveWork(w);assert.doesNotMatch(row.textContent,/TASK-1/,'ambiguous membership withheld');
  w.workstreams.pop();w.board.stale=true;c.receiveWork(w);assert.doesNotMatch(row.textContent,/TASK-1/);assert.match(get(c,'source-notice').textContent,/Board cache stale/);
  w.board={...w.board,state:'error',error:'failed board read'};c.receiveWork(w);assert.equal(visible(c).length,2);assert.match(get(c,'work-sources').textContent,/last good retained/);
  w.board={...w.board,state:'ready',stale:false,error:''};w.memberships=[{checkout:'/one/repo',stream_key:'stream1',task_key:'task1',panes:['pane'],board_panes:[]}];c.receiveWork(w);assert.match(row.textContent,/TASK-1/);
});

test('board estimates and initial endpoint failure never invent live activity or zero status', () => {
  const c=app({},true);run(c,"workFetchError='503';renderOverview();");assert.match(get(c,'overview-counts').textContent,/Working unknown/);assert.match(get(c,'source-notice').textContent,/Work API unavailable/);
  const w=projection([agent('p1','/idle','idle')]);w.board_agents=[{observation:{pane:'p1',checkout:'/idle',cwd:'/idle'},meta:{collected_at:w.now},assessment:{waiting_on:'clay',attention_score:1,finished:1}}];
  run(c,"workFetchError='';");observe(c,w);assert.equal(visible(c).length,0);assert.match(get(c,'overview-counts').textContent,/0 needs input/);assert.doesNotMatch(get(c,'overview-list').textContent,/Needs input/);
});

test('optional provider errors do not turn fresh process/app counts into unknown', () => {
  const c=app({},true),w=projection();w.herdr={state:'missing'};w.board={state:'missing'};observe(c,w,[checkout('/apps',[5173])]);
  run(c,"lastState.errors=['herdr: not installed','reaper: not installed','docker df: not installed'];renderOverview();");
  assert.match(get(c,'overview-counts').textContent,/1 app/);assert.doesNotMatch(get(c,'overview-counts').textContent,/Apps unknown/);
});

test('failed matching resource payload cannot erase known app links; cached plan details retain focus on board failure', () => {
  const c=app({},true),w=projection([agent('p1','/repo')]);
  w.workstreams=[{key:'s',name:'Delivery',repo:'/projects/repo',prs:[],derived:{version:1,tasks:[{id:'TASK-1',key:'t',live_panes:['p1'],checkouts:[{path:'/repo',observed_at:w.now,plan:{path:'/repo/plan.md',done:1,open:2,blocked:0}}]}]}}];
  w.memberships=[{checkout:'/repo',stream_key:'s',task_key:'t',panes:['p1'],board_panes:[]}];observe(c,w,[checkout('/repo',[5173])]);const row=rows(c)[0],plan=all(row).find(n=>n.className==='work-plan'),planPath=all(plan).find(n=>n.tagName==='DETAILS');planPath.open=true;planPath.firstChild.focus();
  w.board={...w.board,state:'error',stale:true};w.workstreams=[];c.receiveWork(w);
  assert.strictEqual(all(row).find(n=>n.className==='work-plan'),plan);assert.equal(planPath.open,true);assert.equal(planPath.firstChild.focused,true);assert.match(row.textContent,/last known compatible facts/);
  c.receiveState({now:new Date().toISOString(),errors:['procs: failed'],checkouts:[checkout('/repo')]});c.renderOverview();
  assert.match(row.textContent,/5173/);assert.match(row.textContent,/Last known ports/);
});

test('Overview, task, stream and Resources maintain separate exact disclosure namespaces and confirmed controls', () => {
  const c=app({},true), w=projection([agent('p1','/repo')]);observe(c,w,[checkout('/repo',[5173])]);
  const row=rows(c)[0],resource=all(row).find(n=>n.className.includes('card-resources'));resource.open=true;resource.dispatchEvent({type:'toggle'});
  const main=all(c.card(run(c,'lastState.checkouts[0]'),false)).find(n=>n.className.includes('card-resources'));assert.equal(main.open,false);
  c.renderOverview();assert.strictEqual(all(row).find(n=>n.className.includes('card-resources')),resource);assert.equal(resource.open,true);
  const kill=all(row).find(n=>n.tagName==='BUTTON'&&n.textContent==='Kill');let called='';c.confirmAction=kind=>{called=kind};kill.onclick();assert.equal(called,'TERM');
});
