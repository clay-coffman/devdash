// Run with: node --test internal/web/app_test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const vm = require('node:vm');

function element(tag) {
  return {
    tag, attrs: {}, children: [],
    setAttribute(key, value) { this.attrs[key] = value; },
    addEventListener() {},
    append(...children) { this.children.push(...children); },
    replaceChildren(...children) { this.children = children; },
    get childElementCount() { return this.children.filter(c => typeof c === 'object').length; },
  };
}
function app() {
  const elements = new Map();
  const context = vm.createContext({
    document: {
      createElement: element,
      getElementById(id) {
        if (!elements.has(id)) elements.set(id, element('div'));
        return elements.get(id);
      },
      querySelectorAll: () => [],
    },
    localStorage: { getItem: () => null, setItem() {} },
    window: { addEventListener() {} },
    fetch: () => new Promise(() => {}), // Don't start polling a real backend.
    setInterval() {},
  });
  vm.runInContext(readFileSync(`${__dirname}/static/app.js`, 'utf8'), context);
  return context;
}
const co = (path, bytes) => ({ path, total_bytes: bytes });
function order(ctx, checkouts, previous = []) {
  return Array.from(ctx.stableCheckoutOrder(checkouts, previous));
}

test('memory alerts work without reclaim data and do not recommend stopping anything', () => {
  const ctx = app();
  const notifications = [];
  ctx.Notification = class {
    static permission = 'granted';
    constructor(title, options) { notifications.push({ title, ...options }); }
  };
  ctx.maybeNotify({ host: 'devbox', mem: { available: 2 * 1024 ** 3, psi: {} } }, null);
  assert.equal(notifications.length, 1);
  assert.equal(notifications[0].body, '2.0 GiB available.');
});

test('dashboard markup has no reclaim section', () => {
  const html = readFileSync(`${__dirname}/static/index.html`, 'utf8');
  assert.doesNotMatch(html, /id="reclaim"|Safe to stop|Stop everything/);
});

test('initial order is largest first, with deterministic ties', () => {
  assert.deepEqual(order(app(), [co('b', 1), co('c', 2), co('a', 1)]), ['c', 'a', 'b']);
});
test('polling updates retain row positions despite changing memory and API order', () => {
  const ctx = app();
  const before = order(ctx, [co('a', 3), co('b', 2), co('c', 1)]);
  assert.deepEqual(order(ctx, [co('c', 8), co('b', 7), co('a', 1)], before), before);
});
test('new checkouts append, vanished ones disappear, and empty state resets cleanly', () => {
  const ctx = app();
  assert.deepEqual(order(ctx, [co('new', 9), co('b', 4)], ['a', 'b']), ['b', 'new']);
  assert.deepEqual(order(ctx, [], ['a', 'b']), []);
});
test('explicit sort uses current memory and renders fresh totals', () => {
  const ctx = app();
  vm.runInContext(`
    card = co => el('div', { text: co.path + ':' + co.total_bytes });
    drawCheckoutSparks = () => {};
    lastState = { checkouts: [
      { path: 'b', total_bytes: 8, agents: [1], stacks: [] },
      { path: 'a', total_bytes: 1, agents: [1], stacks: [] },
    ], unattributed: { processes: [], stacks: [] } };
    checkoutOrder = ['a', 'b'];
    renderCheckouts(lastState);
  `, ctx);
  const list = ctx.document.getElementById('checkout-list');
  assert.deepEqual(Array.from(list.children, c => c.textContent), ['a:1', 'b:8']);
  ctx.document.getElementById('sort-checkouts').onclick();
  assert.deepEqual(Array.from(list.children, c => c.textContent), ['b:8', 'a:1']);
});
test('app links visibly say Open and announce the new tab', () => {
  const ctx = app();
  const card = ctx.card({
    path: '/repo', display: '/repo', branch: 'main', total_bytes: 1, proc_bytes: 1,
    agents: [], stacks: [], processes: [
      { name: 'node', cmd: 'vite', cwd: '/repo/staff-web', ports: [5173], pid: 1, rss: 1, cpu: 0 },
    ],
  }, false);
  function find(node) {
    if (node?.className === 'view') return node;
    for (const child of node?.children || []) {
      const result = find(child);
      if (result) return result;
    }
  }
  const link = find(card);
  assert.ok(link);
  assert.equal(link.children[0], 'Open staff-web');
  assert.equal(link.attrs.href, 'http://localhost:5173/');
  assert.equal(link.attrs.target, '_blank');
  assert.equal(link.attrs.rel, 'noopener');
  assert.match(link.attrs['aria-label'], /opens in a new tab/);
  assert.equal(link.children.at(-1).textContent, '↗');
  assert.equal(card.children[0].className, 'card-head');
  assert.equal(card.children[1].className, 'card-apps');
  const resources = card.children[2];
  assert.match(resources.className, /card-resources/);
  assert.equal(resources.tag, 'details');
  assert.ok(!resources.open);
});

test('shared app extraction uses an exact checkout boundary and observed valid unique ports', () => {
  const ctx=app(),co={path:'/repo',processes:[
    {name:'node',cmd:'vite',cwd:'/repo/web',ports:[5173,5173,0,70000]},
    {name:'node',cmd:'vite',cwd:'/repo-neighbor/web',ports:[8080]},
    {name:'node',cmd:'next dev',cwd:'/repo/admin-web',ports:[3000]},
  ]};
  const {views,others}=ctx.checkoutApps(co);assert.deepEqual(Array.from(views,v=>v.port),[5173,3000]);assert.equal(others.get('node')[0],8080);
  assert.equal(ctx.checkoutApps(co,true).views.length,0,'unattributed apps must not acquire checkout buttons');
});

test('infrastructure and agent details are grouped behind Resources, not app actions', () => {
  const ctx = app();
  const card = ctx.card({
    path: '/repo', display: '/repo', branch: 'main', total_bytes: 1, proc_bytes: 1,
    agents: [{ name: 'builder', status: 'working' }],
    stacks: [{ project: 'database', class: 'live', running: 0, containers: [], bytes: 0 }],
    processes: [{ name: 'api', cmd: 'server', cwd: '/repo', ports: [3000], pid: 1, rss: 1, cpu: 0 }],
  }, false);
  assert.equal(card.children.length, 2, 'no empty app action row');
  const resources = card.children[1];
  const summary = resources.children[0];
  assert.equal(summary.tag, 'summary');
  assert.equal(summary.children[1].textContent, '1 process · 1 stack · 1 agent');
  assert.equal(summary.children.length, 2, 'only Resources and counts, no stop verdict');
  const body = resources.children[1];
  assert.equal(body.className, 'resource-body');
  assert.ok(body.children.some(c => c.className === 'resource-agents'));
  assert.ok(body.children.some(c => c.className === 'resource-ports'));
  assert.ok(body.children.some(c => c.className === 'folds'));
});
