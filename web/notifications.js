import { $ } from './dom.js';
import { load, save } from './storage.js';

const NS = 'http://www.w3.org/2000/svg';
const QUIET_MIN_SEC = 15,
  QUIET_MAX_SEC = 300;
let activateAgent;
const recentlyQuiet = (it) =>
  it.status === 'waiting' &&
  it.idleSec >= QUIET_MIN_SEC &&
  it.idleSec <= QUIET_MAX_SEC &&
  eps.get(it.pid)?.worked;
// tab title + favicon badge; the favicon is rebuilt only when the count changes
let lastN = -1;
const fav = document.querySelector('link[rel="icon"]');
function updateTabBadge(n) {
  const t = n ? `(${n}) recently quiet · Swarmview` : 'Swarmview';
  if (document.title !== t) document.title = t;
  if (n === lastN) return;
  lastN = n;
  if (!n) {
    fav.href = '/logo.svg';
    return;
  }
  const css = (v) => getComputedStyle(document.documentElement).getPropertyValue(v).trim();
  const svg =
    `<svg xmlns="${NS}" viewBox="0 0 32 32"><circle cx="16" cy="16" r="15" fill="${css('--idle')}"/>` +
    (n
      ? `<text x="16" y="22" font-size="18" font-weight="700" text-anchor="middle" fill="#fff" font-family="sans-serif">${n > 9 ? '9+' : n}</text>`
      : '') +
    '</svg>';
  fav.href = 'data:image/svg+xml,' + encodeURIComponent(svg);
}

// Notifications describe quiet observations, never requests for human input.
let notifyOn = load('swarmview.notify', false) === true;
const bell = $('notify'),
  eps = new Map(); // pid -> { worked, fired }
const paintBell = () => {
  bell.textContent = notifyOn ? 'Notifications on' : 'Enable notifications';
  bell.setAttribute('aria-pressed', notifyOn);
  bell.title =
    'Recently quiet notifications ' + (notifyOn ? 'on' : 'off') + ' · not requests for input';
  bell.setAttribute('aria-label', bell.title);
};
// The macOS app's WKWebView has no web notification permission UI, so it bridges to native.
const native = () => window.webkit?.messageHandlers?.notify;
addEventListener('swarmview-notification', (event) => activateAgent(event.detail));
function fire(it) {
  if (!notifyOn) return;
  const body = 'Recently quiet · ' + it.repoName + ' · activity observation, not a request for input';
  const tag = 'swarmview-' + it.pid;
  if (native()) {
    native().postMessage({ action: 'post', title: it.name, body, tag, pid: it.pid });
    return;
  }
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  const n = new Notification(it.name, { body, tag });
  n.onclick = () => {
    window.focus();
    activateAgent(it.pid);
    n.close();
  };
}
function watch(list) {
  const alive = new Set();
  for (const it of list) {
    alive.add(it.pid);
    let e = eps.get(it.pid);
    if (!e) eps.set(it.pid, (e = { worked: false, fired: false }));
    if (it.status === 'working') {
      e.worked = true;
      e.fired = false;
    } else if (e.worked && !e.fired && recentlyQuiet(it)) {
      e.fired = true;
      fire(it);
    }
  }
  for (const pid of eps.keys()) if (!alive.has(pid)) eps.delete(pid);
}

async function permitted() {
  if (native()) return (await native().postMessage({ action: 'request' })) === true;
  if (Notification.permission === 'granted') return true;
  return (await Notification.requestPermission()) === 'granted';
}

export function setupNotifications(onActivate) {
  activateAgent = onActivate;
  if (native() || 'Notification' in window) {
    bell.hidden = false;
    paintBell();
    bell.onclick = async () => {
      notifyOn = !notifyOn && (await permitted());
      save('swarmview.notify', notifyOn);
      paintBell();
    };
  }
}

export function updateNotifications(agents, { observe, stale }) {
  if (observe && !stale) watch(agents);
  const quiet = stale ? [] : agents.filter(recentlyQuiet).sort((a, b) => a.idleSec - b.idleSec);
  updateTabBadge(quiet.length);
  return quiet;
}

export function clearBadge() {
  updateTabBadge(0);
}
