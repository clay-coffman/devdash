const { test } = require('node:test');
const assert = require('node:assert/strict');
const { app, get, all, run } = require('./dom_support.cjs');
const now = () => new Date().toISOString();
const path = '/checkouts/shared';
function projection() {
 const time=now(), ready={state:'ready',has_data:true,stale:false,last_success:time};
 const task=(key,plan)=>({key,id:key,checkouts:[{key:plan,path,repo:'same',server:'1111111111111111',registered_plan:plan,plan:{path:plan,done:0,open:1}}],live_panes:[]});
 return {version:1,now:time,sweep_at:time,board:{...ready},herdr:{...ready},route:{server:'1111111111111111',verified:true},board_server:'1111111111111111',
  workstreams:[{key:'host',id:'host',name:'Multi-repository',repo:'/not-a-repository',host_scoped:true,source_mode:'catalog',derived:{version:1,section:'Now',runtime_known:true,tasks:[task('A','a'),task('B','b')]},prs:[]}],
  agents:[{pane:'p',name:'visitor',checkout:path,cwd:path,repository:'/actual/repository',status:'working'}],activity:[{key:'checkout:'+path,checkout:path,panes:['p']}],board_agents:[],memberships:[]};
}
function state() {return {now:now(),errors:[],docker:{available:true},checkouts:[{path,display:path,branch:'branch',agents:[],processes:[{pid:5173,cmd:'vite',cwd:path+'/web',ports:[5173],cpu:0,name:'node',rss:1}],proc_bytes:1,stacks:[],total_bytes:1}]};}
function rows(c) {return all(get(c,'overview-list')).filter(n=>n.className==='overview-row');}
test('two bindings keep one physical app and unproven visitor ungrouped; catalog facts remain separate',()=>{
 const c=app({'devdash.work.scope':'live'},true),w=projection();c.receiveWork(w);c.receiveState(state());c.renderOverview();
 assert.equal(rows(c).length,1);assert.doesNotMatch(rows(c)[0].textContent,/\bA\b|\bB\b|not-a-repository/);
 assert.match(get(c,'overview-counts').textContent,/1 app/);assert.match(get(c,'work-list').textContent,/Ungrouped activity/);
 get(c,'view-work').onclick();get(c,'work-scope-all').onclick();c.chooseWork('A');assert.match(get(c,'work-detail-body').textContent,/Registered plan: a/);
 c.chooseWork('B');assert.match(get(c,'work-detail-body').textContent,/Registered plan: b/);
 assert.doesNotMatch(get(c,'work-repo').textContent,/not-a-repository/);
});
test('coordination and standalone stay secondary, unknown sections remain unverified',()=>{
 const c=app({'devdash.work.scope':'all'},true),w=projection();
 w.workstreams.push({key:'coord',name:'Coordinator',repo:'/actual/repository',source_mode:'coordination',panes:['p']});
 w.workstreams.push({key:'solo',name:'Solo',repo:'/actual/repository',source_mode:'standalone',panes:['p']});
 w.workstreams.push({key:'future-section',name:'Unknown',repo:'/actual/repository',derived:{version:1,section:'Unrecognized',tasks:[]}});
 c.receiveWork(w);c.receiveState(state());c.renderOverview();
 get(c,'view-work').onclick();get(c,'work-scope-all').onclick();
 assert.match(get(c,'work-list').textContent,/Coordination/);assert.match(get(c,'work-list').textContent,/Standalone sessions/);
 assert.match(get(c,'work-list').textContent,/Activity unverified/);
 get(c,'work-scope-live').onclick();assert.match(get(c,'work-list').textContent,/Ungrouped activity/);
});
test('server change and pane reuse clear cached plan/PR association without erasing physical apps',()=>{
 const c=app({},true),w=projection();w.workstreams[0].derived.tasks.pop();w.agents[0].name='builder';w.memberships=[{checkout:path,stream_key:'host',task_key:'A',binding_key:'a',panes:['p'],board_panes:['p']}];
 w.board_agents=[{observation:{pane:'p',checkout:path,cwd:path,plan:{path:'a',open:1},pr:{number:1,repository:'org/repo',state:'OPEN'}},meta:{collected_at:w.now}}];
 c.receiveWork(w);c.receiveState(state());c.renderOverview();assert.equal(all(rows(c)[0]).find(n=>n.tagName==='H2').textContent,'A');
 w.board_server='2222222222222222';w.memberships=[];w.board_agents=[];c.receiveWork(w);
 assert.doesNotMatch(rows(c)[0].textContent,/Plan:|org\/repo/);assert.notEqual(all(rows(c)[0]).find(n=>n.tagName==='H2').textContent,'A');assert.match(get(c,'overview-counts').textContent,/1 app/);
 w.board_server='1111111111111111';w.agents=[{...w.agents[0],cwd:'/new/location',checkout:'/elsewhere'}];c.receiveWork(w);
 assert.doesNotMatch(rows(c)[0].textContent,/Plan:|org\/repo/);assert.notEqual(all(rows(c)[0]).find(n=>n.tagName==='H2').textContent,'A');
});
