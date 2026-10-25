import { $, el, txt, attr } from './dom.js';
import { getCommunicationRow } from './dashboard.js';

let readSnapshot, readSnapshotState, renderSnapshot, refreshSnapshot;
let communicationSource = null;
let communicationGap = false;
const activeFeedback = new Map();
const communicationStatus = el('p');
communicationStatus.id = 'communication-status';
communicationStatus.hidden = true;
document.body.append(communicationStatus);
const communicationFeedback = el('p');
communicationFeedback.id = 'communication-feedback';
communicationFeedback.className = 'communication-announcement';
communicationFeedback.hidden = true;
communicationFeedback.setAttribute('role', 'status');
communicationFeedback.setAttribute('aria-live', 'polite');
document.body.append(communicationFeedback);

export function clearPulses() {
  for (const entry of activeFeedback.values()) entry.clear();
}

export function refreshCommunication(data) {
  communicationStatus.hidden = !data?.communication;
  communicationFeedback.hidden = !data?.communication;
  for (const entry of activeFeedback.values()) if (!entry.valid()) entry.clear();
}

function showFeedback(key, direction) {
  if (document.hidden || readSnapshotState() || $('search-panel').open) return;
  const { item, child, parent } = getCommunicationRow(key);
  if (!item?.live || item.dying || !child || !parent || child.node.parentElement !== parent.kids) return;
  let entry = activeFeedback.get(key);
  if (entry && !entry.valid()) { entry.clear(); entry = null; }
  if (!entry && activeFeedback.size >= 4) return;
  // One visibility check per observation; static feedback has no travel/refresh geometry.
  const bounds = child.btn.getClientRects()[0];
  if (!bounds || bounds.top < 0 || bounds.bottom > innerHeight || bounds.left < 0 || bounds.right > innerWidth) {
    entry?.clear();
    return;
  }
  if (!entry) {
    const line = child.btn.parentElement;
    const label = el('span', 'communication-note');
    label.setAttribute('aria-hidden', 'true'); // full context is announced once in the polite region
    entry = { label, line, parent: item.parent, pid: item.pid, timer: null, summary: '' };
    entry.valid = () => {
      const current = getCommunicationRow(key);
      return readSnapshot()?.communication && !document.hidden && !readSnapshotState() && !$('search-panel').open &&
        current.item?.live && !current.item.dying && current.item.parent === entry.parent && current.item.pid === entry.pid &&
        current.child === child && current.parent === parent && child.node.isConnected &&
        parent.node.isConnected && child.node.parentElement === parent.kids && !child.node.closest('[hidden]');
    };
    entry.clear = () => {
      if (activeFeedback.get(key) !== entry) return;
      clearTimeout(entry.timer);
      label.remove();
      line.classList.remove('communication-active');
      child.context.classList.remove('has-communication');
      child.context.removeAttribute('data-communication');
      if (communicationFeedback.textContent === entry.summary) txt(communicationFeedback, '');
      activeFeedback.delete(key);
    };
    activeFeedback.set(key, entry);
    child.context.append(label);
    child.context.classList.add('has-communication');
    line.classList.add('communication-active');
  }
  const name = text => text.length > 160 ? text.slice(0, 160) + '…' : text;
  const from = name(direction === 'returned' ? child.name.textContent : parent.name.textContent);
  const to = name(direction === 'returned' ? parent.name.textContent : child.name.textContent);
  const message = direction === 'attempted' ? 'Delegation attempted; acceptance not confirmed'
    : direction === 'sent' ? 'Sent to worker' : 'Reply received';
  entry.summary = `${message}: ${from} → ${to} · observed live`;
  entry.direction = direction;
  txt(entry.label, direction === 'attempted' ? 'Attempted' : direction === 'sent' ? 'Sent' : 'Reply');
  attr(entry.label, 'title', entry.summary);
  attr(child.context, 'data-communication', direction);
  txt(communicationFeedback, entry.summary);
  clearTimeout(entry.timer);
  entry.timer = setTimeout(entry.clear, 1800);
}

export function communicationStream() {
  const data = readSnapshot();
  if (!data?.communication || document.hidden) {
    communicationSource?.close();
    communicationSource = null;
    clearPulses();
    return;
  }
  if (communicationSource) return;
  const source = communicationSource = new EventSource('/communication');
  source.onmessage = event => {
    if (communicationSource !== source) return;
    const frame = JSON.parse(event.data);
    const snapshot = readSnapshot();
    if (!snapshot || document.hidden) return;
    const previous = snapshot.liveWorkers || [];
    // All worker presentation fields, including order; metrics still update through ordinary polls.
    const unchanged = frame.workers.length === previous.length && frame.workers.every((worker, index) =>
      worker.key === previous[index].key && worker.parent === previous[index].parent && worker.pid === previous[index].pid);
    if (!unchanged) { clearPulses(); renderSnapshot({ ...snapshot, liveWorkers: frame.workers }); }
    else refreshSnapshot();
    if (frame.lost) {
      clearPulses();
      communicationGap = true;
      txt(communicationStatus, 'Communication observations dropped · live only, no replay');
      return;
    }
    if (frame.ambiguous) clearPulses();
    txt(communicationStatus,
      (frame.ambiguous ? 'Communication: ambiguous TUI views · affected pulses unavailable; select the session in one terminal'
        : frame.workers.length ? 'Communication: observing live metadata only'
          : 'Communication: waiting for live worker metadata · requires opt-in OpenCode TUI bridge') +
      ' · other providers unavailable' + (communicationGap ? ' · some observations missing; no replay' : ''));
    if (frame.key && ['attempted', 'sent', 'returned'].includes(frame.direction)) showFeedback(frame.key, frame.direction);
  };
  source.onerror = () => {
    if (communicationSource !== source) return;
    communicationGap = true;
    clearPulses();
    txt(communicationStatus, 'Communication disconnected · observations may be missing; no replay');
  };
}

export function setupCommunication({ snapshot, snapshotState, render, freshness }) {
  readSnapshot = snapshot;
  readSnapshotState = snapshotState;
  renderSnapshot = render;
  refreshSnapshot = freshness;
  addEventListener('resize', clearPulses);
  addEventListener('scroll', clearPulses, true);
  document.addEventListener('visibilitychange', communicationStream);
}
