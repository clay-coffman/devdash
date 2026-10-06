'use strict';

const $ = (id) => document.getElementById(id);
const GiB = 1024 ** 3, MiB = 1024 ** 2;
let token = '';
let lastState = null;
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
  const top = s.reclaim[0] ? ` Safest to stop: ${s.reclaim[0].display.split('/').pop()} (${fmtBytes(s.reclaim[0].bytes)}).` : '';
  const n = new Notification(`devdash: ${s.host} memory`, { body: reasons.join(' · ') + '.' + top, tag: 'devdash-mem' });
  n.onclick = () => { window.focus(); n.close(); };
}

const openSections = new Set(); // keys of <details> the user opened; survives re-render
function details(key, summaryChildren, ...body) {
  const d = el('details', { class: 'fold' }, el('summary', {}, ...summaryChildren), ...body);
  if (openSections.has(key)) d.open = true;
  d.addEventListener('toggle', () => { if (d.open) openSections.add(key); else openSections.delete(key); });
  return d;
}
function isWebApp(p, rel) {
  return /\b(vite|next|webpack|astro|remix|nuxt)\b/.test(p.cmd) || /(^|\/)[^/]*(web|frontend|ui)$/.test(rel) || /\bpreview\b/.test(p.cmd);
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
    if (k === 'class') e.className = v;
    else if (k === 'text') e.textContent = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v);
  }
  for (const c of children) if (c != null) e.append(c);
  return e;
}
function kv(container, rows) {
  container.replaceChildren(...rows.flatMap(([k, v, cls]) => [el('b', { text: k }), el('span', { text: v, class: cls || '' })]));
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
  const n = 300; // fixed 10-minute window at 2s
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
  $('meta').textContent = `up ${fmtDur(s.uptime)} · ${s.cores} cores · ${s.counts.agents} agents (${s.counts.working} working) · ${s.counts.stacks} stacks · ${s.counts.containers} containers · ${s.counts.processes} processes`;
  const etaMin = forecast(s.live, settings.availGiB * GiB);
  const etaText = etaMin == null ? '' : etaMin < 60 ? ` · ≈${Math.max(0, Math.round(etaMin))} min to ${settings.availGiB} GiB free at this rate` : '';
  $('glance').textContent = `mem ${fmtBytes(s.mem.used)} used · ${fmtBytes(s.mem.available)} free · cpu ${s.cpu.percent.toFixed(0)}%${etaText}`;
  $('glance').className = pct(s.mem.available, s.mem.total) < 10 || (etaMin != null && etaMin < settings.etaMin) ? 'bad' : 'muted';
  maybeNotify(s, etaMin);
  $('notify-btn').textContent = Notification.permission === 'granted' ? 'Notifying' : Notification.permission === 'denied' ? 'Notifications blocked' : 'Notify';
  $('notify-btn').disabled = Notification.permission !== 'default';
  const st = $('status');
  if (s.errors && s.errors.length) { st.textContent = '⚠ ' + s.errors.join(' · '); st.className = 'err'; }
  else { st.textContent = 'live · ' + new Date(s.now).toLocaleTimeString(); st.className = 'muted'; }

  const m = s.mem, usedP = pct(m.used, m.total), availP = pct(m.available, m.total);
  $('mem-big').textContent = `${fmtBytes(m.used)} / ${fmtBytes(m.total)}`;
  $('mem-sub').textContent = s.no_swap ? 'no swap: OOM kills land immediately' : `swap ${fmtBytes(m.swap_total - m.swap_free)} / ${fmtBytes(m.swap_total)}`;
  const memLevel = availP < 7 ? 'bad' : availP < 15 ? 'warn' : '';
  const mb = $('mem-bar'); mb.style.width = usedP.toFixed(1) + '%'; mb.className = memLevel;
  const psi = m.psi || {};
  kv($('mem-kv'), [
    ['available', `${fmtBytes(m.available)} (${availP.toFixed(0)}%)`, memLevel],
    ['buffers + cache', fmtBytes(m.buffers + m.cached)],
    ['memory pressure (some, 10s / 60s)', `${(psi.Some10 || 0).toFixed(1)}% / ${(psi.Some60 || 0).toFixed(1)}%`, level(psi.Some10 || 0, 5, 20)],
    ['memory pressure (full, 10s)', `${(psi.Full10 || 0).toFixed(1)}%`, level(psi.Full10 || 0, 1, 5)],
    ['forecast (5-minute trend)', etaMin == null ? 'not falling' : etaMin < 240 ? `≈${Math.max(0, Math.round(etaMin))} min to ${settings.availGiB} GiB free` : 'falling slowly', etaMin == null ? '' : etaMin < settings.etaMin ? 'bad' : etaMin < 60 ? 'warn' : ''],
  ]);
  spark($('mem-spark'), s.live.map(p => p.used), m.total, '#d64545', true);

  const c = s.cpu, loadP = pct(c.load.One, s.cores);
  $('cpu-big').textContent = c.percent.toFixed(0) + '%';
  $('cpu-sub').textContent = `load ${c.load.One.toFixed(1)} / ${c.load.Five.toFixed(1)} / ${c.load.Fifteen.toFixed(1)}`;
  const cb = $('cpu-bar'); cb.style.width = Math.min(c.percent, 100).toFixed(1) + '%'; cb.className = level(c.percent, 80, 95);
  kv($('cpu-kv'), [
    ['load₁ vs cores', `${loadP.toFixed(0)}%`, level(loadP, 100, 200)],
    ['cpu pressure (some, 10s / 60s)', `${(c.psi.Some10 || 0).toFixed(0)}% / ${(c.psi.Some60 || 0).toFixed(0)}%`, level(c.psi.Some10 || 0, 30, 60)],
    ['runnable / threads', `${c.load.Running} / ${c.load.Threads}`],
  ]);
  spark($('cpu-spark'), s.live.map(p => p.cpu), 100, '#3b82f6', true);

  const b = s.buckets;
  kv($('buckets-kv'), [
    ['herdr cgroup (agents + what they ran)', fmtBytes(b.herdr_cgroup)],
    ['docker containers', fmtBytes(b.containers)],
    ['your processes (RSS sum)', fmtBytes(b.own_processes)],
    ['checkouts with memory', String(s.checkouts.length)],
  ]);

  $('disk-list').replaceChildren(...s.disks.map(d => {
    const p = pct(d.used, d.total);
    return el('div', {}, el('div', { class: 'kv' }, el('b', { text: d.mount }), el('span', { text: `${fmtBytes(d.used)} / ${fmtBytes(d.total)} (${p.toFixed(0)}%)`, class: level(p, 80, 90) })),
      el('div', { class: 'bar' }, el('div', { style: `width:${p}%`, class: level(p, 80, 90) })));
  }));
  kv($('docker-df'), (s.docker.df || []).map(d => [`docker ${d.type.toLowerCase()} (${d.active}/${d.total})`, `${fmtBytes(d.size)}, ${fmtBytes(d.reclaimable)} reclaimable`]));
}

