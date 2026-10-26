import { $, el, txt, cls, attr } from './dom.js';
import { fmtBytes, label } from './agents.js';
import { logo } from './icons.js';

let selectedRow = null, selectedIdentity, detailCurrent, detailLookup, beforeDetails, readDetailsState;
let detailsGeneration = 0;
export function setupDetails(lookup, beforeOpen, snapshotState) {
  detailLookup = lookup;
  beforeDetails = beforeOpen;
  readDetailsState = snapshotState;
  $('close-details').onclick = closeDetails;
  $('detail-dialog').oncancel = event => { event.preventDefault(); closeDetails(); };
  $('detail-dialog').addEventListener('keydown', event => {
    if (event.key === 'Escape') { event.preventDefault(); closeDetails(); }
  });
  $('detail-dialog').addEventListener('close', () => { if (!$('detail-dialog').open && selectedRow) releaseDetails(); });
}
function releaseDetails() {
  detailsGeneration++;
  const previous = selectedRow;
  selectedRow = null;
  selectedIdentity = null;
  detailCurrent = null;
  $('detail-body').replaceChildren();
  txt($('detail-title'), 'Details');
  if (previous) attr(previous.toggle, 'aria-expanded', 'false');
  const target = previous?.toggle;
  if (!document.querySelector('dialog[open]'))
    (target?.isConnected && !previous.item.dying && !target.closest('[hidden]') ? target : $('search-toggle')).focus({ preventScroll: true });
}
export function closeDetails() {
  if (!$('detail-dialog').open && !selectedRow) return;
  $('detail-dialog').close();
  releaseDetails();
}
export function showDetails(row) {
  if (selectedRow === row && $('detail-dialog').open) { closeDetails(); return; }
  beforeDetails();
  if (selectedRow) attr(selectedRow.toggle, 'aria-expanded', 'false');
  selectedRow = row;
  detailsGeneration++;
  selectedIdentity = [row.item.pid, row.item.parent, row.item.live, row.item.sub];
  detailCurrent = null;
  attr(row.toggle, 'aria-expanded', 'true');
  refreshDetails();
  if (selectedRow && !$('detail-dialog').open) $('detail-dialog').showModal();
}
export function refreshDetails(retained = readDetailsState()) {
  if (!selectedRow) return;
  const { item, row, parent, host } = detailLookup(selectedRow.item.key);
  if (!item || row !== selectedRow || item.dying || ![item.pid, item.parent, item.live, item.sub].every((value, i) => value === selectedIdentity[i]) || !row.node.isConnected) { closeDetails(); return; }
  const current = [item, parent, host, row.fold, retained];
  if (detailCurrent && current.every((value, i) => value === detailCurrent[i])) return;
  detailCurrent = current;
  txt($('detail-title'), item.name);
  const body = $('detail-body'), hadFocus = body.contains(document.activeElement), focusID = document.activeElement.id;
  const list = el('dl', 'detail-list');
  const pairs = [
    ['Provider', item.agentName],
    ['Source', item.live ? 'Live OpenCode session metadata' : item.sub ? 'Read-only Claude worker metadata · log-write activity, not CPU' : 'Detected process · PID ' + item.pid],
    ['Identity', item.key],
    ['Immediate parent', parent?.name || 'No detected parent'],
    ['Main terminal', [host?.name, host?.target || item.target, item.cwd].filter(Boolean).join(' · ')],
    ['Activity', retained ? retained + ' · retained, not live state' : item.stText],
    ['Approx. RAM', item.mem == null ? 'Unavailable' : '~' + fmtBytes(item.mem) + ' · process-tree RSS'],
    ['CPU', item.cpu], ['Process age', item.up], ['Model', item.model || 'Unavailable']
  ];
  for (const [key, value] of pairs) list.append(elText('dt', key), elText('dd', value));
  const help = elText('p', 'Activity is not progress or a request for input. RSS is approximate, not additive physical memory or guaranteed reclaimable RAM. CPU 100% is one core, not task progress. Process age is not task duration.');
  help.className = 'detail-help';
  if (item.sub) help.append(document.createTextNode(' The tree shows the immediate parent; the name opens the main agent’s terminal when available, not a separate worker terminal. Sent is not completion; Reply is not success; Attempted means acceptance is unconfirmed.'));
  const terminal = elText('button', item.sub ? 'Open main terminal' : 'Open terminal');
  terminal.id = 'detail-terminal'; terminal.disabled = row.btn.disabled;
  terminal.onclick = async () => {
    const generation = detailsGeneration;
    const focused = await focusTerminal(row);
    if (generation !== detailsGeneration || selectedRow !== row || !$('detail-dialog').open) return;
    if (focused) closeDetails();
    else txt($('detail-error'), row.note?.textContent || 'Focus unavailable');
  };
  const error = el('p', 'note'); error.id = 'detail-error'; error.setAttribute('role', 'alert');
  if (row.note) txt(error, row.note.textContent);
  const actions = el('div', 'button-row'); actions.append(terminal);
  if (row.fold?.count) {
    const branch = elText('button', row.fold.label);
    branch.id = 'detail-branch';
    attr(branch, 'aria-expanded', !row.fold.closed); attr(branch, 'aria-controls', row.kids.id);
    branch.onclick = row.fold.toggle; actions.append(branch);
  }
  body.replaceChildren(list, help, actions, error);
  if (hadFocus) (document.getElementById(focusID) || $('close-details')).focus({ preventScroll: true });
}
const elText = (tag, value) => { const node = el(tag); txt(node, value || 'Unavailable'); return node; };

