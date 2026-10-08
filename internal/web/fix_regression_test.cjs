const { test } = require('node:test');
const assert = require('node:assert/strict');
const { app, get, all, run } = require('./dom_support.cjs');
const iso=()=>new Date().toISOString(), path='/checkouts/owned';
function fixture() {
 const t=iso(),ready={state:'ready',has_data:true,stale:false,last_success:t};
 return {version:1,now:t,sweep_at:t,board:{...ready},herdr:{...ready},route:{server:'1111111111111111',verified:true},board_server:'1111111111111111',
  workstreams:[{key:'S',id:'S',name:'Owned stream',repo:'/repo',source_mode:'catalog',derived:{version:1,section:'Now',runtime_known:true,tasks:[{key:'T',id:'T',checkouts:[{key:'K',path,server:'1111111111111111',registered_plan:'/p'}]}]},prs:[]}],
  agents:[],activity:[],board_agents:[],memberships:[]};
}
function state(checkouts=[{path,repository:'/repo'}]) {return {now:iso(),errors:[],docker:{available:true},checkouts:checkouts.map(c=>({display:c.path,branch:'b',agents:[],processes:[],proc_bytes:1,stacks:[],total_bytes:1,...c}))};}
const person=(pane,name,status,checkout=path)=>({pane,name,checkout,cwd:checkout,repository:'/repo',status});
const membership=(panes,task='T',binding='K')=>({checkout:path,stream_key:'S',task_key:task,binding_key:binding,panes,board_panes:panes});
function labels(c) {return all(get(c,'work-list')).filter(n=>n.className==='work-row-title').map(n=>n.textContent);}

test('R6: full checkout ownership survives a quiet lead/reviewer in Overview and Live',()=>{
 const c=app({'devdash.work.scope':'live'},true),w=fixture();w.agents=[person('p1','worker','working'),person('p2','lead','idle')];w.memberships=[membership(['p1','p2'])];
 c.receiveWork(w);c.receiveState(state());c.renderOverview();
 assert.equal(all(get(c,'overview-list')).find(n=>n.tagName==='H2').textContent,'T');
 get(c,'view-work').onclick();assert.ok(labels(c).includes('Owned stream'));assert.doesNotMatch(get(c,'work-list').textContent,/Ungrouped activity/);
});
test('R7: one legacy membership for two physical panes groups both views',()=>{
 const c=app({'devdash.work.scope':'live'},true),w=fixture();w.workstreams=[{key:'L',name:'Legacy stream',repo:'/repo',source_mode:'legacy',panes:['p1','p2'],prs:[]}];
 w.agents=[person('p1','a','working'),person('p2','b','working')];w.memberships=[{checkout:path,stream_key:'L',panes:['p1','p2'],board_panes:['p1','p2']}];
 c.receiveWork(w);c.receiveState(state());c.renderOverview();assert.equal(all(get(c,'overview-list')).find(n=>n.tagName==='H2').textContent,'Legacy stream');
 get(c,'view-work').onclick();assert.ok(labels(c).includes('Legacy stream'));
});

