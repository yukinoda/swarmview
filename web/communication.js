import { $, el, txt, attr } from './dom.js';
import { getCommunicationRow } from './dashboard.js';

let readSnapshot, readSnapshotState, renderSnapshot, refreshSnapshot;
let communicationSource = null;
let communicationGap = false;
const activeFeedback = new Map();
// Claude cues can arrive before the next poll renders their worker row; retried on render.
const pendingFeedback = new Map();
const communicationStatus = el('p');
communicationStatus.id = 'communication-status';
communicationStatus.hidden = true;
$('communication-info').append(communicationStatus);
const communicationFeedback = el('p');
communicationFeedback.id = 'communication-feedback';
communicationFeedback.className = 'communication-announcement';
communicationFeedback.hidden = true;
communicationFeedback.setAttribute('role', 'status');
communicationFeedback.setAttribute('aria-live', 'polite');
communicationFeedback.setAttribute('aria-atomic', 'true');
document.body.append(communicationFeedback);

export function clearPulses() {
  for (const entry of activeFeedback.values()) entry.clear();
  pendingFeedback.clear();
}

export function refreshCommunication(data) {
  txt(
    $('communication-capability'),
    data?.communication
      ? 'Enabled by server · OpenCode TUI and Claude hook metadata only'
      : 'Off · requires manual server and OpenCode TUI or Claude hook setup',
  );
  communicationStatus.hidden = !data?.communication;
  communicationFeedback.hidden = !data?.communication;
  for (const entry of activeFeedback.values())
    if (!entry.valid()) entry.clear();
  const due = [...pendingFeedback]; // a still-missing row re-queues itself
  pendingFeedback.clear();
  for (const [key, cue] of due) {
    if (cue.until > Date.now())
      showFeedback(key, cue.direction, cue.pid, cue.until);
  }
}

