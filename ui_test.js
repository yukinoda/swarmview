// Injected into index.html by TestUI; no browser automation dependency needed.
(async () => { try {
  const assert = (ok, message) => { if (!ok) throw new Error(message); };
  const agent = (pid, repo, extra = {}) => ({
    pid, ppid: 1, kind: 'codex', cwd: repo, repo, repoName: repo.slice(1), worktree: repo,
    recentCpu: 0, idleSec: null, isNew: false, etime: '01:00', ...extra,
  });
  const show = agents => render({ agents, subagents: [] });
  const a = agent(100, '/a');

  show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
  const metrics = rows.get('a:100');
  assert(metrics.cpu.textContent === 'CPU 136%' && metrics.mem.textContent === 'RAM ~1.6 GB', 'metrics identify CPU (including multi-core use) and approximate RAM');
  assert(metrics.up.textContent === 'up 1m' && metrics.up.title.includes('process age'), 'uptime is labeled as process age');
  assert(groups.get('r:/a').counts.textContent.includes('1 working'), 'repository counts include status text, not just color');
  assert(!$('reading').open && $('reading').textContent.includes('not percent complete'), 'metric help is available without leaving the dashboard');
  $('reading').open = true;
  assert($('reading').getBoundingClientRect().height > 200, 'expanded help is visible in the dashboard');
  $('reading').open = false;
  assert(document.documentElement.scrollWidth <= window.innerWidth, 'labeled metrics and repository counts fit the narrow viewport');

  let notifications = 0;
  window.Notification = class {
    static permission = 'granted';
    constructor() { notifications++; }
  };
  notifyOn = true;
  show([{ ...a, recentCpu: 0.1, idleSec: 0 }]);
  show([{ ...a, idleSec: 14 }]);
  assert($('needs').hidden && notifications === 0, 'waiting grace period');
  show([{ ...a, idleSec: 15 }]);
  assert(!$('needs').hidden && $('needs-list').children.length === 1, 'needs-you section');
  assert(document.title === '(1) waiting · Swarmview' && notifications === 1, 'waiting badge and notification');
  show([{ ...a, idleSec: 20 }]);
  assert(notifications === 1, 'notify once per waiting episode');
  show([{ ...a, recentCpu: 0.1, idleSec: 0 }]);
  assert($('needs').hidden && document.title === 'Swarmview', 'working clears waiting badge');
  show([{ ...a, idleSec: 15 }]);
  assert(notifications === 2, 'new working episode rearms notification');
  show([]);
  show([{ ...a, idleSec: 15 }]);
  assert(notifications === 2, 'unseen work does not notify');

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
  show([{ ...focusable, idleSec: 15 }]);
  assert(!needRows.get('a:100').btn.disabled, 'needs-you card allows tmux selection');
  show([agent(100, '/b', { idleSec: 15 })]);
  assert(needRows.get('a:100').btn.disabled, 'monitoring-only card is not clickable');

  render({ agents: [a], at: 100000 });
  freshness(140000);
  assert($('freshness').textContent === 'Last updated 40s ago', 'freshness uses snapshot time');
  freshness(220000);
  assert($('freshness').textContent === 'Last updated 2m ago', 'retained snapshot keeps aging');
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
    await poll(); clearTimeout(timer);
    assert(!$('offline').hidden && $('freshness').textContent === 'Last updated 40s ago', 'failed poll retains and ages last snapshot');
    assert(rows.has('a:100'), 'failed poll retains agent rows');
    window.fetch = async () => ({ ok: true, json: async () => ({ agents: [a], at: 260000 }) });
    await poll(); clearTimeout(timer);
    assert($('offline').hidden && $('freshness').textContent === 'Last updated 0s ago', 'successful retry clears offline and refreshes age');
  } finally { window.fetch = realFetch; Date.now = realNow; clearTimeout(timer); }

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
  assert([...rows.values()].every(r => r.search === null), 'unused search creates no per-agent index');
  const cachedNode = rows.get('a:502').node;
  toggleFold('r:/api');
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
  assert(!$('needs').hidden && document.title === '(1) waiting · Swarmview' && notifications === beforeHiddenNotification + 1, 'the palette does not filter or suppress global waiting sections, badges, or notifications');
  $('clear-search').click();
  assert(!$('needs').hidden && $('search').value === '' && !$('search-panel').hidden, 'clear restores waiting cards and keeps search open');
  search('task:Coordinator');
  $('close-search').click();
  assert($('search-panel').hidden && $('search').value === '' && $('search-toggle').getAttribute('aria-expanded') === 'false', 'Done closes and clears search without needing Escape');
  assert(groups.get('r:/api').kids.hidden && [...rows.values()].every(r => r.search === null), 'Done restores folds and releases the search index');

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
  assert([...rows.values()].every(r => r.search === null), 'clearing search releases the metadata index');
  show(many.slice(0, 1)); show(many.slice(0, 1));
  assert(rows.size === 1 && groups.size === 1, 'exited agents and their search metadata are discarded');
  $('close-search').click();
  assert($('search-panel').hidden && !$('search-panel').open && $('search').value === '' && searchRows.size === 0 && searchReturnFocus === null, 'closing removes temporary results, releases old focus references, and leaves no hidden query');

  document.body.dataset.uiTests = 'passed';
} catch (error) {
  document.body.dataset.uiTests = 'failed';
  const failure = document.createElement('pre');
  failure.textContent = error.stack;
  document.body.append(failure);
} })();
