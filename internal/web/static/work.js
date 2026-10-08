'use strict';

// Work is a separate cached projection. None of these reads trigger a board sweep.
let lastWork = null;
let workFetchError = '';
let workReceivedAt = null;
let workRevision = 0;
function receiveWork(w) {
  if (w?.version !== 1 || !w.board || !w.herdr || !Array.isArray(w.workstreams) || !Array.isArray(w.activity) || !Array.isArray(w.agents)) throw new Error('Unsupported devdash work projection');
  workFetchError = ''; workReceivedAt = pollClock(); workRevision++;
  renderWork(w);
}
let activeView = localStorage.getItem('devdash.view');
if (activeView === 'work') { activeView = 'overview'; localStorage.setItem('devdash.view', activeView); }
if (!['overview', 'workstreams', 'resources'].includes(activeView)) activeView = 'overview';
let selectedWork = localStorage.getItem('devdash.work.selection') || '';
let detailTab = localStorage.getItem('devdash.work.tab') || 'summary';
if (!['summary', 'agents', 'resources'].includes(detailTab)) detailTab = 'summary';
let workOrder = [];
const taskOrders = new Map();
const initializedWorkSections = new Set();
const workSections = ['Declared current', 'Activity unverified', 'Follow-up (declared)', 'Parked', 'Completed/canceled', 'Coordination', 'Standalone sessions', 'Legacy'];
let workScope = localStorage.getItem('devdash.work.scope') === 'all' ? 'all' : 'live';
let liveRecords = new Map();
function setWorkScope(scope) {
  workScope = scope;
  localStorage.setItem('devdash.work.scope', scope);
  $('work-scope-live').setAttribute('aria-pressed', String(scope === 'live'));
  $('work-scope-all').setAttribute('aria-pressed', String(scope === 'all'));
  renderWorkList(); renderWorkDetail();
}
$('work-scope-live').onclick = () => setWorkScope('live');
$('work-scope-all').onclick = () => setWorkScope('all');
$('work-scope-live').setAttribute('aria-pressed', String(workScope === 'live'));
$('work-scope-all').setAttribute('aria-pressed', String(workScope === 'all'));
const workDetailPanel = $('work-detail');
const workDetailHome = workDetailPanel.parentNode;

function setView(view, remember = true) {
  activeView = view;
  if (remember) localStorage.setItem('devdash.view', view);
  $('overview-view').hidden = view !== 'overview';
  $('overview-counts').hidden = view !== 'overview';
  $('work-view').hidden = view !== 'workstreams';
  $('resources-view').hidden = view !== 'resources';
  $('view-overview').setAttribute('aria-pressed', String(view === 'overview'));
  $('view-work').setAttribute('aria-pressed', String(view === 'workstreams'));
  $('view-resources').setAttribute('aria-pressed', String(view === 'resources'));
  if (view === 'resources' && lastState) { renderHeader(lastState); drawChart(); drawCheckoutSparks(); }
  // During script boot overview.js (which owns freshness/observations) has
  // not loaded yet. Its initial render will populate this view afterward.
  if (typeof overviewSnapshot === 'function' && view === 'workstreams') { renderWorkRepositories(); renderWorkList(); renderWorkDetail(); }
  if (view !== 'workstreams') $('work-detail').hidden = true;
  if (view === 'workstreams') $('source-notice').hidden = true;
  else if (typeof renderOverview === 'function') renderOverview();
}
$('view-overview').onclick = () => setView('overview');
$('view-work').onclick = () => setView('workstreams');
$('view-resources').onclick = () => setView('resources');
if (activeView) setView(activeView, false);

