import { $, el, txt } from './dom.js';
import { getCommunicationEdge } from './dashboard.js';

let readSnapshot, readSnapshotState, renderSnapshot, refreshSnapshot;
let communicationSource = null;
let communicationGap = false;
const activePulses = new Map();
const communicationStatus = el('p');
communicationStatus.id = 'communication-status';
communicationStatus.hidden = true;
document.body.append(communicationStatus);
const communicationFeedback = el('p');
communicationFeedback.id = 'communication-feedback';
communicationFeedback.hidden = true;
communicationFeedback.setAttribute('role', 'status');
communicationFeedback.setAttribute('aria-live', 'polite');
document.body.append(communicationFeedback);

export function clearPulses() {
  for (const finish of [...activePulses.values()]) finish();
}

export function refreshCommunication(data) {
  communicationStatus.hidden = !data?.communication;
  communicationFeedback.hidden = !data?.communication;
  for (const finish of [...activePulses.values()]) if (!finish.valid()) finish();
}

function communicationPulse(key, direction) {
  if (document.hidden || readSnapshotState() || $('search-panel').open) return;
  const previous = activePulses.get(key);
  if (previous) {
    if (previous.direction === direction || direction === 'attempted') return;
    previous();
  }
  if (activePulses.size >= 4) return;
  const { item, child, parent } = getCommunicationEdge(key);
  if (!item?.live || item.dying || !child || !parent || child.node.parentElement !== parent.kids) return;
  const anchor = parent.kids.getBoundingClientRect();
  const bounds = child.btn.getBoundingClientRect();
  if (!child.btn.getClientRects().length || !parent.btn.getClientRects().length ||
      anchor.top < 0 || bounds.bottom > innerHeight || bounds.left < 0 || bounds.right > innerWidth) return;
  const vertical = getComputedStyle(parent.kids, '::before');
  const horizontal = getComputedStyle(child.btn, '::before');
  const x = anchor.left + parseFloat(vertical.left);
  const y = bounds.top + bounds.height / 2;
  const end = bounds.left + parseFloat(horizontal.left) + parseFloat(horizontal.width);
  if (![x, y, end].every(Number.isFinite)) return;
  const label = el('span', 'communication-note');
  label.textContent = (direction === 'attempted' ? 'Delegation attempted · acceptance not confirmed'
    : direction === 'sent' ? 'Sent to worker' : 'Reply received') + ' · ' + child.name.textContent + ' · observed live ';
  communicationFeedback.hidden = false;
  communicationFeedback.append(label);
  let dot = null, animation = null, timer;
  const finish = () => {
    clearTimeout(timer);
    animation?.cancel();
    dot?.remove();
    label.remove();
    activePulses.delete(key);
    communicationFeedback.hidden = !readSnapshot()?.communication;
  };
  finish.valid = () => {
    if (document.hidden || readSnapshotState() || getCommunicationEdge(key).item?.parent !== item.parent ||
        !child.node.isConnected || !parent.node.isConnected) return false;
    const next = child.btn.getBoundingClientRect();
    const nextAnchor = parent.kids.getBoundingClientRect();
    return child.btn.getClientRects().length && next.top === bounds.top && next.left === bounds.left &&
      next.width === bounds.width && next.height === bounds.height &&
      nextAnchor.top === anchor.top && nextAnchor.left === anchor.left;
  };
  finish.direction = direction;
  activePulses.set(key, finish);
  if (direction === 'attempted' || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    timer = setTimeout(finish, 1800);
    return;
  }
  dot = el('span', 'communication-dot');
  dot.setAttribute('aria-hidden', 'true');
  document.body.append(dot);
  const point = (x, y, offset, opacity = 1) => ({
    transform: `translate(${x - 4}px, ${y - 4}px)`, offset, opacity
  });
  const frames = direction === 'sent'
    ? [point(x, anchor.top, 0), point(x, y, .7), point(end, y, .9), point(end, y, 1, 0)]
    : [point(end, y, 0), point(x, y, .2), point(x, anchor.top, .9), point(x, anchor.top, 1, 0)];
  animation = dot.animate(frames, { duration: 800, easing: 'linear', iterations: 1 });
  animation.onfinish = finish;
  timer = setTimeout(finish, 1200);
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
    if (!unchanged) renderSnapshot({ ...snapshot, liveWorkers: frame.workers });
    else refreshSnapshot();
    if (frame.lost) {
      communicationGap = true;
      txt(communicationStatus, 'Communication observations dropped · live only, no replay');
      return;
    }
    txt(communicationStatus,
      (frame.ambiguous ? 'Communication: ambiguous TUI views · affected pulses unavailable; select the session in one terminal'
        : frame.workers.length ? 'Communication: observing live metadata only'
          : 'Communication: waiting for live worker metadata · requires opt-in OpenCode TUI bridge') +
      ' · other providers unavailable' + (communicationGap ? ' · some observations missing; no replay' : ''));
    if (frame.key && ['attempted', 'sent', 'returned'].includes(frame.direction)) communicationPulse(frame.key, frame.direction);
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
  matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', clearPulses);
  document.addEventListener('visibilitychange', communicationStream);
}
