import { test } from 'node:test';
import assert from 'node:assert/strict';
import { observeCommunication } from './observer.js';

function fixture() {
  const output = [], parents = new Map([['ses_child', 'ses_root'], ['ses_nested', 'ses_child']]);
  let clock = 10000, ticks = 100;
  const observer = observeCommunication({ data: { session: { get: id => parents.has(id) ? { parentID: parents.get(id) } : undefined } } }, m => output.push(m), () => clock, () => {}, () => ticks);
  observer.presence('12:2.1.4', 'ses_root'); output.length = 0;
  const event = (type, data = {}) => observer.event({ created: 10000, type: `session.${type}`, data: { sessionID: 'ses_root', assistantMessageID: 'msg_1', id: 'call_1', ...data } });
  const start = (child = 'ses_child', extra = {}) => {
    event('tool.input.started', { name: 'subagent', ...extra });
    event('tool.progress', { metadata: { sessionID: child, status: 'running' }, ...extra });
  };
  const admit = (sessionID = 'ses_child') => event('inbox.enqueued', { sessionID, item: { type: 'user' } });
  const finish = (status = 'completed', extra = {}) => event('tool.success', { executed: false, metadata: { sessionID: 'ses_child', status }, ...extra });
  return { output, observer, event, start, admit, finish, parents, advance: ms => { clock += ms; ticks += ms; }, jumpWall: ms => { clock += ms; } };
}
test('progress is an attempt, unrelated inbox admission never confirms send; foreground returns once', () => {
  const f = fixture(); f.start(); assert.deepEqual(f.output.map(x => x.type), ['attempted']);
  f.admit(); f.admit(); assert.deepEqual(f.output.map(x => x.type), ['attempted']);
  f.finish(); f.finish(); assert.deepEqual(f.output.map(x => x.type), ['attempted', 'returned']);
  assert.equal(JSON.stringify(f.output).includes('prompt'), false);
});
test('background-running, failure, wrong child, and provider-executed tools are not replies', () => {
  for (const mode of ['running', 'failed', 'wrong', 'provider']) {
    const f = fixture(); f.start(); f.admit();
    if (mode === 'failed') f.event('tool.failed');
    else f.finish(mode === 'running' ? 'running' : 'completed', mode === 'wrong' ? { metadata: { status: 'completed', sessionID: 'ses_wrong' } } : mode === 'provider' ? { executed: true } : {});
    assert(!f.output.some(x => x.type === 'returned'), mode);
    assert.equal(f.output.some(x => x.type === 'sent'), mode === 'running');
  }
});
test('nested, parallel, ambiguous calls, and unknown parent are bounded and explicit', () => {
  const f = fixture(); f.start(); f.admit(); f.finish();
  f.start('ses_nested', { sessionID: 'ses_child', id: 'call_2' }); f.admit('ses_nested');
  assert.equal(f.output.at(-1).parent, 'ses_child');
  const g = fixture(); g.start(); g.start('ses_child', { id: 'call_2' }); g.admit(); assert(!g.output.some(x => x.type === 'sent'));
  const h = fixture(); h.start('ses_child', { sessionID: 'ses_stranger' }); h.admit(); assert.equal(h.output.length, 0);
  for (let i = 0; i < 1000; i++) h.event('tool.input.started', { name: 'subagent', id: `c${i}` });
  assert(h.observer.size() <= 64);
});
test('switch, disconnect, stale events, status, CPU, and arbitrary tools never replay', () => {
  const f = fixture(); f.start(); f.admit(); f.output.length = 0;
  f.observer.reset(); f.finish(); assert.equal(f.output.length, 0);
  f.start(); f.observer.presence('12:2.1.4', 'ses_other'); f.output.length = 0; f.finish(); assert.equal(f.output.length, 0);
  f.observer.event({ created: 0, type: 'session.tool.input.started', data: { name: 'subagent', sessionID: 'ses_other' } });
  assert.equal(f.observer.size(), 0);
  for (const type of ['status', 'execution.succeeded', 'created', 'deleted', 'cpu', 'mtime']) f.event(type);
  f.event('tool.input.started', { name: 'shell' }); f.finish(); assert(f.output.every(x => x.type === 'gap'));
});
test('private inputs/results/inbox contents are never accessed or retained', () => {
  const f = fixture(); f.start();
  const data = { sessionID: 'ses_root', assistantMessageID: 'msg_1', id: 'call_1', executed: false, metadata: { sessionID: 'ses_child', status: 'completed' } };
  for (const name of ['input', 'content', 'output', 'text']) Object.defineProperty(data, name, { get() { throw Error('Private body accessed'); } });
  f.observer.event({ created: 10000, type: 'session.tool.success', data });
  assert.equal(f.output.at(-1).type, 'returned');
  assert.equal(f.observer.size(), 1); // one identifier-only relationship, no completed call
});
test('failed ancestry is released; unsupported child metadata and overload explicitly report a gap', () => {
  const f = fixture(); f.start(); f.start('ses_nested', { sessionID: 'ses_child', id: 'call_2' });
  f.event('tool.failed'); assert.equal(f.observer.size(), 0); assert.equal(f.output.at(-1).type, 'forget');
  const g = fixture(); g.parents.set('ses_unknown', 'ses_wrong'); g.start('ses_unknown'); assert.deepEqual(g.output.map(x => x.type), ['gap']);
  for (let i = 0; i < 200; i++) g.event('tool.input.started', { name: 'subagent', id: `c${i}` });
  assert.equal(g.output.filter(x => x.type === 'gap').length, 1); assert(g.observer.size() <= 64);
});
test('new child metadata may hydrate after progress, without losing a valid terminal result', () => {
  for (const status of ['running', 'completed']) {
    const f = fixture(); f.parents.delete('ses_child');
    f.event('tool.input.started', { name: 'subagent' });
    f.event('created', { sessionID: 'ses_child', parentID: 'ses_root' }); // native cache GET still pending
    f.event('tool.progress', { metadata: { sessionID: 'ses_child', status: 'running' } });
    assert.equal(f.output.length, 0, 'missing cache is unresolved identity, not a wrong parent');
    f.parents.set('ses_child', 'ses_root'); f.finish(status);
    assert.deepEqual(f.output.map(x => x.type), [status === 'running' ? 'sent' : 'returned']);
    assert.equal(f.observer.size(), 1, 'only one current relationship remains');
  }
});
test('terminal-before-hydration resolves from relevant metadata only within a short TTL', () => {
  for (const status of ['running', 'completed']) {
    const f = fixture(); f.parents.delete('ses_child'); f.start(); f.finish(status); f.finish(status);
    assert.equal(f.output.length, 0); assert.equal(f.observer.size(), 1);
    f.parents.set('ses_child', 'ses_root'); f.observer.reconcile(); f.observer.reconcile();
    assert.deepEqual(f.output.map(x => x.type), [status === 'running' ? 'sent' : 'returned']);
  }
  const expired = fixture(); expired.parents.delete('ses_child'); expired.start(); expired.finish();
  expired.advance(1000); expired.finish(); // a duplicate terminal must not extend expiry
  expired.advance(1001); expired.parents.set('ses_child', 'ses_root'); expired.observer.reconcile();
  assert.deepEqual(expired.output.map(x => x.type), ['gap']); assert.equal(expired.observer.size(), 0);
  const skewed = fixture(); skewed.parents.delete('ses_child'); skewed.start(); skewed.finish();
  skewed.advance(2001); skewed.jumpWall(-60000); skewed.parents.set('ses_child', 'ses_root'); skewed.observer.reconcile();
  assert.deepEqual(skewed.output.map(x => x.type), ['gap'], 'wall-clock rollback cannot resurrect an expired confirmation');
  const wrong = fixture(); wrong.parents.delete('ses_child'); wrong.start(); wrong.finish();
  wrong.parents.set('ses_child', 'ses_attacker'); wrong.observer.reconcile();
  assert.deepEqual(wrong.output.map(x => x.type), ['gap']); assert.equal(wrong.observer.size(), 0);
  for (const reset of [f => f.observer.reset(), f => f.observer.presence('12:2.1.4', 'ses_other')]) {
    const f = fixture(); f.parents.delete('ses_child'); f.start(); f.finish(); reset(f); f.output.length = 0;
    f.parents.set('ses_child', 'ses_root'); f.observer.reconcile(); assert.equal(f.output.length, 0);
  }
});
test('terminal metadata can identify a missed progress target, but a known root is not a child', () => {
  const f = fixture(); f.event('tool.input.started', { name: 'subagent' }); f.finish();
  assert.deepEqual(f.output.map(x => x.type), ['returned']);
  const g = fixture(); g.parents.set('ses_child', undefined); g.start(); g.finish();
  assert(!g.output.some(x => ['sent', 'returned', 'attempted'].includes(x.type)));
});
