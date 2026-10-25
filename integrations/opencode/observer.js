// OpenCode 2.1.3/2.1.4 native event contract; retain identifiers only, never bodies.
export function observeCommunication(context, emit, now = Date.now, changed = () => {}, elapsed = now) {
  const calls = new Map(), workers = new Map();
  let service = '', session = '', lost = false;
  function gap() { if (!lost) { lost = true; emit({ type: 'gap', service, session }); } }
  function presence(nextService, nextSession) {
    if (nextService !== service || nextSession !== session) { calls.clear(); workers.clear(); lost = false; changed(); }
    service = nextService; session = nextSession;
    if (service) emit({ type: 'presence', service, session });
  }
  function reset() { calls.clear(); workers.clear(); lost = false; changed(); }
  function forget(c) {
    const published = workers.has(c.child), removed = new Set([c.child]);
    for (let i = 0; i < 64; i++) { let more = false; for (const [id, parent] of workers) if (removed.has(parent) && !removed.has(id)) { removed.add(id); more = true; } if (!more) break; }
    for (const id of removed) workers.delete(id);
    for (const [id, call] of calls) if (removed.has(call.parent)) calls.delete(id);
    if (published) emit({ type: 'forget', service, session, child: c.child, parent: c.parent });
  }
  // session.get reads the host's reactive metadata cache; this never fetches history or starts a sync.
  function reconcile() {
    let dirty = false;
    for (const [key, c] of calls) {
      if (c.expires && elapsed() >= c.expires) { calls.delete(key); gap(); dirty = true; continue; }
      if (!c.child) continue;
      const info = context.data.session.get(c.child);
      if (!info) continue; // the native session.created metadata GET may still be pending
      if (info.parentID !== c.parent) { calls.delete(key); forget(c); gap(); dirty = true; continue; }
      if (c.parent !== session && !workers.has(c.parent)) continue;
      if (workers.size >= 64 && !workers.has(c.child)) { calls.delete(key); gap(); dirty = true; continue; }
      if (c.terminal) {
        // Publish the relationship and confirmation atomically, not an attempt/confirmation burst.
        workers.set(c.child, c.parent); calls.delete(key);
        emit({ type: c.terminal, service, session, child: c.child, parent: c.parent }); dirty = true;
      } else if (!c.published) {
        workers.set(c.child, c.parent); c.published = true;
        emit({ type: 'attempted', service, session, child: c.child, parent: c.parent }); dirty = true;
      }
    }
    if (dirty) changed();
  }
  function event(event) {
    if (!service || !session) return;
    const d = event.data;
    if (!d || typeof d.sessionID !== 'string') return;
    if (!Number.isFinite(event.created) || Math.abs(now() - event.created) > 5000) { if (d.sessionID === session || workers.has(d.sessionID)) gap(); return; }
    const key = `${d.sessionID}:${d.assistantMessageID}:${d.id}`;
    if (event.type === 'session.tool.input.started') {
      if (d.name === 'subagent' && (d.sessionID === session || workers.has(d.sessionID) || [...calls.values()].some(c => c.child === d.sessionID)) && !calls.has(key)) {
        if (calls.size >= 64) { gap(); return; }
        calls.set(key, { parent: d.sessionID, child: '' });
      }
      return;
    }
    const c = calls.get(key); if (!c) return;
    if (event.type === 'session.tool.progress') {
      const child = d.metadata?.sessionID;
      if (d.metadata?.status !== 'running' || typeof child !== 'string' || c.terminal) return;
      if (c.child && c.child !== child) { calls.delete(key); forget(c); gap(); changed(); return; }
      c.child = child; changed(); reconcile();
    } else if (event.type === 'session.tool.success' || event.type === 'session.tool.failed') {
      // `executed` means providerExecuted in the native schema, not tool success. This builtin runs locally.
      if (event.type === 'session.tool.success' && d.executed === false && typeof d.metadata?.sessionID === 'string' && (!c.child || d.metadata.sessionID === c.child) && ['running', 'completed'].includes(d.metadata.status)) {
        if (c.terminal) return; // duplicates cannot extend the short identity-resolution deadline
        c.child = d.metadata.sessionID; c.terminal = d.metadata.status === 'running' ? 'sent' : 'returned';
        c.expires = elapsed() + 2000; changed(); reconcile();
      } else {
        calls.delete(key); if (c.child) forget(c); changed();
      }
    }
  }
  return { presence, reset, event, reconcile, size: () => calls.size + workers.size };
}
