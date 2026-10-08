const { test } = require('node:test');
const assert = require('node:assert/strict');
const { app, get, all } = require('./dom_support.cjs');
const at=()=>new Date().toISOString();
const path='/home/u/co/lead', absolute='/home/u/co/lead/.fleet/t/plan.md', short='~/co/lead/.fleet/t/plan.md';
function producerProjection() {
 const now=at(),ready={state:'ready',has_data:true,stale:false,last_success:now};
 const plan={path:short,done:3,open:1,blocked:0};
 return {version:1,now,sweep_at:now,board:{...ready},herdr:{...ready},route:{server:'1111111111111111',verified:true},board_server:'1111111111111111',
  workstreams:[{key:'S',id:'ws',name:'Owned stream',source_mode:'catalog',host_scoped:true,derived:{version:1,section:'Now',runtime_known:true,
    tasks:[{key:'T',id:'T',checkouts:[{key:'K',path,server:'1111111111111111',registered_plan:absolute,agent:'lead',plan},
      {key:'KR',path,server:'1111111111111111',agent:'rev',observer_only:true}]}]},prs:[]}],
  agents:[{pane:'p1',name:'lead',checkout:path,cwd:path,repository:'/home/u/repo',status:'working'},
    {pane:'p2',name:'rev',checkout:path,cwd:path,repository:'/home/u/repo',status:'idle'}],activity:[],
  board_agents:[{observation:{pane:'p1',agent_name:'lead',checkout:path,cwd:path,repository:'/home/u/repo',task_id:'T',workstream_id:'ws',task_key:'T',
    role_from_name:'lead',role_source:'registered',plan_source:'registered: '+absolute,plan,pr_lookup:'unknown'},meta:{collected_at:now}},
    {observation:{pane:'p2',agent_name:'rev',checkout:path,cwd:path,repository:'/home/u/repo',task_id:'T',workstream_id:'ws',task_key:'T',
    role_from_name:'reviewer',role_source:'registered',plan_source:'registered review assignment; no implementation plan'},meta:{collected_at:now}}],
  memberships:[{checkout:path,stream_key:'S',task_key:'T',binding_key:'K',panes:['p1','p2'],board_panes:['p1','p2']}]};
}
const resource=()=>({now:at(),errors:[],docker:{available:true},checkouts:[{path,repository:'/home/u/repo',display:path,branch:'b',agents:[],processes:[],proc_bytes:1,stacks:[],total_bytes:1}]});
function display(w){const c=app({'devdash.work.scope':'all'},true);c.receiveWork(w);c.receiveState(resource());c.renderOverview();
 const overview=get(c,'overview-list').textContent,heading=all(get(c,'overview-list')).find(n=>n.tagName==='H2')?.textContent;
 get(c,'view-work').onclick();c.chooseWork('T');const summary=get(c,'work-detail-body').textContent;
 c.selectDetailTab('agents');const agents=get(c,'work-detail-body').textContent;
 return {overview,heading,summary,agents,c};
}
test('RB1: producer-shortened checkout and observation paths keep owner/reviewer plan facts in both views',()=>{
 const w=producerProjection(),r=display(w);
 assert.equal(w.workstreams[0].derived.tasks[0].checkouts[0].plan.path,short,'do not normalize the fixture');
 assert.equal(r.heading,'T');assert.match(r.overview,/3 done/);assert.match(r.summary,/3 done/);
 assert.match(r.agents,/Board cache: lead/);assert.match(r.agents,/Board cache: rev/);
 // When the checkout plan read is unavailable, only the matching observed
 // owner can supply the cached plan in either view.
 const observedOnly=producerProjection();delete observedOnly.workstreams[0].derived.tasks[0].checkouts[0].plan;
 const cached=display(observedOnly);assert.match(cached.overview,/3 done/);assert.match(cached.summary,/3 done/);
});
test('RB1: authoritative source protects owner plan enrichment, not tilde basename or changed assignment',()=>{
 const wrong=producerProjection();delete wrong.workstreams[0].derived.tasks[0].checkouts[0].plan;
 wrong.board_agents[0].observation.plan_source='registered: /other/.fleet/t/plan.md';
 let r=display(wrong);assert.doesNotMatch(r.overview,/3 done/);assert.doesNotMatch(r.agents,/Board cache: lead/);
 const absent=producerProjection();delete absent.workstreams[0].derived.tasks[0].checkouts[0].plan;absent.board_agents[0].observation.plan_source='';
 r=display(absent);assert.doesNotMatch(r.overview,/3 done/);assert.doesNotMatch(r.agents,/Board cache: lead/);
 const changed=producerProjection();delete changed.workstreams[0].derived.tasks[0].checkouts[0].plan;
 changed.workstreams[0].derived.tasks[0].checkouts[0].registered_plan='/home/u/co/other/.fleet/t/plan.md';
 r=display(changed);assert.doesNotMatch(r.overview,/3 done/);assert.doesNotMatch(r.agents,/Board cache: lead/);
});
test('RB1: explicit different absolute plan.path rejects but matching non-home absolute remains compatible',()=>{
 const conflict=producerProjection();delete conflict.workstreams[0].derived.tasks[0].checkouts[0].plan;
 conflict.board_agents[0].observation.plan.path='/other/.fleet/t/plan.md';
 let r=display(conflict);assert.doesNotMatch(r.overview,/3 done/);assert.doesNotMatch(r.agents,/Board cache: lead/);
 const valid=producerProjection();delete valid.workstreams[0].derived.tasks[0].checkouts[0].plan;
 valid.board_agents[0].observation.plan.path=absolute;
 r=display(valid);assert.match(r.overview,/3 done/);assert.match(r.agents,/Board cache: lead/);
});