// pid is set for Claude cues: the worker row must belong to that Claude process.
function showFeedback(key, direction, pid, until = Date.now() + 2500) {
  if (
    !readSnapshot()?.communication ||
    document.hidden ||
    readSnapshotState() ||
    document.querySelector('dialog[open]')
  )
    return;
  const { item, child, parent } = getCommunicationRow(key);
  if (!item && pid) {
    const queued = pendingFeedback.get(key);
    const restart = direction === 'sent' && queued?.direction === 'message';
    if (!restart && (queued || pendingFeedback.size < 4))
      pendingFeedback.set(key, { direction, pid, until });
    return;
  }
  // Claude cues mark only Claude metadata rows of their own host; OpenCode cues only live rows.
  const eligible = (row) => (pid ? row?.sub && row.pid === pid : row?.live);
  if (
    !eligible(item) ||
    item.dying ||
    !child ||
    !parent ||
    child.node.parentElement !== parent.kids
  )
    return;
  let entry = activeFeedback.get(key);
  if (entry && !entry.valid()) {
    entry.clear();
    entry = null;
  }
  if (!entry && activeFeedback.size >= 4) return;
  // Claude restarts a finished worker for a follow-up message; that start is the same message.
  if (direction === 'sent' && entry?.direction === 'message') return;
  // One visibility check per observation; static feedback has no travel/refresh geometry.
  const bounds = child.btn.getClientRects()[0];
  if (
    !bounds ||
    bounds.top < 0 ||
    bounds.bottom > innerHeight ||
    bounds.left < 0 ||
    bounds.right > innerWidth
  ) {
    entry?.clear();
    return;
  }
  if (!entry) {
    const line = child.btn.parentElement;
    const label = el('span', 'communication-note');
    label.setAttribute('aria-hidden', 'true'); // full context is announced once in the polite region
    entry = {
      label,
      line,
      parent: item.parent,
      pid: item.pid,
      timer: null,
      summary: '',
    };
    entry.valid = () => {
      const current = getCommunicationRow(key);
      return (
        readSnapshot()?.communication &&
        !document.hidden &&
        !readSnapshotState() &&
        !document.querySelector('dialog[open]') &&
        eligible(current.item) &&
        !current.item.dying &&
        current.item.parent === entry.parent &&
        current.item.pid === entry.pid &&
        current.child === child &&
        current.parent === parent &&
        child.node.isConnected &&
        parent.node.isConnected &&
        child.node.parentElement === parent.kids &&
        !child.node.closest('[hidden]')
      );
    };
    entry.clear = () => {
      if (activeFeedback.get(key) !== entry) return;
      clearTimeout(entry.timer);
      label.remove();
      child.context.removeAttribute('data-communication');
      if (communicationFeedback.textContent === entry.summary)
        txt(communicationFeedback, '');
      activeFeedback.delete(key);
    };
    activeFeedback.set(key, entry);
    child.context.append(label);
  }
  const name = (text) => (text.length > 160 ? text.slice(0, 160) + '…' : text);
  const from = name(
    direction === 'returned' ? child.name.textContent : parent.name.textContent,
  );
  const to = name(
    direction === 'returned' ? parent.name.textContent : child.name.textContent,
  );
  const message =
    direction === 'attempted'
      ? 'Delegation attempted; acceptance not confirmed'
      : direction === 'sent'
        ? 'Sent to worker'
        : direction === 'message'
          ? 'Message sent to worker'
          : 'Reply received';
  // A follow-up message may come from any agent, so its sender is not named.
  entry.summary =
    direction === 'message'
      ? `${message}: ${name(child.name.textContent)} · observed live`
      : `${message}: ${from} → ${to} · observed live`;
  entry.direction = direction;
  txt(
    entry.label,
    direction === 'attempted'
      ? 'Attempted from parent · unconfirmed'
      : direction === 'sent'
        ? 'Sent from parent'
        : direction === 'message'
          ? 'Message sent to worker'
          : 'Reply received by parent',
  );
  attr(entry.label, 'title', entry.summary);
  attr(child.context, 'data-communication', direction);
  txt(communicationFeedback, entry.summary);
  clearTimeout(entry.timer);
  entry.timer = setTimeout(entry.clear, 2500);
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
  const source = (communicationSource = new EventSource('/communication'));
  source.onmessage = (event) => {
    if (communicationSource !== source) return;
    const frame = JSON.parse(event.data);
    const snapshot = readSnapshot();
    if (!snapshot || document.hidden) return;
    const previous = snapshot.liveWorkers || [];
    // All worker presentation fields, including order; metrics still update through ordinary polls.
    const unchanged =
      frame.workers.length === previous.length &&
      frame.workers.every(
        (worker, index) =>
          worker.key === previous[index].key &&
          worker.parent === previous[index].parent &&
          worker.pid === previous[index].pid,
      );
    if (!unchanged) {
      clearPulses();
      renderSnapshot({ ...snapshot, liveWorkers: frame.workers }, true);
    } // a stream frame is not a new poll
    else refreshSnapshot();
    if (frame.lost) {
      clearPulses();
      communicationGap = true;
      txt(
        communicationStatus,
        'Communication observations dropped · live only, no replay',
      );
      return;
    }
    // OpenCode ambiguity never affects a Claude cue, which names its own host.
    if (frame.ambiguous && !frame.pid) clearPulses();
    txt(
      communicationStatus,
      (frame.ambiguous
        ? 'Communication: ambiguous TUI views · affected pulses unavailable; select the session in one terminal'
        : frame.workers.length
          ? 'Communication: observing live metadata only'
          : 'Communication: waiting for live worker metadata · requires opt-in OpenCode TUI bridge or Claude hooks') +
        ' · other providers unavailable' +
        (communicationGap ? ' · some observations missing; no replay' : ''),
    );
    if (
      (!frame.ambiguous || frame.pid) &&
      frame.key &&
      ['attempted', 'sent', 'returned', 'message'].includes(frame.direction)
    )
      showFeedback(frame.key, frame.direction, frame.pid);
  };
  source.onerror = () => {
    if (communicationSource !== source) return;
    // Non-200 replies (e.g. viewer limit) close without retry; let the next poll reconnect.
    if (source.readyState === 2) communicationSource = null; // EventSource.CLOSED
    communicationGap = true;
    clearPulses();
    txt(
      communicationStatus,
      'Communication disconnected · observations may be missing; no replay',
    );
  };
}

export function setupCommunication({
  snapshot,
  snapshotState,
  render,
  freshness,
}) {
  readSnapshot = snapshot;
  readSnapshotState = snapshotState;
  renderSnapshot = render;
  refreshSnapshot = freshness;
  addEventListener('resize', clearPulses);
  addEventListener('scroll', clearPulses, true);
  document.addEventListener('visibilitychange', communicationStream);
}
