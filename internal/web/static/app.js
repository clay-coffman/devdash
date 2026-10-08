'use strict';

const $ = (id) => document.getElementById(id);
const GiB = 1024 ** 3, MiB = 1024 ** 2;
let token = '';
let lastState = null;
let stateFetchError = '';
let stateReceivedAt = null;
let stateRevision = 0;
// Monotonic browser-local poll age; server timestamps remain display facts.
function pollClock() { return typeof performance === 'undefined' ? Date.now() : performance.now(); }
function pollRecent(receivedAt) { return receivedAt != null && pollClock() - receivedAt <= 15000; }
function receiveState(s) {
  if (!s || !Array.isArray(s.checkouts) || !s.checkouts.every(c => typeof c.path === 'string' && Array.isArray(c.processes) && Array.isArray(c.stacks))) throw new Error('Unsupported devdash resource snapshot');
  lastState = s; stateFetchError = ''; stateReceivedAt = pollClock(); stateRevision++;
}
let checkoutOrder = []; // Hold row positions between polls; only explicitly re-sort.
let lastHistory = null; // samples from /api/history, redrawn when the chart is opened
let checkoutHistory = {}; // path -> [{t, bytes}] from /api/history
const pendingKill = new Map(); // pid -> time TERM was sent
const settings = Object.assign({ availGiB: 4, etaMin: 15, psiSome: 10 }, JSON.parse(localStorage.getItem('devdash.settings') || '{}'));
function saveSettings() { localStorage.setItem('devdash.settings', JSON.stringify(settings)); }
let lastNotify = Number(localStorage.getItem('devdash.lastNotify') || 0);

// forecast fits available memory over the last five minutes of live points
// and returns minutes until it reaches the threshold, or null if not falling.
function forecast(live, thresholdBytes) {
  const pts = live.slice(-150);
  if (pts.length < 30) return null;
  const n = pts.length, t0 = pts[0].t;
  let sx = 0, sy = 0, sxx = 0, sxy = 0;
  for (const p of pts) { const x = p.t - t0, y = p.avail; sx += x; sy += y; sxx += x * x; sxy += x * y; }
  const slope = (n * sxy - sx * sy) / (n * sxx - sx * sx); // bytes per second
  if (!(slope < 0)) return null;
  const last = pts[n - 1].avail;
  if (last <= thresholdBytes) return 0;
  return (last - thresholdBytes) / -slope / 60;
}

function maybeNotify(s, etaMin) {
  if (Notification.permission !== 'granted') return;
  const availGiB = s.mem.available / GiB, psi = s.mem.psi.Some10 || 0;
  const reasons = [];
  if (availGiB < settings.availGiB) reasons.push(`${availGiB.toFixed(1)} GiB available`);
  if (etaMin != null && etaMin < settings.etaMin) reasons.push(`≈${Math.max(0, Math.round(etaMin))} min to ${settings.availGiB} GiB at this rate`);
  if (psi > settings.psiSome) reasons.push(`memory pressure ${psi.toFixed(0)}%`);
  if (!reasons.length || Date.now() - lastNotify < 10 * 60e3) return;
  lastNotify = Date.now(); localStorage.setItem('devdash.lastNotify', String(lastNotify));
  const n = new Notification(`devdash: ${s.host} memory`, { body: reasons.join(' · ') + '.', tag: 'devdash-mem' });
  n.onclick = () => { window.focus(); n.close(); };
}

const openSections = new Set(); // keys of <details> the user opened; survives re-render
function details(key, summaryChildren, ...body) {
  const d = el('details', { class: 'fold', 'data-key': key }, el('summary', {}, ...summaryChildren), ...body);
  if (openSections.has(key)) d.open = true;
  d.addEventListener('toggle', () => { if (d.open) openSections.add(key); else openSections.delete(key); });
  return d;
}
// chip renders one pill: a tone dot, a label, and an optional dimmed suffix.
function chip(tone, label, dim, attrs = {}) {
  const { class: extra = '', ...rest } = attrs;
  return el('span', { class: `chip t-${tone} ${extra}`.trim(), ...rest }, label, dim ? el('span', { class: 'dim', text: ' ' + dim }) : null);
}
function isWebApp(p, rel) {
  return /\b(vite|next|webpack|astro|remix|nuxt)\b/.test(p.cmd) || /(^|\/)[^/]*(web|frontend|ui)$/.test(rel) || /\bpreview\b/.test(p.cmd);
}

