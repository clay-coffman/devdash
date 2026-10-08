'use strict';

// Two independent observations, not a new data source. Retain each source's
// last known entities until that source supplies a successful fresh snapshot.
let overviewAgents = new Map();
let overviewResources = new Map();
let overviewProcesses = new Map();
let overviewContainers = new Map();
let overviewWorkRevision = -1;
let overviewStateRevision = -1;
let overviewBoardAgents = new Map();
let overviewHerdrAt = '';
let overviewResourceAt = '';
let overviewOrder = [];
let overviewQuietOrder = [];
let overviewAdmitted = new Set();
let overviewShowOther = false;
let overviewInitialized = false;
const overviewFactMembership = new Map(); // physical row key -> bounded, scoped last-compatible assignment
let overviewNamespace = ''; 

function workPollFresh() { return !workFetchError && pollRecent(workReceivedAt); }
function herdrFresh() {
  const p = lastWork?.herdr;
  return workPollFresh() && p?.state === 'ready' && p.has_data && !p.stale;
}
function resourcePollFresh() { return !!lastState && !stateFetchError && pollRecent(stateReceivedAt); }
function resourceError(prefix) { return (lastState?.errors || []).some(e => prefix.test(e)); }
// /proc supplies processes, ports and RSS independently of optional Docker.
function resourcesFresh() { return resourcePollFresh() && !resourceError(/^procs:/); }
function containersFresh() {
  return resourcePollFresh() && lastState.docker?.available !== false && !resourceError(/^docker(?: stats)?:/);
}
function boardFresh() {
  const p = lastWork?.board;
  return workPollFresh() && p?.state === 'ready' && p.has_data && !p.stale;
}
function retainObservation(previous, entries, fresh, usable, hasData = usable) {
  if (fresh) return new Map(entries);
  if (usable) return new Map([...previous, ...entries]);
  // Failed payloads cannot replace a known usable value. Initial backend cache
  // entries can still be shown explicitly as unknown/last known.
  return hasData ? new Map([...entries, ...previous]) : previous;
}
function scopedBoardAgent(a, membership) {
  const o = a.observation || {};
  if (!membership?.task) return !!membership?.stream && !membership.stream.derived;
  if (o.task_key !== membership.task.key) return false;
  const path = membership.checkouts?.[0]?.registered_plan || '';
  const reviewer = o.role_from_name === 'reviewer' && o.role_source === 'registered' &&
    o.plan_source === 'registered review assignment; no implementation plan' && !o.plan;
  if (!reviewer && !compatibleObservedPlan(o, path)) return false;
  return true;
}
function retainedMembershipValid(row, prior) {
  if (!prior || prior.evidence !== JSON.stringify([overviewNamespace, row.key, row.agents.map(a => [a.pane, a.name, a.checkout, a.cwd]).sort()])) return false;
  if (!boardFresh()) return true; // actual provider failure retains only compatible last-known evidence
  const m = prior.membership, matches = (lastWork.workstreams || []).filter(s => s.key === m.stream.key);
  if (matches.length !== 1) return false;
  const stream = matches[0];
  if (!m.task && (!row.agents.every(a => (stream.panes || []).includes(a.pane)) || stream.derived)) return false;
  if (m.task) {
    const tasks = (stream.derived?.tasks || []).filter(t => t.key === m.task.key);
    if (tasks.length !== 1) return false;
    const bindings = (tasks[0].checkouts || []).filter(c => c.key === m.mapping?.binding_key && c.path === row.path);
    if (!bindings.length || bindings.some(c => JSON.stringify(c) !== JSON.stringify(bindings[0]))) return false;
  }
  return !(lastWork.board_agents || []).some(a => row.agents.some(live => live.pane === a.observation.pane && live.name === a.observation.agent_name &&
    live.checkout === a.observation.checkout && (!a.observation.cwd || live.cwd === a.observation.cwd)) && !scopedBoardAgent(a, m));
}
function overviewSnapshot() {
  // Consumption is separate from painting: cached renders/filters/errors never
  // become new observations or renew source times.
  if (lastWork && overviewWorkRevision !== workRevision) {
    overviewWorkRevision = workRevision;
    const hp = lastWork.herdr, bp = lastWork.board;
    const usable = workPollFresh() && hp.state === 'ready' && hp.has_data;
    if (usable || (!overviewHerdrAt && hp.has_data)) overviewHerdrAt = hp.last_success;
    const namespace = JSON.stringify([lastWork.route?.server || '', lastWork.route?.verified || false, lastWork.board_server || '', lastWork.board?.state === 'missing']);
    if (overviewNamespace && overviewNamespace !== namespace) { overviewAgents.clear(); overviewBoardAgents.clear(); overviewFactMembership.clear(); }
    overviewNamespace = namespace;
    overviewAgents = retainObservation(overviewAgents, (lastWork.agents || []).map(a => [JSON.stringify([lastWork.route?.server || '', a.pane, a.checkout || '', a.cwd || '']), a]), herdrFresh(), usable, hp.has_data);
    overviewBoardAgents = retainObservation(overviewBoardAgents, (lastWork.board_agents || []).map(a => [JSON.stringify([lastWork.board_server || '', a.observation.pane, a.observation.checkout || '', a.observation.cwd || '']), a]), boardFresh(), workPollFresh() && bp.state === 'ready' && bp.has_data, bp.has_data);
  }
  if (lastState && overviewStateRevision !== stateRevision) {
    overviewStateRevision = stateRevision;
    const entries = lastState.checkouts.map(co => [co.path, co]);
    if (resourcesFresh()) { overviewProcesses = new Map(entries); overviewResourceAt = lastState.now; }
    overviewContainers = retainObservation(overviewContainers, entries.filter(([, co]) => co.stacks.length), containersFresh(), false, resourcePollFresh());
  }
  overviewResources = new Map();
  for (const path of new Set([...overviewProcesses.keys(), ...overviewContainers.keys()])) {
    const processes = overviewProcesses.get(path), containers = overviewContainers.get(path);
    const procBytes = processes?.proc_bytes || 0, stacks = containers?.stacks || [];
    const containerBytes = containers?.container_bytes ?? stacks.reduce((n, s) => n + s.bytes, 0);
    overviewResources.set(path, { ...(processes || containers), path, processes: processes?.processes || [], proc_bytes: procBytes,
      stacks, container_bytes: containerBytes, total_bytes: procBytes + containerBytes, containers_unknown: !containersFresh() });
  }
  const rows = new Map();
  const checkoutKey = path => JSON.stringify(['checkout', path]);
  for (const [path, co] of overviewResources) {
    if (!path) continue;
    rows.set(checkoutKey(path), { key: checkoutKey(path), path, co, agents: [], repository: '' });
  }
  for (const a of overviewAgents.values()) {
    // cwd is already normalized by the backend to foreground_cwd with fallback.
    const key = a.checkout ? checkoutKey(a.checkout) : JSON.stringify(['non-git', a.cwd || '', a.workspace || '', a.cwd ? '' : a.pane]);
    let row = rows.get(key);
    if (!row) { row = { key, path: a.checkout || '', cwd: a.cwd || '', workspace: a.workspace || '', agents: [], repository: '' }; rows.set(key, row); }
    row.agents.push(a);
  }
  for (const row of rows.values()) {
    row.apps = row.co ? checkoutApps(row.co).views : [];
    const repos = new Set([row.co?.repository, ...row.agents.map(a => a.repository)].filter(Boolean));
    // API activity repository is an exact, fresh mapping (never a name match).
    if (herdrFresh()) for (const g of lastWork?.activity || []) {
      if (g.repository && row.path && g.checkout === row.path && row.agents.some(a => g.panes.includes(a.pane))) repos.add(g.repository);
    }
    row.membership = overviewMembership(row);
    const evidenceKey = JSON.stringify([overviewNamespace, row.key, row.agents.map(a => [a.pane, a.name, a.checkout, a.cwd]).sort()]);
    const prior = overviewFactMembership.get(row.key);
    if (row.membership) overviewFactMembership.set(row.key, { evidence: evidenceKey, membership: row.membership });
    else if (boardFresh() && herdrFresh() || !retainedMembershipValid(row, prior)) overviewFactMembership.delete(row.key);
    row.factMembership = row.membership || overviewFactMembership.get(row.key)?.membership;
    if (row.membership?.stream.repo && !row.membership.stream.host_scoped) repos.add(row.membership.stream.repo);
    row.repository = repos.size === 1 ? [...repos][0] : '';
  }
  for (const key of overviewFactMembership.keys()) if (!rows.has(key)) overviewFactMembership.delete(key);
  return rows;
}
function overviewMembership(row) { return validatedMembership(row); }
function overviewPriority(row) {
  if (row.agents.some(a => a.status === 'blocked')) return 0;
  if (row.agents.some(a => a.status === 'working')) return 1;
  if (row.apps.length) return 2;
  if (row.agents.some(a => !['idle', 'done'].includes(a.status))) return 3;
  return 4;
}
function overviewEligible(row) { return overviewPriority(row) < 4; }
function admitOverviewRows(rows, refresh = false) {
  const priority = (a, b) => overviewPriority(rows.get(a)) - overviewPriority(rows.get(b)) || a.localeCompare(b);
  if (refresh) { overviewAdmitted = new Set(); overviewOrder = []; overviewShowOther = false; }
  overviewOrder = overviewOrder.filter(key => rows.has(key));
  overviewAdmitted = new Set(overviewOrder);
  const added = [...rows.keys()].filter(key => !overviewAdmitted.has(key) && overviewEligible(rows.get(key))).sort(priority);
  overviewOrder.push(...added);
  overviewAdmitted = new Set(overviewOrder);
  overviewQuietOrder = stableWorkOrder([...rows.keys()].filter(key => !overviewAdmitted.has(key)), overviewQuietOrder);
  return overviewOrder.concat(overviewQuietOrder);
}
function overviewMatches(row, query, repo) {
  if (repo && row.repository !== repo) return false;
  const text = [row.path, row.cwd, row.workspace, row.repository, row.co?.branch, row.membership?.stream.name, row.membership?.task?.id,
    ...row.agents.flatMap(a => [a.name, a.title, a.pane, a.workspace, a.group, a.status]), ...row.apps.flatMap(a => [a.label, a.port])].filter(Boolean).join(' ').toLowerCase();
  return !query || text.includes(query);
}
function overviewStatus(row) {
  const counts = new Map();
  for (const a of row.agents) counts.set(a.status || 'unknown', (counts.get(a.status || 'unknown') || 0) + 1);
  if (!herdrFresh()) {
    if (!counts.size) return [chip('muted', 'Agents unknown')];
    return [chip('muted', 'Last known', [...counts].map(([status, n]) => `${agentStatus(status)} · ${n}`).join(', '))];
  }
  if (!counts.size) return [el('span', { class: 'muted', text: 'No agents' })];
  return [...counts].sort(([a], [b]) => ['blocked', 'working', 'idle', 'done'].indexOf(a) - ['blocked', 'working', 'idle', 'done'].indexOf(b))
    .map(([status, n]) => chip(agentTone[status] || 'muted', agentStatus(status), n > 1 ? String(n) : '', { 'data-key': 'status|' + status }));
}
function overviewFacts(row) {
  const allowed = new Set(row.factMembership?.mapping?.board_panes || []);
  const cached = [...overviewBoardAgents.values()].filter(a => allowed.has(a.observation.pane) && scopedBoardAgent(a, row.factMembership) &&
    row.agents.some(live => live.pane === a.observation.pane && live.name === a.observation.agent_name && live.checkout === a.observation.checkout &&
      (!a.observation.cwd || live.cwd === a.observation.cwd)));
  const r = { type: row.factMembership?.task ? 'task' : 'activity', stream: row.factMembership?.stream, task: row.factMembership?.task,
    checkouts: row.factMembership?.checkouts || [], panes: row.agents.map(a => a.pane) };
  let prs = r.stream?.prs || [];
  if (r.task) prs = prs.filter(pr => (r.task.pr_references || []).includes(pr.reference));
  const uniquePRs = new Map(prs.map(pr => [JSON.stringify([pr.repository, pr.number]), pr]));
  for (const a of cached) if (a.observation.pr) uniquePRs.set(JSON.stringify([a.observation.pr.repository, a.observation.pr.number]), a.observation.pr);
  const plans = new Map();
  for (const c of r.checkouts) if (c.plan?.path) plans.set(c.plan.path, { plan: c.plan, at: c.observed_at });
  for (const a of cached) if (a.observation.plan?.path && !plans.has(a.observation.plan.path)) plans.set(a.observation.plan.path, { plan: a.observation.plan, at: a.meta.collected_at });
  const planNodes = [...plans].map(([path, { plan, at }]) => keyed(el('article', { class: 'work-plan' },
    paragraph(`Plan: ${plan.done} done · ${plan.open} open · ${plan.blocked} blocked`),
    paragraph('Supplied observation ' + stamp(at), 'muted'),
    ...(plan.blocked_lines || []).map(line => paragraph(line, 'source-warning')),
    details('overview|' + row.key + '|plan|' + path, [el('span', { text: 'Plan path & age' })], paragraph(path, 'mono'), paragraph(`File age at collection: ${plan.plan_age_min ?? 'unknown'} min`, 'muted'))), 'plan|' + path));
  return [...planNodes, ...[...uniquePRs.values()].map(prView)];
}
function overviewDetails(row) {
  const agents = row.agents.map(a => keyed(el('article', { class: 'work-agent' }, el('h3', { text: a.name || a.title || 'Agent' }),
    paragraph(`${agentStatus(a.status)}${herdrFresh() ? '' : ' · last known, not live'} · ${a.pane} · workspace ${a.workspace || 'unknown'}${a.group ? ' · ' + a.group : ''}`),
    paragraph(`${a.provider || 'Provider unknown'} · context ${a.context || 'unknown'}${a.pr ? ' · reported PR: ' + a.pr : ''}`, 'muted'),
    paragraph('Herdr observed ' + stamp(overviewHerdrAt), 'muted')), 'agent|' + a.pane));
  const supplied = overviewFacts(row);
  const diagnostics = keyed(details('overview|' + row.key + '|diagnostics', [el('span', { text: 'Full identity & source facts' })],
    facts([['Exact checkout', row.path || 'Non-Git working directory'], ['Working directory', row.cwd || row.path], ['Repository', row.repository || 'Not mapped'], ['Branch', row.co?.branch || 'Not observed'], ['Workspace IDs', row.agents.map(a => a.workspace).filter(Boolean).join(', ')], ['Pane IDs', row.agents.map(a => a.pane).join(', ')]]),
    paragraph(`Herdr: ${herdrFresh() ? 'fresh' : 'unavailable / stale'} · ${stamp(overviewHerdrAt)}`, 'muted'),
    paragraph(`Processes/apps: ${resourcesFresh() ? 'fresh' : 'unavailable / last known'} · ${stamp(overviewResourceAt)}; container memory ${containersFresh() ? 'available' : 'unknown'}`, 'muted'),
    paragraph(`Board: ${boardFresh() ? 'fresh cached evidence' : 'unavailable / stale'} · ${stamp(lastWork?.sweep_at)}; AI estimates never supply live input status.`, 'muted')),
  'diagnostics|' + row.key);
  const resource = row.co ? keyed(card(row.co, false, 'overview|' + row.key + '|'), 'overview-resource|' + row.key + '|' + row.path)
    : paragraph(row.path ? 'No resource observation for this exact checkout. No other app has been substituted.' : 'Non-Git agent: no Git checkout resource match.', 'muted');
  return keyed(details('overview|' + row.key + '|details', [el('span', { text: 'Details' }), el('span', { class: 'sr-only', text: ' for ' + (row.path || row.cwd || row.agents[0]?.pane) })],
    el('div', { class: 'overview-detail-body' }, el('h3', { text: 'Agents' }), ...agents,
      row.agents.some(a => ['idle', 'done'].includes(a.status)) ? paragraph('Idle / agent finished does not establish task completion.', 'muted') :
        !agents.length ? paragraph(herdrFresh() ? 'No agents in the current observation.' : 'Agent availability is unknown.', 'muted') : null,
      supplied.length ? el('div', { class: 'overview-supplied' }, el('h3', { text: 'Supplied plans & PRs' + (!boardFresh() || !herdrFresh() || !row.membership ? ' · last known compatible facts' : '') }), ...supplied) : null,
      diagnostics, keyed(details('overview|' + row.key + '|resource-detail', [el('span', { text: 'Processes, containers & resource controls' })], resource), 'resource-detail|' + row.key))), 'details|' + row.key);
}
function overviewRow(row, paths, repositories) {
  const name = row.path ? shortRepository(row.path, paths) : (row.cwd ? shortRepository(row.cwd, paths) : 'Working directory unknown');
  const title = row.membership?.task?.id || row.membership?.stream.name || name;
  const context = [shortRepository(row.repository, repositories), row.co?.branch || 'Branch unknown', row.membership ? name : '', !row.path ? 'Non-Git · workspace ' + (row.workspace || 'unknown') : ''].filter(Boolean).join(' · ');
  return keyed(el('article', { class: 'overview-row' },
    el('div', { class: 'overview-row-main' },
      keyed(el('div', { class: 'overview-identity' }, el('h2', { text: title }),
        row.membership?.task && row.membership.stream.name ? el('div', { class: 'muted', text: row.membership.stream.name }) : null,
        el('div', { class: 'overview-context muted', text: context })), 'identity'),
      keyed(el('div', { class: 'overview-agents', role: 'group', 'aria-label': 'Agents' }, ...overviewStatus(row)), 'agents'),
      keyed(el('div', { class: 'overview-memory', role: 'group', 'aria-label': 'Memory' }, row.co ? fmtBytes(row.co.total_bytes) : 'Unknown',
        row.co && (!resourcesFresh() || !containersFresh()) ? el('small', { class: 'muted', text: !resourcesFresh() ? 'processes last known' : 'containers unknown' }) : null), 'memory'),
      keyed(el('div', { class: 'overview-apps', role: 'group', 'aria-label': 'Apps' }, ...row.apps.map(a => appButton(a, row.path)),
        row.apps.length && !resourcesFresh() ? el('span', { class: 'muted', text: 'Last known ports' }) : null,
        !row.apps.length ? el('span', { class: 'muted', text: resourcesFresh() ? '—' : 'Apps unknown' }) : null), 'apps')),
    overviewDetails(row)), row.key);
}
function renderOverviewSources() {
  const warning = [];
  if (workFetchError) warning.push(overviewAgents.size ? 'Work API unavailable; last known activity retained' : 'Work API unavailable; agent status unknown');
  else if (!herdrFresh()) warning.push(`Herdr ${lastWork?.herdr?.state === 'ready' ? 'stale' : lastWork?.herdr?.state || 'pending'}; agent status unknown`);
  if (lastWork?.board?.has_data && !boardFresh()) warning.push('Board cache stale; task labels withheld');
  else if (lastWork?.board?.state === 'error') warning.push('Board unavailable; catalog not available');
  if (stateFetchError || (lastState && !resourcesFresh())) warning.push(overviewResources.size ? 'Processes/apps unavailable; last known values' : 'Processes/apps unavailable; values unknown');
  else if (lastState && !containersFresh()) warning.push('Container memory unknown; process/apps live');
  const notice = $('source-notice'); notice.textContent = warning.join(' · '); notice.hidden = !warning.length;
  $('source-badge').textContent = warning.length ? 'Check freshness' : lastWork && lastState ? (lastWork.board.state === 'missing' ? 'Fresh resources · no Board' : 'Fresh') : 'Loading';
  const resourceLines = [paragraph(`Processes/apps: ${resourcesFresh() ? 'fresh' : 'unavailable / uncertain'} · ${stamp(overviewResourceAt)}; container memory ${containersFresh() ? 'available' : 'unknown'}`, 'muted')];
  if (stateFetchError) resourceLines.push(paragraph(stateFetchError, 'source-warning'));
  resourceLines.push(...(lastState?.errors || []).map(e => paragraph(e, 'source-warning')));
  if (lastWork && !herdrFresh() && !lastWork.herdr.stale) resourceLines.push(paragraph('Herdr browser poll is unavailable or aged beyond 15s; counts are unknown until a fresh read.', 'source-warning'));
  syncChildren($('resource-source'), resourceLines);
}
function renderOverview(refresh = false) {
  const rows = overviewSnapshot();
  // Network response order must not decide the initial priority. Wait until
  // both independent endpoints have either returned or explicitly failed.
  const settled = !!(lastWork || workFetchError) && !!(lastState || stateFetchError);
  const first = settled && !overviewInitialized;
  if (settled) overviewInitialized = true;
  const order = settled ? admitOverviewRows(rows, refresh || first) : [];
  const repositories = [...new Set([...rows.values()].map(r => r.repository).filter(Boolean))].sort();
  const select = $('overview-repo'), before = select.value;
  if (before && !repositories.includes(before)) repositories.push(before);
  syncChildren(select, [keyed(el('option', { value: '', text: 'All repositories' }), 'all'), ...repositories.map(repo => keyed(el('option', { value: repo, text: shortRepository(repo, repositories) }), repo))]);
  select.value = before;
  const query = $('overview-search').value.trim().toLowerCase();
  const paths = [...new Set([...rows.values()].map(row => row.path || row.cwd).filter(Boolean))];
  let shown = 0;
  const children = order.map(key => {
    const row = rows.get(key), node = overviewRow(row, paths, repositories);
    const visible = (overviewAdmitted.has(key) || overviewShowOther) && overviewMatches(row, query, before);
    if (!visible) node.setAttribute('hidden', ''); else shown++;
    return node;
  });
  syncChildren($('overview-list'), children);
  $('overview-other').textContent = `${overviewShowOther ? 'Hide' : 'Show'} other checkouts (${overviewQuietOrder.length})`;
  $('overview-other').setAttribute('aria-expanded', String(overviewShowOther));
  const empty = $('overview-empty'); empty.hidden = shown > 0;
  empty.textContent = !settled ? 'Waiting for work and resource observations. Unavailable does not mean no work.' : query || before ? 'No matching visible work. Show other checkouts to include quiet rows.' : rows.size ? 'No active agents or apps. Show other checkouts for quiet observations.' :
    !lastWork || !lastState ? 'Waiting for work and resource observations. Unavailable does not mean no work.' : herdrFresh() && resourcesFresh() ? 'No agents or checkout apps in the current observations.' : 'No current observations available. Resources and Workstreams remain accessible; unknown is not zero.';
  const working = [...overviewAgents.values()].filter(a => a.status === 'working').length;
  const blocked = [...overviewAgents.values()].filter(a => a.status === 'blocked').length;
  const apps = [...rows.values()].reduce((n, row) => n + row.apps.length, 0);
  syncChildren($('overview-counts'), [chip(herdrFresh() ? 'accent' : 'muted', herdrFresh() ? `${working} working` : 'Working unknown'),
    chip(herdrFresh() && blocked ? 'warn' : 'muted', herdrFresh() ? `${blocked} needs input` : 'Input status unknown'),
    chip(resourcesFresh() ? 'ok' : 'muted', resourcesFresh() ? `${apps} ${apps === 1 ? 'app' : 'apps'}` : `Apps unknown${apps ? ' · ' + apps + ' last known' : ''}`)]);
  renderOverviewSources();
  if (activeView === 'workstreams') { $('source-notice').hidden = true; renderWorkResources(); }
}
$('overview-search').oninput = () => renderOverview();
$('overview-repo').onchange = () => renderOverview();
$('overview-other').onclick = () => { overviewShowOther = !overviewShowOther; renderOverview(); };
$('overview-refresh').onclick = () => renderOverview(true);
renderOverview();
// A hanging poll must age into unknown even when no response/catch repaints.
setInterval(() => { if ((lastWork && !workPollFresh()) || (lastState && !resourcePollFresh())) renderOverview(); }, 1000);
