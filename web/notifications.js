import { $ } from './dom.js';
import { load, save } from './storage.js';
import { label } from './agents.js';

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
  bell.textContent = notifyOn ? '\u{1F514}' : '\u{1F515}';
  bell.setAttribute('aria-pressed', notifyOn);
  bell.title =
    'Recently quiet notifications ' + (notifyOn ? 'on' : 'off') + ' · not requests for input';
  bell.setAttribute('aria-label', bell.title);
};
function fire(it) {
  if (!notifyOn || !('Notification' in window) || Notification.permission !== 'granted') return;
  const n = new Notification(it.name, {
    body: 'Recently quiet · ' + label(it) + ' · activity observation, not a request for input',
    tag: 'swarmview-' + it.pid
  });
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

export function setupNotifications(onActivate) {
  activateAgent = onActivate;
  if ('Notification' in window) {
    bell.hidden = false;
    paintBell();
    bell.onclick = async () => {
      notifyOn =
        !notifyOn &&
        (Notification.permission === 'granted' ||
          (await Notification.requestPermission()) === 'granted');
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