// Shared extraction and Open construction: Overview and Resources use the same
// observed process/checkout/port association, never board or branch guesses.
function checkoutApps(co, unattributed = false) {
  const views = [], others = new Map(), seen = new Set();
  for (const p of co.processes || []) {
    const inside = co.path && (p.cwd === co.path || p.cwd?.startsWith(co.path + '/'));
    const rel = inside ? p.cwd.slice(co.path.length).replace(/^\//, '') : '';
    const label = rel.split('/').pop() || p.name;
    for (const port of p.ports || []) {
      if (!Number.isInteger(port) || port < 1 || port > 65535) continue;
      if (!unattributed && inside && isWebApp(p, rel)) {
        if (!seen.has(port)) { views.push({ port, label }); seen.add(port); }
      } else others.set(label, [...(others.get(label) || []), port]);
    }
  }
  return { views, others };
}
function appButton(v, context = '') {
  return el('a', {
    class: 'view', 'data-key': 'app|' + v.port,
    href: `http://localhost:${v.port}/`, target: '_blank', rel: 'noopener',
    title: `Open ${v.label} on port ${v.port} in a new tab${context ? ' · ' + context : ''}`,
    'aria-label': `Open ${v.label}, port ${v.port}${context ? ', ' + context : ''} (opens in a new tab)`,
  }, `Open ${v.label}`, el('span', { class: 'port', text: ':' + v.port }), el('span', { 'aria-hidden': 'true', text: '↗' }));
}

function fmtBytes(b) {
  if (b >= GiB) return (b / GiB).toFixed(b >= 10 * GiB ? 0 : 1) + ' GiB';
  if (b >= MiB) return Math.round(b / MiB) + ' MiB';
  return Math.round(b / 1024) + ' KiB';
}
function pct(a, b) { return b > 0 ? 100 * a / b : 0; }
function level(p, warn, bad) { return p >= bad ? 'bad' : p >= warn ? 'warn' : ''; }
function fmtDur(s) {
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
}
function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined) continue;
    if (k === 'class') e.className = v;
    else if (k === 'text') e.textContent = v;
    else if (k.startsWith('on')) e[k] = v;
    else e.setAttribute(k, v);
  }
  for (const c of children) if (c != null) e.append(c);
  return e;
}
function kv(container, rows) {
  container.replaceChildren(...rows.flatMap(([k, v, cls, title]) => [el('b', { text: k, title: title || '' }), el('span', { text: v, class: cls || '', title: title || '' })]));
}
function shortCmd(cmd) {
  return cmd.replace(/(^| )\/\S*\/([^ \/]+\/[^ \/]+)/g, '$1$2');
}
function link(port) {
  return el('a', { href: `http://localhost:${port}/`, target: '_blank', rel: 'noopener', text: ':' + port });
}

// ---- sparklines -----------------------------------------------------------
function spark(canvas, points, max, color, fill) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth, h = canvas.clientHeight;
  if (canvas.width !== w * dpr) { canvas.width = w * dpr; canvas.height = h * dpr; }
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  if (points.length < 2) return;
  const n = Math.max(points.length, 60); // at least a two-minute window at 2 s per point
  const x = (i) => w - (points.length - 1 - i) * (w / (n - 1));
  const y = (v) => h - 1 - (Math.min(v, max) / max) * (h - 2);
  ctx.beginPath();
  points.forEach((v, i) => i ? ctx.lineTo(x(i), y(v)) : ctx.moveTo(x(i), y(v)));
  ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.stroke();
  if (fill) {
    ctx.lineTo(x(points.length - 1), h); ctx.lineTo(x(0), h); ctx.closePath();
    ctx.fillStyle = color + '33'; ctx.fill();
  }
}

function drawHistory(samples, total) {
  const canvas = $('history');
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth, h = canvas.clientHeight;
  canvas.width = w * dpr; canvas.height = h * dpr;
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  if (!samples.length) { ctx.fillStyle = 'gray'; ctx.fillText('no samples', 8, 20); return; }
  const t0 = Date.parse(samples[0].t), t1 = Date.parse(samples[samples.length - 1].t) || t0 + 1;
  const max = total || Math.max(...samples.map(s => s.used + s.avail));
  const x = (t) => (Date.parse(t) - t0) / (t1 - t0) * w;
  const y = (v) => h - 1 - (v / max) * (h - 14);
  // gridlines every quarter of total
  ctx.strokeStyle = 'rgba(128,128,128,.25)'; ctx.lineWidth = 1; ctx.font = '10px system-ui'; ctx.fillStyle = 'gray';
  for (let f = 0.25; f < 1; f += 0.25) {
    ctx.beginPath(); ctx.moveTo(0, y(max * f)); ctx.lineTo(w, y(max * f)); ctx.stroke();
    ctx.fillText(fmtBytes(max * f), 4, y(max * f) - 2);
  }
  // hour ticks
  for (let t = Math.ceil(t0 / 3600e3) * 3600e3; t < t1; t += 3600e3) {
    const d = new Date(t);
    if (d.getHours() % 3 === 0) ctx.fillText(d.getHours() + ':00', x(d.toISOString()) + 2, h - 2);
  }
  const line = (key, color, fill) => {
    ctx.beginPath();
    samples.forEach((s, i) => i ? ctx.lineTo(x(s.t), y(s[key])) : ctx.moveTo(x(s.t), y(s[key])));
    ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.stroke();
    if (fill) { ctx.lineTo(x(samples[samples.length - 1].t), h); ctx.lineTo(x(samples[0].t), h); ctx.fillStyle = color + '22'; ctx.fill(); }
  };
  line('used', '#d64545', true);
  line('avail', '#3a9d5d', false);
  line('herdr', '#3b82f6', false);
  line('hapi', '#d9a000', false);
}

