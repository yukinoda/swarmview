import { $, txt } from './dom.js';
import { formatDuration } from './agents.js';
import {
  renderDashboard,
  refreshDashboard,
  showQuiet,
  focusAgent,
  setupDashboard
} from './dashboard.js';
import { setupSearch, updateSearch, refreshSearch } from './search.js';
import { setupNotifications, updateNotifications, clearBadge } from './notifications.js';

const POLL_MS = 2000;
const POLL_HIDDEN_MS = 5000;
let lastData = null;
let busy = false;
let timer;

function snapshotState(now = Date.now()) {
  if (!$('offline').hidden) return 'offline';
  return lastData?.at != null && now - lastData.at > 15000 ? 'stale' : null;
}

function render(data, again = false) {
  lastData = data;
  const { items, agents } = renderDashboard(data, again);
  const stale = data.at != null && Date.now() - data.at > 15000;
  showQuiet(updateNotifications(agents, { observe: !again, stale }));
  updateSearch(items);
  freshness();
}

function freshness(now = Date.now()) {
  const retained = snapshotState(now);
  const prefix = retained === 'offline' ? 'Offline · ' : retained === 'stale' ? 'Stale · ' : '';
  const updated =
    lastData?.at != null
      ? `Last updated ${formatDuration(Math.max(0, (now - lastData.at) / 1000))} ago${retained ? ' · retained measurements, not live state' : ''}`
      : lastData
        ? 'Snapshot time unavailable'
        : 'Waiting for first snapshot';
  txt($('freshness'), prefix + updated);
  refreshDashboard(retained);
  refreshSearch(retained);
  if (retained) clearBadge();
}

async function poll() {
  if (busy) return;
  busy = true;
  freshness();
  try {
    const response = await fetch('/agents', { signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(response.status);
    $('offline').hidden = true;
    render(await response.json());
  } catch {
    $('offline').hidden = false;
  }
  freshness();
  busy = false;
  timer = setTimeout(poll, document.hidden ? POLL_HIDDEN_MS : POLL_MS);
}

setupDashboard(() => lastData && render(lastData, true));
setupSearch(snapshotState);
setupNotifications(focusAgent);
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) {
    clearTimeout(timer);
    poll();
  }
});
poll();
