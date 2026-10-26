const WORKING_RATE = 0.025; // CPU cores; keep equal to the server's WORKING_RATE
export const MB = 1024 ** 2,
  GB = 1024 ** 3;
export const HEAT = ['working', 'waiting', 'new', 'idle', 'unknown', 'exiting']; // activity first, for group trunk colour
export const stateLabel = (s) => (s === 'waiting' ? 'quiet' : s);
// ps etime [[dd-]hh:]mm:ss -> largest unit, e.g. 6d, 5h, 12m, 40s
const formatProcessAge = (e) => {
  const [d, rest] = e.includes('-') ? e.split('-') : [0, e];
  const p = rest.split(':').map(Number),
    [h, m, sec] = p.length === 3 ? p : [0, ...p];
  return +d ? +d + 'd' : h ? h + 'h' : m ? m + 'm' : sec + 's';
};

export const fmtBytes = (b) => (b >= GB ? (b / GB).toFixed(1) + ' GB' : Math.round(b / MB) + ' MB');
export const formatDuration = (s) =>
  s < 60
    ? Math.floor(s) + 's'
    : s < 3600
      ? Math.floor(s / 60) + 'm'
      : s < 86400
        ? Math.floor(s / 3600) + 'h'
        : Math.floor(s / 86400) + 'd';

// Keep the existing waiting search alias, but never infer a human request from CPU quietness.
const statusOf = (a) =>
  a.isNew
    ? 'new'
    : !Number.isFinite(a.recentCpu)
      ? 'unknown'
      : a.recentCpu >= WORKING_RATE
        ? 'working'
        : a.idleSec != null && a.idleSec < 1800
          ? 'waiting'
          : 'idle';
const terminalTarget = (a) =>
  a.tmuxTarget
    ? a.tmuxTarget + (a.tmuxAttached ? '' : a.canFocus ? ' · open Ghostty' : ' · attach client')
    : 'Monitoring only · outside tmux';

function agentItem(a) {
  const status = statusOf(a);
  const target = terminalTarget(a);
  const agentName =
    {
      opencode: 'OpenCode',
      claude: 'Claude Code',
      codex: 'Codex',
      gemini: 'Gemini',
      aider: 'Aider',
      'cursor-agent': 'Cursor'
    }[a.kind] || a.kind;
  return {
    key: 'a:' + a.pid,
    parent: 'a:' + a.ppid,
    pid: a.pid,
    cwd: a.cwd,
    agentName,
    repo: a.repo ?? a.cwd ?? 'unknown',
    repoName: a.repoName ?? a.project,
    wt: a.worktree ?? a.cwd ?? 'unknown',
    kind: a.kind,
    size: 20,
    name: a.title || agentName,
    agentLabel: a.title ? agentName : '',
    target,
    tip: [
      agentName,
      a.title,
      a.cwd,
      target,
      a.canFocus
        ? 'Open terminal at tmux pane'
        : a.tmuxTarget
          ? 'Attach a tmux client to select this pane'
          : 'Monitoring only · start in tmux to select a pane'
    ]
      .filter(Boolean)
      .join(' · '),
    canFocus: a.canFocus === true,
    idleSec: a.idleSec,
    mem: Number.isFinite(a.memBytes) && a.memBytes > 0 ? a.memBytes : null,
    status,
    stText: stateLabel(status),
    up: a.etime ? 'Process age: ' + formatProcessAge(a.etime) : 'Process age: unavailable',
    cpu: Number.isFinite(a.recentCpu)
      ? 'CPU ' + Math.round(a.recentCpu * 100) + '%'
      : 'CPU: unavailable',
    ord: a.pid
  };
}

// Claude subagent: nests under the subagent that spawned it, else its claude session, else the worktree.
// Recent writes are activity, not communication; click focuses the top-level Claude (if known).
function subItem(s, i, host) {
  return {
    key: 'sub:' + s.id,
    parent: s.parentAgentId
      ? 'sub:' + s.parentAgentId
      : s.parentPid == null
        ? null
        : 'a:' + s.parentPid,
    pid: s.parentPid,
    cwd: s.cwd,
    agentName: 'Claude Code',
    canFocus: s.parentPid != null && host?.pid === s.parentPid && host.canFocus === true,
    repo: host?.repo ?? s.cwd,
    repoName: host?.repoName ?? (s.cwd.split('/').pop() || s.cwd),
    wt: host?.worktree ?? s.cwd,
    kind: 'claude',
    size: 16,
    name: s.description ?? 'subagent',
    agentLabel: 'Claude Code',
    target: host ? terminalTarget(host) : 'Worker · parent terminal unavailable',
    tip: s.description ?? 'subagent',
    mem: null,
    status: s.working === true ? 'working' : s.working === false ? 'idle' : 'unknown',
    stText: s.working === true ? 'working' : s.working === false ? 'quiet' : 'unknown',
    up: 'Process age: unavailable',
    cpu: 'CPU: unavailable',
    model: 'Model: ' + (s.model || 'unavailable'),
    ord: 1e9 + i,
    sub: true
  };
}

function liveItem(worker, index, host) {
  return {
    ...host,
    key: worker.key,
    parent: worker.parent,
    size: 16,
    kind: 'opencode',
    sub: true,
    live: true,
    name: 'Worker ' + worker.key.split(':').at(-1).slice(-6),
    agentLabel: 'Observed live',
    mem: null,
    status: 'unknown',
    stText: 'unknown',
    idleSec: null,
    up: 'Process age: unavailable',
    cpu: 'CPU: unavailable',
    model: 'Model: unavailable',
    ord: 2e9 + index,
    tip: 'Live session relationship, not process activity or task completion. Opens main agent terminal when available; immediate parent may be another worker. Full worker ID: ' + worker.key
  };
}
export const label = (it) =>
  it.wt === it.repo ? it.repoName : it.repoName + ' / ' + it.wt.split('/').pop();

export function snapshotItems(data, last, again = false) {
  const { agents, subagents = [] } = data;
  if (again) last = new Map([...last].filter(([, it]) => !it.dying));
  const cur = new Map(),
    byCwd = new Map(agents.map((a) => [a.cwd, a])),
    byPid = new Map(agents.map((a) => [a.pid, a]));
  for (const a of agents) {
    const it = agentItem(a);
    cur.set(it.key, it);
  }
  subagents.forEach((s, i) => {
    const it = subItem(s, i, byPid.get(s.parentPid) ?? byCwd.get(s.cwd));
    cur.set(it.key, it);
  });
  (data.liveWorkers || []).forEach((worker, index) => {
    const host = cur.get('a:' + worker.pid);
    if (host?.kind === 'opencode') {
      const item = liveItem(worker, index, host);
      cur.set(item.key, item);
    }
  });
  for (const [k, it] of last) // gone since last poll: one poll in red, then removed
    if (!cur.has(k) && !it.dying && !it.live)
      cur.set(k, { ...it, dying: true, status: 'exiting', stText: 'exiting' });
  // Resolve orphaned workers once so counts, indentation and placement share the same parent.
  for (const it of cur.values()) {
    if (!it.parent || it.parent === it.key || !cur.has(it.parent))
      it.parent = it.sub && it.pid != null && cur.has('a:' + it.pid) ? 'a:' + it.pid : null;
  }
  const checked = new Set();
  for (const it of cur.values()) {
    const path = new Set();
    let key = it.key;
    while (key && !checked.has(key)) {
      if (path.has(key)) {
        cur.get(key).parent = null;
        break;
      }
      path.add(key);
      key = cur.get(key).parent;
    }
    for (const k of path) checked.add(k);
  }
  return cur;
}