test('R8: All registered host stream filters exact task checkouts across repositories, not display labels',()=>{
 const c=app({'devdash.work.scope':'all'},true),w=fixture();w.workstreams[0].host_scoped=true;w.workstreams[0].repo='';w.workstreams[0].source='';
 const task=w.workstreams[0].derived.tasks[0];task.checkouts[0].repo='same-label';
 w.workstreams[0].derived.tasks.push({key:'B',id:'BEACON',checkouts:[{key:'Bk',path:'/checkouts/beacon',repo:'same-label'}]});
 w.workstreams[0].derived.tasks.push({key:'U',id:'UNKNOWN',checkouts:[{key:'Uk',path:'/checkouts/unknown',repo:'same-label'}]});
 c.receiveWork(w);c.receiveState(state([{path,repository:'/repo/atlas'},{path:'/checkouts/beacon',repository:'/repo/beacon'},{path:'/checkouts/app',repository:'/repo/atlas'}]));c.renderOverview();
 get(c,'view-work').onclick();get(c,'work-scope-all').onclick();
 assert.ok(all(get(c,'work-repo')).some(n=>n.getAttribute('value')==='/repo/atlas'));
 get(c,'work-repo').value='/repo/atlas';get(c,'work-repo').onchange();
 assert.match(get(c,'work-list').textContent,/Owned stream/);assert.ok(labels(c).includes('T'));
 assert.doesNotMatch(get(c,'work-list').textContent,/BEACON|UNKNOWN/);
 get(c,'work-repo').value='/repo/beacon';get(c,'work-repo').onchange();
 assert.match(get(c,'work-list').textContent,/BEACON/);assert.doesNotMatch(get(c,'work-list').textContent,/UNKNOWN/);
 get(c,'work-repo').value='';get(c,'work-repo').onchange();assert.match(get(c,'work-list').textContent,/UNKNOWN/);
});
test('L5: exact duplicate checkout facts preserve task identity, conflicting duplicates do not',()=>{
 const c=app({},true),w=fixture();w.agents=[person('p1','worker','working')];w.memberships=[membership(['p1'])];
 const co=w.workstreams[0].derived.tasks[0].checkouts[0];w.workstreams[0].derived.tasks[0].checkouts.push(JSON.parse(JSON.stringify(co)));
 c.receiveWork(w);c.receiveState(state());c.renderOverview();assert.equal(all(get(c,'overview-list')).find(n=>n.tagName==='H2').textContent,'T');
 w.workstreams[0].derived.tasks[0].checkouts[1].registered_plan='/different';c.receiveWork(w);
 assert.notEqual(all(get(c,'overview-list')).find(n=>n.tagName==='H2').textContent,'T');
});
test('L4: changed foreground locations cannot grow retained assignment cache without bound',()=>{
 const c=app({},true),w=fixture();w.agents=[person('p1','worker','working')];w.memberships=[membership(['p1'])];
 c.receiveWork(w);c.receiveState(state());c.renderOverview();
 for(let i=0;i<25;i++) {w.agents=[{...w.agents[0],cwd:'/other/location/'+i}];w.memberships=[];c.receiveWork(w);}
 assert.ok(run(c,'overviewFactMembership.size')<=1);
});
test('R9: fresh task, plan or catalog change while Herdr is stale drops retained facts',()=>{
 const c=app({},true),w=fixture();w.agents=[person('p1','builder','working')];
 const plan='/plans/old';w.workstreams[0].derived.tasks[0].checkouts[0]={key:'OLDk',path,registered_plan:plan,plan:{path:plan,done:1,open:1,blocked:0}};
 w.memberships=[membership(['p1'],'T','OLDk')];
 w.board_agents=[{observation:{pane:'p1',agent_name:'builder',checkout:path,cwd:path,task_id:'T',workstream_id:'S',task_key:'T',plan_source:'registered: '+plan,plan:{path:plan,done:1,open:1,blocked:0}},meta:{collected_at:w.now}}];
 c.receiveWork(w);c.receiveState(state());c.renderOverview();assert.match(get(c,'overview-list').textContent,/\/plans\/old/);
 const stale=JSON.parse(JSON.stringify(w));stale.herdr={state:'error',has_data:true,stale:true,last_success:w.now};stale.board_agents=[];stale.memberships=[];
 c.receiveWork(stale);assert.match(get(c,'overview-list').textContent,/\/plans\/old/);assert.match(get(c,'overview-list').textContent,/last known compatible facts/);
 const change=(mutate)=>{mutate();c.receiveWork(stale);assert.doesNotMatch(get(c,'overview-list').textContent,/\/plans\/old/);};
 change(()=>{stale.workstreams[0].derived.tasks[0].key='NEW';stale.workstreams[0].derived.tasks[0].id='NEW';stale.board_agents=[{observation:{pane:'p1',agent_name:'builder',checkout:path,cwd:path,task_id:'NEW',workstream_id:'S',task_key:'NEW',plan:{path:'/plans/new'}},meta:{collected_at:stale.now}}];});
 // A reused task ID with only its binding's registered plan changed also invalidates it.
 c.receiveWork(w);stale.workstreams[0].derived.tasks[0].key='T';stale.workstreams[0].derived.tasks[0].id='T';
 change(()=>{stale.workstreams[0].derived.tasks[0].checkouts[0].key='NEWk';stale.workstreams[0].derived.tasks[0].checkouts[0].registered_plan='/plans/new';});
 c.receiveWork(w);
 change(()=>{stale.workstreams[0].key='another-catalog';stale.workstreams[0].derived.tasks[0].key='T';stale.board_agents[0].observation.task_key='T';});
});
