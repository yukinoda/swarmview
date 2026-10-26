import { $, el, txt, cls, attr, uniqueID } from './dom.js';
import { load, save } from './storage.js';
import { HEAT, stateLabel, fmtBytes, formatDuration, snapshotItems } from './agents.js';
import { createAgentRow, updateAgentRow, focusTerminal, syncAgentCards, setupDetails, refreshDetails } from './rows.js';

const QUIET_LIMIT = 3;
const rows = new Map(),
  groups = new Map(),
  quietRows = new Map();
let last = new Map(),
  rerender;
let pins = load('swarmview.pins', []),
  folded = new Set(load('swarmview.folded', []));
function togglePin(k) {
  pins = pins.includes(k) ? pins.filter((p) => p !== k) : [...pins, k];
  save('swarmview.pins', pins);
  rerender();
}
function toggleFold(k) {
  folded.has(k) ? folded.delete(k) : folded.add(k);
  save('swarmview.folded', [...folded]);
  rerender();
}
// fold every repo/worktree on screen (also drops keys of groups that no longer exist), or unfold everything
const foldAll = (on) => {
  folded = new Set(on ? [...groups.keys(), ...rows.keys()] : []);
  save('swarmview.folded', [...folded]);
  rerender();
};
function group(key, wt) {
  // repo or worktree header: fold toggle, label, status counts, pin (repos only)
  let g = groups.get(key);
  if (g) return g;
  g = {
    node: el('div'),
    head: el('div', 'gh'),
    fold: el('button', 'fold'),
    chev: el('span', 'chev'),
    label: el('span', 'gl'),
    counts: el('span', 'counts'),
    kids: el('div', 'kids')
  };
  g.chev.setAttribute('aria-hidden', 'true');
  g.kids.id = uniqueID('k');
  g.fold.setAttribute('aria-controls', g.kids.id);
  g.fold.append(g.chev, g.label);
  g.fold.onclick = () => toggleFold(key);
  g.head.append(g.fold, g.counts);
  if (!wt) {
    g.pin = el('button', 'pin');
    g.pin.append($('pin-icon').content.cloneNode(true));
    g.pin.setAttribute('aria-label', 'Pin ' + key.slice(2).split('/').pop());
    g.pin.onclick = () => togglePin(key.slice(2));
    g.head.append(g.pin);
  }
  g.node.append(g.head, g.kids);
  groups.set(key, g);
  return g;
}
function updateSystemMemory(sys) {
  const g = $('mem'),
    swap = sys?.swapUsedBytes ?? null,
    free = sys?.freePct ?? null,
    p = sys?.pressure;
  g.hidden = false;
  const pressure = { 1: 'normal', 2: 'warning', 4: 'critical' }[p] || 'unavailable';
  const context = [
    swap != null && 'swap ' + fmtBytes(swap),
    free != null && `RAM ${free}% free`
  ].filter(Boolean);
  const full = ['Pressure ' + pressure, ...context].join(' · ');
  txt(g, pressure === 'normal' && context.length ? context.join(' · ') : full);
  g.title = full + ' · machine-wide, not just these agents';
  txt($('system-details'), full);
  cls(g, p === 4 ? 'crit' : p === 2 ? 'warn' : '');
}

