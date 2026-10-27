import { $, txt, cls } from './dom.js';
import { label } from './agents.js';
import { syncAgentCards, focusTerminal } from './rows.js';

let items = new Map();
let readSnapshotState;
let beforeOpen = () => {};
const searchIndex = new Map();

export const SEARCH_LIMIT = 20;
const searchRows = new Map();
let firstSearchMatch = null,
  searchActive = false;
function searchFields(it) {
  const fields = {
    task: [it.name, it.live ? it.key : ''].join(' '),
    agent: [it.kind, it.agentName].filter(Boolean).join(' '),
    kind: it.kind,
    repo: it.repoName + ' ' + it.repo,
    path: [it.wt, it.cwd].filter(Boolean).join(' '),
    worktree: it.wt,
    status: [it.status, it.stText].join(' '),
    pid: String(it.pid ?? ''),
    pane: it.target || ''
  };
  for (const k in fields) fields[k] = String(fields[k] ?? '').toLowerCase();
  fields.text = [fields.task, fields.agent, label(it), it.stText, fields.pane]
    .join(' ')
    .toLowerCase();
  return fields;
}
export function applySearch() {
  const input = $('search');
  const terms = (input.value.toLowerCase().match(/(?:[^\s"]|"[^"]*")+/g) || [])
    .map((token) => {
      const colon = token.indexOf(':'),
        field = token.slice(0, colon);
      return colon > 0 && ['task', 'agent', 'kind', 'repo', 'path', 'worktree', 'status', 'pid', 'pane'].includes(field)
        ? [field, token.slice(colon + 1).replaceAll('"', '')]
        : ['text', token.replaceAll('"', '')];
    })
    .filter(([, value]) => value);
  const active = terms.length > 0;
  const hide = (node, on) => {
    if (node.hidden !== on) node.hidden = on;
  };
  firstSearchMatch = null;
  hide($('clear-search'), !input.value);
  if (!active && !searchActive) return; // No index, scans, or DOM work while unused.
  searchActive = active;
  let matches = 0;
  const results = [];
  for (const [key, item] of items) {
    if (active) {
      let fields = searchIndex.get(key);
      if (!fields) searchIndex.set(key, (fields = searchFields(item)));
      if (
        !terms.every(([field, value]) =>
          field === 'pid' ? fields[field] === value : fields[field].includes(value)
        )
      )
        continue;
    } else searchIndex.delete(key);
    if (active) {
      matches++;
      if (results.length < SEARCH_LIMIT) results.push(item);
    }
  }
  const resultHadFocus = $('search-results').contains(document.activeElement);
  const retained = readSnapshotState();
  syncAgentCards($('search-results'), searchRows, results, (it) => retained || it.stText);
  for (const [key, r] of searchRows) {
    if (retained) cls(r.node, 'node s-unknown');
    if (!firstSearchMatch && !r.btn.disabled) firstSearchMatch = r;
    r.btn.onclick = async () => {
      if ((await focusTerminal(r)) && searchRows.get(key) === r) closeSearch();
    };
  }
  if (resultHadFocus && !$('search-results').contains(document.activeElement)) $('search').focus();
  hide($('search-message'), active && matches > 0);
  txt(
    $('search-message'),
    active
      ? items.size
        ? 'No matching agents. Try fewer terms.'
        : 'No agents running.'
      : 'Type to search current tasks, agents, and repositories.'
  );
  hide($('search-count'), !active);
  txt(
    $('search-count'),
    active
      ? `${matches} match${matches === 1 ? '' : 'es'}${matches > SEARCH_LIMIT ? ` · showing first ${SEARCH_LIMIT}; refine to narrow` : ''}`
      : ''
  );
}

let searchReturnFocus = null;
export function openSearch() {
  beforeOpen();
  if ($('search-panel').hidden) searchReturnFocus = document.activeElement;
  $('search-panel').hidden = false;
  if (!$('search-panel').open) $('search-panel').showModal();
  $('search-toggle').setAttribute('aria-expanded', 'true');
  $('search').focus({ preventScroll: true });
  $('search').select();
}
export function closeSearch() {
  $('search').value = '';
  applySearch();
  $('search-panel').close();
  $('search-panel').hidden = true;
  $('search-toggle').setAttribute('aria-expanded', 'false');
  const target = searchReturnFocus;
  searchReturnFocus = null;
  (target?.isConnected &&
  target !== document.body &&
  !target.disabled &&
  !target.closest('[hidden]')
    ? target
    : $('search-toggle')
  ).focus({ preventScroll: true });
}
export function setupSearch(snapshotState, onOpen = () => {}) {
  beforeOpen = onOpen;
  readSnapshotState = snapshotState;
  const isMac = navigator.platform.startsWith('Mac');
  const searchShortcut = isMac ? '⌘ K' : 'Ctrl K';
  $('search-key').textContent = searchShortcut;
  $('search-toggle').title = `Search agents (${searchShortcut})`;
  $('search').setAttribute('aria-keyshortcuts', isMac ? 'Meta+k' : 'Control+k');
  $('search-toggle').onclick = () => ($('search-panel').hidden ? openSearch() : closeSearch());
  $('close-search').onclick = closeSearch;
  $('search-panel').oncancel = (e) => {
    e.preventDefault();
    closeSearch();
  };
  $('search-panel').onclick = (e) => {
    if (e.target !== $('search-panel')) return;
    const rect = $('search-panel').getBoundingClientRect();
    if (
      e.clientX < rect.left ||
      e.clientX > rect.right ||
      e.clientY < rect.top ||
      e.clientY > rect.bottom
    )
      closeSearch();
  };
  $('search').oninput = applySearch;
  $('clear-search').onclick = () => {
    $('search').value = '';
    applySearch();
    $('search').focus();
  };
  $('search').onkeydown = (e) => {
    if (e.isComposing) return;
    if (e.key === 'ArrowDown' && firstSearchMatch) {
      e.preventDefault();
      firstSearchMatch.btn.focus();
    }
    if (e.key === 'Enter' && firstSearchMatch) {
      e.preventDefault();
      firstSearchMatch.btn.click();
    }
  };
  $('search-results').onkeydown = (e) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const buttons = [...searchRows.values()].filter((r) => !r.btn.disabled).map((r) => r.btn);
    const index = buttons.indexOf(document.activeElement);
    if (index < 0) return;
    e.preventDefault();
    if (e.key === 'ArrowUp' && index === 0) $('search').focus();
    else buttons[Math.min(buttons.length - 1, index + (e.key === 'ArrowDown' ? 1 : -1))].focus();
  };
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !e.isComposing && $('search-panel').open) {
      e.preventDefault();
      closeSearch();
      return;
    }
    if ((isMac ? e.metaKey : e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      openSearch();
    }
  });
}

export function updateSearch(nextItems) {
  items = nextItems;
  searchIndex.clear();
  applySearch();
}

export function refreshSearch(retained) {
  $('search-freshness').hidden = !retained;
  txt(
    $('search-freshness'),
    retained
      ? `${retained === 'offline' ? 'Offline' : 'Stale'} · searching retained measurements, not live state`
      : ''
  );
  for (const [key, row] of searchRows) {
    txt(row.meta, retained || items.get(key).stText);
    cls(row.node, 'node s-' + (retained ? 'unknown' : items.get(key).status));
  }
}