// ---- rendering ------------------------------------------------------------
function renderHeader(s) {
  $('host').textContent = s.host;
  $('meta').textContent = `Memory ${fmtBytes(s.mem.used)} / ${fmtBytes(s.mem.total)} · ${fmtBytes(s.mem.available)} available · CPU ${s.cpu.percent.toFixed(0)}%`;
  const etaMin = forecast(s.live, settings.availGiB * GiB);
  maybeNotify(s, etaMin);
  $('notify-btn').textContent = Notification.permission === 'granted' ? 'Notifying' : Notification.permission === 'denied' ? 'Notifications blocked' : 'Notify';
  $('notify-btn').disabled = Notification.permission !== 'default';
  const st = $('status');
  if (stateFetchError || (s.errors && s.errors.length)) { st.textContent = stateFetchError ? 'Resources unavailable · last known' : 'Resource warnings'; st.className = 'err'; }
  else { st.textContent = 'live · ' + new Date(s.now).toLocaleTimeString(); st.className = 'muted'; }

  const m = s.mem, usedP = pct(m.used, m.total), availP = pct(m.available, m.total);
  $('mem-big').replaceChildren(fmtBytes(m.used), el('small', { text: ` / ${fmtBytes(m.total)}` }));
  const memLevel = availP < 7 ? 'bad' : availP < 15 ? 'warn' : '';
  // The one-line headline under the title: the forecast when it matters, otherwise headroom.
  const etaLevel = etaMin == null ? '' : etaMin < settings.etaMin ? 'bad' : etaMin < 60 ? 'warn' : '';
  const sub = $('mem-sub');
  if (etaMin != null && etaMin < 240) { sub.textContent = `≈${Math.max(0, Math.round(etaMin))} min to ${settings.availGiB} GiB free at this rate`; sub.className = 'muted ' + etaLevel; }
  else { sub.textContent = `${fmtBytes(m.available)} available`; sub.className = 'muted ' + memLevel; }
  const mb = $('mem-bar'); mb.style.width = usedP.toFixed(1) + '%'; mb.className = memLevel;
  const psi = m.psi || {};
  kv($('mem-kv'), [
    ['available', `${fmtBytes(m.available)} (${availP.toFixed(0)}%)`, memLevel],
    ['pressure, some / full 10s', `${(psi.Some10 || 0).toFixed(1)}% / ${(psi.Full10 || 0).toFixed(1)}%`, level(psi.Some10 || 0, 5, 20) || level(psi.Full10 || 0, 1, 5), `some 60s: ${(psi.Some60 || 0).toFixed(1)}%`],
    ['forecast', etaMin == null ? 'not falling' : etaMin < 240 ? `≈${Math.max(0, Math.round(etaMin))} min to ${settings.availGiB} GiB` : 'falling slowly', etaLevel],
    ['swap', s.no_swap ? 'none' : `${fmtBytes(m.swap_total - m.swap_free)} / ${fmtBytes(m.swap_total)}`, '', s.no_swap ? 'no swap: OOM kills land immediately' : ''],
  ]);
  spark($('mem-spark'), s.live.map(p => p.used), m.total, '#d64545', false);

  const c = s.cpu, loadP = pct(c.load.One, s.cores);
  $('cpu-big').textContent = c.percent.toFixed(0) + '%';
  $('cpu-sub').textContent = `load ${c.load.One.toFixed(1)} / ${c.load.Five.toFixed(1)} / ${c.load.Fifteen.toFixed(1)}`;
  const cb = $('cpu-bar'); cb.style.width = Math.min(c.percent, 100).toFixed(1) + '%'; cb.className = level(c.percent, 80, 95);
  kv($('cpu-kv'), [
    ['load₁ vs ' + s.cores + ' cores', `${loadP.toFixed(0)}%`, level(loadP, 100, 200)],
    ['pressure, some 10s', `${(c.psi.Some10 || 0).toFixed(0)}%`, level(c.psi.Some10 || 0, 30, 60), `some 60s: ${(c.psi.Some60 || 0).toFixed(0)}%`],
    ['runnable / threads', `${c.load.Running} / ${c.load.Threads}`],
  ]);
  spark($('cpu-spark'), s.live.map(p => p.cpu), 100, '#3b82f6', true);

  const b = s.buckets;
  kv($('buckets-kv'), [
    ['herdr cgroup', fmtBytes(b.herdr_cgroup), '', 'agents and everything they started'],
    ['docker containers', fmtBytes(b.containers)],
    ['your processes', fmtBytes(b.own_processes), '', 'RSS sum; shared pages counted more than once'],
    ['checkouts', String(s.checkouts.length)],
  ]);

  $('disk-list').replaceChildren(...s.disks.map(d => {
    const p = pct(d.used, d.total);
    return el('div', {}, el('div', { class: 'kv' }, el('b', { text: d.mount }), el('span', { text: `${fmtBytes(d.used)} / ${fmtBytes(d.total)} (${p.toFixed(0)}%)`, class: level(p, 80, 90) })),
      el('div', { class: 'bar' }, el('div', { style: `width:${p}%`, class: level(p, 80, 90) })));
  }));
}

