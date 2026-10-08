// Synthetic DOM contract checks; headless browser checks cover native focus/layout.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { app, get, run, all } = require('./dom_support.cjs');
function projection() {
  const at = new Date().toISOString(), ready = {state:'ready',available:true,has_data:true,stale:false,last_success:at};
  return {version:1,now:at,board:{...ready},herdr:{...ready},sweep_at:at,
    workstreams:[{key:'s1',id:'s1',name:'Atlas <img src=x onerror=bad()>',repo:'/projects/atlas',source_mode:'catalog',source:'/projects/atlas/.fleet/workstreams.json',stage:'Working',next:'Follow owning task',live_panes:['p1'],panes:['p1'],derived:{version:1,section:'Now',runtime_known:true,runtime_observed_at:at,tasks:[{key:'t1',id:'TASK-1',intent:'active',live_panes:['p1'],pr_references:[],checkouts:[{path:'/projects/atlas-nav',registered_plan:'/projects/atlas-nav/plan.md',plan:{path:'/projects/atlas-nav/plan.md',done:1,open:2,blocked:0},observed_at:at},{path:'/projects/atlas-lane',status:'absent'}]}]},prs:[]}],
    agents:[{pane:'p1',name:'builder',checkout:'/projects/atlas-nav',cwd:'/projects/atlas-nav',status:'working',context:'20%'}],
    memberships:[{checkout:'/projects/atlas-nav',stream_key:'s1',task_key:'t1',panes:['p1'],board_panes:['p1']}],
    board_agents:[{observation:{pane:'p1',agent_name:'builder',task_key:'t1',repository:'/projects/atlas',checkout:'/projects/atlas-nav',plan:{path:'/projects/atlas-nav/plan.md',done:1,open:2,blocked:0},pr_lookup:'unknown'},meta:{collected_at:at},assessment:{status:'ok',waiting_on:'clay',assessed_at:at,finished:0.2}}],activity:[]};
}
test('Overview defaults even without providers; explicit Resources selection stays remembered', () => {
  const c = app(), p = projection(); c.renderWork(p); assert.equal(get(c,'resources-view').hidden,true);
  c.renderWork({...p,board:{state:'missing'},herdr:{state:'missing'},workstreams:[],activity:[]}); assert.equal(get(c,'resources-view').hidden,true);
  get(c,'view-resources').onclick(); assert.equal(c.storage.get('devdash.view'),'resources');
  const absent=app();absent.renderWork({...p,board:{state:'missing'},herdr:{state:'missing'},workstreams:[],activity:[]});assert.equal(get(absent,'overview-view').hidden,false);
  const remembered=app({'devdash.view':'resources'});remembered.renderWork(p);assert.equal(get(remembered,'overview-view').hidden,true);
});
test('live fallback uses agents, not backend activity; unsupported declarations stay inspectable', () => {
  const c=app(),p=projection();p.board={state:'missing',has_data:false};p.workstreams=[];p.activity=[];c.receiveWork(p);
  assert.match(get(c,'work-list').textContent,/Ungrouped activity/);assert.match(get(c,'work-list').textContent,/Working/);
  c.receiveWork({...p,activity:[],agents:[]});assert.match(get(c,'work-list').textContent,/Current activity unknown/);
  const s=projection();s.workstreams[0].derived.version=99;s.workstreams[0].warning='Unsupported derived version';s.workstreams[0].derived.tasks=[];c.receiveWork(s);get(c,'work-scope-all').onclick();c.chooseWork('s1');assert.match(get(c,'work-detail-body').textContent,/Unsupported derived version/);
});
test('rows append and never reorder for polling; sections, focused row and search controls retain identity', () => {
  const c=app({'devdash.work.scope':'all'}),p=projection();c.receiveWork(p);
  const list=get(c,'work-list'),section=list.firstChild,row=all(list).find(n=>n.getAttribute('data-key')==='s1');section.open=true;row.focus();get(c,'work-search').value='Atlas';
  p.workstreams.push({...p.workstreams[0],key:'s2',name:'Atlas second',derived:null});p.workstreams.reverse();c.receiveWork(p);
  assert.strictEqual(list.firstChild,section);assert.equal(section.open,true);assert.equal(row.focused,true);assert.equal(get(c,'work-search').value,'Atlas');assert.strictEqual(all(list).find(n=>n.getAttribute('data-key')==='s1'),row);
  assert.deepEqual(Array.from(c.stableWorkOrder(['c','b','a'],['a','b'])),['a','b','c']);
});
test('plans deduplicate, estimates stay in agent details, PR unknown stays unknown, and text is never HTML', () => {
  const c=app({'devdash.work.scope':'all'});c.receiveWork(projection());c.chooseWork('t1');
  const body=get(c,'work-detail-body');assert.equal(all(body).filter(n=>n.className==='work-plan').length,1);
  assert.match(body.textContent,/unknown is not/);assert.doesNotMatch(body.textContent,/AI estimates/);
  c.selectDetailTab('agents');assert.match(body.textContent,/AI estimates/);assert.match(body.textContent,/waiting on: you/);
  assert.equal(all(get(c,'work-list')).filter(n=>n.tagName==='IMG').length,0);
  for (const url of ['javascript:bad()','data:text/html,bad','https://user:pass@example.org/','//example.org']) assert.equal(c.safeWorkURL(url),'');
  assert.equal(c.outbound('javascript:bad()','x').tagName,'SPAN');assert.equal(c.outbound('https://example.org/','x').tagName,'A');
});
test('Resources matches exact paths only, retains confirmation and has independent disclosure keys', () => {
  const c=app({'devdash.work.scope':'all'});c.receiveWork(projection());get(c,'view-work').onclick();c.chooseWork('t1');c.selectDetailTab('resources');assert.match(get(c,'work-detail-body').textContent,/No resource card currently exists/);
  run(c,`lastState={checkouts:[{path:'/projects/atlas-nav',display:'/projects/atlas-nav',branch:'feature/nav',total_bytes:1,proc_bytes:1,working:1,agents:[],stacks:[],processes:[{pid:10,name:'node',cmd:'vite',cwd:'/projects/atlas-nav/web',rss:1,cpu:0,ports:[5173]}]}]};renderWorkResources();`);
  const body=get(c,'work-detail-body'),link=all(body).find(n=>n.tagName==='A');assert.equal(link.getAttribute('href'),'http://localhost:5173/');
  const kill=all(body).find(n=>n.tagName==='BUTTON'&&n.textContent==='Kill');assert.ok(kill);
  let invoked=false;c.confirmAction=(kind)=>{invoked=kind==='TERM'};kill.onclick();assert.equal(invoked,true);
  const disclosure=all(body).find(n=>n.tagName==='DETAILS');disclosure.open=true;run(c,'renderWorkResources()');assert.strictEqual(all(body).find(n=>n.tagName==='DETAILS'),disclosure);assert.equal(disclosure.open,true);
  assert.doesNotMatch(readFileSync(`${__dirname}/static/work.js`,'utf8'),/checkout\.stop|Safe to stop|Stop everything/);
});
test('stream, task and main Resources disclosures have independent identities and toggle state', () => {
  const c = app({'devdash.work.scope':'all'}); c.receiveWork(projection());
  run(c, `lastState={checkouts:[{path:'/projects/atlas-nav',display:'/projects/atlas-nav',branch:'feature/nav',total_bytes:1,proc_bytes:1,working:1,agents:[],stacks:[],processes:[]}]};`);
  const body = get(c, 'work-detail-body');
  const disclosure = root => all(root).find(n => n.className.includes('card-resources'));
  const toggle = (node, open) => { node.open = open; node.dispatchEvent({ type: 'toggle' }); };
  const has = key => run(c, `openSections.has(${JSON.stringify(key)})`);
  const streamKey = 'work|s1|/projects/atlas-nav|resources';
  const taskKey = 'work|t1|/projects/atlas-nav|resources';
  const mainKey = '/projects/atlas-nav|resources';
  const main = disclosure(c.card(run(c, 'lastState.checkouts[0]'), false));

  c.chooseWork('s1'); c.selectDetailTab('resources');
  const stream = disclosure(body); toggle(stream, true); stream.focus(); body.scrollTop = 60;
  c.renderWorkResources();
  assert.strictEqual(disclosure(body), stream, 'same-selection poll must retain node');
  assert.equal(stream.open, true); assert.equal(stream.focused, true); assert.equal(body.scrollTop, 60);
  assert.equal(has(streamKey), true); assert.equal(has(taskKey), false); assert.equal(has(mainKey), false);

  c.chooseWork('t1');
  const task = disclosure(body);
  assert.notStrictEqual(task, stream, 'different selection must replace disclosure identity');
  assert.equal(task.open, false, 'task must not inherit stream disclosure');
  toggle(task, true); assert.equal(has(taskKey), true); assert.equal(has(streamKey), true);
  toggle(task, false); assert.equal(has(taskKey), false); assert.equal(has(streamKey), true);
  toggle(main, true); assert.equal(has(mainKey), true);

  c.chooseWork('s1');
  const restored = disclosure(body); assert.equal(restored.open, true, 'restore stream choice');
  toggle(restored, false); assert.equal(has(streamKey), false); assert.equal(has(mainKey), true);
  c.chooseWork('t1'); assert.equal(disclosure(body).open, false, 'restore task choice');
  assert.equal(main.open, true, 'main Resources remains separate');
});

