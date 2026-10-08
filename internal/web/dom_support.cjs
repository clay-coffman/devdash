// Minimal synthetic DOM; native browser checks remain authoritative for layout/focus.
const { readFileSync } = require('node:fs');
const vm = require('node:vm');
class Node {
  constructor(tag, text) { this.nodeType = tag ? 1 : 3; this.nodeName = tag ? tag.toUpperCase() : '#text'; this.tagName = tag?.toUpperCase(); this.nodeValue = text || ''; this.childNodes = []; this.map = new Map(); this.value = ''; this.parentNode = null; this.open = false; this.listeners = new Map(); }
  get attributes() { return Array.from(this.map, ([name, value]) => ({ name, value })); }
  get className() { return this.getAttribute('class') || ''; }
  set className(v) { this.setAttribute('class', v); }
  get textContent() { return this.nodeType === 3 ? this.nodeValue : this.childNodes.map(n => n.textContent).join(''); }
  set textContent(v) { this.replaceChildren(String(v)); }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  get childElementCount() { return this.children.length; }
  get firstChild() { return this.childNodes[0] || null; }
  get nextSibling() { return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) + 1] || null; }
  setAttribute(k,v) { this.map.set(k, String(v)); }
  getAttribute(k) { return this.map.get(k) ?? null; }
  hasAttribute(k) { return this.map.has(k); }
  removeAttribute(k) { this.map.delete(k); }
  addEventListener(type, handler) { if (!this.listeners.has(type)) this.listeners.set(type, []); this.listeners.get(type).push(handler); }
  dispatchEvent(event) { for (const handler of this.listeners.get(event.type) || []) handler.call(this, event); }
  append(...nodes) { for (let n of nodes) { if (!(n instanceof Node)) n = new Node(null, String(n)); n.remove(); n.parentNode = this; this.childNodes.push(n); } }
  replaceChildren(...nodes) { for (const n of this.childNodes) n.parentNode = null; this.childNodes = []; this.append(...nodes); }
  insertBefore(n, before) { n.remove(); const i = before ? this.childNodes.indexOf(before) : this.childNodes.length; this.childNodes.splice(i,0,n); n.parentNode = this; }
  remove() { if (this.parentNode) { const p = this.parentNode; p.childNodes.splice(p.childNodes.indexOf(this),1); this.parentNode = null; } }
  focus() { this.focused = true; }
}
function app(initial = {}, overview = true) {
  const elements = new Map(), storage = new Map(Object.entries(initial)), intervals = [];
  const c = vm.createContext({ URL, document: { createElement: tag => new Node(tag), getElementById(id) { if (!elements.has(id)) elements.set(id,new Node('div')); return elements.get(id); }, querySelectorAll: () => [] },
    localStorage: { getItem: k => storage.get(k) || null, setItem: (k,v) => storage.set(k,v) }, window: { addEventListener() {} }, fetch: () => new Promise(() => {}), setInterval: fn => { intervals.push(fn); return intervals.length; } });
  for (const name of ['app.js','work.js', ...(overview ? ['overview.js'] : [])]) vm.runInContext(readFileSync(`${__dirname}/static/${name}`, 'utf8'),c);
  c.storage = storage; c.intervals = intervals; return c;
}
module.exports = { app, get: (c,id) => c.document.getElementById(id), run: (c,s) => vm.runInContext(s,c), all: function all(n) { return [n,...n.childNodes.flatMap(all)]; } };