function stableWorkOrder(keys, previous) {
  const present = new Set(keys), kept = previous.filter(k => present.has(k)), seen = new Set(kept);
  return kept.concat(keys.filter(k => !seen.has(k)));
}
function keyed(node, key) { node.setAttribute('data-key', key); return node; }
// Small keyed DOM reconciliation: values change in place, not entire panels.
// Existing nodes retain focus, disclosure state, selection ranges and scroll.
function syncNode(old, fresh) {
  if (old.nodeType === 3) { if (old.nodeValue !== fresh.nodeValue) old.nodeValue = fresh.nodeValue; return; }
  for (const attr of Array.from(old.attributes)) {
    if (attr.name === 'open' && old.tagName === 'DETAILS') continue;
    if (!fresh.hasAttribute(attr.name)) old.removeAttribute(attr.name);
  }
  for (const attr of Array.from(fresh.attributes)) {
    if (attr.name === 'open' && old.tagName === 'DETAILS') continue;
    if (old.getAttribute(attr.name) !== attr.value) old.setAttribute(attr.name, attr.value);
  }
  for (const event of ['onclick', 'oninput', 'onchange', 'onkeydown']) old[event] = fresh[event];
  // A keyed selection slot owns the existing tabbed inspector, reconciled
  // separately below. Do not detach its live children during list polling.
  if (!fresh.hasAttribute('data-preserve-children')) syncChildren(old, Array.from(fresh.childNodes));
}
function syncChildren(parent, freshChildren) {
  const focused = document.activeElement;
  const oldChildren = Array.from(parent.childNodes), kept = new Set();
  const keyOf = n => n.nodeType === 1 ? n.getAttribute('data-key') : null;
  let cursor = parent.firstChild;
  for (const fresh of freshChildren) {
    const key = keyOf(fresh);
    let old = key != null ? oldChildren.find(n => !kept.has(n) && keyOf(n) === key) : cursor;
    if (old && (kept.has(old) || keyOf(old) !== key || old.nodeType !== fresh.nodeType || old.nodeName !== fresh.nodeName)) old = null;
    if (old) syncNode(old, fresh); else old = fresh;
    if (old !== cursor) parent.insertBefore(old, cursor);
    kept.add(old); cursor = old.nextSibling;
  }
  for (const old of oldChildren) if (!kept.has(old)) old.remove();
  // insertBefore can blur a moved node in native browsers. Restore only a still
  // connected previous focus target, without scrolling or focusing a replacement.
  if (focused?.isConnected && document.activeElement !== focused) focused.focus({ preventScroll: true });
}
function shortRepository(path, paths = []) {
  if (!path) return 'Repository unknown';
  const parts = path.split('/').filter(Boolean);
  for (let n = 1; n <= parts.length; n++) {
    const label = parts.slice(-n).join('/');
    if (!paths.some(other => other !== path && other.split('/').filter(Boolean).slice(-n).join('/') === label)) return label;
  }
  return path;
}
function stamp(at) {
  const t = Date.parse(at);
  if (!Number.isFinite(t) || t <= 0 || new Date(t).getUTCFullYear() < 1970) return 'observation time unknown';
  const age = (Date.now() - t) / 1000;
  if (age < -1) return `${new Date(t).toLocaleString()} · future timestamp (clock mismatch)`;
  return `${new Date(t).toLocaleString()} · ${age < 60 ? Math.max(0, Math.floor(age)) + 's' : fmtDur(age)} ago`;
}
function safeWorkURL(raw) {
  try { const u = new URL(raw); return ['http:', 'https:'].includes(u.protocol) && !u.username && !u.password ? u.href : ''; }
  catch { return ''; }
}
function outbound(raw, text) {
  const href = safeWorkURL(raw);
  return href ? el('a', { href, target: '_blank', rel: 'noopener noreferrer', text, 'aria-label': text + ' (opens in a new tab)' }) : el('span', { text });
}
function providerLine(name, p, observed) {
  const state = p?.state || 'pending';
  const parts = [`${name}: ${state}${p?.has_data && state !== 'ready' ? ' · last good retained' : ''}`];
  if (p?.has_data) parts.push(`${p.stale ? 'stale / unknown age · ' : ''}${stamp(observed)}`);
  if (p?.error) parts.push(p.error);
  if (name === 'Board' && p?.has_data) parts.push(`cache read ${stamp(p.last_success)} (not a new observation)`);
  return el('p', { class: p?.stale || state === 'error' ? 'source-warning' : 'muted', text: parts.join(' · ') });
}
function renderWorkHealth() { /* Host health is shared in the compact header. */ }
function streamSection(s) {
  if (s.source_mode === 'coordination') return 'Coordination';
  if (s.source_mode === 'standalone') return 'Standalone sessions';
  if (s.derived?.version !== 1) return 'Legacy';
  const section = s.derived.section;
  if (['Parked', 'Completed/canceled', 'Coordination', 'Standalone sessions'].includes(section)) return section;
  if (section === 'Follow-up') return 'Follow-up (declared)';
  if (section === 'Now') return boardFresh() && !/reconcil/i.test(s.stage || '') ? 'Declared current' : 'Activity unverified';
  return 'Activity unverified';
}
function workDisclosure(key, title, body, defaultOpen = false) {
  if (!initializedWorkSections.has(key)) {
    initializedWorkSections.add(key);
    if (defaultOpen) openSections.add(key);
  }
  return keyed(details(key, title, ...body), key);
}
function chooseWork(key, e) {
  if (e) { e.preventDefault(); e.stopPropagation(); }
  selectedWork = key; localStorage.setItem('devdash.work.selection', key);
  renderWorkList(); renderWorkDetail();
}
function workButton(key, title, meta, summary, apps = []) {
  const button = keyed(el('button', { class: 'work-row', 'aria-pressed': String(selectedWork === key), onclick: e => chooseWork(key, e) },
    el('span', { class: 'work-row-title', text: title }),
    el('span', { class: 'work-row-meta muted', text: meta }),
    summary ? el('span', { class: 'work-row-summary', text: summary }) : null), key);
  return keyed(el('div', { class: 'work-inline-selection' }, button,
    apps.length ? el('div', { class: 'card-apps' }, ...apps.map(({ app, path }) => appButton(app, path))) : null,
    selectedWork === key ? keyed(el('div', { 'data-preserve-children': 'true' }), 'work-detail-slot|' + key) : null), 'selection|' + key);
}
function workData() {
  const streams = lastWork?.workstreams || [], activity = lastWork?.activity || [];
  const rows = new Map([...streams, ...activity].map(r => [r.key, r]));
  workOrder = stableWorkOrder(Array.from(rows.keys()), workOrder);
  return { streams, activity, rows };
}
function checkoutRepository(path) {
  if (!path) return '';
  const exact = new Set([...(lastState?.checkouts || []).filter(c => c.path === path).map(c => c.repository),
    ...(lastWork?.agents || []).filter(a => a.checkout === path).map(a => a.repository)].filter(Boolean));
  return exact.size === 1 ? [...exact][0] : '';
}
function matchesWork(s, query, repo) {
  if (repo && (s.host_scoped ? !(s.derived?.tasks || []).some(t => (t.checkouts || []).some(c => checkoutRepository(c.path) === repo)) : (s.repo || s.repository || '') !== repo)) return false;
  const text = [s.name, s.id, s.repo, s.stage, s.checkout, s.cwd, s.workspace,
    ...(s.derived?.tasks || []).flatMap(t => [t.id, ...(t.checkouts || []).map(c => c.path)])].filter(Boolean).join(' ').toLowerCase();
  return !query || text.includes(query);
}
// One backend resolver owns task, pane and server attribution. The browser
// consumes its validated physical-checkout projection, never re-joins paths.
function validatedMembership(row) {
  if (!boardFresh() || !row.path || row.agents.some(a => a.checkout !== row.path)) return null;
  const matches = (lastWork.memberships || []).filter(m => m.checkout === row.path);
  if (matches.length !== 1) return null;
  const mapping = matches[0];
  if (row.agents.length && (!herdrFresh() || !row.agents.every(a => mapping.panes.includes(a.pane)) || mapping.panes.length !== row.agents.length)) return null;
  const stream = lastWork.workstreams.find(s => s.key === mapping.stream_key);
  if (!stream || lastWork.workstreams.filter(s => s.key === mapping.stream_key).length !== 1) return null;
  if (!mapping.task_key && !stream.derived && row.agents.length) return { stream, checkouts: [], mapping };
  const task = (stream.derived?.tasks || []).find(t => t.key === mapping.task_key);
  if (!task || (stream.derived?.tasks || []).filter(t => t.key === mapping.task_key).length !== 1) return null;
  const checkouts = (task.checkouts || []).filter(c => c.key === mapping.binding_key || (!mapping.binding_key && c.path === row.path && !c.observer_only));
  if (!checkouts.length || checkouts.some(c => JSON.stringify(c) !== JSON.stringify(checkouts[0]))) return null;
  return { stream, task, checkouts: [checkouts[0]], mapping };
}
function liveMembership(row) { return validatedMembership(row); }
function liveWorkData() {
  const entries = typeof overviewSnapshot === 'function' ? [...overviewSnapshot().values()] : [];
  const records = new Map(), ungrouped = [];
  for (const row of entries) {
    const agents = row.agents.filter(a => a.status === 'working' || a.status === 'blocked');
    // Keep last-known running activity explicitly uncertain during failures,
    // but do not promote quiet agents to current work.
    if (!agents.length && !row.apps.length) continue;
    const member = liveMembership(row); // validate every physical agent, including quiet ones
    const current = { ...row, agents };
    if (member) {
      const key = member.stream.key;
      if (!records.has(key)) records.set(key, { key, stream: member.stream, rows: [] });
      records.get(key).rows.push(current);
    } else ungrouped.push({ key: 'live|' + row.key, rows: [current] });
  }
  // Duplicate identifiers cannot be used as a stable selection or grouping key.
  for (const [key, record] of records) if (lastWork.workstreams.filter(s => s.key === key).length !== 1) {
    records.delete(key);
    for (const row of record.rows) ungrouped.push({ key: 'live|' + row.key, rows: [row] });
  }
  for (const record of ungrouped) records.set(record.key, record);
  return records;
}
function liveRowData(record) {
  const rows = record.rows, agents = rows.flatMap(r => r.agents), apps = rows.flatMap(r => r.apps.map(app => ({ app, path: r.path })));
  const paths = rows.map(r => r.path || r.cwd).filter(Boolean);
  return { agents, apps, paths, status: [
    ...new Set(agents.map(a => agentStatus(a.status))),
    ...(apps.length ? [`${apps.length} ${apps.length === 1 ? 'app' : 'apps'}`] : []),
    ...(!herdrFresh() && agents.length ? ['agents last known'] : []),
    ...(!resourcesFresh() && apps.length ? ['apps last known'] : [])].join(' · ') };
}
function renderWorkList() {
  const notice = $('work-notice');
  const uncertain = !boardFresh();
  notice.hidden = !uncertain && herdrFresh() && resourcesFresh();
  notice.textContent = uncertain ? 'Grouping outdated or unavailable; activity stays visible without a guessed catalog link. See Data sources.' :
    'Some observations are unavailable; retained activity is last known. See Data sources.';
  if (!lastWork && typeof overviewSnapshot !== 'function') return;
  const query = $('work-search').value.trim().toLowerCase(), repo = $('work-repo').value;
  const children = [];
  if (workScope === 'live') {
    liveRecords = liveWorkData();
    workOrder = stableWorkOrder([...liveRecords.keys()], workOrder);
    const visible = workOrder.map(k => liveRecords.get(k)).filter(Boolean).map(record => {
      // A host-catalog stream can span repositories. Filter physical rows by
      // their observed canonical repository, never by the catalog directory.
      const rows = repo && record.stream?.host_scoped ? record.rows.filter(row => row.repository === repo) : record.rows;
      return rows.length ? { ...record, rows } : null;
    }).filter(Boolean).filter(record => {
      const data = liveRowData(record);
      return (!repo || record.stream?.host_scoped || (record.stream?.repo || record.rows[0].repository) === repo) &&
        (!query || [record.stream?.name, record.stream?.id, record.stream?.host_scoped ? '' : record.stream?.repo, ...data.paths,
          ...data.agents.flatMap(a => [a.name, a.pane, a.status]), ...data.apps.map(a => a.app.label)].filter(Boolean).join(' ').toLowerCase().includes(query));
    });
    liveRecords = new Map(visible.map(record => [record.key, record]));
    for (const [title, group] of [['Live workstreams', visible.filter(r => r.stream)], ['Ungrouped activity', visible.filter(r => !r.stream)]]) {
      if (!group.length) continue;
      children.push(workDisclosure('work|live|' + title, [el('h2', { text: title }), el('span', { class: 'muted', text: String(group.length) })], group.map(record => {
        const data = liveRowData(record);
        const label = record.stream ? record.stream.name || record.stream.id || 'Unnamed workstream' : shortRepository(data.paths[0], [...liveRecords.values()].flatMap(r => r.rows.map(row => row.path || row.cwd)));
        const declaration = record.stream && /park|cancel|complet/i.test(record.stream.derived?.section || '') ? ` · declared ${record.stream.derived.section.toLowerCase()} (unchanged)` : '';
        return workButton(record.key, label, `${record.stream && !record.stream.host_scoped ? shortRepository(record.stream.repo) : shortRepository(record.rows[0].repository)} · ${data.paths.map(p => shortRepository(p, data.paths)).join(', ')}${declaration}`, data.status, data.apps);
      }), true));
    }
  } else {
    const { streams, rows } = workData();
    const visible = workOrder.map(k => rows.get(k)).filter(s => s && streams.includes(s) && matchesWork(s, query, repo));
    for (const section of workSections) {
      const group = visible.filter(s => streamSection(s) === section);
      if (!group.length) continue;
      children.push(workDisclosure('work|section|' + section, [el('h2', { text: section }), el('span', { class: 'muted', text: String(group.length) })],
        group.map(s => {
          const tasks = s.derived?.version === 1 ? (s.derived.tasks || []).filter(t => !repo || !s.host_scoped || (t.checkouts || []).some(c => checkoutRepository(c.path) === repo)) : [];
          taskOrders.set(s.key, stableWorkOrder(tasks.map(t => t.key), taskOrders.get(s.key) || []));
          const byKey = new Map(tasks.map(t => [t.key, t]));
          const row = workButton(s.key, s.name || s.id || 'Unnamed workstream', `${s.host_scoped ? 'Catalog scope · repository unverified' : shortRepository(s.repo)} · declared ${s.stage || 'stage unknown'}`, 'Registered history · activity not verified');
          if (!tasks.length) return keyed(el('div', { class: 'work-stream' }, row), 'row|' + s.key);
          return keyed(el('div', { class: 'work-stream' }, row,
            workDisclosure('work|stream|' + s.key, [el('span', { text: `${tasks.length} registered task(s)` })], taskOrders.get(s.key).map(k => {
              const t = byKey.get(k);
              return workButton(t.key, t.id, `Declared ${t.intent || 'intent unknown'} · ${(t.checkouts || []).length} checkout(s)`, t.decision?.reason);
            }), tasks.some(t => t.key === selectedWork))), 'row|' + s.key);
        }), group.some(s => s.key === selectedWork || (s.derived?.tasks || []).some(t => t.key === selectedWork))));
    }
  }
  if (!children.length) children.push(paragraph(query || repo ? 'No matching activity in this scope.' : workScope === 'all' ?
    'No registered workstreams supplied. This does not establish completion.' :
    herdrFresh() && resourcesFresh() ? 'No working agents or running checkout apps in fresh observations.' : 'Current activity unknown. Last known activity appears when available; see Data sources.', 'muted'));
  syncChildren($('work-list'), children);
}
function selectedRecord() {
  if (workScope === 'live') {
    const record = liveRecords.get(selectedWork);
    if (!record) return null;
    const data = liveRowData(record);
    const checkouts = record.rows.filter(row => row.path).map(row => ({ path: row.path }));
    return record.stream ? { type: 'stream', stream: record.stream, title: record.stream.name, panes: data.agents.map(a => a.pane), checkouts } :
      { type: 'activity', activity: { checkout: record.rows[0].path, cwd: record.rows[0].cwd }, title: data.paths[0] || 'Working directory unknown', panes: data.agents.map(a => a.pane), checkouts };
  }
  if (!lastWork) return null;
  for (const s of lastWork.workstreams || []) {
    if (s.key === selectedWork) return { type: 'stream', stream: s, title: s.name, panes: s.live_panes || [], checkouts: (s.derived?.tasks || []).flatMap(t => t.checkouts || []) };
    for (const t of s.derived?.tasks || []) if (t.key === selectedWork) return { type: 'task', stream: s, task: t, title: t.id, panes: t.live_panes || [], checkouts: t.checkouts || [] };
  }
  for (const g of lastWork.activity || []) if (g.key === selectedWork) return { type: 'activity', activity: g, title: g.checkout || g.cwd || 'Working directory unknown', panes: g.panes, checkouts: g.checkout ? [{ path: g.checkout }] : [] };
  return null;
}
// The producer shortens read plan.path for display. Registered provenance
// is authoritative; a ~/ label alone cannot prove a plan or contradict it.
function compatibleObservedPlan(o, registeredPlan) {
  const source = o.plan_source || '', display = o.plan?.path || '';
  if (source === 'registered review assignment; no implementation plan') return false;
  if (source.startsWith('registered: ') && source.slice('registered: '.length) !== registeredPlan) return false;
  if (!display) return true; // missing read: scoped binding evidence still decides membership
  if (display.startsWith('~/')) return !!registeredPlan && source === 'registered: ' + registeredPlan;
  return display.startsWith('/') && display === registeredPlan;
}
function boardAgentsFor(r) {
  // Cached observations may be shown only when the resolver assigned that
  // exact pane to this task. Shared paths and repository labels prove nothing.
  const assignments = (lastWork?.memberships || []).filter(m => r.type !== 'activity' &&
    m.stream_key === r.stream?.key && (!r.task || m.task_key === r.task.key));
  return (lastWork?.board_agents || []).filter(a => {
    const o = a.observation;
    const mapping = assignments.find(m => (m.board_panes || []).includes(o.pane));
    if (!mapping || mapping.task_key && o.task_key !== mapping.task_key) return false;
    const task = (r.stream?.derived?.tasks || []).find(t => t.key === mapping.task_key);
    const bindings = (task?.checkouts || []).filter(c => c.path === mapping.checkout && !c.observer_only &&
      (mapping.binding_key ? c.key === mapping.binding_key : true));
    const binding = bindings.length && bindings.every(c => JSON.stringify(c) === JSON.stringify(bindings[0])) ? bindings[0] : null;
    const reviewer = o.role_from_name === 'reviewer' && o.role_source === 'registered' &&
      o.plan_source === 'registered review assignment; no implementation plan' && !o.plan;
    if (mapping.task_key && !binding) return false;
    if (!reviewer && binding && !compatibleObservedPlan(o, binding.registered_plan || '')) return false;
    return (lastWork.agents || []).some(live => live.pane === o.pane && live.name === o.agent_name && live.checkout === o.checkout && (!o.cwd || live.cwd === o.cwd));
  });
}
function paragraph(text, cls = '') { return el('p', { text, class: cls }); }
function facts(rows) { const root = el('dl', { class: 'work-facts' }); for (const [k, v] of rows) root.append(el('dt', { text: k }), el('dd', { text: v || 'Unknown' })); return root; }
function checksView(c, label) {
  if (!c) return paragraph(label + ': not observed', 'muted');
  return el('div', { class: 'work-checks' }, paragraph(`${label}: ${c.rollup || 'rollup unknown'} · ${c.passing}/${c.total} passing · ${c.failing} failing · ${c.pending} pending · ${c.awaiting_approval} awaiting approval`),
    ...['failed', 'waiting', 'running'].flatMap(k => (c[k] || []).map(check => el('p', {}, outbound(check.url, check.name), ` · ${check.state}${check.workflow ? ' · ' + check.workflow : ''}`))),
    c.truncated || c.unnamed ? paragraph(`Check names incomplete${c.unnamed ? ' · ' + c.unnamed + ' unnamed' : ''}`, 'muted') : null);
}
function prView(pr) {
  return keyed(el('article', { class: 'work-pr' }, el('h3', {}, outbound(pr.url, `${pr.repository || pr.reference || 'PR'} #${pr.number}${pr.title ? ' · ' + pr.title : ''}`)),
    paragraph(`${pr.state || 'state unknown'}${pr.is_draft ? ' · draft' : ''} · ${pr.review_decision || 'review unknown'}`),
    paragraph('PR observed ' + stamp(pr.observed_at), 'muted'), pr.error ? paragraph(pr.error, 'source-warning') : null,
    checksView(pr.checks, 'Head checks'), pr.merge_queue ? el('div', {}, paragraph(`Merge queue: ${pr.merge_queue.state} · position ${pr.merge_queue.position}${pr.merge_queue.eta_seconds != null ? ' · supplied ETA ' + pr.merge_queue.eta_seconds + 's' : ''}`), paragraph('Enqueued ' + stamp(pr.merge_queue.enqueued_at), 'muted'), checksView(pr.merge_queue.group, 'Merge-group checks')) : null,
    pr.merge_queue_exit ? el('div', {}, paragraph(`Retained queue exit: ${pr.merge_queue_exit.last_state} · position ${pr.merge_queue_exit.position}`), paragraph('Exit observed ' + stamp(pr.merge_queue_exit.observed_at), 'muted'), checksView(pr.merge_queue_exit.group, 'Retained merge-group checks')) : null), `pr|${pr.repository}|${pr.number}|${pr.reference || ''}`);
}
function planViews(r, boardAgents) {
  const plans = new Map();
  for (const c of r.checkouts) if (c.plan?.path && !plans.has(c.plan.path)) plans.set(c.plan.path, { plan: c.plan, at: c.observed_at });
  for (const a of boardAgents) if (a.observation.plan?.path && !plans.has(a.observation.plan.path)) plans.set(a.observation.plan.path, { plan: a.observation.plan, at: a.meta.collected_at });
  return Array.from(plans, ([path, { plan, at }]) => keyed(el('article', { class: 'work-plan' },
    paragraph(`Plan: ${plan.done} done · ${plan.open} open · ${plan.blocked} blocked`), paragraph(`Plan observed ${stamp(at)}`, 'muted'),
    ...(plan.blocked_lines || []).map(line => paragraph(line, 'source-warning')),
    details('work|' + selectedWork + '|plan|' + path, [el('span', { text: 'Plan path & age' })], paragraph(path, 'mono'), paragraph(`Reported file age at collection: ${plan.plan_age_min ?? 'unknown'} min`, 'muted'))), 'plan|' + path));
}
function summaryView(r) {
  const nodes = [];
  if (r.stream) {
    const s = r.stream, d = s.derived;
    nodes.push(facts([[s.host_scoped ? 'Catalog scope (not a repository)' : 'Repository', s.repo], ['Board source', `${s.source_mode || 'unknown'} · ${s.source || 'unknown'}`], ['Source modified', stamp(s.modified_at)], ['Board-reported stage', s.stage], ['Board-reported next', s.next], ['Board-reported needs (may include estimates)', s.needs || s.declared_needs || 'None supplied']]));
    if (s.warning) nodes.push(paragraph(s.warning, 'source-warning'));
    if (d) {
      nodes.push(paragraph(`Derived version ${d.version} · catalog revision ${d.catalog_revision} · board section ${d.section || 'unknown'} · declared ${r.task?.intent || d.intent || 'intent unknown'}`, 'muted'));
      nodes.push(paragraph(`Runtime ${d.runtime_known ? 'known at source' : 'unknown'} · ${stamp(d.runtime_observed_at)} · source fact maximum age ${d.fact_max_age || 'unknown'}`, 'muted'));
      if (d.input_error) nodes.push(paragraph(d.input_error, 'source-warning'));
      nodes.push(...[...(d.reasons || []), ...(d.diagnostics || [])].map(text => paragraph('Board: ' + text, 'muted')));
      const decision = r.task?.decision || d.decision;
      if (decision?.reason) nodes.push(paragraph(`Declared decision: ${decision.reason} · ${stamp(decision.at)}${decision.reference ? ' · ' + decision.reference : ''}`));
    } else nodes.push(paragraph('Legacy workstream: no registered task structure supplied.', 'muted'));
  } else nodes.push(paragraph('Observed checkout activity only. This is not a registered task or a completion classification.', 'muted'));
  nodes.push(paragraph(`Herdr observed ${stamp(lastWork?.herdr?.last_success)}${!herdrFresh() ? ' · last known / unknown' : ''}`, 'muted'));
  for (const c of r.checkouts) nodes.push(keyed(el('article', { class: 'work-checkout' }, paragraph(c.path, 'mono'),
    c.status ? paragraph(`Board checkout fact: ${c.status}${c.observer_only ? ' · observer only, not ownership' : ''}${c.server && c.server !== lastWork?.board_server ? ' · another Herdr server, runtime unavailable here' : ''} · ${stamp(c.observed_at)}`, 'muted') : null,
    c.repo ? paragraph('Board repository label: ' + c.repo + ' (display only)', 'muted') : null,
    c.expected_branch ? paragraph('Expected branch: ' + c.expected_branch) : null,
    c.git ? paragraph(`Observed branch: ${c.git.branch || 'unknown'} · ${c.git.status_known ? c.git.dirty_files + ' dirty file(s)' : 'git status unknown'}${c.git.head ? ' · ' + c.git.head : ''}`) : null,
    c.head_covered_by_merged_pr != null ? paragraph(`Head covered by merged PR: ${c.head_covered_by_merged_pr ? 'yes' : 'no'} (not completion or deployment)`, 'muted') : null,
    c.registered_plan ? paragraph('Registered plan: ' + c.registered_plan, 'mono') : null,
    c.plan_error ? paragraph(c.plan_error, 'source-warning') : null), 'checkout|' + (c.key || c.path)));
  const diagnostics = nodes.splice(0);
  const cached = boardAgentsFor(r);
  nodes.push(...planViews(r, cached));
  let prs = r.stream?.prs || [];
  if (r.task) prs = prs.filter(pr => (r.task.pr_references || []).includes(pr.reference));
  const seen = new Set(prs.map(pr => `${pr.repository}|${pr.number}`));
  for (const a of cached) {
    const pr = a.observation.pr;
    if (pr && !seen.has(`${pr.repository}|${pr.number}`)) { prs = prs.concat(pr); seen.add(`${pr.repository}|${pr.number}`); }
    if (!pr) nodes.push(paragraph(`Board PR lookup (${a.observation.agent_name || a.observation.pane}): ${a.observation.pr_lookup || 'unknown'} · ${stamp(a.meta.collected_at)}; unknown is not “no PR”.`, 'muted'));
  }
  if (prs.length) nodes.push(...prs.map(prView));
  else nodes.push(paragraph('No PR facts supplied for this selection; this does not establish that no PR exists.', 'muted'));
  nodes.push(workDisclosure('work|' + selectedWork + '|diagnostics', [el('span', { text: 'Full identity & source diagnostics' })], diagnostics));
  return nodes;
}
function agentsView(r) {
  const nodes = [paragraph('Current Herdr observations and cached board claims are separate sources.', 'muted')];
  const live = (workScope === 'live' ? [...overviewAgents.values()] : lastWork?.agents || []).filter(a => r.panes.includes(a.pane));
  for (const a of live) nodes.push(keyed(el('article', { class: 'work-agent' }, el('h3', { text: a.name || a.title || a.pane }),
    paragraph(`Herdr: ${agentStatus(a.status)} · ${a.pane} · ${a.workspace || 'workspace unknown'}${a.group ? ' · ' + a.group : ''}`),
    paragraph(`Context ${a.context || 'unknown'} · ${a.provider || 'provider unknown'}${a.pr ? ' · reported PR metadata: ' + a.pr : ''}`),
    paragraph(a.checkout || a.cwd || 'Working directory unknown', 'mono'), paragraph(stamp(lastWork?.herdr?.last_success) + (!herdrFresh() ? ' · last known / unknown' : ''), 'muted')), 'live|' + a.pane));
  if (!live.length) nodes.push(paragraph('No fresh, unambiguous live agent mapping for this selection. Check Work in progress for unmatched agents.', 'muted'));
  for (const a of boardAgentsFor(r)) {
    const o = a.observation, assessment = a.assessment;
    nodes.push(keyed(el('article', { class: 'work-agent board-claim' }, el('h3', { text: 'Board cache: ' + (o.agent_name || o.pane || 'Agent') }),
      paragraph(`${agentStatus(o.herdr_status)} · ${o.pane || 'pane unknown'} · ${o.workspace_label || o.workspace || 'workspace unknown'}`),
      paragraph(`Observed ${stamp(a.meta.collected_at)} · context ${o.context_used || 'unknown'}`, 'muted'),
      facts([['Checkout', o.checkout || o.cwd], ['Reported assignment', [o.repository, o.workstream_id, o.task_id].filter(Boolean).join(' · ')], ['Role / source', [o.role_from_name, o.role_source, o.plan_source].filter(Boolean).join(' · ')], ['Model', [o.provider, o.model_id].filter(Boolean).join(' / ')], ['PR lookup', o.pr_lookup || 'unknown']]),
      o.catalog_error ? paragraph(o.catalog_error, 'source-warning') : null,
      assessment ? el('div', { class: 'work-estimate' }, paragraph('AI estimates — not observations or task classifications', 'muted'),
        paragraph(`${assessment.status || 'unknown'} · ${assessment.model || 'model unknown'} · assessed ${stamp(assessment.assessed_at)}`, 'muted'),
        assessment.waiting_on ? paragraph('Estimated waiting on: ' + (assessment.waiting_on === 'clay' ? 'you' : assessment.waiting_on)) : null,
        ...[['Finished probability', assessment.finished], ['Stuck probability', assessment.stuck], ['Attention score', assessment.attention_score]].filter(([,v]) => v != null).map(([k,v]) => paragraph(`${k}: ${v}`))) : null), 'board|' + o.pane));
  }
  return nodes;
}
function resourceViews(r) {
  const paths = Array.from(new Set(r.checkouts.map(c => c.path).filter(Boolean)));
  if (!paths.length) return [paragraph('No declared Git checkout to match to Resources. Non-Git agents remain visible in Agents.', 'muted')];
  return paths.map(path => {
    const co = workScope === 'live' ? overviewResources.get(path) : lastState?.checkouts?.find(c => c.path === path);
    return keyed(co ? el('div', {}, paragraph('Physical checkout resources · shared bindings do not duplicate these values or imply exclusive task ownership.', 'muted'), card(co, false, 'work|' + selectedWork + '|')) : paragraph('No resource card currently exists for ' + path + '. No other app has been substituted.', 'muted'), 'resource|' + selectedWork + '|' + path);
  });
}
function renderWorkResources() {
  if (activeView === 'workstreams') { renderWorkRepositories(); renderWorkList(); renderWorkDetail(); }
}
function renderWorkDetail() {
  const r = selectedRecord();
  const slot = Array.from(document.querySelectorAll('[data-key]')).find(node => node.getAttribute('data-key') === 'work-detail-slot|' + selectedWork);
  const parent = slot || workDetailHome;
  if (parent && workDetailPanel.parentNode !== parent) parent.append(workDetailPanel);
  $('work-detail-title').textContent = r?.title || (selectedWork ? 'Selection no longer in the current projection' : 'Select a workstream or checkout');
  $('work-detail').hidden = activeView !== 'workstreams' || !r || !slot;
  $('work-detail-tabs').hidden = !r;
  for (const tab of ['summary', 'agents', 'resources']) {
    const button = $('work-tab-' + tab), active = tab === detailTab;
    button.setAttribute('aria-selected', String(active)); button.setAttribute('tabindex', active ? '0' : '-1');
  }
  $('work-detail-body').setAttribute('aria-labelledby', 'work-tab-' + detailTab);
  const body = !r ? [paragraph(selectedWork ? 'The selection is remembered. Empty results or provider failures do not establish completion.' : 'Choose a row to inspect its facts, agents and exact resource matches.', 'muted')] : detailTab === 'agents' ? agentsView(r) : detailTab === 'resources' ? resourceViews(r) : summaryView(r);
  syncChildren($('work-detail-body'), body);
  if (detailTab === 'resources') drawCheckoutSparks();
}
function selectDetailTab(tab, focus = false) {
  detailTab = tab; localStorage.setItem('devdash.work.tab', tab); renderWorkDetail();
  if (focus) $('work-tab-' + tab).focus();
}
for (const tab of ['summary', 'agents', 'resources']) {
  $('work-tab-' + tab).onclick = () => selectDetailTab(tab);
  $('work-tab-' + tab).onkeydown = e => {
    const tabs = ['summary', 'agents', 'resources'], i = tabs.indexOf(tab);
    const next = e.key === 'ArrowRight' ? (i + 1) % 3 : e.key === 'ArrowLeft' ? (i + 2) % 3 : e.key === 'Home' ? 0 : e.key === 'End' ? 2 : null;
    if (next != null) { e.preventDefault(); selectDetailTab(tabs[next], true); }
  };
}
function filterWork() { renderWorkList(); renderWorkDetail(); }
$('work-search').oninput = filterWork;
$('work-repo').onchange = filterWork;
function renderWorkRepositories() {
  const repos = Array.from(new Set([...(lastWork?.workstreams || []).filter(s => !s.host_scoped).map(s => s.repo),
    ...(lastWork?.agents || []).map(a => a.repository), ...(lastWork?.activity || []).map(g => g.repository),
    ...(lastState?.checkouts || []).map(c => c.repository)].filter(Boolean))).sort();
  const select = $('work-repo'), before = select.value;
  if (before && !repos.includes(before)) repos.push(before);
  syncChildren(select, [keyed(el('option', { value: '', text: 'All repositories' }), 'all'), ...repos.map(repo => keyed(el('option', { value: repo, text: shortRepository(repo, repos) }), repo))]);
  select.value = before;
}
function renderWork(w) {
  lastWork = w;
  const sources = [providerLine('Board', w.board, w.sweep_at), providerLine('Herdr', w.herdr, w.herdr.last_success)];
  if (w.route?.reason) sources.push(paragraph('Server association: ' + w.route.reason + '. Observations remain visible without an inferred task.', 'source-warning'));
  if (w.board.has_data) sources.push(paragraph('Board reads its existing cache every 15s. Observation/assessment refresh belongs to herdr-board; no sweep is triggered here.', 'muted'));
  if (workFetchError) sources.unshift(paragraph('Work API unavailable · last projection retained · ' + workFetchError, 'source-warning'));
  syncChildren($('work-sources'), sources);
  renderWorkRepositories();
  if (typeof renderOverview === 'function') renderOverview();
  renderWorkList(); renderWorkDetail();
}
let workPolling = false;
async function workTick() {
  if (workPolling) return;
  workPolling = true;
  try {
    const r = await fetch('/api/work');
    if (!r.ok) throw new Error(r.status + ' ' + r.statusText);
    const w = await r.json();
    receiveWork(w);
  } catch (e) {
    workFetchError = e.message;
    if (lastWork) renderWork(lastWork);
    else { $('work-sources').textContent = 'Work API unavailable: ' + e.message; renderWorkList(); }
    if (typeof renderOverview === 'function') renderOverview();
  } finally { workPolling = false; }
}
workTick();
setInterval(workTick, 5000);