function processTable(allProcs, ownCard) {
  const minor = (p) => p.rss < 32 * MiB && !(p.ports && p.ports.length);
  const procs = allProcs.filter(p => !minor(p)), rest = allProcs.filter(minor);
  const t = el('table');
  t.append(el('tr', { 'data-key': JSON.stringify(['process-header', ownCard?.path || '']) }, el('th', { text: 'process' }), el('th', { text: 'command' }), el('th', { text: 'ports' }),
    el('th', { class: 'num', text: 'cpu' }), el('th', { class: 'num', text: 'rss' }), el('th')));
  for (const p of procs) {
    const row = el('tr', { 'data-key': JSON.stringify(['process', ownCard?.path || '', p.pid]) });
    row.append(el('td', { text: p.name, title: 'pid ' + p.pid }));
    row.append(el('td', { class: 'cmd', text: shortCmd(p.cmd), title: p.cmd }));
    const ports = el('td', {});
    (p.ports || []).forEach((port, i) => { if (i) ports.append(' '); ports.append(link(port)); });
    row.append(ports);
    row.append(el('td', { class: 'num', text: p.cpu.toFixed(0) + '%' }));
    row.append(el('td', { class: 'num', text: fmtBytes(p.rss) }));
    const sent = pendingKill.get(p.pid);
    const forceReady = sent && Date.now() - sent > 5000;
    row.append(el('td', { class: 'num' }, el('button', {
      class: 'danger', text: forceReady ? 'Force kill' : sent ? 'TERM sent…' : 'Kill',
      onclick: () => confirmAction(forceReady ? 'KILL' : 'TERM', p, ownCard),
    })));
    t.append(row);
  }
  if (rest.length) {
    const row = el('tr', { class: 'minor', 'data-key': JSON.stringify(['process-minor', ownCard?.path || '']) });
    row.append(el('td', { class: 'muted', text: `${rest.length} small`, colspan: 4,
      title: rest.map(p => `${p.name} ${p.pid}: ${shortCmd(p.cmd)}`).join('\n') }));
    row.append(el('td', { class: 'num muted', text: fmtBytes(rest.reduce((a, p) => a + p.rss, 0)) }), el('td'));
    t.append(row);
  }
  return t;
}