function processTable(allProcs, ownCard) {
  const minor = (p) => p.rss < 32 * MiB && !(p.ports && p.ports.length);
  const procs = allProcs.filter(p => !minor(p)), rest = allProcs.filter(minor);
  const t = el('table');
  t.append(el('tr', {}, el('th', { text: 'process' }), el('th', { text: 'command' }), el('th', { text: 'ports' }),
    el('th', { class: 'num', text: 'cpu' }), el('th', { class: 'num', text: 'rss' }), el('th')));
  for (const p of procs) {
    const row = el('tr', {});
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
    const row = el('tr', { class: 'minor' });
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
    if (c.health) status.append(el('span', { class: 'badge ' + c.health, text: c.health }));
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
  const perHour = d * 60 / ageMin;
  const sign = d >= 0 ? '+' : '−';
  const span = ageMin >= 55 && ageMin <= 65 ? '1 h' : `${ageMin} min`;
  return el('span', { class: 'growth ' + (perHour > 512 * MiB ? 'bad' : perHour > 128 * MiB ? 'warn' : ''), text: `${sign}${fmtBytes(Math.abs(d))} / ${span}`, title: 'change in total memory since the earliest sample within the last hour' });
}

function drawCheckoutSparks() {
  for (const canvas of document.querySelectorAll('canvas.co-spark')) {
    const h = checkoutHistory[canvas.dataset.path];
    if (!h || h.length < 2) { canvas.hidden = true; continue; }
    canvas.hidden = false;
    const dpr = window.devicePixelRatio || 1, w = 120, ht = 22;
    canvas.width = w * dpr; canvas.height = ht * dpr;
    const ctx = canvas.getContext('2d'); ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const t0 = Date.now() - 24 * 3600e3, t1 = Date.now();
    const max = Math.max(...h.map(p => p.bytes), 1);
    ctx.beginPath();
    h.forEach((p, i) => {
      const x = Math.max(0, (Date.parse(p.t) - t0) / (t1 - t0)) * w, y = ht - 1 - (p.bytes / max) * (ht - 2);
      i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
    });
    ctx.strokeStyle = '#3b82f6'; ctx.lineWidth = 1.2; ctx.stroke();
  }
}

function card(co, unattributed) {
  const c = el('div', { class: 'card' + (unattributed ? ' unattributed' : '') });
  // Web apps get a View button; other listening ports are listed quietly.
  const views = [], others = [];
  for (const p of co.processes) {
    if (!p.ports || !p.ports.length) continue;
    let rel = co.path && p.cwd.startsWith(co.path) ? p.cwd.slice(co.path.length).replace(/^\//, '') : '';
    const label = (rel.split('/').pop() || p.name);
    for (const port of p.ports) {
      if (!unattributed && isWebApp(p, rel)) views.push({ port, label });
      else others.push({ port, label });
    }
  }
  const head = el('div', { class: 'card-head' },
    el('h3', { text: unattributed ? 'Unattributed' : co.display.split('/').pop() }),
    co.branch ? el('span', { class: 'branch', text: co.branch }) : null,
    el('span', { class: 'views' },
      ...views.map(v => el('a', { class: 'view', href: `http://localhost:${v.port}/`, target: '_blank', rel: 'noopener' }, 'View ' + v.label + ' ', el('span', { class: 'port', text: ':' + v.port }))),
      ...others.map(v => el('span', { class: 'otherport', title: v.label }, link(v.port), ' ' + v.label))),
    el('span', { class: 'right' },
      growthBadge(co),
      unattributed ? null : el('canvas', { class: 'co-spark', 'data-path': co.path, width: 120, height: 22, title: 'last 24 hours' }),
      el('span', { class: 'total', text: fmtBytes(co.total_bytes) })),
    el('span', { class: 'path', text: unattributed ? 'processes and stacks outside any git checkout' : co.display }),
    !unattributed && co.reclaim ? el('span', { class: 'verdict score-' + (co.reclaim.score >= 70 ? 'high' : co.reclaim.score >= 40 ? 'mid' : co.reclaim.score > 0 ? 'low' : 'never'),
      text: (co.reclaim.score ? 'reclaim ' + co.reclaim.score : 'keep') + ' · ' + co.reclaim.reasons.join(' · ') }) : null);
  c.append(head);
  if (co.agents.length) {
    c.append(el('div', { class: 'agents' }, ...co.agents.map(a =>
      el('span', { class: 'agent ' + a.status, text: `${a.name || a.title || 'agent'} · ${a.status}${a.pr ? ' · ' + a.pr : ''}`, title: [a.group, a.title, a.context].filter(Boolean).join('\n') }))));
  }
  if (co.processes.length) {
    c.append(details(co.path + '|procs',
      [el('b', { text: `${co.processes.length} processes` }), el('span', { class: 'muted', text: ' · ' + fmtBytes(co.proc_bytes) })],
      processTable(co.processes, co)));
  }
  for (const st of co.stacks) {
    const summary = [el('b', { text: st.project }), ' ',
      el('span', { class: 'badge ' + st.class, text: st.class }), ' ',
      el('span', { class: 'muted', text: `${st.running}/${st.containers.length} running · ${fmtBytes(st.bytes)}${st.volumes ? ` · ${st.volumes} volumes` : ''}` })];
    if (st.class !== 'foreign') {
      summary.push(el('button', { class: 'danger stop-stack', text: 'Stop stack',
        onclick: (e) => { e.preventDefault(); confirmAction('stack.down', st, co); } }));
    }
    c.append(details(co.path + '|stack|' + st.project, summary, containerTable(st)));
  }
  return c;
}

function renderReclaim(s) {
  const sec = $('reclaim');
  const tight = pct(s.mem.available, s.mem.total) < 15 || (s.mem.psi.Some10 || 0) > 5;
  sec.hidden = !s.reclaim.length;
  sec.classList.toggle('tight', tight);
  $('reclaim-sub').textContent = tight ? 'memory is tight: these are the safest things to stop, best first' : 'safest things to stop if memory gets tight, best first';
  $('reclaim-list').replaceChildren(...s.reclaim.map(c =>
    el('div', { class: 'cand' },
      el('b', { text: c.display.split('/').pop() }),
      c.branch ? el('span', { class: 'branch', text: c.branch }) : null,
      el('span', { class: 'muted', text: c.reasons.join(' · ') }),
      el('span', { class: 'bytes', text: fmtBytes(c.bytes) }),
      el('button', { class: 'danger', text: 'Stop everything', onclick: () => confirmAction('checkout.stop', c) }))));
}

function renderCheckouts(s) {
  const root = $('checkouts');
  const small = (co) => !co.agents.length && !co.stacks.length && co.total_bytes < 256 * MiB;
  const main = s.checkouts.filter(co => !small(co)), rest = s.checkouts.filter(small);
  root.replaceChildren(el('h2', { text: 'Checkouts', }, ' ', el('span', { class: 'muted', text: 'sorted by memory: processes with their cwd inside, Herdr agents there, and the Compose stack started from it' })),
    ...main.map(co => card(co, false)));
  const u = s.unattributed;
  if (u.processes.length || u.stacks.length) root.append(card(u, true));
  drawCheckoutSparks();
  if (rest.length) {
    root.append(el('details', { class: 'small-checkouts' },
      el('summary', { class: 'muted', text: `${rest.length} small checkouts (no agents, no stack, under 256 MiB): ${fmtBytes(rest.reduce((a, c) => a + c.total_bytes, 0))}` }),
      ...rest.map(co => card(co, false))));
  }
}

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
  $('ports').replaceChildren(...s.ports.map(p =>
    el('div', {}, link(p.port), el('span', { text: p.name }), el('span', { class: 'cwd', text: p.cwd, title: p.cwd }))));
}

function render(s) {
  lastState = s; token = s.token;
  renderHeader(s); renderReclaim(s); renderCheckouts(s); renderExtras(s);
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
  } else if (kind === 'checkout.stop') {
    $('confirm-title').textContent = `Stop everything in ${target.display.split('/').pop()}?`;
    $('confirm-body').textContent = `Compose stacks go down (volumes kept) and every process with its cwd inside gets SIGTERM, except pi agent processes, which are left to Herdr. ${fmtBytes(target.bytes)} · ${target.reasons.join(' · ')}`;
    req = () => ({ type: 'checkout.stop', target: target.path });
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
    render(s);
  } catch (e) {
    $('status').textContent = '⚠ ' + e.message; $('status').className = 'err';
  }
}
async function history() {
  try {
    const r = await fetch('/api/history?hours=24');
    const j = await r.json();
    checkoutHistory = j.checkouts || {};
    drawHistory(j.samples || [], lastState ? lastState.mem.total : 0);
  } catch (e) { /* chart is optional */ }
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
window.addEventListener('resize', () => { if (lastState) renderHeader(lastState); history(); });
