// Focused review regressions at validated response and keyed DOM seams.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { app, get, run, all } = require('./dom_support.cjs');
const GiB=1024**3;
const proc=(pid,port,bytes=GiB)=>({pid,name:'node',cmd:'vite',cwd:'/repo/web',ports:[port],rss:bytes,cpu:1});
const co=(port=5173,bytes=GiB)=>({path:'/repo',display:'/repo',branch:'main',agents:[],processes:port?[proc(10,port,bytes)]:[],proc_bytes:port?bytes:0,total_bytes:port?bytes:0,stacks:[],container_bytes:0});
function work(at=new Date().toISOString(),status='working') {const ready={state:'ready',has_data:true,stale:false,last_success:at};return{version:1,now:at,sweep_at:at,board:{...ready},herdr:{...ready},workstreams:[],board_agents:[],activity:[],agents:status?[{pane:'p1',name:'builder',checkout:'/repo',cwd:'/repo',workspace:'w1',status}]:[]};}
function state(checkouts=[co()],errors=[],at=new Date().toISOString(),available=true){return{now:at,checkouts,errors,docker:{available}};}
function setup(){const c=app({},true);run(c,'let fixtureClock=100;globalThis.performance={now:()=>fixtureClock};');return c;}
function receive(c,w,s){c.receiveWork(w);c.receiveState(s);c.renderOverview();}
const row=c=>get(c,'overview-list').firstChild;

test('R1: Docker stats/absence cannot freeze changed processes, app ports or process memory; stopped apps disappear',()=>{
 for(const error of ['docker stats: timeout','docker: not installed']){
  const c=setup();receive(c,work(undefined,''),state());const before=row(c);
  c.receiveState(state([co(3000,9*GiB)],[error],undefined,!error.startsWith('docker:')));c.renderOverview();
  assert.strictEqual(row(c),before);assert.match(row(c).textContent,/3000/);assert.doesNotMatch(row(c).textContent,/5173/);assert.match(row(c).textContent,/9\.0 GiB/);
  assert.match(row(c).textContent,/containers unknown/);assert.match(get(c,'overview-counts').textContent,/1 app/);assert.doesNotMatch(get(c,'overview-counts').textContent,/Apps unknown/);
  c.receiveState(state([co(0)],[error]));c.renderOverview();assert.equal(all(row(c)).filter(n=>n.className==='view').length,0);assert.match(get(c,'overview-counts').textContent,/0 apps/);
  c.receiveState(state([],[error]));c.renderOverview();assert.equal(get(c,'overview-list').children.length,0,'successful process absence removes app-only checkout even without Docker');
 }
});
test('R1: failed process payload and HTTP cached redraw cannot erase or overwrite known apps; container changes remain independent',()=>{
 const c=setup();receive(c,work(),state());
 c.receiveState(state([co(3000,9*GiB)],['procs: failed']));c.renderOverview();assert.match(row(c).textContent,/5173/);assert.doesNotMatch(row(c).textContent,/3000/);
 c.receiveState(state([co(0)],['procs: failed']));c.renderOverview();assert.match(row(c).textContent,/5173/);
 const revision=run(c,'stateRevision');run(c,"stateFetchError='503';renderOverview();");assert.equal(run(c,'stateRevision'),revision);assert.match(row(c).textContent,/5173/);
 c.receiveState(state([co(3000,9*GiB)]));c.renderOverview();assert.match(row(c).textContent,/3000/);assert.doesNotMatch(row(c).textContent,/5173/);
});
test('R1: process failure retains ports while valid Docker stats update; Docker failure retains only container entities',()=>{
 const c=setup();const first=co();first.stacks=[{project:'db',class:'live',bytes:GiB,running:0,containers:[]}];first.container_bytes=GiB;receive(c,work(undefined,''),state([first]));
 const second=co(0);second.stacks=[{...first.stacks[0],bytes:2*GiB}];second.container_bytes=2*GiB;c.receiveState(state([second],['procs: failed']));c.renderOverview();assert.match(row(c).textContent,/5173/);assert.equal(run(c,'overviewResources.get("/repo").total_bytes'),3*GiB);
 c.receiveState(state([],['docker stats: failed']));c.renderOverview();assert.equal(all(row(c)).filter(n=>n.className==='view').length,0);assert.equal(run(c,'overviewResources.get("/repo").proc_bytes'),0);assert.equal(run(c,'overviewResources.get("/repo").container_bytes'),2*GiB);assert.match(row(c).textContent,/containers unknown/);
 c.receiveState(state([]));c.renderOverview();assert.equal(get(c,'overview-list').children.length,0);
});