function containerTable(stack) {
  const t = el('table');
  t.append(el('tr', {}, el('th', { text: 'service' }), el('th', { text: 'status' }), el('th', { text: 'ports' }),
    el('th', { class: 'num', text: 'cpu' }), el('th', { class: 'num', text: 'memory' }), el('th')));
  for (const c of stack.containers) {
    const row = el('tr', { class: c.state === 'running' ? '' : 'exited' });
    row.append(el('td', { text: c.service || c.name, title: c.name + ' · ' + c.image }));
    const status = el('td', { text: c.status.replace(/\s*\(.*\)\s*$/, '') + ' ' });
    if (c.health) status.append(chip(c.health === 'healthy' ? 'ok' : 'bad', c.health, null, { class: 'bare' }));
    row.append(status);
    const ports = el('td', {});
    (c.ports || []).forEach((port, i) => { if (i) ports.append(' '); ports.append(link(port)); });
    row.append(ports);
    row.append(el('td', { class: 'num', text: c.state === 'running' ? c.cpu.toFixed(1) + '%' : '' }));
    const mem = el('td', { class: 'num' });
    if (c.state === 'running') {
      const limited = c.mem_limit && c.mem_limit < 32 * GiB;
      mem.append(fmtBytes(c.mem_usage) + (limited ? ' / ' + fmtBytes(c.mem_limit) : ''));
      if (limited) {
        const p = pct(c.mem_usage, c.mem_limit);
        mem.append(el('span', { class: 'mini' }, el('i', { style: `width:${p}%`, class: level(p, 75, 90) })));
      }
    }
    row.append(mem);
    row.append(el('td', { class: 'num' },
      el('button', { text: 'logs', onclick: () => showLogs(c) }), ' ',
      c.state === 'running' && stack.class !== 'foreign'
        ? el('button', { class: 'danger', text: 'Stop', onclick: () => confirmAction('container.stop', c, stack) }) : null));
    t.append(row);
  }
  return t;
}

function growthBadge(co) {
  const h = checkoutHistory[co.path];
  if (!h || h.length < 2) return null;
  const cutoff = Date.now() - 3600e3;
  let ref = null;
  for (const p of h) { if (Date.parse(p.t) <= cutoff) ref = p; else break; }
  if (!ref) ref = h[0];
  const ageMin = Math.max(1, Math.round((Date.now() - Date.parse(ref.t)) / 60e3));
  const d = co.total_bytes - ref.bytes;
  if (Math.abs(d) < 64 * MiB) return null; // flat: say nothing
  const perHour = d * 60 / ageMin;
  const span = ageMin >= 55 ? 'in the last hour' : `in ${ageMin} min`;
  return el('span', { class: 'growth ' + (perHour > GiB ? 'bad' : perHour > 256 * MiB ? 'warn' : ''),
    text: `${d >= 0 ? '+' : '−'}${fmtBytes(Math.abs(d))} ${span}` });
}

function drawCheckoutSparks() {
  for (const canvas of document.querySelectorAll('canvas.co-spark')) {
    const h = checkoutHistory[canvas.dataset.path];
    if (!h || h.length < 3) { canvas.hidden = true; continue; }
    canvas.hidden = false;
    const dpr = window.devicePixelRatio || 1, w = 96, ht = 20;
    canvas.width = w * dpr; canvas.height = ht * dpr;
    const ctx = canvas.getContext('2d'); ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const t1 = Date.now(), t0 = Math.min(Date.parse(h[0].t), t1 - 3600e3); // data span, at least one hour
    canvas.title = `last ${fmtDur((t1 - t0) / 1000)}`;
    const vals = h.map(p => p.bytes), lo = Math.min(...vals), hi = Math.max(...vals);
    const span = Math.max(hi - lo, 256 * MiB); // never zoom into noise
    const mid = (hi + lo) / 2;
    const y = (v) => ht / 2 - ((v - mid) / span) * (ht - 4);
    ctx.beginPath();
    h.forEach((p, i) => {
      const x = Math.max(0, (Date.parse(p.t) - t0) / (t1 - t0)) * w;
      i ? ctx.lineTo(x, y(p.bytes)) : ctx.moveTo(x, y(p.bytes));
    });
    ctx.strokeStyle = '#3b82f6'; ctx.lineWidth = 1.2; ctx.stroke();
  }
}

const agentTone = { working: 'accent', blocked: 'warn', done: 'ok' };
function agentStatus(status) { return ({ working: 'Working', blocked: 'Needs input', idle: 'Idle', done: 'Agent finished' })[status] || 'Status unknown'; }
const stackTone = { live: 'ok', detached: 'warn', orphaned: 'bad' };

