import { el, txt, cls, attr, uniqueID } from './dom.js';
import { fmtBytes, label } from './agents.js';
import { logo } from './icons.js';

export function createAgentRow(onDetailsChange = () => {}) {
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
    agentKind: el('span', 'agent-kind'),
    target: el('span', 'target'),
    st: el('span', 'st'),
    mem: el('span', 'mem'),
    up: el('span', 'up'),
    cpu: el('span', 'cpu'),
    model: el('span'),
    terminal: el('p'),
    detail: el('div', 'agent-details'),
    toggle: el('button', 'metrics'),
    branch: el('button'),
    workers: el('span'),
    note: null,
    noteUntil: 0
  };
  r.context.append(r.agentKind, r.target);
  r.identity.append(r.name, r.context);
  r.context.append(r.workers);
  r.mem.title =
    'Approximate agent + child-process RAM (RSS); shared pages may be counted more than once';
  r.cpu.title = 'Recent agent + child-process CPU; 100% = one core, not task progress';
  r.target.title = 'tmux session/pane target; click an enabled task to select this pane';
  r.detail.id = uniqueID('d');
  r.detail.hidden = true;
  r.toggle.setAttribute('aria-controls', r.detail.id);
  r.toggle.setAttribute('aria-expanded', 'false');
  r.toggle.title = 'Details: CPU, process age and worker branch';
  r.toggle.onclick = () => {
    onDetailsChange();
    r.detail.hidden = !r.detail.hidden;
    r.toggle.setAttribute('aria-expanded', !r.detail.hidden);
  };
  r.toggle.append(r.st, r.mem);
  for (const metric of [r.cpu, r.up, r.model]) {
    const p = el('p');
    p.append(metric);
    r.detail.append(p);
  }
  const help = el('p');
  help.textContent =
    'Activity is not progress or a request for input. Task opens terminal when available.';
  r.detail.append(r.terminal, help, r.branch);
  r.branch.hidden = true;
  line.append(btn, r.toggle);
  node.append(line, r.detail, kids);
  btn.onclick = () => focusTerminal(r);
  return r;
}

export function updateAgentRow(r, it) {
  if (r.kind !== it.kind || r.size !== it.size) {
    r.kind = it.kind;
    r.size = it.size;
    const l = logo(it.kind, it.size);
    r.logo ? r.logo.replaceWith(l) : r.btn.prepend(l);
    r.logo = l;
    r.btn.append(r.identity);
  }
  r.pid = it.pid; // focus target: the agent itself, or a subagent's top-level agent (may be null)
  r.btn.disabled = !it.canFocus || !!it.dying;
  cls(r.node, 'node s-' + it.status);
  txt(r.name, it.name);
  txt(r.st, it.stText);
  txt(r.up, it.up);
  txt(r.cpu, it.cpu);
  txt(r.model, it.model || '');
  txt(r.target, it.target || '');
  txt(r.agentKind, it.agentLabel || '');
  txt(r.terminal, [it.agentName, it.target, it.cwd].filter(Boolean).join(' · '));
  r.up.title = 'Agent process age, not current task duration';
  txt(r.mem, it.mem != null ? '~' + fmtBytes(it.mem) : '—');
  attr(
    r.toggle,
    'aria-label',
    `Details for ${it.name}: ${it.stText}, RAM ${r.mem.textContent}`
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
      (r.detail || r.btn).insertAdjacentElement(r.detail ? 'beforebegin' : 'afterend', r.note);
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