test('R1: newest usable retained agent values win, failed arrays do not retarget them, successful empty clears',()=>{
 const c=setup();receive(c,work(),state([]));const newer=work(undefined,'blocked');newer.herdr.stale=true;c.receiveWork(newer);assert.match(row(c).textContent,/Last known.*Needs input/);
 const failed=work(undefined,'idle');failed.herdr.state='error';failed.herdr.stale=true;c.receiveWork(failed);assert.match(row(c).textContent,/Last known.*Needs input/);assert.doesNotMatch(row(c).textContent,/Last known.*Idle/);
 c.receiveWork(work(undefined,''));assert.equal(get(c,'overview-list').children.length,0);
});
test('R2: receipt age ignores ahead/behind server clocks and respects server-stale board/Herdr',()=>{
 for(const skew of [-3600000,3600000]){const c=setup(),at=new Date(Date.now()+skew).toISOString();receive(c,work(at),state([co()],[],at));assert.match(get(c,'overview-counts').textContent,/1 working/);assert.match(get(c,'overview-counts').textContent,/1 app/);assert.equal(c.boardFresh(),true);
 const stale=work(at);stale.herdr.stale=true;stale.board.stale=true;c.receiveWork(stale);assert.equal(c.boardFresh(),false);assert.match(get(c,'overview-counts').textContent,/Working unknown/);}
});
test('R2: cached renders/search do not renew receipt age, errors retain original timestamps, validated recovery renews locally',async()=>{
 const c=setup(),w=work();receive(c,w,state());const workReceipt=run(c,'workReceivedAt'),resourceReceipt=run(c,'stateReceivedAt');
 run(c,'fixtureClock+=16000;renderWork(lastWork);renderOverview();');get(c,'overview-search').oninput();assert.equal(run(c,'workReceivedAt'),workReceipt);assert.equal(run(c,'stateReceivedAt'),resourceReceipt);assert.match(get(c,'overview-counts').textContent,/Working unknown/);assert.match(get(c,'overview-counts').textContent,/Apps unknown/);
 c.fetch=async()=>{throw new Error('503')};await c.workTick();await c.tick();assert.equal(run(c,'workReceivedAt'),workReceipt);assert.equal(run(c,'stateReceivedAt'),resourceReceipt);
 receive(c,work(),state());assert.match(get(c,'overview-counts').textContent,/1 working/);assert.match(get(c,'overview-counts').textContent,/1 app/);
 const stale=work();stale.board.stale=true;stale.sweep_at=w.sweep_at;c.receiveWork(stale);const sweep=run(c,'lastWork.sweep_at');run(c,'fixtureClock+=1000;');c.receiveWork({...stale});assert.equal(c.boardFresh(),false);assert.equal(run(c,'lastWork.sweep_at'),sweep);
 const receipt=run(c,'workReceivedAt'),stateReceipt=run(c,'stateReceivedAt');c.fetch=async()=>({ok:true,json:async()=>({version:99})});await c.workTick();await c.tick();assert.equal(run(c,'workReceivedAt'),receipt,'invalid response is not receipt');assert.equal(run(c,'stateReceivedAt'),stateReceipt);
});
test('W1: filtering hides the inspector immediately and restores its exact slot without a poll',()=>{
 const c=setup();c.document.querySelectorAll=selector=>selector==='[data-key]'?all(get(c,'work-list')).filter(n=>n.hasAttribute('data-key')):[];
 const w=work();w.workstreams=[{key:'s',name:'Delivery',repo:'/projects/repo',stage:'Working',derived:{version:1,section:'Now',tasks:[]},prs:[],live_panes:[]}];c.receiveWork(w);get(c,'view-work').onclick();get(c,'work-scope-all').onclick();c.chooseWork('s');
 const panel=get(c,'work-detail'),slot=panel.parentNode;assert.equal(slot.getAttribute('data-key'),'work-detail-slot|s');
 get(c,'work-search').value='unrelated';get(c,'work-search').oninput();assert.equal(panel.hidden,true,'hide immediately, not after polling');c.renderWork(w);assert.equal(panel.hidden,true,'no orphan under unrelated rows');
 get(c,'work-search').value='';get(c,'work-search').oninput();assert.equal(panel.hidden,false);assert.equal(panel.parentNode.getAttribute('data-key'),'work-detail-slot|s');assert.notStrictEqual(panel.parentNode,slot);
 get(c,'work-repo').value='/different';get(c,'work-repo').onchange();assert.equal(panel.hidden,true);get(c,'work-repo').value='';get(c,'work-repo').onchange();assert.equal(panel.hidden,false);
});
test('L1/L2: nested overview sparks are hidden and shared link/danger/success text meets contrast',()=>{
 const css=require('node:fs').readFileSync(__dirname+'/static/style.css','utf8');assert.match(css,/\.overview-detail-body \.co-spark\s*\{\s*display:\s*none/);
 const luminance=hex=>{const rgb=hex.match(/\w\w/g).map(v=>parseInt(v,16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);return .2126*rgb[0]+.7152*rgb[1]+.0722*rgb[2]};
 for(const selector of ['a','button.danger','.chip.bare.t-ok']){const escaped=selector.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');const match=css.match(new RegExp('(?:^|\\n)'+escaped+' \\{[^}]*color: light-dark\\(#([a-f0-9]{6}), #([a-f0-9]{6})\\)'));assert.ok(match,selector+' has theme-specific readable text');for(let i=1;i<3;i++){const a=luminance(match[i]),b=luminance(i===1?'ffffff':'121212');assert.ok((Math.max(a,b)+.05)/(Math.min(a,b)+.05)>=4.5,selector);}}
});

test('L3: keyed process controls keep PID/action identity through reorder; a disappeared PID is removed, never retargeted',()=>{
 const c=setup(),parent=c.el('div'),scope={path:'/repo',working:1};let selected; c.confirmAction=(kind,target,own)=>{selected={kind,pid:target.pid,own}};
 const p1=proc(10,5173),p2=proc(20,3000);parent.append(c.processTable([p1,p2],scope));const kill=all(parent).find(n=>n.tagName==='BUTTON');kill.focus();
 c.syncChildren(parent,[c.processTable([p2,{...p1,rss:9*GiB}],scope)]);assert.equal(kill.focused,true);assert.ok(all(parent).includes(kill));kill.onclick();assert.equal(selected.pid,10);assert.equal(selected.kind,'TERM');assert.strictEqual(selected.own,scope);
 c.syncChildren(parent,[c.processTable([p2],scope)]);assert.equal(all(parent).includes(kill),false);
});
test('L4: human-readable row/detail statuses agree; caveat appears only for idle/finished agents',()=>{
 const c=setup();receive(c,work(undefined,'blocked'),state([]));assert.match(row(c).textContent,/Needs input/);assert.doesNotMatch(row(c).textContent,/\bblocked · p1/);assert.doesNotMatch(row(c).textContent,/does not establish task completion/);
 c.receiveWork(work(undefined,'done'));assert.match(row(c).textContent,/Agent finished/);assert.doesNotMatch(row(c).textContent,/\bdone · p1/);assert.match(row(c).textContent,/does not establish task completion/);
});