function card(co, unattributed, namespace = '') {
  const c = el('div', { class: 'card' + (unattributed ? ' unattributed' : '') });
  const { views, others } = checkoutApps(co, unattributed);
  // Identity and memory form a consistent two-column header.
  c.append(el('div', { class: 'card-head' },
    el('div', { class: 'card-identity' },
      el('h3', { text: unattributed ? 'Unattributed' : co.display.split('/').pop(), title: co.display }),
      el('div', { class: 'branch', text: unattributed ? 'Outside any git checkout' : co.branch || co.display })),
    el('div', { class: 'card-metric' },
      el('div', { class: 'memory-reading' },
        unattributed ? null : el('canvas', { class: 'co-spark', 'data-path': co.path, width: 96, height: 20, title: 'last 24 hours' }),
        el('div', {}, el('span', { class: 'metric-label', text: 'Memory' }),
          el('span', { class: 'total', text: fmtBytes(co.total_bytes) }),
          co.containers_unknown ? el('small', { class: 'muted', text: 'containers unknown' }) : null)),
      growthBadge(co))));
  // App actions get their own row, never mixed with infrastructure ports or status.
  if (views.length) {
    c.append(el('div', { class: 'card-apps' },
      ...views.map(v => appButton(v))));
  }
  // Everything diagnostic lives behind one disclosure.
  const body = el('div', { class: 'resource-body' },
    el('div', { class: 'resource-path mono', text: co.display }));
  if (co.agents.length) {
    body.append(el('div', { class: 'resource-agents', role: 'group', 'aria-label': 'Agents' },
      ...co.agents.map(a => chip(agentTone[a.status] || 'muted', a.name || a.title || 'agent', agentStatus(a.status) + (a.pr ? ' · ' + a.pr : ''),
        { title: [a.group, a.title, a.context].filter(Boolean).join('\n') }))));
  }
  if (others.size) {
    body.append(el('div', { class: 'resource-ports' }, el('span', { text: 'Other ports', class: 'muted' }),
      ...Array.from(others, ([label, ports]) => el('span', { class: 'portgroup' }, label + ' ',
        ...ports.flatMap((port, i) => [i ? ' ' : null, link(port)])))));
  }
  const folds = el('div', { class: 'folds' });
  if (co.processes.length) {
    folds.append(details(namespace + co.path + '|procs',
      [el('b', { text: `${co.processes.length} processes` }), el('span', { text: fmtBytes(co.proc_bytes) })],
      el('div', { class: 'table-scroll' }, processTable(co.processes, co))));
  }
  for (const st of co.stacks) {
    const summary = [el('b', { text: st.project }),
      chip(stackTone[st.class] || 'muted', st.class, null, { class: 'bare' }),
      el('span', { text: `${st.running}/${st.containers.length} running · ${fmtBytes(st.bytes)}${st.volumes ? ` · ${st.volumes} vol` : ''}` })];
    const tools = st.class === 'foreign' ? null : el('div', { class: 'fold-tools' },
      el('button', { class: 'danger', text: 'Stop stack', onclick: () => confirmAction('stack.down', st, co) }));
    folds.append(details(namespace + co.path + '|stack|' + st.project, summary, tools,
      el('div', { class: 'table-scroll' }, containerTable(st))));
  }
  body.append(folds);
  const counts = [`${co.processes.length} ${co.processes.length === 1 ? 'process' : 'processes'}`];
  if (co.stacks.length) counts.push(`${co.stacks.length} ${co.stacks.length === 1 ? 'stack' : 'stacks'}`);
  if (co.agents.length) counts.push(`${co.agents.length} ${co.agents.length === 1 ? 'agent' : 'agents'}`);
  const resources = details(namespace + co.path + '|resources', [
    el('b', { text: 'Resources' }),
    el('span', { class: 'resource-counts', text: counts.join(' · ') }),
  ], body);
  resources.className += ' card-resources';
  c.append(resources);
  return c;
}

// Existing checkouts keep their positions. New arrivals are appended, and vanished
// checkouts are removed. Passing [] deliberately resets the order to current memory.
function stableCheckoutOrder(checkouts, previous) {
  const present = new Set(checkouts.map(co => co.path));
  const retained = previous.filter(path => present.has(path));
  const seen = new Set(retained);
  const added = checkouts.filter(co => !seen.has(co.path))
    .sort((a, b) => b.total_bytes - a.total_bytes || a.path.localeCompare(b.path));
  return retained.concat(added.map(co => co.path));
}

function renderCheckouts(s) {
  const root = $('checkout-list');
  checkoutOrder = stableCheckoutOrder(s.checkouts, checkoutOrder);
  const byPath = new Map(s.checkouts.map(co => [co.path, co]));
  const ordered = checkoutOrder.map(path => byPath.get(path));
  const small = (co) => !co.agents.length && !co.stacks.length && co.total_bytes < 256 * MiB;
  const main = ordered.filter(co => !small(co)), rest = ordered.filter(small);
  root.replaceChildren(...main.map(co => card(co, false)));
  const u = s.unattributed;
  if (u.processes.length || u.stacks.length) root.append(card(u, true));
  if (rest.length) {
    root.append(details('checkouts|small',
      [el('span', { class: 'muted', text: `${rest.length} small checkouts · ${fmtBytes(rest.reduce((a, c) => a + c.total_bytes, 0))}` })],
      ...rest.map(co => card(co, false))));
  }
  drawCheckoutSparks();
}

