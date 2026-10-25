import { testState as app } from './app.js';
import { testState as dashboard } from './dashboard.js';
import { testState as searchState, SEARCH_LIMIT, openSearch, closeSearch, applySearch } from './search.js';
import { testState as notificationsState } from './notifications.js';
import { LOGOS, logo } from './icons.js';
import { MB, GB, snapshotItems, testState as agentsState } from './agents.js';
import { testState as rowsState } from './rows.js';
import { testState as communication, communicationStream, clearPulses } from './communication.js';
import { $ } from './dom.js';

const { rows, groups, quietRows } = dashboard;
const { searchRows, searchIndex } = searchState;
const { eps } = notificationsState;
const { render, poll, freshness } = app;
const { activePulses, communicationPulse, communicationFeedback, communicationStatus } = communication;

// Test-only exports are appended by TestUI, never embedded or served in production.
(async () => { try {
  const assert = (ok, message) => { if (!ok) throw new Error(message); };
  const agent = (pid, repo, extra = {}) => ({
    pid, ppid: 1, kind: 'codex', cwd: repo, repo, repoName: repo.slice(1), worktree: repo,
    recentCpu: 0, idleSec: null, isNew: false, etime: '01:00', ...extra,
  });
  const show = agents => render({ agents, subagents: [] });
  const a = agent(100, '/a');
  assert(!Object.hasOwn(window, 'render') && !Object.hasOwn(window, 'rows'), 'module internals do not become browser globals');
  const cycle = snapshotItems({ agents: [agent(1, '/cycle', { ppid: 2 }), agent(2, '/cycle', { ppid: 1 })] }, new Map());
  for (const item of cycle.values()) {
    const visited = new Set();
    for (let current = item; current; current = cycle.get(current.parent)) {
      assert(!visited.has(current.key), 'snapshot normalization breaks parent cycles');
      visited.add(current.key);
    }
  }
  const scenario = new URLSearchParams(location.search);
  assert(window.innerWidth === (scenario.get('size') === 'native' ? 680 : 375) && window.innerHeight === (scenario.get('size') === 'native' ? 820 : 812), 'checks run at the actual compact/native content viewport, not Chrome minimum window dimensions');
  assert(matchMedia('(prefers-color-scheme: dark)').matches === (scenario.get('theme') === 'dark'), 'browser runs the requested color scheme');
  assert(matchMedia('(prefers-reduced-motion: reduce)').matches === (scenario.get('reduced') === 'true'), 'browser runs the requested motion preference');
  const brand = document.querySelector('.brand'), brandLogo = brand.querySelector('img');
  assert(brandLogo.getBoundingClientRect().width === 24 && parseFloat(getComputedStyle(brand.querySelector('h1')).fontSize) === 16 && !brand.querySelector('p'), 'compact brand keeps the 24px logo, 16px name, and no tagline');
  assert($('search-toggle').getBoundingClientRect().height >= 44 && $('search-toggle').getBoundingClientRect().width >= 44, 'Search retains a 44px target');

  for (const kind of ['aider', 'cursor-agent']) {
    show([{ ...a, kind, recentCpu: 0.1, idleSec: 0 }]);
    show([{ ...a, kind, idleSec: 15 }]);
    const path = rows.get('a:100').logo.querySelector('svg path');
    assert(path && path.getAttribute('d') === LOGOS[kind], kind + ' uses its embedded SVG logo in agent rows');
    assert(quietRows.get('a:100').logo.querySelector('svg path').getAttribute('d') === LOGOS[kind], kind + ' uses its logo in recently quiet cards after observed work');
    openSearch();
    $('search').value = 'pid:100'; applySearch();
    assert(searchRows.get('a:100').logo.querySelector('svg path').getAttribute('d') === LOGOS[kind], kind + ' uses its logo in search results');
    closeSearch();
  }
  assert(logo('unknown', 20).textContent === 'U', 'unknown agent kinds retain the monogram fallback');

  show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
  const metrics = rows.get('a:100');
  const taskStyle = getComputedStyle(metrics.btn), metricStyle = getComputedStyle(metrics.toggle);
  assert(parseFloat(taskStyle.paddingLeft) >= 8 && parseFloat(taskStyle.paddingRight) >= 8 && parseFloat(taskStyle.gap) >= 8 && metrics.identity.getBoundingClientRect().left - metrics.logo.getBoundingClientRect().right >= 8, 'task controls keep 8px horizontal padding and icon/text spacing');
  assert(parseFloat(metricStyle.paddingLeft) >= 8 && parseFloat(metricStyle.paddingRight) >= 8 && metrics.toggle.getBoundingClientRect().right - metrics.mem.getBoundingClientRect().right >= 8, 'metric contents stay inset 8px inside the hover/focus control');
  assert(metrics.st.getBoundingClientRect().width === 64 && metrics.mem.getBoundingClientRect().width === 64 && metrics.btn.getBoundingClientRect().height === 47, 'horizontal breathing room preserves 64px metric columns and 47px short rows');
  const columnLabels = document.querySelector('.column-head').children;
  assert(Math.abs(columnLabels[1].getBoundingClientRect().left - metrics.st.getBoundingClientRect().left) <= 1 && Math.abs(columnLabels[2].getBoundingClientRect().right - metrics.mem.getBoundingClientRect().right) <= 1, 'State and RAM headers align with inset metric content');
  assert(metrics.cpu.textContent === 'CPU 136%' && metrics.mem.textContent === '~1.6 GB' && metrics.toggle.getAttribute('aria-label').includes('RAM'), 'metrics identify CPU (including multi-core use) and approximate RAM');
  assert(metrics.up.textContent === 'Process age: 1m' && metrics.up.title.includes('process age'), 'uptime is labeled as process age');
  assert(metrics.detail.hidden && !metrics.btn.contains(metrics.toggle) && !metrics.btn.contains(metrics.cpu) && !document.querySelector('button button'), 'CPU and age start in separate details, never nested terminal buttons');
  const detailFetch = window.fetch;
  let detailRequests = 0;
  try {
    window.fetch = async () => { detailRequests++; throw new Error('Details must not request terminal focus'); };
    metrics.toggle.focus(); metrics.toggle.click();
    assert(!metrics.detail.hidden && metrics.toggle.getAttribute('aria-expanded') === 'true' && document.getElementById(metrics.toggle.getAttribute('aria-controls')) === metrics.detail, 'monitoring-only details open accessibly');
    assert(metrics.cpu.getBoundingClientRect().height > 0 && metrics.up.getBoundingClientRect().height > 0, 'opened details expose CPU and process age');
    show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
    assert(!metrics.detail.hidden && document.activeElement === metrics.toggle, 'snapshot updates preserve details and keyboard focus');
    metrics.toggle.click();
    assert(metrics.detail.hidden && metrics.toggle.getAttribute('aria-expanded') === 'false', 'details close independently of terminal navigation');
    assert(detailRequests === 0, 'details never requests terminal focus');
  } finally { window.fetch = detailFetch; }
  assert(groups.get('r:/a').counts.textContent.includes('1 working'), 'repository counts include status text, not just color');
  assert(!$('reading').open && $('reading').textContent.includes('not percent complete'), 'metric help is available without leaving the dashboard');
  $('reading').open = true;
  assert($('reading').getBoundingClientRect().height > 200, 'expanded help is visible in the dashboard');
  $('reading').open = false;
  assert(document.documentElement.scrollWidth <= window.innerWidth, 'labeled metrics and repository counts fit the narrow viewport');

  let notifications = 0, notificationBody = '';
  window.Notification = class {
    static permission = 'granted';
    constructor(title, options) { notifications++; notificationBody = options.body; }
  };
  await $('notify').onclick();
  show([{ ...a, recentCpu: 0.1, idleSec: 0 }]);
  show([{ ...a, idleSec: 14 }]);
  assert($('quiet').hidden && notifications === 0, 'quiet observation grace period');
  show([{ ...a, idleSec: 15 }]);
  assert(!$('quiet').hidden && $('quiet-list').children.length === 1, 'recently quiet observation section');
  assert(document.title === '(1) recently quiet · Swarmview' && notifications === 1, 'quiet observation badge and notification');
  assert(notificationBody.includes('Recently quiet') && notificationBody.includes('not a request'), 'notification describes evidence without inventing a human request');
  show([{ ...a, idleSec: 20 }]);
  assert(notifications === 1, 'notify once per waiting episode');
  show([{ ...a, recentCpu: 0.1, idleSec: 0 }]);
  assert($('quiet').hidden && document.title === 'Swarmview', 'working clears quiet badge');
  show([{ ...a, idleSec: 15 }]);
  assert(notifications === 2, 'new working episode rearms notification');
  show([]);
  show([{ ...a, idleSec: 15 }]);
  assert(notifications === 2, 'unseen work does not notify');
  assert($('quiet').hidden, 'unseen quiet processes are not presented as newly observed transitions');

  const b = agent(200, '/b');
  show([a, b]);
  assert($('tree').firstElementChild === groups.get('r:/a').node, 'default repo order');
  groups.get('r:/b').pin.click();
  assert($('tree').firstElementChild === groups.get('r:/b').node, 'pin moves repo to top');
  assert(JSON.parse(localStorage.getItem('swarmview.pins'))[0] === '/b', 'pin persisted');
  groups.get('r:/b').fold.click();
  assert(groups.get('r:/b').kids.hidden && groups.get('r:/b').fold.getAttribute('aria-expanded') === 'false', 'fold hides children accessibly');
  assert(JSON.parse(localStorage.getItem('swarmview.folded')).includes('r:/b'), 'fold persisted');
  $('collapse').click();
  assert([...groups.values()].every(g => g.kids.hidden), 'collapse all');
  $('expand').click();
  assert([...groups.values()].every(g => !g.kids.hidden), 'expand all');

  show([a]); show([a]); // Remove the exiting row before checking group placement.
  const row = rows.get('a:100').node;
  const branch = agent(300, '/a', { worktree: '/a/feature', cwd: '/a/feature' });
  show([a, branch]);
  assert(row.parentElement === groups.get('w:/a').kids, 'main checkout gains worktree group');
  assert(rows.get('a:300').node.parentElement === groups.get('w:/a/feature').kids, 'branch in its worktree');
  show([a]); show([a]);
  assert(row.parentElement === groups.get('r:/a').kids && !groups.has('w:/a'), 'lone checkout drops worktree level');
  show([agent(100, '/b')]);
  assert(rows.get('a:100').node === row && row.parentElement === groups.get('r:/b').kids, 'keyed row moves between repos');
  assert(!groups.has('r:/a'), 'old repo removed');

  assert(rows.get('a:100').btn.disabled, 'standalone agents are monitoring-only');
  assert(rows.get('a:100').target.textContent === 'Monitoring only · outside tmux', 'non-navigable rows explain why inline');
  const focusable = agent(100, '/b', { canFocus: true, tmuxAttached: true, tmuxTarget: 'dev:%42' });
  show([focusable]);
  assert(!rows.get('a:100').btn.disabled, 'tmux agents allow pane selection');
  assert(rows.get('a:100').target.textContent === 'dev:%42', 'session and pane visible');
  assert(rows.get('a:100').target.title.includes('session/pane'), 'target help identifies the session, not the window');
  const terminalRow = rows.get('a:100'), focusFetch = window.fetch;
  try {
    window.fetch = async () => { throw new Error('offline'); };
    await terminalRow.btn.onclick();
    assert(terminalRow.note.textContent === 'server unreachable' && terminalRow.note.isConnected && terminalRow.btn.getAttribute('aria-busy') === 'false', 'terminal focus failures stay visible and release the busy state');
  } finally { window.fetch = focusFetch; }
  show([{ ...focusable, canFocus: false, tmuxAttached: false }]);
  assert(rows.get('a:100').btn.disabled && rows.get('a:100').target.textContent.includes('attach client'), 'detached pane shows recovery and disables selection');
  assert(rows.get('a:100').btn.title.includes('Attach a tmux client'), 'detached pane explains recovery');
  show([{ ...focusable, kind: 'opencode', title: 'Next steps for swarmview' }]);
  assert(rows.get('a:100').name.textContent === 'Next steps for swarmview', 'conversation title is the primary label');
  assert(rows.get('a:100').logo.querySelector('svg path'), 'OpenCode uses an inline SVG rather than a monogram or remote image');
  assert(rows.get('a:100').agentKind.textContent === 'OpenCode' && rows.get('a:100').target.textContent === 'dev:%42', 'agent identity and tmux target stay visible below title');
  show([{ ...focusable, kind: 'opencode', title: null }]);
  assert(rows.get('a:100').name.textContent === 'OpenCode', 'agent name remains useful when no session title is available');
  show([{ ...focusable, tmuxAttached: false }]);
  assert(!rows.get('a:100').btn.disabled && rows.get('a:100').target.textContent.includes('open Ghostty'), 'detached pane can open Ghostty when supported');
  render({ agents: [focusable, agent(200, '/b')], subagents: [{ id: 'child', parentPid: 100, cwd: '/b', working: true }] });
  assert(!rows.get('sub:child').btn.disabled && rows.get('sub:child').pid === 100, 'subagent uses its actual parent focus target');
  show([{ ...focusable, recentCpu: 0.1, idleSec: 0 }]);
  show([{ ...focusable, idleSec: 15 }]);
  assert(!quietRows.get('a:100').btn.disabled, 'quiet observation card allows tmux selection');
  show([agent(100, '/b', { idleSec: 15 })]);
  assert(quietRows.get('a:100').btn.disabled, 'monitoring-only card is not clickable');

  render({ agents: [a], at: 100000 });
  freshness(140000);
  assert($('freshness').textContent.includes('Last updated 40s ago') && $('freshness').textContent.includes('retained') && rows.get('a:100').st.textContent === 'stale', 'freshness uses snapshot time and marks old state stale');
  freshness(220000);
  assert($('freshness').textContent.includes('Last updated 2m ago'), 'retained snapshot keeps aging');
  render({ agents: [a], at: 220000 });
  freshness(220000);
  assert($('freshness').textContent === 'Last updated 0s ago', 'recovery refreshes snapshot age');
  const realFetch = window.fetch, realNow = Date.now;
  try {
    Date.now = () => 260000;
    window.fetch = async (url, options) => {
      assert(url === '/agents' && options.signal instanceof AbortSignal, 'poll has a timeout signal');
      throw new Error('offline');
    };
    await poll(); clearTimeout(app.timer);
    assert(!$('offline').hidden && $('freshness').textContent.includes('Last updated 40s ago') && rows.get('a:100').st.textContent === 'offline', 'failed poll retains and ages last snapshot without labeling it quiet');
    assert(rows.has('a:100'), 'failed poll retains agent rows');
    window.fetch = async () => ({ ok: true, json: async () => ({ agents: [a], at: 260000 }) });
    await poll(); clearTimeout(app.timer);
    assert($('offline').hidden && $('freshness').textContent === 'Last updated 0s ago', 'successful retry clears offline and refreshes age');
  } finally { window.fetch = realFetch; Date.now = realNow; clearTimeout(app.timer); }

  show([agent(100, '/b'), agent(400, '/c')]);
  const ids = [...groups.values()].map(g => g.kids.id);
  assert(new Set(ids).size === ids.length, 'group IDs stay unique after removals');
  for (const g of groups.values()) {
    assert(document.getElementById(g.fold.getAttribute('aria-controls')) === g.kids, 'fold controls its own children');
  }

  assert($('search-panel').hidden && $('search-toggle').getAttribute('aria-expanded') === 'false', 'search stays out of the default dashboard');
  const macPlatform = navigator.platform.startsWith('Mac');
  assert($('search-key').textContent === (macPlatform ? '⌘ K' : 'Ctrl K') && $('search-toggle').title === `Search agents (${macPlatform ? '⌘ K' : 'Ctrl K'})` && $('search').getAttribute('aria-keyshortcuts') === (macPlatform ? 'Meta+k' : 'Control+k'), 'shortcut hints, tooltips, and accessibility metadata match the platform');
  const search = value => { if ($('search-panel').hidden) openSearch(); $('search').value = value; $('search').dispatchEvent(new Event('input')); };
  const coordinator = agent(501, '/api', { kind: 'claude', title: 'Coordinator', canFocus: true });
  const cacheTask = agent(502, '/api', { ppid: 501, kind: 'opencode', title: 'Fix cache invalidation', cwd: '/api/feature/src', worktree: '/api/feature', canFocus: true, tmuxAttached: true, tmuxTarget: 'dev:%42', recentCpu: 0.2, idleSec: 0 });
  const documentation = agent(503, '/docs', { title: 'Review glossary' });
  const sibling = agent(504, '/api', { title: 'Review authentication', idleSec: 30 });
  show([coordinator, cacheTask, documentation, sibling]); show([coordinator, cacheTask, documentation, sibling]);
  assert(searchIndex.size === 0, 'unused search creates no per-agent index');
  const cachedNode = rows.get('a:502').node;
  groups.get('r:/api').fold.click();
  const savedFolds = localStorage.getItem('swarmview.folded');
  const treeTop = $('tree').getBoundingClientRect().top, dashboardHeight = document.documentElement.scrollHeight;
  search('agent:OPENCODE "cache invalidation"');
  assert($('search-panel').open && $('search-count').textContent === '1 match' && searchRows.has('a:502'), 'native dialog matches fields and quoted phrases');
  assert(!rows.get('a:504').node.hidden && !groups.get('r:/docs').node.hidden && groups.get('r:/api').kids.hidden, 'search leaves the background dashboard and folds unchanged');
  assert($('tree').getBoundingClientRect().top === treeTop && document.documentElement.scrollHeight === dashboardHeight, 'opening and filtering the palette causes no background layout shift');
  assert(localStorage.getItem('swarmview.folded') === savedFolds, 'search never overwrites saved folds');
  $('search').focus();
  $('search').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', cancelable: true }));
  assert(document.activeElement === searchRows.get('a:502').btn, 'down arrow reaches the matching result inside the modal');
  const selected = searchRows.get('a:502').btn, selectedBox = selected.getBoundingClientRect(), resultsBox = $('search-results-wrap').getBoundingClientRect();
  const selectionStyle = getComputedStyle(selected);
  const ringOutside = Math.max(0, parseFloat(selectionStyle.outlineWidth) + parseFloat(selectionStyle.outlineOffset));
  assert(selectedBox.left - ringOutside >= resultsBox.left && selectedBox.right + ringOutside <= resultsBox.right && selectedBox.top - ringOutside >= resultsBox.top && selectedBox.bottom + ringOutside <= resultsBox.bottom, 'the entire selected-result focus ring fits inside the scroll area: ' + JSON.stringify({ selected: selectedBox, area: resultsBox, ringOutside }));
  $('search-results').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true }));
  assert(document.activeElement === $('search'), 'up arrow from the first result returns to the query');
  search('path:/feature/src status:working');
  assert($('search-count').textContent === '1 match', 'working directory and live status are searchable');
  search('pid:504 status:quiet');
  assert(searchRows.has('a:504') && searchRows.size === 1, 'visible quiet status is searchable');
  search('pid:504 status:waiting');
  assert(searchRows.has('a:504') && searchRows.size === 1, 'legacy waiting status remains a compatible search alias');
  search('dev:%42');
  assert($('search-count').textContent === '1 match', 'a colon in a tmux target remains literal text');
  search('pid:50');
  assert(!$('search-message').hidden && $('search-message').textContent.includes('No matching agents') && $('empty').hidden, 'PID filters are exact and no matches differs from no agents');
  search('pid:502');
  assert($('search-count').textContent === '1 match' && rows.get('a:502').node === cachedNode, 'typing reuses existing row nodes');
  show([coordinator, { ...cacheTask, title: 'Trace request headers' }, documentation, sibling]);
  search('cache invalidation');
  assert(!$('search-message').hidden && searchRows.size === 0, 'updated snapshots replace old searchable metadata');
  $('search').focus();
  $('search-panel').dispatchEvent(new Event('cancel', { cancelable: true }));
  assert($('search').value === '' && !$('search-panel').open && $('search-panel').hidden && document.activeElement === $('search-toggle') && groups.get('r:/api').kids.hidden, 'native Escape cancellation clears and closes search and returns keyboard focus');
  $('search-toggle').click(); search('agent:codex');
  $('search').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
  assert($('search-panel').hidden && $('search').value === '' && document.activeElement === $('search-toggle'), 'Escape key closes search even when the engine does not emit dialog cancel');
  $('expand').focus();
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: macPlatform, metaKey: !macPlatform, bubbles: true, cancelable: true }));
  assert($('search-panel').hidden, 'the other platform modifier does not hijack native shortcuts');
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: !macPlatform, metaKey: macPlatform, bubbles: true, cancelable: true }));
  assert(document.activeElement === $('search') && !$('search-panel').hidden, 'Ctrl/Cmd K opens and focuses search');
  $('search-panel').dispatchEvent(new MouseEvent('click', { clientX: 0, clientY: 0, bubbles: true }));
  assert($('search-panel').hidden && document.activeElement === $('expand'), 'closing search restores its previous keyboard focus');
  $('search-toggle').click();
  assert(!$('search-panel').hidden && document.activeElement === $('search'), 'mouse and touch users can open search');

  search('no-such-task');
  const beforeHiddenNotification = notifications;
  show([{ ...coordinator, recentCpu: 0.1, idleSec: 0 }]);
  show([{ ...coordinator, idleSec: 20 }]);
  assert(!$('quiet').hidden && document.title === '(1) recently quiet · Swarmview' && notifications === beforeHiddenNotification + 1, 'the palette does not filter or suppress global quiet observations, badges, or notifications');
  $('clear-search').click();
  assert(!$('quiet').hidden && $('search').value === '' && !$('search-panel').hidden, 'clear preserves quiet cards and keeps search open');
  search('task:Coordinator');
  $('close-search').click();
  assert($('search-panel').hidden && $('search').value === '' && $('search-toggle').getAttribute('aria-expanded') === 'false', 'Done closes and clears search without needing Escape');
  assert(groups.get('r:/api').kids.hidden && searchIndex.size === 0, 'Done restores folds and releases the search index');

  const shortQueryTasks = [
    agent(601, '/Users/dev/alpha', { repoName: 'alpha', title: 'Build login' }),
    agent(602, '/Users/dev/beta', { repoName: 'beta', title: 'Write tests', kind: 'opencode' }),
  ];
  show(shortQueryTasks); show(shortQueryTasks);
  search('se');
  assert(searchRows.size === 0 && $('search-count').textContent === '0 matches', 'plain short queries do not accidentally match hidden /Users paths');
  search('path:users');
  assert(searchRows.size === 2, 'explicit path filters still search full working directories');
  search('alpha');
  assert(searchRows.has('a:601') && searchRows.size === 1, 'plain queries include visible repository labels');

  show([cacheTask]); show([cacheTask]);
  search('pid:502');
  const result = searchRows.get('a:502'), originalFetch = window.fetch;
  let openedPID = null;
  try {
    window.fetch = async () => ({ ok: false, json: async () => ({ error: 'Focus unavailable' }) });
    await result.btn.onclick();
    assert($('search-panel').open && result.note.textContent === 'Focus unavailable', 'failed activation keeps its error visible inside the palette');
    window.fetch = async (url, options) => { openedPID = JSON.parse(options.body).pid; return { ok: true }; };
    await result.btn.onclick();
    assert(openedPID === 502 && !$('search-panel').open && searchRows.size === 0, 'successful result activation uses the correct PID and closes/releases the palette');
  } finally { window.fetch = originalFetch; }

  const many = Array.from({ length: 1000 }, (_, i) => agent(10000 + i, '/project-' + (i % 50), { ppid: 0, title: 'Task ' + i, canFocus: true }));
  show(many); show(many);
  search('agent:codex');
  assert(searchRows.size === SEARCH_LIMIT && $('search-count').textContent.startsWith('1000 matches'), 'large result sets render at most 20 rows, not the whole fleet');
  const largeSetNode = rows.get('a:10867').node;
  let searchRequests = 0;
  const previousFetch = window.fetch;
  try {
    window.fetch = async () => { searchRequests++; throw new Error('search must stay local'); };
    const start = performance.now();
    for (let i = 0; i < 40; i++) search(i % 2 ? 'repo:project-17 "task 867"' : 'repo:project-18 "task 918"');
    document.body.dataset.searchMs = ((performance.now() - start) / 40).toFixed(2);
    assert(rows.size === 1000 && rows.get('a:10867').node === largeSetNode && !largeSetNode.hidden, 'large-set searches preserve the existing DOM');
    assert($('search-count').textContent === '1 match' && searchRows.has('a:10867') && searchRows.size === 1 && [...rows.values()].every(r => !r.node.hidden), '1,000-agent search narrows palette results without altering dashboard rows');
    assert(searchRequests === 0, 'typing never requests the server');
  } finally { window.fetch = previousFetch; }
  $('clear-search').click();
  assert(searchIndex.size === 0, 'clearing search releases the metadata index');
  show(many.slice(0, 1)); show(many.slice(0, 1));
  assert(rows.size === 1 && groups.size === 1, 'exited agents and their search metadata are discarded');
  $('close-search').click();
  assert($('search-panel').hidden && !$('search-panel').open && $('search').value === '' && searchRows.size === 0 && searchState.searchReturnFocus === null, 'closing removes temporary results, releases old focus references, and leaves no hidden query');

  const fallbackHost = agent(100, '/fallback', { kind: 'claude', canFocus: true, tmuxAttached: true, tmuxTarget: 'synthetic:%1' });
  const survivingWorker = { id: 'survivor', parentPid: 100, parentAgentId: 'finished-parent', cwd: '/fallback', description: 'Surviving worker', working: true };
  const finishedParent = { id: 'finished-parent', parentPid: 100, cwd: '/fallback', description: 'Temporary parent', working: true };
  const fallbackSnapshot = { agents: [fallbackHost], subagents: [survivingWorker] };
  render(fallbackSnapshot); render(fallbackSnapshot); $('expand').click();
  const fallbackRoot = rows.get('a:100'), survivor = rows.get('sub:survivor');
  assert(survivor.node.parentElement === fallbackRoot.kids && survivor.name.getBoundingClientRect().left > fallbackRoot.name.getBoundingClientRect().left, 'worker with an absent declared parent is placed and indented under its effective top-level parent');
  assert(!fallbackRoot.branch.hidden && /\b1 worker\b/.test(fallbackRoot.workers.textContent), 'fallback worker contributes to its effective parent branch count and control');
  assert(Math.abs(survivor.st.getBoundingClientRect().left - fallbackRoot.st.getBoundingClientRect().left) <= 1, 'fallback indentation does not move the state column');
  const fallbackFetch = window.fetch;
  let fallbackFocusRequests = 0, fallbackFocusURL = null, fallbackFocusMethod = null, fallbackFocusBody = null;
  try {
    window.fetch = async (url, options) => {
      fallbackFocusRequests++; fallbackFocusURL = url; fallbackFocusMethod = options?.method; fallbackFocusBody = options?.body;
      return { ok: true };
    };
    fallbackRoot.toggle.click(); fallbackRoot.branch.click();
    assert(fallbackRoot.kids.hidden && fallbackRoot.branch.textContent === 'Show 1 worker' && fallbackRoot.workers.textContent.includes('1 hidden worker') && fallbackRoot.branch.getAttribute('aria-expanded') === 'false', 'fallback worker can be hidden with an accurate count');
    assert(document.getElementById(fallbackRoot.branch.getAttribute('aria-controls')) === fallbackRoot.kids, 'fallback branch control addresses its effective children accessibly');
    fallbackRoot.branch.click();
    assert(!fallbackRoot.kids.hidden && fallbackRoot.branch.textContent === 'Hide 1 worker', 'fallback worker can be shown again');
    assert(fallbackFocusRequests === 0, 'fallback branch controls never request terminal focus');
    assert(await survivor.btn.onclick() === true, 'fallback worker terminal activation succeeds');
    assert(fallbackFocusURL === '/focus' && fallbackFocusMethod === 'POST', 'worker focus uses the terminal endpoint');
    assert(fallbackFocusRequests === 1 && JSON.parse(fallbackFocusBody).pid === 100, 'fallback worker terminal action targets its actual top-level agent exactly once');
    const fallbackElements = $('tree').querySelectorAll('*').length;
    for (let i = 0; i < 10; i++) render(fallbackSnapshot);
    assert(rows.get('sub:survivor') === survivor && $('tree').querySelectorAll('*').length === fallbackElements && fallbackFocusRequests === 1, 'fallback snapshots reuse bounded DOM without adding focus requests');
    render({ agents: [fallbackHost], subagents: [finishedParent, survivingWorker] });
    const temporaryParent = rows.get('sub:finished-parent');
    assert(survivor.node.parentElement === temporaryParent.kids && survivor.name.getBoundingClientRect().left > temporaryParent.name.getBoundingClientRect().left, 'a present declared parent owns the worker and its nested indentation');
    render(fallbackSnapshot);
    assert(temporaryParent.node.classList.contains('s-exiting') && temporaryParent.btn.disabled && survivor.node.parentElement === temporaryParent.kids, 'disappeared parent is retained as exiting for one snapshot without losing its worker');
    render(fallbackSnapshot);
    assert(rows.size === 2 && !rows.has('sub:finished-parent') && !temporaryParent.node.isConnected && survivor.node.parentElement === fallbackRoot.kids, 'worker moves to its effective parent when the exiting declared parent is removed without leaking rows');
    assert(survivor.name.getBoundingClientRect().left > fallbackRoot.name.getBoundingClientRect().left && !fallbackRoot.branch.hidden && /\b1 worker\b/.test(fallbackRoot.workers.textContent), 'removed-parent fallback restores indentation and the single-worker branch count');
    fallbackRoot.branch.click();
    assert(fallbackRoot.kids.hidden && fallbackRoot.branch.textContent === 'Show 1 worker', 'reparented worker remains foldable after its declared parent is removed');
    fallbackRoot.branch.click(); fallbackRoot.toggle.click();
    assert(!fallbackRoot.kids.hidden && fallbackFocusRequests === 1, 'reparented worker shows again without accidental terminal actions');
  } finally { window.fetch = fallbackFetch; }

  const providers = ['claude', 'codex', 'opencode', 'gemini', 'aider', 'cursor-agent'];
  const fleet = providers.map((kind, i) => agent(20000 + i, '/synthetic', { kind, title: 'Task ' + kind, recentCpu: 0.2, memBytes: 2 * GB }));
  const nested = Array.from({ length: 5 }, (_, i) => ({
    id: 'level-' + i, parentPid: 20000, parentAgentId: i ? 'level-' + (i - 1) : null,
    cwd: '/synthetic', description: i === 4 ? 'Investigate long nested task titles and preserve every word while keeping state and memory aligned across the whole tree' : 'Level ' + (i + 1), working: true,
  }));
  const workers = Array.from({ length: 20 }, (_, i) => ({ id: 'worker-' + i, parentPid: 20000, parentAgentId: 'level-4', cwd: '/synthetic', description: 'Worker ' + i, working: i % 2 === 0 }));
  const treeSnapshot = { agents: fleet, subagents: [...nested, ...workers] };
  render(treeSnapshot); render(treeSnapshot); $('expand').click();
  for (let i = 0; i < providers.length; i++) {
    const r = rows.get('a:' + (20000 + i));
    assert(r.logo.textContent || r.logo.querySelector('svg path'), providers[i] + ' keeps its provider icon or monogram');
    assert(r.agentKind.textContent, providers[i] + ' keeps a readable provider label');
    assert(!r.mem.classList.contains('warn') && !r.mem.classList.contains('crit'), 'large per-agent RAM stays neutral');
  }
  const reference = rows.get('a:20000'), referenceState = reference.st.getBoundingClientRect(), referenceRAM = reference.mem.getBoundingClientRect();
  for (const r of rows.values()) {
    const state = r.st.getBoundingClientRect(), ram = r.mem.getBoundingClientRect();
    assert(Math.abs(state.left - referenceState.left) <= 1 && Math.abs(ram.right - referenceRAM.right) <= 1, 'state and RAM columns align across providers, five levels, and 20 workers');
    assert(r.btn.getBoundingClientRect().right <= window.innerWidth && r.name.getBoundingClientRect().left >= 0 && r.name.getBoundingClientRect().right <= state.left, 'nested identity stays inside the viewport without overlapping metrics');
  }
  const deepest = rows.get('sub:level-4'), firstNested = rows.get('sub:level-0');
  assert(deepest.name.getBoundingClientRect().left > firstNested.name.getBoundingClientRect().left, 'nesting still indents task identity');
  assert(deepest.name.getBoundingClientRect().height > parseFloat(getComputedStyle(deepest.name).lineHeight), 'long nested task title wraps rather than truncates');
  assert(deepest.mem.textContent.includes('—') && !deepest.mem.textContent.includes('0 MB'), 'unavailable worker RAM is a dash, not zero');
  assert(deepest.cpu.textContent.includes('unavailable') && deepest.up.textContent.includes('unavailable'), 'worker details never invent CPU or process age');
  assert(document.documentElement.scrollWidth <= window.innerWidth, 'expanded five-level tree with 20 workers never causes horizontal overflow');
  assert($('tree').getAnimations({ subtree: true }).length === 0, 'CPU activity and nesting do not create communication pulses or a continuously animated tree');
  if (window.innerWidth >= 560) {
    assert(Math.abs($('tree').getBoundingClientRect().width - 528) <= 1, 'native layout keeps 528px inner content inside the 560px body');
    assert(reference.btn.getBoundingClientRect().height <= 55, 'short native rows remain compact');
  }
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) {
    assert([...rows.values()].every(r => getComputedStyle(r.logo).animationName === 'none'), 'reduced motion disables working-icon animation');
  }
  const stableNode = deepest.node, stableElements = $('tree').querySelectorAll('*').length;
  for (let i = 0; i < 20; i++) render(treeSnapshot);
  assert(rows.size === 31 && rows.get('sub:level-4').node === stableNode && $('tree').querySelectorAll('*').length === stableElements, 'repeated snapshots reuse rows without DOM growth');
  deepest.toggle.click(); deepest.branch.click();
  assert(deepest.kids.hidden && deepest.branch.getAttribute('aria-expanded') === 'false' && deepest.workers.textContent.includes('20 hidden workers'), 'worker branch folds independently and keeps the hidden count');
  assert(!firstNested.kids.hidden && !groups.get('r:/synthetic').kids.hidden, 'folding one worker branch leaves ancestors expanded');
  deepest.branch.click(); deepest.toggle.click();
  assert(!deepest.kids.hidden, 'worker branch can be reopened from its details');
  const outerBranch = rows.get('sub:level-3');
  const branchFetch = window.fetch;
  let branchFocusRequests = 0;
  try {
    window.fetch = async () => { branchFocusRequests++; throw new Error('Branch controls must not focus a terminal'); };
    outerBranch.toggle.click(); outerBranch.branch.click();
    assert(outerBranch.kids.hidden && outerBranch.workers.textContent.includes('21 hidden workers') && outerBranch.branch.textContent === 'Show 21 workers', 'collapsing one child with 20 grandchildren reports all 21 hidden workers, not just the direct child');
    assert(/\b20 workers\b/.test(deepest.workers.textContent) && /\b24 workers\b/.test(firstNested.workers.textContent) && /\b25 workers\b/.test(reference.workers.textContent), 'nested branch totals count each worker descendant once without including the branch itself');
    assert(groups.get('r:/synthetic').counts.textContent.includes('25 workers'), 'nested descendant totals do not double-count repository workers');
    outerBranch.branch.click(); outerBranch.toggle.click();
    assert(!outerBranch.kids.hidden && !deepest.kids.hidden && outerBranch.branch.textContent === 'Hide 21 workers', 'reopening the outer branch preserves its expanded child and full descendant count');
    const processChild = agent(20006, '/synthetic', { ppid: 20000, kind: 'codex', title: 'Nested process', recentCpu: 0.2 });
    render({ ...treeSnapshot, agents: [...fleet, processChild] });
    reference.toggle.click(); reference.branch.click();
    assert(reference.kids.hidden && reference.workers.textContent.includes('26 hidden nested agents') && reference.branch.textContent === 'Show 26 nested agents', 'mixed process and worker descendants are not mislabeled as 26 workers');
    assert(groups.get('r:/synthetic').counts.textContent.includes('7 working') && groups.get('r:/synthetic').counts.textContent.includes('25 workers'), 'mixed descendants preserve distinct process and worker repository counts');
    reference.branch.click(); reference.toggle.click();
    render(treeSnapshot); render(treeSnapshot);
    assert(!rows.has('a:20006') && /\b25 workers\b/.test(reference.workers.textContent) && branchFocusRequests === 0, 'removing a nested process restores worker-only totals without focus requests or retained rows');
  } finally { window.fetch = branchFetch; }
  $('collapse').click();
  assert(groups.get('r:/synthetic').kids.hidden && groups.get('r:/synthetic').counts.textContent.includes('6 working') && groups.get('r:/synthetic').counts.textContent.includes('25 workers'), 'collapsed repository preserves rolled-up agent and worker counts');
  $('expand').click();
  assert(!groups.get('r:/synthetic').kids.hidden && rows.get('sub:worker-19').node.isConnected, 'expanding restores all workers');
  show([fleet[0]]); show([fleet[0]]);
  assert(rows.size === 1 && groups.size === 1 && !$('tree').contains(stableNode), 'departed workers and nested DOM are released');

  for (const pressure of [null, 1, 2, 4]) {
    render({ agents: [fleet[0]], system: { pressure, swapUsedBytes: 8 * GB, freePct: 1 } });
    assert($('mem').classList.contains('warn') === (pressure === 2) && $('mem').classList.contains('crit') === (pressure === 4), 'system severity follows OS pressure, not low free RAM or existing swap');
    if (pressure === 1) assert(!$('mem').textContent.includes('Pressure normal') && $('mem').title.includes('Pressure normal') && $('system-details').textContent.includes('Pressure normal'), 'normal pressure keeps the summary compact and remains available in help and title');
  }
  for (const [pressure, label] of [[1, 'normal'], [2, 'warning'], [4, 'critical']]) {
    render({ agents: [fleet[0]], system: { pressure } });
    assert(!$('mem').hidden && $('mem').textContent.includes('Pressure ' + label), 'OS pressure is visible even when free RAM and swap are unavailable');
  }
  show([agent(20000, '/synthetic')]);
  assert(rows.get('a:20000').mem.textContent.includes('—') && $('mem').textContent.includes('unavailable'), 'missing agent RAM and unavailable system telemetry stay distinct from zero');
  show([agent(20000, '/synthetic', { recentCpu: null, idleSec: 30 })]);
  assert(rows.get('a:20000').st.textContent === 'unknown' && rows.get('a:20000').cpu.textContent.includes('unavailable') && $('quiet').hidden, 'missing activity telemetry is unknown, never quiet or zero CPU');

  const observations = Array.from({ length: 12 }, (_, i) => agent(30000 + i, '/observations', { recentCpu: 0.2, idleSec: 0 }));
  show(observations); show(observations);
  const quietObservations = observations.map((it, i) => ({ ...it, recentCpu: 0, idleSec: 15 + i }));
  const beforeQuiet = notifications;
  show(quietObservations);
  assert($('quiet-list').children.length === 3 && quietRows.size === 3 && notifications === beforeQuiet + observations.length, 'recent observation cards stay bounded while transitions notify once');
  const observationElements = $('quiet-list').querySelectorAll('*').length;
  for (let i = 0; i < 20; i++) show(quietObservations);
  assert($('quiet-list').querySelectorAll('*').length === observationElements && notifications === beforeQuiet + observations.length, 'quiet observations neither grow DOM nor repeat notifications');
  show(observations.map(it => ({ ...it, recentCpu: 0, idleSec: 301 })));
  assert($('quiet').hidden && quietRows.size === 0 && document.title === 'Swarmview', 'quiet observations expire after five minutes instead of becoming an alert queue');
  const beforeStale = notifications, snapshotNow = Date.now();
  render({ agents: observations, at: snapshotNow });
  render({ agents: quietObservations, at: snapshotNow - 20000 });
  assert($('quiet').hidden && notifications === beforeStale && [...rows.values()].filter(r => !r.node.classList.contains('s-exiting')).every(r => r.st.textContent === 'stale'), 'stale snapshots cannot produce quiet observations, notifications, or live state labels');
  const queryNow = Date.now;
  try {
    render({ agents: [a], at: snapshotNow });
    Date.now = () => snapshotNow + 16000;
    search('pid:100');
    assert(searchRows.get('a:100').meta.textContent === 'stale' && searchRows.get('a:100').node.classList.contains('s-unknown'), 'a query reads current freshness even before another poll or freshness update');
    closeSearch();
  } finally { Date.now = queryNow; }
  show([]); show([]);
  assert(rows.size === 0 && groups.size === 0 && quietRows.size === 0 && eps.size === 0, 'exited fleets release rows, observations, and notification episode metadata');

  // Synthetic protocol frames only: no user provider, plugin, or agent is started.
  const liveHost = agent(45000, '/live-test', { kind: 'opencode' });
  const liveWorkers = Array.from({ length: 25 }, (_, i) => ({ key: 'live:test:' + i, parent: i > 0 && i < 5 ? 'live:test:' + (i - 1) : 'a:45000', pid: 45000 }));
  const liveSnapshot = { agents: [liveHost], liveWorkers, communication: true, at: Date.now() };
  render(liveSnapshot); render(liveSnapshot); $('expand').click(); window.scrollTo(0, 0);
  assert(rows.size === 26 && $('count').textContent.startsWith('1 agents'), 'live workers do not fabricate process counts');
  for (const w of liveWorkers) {
    const r = rows.get(w.key);
    assert(r.st.textContent === 'unknown' && r.mem.textContent === '—' && r.cpu.textContent.includes('unavailable'), 'native session workers never invent CPU, RSS or lifecycle status');
    assert(Math.abs(r.mem.getBoundingClientRect().right - rows.get('a:45000').mem.getBoundingClientRect().right) <= 1, 'live five-level/20-worker metrics remain aligned');
  }
  assert(document.querySelectorAll('.communication-dot').length === 0, 'worker appearance does not communicate');
  communicationPulse('live:test:0', 'attempted');
  assert(activePulses.size === 1 && !document.querySelector('.communication-dot') && communicationFeedback.textContent.includes('acceptance not confirmed'), 'pre-admission attempt is static, never a sent dot');
  communicationPulse('live:test:0', 'sent');
  assert(activePulses.size === 1 && communicationFeedback.textContent.includes('Sent to worker'), 'explicit live send displays meaningful text');
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
  assert(document.querySelectorAll('.communication-dot').length === (reduced ? 0 : 1), 'reduced motion uses static feedback without dots');
  if (!reduced) {
    const animations = document.querySelector('.communication-dot').getAnimations();
    assert(animations.length === 1 && animations[0].effect.getTiming().iterations === 1 && animations[0].effect.getTiming().duration === 800, 'communication is a one-shot 800ms animation');
  }
  communicationPulse('live:test:0', 'sent'); assert(activePulses.size === 1, 'same-edge burst coalesces');
  const activeSend = activePulses.get('live:test:0'); render(liveSnapshot);
  assert(activePulses.get('live:test:0') === activeSend, 'unchanged snapshot preserves one-shot motion without restarting it');
  dispatchEvent(new Event('resize')); assert(activePulses.size === 0 && !document.querySelector('.communication-dot'), 'resize cleans animations and feedback');
  communicationPulse('live:test:0', 'returned'); openSearch();
  assert(activePulses.size === 0, 'search dialog cancels covered-edge feedback without changing focus behavior');
  communicationPulse('live:test:0', 'sent'); assert(activePulses.size === 0, 'covered edges do not animate or queue behind search');
  closeSearch();
  communicationPulse('live:test:0', 'returned'); assert(communicationFeedback.textContent.includes('Reply received'), 'returned result has distinct accessible text');
  rows.get('a:45000').branch.click();
  assert(activePulses.size === 0, 'fold cancels in-flight pulse');
  communicationPulse('live:test:0', 'sent'); assert(activePulses.size === 0, 'collapsed edges never animate or queue');
  rows.get('a:45000').branch.click();
  const previousEventSource = window.EventSource;
  let stream = null, opens = 0;
  window.EventSource = class { constructor() { stream = this; opens++; } close() { this.closed = true; } };
  try {
    communicationStream(); assert(opens === 1, 'opt-in uses one stream');
    stream.onmessage({ data: JSON.stringify({ workers: liveWorkers }) });
    assert(activePulses.size === 0, 'initial/reconnected state never replays pulses');
    const stableWorkers = liveWorkers.slice(0, 20), routeHost = agent(45100, '/live-route', { kind: 'opencode' });
    render({ ...liveSnapshot, agents: [liveHost, routeHost], liveWorkers: stableWorkers });
    const work = { get renders() { return app.work.renders; }, get rowUpdates() { return rowsState.work.calls; }, get hosts() { return agentsState.work.calls; } }, attributes = new MutationObserver(() => {});
    app.work.renders = rowsState.work.calls = agentsState.work.calls = 0;
    attributes.observe($('tree'), { subtree: true, attributes: true });
    try {
      for (let i = 0; i < 36; i++) {
        clearPulses();
        const direction = i % 2 ? 'returned' : 'sent';
        stream.onmessage({ data: JSON.stringify({ workers: stableWorkers, key: stableWorkers[0].key, direction }) });
        assert(activePulses.size === 1 && communicationFeedback.textContent.includes(i % 2 ? 'Reply received' : 'Sent to worker'), 'unchanged worker frames still present every confirmed observation');
        assert(document.querySelectorAll('.communication-dot').length === (reduced ? 0 : 1), 'unchanged frames preserve normal motion and reduced-motion feedback');
      }
      clearPulses();
      assert(work.renders === 0 && work.rowUpdates === 0 && work.hosts === 0 && attributes.takeRecords().length === 0, '36 unchanged SSE frames perform no full render, updateAgentRow calls, host formatting or tree attribute writes');
      assert(activePulses.size === 0 && !document.querySelector('.communication-dot') && !document.getAnimations().length, 'unchanged-frame burst cleanup stays bounded');
      const frame = workers => stream.onmessage({ data: JSON.stringify({ workers }) });
      let count = work.renders;
      frame([...stableWorkers].reverse());
      assert(work.renders === ++count && app.lastData.liveWorkers[0].key === stableWorkers.at(-1).key, 'changed worker order renders even when the same keys remain');
      frame(stableWorkers); count++;
      const reparented = stableWorkers.map((w, i) => i ? w : { ...w, parent: stableWorkers[5].key });
      frame(reparented);
      assert(work.renders === ++count && rows.get(stableWorkers[0].key).node.parentElement === rows.get(stableWorkers[5].key).kids, 'same-length parent changes reparent live rows');
      frame(stableWorkers); count++;
      const rerouted = stableWorkers.map((w, i) => i ? w : { ...w, pid: routeHost.pid });
      frame(rerouted);
      assert(work.renders === ++count && rows.get(stableWorkers[0].key).pid === routeHost.pid && rows.get(stableWorkers[0].key).terminal.textContent.includes('/live-route'), 'same-key owner changes refresh focus and presentation fields');
      frame(stableWorkers); count++;
      const replaced = stableWorkers.map((w, i) => i ? w : { ...w, key: 'live:replacement:0' });
      frame(replaced);
      assert(work.renders === ++count && rows.has('live:replacement:0') && !rows.has(stableWorkers[0].key), 'same-length worker identity changes replace the correct row');
      frame(stableWorkers); count++;
      const savedFetch = window.fetch, beforeHosts = work.hosts;
      try {
        window.fetch = async url => {
          assert(url === '/agents', 'normal poll still uses the process snapshot endpoint');
          return { ok: true, json: async () => ({ ...app.lastData, agents: [{ ...liveHost, memBytes: 750 * MB, title: 'Updated host task' }, routeHost], at: Date.now() }) };
        };
        await poll(); clearTimeout(app.timer);
      } finally { window.fetch = savedFetch; }
      assert(work.renders === ++count && rows.get('a:45000').mem.textContent === '~750 MB' && rows.get('a:45000').name.textContent === 'Updated host task', 'ordinary polls continue updating host RAM and task identity');
      assert(work.hosts - beforeHosts === 2, 'a poll formats each host once, not again for its 20 workers');
      const realNow = Date.now;
      try {
        communicationPulse(stableWorkers[0].key, 'sent');
        Date.now = () => app.lastData.at + 20000;
        frame(stableWorkers);
        assert(work.renders === count && activePulses.size === 0 && rows.get('a:45000').st.textContent === 'stale', 'unchanged frames still age snapshots and cancel stale motion');
      } finally { Date.now = realNow; freshness(); }
      $('offline').hidden = false; frame(stableWorkers);
      assert(work.renders === count && rows.get('a:45000').st.textContent === 'offline', 'unchanged topology cannot mask offline state');
      $('offline').hidden = true; freshness();
      stream.onmessage({ data: JSON.stringify({ workers: stableWorkers, lost: true, key: stableWorkers[0].key, direction: 'sent' }) });
      assert(work.renders === count && activePulses.size === 0 && communicationStatus.textContent.includes('dropped'), 'unchanged topology still acknowledges loss without replaying a pulse');
      stream.onmessage({ data: JSON.stringify({ workers: stableWorkers, ambiguous: true }) });
      assert(work.renders === count && communicationStatus.textContent.includes('ambiguous'), 'unchanged topology still updates ambiguity availability');
    } finally {
      attributes.disconnect(); clearPulses();
      $('offline').hidden = true;
    }
    render(liveSnapshot);
    stream.onmessage({ data: JSON.stringify({ workers: [], ambiguous: true }) });
    assert($('communication-status').textContent.includes('ambiguous') && rows.size === 1, 'ambiguous native viewers suppress worker attribution with an explicit availability reason');
    stream.onmessage({ data: JSON.stringify({ workers: liveWorkers, lost: true }) });
    assert($('communication-status').textContent.includes('dropped') && activePulses.size === 0, 'dropped burst is acknowledged rather than replayed');
    stream.onerror(); assert($('communication-status').textContent.includes('missing'), 'stream gap is acknowledged');
    Object.defineProperty(document, 'hidden', { configurable: true, value: true }); communicationStream();
    assert(stream.closed && !communication.source, 'hidden tab disconnects and discards observations');
    communicationPulse('live:test:0', 'sent'); assert(activePulses.size === 0, 'hidden tab never animates');
    delete document.hidden;
    const oldStream = stream; communicationStream();
    oldStream.onmessage({ data: JSON.stringify({ workers: liveWorkers, key: 'live:test:0', direction: 'sent' }) });
    assert(opens === 2 && activePulses.size === 0, 'callbacks from a closed stream generation cannot replay after visibility resumes');
    render({ agents: [liveHost], at: Date.now() }); communicationStream();
    assert(opens === 2 && !communication.source && rows.size === 1, 'disabled bridge has no connection and releases live worker rows immediately');
  } finally { delete document.hidden; communication.closeSource(); window.EventSource = previousEventSource; clearPulses(); }

  parent.document.body.dataset.searchMs = document.body.dataset.searchMs;
  parent.document.body.dataset.uiTests = 'passed';
} catch (error) {
  parent.document.body.dataset.uiTests = 'failed';
  const failure = document.createElement('pre');
  failure.textContent = error.stack;
  parent.document.body.append(failure);
} })();
