import { testState as app } from './app.js';
import { testState as dashboard } from './dashboard.js';
import {
  testState as searchState,
  SEARCH_LIMIT,
  openSearch,
  closeSearch,
  applySearch,
} from './search.js';
import { testState as notificationsState } from './notifications.js';
import { LOGOS, logo } from './icons.js';
import { MB, GB, snapshotItems, testState as agentsState } from './agents.js';
import { testState as rowsState } from './rows.js';
import { closeDetails, showDetails } from './rows.js';
import {
  testState as communication,
  communicationStream,
  clearPulses,
} from './communication.js';
import { $ } from './dom.js';

const { rows, groups, quietRows } = dashboard;
const { searchRows, searchIndex } = searchState;
const { eps } = notificationsState;
const { render, poll, freshness } = app;
const {
  activeFeedback,
  showFeedback,
  communicationFeedback,
  communicationStatus,
} = communication;

// Test-only exports are appended by TestUI, never embedded or served in production.
(async () => {
  try {
    const assert = (ok, message) => {
      if (!ok) throw new Error(message);
    };
    const agent = (pid, repo, extra = {}) => ({
      pid,
      ppid: 1,
      kind: 'codex',
      cwd: repo,
      repo,
      repoName: repo.slice(1),
      worktree: repo,
      recentCpu: 0,
      idleSec: null,
      isNew: false,
      etime: '01:00',
      ...extra,
    });
    const show = (agents) => render({ agents, subagents: [] });
    const branchOf = (row) => {
      if (
        !$('detail-dialog').open ||
        $('detail-title').textContent !== row.item.name
      )
        showDetails(row);
      return $('detail-branch');
    };
    const a = agent(100, '/a');
    assert(
      !Object.hasOwn(window, 'render') && !Object.hasOwn(window, 'rows'),
      'module internals do not become browser globals',
    );
    const cycle = snapshotItems(
      {
        agents: [
          agent(1, '/cycle', { ppid: 2 }),
          agent(2, '/cycle', { ppid: 1 }),
        ],
      },
      new Map(),
    );
    for (const item of cycle.values()) {
      const visited = new Set();
      for (let current = item; current; current = cycle.get(current.parent)) {
        assert(
          !visited.has(current.key),
          'snapshot normalization breaks parent cycles',
        );
        visited.add(current.key);
      }
    }
    const scenario = new URLSearchParams(location.search);
    assert(
      window.innerWidth === (scenario.get('size') === 'native' ? 680 : 375) &&
        window.innerHeight === (scenario.get('size') === 'native' ? 820 : 812),
      'checks run at the actual compact/native content viewport, not Chrome minimum window dimensions',
    );
    assert(
      matchMedia('(prefers-color-scheme: dark)').matches ===
        (scenario.get('theme') === 'dark'),
      'browser runs the requested color scheme',
    );
    assert(
      matchMedia('(prefers-reduced-motion: reduce)').matches ===
        (scenario.get('reduced') === 'true'),
      'browser runs the requested motion preference',
    );
    const brand = document.querySelector('.brand'),
      brandLogo = brand.querySelector('img');
    assert(
      brandLogo.getBoundingClientRect().width === 28 &&
        parseFloat(getComputedStyle(brand).fontSize) >= 17 &&
        !brand.querySelector('p'),
      'compact brand keeps the real SVG, readable name, and no tagline',
    );
    assert(
      $('search-toggle').getBoundingClientRect().height >= 44 &&
        $('search-toggle').getBoundingClientRect().width >= 44,
      'Search retains a 44px target',
    );
    $('preferences-toggle').focus();
    $('preferences-toggle').click();
    assert(
      $('preferences-dialog').open &&
        !$('notify').closest('header') &&
        $('communication-capability').textContent.includes('Waiting') &&
        !document.querySelector('#preferences-dialog input[type=checkbox]'),
      'Preferences contains the one real notification control and read-only capability, not a fake bridge switch',
    );
    for (const theme of ['dark', 'light', 'system', 'invalid']) {
      $('appearance').value = theme;
      $('appearance').dispatchEvent(new Event('change'));
      const valid = theme === 'invalid' ? 'system' : theme;
      assert(
        JSON.parse(localStorage.getItem('swarmview.appearance')) === valid &&
          document.documentElement.dataset.theme ===
            (valid === 'system'
              ? matchMedia('(prefers-color-scheme: dark)').matches
                ? 'dark'
                : 'light'
              : valid),
        'appearance validates and persists only the local light/dark/system preference',
      );
    }
    $('preferences-dialog').dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    );
    assert(
      !$('preferences-dialog').open &&
        document.activeElement === $('preferences-toggle'),
      'Preferences Escape works without native dialog cancel and returns focus',
    );

    for (const kind of ['aider', 'cursor-agent']) {
      show([{ ...a, kind, recentCpu: 0.1, idleSec: 0 }]);
      show([{ ...a, kind, idleSec: 15 }]);
      const path = rows.get('a:100').logo.querySelector('svg path');
      assert(
        path && path.getAttribute('d') === LOGOS[kind],
        kind + ' uses its embedded SVG logo in agent rows',
      );
      assert(
        quietRows
          .get('a:100')
          .logo.querySelector('svg path')
          .getAttribute('d') === LOGOS[kind],
        kind + ' uses its logo in recently quiet cards after observed work',
      );
      openSearch();
      $('search').value = 'pid:100';
      applySearch();
      assert(
        searchRows
          .get('a:100')
          .logo.querySelector('svg path')
          .getAttribute('d') === LOGOS[kind],
        kind + ' uses its logo in search results',
      );
      closeSearch();
    }
    assert(
      logo('unknown', 20).textContent === 'U',
      'unknown agent kinds retain the monogram fallback',
    );

    show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
    const metrics = rows.get('a:100');
    const taskStyle = getComputedStyle(metrics.btn),
      metricStyle = getComputedStyle(metrics.toggle);
    assert(
      parseFloat(taskStyle.paddingLeft) >= 8 &&
        parseFloat(taskStyle.paddingRight) >= 8 &&
        parseFloat(taskStyle.gap) >= 8 &&
        metrics.identity.getBoundingClientRect().left -
          metrics.logo.getBoundingClientRect().right >=
          8,
      'task controls keep 8px horizontal padding and icon/text spacing',
    );
    assert(
      parseFloat(metricStyle.paddingLeft) >= 2 &&
        metrics.toggle.getBoundingClientRect().left -
          metrics.mem.getBoundingClientRect().right >=
          4,
      'metrics and explicit Details keep a separate breathing gap',
    );
    assert(
      metrics.toggle.getBoundingClientRect().width >= 56 &&
        metrics.btn.getBoundingClientRect().height === 47,
      'explicit Details retains a usable width and short rows remain 47px',
    );
    assert(
      metrics.st.parentElement === metrics.mem.parentElement &&
        metrics.st.getBoundingClientRect().right ===
          metrics.mem.getBoundingClientRect().right,
      'State and RAM share one aligned readable metric column',
    );
    assert(
      metrics.item.cpu === 'CPU 136%' &&
        metrics.mem.textContent === '~1.6 GB' &&
        metrics.toggle.getAttribute('aria-label').includes('RAM'),
      'metrics identify CPU (including multi-core use) and approximate RAM',
    );
    assert(
      metrics.item.up === 'Process age: 1m',
      'uptime is labeled as process age',
    );
    assert(
      !$('detail-dialog').open &&
        !$('detail-body').children.length &&
        !metrics.cpu &&
        !metrics.detail &&
        !metrics.btn.contains(metrics.toggle) &&
        !document.querySelector('button button'),
      'CPU and age are lazy in one shared dialog, never hidden per-row DOM or nested terminal buttons',
    );
    const detailFetch = window.fetch;
    let detailRequests = 0;
    try {
      window.fetch = async () => {
        detailRequests++;
        throw new Error('Details must not request terminal focus');
      };
      metrics.toggle.focus();
      metrics.toggle.click();
      assert(
        $('detail-dialog').open &&
          metrics.toggle.getAttribute('aria-expanded') === 'true' &&
          document.getElementById(
            metrics.toggle.getAttribute('aria-controls'),
          ) === $('detail-dialog'),
        'monitoring-only details open accessibly',
      );
      assert(
        $('detail-body').textContent.includes('CPU 136%') &&
          $('detail-body').textContent.includes('Process age: 1m') &&
          $('detail-body').getBoundingClientRect().height > 0,
        'opened details expose CPU and process age',
      );
      assert(
        $('detail-terminal').disabled,
        'monitoring-only Details never invents a terminal destination',
      );
      const stableDetails = $('detail-body').firstChild;
      freshness();
      freshness();
      assert(
        $('detail-body').firstChild === stableDetails,
        'unchanged freshness frames reuse the selected Details DOM rather than allocating another dialog body',
      );
      const detailsFocus = document.activeElement;
      show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
      assert(
        $('detail-dialog').open && document.activeElement === detailsFocus,
        'snapshot updates preserve details and keyboard focus',
      );
      const polledDetails = $('detail-body').firstChild;
      show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
      assert(
        $('detail-body').firstChild === polledDetails,
        'an unchanged poll keeps the open Details DOM even though snapshot objects are rebuilt',
      );
      closeDetails();
      assert(
        !$('detail-dialog').open &&
          !$('detail-body').children.length &&
          document.activeElement === metrics.toggle &&
          metrics.toggle.getAttribute('aria-expanded') === 'false',
        'details close independently, release DOM, and return keyboard focus',
      );
      assert(detailRequests === 0, 'details never requests terminal focus');
      showDetails(metrics);
      show([{ ...a, recentCpu: 0.5, memBytes: 500 * MB }]);
      assert(
        $('detail-body').textContent.includes('CPU 50%') &&
          $('detail-body').textContent.includes('~500 MB'),
        'the selected shared Details follows current measurements rather than retaining its opening snapshot',
      );
      $('detail-dialog').dispatchEvent(
        new KeyboardEvent('keydown', {
          key: 'Escape',
          bubbles: true,
          cancelable: true,
        }),
      );
      assert(
        !$('detail-dialog').open &&
          !$('detail-body').children.length &&
          document.activeElement === metrics.toggle,
        'Details Escape releases selected DOM and safely restores focus',
      );
      showDetails(metrics);
      show([]);
      show([]);
      assert(
        !$('detail-dialog').open &&
          !$('detail-body').children.length &&
          document.activeElement === $('search-toggle'),
        'disappearing selected rows close Details and return focus to a connected control',
      );
      show([{ ...a, recentCpu: 1.36, memBytes: 1.6 * GB }]);
    } finally {
      window.fetch = detailFetch;
    }
    assert(
      groups.get('r:/a').counts.textContent.includes('1 working'),
      'repository counts include status text, not just color',
    );
    assert(
      !$('reading').open &&
        $('reading').textContent.includes('not percent complete'),
      'metric help is available without leaving the dashboard',
    );
    $('reading').open = true;
    assert(
      $('reading').getBoundingClientRect().height > 200,
      'expanded help is visible in the dashboard',
    );
    $('reading').open = false;
    assert(
      document.documentElement.scrollWidth <= window.innerWidth,
      'labeled metrics and repository counts fit the narrow viewport',
    );

    let notifications = 0,
      notificationBody = '';
    window.Notification = class {
      static permission = 'granted';
      constructor(title, options) {
        notifications++;
        notificationBody = options.body;
      }
    };
    await $('notify').onclick();
    await $('notify').onclick(); // turn off before testing an explicit permission request
    let permissions = 0;
    Notification.permission = 'default';
    Notification.requestPermission = async () => {
      permissions++;
      return 'denied';
    };
    $('preferences-toggle').click();
    $('appearance').dispatchEvent(new Event('change'));
    $('close-preferences').click();
    assert(
      permissions === 0,
      'opening Preferences or changing appearance never prompts for OS notification permission',
    );
    await $('notify').onclick();
    assert(
      permissions === 1 && $('notify').getAttribute('aria-pressed') === 'false',
      'only explicit notification enable requests permission and denied permission stays off',
    );
    Notification.permission = 'granted';
    await $('notify').onclick();
    assert(
      $('notify').getAttribute('aria-pressed') === 'true',
      'the existing opt-in notification control can be re-enabled after permission is granted',
    );
    show([{ ...a, recentCpu: 0.1, idleSec: 0 }]);
    show([{ ...a, idleSec: 14 }]);
    assert(
      $('quiet').hidden && notifications === 0,
      'quiet observation grace period',
    );
    show([{ ...a, idleSec: 15 }]);
    assert(
      !$('quiet').hidden && $('quiet-list').children.length === 1,
      'recently quiet observation section',
    );
    assert(
      document.title === '(1) recently quiet · Swarmview' &&
        notifications === 1,
      'quiet observation badge and notification',
    );
    assert(
      notificationBody.includes('Recently quiet') &&
        notificationBody.includes('not a request'),
      'notification describes evidence without inventing a human request',
    );
    show([{ ...a, idleSec: 20 }]);
    assert(notifications === 1, 'notify once per waiting episode');
    show([{ ...a, recentCpu: 0.1, idleSec: 0 }]);
    assert(
      $('quiet').hidden && document.title === 'Swarmview',
      'working clears quiet badge',
    );
    show([{ ...a, idleSec: 15 }]);
    assert(notifications === 2, 'new working episode rearms notification');
    show([]);
    show([{ ...a, idleSec: 15 }]);
    assert(notifications === 2, 'unseen work does not notify');
    assert(
      $('quiet').hidden,
      'unseen quiet processes are not presented as newly observed transitions',
    );

    const b = agent(200, '/b');
    show([a, b]);
    assert(
      $('tree').firstElementChild === groups.get('r:/a').node,
      'default repo order',
    );
    groups.get('r:/b').pin.click();
    assert(
      $('tree').firstElementChild === groups.get('r:/b').node,
      'pin moves repo to top',
    );
    assert(
      JSON.parse(localStorage.getItem('swarmview.pins'))[0] === '/b',
      'pin persisted',
    );
    groups.get('r:/b').fold.click();
    assert(
      groups.get('r:/b').kids.hidden &&
        groups.get('r:/b').fold.getAttribute('aria-expanded') === 'false',
      'fold hides children accessibly',
    );
    assert(
      JSON.parse(localStorage.getItem('swarmview.folded')).includes('r:/b'),
      'fold persisted',
    );
    $('collapse').click();
    assert(
      [...groups.values()].every((g) => g.kids.hidden),
      'collapse all',
    );
    $('expand').click();
    assert(
      [...groups.values()].every((g) => !g.kids.hidden),
      'expand all',
    );

    show([a]);
    show([a]); // Remove the exiting row before checking group placement.
    const row = rows.get('a:100').node;
    const branch = agent(300, '/a', {
      worktree: '/a/feature',
      cwd: '/a/feature',
    });
    show([a, branch]);
    assert(
      row.parentElement === groups.get('w:/a').kids,
      'main checkout gains worktree group',
    );
    assert(
      rows.get('a:300').node.parentElement === groups.get('w:/a/feature').kids,
      'branch in its worktree',
    );
    show([a]);
    show([a]);
    assert(
      row.parentElement === groups.get('r:/a').kids && !groups.has('w:/a'),
      'lone checkout drops worktree level',
    );
    show([agent(100, '/b')]);
    assert(
      rows.get('a:100').node === row &&
        row.parentElement === groups.get('r:/b').kids,
      'keyed row moves between repos',
    );
    assert(!groups.has('r:/a'), 'old repo removed');

    assert(
      rows.get('a:100').btn.disabled,
      'standalone agents are monitoring-only',
    );
    assert(
      rows.get('a:100').target.textContent === 'Monitoring only · outside tmux',
      'non-navigable rows explain why inline',
    );
    const focusable = agent(100, '/b', {
      canFocus: true,
      tmuxAttached: true,
      tmuxTarget: 'dev:%42',
    });
    show([focusable]);
    assert(!rows.get('a:100').btn.disabled, 'tmux agents allow pane selection');
    assert(
      rows.get('a:100').target.textContent === 'dev:%42',
      'session and pane visible',
    );
    assert(
      rows.get('a:100').target.title.includes('session/pane'),
      'target help identifies the session, not the window',
    );
    const terminalRow = rows.get('a:100'),
      focusFetch = window.fetch;
    try {
      window.fetch = async () => {
        throw new Error('offline');
      };
      await terminalRow.btn.onclick();
      assert(
        terminalRow.note.textContent === 'server unreachable' &&
          terminalRow.note.isConnected &&
          terminalRow.btn.getAttribute('aria-busy') === 'false',
        'terminal focus failures stay visible and release the busy state',
      );
      showDetails(terminalRow);
      await $('detail-terminal').onclick();
      assert(
        $('detail-dialog').open &&
          $('detail-error').textContent === 'server unreachable',
        'the shared Details keeps a terminal failure visible inside the modal',
      );
      closeDetails();
    } finally {
      window.fetch = focusFetch;
    }
    show([{ ...focusable, canFocus: false, tmuxAttached: false }]);
    assert(
      rows.get('a:100').btn.disabled &&
        rows.get('a:100').target.textContent.includes('attach client'),
      'detached pane shows recovery and disables selection',
    );
    assert(
      rows.get('a:100').btn.title.includes('Attach a tmux client'),
      'detached pane explains recovery',
    );
    show([
      { ...focusable, kind: 'opencode', title: 'Next steps for swarmview' },
    ]);
    assert(
      rows.get('a:100').name.textContent === 'Next steps for swarmview',
      'conversation title is the primary label',
    );
    assert(
      rows.get('a:100').logo.querySelector('svg path'),
      'OpenCode uses an inline SVG rather than a monogram or remote image',
    );
    assert(
      rows.get('a:100').agentKind.textContent === 'OpenCode' &&
        rows.get('a:100').target.textContent === 'dev:%42',
      'agent identity and tmux target stay visible below title',
    );
    show([{ ...focusable, kind: 'opencode', title: null }]);
    assert(
      rows.get('a:100').name.textContent === 'OpenCode',
      'agent name remains useful when no session title is available',
    );
    show([{ ...focusable, tmuxAttached: false }]);
    assert(
      !rows.get('a:100').btn.disabled &&
        rows.get('a:100').target.textContent.includes('open Ghostty'),
      'detached pane can open Ghostty when supported',
    );
    render({
      agents: [focusable, agent(200, '/b')],
      subagents: [{ id: 'child', parentPid: 100, cwd: '/b', working: true }],
    });
    assert(
      !rows.get('sub:child').btn.disabled && rows.get('sub:child').pid === 100,
      'subagent uses its actual parent focus target',
    );
    show([{ ...focusable, recentCpu: 0.1, idleSec: 0 }]);
    show([{ ...focusable, idleSec: 15 }]);
    assert(
      !quietRows.get('a:100').btn.disabled,
      'quiet observation card allows tmux selection',
    );
    show([agent(100, '/b', { idleSec: 15 })]);
    assert(
      quietRows.get('a:100').btn.disabled,
      'monitoring-only card is not clickable',
    );

    render({ agents: [a], at: 100000 });
    freshness(140000);
    assert(
      $('freshness').textContent.includes('Last updated 40s ago') &&
        $('freshness').textContent.includes('retained') &&
        rows.get('a:100').st.textContent === 'stale',
      'freshness uses snapshot time and marks old state stale',
    );
    freshness(220000);
    assert(
      $('freshness').textContent.includes('Last updated 2m ago'),
      'retained snapshot keeps aging',
    );
    render({ agents: [a], at: 220000 });
    freshness(220000);
    assert(
      $('freshness').textContent === 'Last updated 0s ago',
      'recovery refreshes snapshot age',
    );
    const realFetch = window.fetch,
      realNow = Date.now;
    try {
      Date.now = () => 260000;
      window.fetch = async (url, options) => {
        assert(
          url === '/agents' && options.signal instanceof AbortSignal,
          'poll has a timeout signal',
        );
        throw new Error('offline');
      };
      await poll();
      clearTimeout(app.timer);
      assert(
        !$('offline').hidden &&
          $('freshness').textContent.includes('Last updated 40s ago') &&
          rows.get('a:100').st.textContent === 'offline',
        'failed poll retains and ages last snapshot without labeling it quiet',
      );
      assert(rows.has('a:100'), 'failed poll retains agent rows');
      window.fetch = async () => ({
        ok: true,
        json: async () => ({ agents: [a], at: 260000 }),
      });
      await poll();
      clearTimeout(app.timer);
      assert(
        $('offline').hidden &&
          $('freshness').textContent === 'Last updated 0s ago',
        'successful retry clears offline and refreshes age',
      );
    } finally {
      window.fetch = realFetch;
      Date.now = realNow;
      clearTimeout(app.timer);
    }

    show([agent(100, '/b'), agent(400, '/c')]);
    const ids = [...groups.values()].map((g) => g.kids.id);
    assert(
      new Set(ids).size === ids.length,
      'group IDs stay unique after removals',
    );
    for (const g of groups.values()) {
      assert(
        document.getElementById(g.fold.getAttribute('aria-controls')) ===
          g.kids,
        'fold controls its own children',
      );
    }

    assert(
      $('search-panel').hidden &&
        $('search-toggle').getAttribute('aria-expanded') === 'false',
      'search stays out of the default dashboard',
    );
    const macPlatform = navigator.platform.startsWith('Mac');
    assert(
      $('search-key').textContent === (macPlatform ? '⌘ K' : 'Ctrl K') &&
        $('search-toggle').title ===
          `Search agents (${macPlatform ? '⌘ K' : 'Ctrl K'})` &&
        $('search').getAttribute('aria-keyshortcuts') ===
          (macPlatform ? 'Meta+k' : 'Control+k'),
      'shortcut hints, tooltips, and accessibility metadata match the platform',
    );
    const search = (value) => {
      if ($('search-panel').hidden) openSearch();
      $('search').value = value;
      $('search').dispatchEvent(new Event('input'));
    };
    const coordinator = agent(501, '/api', {
      kind: 'claude',
      title: 'Coordinator',
      canFocus: true,
    });
    const cacheTask = agent(502, '/api', {
      ppid: 501,
      kind: 'opencode',
      title: 'Fix cache invalidation',
      cwd: '/api/feature/src',
      worktree: '/api/feature',
      canFocus: true,
      tmuxAttached: true,
      tmuxTarget: 'dev:%42',
      recentCpu: 0.2,
      idleSec: 0,
    });
    const documentation = agent(503, '/docs', { title: 'Review glossary' });
    const sibling = agent(504, '/api', {
      title: 'Review authentication',
      idleSec: 30,
    });
    show([coordinator, cacheTask, documentation, sibling]);
    show([coordinator, cacheTask, documentation, sibling]);
    assert(searchIndex.size === 0, 'unused search creates no per-agent index');
    const cachedNode = rows.get('a:502').node;
    groups.get('r:/api').fold.click();
    const savedFolds = localStorage.getItem('swarmview.folded');
    const treeTop = $('tree').getBoundingClientRect().top,
      dashboardHeight = document.documentElement.scrollHeight;
    search('agent:OPENCODE "cache invalidation"');
    assert(
      $('search-panel').open &&
        $('search-count').textContent === '1 match' &&
        searchRows.has('a:502'),
      'native dialog matches fields and quoted phrases',
    );
    assert(
      !rows.get('a:504').node.hidden &&
        !groups.get('r:/docs').node.hidden &&
        groups.get('r:/api').kids.hidden,
      'search leaves the background dashboard and folds unchanged',
    );
    assert(
      $('tree').getBoundingClientRect().top === treeTop &&
        document.documentElement.scrollHeight === dashboardHeight,
      'opening and filtering the palette causes no background layout shift',
    );
    assert(
      localStorage.getItem('swarmview.folded') === savedFolds,
      'search never overwrites saved folds',
    );
    $('search').focus();
    $('search').dispatchEvent(
      new KeyboardEvent('keydown', { key: 'ArrowDown', cancelable: true }),
    );
    assert(
      document.activeElement === searchRows.get('a:502').btn,
      'down arrow reaches the matching result inside the modal',
    );
    const selected = searchRows.get('a:502').btn,
      selectedBox = selected.getBoundingClientRect(),
      resultsBox = $('search-results-wrap').getBoundingClientRect();
    const selectionStyle = getComputedStyle(selected);
    const ringOutside = Math.max(
      0,
      parseFloat(selectionStyle.outlineWidth) +
        parseFloat(selectionStyle.outlineOffset),
    );
    assert(
      selectedBox.left - ringOutside >= resultsBox.left &&
        selectedBox.right + ringOutside <= resultsBox.right &&
        selectedBox.top - ringOutside >= resultsBox.top &&
        selectedBox.bottom + ringOutside <= resultsBox.bottom,
      'the entire selected-result focus ring fits inside the scroll area: ' +
        JSON.stringify({
          selected: selectedBox,
          area: resultsBox,
          ringOutside,
        }),
    );
    $('search-results').dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowUp',
        bubbles: true,
        cancelable: true,
      }),
    );
    assert(
      document.activeElement === $('search'),
      'up arrow from the first result returns to the query',
    );
    search('path:/feature/src status:working');
    assert(
      $('search-count').textContent === '1 match',
      'working directory and live status are searchable',
    );
    search('kind:opencode worktree:/api/feature');
    assert(
      $('search-count').textContent === '1 match' && searchRows.has('a:502'),
      'kind and worktree filters retain normalized identity and worktree semantics',
    );
    search('pid:504 status:quiet');
    assert(
      searchRows.has('a:504') && searchRows.size === 1,
      'visible quiet status is searchable',
    );
    search('pid:504 status:waiting');
    assert(
      searchRows.has('a:504') && searchRows.size === 1,
      'legacy waiting status remains a compatible search alias',
    );
    search('dev:%42');
    assert(
      $('search-count').textContent === '1 match',
      'a colon in a tmux target remains literal text',
    );
    search('pid:50');
    assert(
      !$('search-message').hidden &&
        $('search-message').textContent.includes('No matching agents') &&
        $('empty').hidden,
      'PID filters are exact and no matches differs from no agents',
    );
    search('pid:502');
    assert(
      $('search-count').textContent === '1 match' &&
        rows.get('a:502').node === cachedNode,
      'typing reuses existing row nodes',
    );
    show([
      coordinator,
      { ...cacheTask, title: 'Trace request headers' },
      documentation,
      sibling,
    ]);
    search('cache invalidation');
    assert(
      !$('search-message').hidden && searchRows.size === 0,
      'updated snapshots replace old searchable metadata',
    );
    $('search').focus();
    $('search-panel').dispatchEvent(new Event('cancel', { cancelable: true }));
    assert(
      $('search').value === '' &&
        !$('search-panel').open &&
        $('search-panel').hidden &&
        document.activeElement === $('search-toggle') &&
        groups.get('r:/api').kids.hidden,
      'native Escape cancellation clears and closes search and returns keyboard focus',
    );
    $('search-toggle').click();
    search('agent:codex');
    $('search').dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    );
    assert(
      $('search-panel').hidden &&
        $('search').value === '' &&
        document.activeElement === $('search-toggle'),
      'Escape key closes search even when the engine does not emit dialog cancel',
    );
    $('expand').focus();
    document.body.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'k',
        ctrlKey: macPlatform,
        metaKey: !macPlatform,
        bubbles: true,
        cancelable: true,
      }),
    );
    assert(
      $('search-panel').hidden,
      'the other platform modifier does not hijack native shortcuts',
    );
    document.body.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'k',
        ctrlKey: !macPlatform,
        metaKey: macPlatform,
        bubbles: true,
        cancelable: true,
      }),
    );
    assert(
      document.activeElement === $('search') && !$('search-panel').hidden,
      'Ctrl/Cmd K opens and focuses search',
    );
    $('search-panel').dispatchEvent(
      new MouseEvent('click', { clientX: 0, clientY: 0, bubbles: true }),
    );
    assert(
      $('search-panel').hidden && document.activeElement === $('expand'),
      'closing search restores its previous keyboard focus',
    );
    $('search-toggle').click();
    assert(
      !$('search-panel').hidden && document.activeElement === $('search'),
      'mouse and touch users can open search',
    );

    search('no-such-task');
    const beforeHiddenNotification = notifications;
    show([{ ...coordinator, recentCpu: 0.1, idleSec: 0 }]);
    show([{ ...coordinator, idleSec: 20 }]);
    assert(
      !$('quiet').hidden &&
        document.title === '(1) recently quiet · Swarmview' &&
        notifications === beforeHiddenNotification + 1,
      'the palette does not filter or suppress global quiet observations, badges, or notifications',
    );
    $('clear-search').click();
    assert(
      !$('quiet').hidden &&
        $('search').value === '' &&
        !$('search-panel').hidden,
      'clear preserves quiet cards and keeps search open',
    );
    search('task:Coordinator');
    $('close-search').click();
    assert(
      $('search-panel').hidden &&
        $('search').value === '' &&
        $('search-toggle').getAttribute('aria-expanded') === 'false',
      'Done closes and clears search without needing Escape',
    );
    assert(
      groups.get('r:/api').kids.hidden && searchIndex.size === 0,
      'Done restores folds and releases the search index',
    );

    const shortQueryTasks = [
      agent(601, '/Users/dev/alpha', {
        repoName: 'alpha',
        title: 'Build login',
      }),
      agent(602, '/Users/dev/beta', {
        repoName: 'beta',
        title: 'Write tests',
        kind: 'opencode',
      }),
    ];
    show(shortQueryTasks);
    show(shortQueryTasks);
    search('se');
    assert(
      searchRows.size === 0 && $('search-count').textContent === '0 matches',
      'plain short queries do not accidentally match hidden /Users paths',
    );
    search('path:users');
    assert(
      searchRows.size === 2,
      'explicit path filters still search full working directories',
    );
    search('alpha');
    assert(
      searchRows.has('a:601') && searchRows.size === 1,
      'plain queries include visible repository labels',
    );

    show([cacheTask]);
    show([cacheTask]);
    search('pid:502');
    const result = searchRows.get('a:502'),
      originalFetch = window.fetch;
    let openedPID = null;
    try {
      window.fetch = async () => ({
        ok: false,
        json: async () => ({ error: 'Focus unavailable' }),
      });
      await result.btn.onclick();
      assert(
        $('search-panel').open &&
          result.note.textContent === 'Focus unavailable',
        'failed activation keeps its error visible inside the palette',
      );
      window.fetch = async (url, options) => {
        openedPID = JSON.parse(options.body).pid;
        return { ok: true };
      };
      await result.btn.onclick();
      assert(
        openedPID === 502 && !$('search-panel').open && searchRows.size === 0,
        'successful result activation uses the correct PID and closes/releases the palette',
      );
    } finally {
      window.fetch = originalFetch;
    }

    const many = Array.from({ length: 1000 }, (_, i) =>
      agent(10000 + i, '/project-' + (i % 50), {
        ppid: 0,
        title: 'Task ' + i,
        canFocus: true,
      }),
    );
    show(many);
    show(many);
    search('agent:codex');
    assert(
      searchRows.size === SEARCH_LIMIT &&
        $('search-count').textContent.startsWith('1000 matches'),
      'large result sets render at most 20 rows, not the whole fleet',
    );
    const largeSetNode = rows.get('a:10867').node;
    let searchRequests = 0;
    const previousFetch = window.fetch;
    try {
      window.fetch = async () => {
        searchRequests++;
        throw new Error('search must stay local');
      };
      const start = performance.now();
      for (let i = 0; i < 40; i++)
        search(
          i % 2 ? 'repo:project-17 "task 867"' : 'repo:project-18 "task 918"',
        );
      document.body.dataset.searchMs = (
        (performance.now() - start) /
        40
      ).toFixed(2);
      assert(
        rows.size === 1000 &&
          rows.get('a:10867').node === largeSetNode &&
          !largeSetNode.hidden,
        'large-set searches preserve the existing DOM',
      );
      assert(
        $('search-count').textContent === '1 match' &&
          searchRows.has('a:10867') &&
          searchRows.size === 1 &&
          [...rows.values()].every((r) => !r.node.hidden),
        '1,000-agent search narrows palette results without altering dashboard rows',
      );
      assert(searchRequests === 0, 'typing never requests the server');
    } finally {
      window.fetch = previousFetch;
    }
    $('clear-search').click();
    assert(
      searchIndex.size === 0,
      'clearing search releases the metadata index',
    );
    show(many.slice(0, 1));
    show(many.slice(0, 1));
    assert(
      rows.size === 1 && groups.size === 1,
      'exited agents and their search metadata are discarded',
    );
    $('close-search').click();
    assert(
      $('search-panel').hidden &&
        !$('search-panel').open &&
        $('search').value === '' &&
        searchRows.size === 0 &&
        searchState.searchReturnFocus === null,
      'closing removes temporary results, releases old focus references, and leaves no hidden query',
    );

    const fallbackHost = agent(100, '/fallback', {
      kind: 'claude',
      canFocus: true,
      tmuxAttached: true,
      tmuxTarget: 'synthetic:%1',
    });
    const survivingWorker = {
      id: 'survivor',
      parentPid: 100,
      parentAgentId: 'finished-parent',
      cwd: '/fallback',
      description: 'Surviving worker',
      working: true,
    };
    const finishedParent = {
      id: 'finished-parent',
      parentPid: 100,
      cwd: '/fallback',
      description: 'Temporary parent',
      working: true,
    };
    const fallbackSnapshot = {
      agents: [fallbackHost],
      subagents: [survivingWorker],
    };
    render(fallbackSnapshot);
    render(fallbackSnapshot);
    $('expand').click();
    const fallbackRoot = rows.get('a:100'),
      survivor = rows.get('sub:survivor');
    assert(
      survivor.node.parentElement === fallbackRoot.kids &&
        survivor.name.getBoundingClientRect().left >
          fallbackRoot.name.getBoundingClientRect().left,
      'worker with an absent declared parent is placed and indented under its effective top-level parent',
    );
    assert(
      fallbackRoot.fold.count === 1 &&
        /\b1 worker\b/.test(fallbackRoot.workers.textContent),
      'fallback worker contributes to its effective parent branch count and control',
    );
    assert(
      Math.abs(
        survivor.st.getBoundingClientRect().right -
          fallbackRoot.st.getBoundingClientRect().right,
      ) <= 1,
      'fallback indentation does not move the state column',
    );
    const fallbackFetch = window.fetch;
    let fallbackFocusRequests = 0,
      fallbackFocusURL = null,
      fallbackFocusMethod = null,
      fallbackFocusBody = null;
    try {
      window.fetch = async (url, options) => {
        fallbackFocusRequests++;
        fallbackFocusURL = url;
        fallbackFocusMethod = options?.method;
        fallbackFocusBody = options?.body;
        return { ok: true };
      };
      branchOf(fallbackRoot).click();
      assert(
        fallbackRoot.kids.hidden &&
          branchOf(fallbackRoot).textContent === 'Show 1 worker' &&
          fallbackRoot.workers.textContent.includes('1 hidden worker') &&
          branchOf(fallbackRoot).getAttribute('aria-expanded') === 'false',
        'fallback worker can be hidden with an accurate count',
      );
      assert(
        document.getElementById(
          branchOf(fallbackRoot).getAttribute('aria-controls'),
        ) === fallbackRoot.kids,
        'fallback branch control addresses its effective children accessibly',
      );
      branchOf(fallbackRoot).click();
      assert(
        !fallbackRoot.kids.hidden &&
          branchOf(fallbackRoot).textContent === 'Hide 1 worker',
        'fallback worker can be shown again',
      );
      closeDetails();
      assert(
        fallbackFocusRequests === 0,
        'fallback branch controls never request terminal focus',
      );
      assert(
        (await survivor.btn.onclick()) === true,
        'fallback worker terminal activation succeeds',
      );
      assert(
        fallbackFocusURL === '/focus' && fallbackFocusMethod === 'POST',
        'worker focus uses the terminal endpoint',
      );
      assert(
        fallbackFocusRequests === 1 &&
          JSON.parse(fallbackFocusBody).pid === 100,
        'fallback worker terminal action targets its actual top-level agent exactly once',
      );
      const fallbackElements = $('tree').querySelectorAll('*').length;
      for (let i = 0; i < 10; i++) render(fallbackSnapshot);
      assert(
        rows.get('sub:survivor') === survivor &&
          $('tree').querySelectorAll('*').length === fallbackElements &&
          fallbackFocusRequests === 1,
        'fallback snapshots reuse bounded DOM without adding focus requests',
      );
      render({
        agents: [fallbackHost],
        subagents: [finishedParent, survivingWorker],
      });
      const temporaryParent = rows.get('sub:finished-parent');
      assert(
        survivor.node.parentElement === temporaryParent.kids &&
          survivor.name.getBoundingClientRect().left >
            temporaryParent.name.getBoundingClientRect().left,
        'a present declared parent owns the worker and its nested indentation',
      );
      render(fallbackSnapshot);
      assert(
        temporaryParent.node.classList.contains('s-exiting') &&
          temporaryParent.btn.disabled &&
          survivor.node.parentElement === temporaryParent.kids,
        'disappeared parent is retained as exiting for one snapshot without losing its worker',
      );
      render(fallbackSnapshot);
      assert(
        rows.size === 2 &&
          !rows.has('sub:finished-parent') &&
          !temporaryParent.node.isConnected &&
          survivor.node.parentElement === fallbackRoot.kids,
        'worker moves to its effective parent when the exiting declared parent is removed without leaking rows',
      );
      assert(
        survivor.name.getBoundingClientRect().left >
          fallbackRoot.name.getBoundingClientRect().left &&
          fallbackRoot.fold.count === 1 &&
          /\b1 worker\b/.test(fallbackRoot.workers.textContent),
        'removed-parent fallback restores indentation and the single-worker branch count',
      );
      branchOf(fallbackRoot).click();
      assert(
        fallbackRoot.kids.hidden &&
          branchOf(fallbackRoot).textContent === 'Show 1 worker',
        'reparented worker remains foldable after its declared parent is removed',
      );
      branchOf(fallbackRoot).click();
      closeDetails();
      assert(
        !fallbackRoot.kids.hidden && fallbackFocusRequests === 1,
        'reparented worker shows again without accidental terminal actions',
      );
    } finally {
      window.fetch = fallbackFetch;
    }

    const providers = [
      'claude',
      'codex',
      'opencode',
      'gemini',
      'aider',
      'cursor-agent',
    ];
    const fleet = providers.map((kind, i) =>
      agent(20000 + i, '/synthetic', {
        kind,
        title: 'Task ' + kind,
        recentCpu: 0.2,
        memBytes: 2 * GB,
      }),
    );
    const nested = Array.from({ length: 5 }, (_, i) => ({
      id: 'level-' + i,
      parentPid: 20000,
      parentAgentId: i ? 'level-' + (i - 1) : null,
      cwd: '/synthetic',
      description:
        i === 4
          ? 'Investigate long nested task titles and preserve every word while keeping state and memory aligned across the whole tree'
          : 'Level ' + (i + 1),
      working: true,
    }));
    const workers = Array.from({ length: 20 }, (_, i) => ({
      id: 'worker-' + i,
      parentPid: 20000,
      parentAgentId: 'level-4',
      cwd: '/synthetic',
      description: 'Worker ' + i,
      working: i % 2 === 0,
    }));
    const treeSnapshot = { agents: fleet, subagents: [...nested, ...workers] };
    render(treeSnapshot);
    render(treeSnapshot);
    $('expand').click();
    for (let i = 0; i < providers.length; i++) {
      const r = rows.get('a:' + (20000 + i));
      assert(
        r.logo.textContent || r.logo.querySelector('svg path'),
        providers[i] + ' keeps its provider icon or monogram',
      );
      assert(
        r.agentKind.textContent,
        providers[i] + ' keeps a readable provider label',
      );
      assert(
        !r.mem.classList.contains('warn') && !r.mem.classList.contains('crit'),
        'large per-agent RAM stays neutral',
      );
    }
    const reference = rows.get('a:20000'),
      referenceState = reference.st.getBoundingClientRect(),
      referenceRAM = reference.mem.getBoundingClientRect();
    for (const r of rows.values()) {
      const state = r.st.getBoundingClientRect(),
        ram = r.mem?.getBoundingClientRect();
      assert(
        Math.abs(state.right - referenceState.right) <= 1 &&
          (!ram || Math.abs(ram.right - referenceRAM.right) <= 1),
        'available state and RAM columns align across providers, five levels, and 20 workers',
      );
      assert(
        r.btn.getBoundingClientRect().right <= window.innerWidth &&
          r.name.getBoundingClientRect().left >= 0 &&
          r.name.getBoundingClientRect().right <= state.left,
        'nested identity stays inside the viewport without overlapping metrics',
      );
    }
    const deepest = rows.get('sub:level-4'),
      firstNested = rows.get('sub:level-0');
    assert(
      deepest.name.getBoundingClientRect().left >
        firstNested.name.getBoundingClientRect().left,
      'nesting still indents task identity',
    );
    assert(
      deepest.name.getBoundingClientRect().height >
        parseFloat(getComputedStyle(deepest.name).lineHeight),
      'long nested task title wraps rather than truncates',
    );
    assert(
      !deepest.mem && deepest.item.mem === null,
      'unavailable worker RAM has no fabricated zero or idle metric DOM',
    );
    showDetails(deepest);
    assert(
      $('detail-body').textContent.includes('CPU: unavailable') &&
        $('detail-body').textContent.includes('Process age: unavailable'),
      'worker details never invent CPU or process age',
    );
    closeDetails();
    assert(
      document.documentElement.scrollWidth <= window.innerWidth,
      'expanded five-level tree with 20 workers never causes horizontal overflow',
    );
    assert(
      $('tree').getAnimations({ subtree: true }).length === 0,
      'CPU activity and nesting do not create communication pulses or a continuously animated tree',
    );
    const checkOverviewWidth = () => {
      const root = document.documentElement,
        body = document.body.getBoundingClientRect(),
        tree = $('tree').getBoundingClientRect(),
        style = getComputedStyle(document.body);
      const left = parseFloat(style.paddingLeft),
        right = parseFloat(style.paddingRight),
        width = Math.min(680, root.clientWidth);
      const geometry = JSON.stringify({
        viewport: [innerWidth, innerHeight],
        usable: root.clientWidth,
        body,
        tree,
        maxWidth: style.maxWidth,
        padding: [left, right],
        scrollWidth: root.scrollWidth,
      });
      assert(
        style.boxSizing === 'border-box' &&
          parseFloat(style.maxWidth) === 680 &&
          left === (innerWidth <= 480 ? 16 : 24) &&
          right === left,
        'overview retains its declared maximum and responsive padding: ' +
          geometry,
      );
      assert(
        Math.abs(body.width - width) <= 1 &&
          Math.abs(body.left - (root.clientWidth - width) / 2) <= 1,
        'overview body exactly fills the usable viewport up to 680px and stays centered: ' +
          geometry,
      );
      assert(
        Math.abs(tree.width - (width - left - right)) <= 1 &&
          Math.abs(tree.left - (body.left + left)) <= 1 &&
          Math.abs(tree.right - (body.right - right)) <= 1,
        'overview tree exactly fills the bounded body content box: ' + geometry,
      );
      assert(
        root.scrollWidth <= root.clientWidth,
        'overview has no horizontal overflow into the scrollbar gutter: ' +
          geometry,
      );
    };
    checkOverviewWidth();
    // Classic scrollbars consume layout width; overlay scrollbars do not. Test both without resizing.
    const scrollbarFixture = new CSSStyleSheet(),
      originalViewport = [innerWidth, innerHeight];
    scrollbarFixture.replaceSync(
      'html { overflow-y: scroll; scrollbar-gutter: stable; } ' +
        'html::-webkit-scrollbar { width: 17px; }',
    );
    document.adoptedStyleSheets = [
      ...document.adoptedStyleSheets,
      scrollbarFixture,
    ];
    try {
      assert(
        innerWidth === originalViewport[0] &&
          innerHeight === originalViewport[1] &&
          document.documentElement.clientWidth < innerWidth,
        'forced classic scrollbar reserves a nonzero gutter without changing the content viewport: ' +
          JSON.stringify({
            viewport: [innerWidth, innerHeight],
            usable: document.documentElement.clientWidth,
          }),
      );
      checkOverviewWidth();
    } finally {
      document.adoptedStyleSheets = document.adoptedStyleSheets.filter(
        (s) => s !== scrollbarFixture,
      );
    }
    if (window.innerWidth >= 560)
      assert(
        reference.btn.getBoundingClientRect().height <= 55,
        'short native rows remain compact',
      );
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) {
      assert(
        [...rows.values()].every(
          (r) => !r.logo || getComputedStyle(r.logo).animationName === 'none',
        ),
        'reduced motion disables working-icon animation',
      );
    }
    const stableNode = deepest.node,
      stableElements = $('tree').querySelectorAll('*').length;
    for (let i = 0; i < 20; i++) render(treeSnapshot);
    assert(
      rows.size === 31 &&
        rows.get('sub:level-4').node === stableNode &&
        $('tree').querySelectorAll('*').length === stableElements,
      'repeated snapshots reuse rows without DOM growth',
    );
    branchOf(deepest).click();
    assert(
      deepest.kids.hidden &&
        branchOf(deepest).getAttribute('aria-expanded') === 'false' &&
        deepest.workers.textContent.includes('20 hidden workers'),
      'worker branch folds independently and keeps the hidden count',
    );
    assert(
      !firstNested.kids.hidden && !groups.get('r:/synthetic').kids.hidden,
      'folding one worker branch leaves ancestors expanded',
    );
    branchOf(deepest).click();
    closeDetails();
    assert(
      !deepest.kids.hidden,
      'worker branch can be reopened from its details',
    );
    const outerBranch = rows.get('sub:level-3');
    const branchFetch = window.fetch;
    let branchFocusRequests = 0;
    try {
      window.fetch = async () => {
        branchFocusRequests++;
        throw new Error('Branch controls must not focus a terminal');
      };
      branchOf(outerBranch).click();
      assert(
        outerBranch.kids.hidden &&
          outerBranch.workers.textContent.includes('21 hidden workers') &&
          branchOf(outerBranch).textContent === 'Show 21 workers',
        'collapsing one child with 20 grandchildren reports all 21 hidden workers, not just the direct child',
      );
      assert(
        /\b20 workers\b/.test(deepest.workers.textContent) &&
          /\b24 workers\b/.test(firstNested.workers.textContent) &&
          /\b25 workers\b/.test(reference.workers.textContent),
        'nested branch totals count each worker descendant once without including the branch itself',
      );
      assert(
        groups.get('r:/synthetic').counts.textContent.includes('25 workers'),
        'nested descendant totals do not double-count repository workers',
      );
      branchOf(outerBranch).click();
      closeDetails();
      assert(
        !outerBranch.kids.hidden &&
          !deepest.kids.hidden &&
          outerBranch.fold.label === 'Hide 21 workers',
        'reopening the outer branch preserves its expanded child and full descendant count',
      );
      const processChild = agent(20006, '/synthetic', {
        ppid: 20000,
        kind: 'codex',
        title: 'Nested process',
        recentCpu: 0.2,
      });
      render({ ...treeSnapshot, agents: [...fleet, processChild] });
      branchOf(reference).click();
      assert(
        reference.kids.hidden &&
          reference.workers.textContent.includes('26 hidden nested agents') &&
          branchOf(reference).textContent === 'Show 26 nested agents',
        'mixed process and worker descendants are not mislabeled as 26 workers',
      );
      assert(
        groups.get('r:/synthetic').counts.textContent.includes('7 working') &&
          groups.get('r:/synthetic').counts.textContent.includes('25 workers'),
        'mixed descendants preserve distinct process and worker repository counts',
      );
      branchOf(reference).click();
      closeDetails();
      render(treeSnapshot);
      render(treeSnapshot);
      assert(
        !rows.has('a:20006') &&
          /\b25 workers\b/.test(reference.workers.textContent) &&
          branchFocusRequests === 0,
        'removing a nested process restores worker-only totals without focus requests or retained rows',
      );
    } finally {
      window.fetch = branchFetch;
    }
    $('collapse').click();
    assert(
      groups.get('r:/synthetic').kids.hidden &&
        groups.get('r:/synthetic').counts.textContent.includes('6 working') &&
        groups.get('r:/synthetic').counts.textContent.includes('25 workers'),
      'collapsed repository preserves rolled-up agent and worker counts',
    );
    $('expand').click();
    assert(
      !groups.get('r:/synthetic').kids.hidden &&
        rows.get('sub:worker-19').node.isConnected,
      'expanding restores all workers',
    );
    show([fleet[0]]);
    show([fleet[0]]);
    assert(
      rows.size === 1 && groups.size === 1 && !$('tree').contains(stableNode),
      'departed workers and nested DOM are released',
    );

    for (const pressure of [null, 1, 2, 4]) {
      render({
        agents: [fleet[0]],
        system: { pressure, swapUsedBytes: 8 * GB, freePct: 1 },
      });
      assert(
        $('mem').classList.contains('warn') === (pressure === 2) &&
          $('mem').classList.contains('crit') === (pressure === 4),
        'system severity follows OS pressure, not low free RAM or existing swap',
      );
      if (pressure === 1)
        assert(
          !$('mem').textContent.includes('Pressure normal') &&
            $('mem').title.includes('Pressure normal') &&
            $('system-details').textContent.includes('Pressure normal'),
          'normal pressure keeps the summary compact and remains available in help and title',
        );
    }
    for (const [pressure, label] of [
      [1, 'normal'],
      [2, 'warning'],
      [4, 'critical'],
    ]) {
      render({ agents: [fleet[0]], system: { pressure } });
      assert(
        !$('mem').hidden && $('mem').textContent.includes('Pressure ' + label),
        'OS pressure is visible even when free RAM and swap are unavailable',
      );
    }
    show([agent(20000, '/synthetic')]);
    assert(
      rows.get('a:20000').mem.textContent.includes('—') &&
        $('mem').textContent.includes('unavailable'),
      'missing agent RAM and unavailable system telemetry stay distinct from zero',
    );
    show([agent(20000, '/synthetic', { recentCpu: null, idleSec: 30 })]);
    assert(
      rows.get('a:20000').st.textContent === 'unknown' &&
        rows.get('a:20000').item.cpu.includes('unavailable') &&
        $('quiet').hidden,
      'missing activity telemetry is unknown, never quiet or zero CPU',
    );

    const observations = Array.from({ length: 12 }, (_, i) =>
      agent(30000 + i, '/observations', { recentCpu: 0.2, idleSec: 0 }),
    );
    show(observations);
    show(observations);
    const quietObservations = observations.map((it, i) => ({
      ...it,
      recentCpu: 0,
      idleSec: 15 + i,
    }));
    const beforeQuiet = notifications;
    show(quietObservations);
    assert(
      $('quiet-list').children.length === 3 &&
        quietRows.size === 3 &&
        notifications === beforeQuiet + observations.length,
      'recent observation cards stay bounded while transitions notify once',
    );
    const observationElements = $('quiet-list').querySelectorAll('*').length;
    for (let i = 0; i < 20; i++) show(quietObservations);
    assert(
      $('quiet-list').querySelectorAll('*').length === observationElements &&
        notifications === beforeQuiet + observations.length,
      'quiet observations neither grow DOM nor repeat notifications',
    );
    show(observations.map((it) => ({ ...it, recentCpu: 0, idleSec: 301 })));
    assert(
      $('quiet').hidden &&
        quietRows.size === 0 &&
        document.title === 'Swarmview',
      'quiet observations expire after five minutes instead of becoming an alert queue',
    );
    const beforeStale = notifications,
      snapshotNow = Date.now();
    render({ agents: observations, at: snapshotNow });
    render({ agents: quietObservations, at: snapshotNow - 20000 });
    assert(
      $('quiet').hidden &&
        notifications === beforeStale &&
        [...rows.values()]
          .filter((r) => !r.node.classList.contains('s-exiting'))
          .every((r) => r.st.textContent === 'stale'),
      'stale snapshots cannot produce quiet observations, notifications, or live state labels',
    );
    const queryNow = Date.now;
    try {
      render({ agents: [a], at: snapshotNow });
      Date.now = () => snapshotNow + 16000;
      search('pid:100');
      assert(
        searchRows.get('a:100').meta.textContent === 'stale' &&
          searchRows.get('a:100').node.classList.contains('s-unknown'),
        'a query reads current freshness even before another poll or freshness update',
      );
      closeSearch();
    } finally {
      Date.now = queryNow;
    }
    show([]);
    show([]);
    assert(
      rows.size === 0 &&
        groups.size === 0 &&
        quietRows.size === 0 &&
        eps.size === 0,
      'exited fleets release rows, observations, and notification episode metadata',
    );

    const sourceHost = agent(44000, '/source-cases', {
      kind: 'opencode',
      recentCpu: 0.5,
      canFocus: true,
    });
    render({
      agents: [
        sourceHost,
        agent(44001, '/source-cases', {
          ppid: 44000,
          recentCpu: 0.2,
          memBytes: 38 * MB,
        }),
      ],
      subagents: [
        {
          id: 'known-metadata',
          parentPid: 44000,
          cwd: '/source-cases',
          working: true,
          model: 'Known metadata model',
        },
      ],
      liveWorkers: [
        {
          key: 'live:full-normalized:ses_actualsuffix',
          parent: 'a:44000',
          pid: 44000,
        },
      ],
      communication: true,
      at: Date.now(),
    });
    const sourceLive = rows.get('live:full-normalized:ses_actualsuffix'),
      sourceMetadata = rows.get('sub:known-metadata');
    assert(
      rows.get('a:44001').st.textContent === 'working' &&
        rows.get('a:44001').mem.textContent === '~38 MB' &&
        sourceMetadata.st.textContent === 'working' &&
        !sourceMetadata.mem &&
        sourceLive.item.status === 'unknown',
      'process-backed child measurements, known Claude metadata activity and unknown native live status remain distinct',
    );
    assert(
      sourceLive.name.textContent === 'Worker suffix' &&
        !sourceLive.logo &&
        !sourceLive.agentKind &&
        !sourceLive.target &&
        !sourceLive.st &&
        !sourceLive.mem &&
        !sourceLive.detail &&
        !sourceLive.cpu &&
        !$('detail-body').children.length &&
        !activeFeedback.size,
      'short actual ID suffix does not allocate idle provider, metric, helper, event or hidden Details DOM',
    );
    openSearch();
    $('search').value = 'ses_actualsuffix';
    applySearch();
    assert(
      searchRows.has(sourceLive.item.key),
      'full normalized live worker identity remains searchable despite its compact suffix',
    );
    closeSearch();
    showDetails(sourceLive);
    assert(
      $('detail-body').textContent.includes(
        'live:full-normalized:ses_actualsuffix',
      ) &&
        $('detail-body').textContent.includes('Immediate parent') &&
        $('detail-body').textContent.includes('main agent’s terminal') &&
        $('detail-body').textContent.includes('CPU: unavailable'),
      'live Details preserves full identity, immediate-parent/host-terminal meaning and unavailable telemetry',
    );
    closeDetails();
    showDetails(sourceMetadata);
    assert(
      $('detail-body').textContent.includes('Known metadata model') &&
        $('detail-body').textContent.includes('log-write activity, not CPU'),
      'Claude read-only lifecycle/model information is retained without claiming process measurements',
    );
    closeDetails();

    // Synthetic protocol frames only: no user provider, plugin, or agent is started.
    const liveHost = agent(45000, '/live-test', {
      kind: 'opencode',
      title:
        'Review the complete parent task label without losing terminal navigation or moving the aligned state and RAM columns',
      canFocus: true,
      tmuxAttached: true,
      tmuxTarget: 'synthetic:%45',
    });
    const liveWorkers = Array.from({ length: 25 }, (_, i) => ({
      key: 'live:test:' + i,
      parent: i > 0 && i < 5 ? 'live:test:' + (i - 1) : 'a:45000',
      pid: 45000,
    }));
    const liveSnapshot = {
      agents: [liveHost],
      liveWorkers,
      communication: true,
      at: Date.now(),
    };
    render(liveSnapshot);
    render(liveSnapshot);
    $('expand').click();
    window.scrollTo(0, 0);
    assert(
      rows.size === 26 && $('count').textContent.startsWith('1 agent ·'),
      'live workers do not fabricate process counts',
    );
    for (const w of liveWorkers) {
      const r = rows.get(w.key);
      assert(
        !r.st &&
          !r.mem &&
          !r.cpu &&
          !r.logo &&
          r.item.stText === 'unknown' &&
          r.item.mem === null &&
          r.item.cpu.includes('unavailable'),
        'native session workers never invent CPU, RSS or lifecycle status or allocate unavailable metric DOM',
      );
      assert(
        r.toggle.getBoundingClientRect().right ===
          rows.get('a:45000').toggle.getBoundingClientRect().right,
        'live five-level/20-worker Details stay aligned without dummy metrics',
      );
    }
    assert(
      document.querySelectorAll('.communication-note').length === 0 &&
        activeFeedback.size === 0,
      'worker appearance does not communicate or allocate idle badges',
    );
    const liveRow = rows.get('live:test:0'),
      liveParent = rows.get('a:45000');
    liveRow.name.textContent =
      'Keep this long worker title completely readable while static feedback occupies only the existing secondary context line';
    const beforeFeedback = [...rows.values()].map((r) => [
      r,
      r.btn.getBoundingClientRect().height,
      r.toggle.getBoundingClientRect().left,
      r.toggle.getBoundingClientRect().right,
    ]);
    const originalIdentity = [
      liveRow.item.agentName,
      liveRow.item.target,
      liveRow.name.textContent,
    ];
    liveParent.btn.focus();
    const beforeFocus = document.activeElement,
      cuePosition = liveRow.node.parentElement,
      cueScroll = scrollY;
    showFeedback('live:test:0', 'attempted');
    const activeSend = activeFeedback.get('live:test:0'),
      badge = activeSend.label;
    assert(
      activeFeedback.size === 1 &&
        badge.textContent === 'Attempted from parent · unconfirmed' &&
        communicationFeedback.textContent.includes('acceptance not confirmed'),
      'pre-admission attempt is explicitly uncertain static text',
    );
    showFeedback('live:test:0', 'sent');
    assert(
      activeFeedback.get('live:test:0') === activeSend &&
        badge.textContent === 'Sent from parent' &&
        communicationFeedback.textContent.includes('Sent to worker'),
      'confirmed send updates the same row badge and timer',
    );
    assert(
      communicationFeedback.textContent.includes(liveParent.name.textContent) &&
        communicationFeedback.textContent.includes(liveRow.name.textContent) &&
        communicationFeedback.getAttribute('aria-live') === 'polite',
      'polite announcement includes the full bounded parent and worker labels',
    );
    assert(
      document.activeElement === beforeFocus &&
        liveRow.btn.disabled === false &&
        liveRow.item.stText === 'unknown',
      'feedback never steals focus, changes terminal capability, or invents activity state',
    );
    assert(
      originalIdentity.every(
        (value, i) =>
          value ===
          [
            liveRow.item.agentName,
            liveRow.item.target,
            liveRow.name.textContent,
          ][i],
      ),
      'provider identity, target, and long primary title remain intact',
    );
    assert(
      getComputedStyle(liveRow.btn.parentElement).backgroundColor ===
        'rgba(0, 0, 0, 0)' &&
        getComputedStyle(liveRow.btn.parentElement).outlineStyle === 'none' &&
        !rows.get('live:test:1').context.textContent,
      'communication is plain text only, never row/subtree fill, outline or a sibling cue',
    );
    for (const [r, height, stateX, ramX] of beforeFeedback)
      assert(
        r.btn.getBoundingClientRect().height === height &&
          r.toggle.getBoundingClientRect().left === stateX &&
          r.toggle.getBoundingClientRect().right === ramX,
        'static feedback has no row-height or aligned-Details layout shift across five levels and 20 workers',
      );
    const badgeBox = badge.getBoundingClientRect(),
      contextBox = liveRow.context.getBoundingClientRect();
    assert(
      badgeBox.left >= contextBox.left &&
        badgeBox.right <= contextBox.right &&
        badgeBox.bottom <= contextBox.bottom + 0.5 &&
        liveRow.name.getBoundingClientRect().bottom <= contextBox.top,
      'badge stays in its secondary slot without covering title or metrics',
    );
    const contrast = (a, b) => {
      const light = (color) =>
        color
          .match(/[\d.]+/g)
          .slice(0, 3)
          .map(Number)
          .map((v) => v / 255)
          .map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
          .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
      const x = light(a),
        y = light(b);
      return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
    };
    const badgeStyle = getComputedStyle(badge),
      background = getComputedStyle(document.body).backgroundColor;
    assert(
      contrast(badgeStyle.color, background) >= 4.5 &&
        badgeStyle.backgroundColor === 'rgba(0, 0, 0, 0)' &&
        badgeStyle.borderLeftWidth === '0px' &&
        parseFloat(badgeStyle.fontSize) >= 13,
      'both themes keep contrast-qualified plain text without chips, stripes or microscopic type',
    );
    assert(
      !document.getAnimations().length &&
        getComputedStyle(communicationFeedback).width === '1px',
      'normal and reduced motion share static row feedback and a nonvisual announcement, not a viewport layer',
    );
    showFeedback('live:test:0', 'returned');
    assert(
      badge.textContent === 'Reply received by parent' &&
        contrast(getComputedStyle(badge).color, background) >= 4.5,
      'a genuinely different confirmation is never dropped and reply text meets contrast',
    );
    showFeedback('live:test:0', 'attempted');
    assert(
      badge.textContent === 'Attempted from parent · unconfirmed' &&
        contrast(getComputedStyle(badge).color, background) >= 4.5,
      'a new attempt is not misreported as the previous confirmation and uncertain text meets contrast',
    );
    assert(
      liveRow.node.parentElement === cuePosition &&
        scrollY === cueScroll &&
        !liveParent.kids.hidden,
      'cue appearance never reparents, scrolls or expands a branch',
    );
    showDetails(liveRow);
    assert(
      activeFeedback.size === 0,
      'opening shared Details drops active communication cues',
    );
    showFeedback('live:test:0', 'sent');
    assert(
      activeFeedback.size === 0,
      'Details-covered rows cannot admit or queue cues',
    );
    closeDetails();
    $('preferences-toggle').click();
    showFeedback('live:test:0', 'sent');
    assert(
      activeFeedback.size === 0 &&
        $('communication-capability').textContent.includes('Enabled by server'),
      'Preferences-covered rows discard cues and report real server capability',
    );
    $('close-preferences').click();
    // Capacity needs five visible candidates, independent of nested long-title wrapping/font metrics.
    const capacitySnapshot = {
      ...liveSnapshot,
      agents: [{ ...liveHost, title: 'Parent' }],
      liveWorkers: liveWorkers
        .slice(0, 5)
        .map((w) => ({ ...w, parent: 'a:45000' })),
    };
    const treeFont = $('tree').style.fontSize,
      longWorkerTitle = liveRow.name.textContent;
    for (const font of [treeFont, '17px']) {
      clearPulses();
      $('tree').style.fontSize = font;
      render(capacitySnapshot);
      window.scrollTo(0, 0);
      for (const w of capacitySnapshot.liveWorkers) {
        const box = rows.get(w.key).btn.getClientRects()[0];
        assert(
          box &&
            box.top >= 0 &&
            box.bottom <= innerHeight &&
            box.left >= 0 &&
            box.right <= innerWidth,
          'all five capacity candidates are fully visible at the actual content viewport, including larger font metrics',
        );
      }
      for (const w of capacitySnapshot.liveWorkers) showFeedback(w.key, 'sent');
      assert(
        activeFeedback.size === 4 &&
          document.querySelectorAll('.communication-note').length === 4,
        'active badges and timers are bounded at four without an idle-row journal',
      );
      assert(
        capacitySnapshot.liveWorkers
          .slice(0, 4)
          .every((w) => activeFeedback.has(w.key)) &&
          !activeFeedback.has('live:test:4'),
        'capacity admits the first four visible targets and rejects the fifth visible target',
      );
      const stressRow = rows.get('live:test:3');
      stressRow.context.style.fontSize = '17px';
      stressRow.context.style.lineHeight = '1.5';
      showFeedback('live:test:3', 'attempted');
      const stressCue = activeFeedback.get('live:test:3').label,
        cueBox = stressCue.getBoundingClientRect(),
        stressBox = stressRow.context.getBoundingClientRect();
      assert(
        stressCue.textContent.endsWith('· unconfirmed') &&
          cueBox.right <= stressBox.right + 0.5 &&
          cueBox.bottom <= stressBox.bottom + 0.5 &&
          getComputedStyle(stressRow.context).overflow === 'visible',
        'enlarged cue text wraps without clipping or shrinking the unconfirmed qualifier',
      );
      stressRow.context.style.fontSize = '';
      stressRow.context.style.lineHeight = '';
    }
    clearPulses();
    $('tree').style.fontSize = treeFont;
    render(liveSnapshot);
    window.scrollTo(0, 0);
    liveRow.name.textContent = longWorkerTitle;
    showFeedback('live:test:0', 'sent');
    for (let i = 1; i < 5; i++) showFeedback('live:test:' + i, 'sent');
    showFeedback('live:test:3', 'attempted');
    for (const entry of activeFeedback.values()) {
      const box = entry.label.getBoundingClientRect(),
        slot = entry.label.parentElement.getBoundingClientRect();
      assert(
        box.left >= slot.left &&
          box.right <= slot.right &&
          box.bottom <= slot.bottom + 0.5,
        'even the longest badge fits nested compact secondary slots without overlap',
      );
    }
    const parentLabel = liveParent.name.textContent,
      workerLabel = liveRow.name.textContent;
    liveParent.name.textContent = 'Parent '.repeat(24);
    liveRow.name.textContent = 'Worker '.repeat(24);
    // Long labels wrap the nested compact rows; feedback is only shown for an onscreen worker.
    liveRow.btn.scrollIntoView({ block: 'nearest' });
    showFeedback('live:test:0', 'returned');
    assert(
      communicationFeedback.textContent.length <= 400 &&
        communicationFeedback.textContent.includes('…'),
      'untrusted long display labels cannot grow a feedback announcement without bound',
    );
    liveParent.name.textContent = parentLabel;
    liveRow.name.textContent = workerLabel;
    window.scrollTo(0, 0);
    clearPulses();
    showFeedback('live:test:24', 'sent');
    assert(
      activeFeedback.size === 0,
      'offscreen feedback is discarded, not queued',
    );
    showFeedback('live:test:0', 'sent');
    const repeated = activeFeedback.get('live:test:0');
    showFeedback('live:test:0', 'sent');
    assert(
      activeFeedback.get('live:test:0') === repeated,
      'same-row burst reuses its entry and badge',
    );
    render(liveSnapshot);
    assert(
      activeFeedback.get('live:test:0') === repeated,
      'unchanged snapshot preserves readable feedback without allocating it again',
    );
    dispatchEvent(new Event('resize'));
    assert(
      activeFeedback.size === 0 &&
        !document.querySelector('.communication-note'),
      'resize cleans static feedback',
    );
    showFeedback('live:test:0', 'returned');
    openSearch();
    assert(
      activeFeedback.size === 0,
      'search dialog cancels covered-row feedback without changing focus behavior',
    );
    showFeedback('live:test:0', 'sent');
    assert(
      activeFeedback.size === 0,
      'covered rows do not queue feedback behind search',
    );
    closeSearch();
    showFeedback('live:test:0', 'returned');
    assert(
      communicationFeedback.textContent.includes('Reply received'),
      'returned result has distinct accessible text',
    );
    branchOf(rows.get('a:45000')).click();
    closeDetails();
    assert(activeFeedback.size === 0, 'fold clears active feedback');
    showFeedback('live:test:0', 'sent');
    assert(activeFeedback.size === 0, 'collapsed rows never queue feedback');
    branchOf(rows.get('a:45000')).click();
    closeDetails();
    showFeedback('live:test:0', 'returned');
    await new Promise((resolve) => setTimeout(resolve, 2600));
    assert(
      activeFeedback.size === 0 &&
        !document.querySelector('.communication-note') &&
        !document.querySelector('.communication-active') &&
        communicationFeedback.textContent === '',
      'feedback expiry removes badges, highlight, timers, and announcement without retaining an event',
    );

    // Claude hook cues mark existing metadata rows, only for the host that owns them.
    const claudeHost = agent(46000, '/claude-test', { kind: 'claude' }),
      claudeWorker = {
        id: 'a1234567',
        parentPid: 46000,
        cwd: '/claude-test',
        description: 'Claude hook worker',
        working: true,
      };
    const claudeSnapshot = {
      agents: [claudeHost],
      subagents: [claudeWorker],
      communication: true,
      at: Date.now(),
    };
    render({ ...claudeSnapshot, subagents: [] });
    window.scrollTo(0, 0);
    showFeedback('sub:a1234567', 'sent', 46000);
    assert(
      communication.pendingFeedback.size === 1 && activeFeedback.size === 0,
      'a Claude cue waits briefly for the row the next poll renders',
    );
    dispatchEvent(new Event('scroll'));
    assert(
      communication.pendingFeedback.size === 1,
      'scrolling keeps a waiting cue, which has no on-screen position yet',
    );
    render(claudeSnapshot);
    assert(
      activeFeedback.size === 1 &&
        document.querySelector('.communication-note').textContent ===
          'Sent from parent' &&
        communication.pendingFeedback.size === 0,
      'pending Claude cue shows once its worker row renders',
    );
    clearPulses();
    showFeedback('sub:a1234567', 'returned', 46001);
    assert(
      activeFeedback.size === 0 && communication.pendingFeedback.size === 0,
      'a cue from another Claude process never marks this worker',
    );
    showFeedback('sub:a1234567', 'sent');
    assert(
      activeFeedback.size === 0,
      'an OpenCode cue never marks a Claude metadata row',
    );
    showFeedback('sub:a1234567', 'message', 46000);
    assert(
      document.querySelector('.communication-note').textContent ===
        'Message sent to worker' &&
        communicationFeedback.textContent.startsWith(
          'Message sent to worker: Claude hook worker',
        ),
      'a follow-up message names only its recipient',
    );
    showFeedback('sub:a1234567', 'sent', 46000);
    assert(
      document.querySelector('.communication-note').textContent ===
        'Message sent to worker',
      'the worker restart a follow-up message causes keeps the message cue',
    );
    clearPulses();
    render(liveSnapshot);
    const previousEventSource = window.EventSource;
    let stream = null,
      opens = 0;
    window.EventSource = class {
      constructor() {
        stream = this;
        opens++;
      }
      close() {
        this.closed = true;
      }
    };
    try {
      communicationStream();
      assert(opens === 1, 'opt-in uses one stream');
      stream.onmessage({ data: JSON.stringify({ workers: liveWorkers }) });
      assert(
        activeFeedback.size === 0,
        'initial/reconnected state never replays feedback',
      );
      render(claudeSnapshot);
      showFeedback('sub:a1234567', 'sent', 46000);
      stream.onmessage({
        data: JSON.stringify({ workers: [], ambiguous: true }),
      });
      assert(
        activeFeedback.size === 1,
        'OpenCode ambiguity keeps a Claude cue, which names its own host',
      );
      clearPulses();
      // Two renders: removed rows stay one poll as exiting before they go.
      render(liveSnapshot);
      render(liveSnapshot);
      const stableWorkers = liveWorkers.slice(0, 20),
        routeHost = agent(45100, '/live-route', { kind: 'opencode' });
      render({
        ...liveSnapshot,
        agents: [liveHost, routeHost],
        liveWorkers: stableWorkers,
      });
      const work = {
          get renders() {
            return app.work.renders;
          },
          get rowUpdates() {
            return rowsState.work.calls;
          },
          get hosts() {
            return agentsState.work.calls;
          },
        },
        attributes = new MutationObserver(() => {});
      const originalAnimate = Element.prototype.animate,
        originalRAF = window.requestAnimationFrame,
        originalStyle = window.getComputedStyle,
        originalBounds = Element.prototype.getBoundingClientRect,
        originalRects = Element.prototype.getClientRects;
      const originalSetTimeout = window.setTimeout,
        originalClearTimeout = window.clearTimeout,
        feedbackTimers = new Set();
      let forbiddenWork = 0,
        visibilityReads = 0;
      const forbidden = () => {
        forbiddenWork++;
        throw Error(
          'Static feedback must not animate, schedule frames, or inspect travel geometry/styles',
        );
      };
      Element.prototype.animate =
        window.requestAnimationFrame =
        window.getComputedStyle =
        Element.prototype.getBoundingClientRect =
          forbidden;
      Element.prototype.getClientRects = function () {
        visibilityReads++;
        return originalRects.call(this);
      };
      window.setTimeout = (fn, delay, ...args) => {
        const id = originalSetTimeout(() => {
          feedbackTimers.delete(id);
          fn(...args);
        }, delay);
        feedbackTimers.add(id);
        return id;
      };
      window.clearTimeout = (id) => {
        feedbackTimers.delete(id);
        originalClearTimeout(id);
      };
      app.work.renders = rowsState.work.calls = agentsState.work.calls = 0;
      attributes.observe($('tree'), {
        subtree: true,
        attributes: true,
        childList: true,
      });
      try {
        let reused, steadyNodes;
        for (let i = 0; i < 36; i++) {
          const direction = i % 2 ? 'returned' : 'sent';
          stream.onmessage({
            data: JSON.stringify({
              workers: stableWorkers,
              key: stableWorkers[0].key,
              direction,
            }),
          });
          const entry = activeFeedback.get(stableWorkers[0].key);
          if (!i) {
            reused = entry;
            steadyNodes = $('tree').querySelectorAll('*').length;
          }
          assert(
            activeFeedback.size === 1 &&
              entry === reused &&
              entry.label.textContent ===
                (i % 2 ? 'Reply received by parent' : 'Sent from parent') &&
              communicationFeedback.textContent.includes(
                i % 2 ? 'Reply received' : 'Sent to worker',
              ),
            '36 unchanged worker frames present every confirmation using one reused badge/timer entry',
          );
          assert(
            $('tree').querySelectorAll('*').length === steadyNodes &&
              !document.getAnimations().length,
            'normal and reduced motion share stable static DOM without frame scheduling or Web Animations',
          );
          assert(
            feedbackTimers.size === 1,
            'updating an active row replaces its one expiry timer rather than accumulating timers',
          );
        }
        const allowedTargets = [
          reused.line,
          rows.get(stableWorkers[0].key).context,
          reused.label,
        ];
        const unrelated = attributes
          .takeRecords()
          .filter((record) => !allowedTargets.includes(record.target));
        assert(
          work.renders === 0 &&
            work.rowUpdates === 0 &&
            work.hosts === 0 &&
            unrelated.length === 0,
          '36 unchanged SSE frames perform no full render, updateAgentRow calls, host formatting or unrelated tree writes',
        );
        assert(
          forbiddenWork === 0 && visibilityReads === 36,
          'each observation uses only one row visibility read, with zero travel bounds, computed styles, WAAPI or RAF',
        );
        Element.prototype.animate = originalAnimate;
        window.requestAnimationFrame = originalRAF;
        window.getComputedStyle = originalStyle;
        Element.prototype.getBoundingClientRect = originalBounds;
        Element.prototype.getClientRects = originalRects;
        clearPulses();
        assert(
          feedbackTimers.size === 0,
          'explicit cleanup clears the final rescheduled timer',
        );
        window.setTimeout = originalSetTimeout;
        window.clearTimeout = originalClearTimeout;
        assert(
          activeFeedback.size === 0 &&
            !document.querySelector('.communication-note') &&
            !document.getAnimations().length,
          'unchanged-frame burst cleanup stays bounded without retained badges',
        );
        const frame = (workers) =>
          stream.onmessage({ data: JSON.stringify({ workers }) });
        let count = work.renders;
        frame([...stableWorkers].reverse());
        assert(
          work.renders === ++count &&
            app.lastData.liveWorkers[0].key === stableWorkers.at(-1).key,
          'changed worker order renders even when the same keys remain',
        );
        frame(stableWorkers);
        count++;
        const reparented = stableWorkers.map((w, i) =>
          i ? w : { ...w, parent: stableWorkers[5].key },
        );
        showDetails(rows.get(stableWorkers[0].key));
        frame(reparented);
        assert(
          work.renders === ++count &&
            rows.get(stableWorkers[0].key).node.parentElement ===
              rows.get(stableWorkers[5].key).kids,
          'same-length parent changes reparent live rows',
        );
        assert(
          !$('detail-dialog').open && !$('detail-body').children.length,
          'changed selected-worker parent identity closes shared Details instead of retaining a stale destination',
        );
        frame(stableWorkers);
        count++;
        const rerouted = stableWorkers.map((w, i) =>
          i ? w : { ...w, pid: routeHost.pid },
        );
        showDetails(rows.get(stableWorkers[0].key));
        frame(rerouted);
        assert(
          work.renders === ++count &&
            rows.get(stableWorkers[0].key).pid === routeHost.pid &&
            rows.get(stableWorkers[0].key).item.cwd.includes('/live-route'),
          'same-key owner changes refresh focus and presentation fields',
        );
        assert(
          !$('detail-dialog').open && !$('detail-body').children.length,
          'changed selected-worker owner PID closes shared Details and releases the old source',
        );
        frame(stableWorkers);
        count++;
        const replaced = stableWorkers.map((w, i) =>
          i ? w : { ...w, key: 'live:replacement:0' },
        );
        frame(replaced);
        assert(
          work.renders === ++count &&
            rows.has('live:replacement:0') &&
            !rows.has(stableWorkers[0].key),
          'same-length worker identity changes replace the correct row',
        );
        frame(stableWorkers);
        count++;
        const savedFetch = window.fetch,
          beforeHosts = work.hosts;
        try {
          window.fetch = async (url) => {
            assert(
              url === '/agents',
              'normal poll still uses the process snapshot endpoint',
            );
            return {
              ok: true,
              json: async () => ({
                ...app.lastData,
                agents: [
                  {
                    ...liveHost,
                    memBytes: 750 * MB,
                    title: 'Updated host task',
                  },
                  routeHost,
                ],
                at: Date.now(),
              }),
            };
          };
          await poll();
          clearTimeout(app.timer);
        } finally {
          window.fetch = savedFetch;
        }
        assert(
          work.renders === ++count &&
            rows.get('a:45000').mem.textContent === '~750 MB' &&
            rows.get('a:45000').name.textContent === 'Updated host task',
          'ordinary polls continue updating host RAM and task identity',
        );
        assert(
          work.hosts - beforeHosts === 2,
          'a poll formats each host once, not again for its 20 workers',
        );
        const realNow = Date.now;
        try {
          showFeedback(stableWorkers[0].key, 'sent');
          Date.now = () => app.lastData.at + 20000;
          frame(stableWorkers);
          assert(
            work.renders === count &&
              activeFeedback.size === 0 &&
              rows.get('a:45000').st.textContent === 'stale',
            'unchanged frames still age snapshots and clear stale feedback',
          );
        } finally {
          Date.now = realNow;
          freshness();
        }
        $('offline').hidden = false;
        frame(stableWorkers);
        assert(
          work.renders === count &&
            rows.get('a:45000').st.textContent === 'offline',
          'unchanged topology cannot mask offline state',
        );
        $('offline').hidden = true;
        freshness();
        showFeedback(stableWorkers[0].key, 'returned');
        stream.onmessage({
          data: JSON.stringify({
            workers: stableWorkers,
            lost: true,
            key: stableWorkers[0].key,
            direction: 'sent',
          }),
        });
        assert(
          work.renders === count &&
            activeFeedback.size === 0 &&
            communicationStatus.textContent.includes('dropped'),
          'unchanged topology still acknowledges loss without replaying feedback',
        );
        showFeedback(stableWorkers[0].key, 'returned');
        stream.onmessage({
          data: JSON.stringify({
            workers: stableWorkers,
            ambiguous: true,
            key: stableWorkers[0].key,
            direction: 'sent',
          }),
        });
        assert(
          work.renders === count &&
            activeFeedback.size === 0 &&
            communicationStatus.textContent.includes('ambiguous'),
          'unchanged topology still clears uncertain feedback and updates ambiguity availability',
        );
      } finally {
        Element.prototype.animate = originalAnimate;
        window.requestAnimationFrame = originalRAF;
        window.getComputedStyle = originalStyle;
        Element.prototype.getBoundingClientRect = originalBounds;
        Element.prototype.getClientRects = originalRects;
        window.setTimeout = originalSetTimeout;
        window.clearTimeout = originalClearTimeout;
        attributes.disconnect();
        clearPulses();
        $('offline').hidden = true;
      }
      render(liveSnapshot);
      stream.onmessage({
        data: JSON.stringify({ workers: [], ambiguous: true }),
      });
      assert(
        $('communication-status').textContent.includes('ambiguous') &&
          rows.size === 1,
        'ambiguous native viewers suppress worker attribution with an explicit availability reason',
      );
      stream.onmessage({
        data: JSON.stringify({ workers: liveWorkers, lost: true }),
      });
      assert(
        $('communication-status').textContent.includes('dropped') &&
          activeFeedback.size === 0,
        'dropped burst is acknowledged rather than replayed',
      );
      showFeedback('live:test:0', 'sent');
      stream.onerror();
      assert(
        activeFeedback.size === 0 &&
          $('communication-status').textContent.includes('missing'),
        'stream gap clears feedback and is acknowledged',
      );
      Object.defineProperty(document, 'hidden', {
        configurable: true,
        value: true,
      });
      communicationStream();
      assert(
        stream.closed && !communication.source,
        'hidden tab disconnects and discards observations',
      );
      showFeedback('live:test:0', 'sent');
      assert(activeFeedback.size === 0, 'hidden tab never queues feedback');
      delete document.hidden;
      const oldStream = stream;
      communicationStream();
      oldStream.onmessage({
        data: JSON.stringify({
          workers: liveWorkers,
          key: 'live:test:0',
          direction: 'sent',
        }),
      });
      assert(
        opens === 2 && activeFeedback.size === 0,
        'callbacks from a closed stream generation cannot replay after visibility resumes',
      );
      showFeedback('live:test:0', 'sent');
      render({ ...liveSnapshot, communication: false });
      communicationStream();
      assert(
        activeFeedback.size === 0 && !communication.source,
        'disabled capability clears feedback even if a retained graph is still supplied',
      );
      render({ agents: [liveHost], at: Date.now() });
      communicationStream();
      assert(
        opens === 2 && !communication.source && rows.size === 1,
        'disabled bridge has no connection and releases live worker rows immediately',
      );
      render(liveSnapshot);
      communicationStream();
      assert(opens === 3, 'enabled bridge reconnects');
      const refused = stream;
      refused.readyState = 2;
      refused.onerror();
      communicationStream(); // EventSource.CLOSED, e.g. 503 viewer limit
      assert(
        opens === 4 && stream !== refused && communication.source === stream,
        'a closed (non-200) stream reconnects on the next poll instead of staying disconnected',
      );
    } finally {
      delete document.hidden;
      communication.closeSource();
      window.EventSource = previousEventSource;
      clearPulses();
    }

    const detailA = agent(46000, '/details-review', {
      title: 'Details A',
      recentCpu: 0.5,
      memBytes: 123 * MB,
      canFocus: true,
      tmuxAttached: true,
      tmuxTarget: 'review:%1',
    });
    const detailB = agent(46001, '/details-review', {
      title: 'Details B',
      recentCpu: 0.2,
      memBytes: 456 * MB,
      canFocus: true,
      tmuxAttached: true,
      tmuxTarget: 'review:%2',
    });
    render({ agents: [detailA, detailB], at: Date.now() - 30000 });
    const rowA = rows.get('a:46000'),
      rowB = rows.get('a:46001');
    rowA.toggle.click();
    assert(
      $('detail-body').textContent.includes(
        'stale · retained, not live state',
      ) &&
        $('detail-body').textContent.includes('CPU 50%') &&
        $('detail-body').textContent.includes('~123 MB'),
      'opening already-stale Details immediately labels retained activity without replacing valid cached measurements',
    );
    closeDetails();
    $('offline').hidden = false;
    freshness();
    rowA.toggle.click();
    assert(
      $('detail-body').textContent.includes(
        'offline · retained, not live state',
      ) &&
        $('detail-body').textContent.includes('CPU 50%') &&
        $('detail-body').textContent.includes('~123 MB'),
      'opening already-offline Details immediately labels retained activity without another freshness pass',
    );
    closeDetails();
    $('offline').hidden = true;
    render({ agents: [detailA, detailB], at: Date.now() });
    const detailsNow = Date.now;
    try {
      const agedNow = Date.now() + 16000;
      Date.now = () => agedNow;
      rowA.toggle.click();
      assert(
        $('detail-body').textContent.includes(
          'stale · retained, not live state',
        ),
        'Details reads the current snapshot age even when it becomes stale between coordinator freshness passes',
      );
      closeDetails();
    } finally {
      Date.now = detailsNow;
    }
    const detailsFetch = window.fetch;
    try {
      for (const outcome of ['success', 'failure'])
        for (const replacement of [rowB, rowA]) {
          let resolveFocus;
          window.fetch = (url, options) => {
            assert(
              url === '/focus' &&
                options.method === 'POST' &&
                JSON.parse(options.body).pid === detailA.pid,
              'deferred Details requests retain the explicitly selected terminal PID',
            );
            return new Promise((resolve) => {
              resolveFocus = resolve;
            });
          };
          rowA.toggle.click();
          const pending = $('detail-terminal').onclick();
          $('detail-dialog').dispatchEvent(
            new KeyboardEvent('keydown', {
              key: 'Escape',
              bubbles: true,
              cancelable: true,
            }),
          );
          replacement.toggle.click();
          $('detail-terminal').focus();
          const currentBody = $('detail-body').firstChild,
            currentFocus = document.activeElement,
            currentError = $('detail-error').textContent;
          resolveFocus({
            ok: outcome === 'success',
            json: async () => ({
              error: 'Delayed error for ' + replacement.item.name,
            }),
          });
          await pending;
          assert(
            $('detail-dialog').open &&
              $('detail-title').textContent === replacement.item.name &&
              $('detail-body').firstChild === currentBody &&
              document.activeElement === currentFocus &&
              $('detail-error')?.textContent === currentError,
            'old terminal ' +
              outcome +
              ' cannot change ' +
              (replacement === rowA ? 'same-row reopened' : 'replacement-row') +
              ' Details, its error content or its focus',
          );
          closeDetails();
        }
      window.fetch = async () => ({ ok: true });
      rowA.toggle.click();
      await $('detail-terminal').onclick();
      assert(
        !$('detail-dialog').open &&
          !$('detail-body').children.length &&
          document.activeElement === rowA.toggle,
        'a terminal success still closes and returns focus for its own current Details selection',
      );
    } finally {
      window.fetch = detailsFetch;
      closeDetails();
      $('offline').hidden = true;
    }
    const connectorHost = agent(47000, '/connector-root', {
      kind: 'claude',
      title: 'Connector parent',
    });
    const connectorProcesses = Array.from({ length: 8 }, (_, i) =>
      agent(47001 + i, '/connector-root', {
        ppid: 47000 + i,
        kind: i % 2 ? 'claude' : 'codex',
        title: 'Process child ' + i,
      }),
    );
    const connectorMetadata = Array.from({ length: 8 }, (_, i) => ({
      id: 'connector-claude-' + i,
      parentPid: 47000,
      parentAgentId: i ? 'connector-claude-' + (i - 1) : null,
      cwd: '/connector-root',
      description: 'Claude worker ' + i,
      working: true,
    }));
    const connectorLiveHost = agent(48000, '/connector-branches', {
      kind: 'opencode',
      title: 'Live connector parent',
      cwd: '/connector-branches/feature',
      worktree: '/connector-branches/feature',
    });
    const connectorLivePrefix =
      'live:48000:' + 'a'.repeat(64) + ':2.1.3:ses_connector-';
    const connectorLive = Array.from({ length: 8 }, (_, i) => ({
      key: connectorLivePrefix + i,
      parent: i ? connectorLivePrefix + (i - 1) : 'a:48000',
      pid: 48000,
    }));
    const connectorSnapshot = {
      agents: [connectorHost, ...connectorProcesses, connectorLiveHost],
      subagents: connectorMetadata,
      liveWorkers: connectorLive,
      communication: true,
      at: Date.now(),
    };
    render(connectorSnapshot);
    render(connectorSnapshot);
    $('expand').click();
    assert(
      rows.size === 26 &&
        rows.get('a:47000').node.parentElement ===
          groups.get('r:/connector-root').kids &&
        rows.get('a:48000').node.parentElement ===
          groups.get('w:/connector-branches/feature').kids,
      'connector fixtures cover all 26 rows and both repository and worktree root junctions',
    );
    for (let i = 0; i < 8; i++) {
      for (const [key, parent] of [
        ['a:' + (47001 + i), 'a:' + (47000 + i)],
        [
          'sub:connector-claude-' + i,
          i ? 'sub:connector-claude-' + (i - 1) : 'a:47000',
        ],
        [connectorLive[i].key, connectorLive[i].parent],
      ]) {
        assert(
          rows.get(key).node.parentElement === rows.get(parent).kids &&
            +rows.get(key).node.style.getPropertyValue('--depth') ===
              Math.min(i + 1, 4),
          'all eight real process/metadata/live levels keep their immediate parent while visual indentation caps at four',
        );
      }
    }
    const connectorJunctions = [];
    for (const [key, row] of rows) {
      const kids = row.node.parentElement,
        trunk = getComputedStyle(kids, '::before'),
        stub = getComputedStyle(row.btn, '::before');
      assert(
        row.btn.getClientRects().length &&
          !row.node.closest('[hidden]') &&
          trunk.content !== 'none' &&
          parseFloat(trunk.borderLeftWidth) === 1 &&
          parseFloat(stub.borderTopWidth) === 1,
        'expanded connector junctions have real visible one-pixel structural strokes',
      );
      const trunkRight =
        kids.getBoundingClientRect().left +
        parseFloat(trunk.left) +
        parseFloat(trunk.borderLeftWidth);
      const btnRect = row.btn.getBoundingClientRect();
      const stubLeft = btnRect.left + parseFloat(stub.left),
        stubWidth = parseFloat(stub.width);
      const isWorker = row.node.classList.contains('worker');
      if (isWorker) {
        const slotLeft =
          btnRect.left + parseFloat(getComputedStyle(row.btn).paddingLeft) - 28;
        const dot = getComputedStyle(row.btn, '::after'),
          dotLeft = btnRect.left + parseFloat(dot.left),
          dotCenterY = btnRect.top + parseFloat(dot.top) + 3.5,
          stubY = btnRect.top + parseFloat(stub.top) + 0.5,
          titleLine = row.name.getClientRects()[0],
          titleCenterY = titleLine.top + titleLine.height / 2;
        const detail = JSON.stringify({
          key,
          dotLeft,
          dotCenterY,
          slotLeft,
          stubY,
          stubWidth,
          titleCenterY,
        });
        assert(
          dot.content !== 'none' && parseFloat(dot.width) === 7,
          'worker rows draw a 7px node dot: ' + detail,
        );
        assert(
          dotLeft >= slotLeft && dotLeft + 7 <= slotLeft + 20,
          'the node dot sits inside the empty logo slot: ' + detail,
        );
        assert(
          Math.abs(dotCenterY - stubY) <= 0.25,
          'the node dot is centered on its branch: ' + detail,
        );
        // The title's line box depends on the platform font, so allow a pixel.
        assert(
          Math.abs(dotCenterY - titleCenterY) <= 1,
          'the node dot is centered on the title text: ' + detail,
        );
        assert(
          stubWidth >= 4 && Math.abs(dotLeft - (stubLeft + stubWidth)) <= 1,
          'worker branches run into their node dot: ' + detail,
        );
        if (row.node.classList.contains('s-working')) {
          assert(
            dot.borderTopColor === stub.borderTopColor,
            'a working node dot takes its branch color: ' + detail,
          );
        } else {
          // The row's context line is drawn in --mut.
          const restColor = getComputedStyle(row.context).color;
          assert(
            dot.borderTopColor === restColor,
            'a resting node dot uses the muted color: ' + detail,
          );
        }
      } else {
        const contentLeft =
            row.btn.firstElementChild.getBoundingClientRect().left,
          stubGap = contentLeft - (stubLeft + stubWidth);
        assert(
          stubWidth >= 4 && stubGap >= 1 && stubGap <= 6,
          'horizontal branches reach just short of the row logo: ' +
            JSON.stringify({ key, stubWidth, stubGap }),
        );
        const logoRect = row.btn.firstElementChild.getBoundingClientRect(),
          logoCenterY = logoRect.top + logoRect.height / 2,
          branchY = btnRect.top + parseFloat(stub.top) + 0.5;
        // The glyph's center is half a pixel above its box center.
        assert(
          Math.abs(branchY - (logoCenterY - 0.5)) <= 0.25,
          'the branch meets the logo glyph center: ' +
            JSON.stringify({ key, branchY, logoCenterY }),
        );
      }
      const parentRow = rows.get(row.item.parent),
        parentDepth = parentRow ? +parentRow.node.style.getPropertyValue('--depth') : 0;
      if (parentRow && parentDepth < 4) {
        const isParentWorker = parentRow.node.classList.contains('worker'),
          parentPad = parseFloat(getComputedStyle(parentRow.btn).paddingLeft),
          parentSlotLeft =
            parentRow.btn.getBoundingClientRect().left +
            parentPad -
            (isParentWorker ? 28 : 0),
          trunkOffset = trunkRight - 1 - parentSlotLeft;
        // A worker's dot spans slot +7..+13, so its children's trunk must drop through it.
        const [low, high] = isParentWorker ? [8, 11] : [0, 20];
        assert(
          trunkOffset >= low && trunkOffset <= high,
          'child trunks drop from the parent logo, or the node dot of a worker parent: ' +
            JSON.stringify({ key, parentDepth, trunkOffset }),
        );
      }
      connectorJunctions.push({
        key,
        source: row.item.live ? 'live' : row.item.sub ? 'metadata' : 'process',
        depth: +row.node.style.getPropertyValue('--depth'),
        trunkRight,
        stubLeft,
        stubWidth,
        delta: stubLeft - trunkRight,
        titleLeft: row.name.getBoundingClientRect().left,
      });
    }
    // Workers keep the title column of logo rows at the same depth.
    const titleColumns = connectorJunctions.filter((junction) =>
      connectorJunctions.some(
        (other) =>
          other.depth === junction.depth &&
          (other.source === 'process') !== (junction.source === 'process'),
      ),
    );
    assert(
      titleColumns.length > 0 &&
        titleColumns.every(
          (junction) =>
            Math.abs(
              junction.titleLeft -
                titleColumns.find(
                  (other) =>
                    other.depth === junction.depth &&
                    other.source === 'process',
                ).titleLeft,
            ) <= 1,
        ),
      'worker titles start in the same column as logo-row titles at the same depth: ' +
        JSON.stringify(titleColumns),
    );
    assert(
      connectorJunctions.every((junction) => Math.abs(junction.delta) <= 0.25),
      'horizontal stubs join the actual parent trunk right edge without crossing left at roots or capped depths: ' +
        JSON.stringify(connectorJunctions),
    );

    const activitySnapshot = {
      agents: [
        agent(49000, '/scan-a', { kind: 'claude', recentCpu: 0.5 }),
        agent(49001, '/scan-a', { ppid: 49000, idleSec: 20 }),
        agent(49002, '/scan-b', { kind: 'opencode', recentCpu: null }),
        agent(49003, '/scan-b', {
          ppid: 49002,
          recentCpu: 0.5,
          canFocus: true,
        }),
      ],
      subagents: [
        { id: 'scan-working', parentPid: 49000, cwd: '/scan-a', working: true },
        { id: 'scan-quiet', parentPid: 49000, cwd: '/scan-a', working: false },
        { id: 'scan-unknown', parentPid: 49000, cwd: '/scan-a' },
      ],
      liveWorkers: [{ key: 'live:scan', parent: 'a:49002', pid: 49002 }],
      communication: true,
      at: Date.now(),
    };
    render(activitySnapshot);
    render(activitySnapshot);
    $('expand').click();
    const activityColor = getComputedStyle(rows.get('a:49000').st).color,
      foreground = getComputedStyle(document.body).color;
    assert(
      contrast(activityColor, background) >= 4.5,
      'working title and branch accent have readable contrast in both themes',
    );
    for (const key of ['a:49000', 'a:49003', 'sub:scan-working']) {
      const row = rows.get(key);
      assert(
        getComputedStyle(row.name).color === activityColor &&
          getComputedStyle(row.btn, '::before').borderTopColor ===
            activityColor &&
          row.st.textContent === 'working',
        'each observed working row accents its own title and short branch while retaining status text, even when monitoring-only',
      );
      if (row.logo)
        assert(
          getComputedStyle(row.logo).color === activityColor &&
            getComputedStyle(row.logo.querySelector('path')).fill ===
              activityColor &&
            row.logo.querySelector('path').getAttribute('d') ===
              LOGOS[row.kind],
          'working monochrome provider glyphs match the title using currentColor without changing their paths',
        );
      assert(
        getComputedStyle(row.kids, '::before').borderLeftColor !==
          activityColor &&
          getComputedStyle(row.node.parentElement, '::before')
            .borderLeftColor !== activityColor,
        'shared parent and child trunks never inherit a working row accent',
      );
    }
    for (const key of [
      'a:49001',
      'a:49002',
      'sub:scan-quiet',
      'sub:scan-unknown',
      'live:scan',
    ]) {
      const row = rows.get(key);
      assert(
        getComputedStyle(row.name).color === foreground &&
          getComputedStyle(row.btn, '::before').borderTopColor !==
            activityColor,
        'quiet and unknown rows remain readable and never inherit activity from a parent or child',
      );
      if (row.logo)
        assert(
          getComputedStyle(row.logo).color !== activityColor &&
            getComputedStyle(row.logo.querySelector('path')).fill !==
              activityColor,
          'quiet/unknown provider glyphs do not inherit activity from working parents or children',
        );
    }
    openSearch();
    $('search').value = 'pid:49000';
    applySearch();
    assert(
      searchRows.get('a:49000').btn.disabled &&
        getComputedStyle(searchRows.get('a:49000').name).color ===
          activityColor &&
        getComputedStyle(searchRows.get('a:49000').logo.querySelector('path'))
          .fill === activityColor,
      'monitoring-only working titles and glyphs retain the same activity cue in search',
    );
    closeSearch();
    for (const retained of ['stale', 'offline']) {
      $('offline').hidden = retained !== 'offline';
      freshness(activitySnapshot.at + 16000);
      for (const row of rows.values())
        assert(
          getComputedStyle(row.name).color === foreground &&
            getComputedStyle(row.btn, '::before').borderTopColor !==
              activityColor,
          'stale/offline measurements cannot retain live working title or branch accents',
        );
      for (const row of rows.values())
        if (row.logo)
          assert(
            getComputedStyle(row.logo).color !== activityColor,
            'stale/offline glyphs cannot retain live activity color',
          );
    }
    $('offline').hidden = true;
    freshness(activitySnapshot.at);
    assert(
      getComputedStyle(rows.get('a:49000').name).color === activityColor &&
        getComputedStyle(rows.get('a:49000').logo).color === activityColor,
      'fresh recovery restores observed title and glyph accents',
    );
    activitySnapshot.agents[0].recentCpu = 0;
    activitySnapshot.subagents[0].working = false;
    render(activitySnapshot);
    for (const key of ['a:49000', 'sub:scan-working'])
      assert(
        getComputedStyle(rows.get(key).name).color === foreground &&
          getComputedStyle(rows.get(key).btn, '::before').borderTopColor !==
            activityColor,
        'working-to-quiet updates remove title and branch accents without leaving inherited color',
      );
    assert(
      getComputedStyle(rows.get('a:49000').logo).color !== activityColor,
      'working-to-quiet updates remove the provider glyph accent',
    );
    show([agent(49100, '/scan-fallback', { kind: 'unknown', recentCpu: 0.5 })]);
    assert(
      rows.get('a:49100').logo.textContent === 'U' &&
        getComputedStyle(rows.get('a:49100').logo).color === activityColor,
      'working monogram fallbacks share the same activity cue without inventing a provider icon',
    );
    show([agent(49100, '/scan-fallback', { kind: 'unknown' })]);
    assert(
      getComputedStyle(rows.get('a:49100').logo).color !== activityColor,
      'quiet monogram fallbacks return to neutral',
    );

    await new Promise((r) => setTimeout(r)); // violation events are queued tasks
    assert(
      !cspViolations.length,
      'no Content-Security-Policy violations: ' + cspViolations.join(', '),
    );
    parent.document.body.dataset.searchMs = document.body.dataset.searchMs;
    parent.document.body.dataset.uiTests = 'passed';
  } catch (error) {
    parent.document.body.dataset.uiTests = 'failed';
    const failure = document.createElement('pre');
    failure.textContent =
      error.stack +
      (cspViolations.length ? ' | CSP: ' + cspViolations.join(', ') : '');
    parent.document.body.append(failure);
  }
})();