$('sort-checkouts').onclick = () => {
  if (!lastState) return;
  checkoutOrder = [];
  renderCheckouts(lastState);
};

function renderExtras(s) {
  const vo = s.docker.volume_only || [];
  const df = Object.fromEntries((s.docker.df || []).map(d => [d.type, d]));
  const volRow = df['Local Volumes'], cacheRow = df['Build Cache'];
  $('cleanup').replaceChildren(
    vo.length ? el('button', { class: 'danger', text: `Remove volumes of ${vo.length} volume-only projects`, title: volRow ? `all local volumes: ${fmtBytes(volRow.size)}, ${fmtBytes(volRow.reclaimable)} unused` : '',
      onclick: () => confirmAction('docker.prune_volumes', { projects: vo }) }) : null,
    cacheRow && cacheRow.size ? el('button', { class: 'danger', text: `Prune build cache (${fmtBytes(cacheRow.size)})`,
      onclick: () => confirmAction('docker.prune_build_cache', cacheRow) }) : null);
  $('volume-only').replaceChildren(vo.length
    ? el('span', {}, `${vo.length} Compose projects exist only as volumes (no containers): `, el('code', { text: vo.map(v => v.project + (v.volumes ? ` (${v.volumes})` : '')).join(', ') }), '. ', el('span', { text: 'compose-stack-reaper --prune-volumes removes them.' }))
    : 'no volume-only projects');
  kv($('docker-df'), (s.docker.df || []).map(d => [`${d.type.toLowerCase().replace('local ', '')} ${d.active}/${d.total}`, fmtBytes(d.size), '', `${fmtBytes(d.reclaimable)} reclaimable`]));
  const sumParts = [];
  if (vo.length) sumParts.push(`${vo.length} volume-only projects`);
  if (cacheRow && cacheRow.size) sumParts.push(`build cache ${fmtBytes(cacheRow.size)}`);
  if (volRow) sumParts.push(`volumes ${fmtBytes(volRow.size)}`);
  $('docker-sum').textContent = sumParts.join(' · ') || 'nothing to clean up';
  $('ports-sum').textContent = `${s.ports.length} yours`;
  $('ports').replaceChildren(...s.ports.map(p =>
    el('div', {}, link(p.port), el('span', { text: p.name }), el('span', { class: 'cwd', text: p.cwd, title: p.cwd }))));
}

function render(s) {
  lastState = s; token = s.token;
  renderHeader(s); renderCheckouts(s); renderExtras(s);
  if (typeof renderWorkHealth === 'function') renderWorkHealth(s);
  if (typeof renderOverview === 'function') renderOverview();
}