export function renderDashboard(data, again = false) {
  const cur = (last = snapshotItems(data, last, again));
  // repo -> worktrees, with rolled-up status for headers
  const repos = new Map();
  const stat = () => ({ n: { working: 0, waiting: 0, workers: 0 }, heat: 9 });
  const add = (s, it) => {
    if (!it.dying) {
      if (it.sub) s.n.workers++;
      else if (it.status in s.n) s.n[it.status]++;
    }
    s.heat = Math.min(s.heat, HEAT.indexOf(it.status));
  };
  for (const it of cur.values()) {
    let r = repos.get(it.repo);
    if (!r)
      repos.set(it.repo, (r = { key: it.repo, name: it.repoName, wts: new Map(), ...stat() }));
    let w = r.wts.get(it.wt);
    if (!w) r.wts.set(it.wt, (w = stat()));
    add(r, it);
    add(w, it);
  }
  const showWt = (r) => r.wts.size > 1 || !r.wts.has(r.key); // hide the level for a lone main checkout
  const box = (it) => {
    const r = repos.get(it.repo);
    return showWt(r) ? 'w:' + it.wt : 'r:' + it.repo;
  };

  const at = new Map(); // next child index per container
  const put = (b, e) => {
    const i = at.get(b) ?? 0;
    at.set(b, i + 1);
    if (b.children[i] !== e) b.insertBefore(e, b.children[i] ?? null);
  };
  const head = (g, key, label, title, s, wt) => {
    cls(g.node, (wt ? 'group wt' : 'group') + ' h-' + HEAT[s.heat]);
    txt(g.label, label);
    const f = folded.has(key),
      p = !!g.pin && pins.includes(key.slice(2));
    const n = Object.entries(s.n).filter(([, v]) => v),
      ck = [f, p, title, ...n.flat()].join();
    if (g.ck === ck) return; // nothing changed since last poll
    g.ck = ck;
    g.kids.hidden = f;
    g.label.title = title;
    txt(g.chev, f ? '▸' : '▾');
    g.fold.setAttribute('aria-expanded', !f);
    g.counts.replaceChildren(
      ...n.map(([st, v]) => {
        const e = el('span', 's-' + (st === 'workers' ? 'idle' : st));
        e.textContent = v + ' ' + stateLabel(st);
        return e;
      })
    );
    if (g.pin) {
      g.pin.setAttribute('aria-pressed', p);
      g.pin.title = p ? 'Unpin' : 'Pin to top';
    }
  };
  const pi = (k) => {
    const i = pins.indexOf(k);
    return i < 0 ? Infinity : i;
  };
  const live = new Set();
  for (const [rk, r] of [...repos].sort(
    ([ka, a], [kb, b]) => pi(ka) - pi(kb) || a.name.localeCompare(b.name)
  )) {
    const g = group('r:' + rk);
    live.add('r:' + rk);
    head(g, 'r:' + rk, r.name, rk, r, false);
    put($('tree'), g.node);
    if (!showWt(r)) continue;
    for (const [wk, w] of [...r.wts].sort(
      ([a], [b]) => (b === rk) - (a === rk) || a.localeCompare(b)
    )) {
      const wg = group('w:' + wk, true);
      live.add('w:' + wk);
      head(wg, 'w:' + wk, wk === rk ? 'main checkout' : wk.split('/').pop(), wk, w, true);
      put(g.kids, wg.node);
    }
  }
  const hot = (it) => !pins.includes(it.repo) && it.status === 'working';
  const items = [...cur.values()].sort((a, b) => hot(b) - hot(a) || a.ord - b.ord);
  const liveParents = new Set(items.filter(it => it.live).map(it => it.parent));
  const pending = new Map(),
    descendants = new Map();
  for (const it of items) if (it.parent) pending.set(it.parent, (pending.get(it.parent) || 0) + 1);
  const leaves = items.filter((it) => !pending.has(it.key));
  // Each row contributes once, after its children, regardless of the visual depth cap or folds.
  for (let i = 0; i < leaves.length; i++) {
    const it = leaves[i];
    if (!it.parent) continue;
    const own = descendants.get(it.key),
      total = descendants.get(it.parent) || { all: 0, workers: 0 };
    total.all += 1 + (own?.all || 0);
    total.workers += (it.sub ? 1 : 0) + (own?.workers || 0);
    descendants.set(it.parent, total);
    pending.set(it.parent, pending.get(it.parent) - 1);
    if (!pending.get(it.parent)) leaves.push(cur.get(it.parent));
  }
  for (const it of items) {
    // all rows exist before placement, so a parent sorted later still hosts its children
    let r = rows.get(it.key);
    if (!r) rows.set(it.key, (r = createAgentRow(it)));
    updateAgentRow(r, it);
    let ancestor = it,
      depth = 0;
    // shortcut: indentation caps at five levels to preserve title space; deeper telemetry still nests in the DOM.
    while (depth < 4 && ancestor.parent) {
      depth++;
      ancestor = cur.get(ancestor.parent);
    }
    r.node.style.setProperty('--depth', depth);
    const total = descendants.get(it.key),
      count = total?.all || 0,
      closed = folded.has(it.key);
    const noun = total?.workers === count ? 'worker' : 'nested agent';
    txt(
      r.workers,
      count ? ` · ${count} ${closed ? 'hidden ' : ''}${noun}${count === 1 ? '' : 's'}` : ''
    );
    r.kids.hidden = closed;
    r.fold = { count, closed, label: `${closed ? 'Show' : 'Hide'} ${count} ${noun}${count === 1 ? '' : 's'}`, toggle: () => toggleFold(it.key) };
    if (!r.kids.id) r.kids.id = uniqueID('k');
    const liveBranch = !it.live && liveParents.has(it.key);
    if (liveBranch && !r.caption) {
      r.caption = el('p', 'worker-caption');
      txt(r.caption, 'Live workers · metrics unavailable · names open main terminal');
      r.node.insertBefore(r.caption, r.kids);
    }
    if (r.caption) r.caption.hidden = !liveBranch || closed;
  }
  for (const it of items) {
    const r = rows.get(it.key),
      p = it.parent ? rows.get(it.parent) : null;
    // A changed parent can still be inside this row until its own placement runs.
    if (p && r.node.contains(p.node)) p.node.remove();
    put(p ? p.kids : groups.get(box(it)).kids, r.node);
  }
  for (const [k, r] of rows)
    if (!cur.has(k)) {
      r.node.remove();
      rows.delete(k);
    }
  for (const [k, g] of groups)
    if (!live.has(k)) {
      g.node.remove();
      groups.delete(k);
    }

  const mine = items.filter((i) => !i.dying && !i.sub);
  updateSystemMemory(data.system);
  txt(
    $('count'),
    `${mine.length} agent${mine.length === 1 ? '' : 's'} · ${mine.filter((it) => it.status === 'working').length} working`
  );
  $('empty').hidden = items.length > 0;
  txt($('tree-count'), `${repos.size} ${repos.size === 1 ? 'repository' : 'repositories'} · ${items.filter(it => it.sub && !it.dying).length} workers`);
  return { items: new Map([...rows.keys()].map((key) => [key, last.get(key)])), agents: mine };
}