test('inline work selection slots preserve their separately reconciled inspector children', () => {
  const c=app(),parent=c.el('div'),panel=c.el('aside',{text:'Existing inspector'});
  const slot=c.keyed(c.el('div',{'data-preserve-children':'true'},panel),'work-detail-slot|s1');parent.append(slot);panel.focus();
  c.syncChildren(parent,[c.keyed(c.el('div',{'data-preserve-children':'true'}),'work-detail-slot|s1')]);
  assert.strictEqual(parent.firstChild,slot);assert.strictEqual(slot.firstChild,panel);assert.equal(panel.focused,true);
});

test('tabs support keyboard navigation and stay selected through polls; original ages stay visible', () => {
  const c=app({'devdash.work.scope':'all'}),p=projection();c.receiveWork(p);c.chooseWork('t1');get(c,'work-tab-summary').onkeydown({key:'ArrowRight',preventDefault(){}});assert.equal(get(c,'work-tab-agents').getAttribute('aria-selected'),'true');
  c.receiveWork(p);assert.equal(get(c,'work-tab-agents').getAttribute('aria-selected'),'true');
  assert.equal(c.stamp('0001-01-01T00:00:00Z'),'observation time unknown');assert.match(c.stamp(new Date(Date.now()-3600000).toISOString()),/1h/);
  p.board={...p.board,state:'error',stale:true,error:'read failed'};p.sweep_at=new Date(Date.now()-3600000).toISOString();c.receiveWork(p);assert.match(get(c,'work-sources').textContent,/last good retained/);assert.match(get(c,'work-sources').textContent,/not a new observation/);
});