// ---- actions --------------------------------------------------------------
function confirmAction(kind, target, parent) {
  const dlg = $('confirm'), volRow = $('confirm-volumes-row'), warn = $('confirm-warn');
  volRow.hidden = true; $('confirm-volumes').checked = false; warn.textContent = '';
  let req;
  const working = parent && parent.working ? `${parent.working} agent(s) in this checkout are working right now.` : '';
  if (kind === 'stack.down') {
    $('confirm-title').textContent = `Stop stack ${target.project}?`;
    $('confirm-body').textContent = `docker compose -p ${target.project} down · ${target.containers.length} containers, ${fmtBytes(target.bytes)} · classified ${target.class}`;
    volRow.hidden = false;
    warn.textContent = working;
    req = () => ({ type: 'stack.down', target: target.project, volumes: $('confirm-volumes').checked });
  } else if (kind === 'docker.prune_volumes') {
    $('confirm-title').textContent = `Remove volumes of ${target.projects.length} projects?`;
    $('confirm-body').textContent = 'Projects with no containers: ' + target.projects.map(p => p.project).join(', ') + '. Database contents in those volumes are lost. Docker refuses any volume still attached to a container.';
    req = () => ({ type: 'docker.prune_volumes', target: 'volume-only' });
  } else if (kind === 'docker.prune_build_cache') {
    $('confirm-title').textContent = 'Prune the Docker build cache?';
    $('confirm-body').textContent = `docker builder prune -f · ${fmtBytes(target.size)} total, ${fmtBytes(target.reclaimable)} reported reclaimable. The next image build is slower.`;
    req = () => ({ type: 'docker.prune_build_cache', target: 'build-cache' });
  } else if (kind === 'container.stop') {
    $('confirm-title').textContent = `Stop container ${target.name}?`;
    $('confirm-body').textContent = `docker stop · ${fmtBytes(target.mem_usage)} · ${target.status}`;
    warn.textContent = parent && parent.class !== 'live' ? '' : 'Its stack is classified live.';
    req = () => ({ type: 'container.stop', target: target.name });
  } else {
    const force = kind === 'KILL';
    $('confirm-title').textContent = `${force ? 'Force kill' : 'Kill'} ${target.name} (pid ${target.pid})?`;
    $('confirm-body').textContent = `${force ? 'SIGKILL' : 'SIGTERM'} · ${fmtBytes(target.rss)} · ${target.cmd}`;
    warn.textContent = working;
    req = () => ({ type: 'process.kill', target: String(target.pid), signal: force ? 'KILL' : 'TERM' });
  }
  dlg.returnValue = 'cancel';
  dlg.onclose = async () => {
    if (dlg.returnValue !== 'ok') return;
    const body = req();
    toast('working…');
    try {
      const r = await fetch('/api/action', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Devdash-Token': token }, body: JSON.stringify(body) });
      const j = await r.json();
      toast(j.detail, !j.ok);
      if (j.ok && body.type === 'process.kill') pendingKill.set(Number(body.target), Date.now());
      setTimeout(tick, 500);
    } catch (e) { toast(String(e), true); }
  };
  dlg.showModal();
}

async function showLogs(c) {
  const dlg = $('logs');
  $('logs-title').textContent = c.name;
  const load = async () => {
    $('logs-body').textContent = 'loading…';
    const r = await fetch(`/api/logs?container=${encodeURIComponent(c.name)}&tail=200`);
    $('logs-body').textContent = await r.text();
    $('logs-body').scrollTop = $('logs-body').scrollHeight;
  };
  $('logs-refresh').onclick = load;
  dlg.showModal();
  load();
}

let toastTimer;
function toast(msg, err) {
  const t = $('toast'); t.textContent = msg; t.hidden = false; t.className = err ? 'err' : '';
  clearTimeout(toastTimer); toastTimer = setTimeout(() => { t.hidden = true; }, err ? 8000 : 3500);
}

// ---- polling --------------------------------------------------------------
async function tick() {
  try {
    const r = await fetch('/api/state');
    if (!r.ok) throw new Error(r.status + ' ' + r.statusText);
    const s = await r.json();
    for (const [pid, t] of pendingKill) if (!s.checkouts.concat([s.unattributed]).some(c => c.processes.some(p => p.pid === pid)) || Date.now() - t > 60000) pendingKill.delete(pid);
    receiveState(s); render(s);
  } catch (e) {
    stateFetchError = e.message;
    $('status').textContent = 'Resources unavailable · last known'; $('status').className = 'err';
    if (typeof renderOverview === 'function') renderOverview();
  }
}
async function history() {
  try {
    const r = await fetch('/api/history?hours=24');
    const j = await r.json();
    checkoutHistory = j.checkouts || {};
    lastHistory = j.samples || [];
    drawChart();
    drawCheckoutSparks();
  } catch (e) { /* chart is optional */ }
}
// The chart sits in a collapsed <details>; a hidden canvas has no size, so draw only when it is open.
function drawChart() {
  if (lastHistory && $('more-history').open) drawHistory(lastHistory, lastState ? lastState.mem.total : 0);
}
// The secondary sections remember whether you left them open.
for (const d of document.querySelectorAll('details.more')) {
  d.open = localStorage.getItem('devdash.open.' + d.id) === '1';
  d.addEventListener('toggle', () => { localStorage.setItem('devdash.open.' + d.id, d.open ? '1' : '0'); if (d.id === 'more-history') drawChart(); });
}

$('notify-btn').onclick = async () => {
  const p = await Notification.requestPermission();
  if (p === 'granted') { toast('notifications on for this browser'); new Notification('devdash', { body: 'Notifications are on. You will hear about memory trouble here.' }); }
  if (lastState) renderHeader(lastState);
};
$('settings-btn').onclick = () => {
  $('set-avail').value = settings.availGiB; $('set-eta').value = settings.etaMin; $('set-psi').value = settings.psiSome;
  const dlg = $('settings');
  dlg.returnValue = 'cancel';
  dlg.onclose = () => {
    if (dlg.returnValue !== 'ok') return;
    settings.availGiB = Number($('set-avail').value) || 4; settings.etaMin = Number($('set-eta').value) || 15; settings.psiSome = Number($('set-psi').value) || 10;
    saveSettings(); if (lastState) renderHeader(lastState); toast('thresholds saved');
  };
  dlg.showModal();
};

tick().then(history);
setInterval(tick, 2000);
setInterval(history, 60000);
window.addEventListener('resize', () => { if (lastState) renderHeader(lastState); drawChart(); });