export function refreshDashboard(retained) {
  const offline = retained === 'offline',
    stale = retained === 'stale';
  for (const [key, r] of rows) {
    const it = last.get(key),
      state = offline ? 'offline' : stale ? 'stale' : it.stText;
    cls(r.node, 'node s-' + (retained ? 'unknown' : it.status) + (it.sub ? ' worker' : '') + (it.live ? ' live-worker' : ''));
    if (r.st) txt(r.st, state);
    attr(
      r.toggle,
      'aria-label',
      `Details for ${it.name}: ${state}, RAM ${r.mem?.textContent || 'unavailable'}${offline || stale ? ' (retained)' : ''}`
    );
  }
  if (retained) $('quiet').hidden = true;
  refreshDetails(retained);
}

export function showQuiet(quiet) {
  syncAgentCards(
    $('quiet-list'),
    quietRows,
    quiet.slice(0, QUIET_LIMIT),
    (it) => 'quiet ' + formatDuration(it.idleSec)
  );
}

export function focusAgent(pid) {
  const row = rows.get('a:' + pid);
  if (row) return focusTerminal(row);
}

// Read-only row lookup for communication; tree ownership stays in this module.
export function getCommunicationRow(key) {
  const item = last.get(key);
  return { item, child: rows.get(key), parent: rows.get(item?.parent) };
}

export function setupDashboard(onChange, beforeDetailsChange, snapshotState) {
  rerender = onChange;
  setupDetails(key => {
    const item = last.get(key);
    return { item, row: rows.get(key), parent: last.get(item?.parent), host: last.get('a:' + item?.pid) };
  }, beforeDetailsChange, snapshotState);
  $('expand').onclick = () => foldAll(false);
  $('collapse').onclick = () => foldAll(true);
  $('legend').replaceChildren(
    ...HEAT.map((s) => {
      const d = el('span', 's-' + s),
        i = el('i');
      d.append(i, stateLabel(s));
      return d;
    })
  );
}