export function createAgentRow(item) {
  const node = el('div'),
    line = el('div', 'row-line'),
    btn = el('button', 'agent'),
    kids = el('div', 'kids');
  const r = {
    node,
    btn,
    kids,
    kind: null,
    size: 0,
    logo: null,
    identity: el('span', 'identity'),
    name: el('span', 'kind'),
    context: el('span', 'context'),
    agentKind: item.live ? null : el('span', 'agent-kind'),
    target: item.live ? null : el('span', 'target'),
    st: item.live ? null : el('span', 'st'),
    mem: item.sub ? null : el('span', 'mem'),
    toggle: el('button', 'metrics'),
    workers: el('span', 'workers'),
    note: null,
    noteUntil: 0
  };
  if (!item.live) r.context.append(r.agentKind, r.target);
  r.identity.append(r.name, r.context);
  r.name.after(r.workers);
  if (r.mem) r.mem.title =
    'Approximate agent + child-process RAM (RSS); shared pages may be counted more than once';
  if (r.target) r.target.title = 'tmux session/pane target; click an enabled task to select this pane';
  r.toggle.setAttribute('aria-controls', 'detail-dialog');
  r.toggle.setAttribute('aria-haspopup', 'dialog');
  r.toggle.setAttribute('aria-expanded', 'false');
  r.toggle.title = 'Details: CPU, process age and worker branch';
  r.toggle.onclick = () => showDetails(r);
  r.toggle.textContent = 'Details';
  line.append(btn);
  if (!item.live) {
    const metrics = el('span', 'row-metrics');
    if (r.st) metrics.append(r.st);
    if (r.mem) metrics.append(r.mem);
    line.append(metrics);
  }
  line.append(r.toggle);
  node.append(line, kids);
  btn.onclick = () => focusTerminal(r);
  return r;
}

export function updateAgentRow(r, it) {
  r.item = it;
  if (r.kind !== it.kind || r.size !== it.size) {
    r.kind = it.kind;
    r.size = it.size;
    if (!it.sub) {
      const l = logo(it.kind, it.size);
      r.logo ? r.logo.replaceWith(l) : r.btn.prepend(l);
      r.logo = l;
    }
    r.btn.append(r.identity);
  }
  r.pid = it.pid; // focus target: the agent itself, or a subagent's top-level agent (may be null)
  r.btn.disabled = !it.canFocus || !!it.dying;
  cls(r.node, 'node s-' + it.status + (it.sub ? ' worker' : '') + (it.live ? ' live-worker' : ''));
  txt(r.name, it.name);
  if (r.st) txt(r.st, it.stText);
  if (r.target) txt(r.target, it.target || '');
  if (r.agentKind) txt(r.agentKind, it.agentLabel || '');
  if (r.mem) txt(r.mem, it.mem != null ? '~' + fmtBytes(it.mem) : '—');
  attr(
    r.toggle,
    'aria-label',
    `Details for ${it.name}: ${it.stText}, RAM ${r.mem?.textContent || 'unavailable'}`
  );
  if (r.btn.title !== it.tip) r.btn.title = it.tip;
  if (r.note && Date.now() > r.noteUntil) {
    r.note.remove();
    r.note = null;
  }
}

export async function focusTerminal(r) {
  if (r.pid == null || r.btn.disabled || r.btn.classList.contains('busy')) return false;
  r.btn.classList.add('busy');
  r.btn.setAttribute('aria-busy', 'true');
  let err = null;
  try {
    const res = await fetch('/focus', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ pid: r.pid })
    });
    if (!res.ok) err = (await res.json().catch(() => ({}))).error || 'focus failed';
  } catch {
    err = 'server unreachable';
  }
  r.btn.classList.remove('busy');
  r.btn.setAttribute('aria-busy', 'false');
  if (err) {
    // removed by the next poll after 3s; no timer needed
    if (!r.note) {
      r.note = el('div', 'note');
      r.note.setAttribute('role', 'alert');
      (r.item ? r.btn.parentElement : r.btn).insertAdjacentElement('afterend', r.note);
    }
    txt(r.note, err);
    r.noteUntil = Date.now() + 3000;
  }
  return !err;
}
// Compact keyed terminal links for bounded quiet observations and search results.
function createAgentCard() {
  const node = el('div'),
    btn = el('button', 'agent');
  const r = {
    node,
    btn,
    kind: null,
    logo: null,
    identity: el('span', 'identity'),
    name: el('span', 'kind'),
    sub: el('span', 'sub'),
    meta: el('span', 'st'),
    note: null,
    noteUntil: 0
  };
  r.identity.append(r.name, r.sub);
  node.append(btn);
  btn.onclick = () => focusTerminal(r);
  return r;
}
export function syncAgentCards(box, store, list, meta) {
  list.forEach((it, i) => {
    let r = store.get(it.key);
    if (!r) store.set(it.key, (r = createAgentCard()));
    if (r.kind !== it.kind) {
      r.kind = it.kind;
      const l = logo(it.kind, 20);
      r.logo ? r.logo.replaceWith(l) : r.btn.prepend(l);
      r.logo = l;
      r.btn.append(r.identity, r.meta);
    }
    r.pid = it.pid;
    r.btn.disabled = !it.canFocus || !!it.dying;
    cls(r.node, 'node s-' + it.status);
    txt(r.name, it.name);
    txt(r.sub, [it.agentLabel, label(it), it.target].filter(Boolean).join(' · '));
    txt(r.meta, meta(it));
    if (r.btn.title !== it.tip) r.btn.title = it.tip;
    if (r.note && Date.now() > r.noteUntil) {
      r.note.remove();
      r.note = null;
    }
    if (box.children[i] !== r.node) box.insertBefore(r.node, box.children[i] ?? null);
  });
  const keep = new Set(list.map((it) => it.key));
  for (const [k, r] of store)
    if (!keep.has(k)) {
      r.node.remove();
      store.delete(k);
    }
  box.parentElement.hidden = !list.length;
}
