// Load the actual plugin against a synthetic host, never a user TUI/service.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as Module from 'node:module';
import { createServer } from 'node:net';
import { mkdtemp, mkdir, chmod, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

test(
  'actual TUI plugin: route identity, owner-only transport, native signals and cleanup',
  { skip: typeof Module.registerHooks !== 'function' },
  async () => {
    const hooks = Module.registerHooks({
      resolve(specifier, context, next) {
        const code =
          specifier === '@opencode/plugin/tui'
            ? 'export const Plugin = { define: x => x };'
            : specifier === 'solid-js'
              ? 'export function createEffect(fn) { (globalThis.__swarmviewPulseEffects ??= []).push(fn); globalThis.__swarmviewPulseEffect ??= fn; fn(); } export function createSignal(value) { return [() => value, update => (value = typeof update === "function" ? update(value) : update)]; }'
              : null;
        return code
          ? {
              url: 'data:text/javascript,' + encodeURIComponent(code),
              shortCircuit: true,
            }
          : next(specifier, context);
      },
    });
    const { default: plugin } = await import('./tui.ts');
    const root = await mkdtemp(join(tmpdir(), 'sv-'));
    const oldHome = process.env.HOME,
      oldCache = process.env.XDG_CACHE_HOME;
    process.env.HOME = root;
    process.env.XDG_CACHE_HOME = root;
    const cache =
      process.platform === 'darwin' ? join(root, 'Library', 'Caches') : root;
    const directory = join(cache, 'swarmview'),
      path = join(directory, 'live.sock');
    await mkdir(directory, { recursive: true, mode: 0o700 });
    const lines = [],
      sockets = new Set();
    const server = createServer((socket) => {
      sockets.add(socket);
      socket.setEncoding('utf8');
      let pending = '';
      socket.on('data', (data) => {
        pending += data;
        let i;
        while ((i = pending.indexOf('\n')) >= 0) {
          lines.push(JSON.parse(pending.slice(0, i)));
          pending = pending.slice(i + 1);
        }
      });
      socket.on('close', () => sockets.delete(socket));
    });
    await new Promise((resolve) => server.listen(path, resolve));
    await chmod(path, 0o600);
    const wait = async (predicate) => {
      const until = Date.now() + 2000;
      while (!predicate() && Date.now() < until)
        await new Promise((resolve) => setTimeout(resolve, 2));
      assert(predicate(), 'synthetic transport did not settle');
    };
    let cleanup;
    try {
      const callbacks = new Map(),
        parents = new Map([['ses_child', 'ses_root']]);
      let route = { type: 'session', sessionID: 'ses_root' },
        healthReads = 0;
      const context = {
        app: { version: '2.1.4' },
        client: {
          server: {
            async info(options) {
              assert(options.signal);
              healthReads++;
              return {
                pid: 42,
                version: '2.1.4',
                urls: ['http://127.0.0.1:1234'],
                paths: { tmp: '/synthetic' },
              };
            },
          },
        },
        data: {
          session: {
            get: (id) =>
              parents.has(id) ? { parentID: parents.get(id) } : undefined,
          },
          on(type, callback) {
            callbacks.set(type, callback);
            return () => callbacks.delete(type);
          },
        },
        ui: {
          router: { current: () => route },
          slot(claim) {
            assert.equal(claim.append, 'app');
            claim.render();
            return () => {
              delete globalThis.__swarmviewPulseEffect;
              delete globalThis.__swarmviewPulseEffects;
            };
          },
        },
      };
      cleanup = plugin.setup(context);
      await wait(() => lines.length === 1);
      assert.equal(lines[0].type, 'presence');
      assert.equal(lines[0].session, 'ses_root');
      assert.match(lines[0].service, /^[a-f0-9]{64}:2\.1\.4$/);
      assert.equal('pid' in lines[0], false);
      assert.equal(healthReads, 1);
      assert.equal(callbacks.has('session.inbox.enqueued'), false);
      const emit = (type, extra) =>
        callbacks.get('session.' + type)?.({
          type: 'session.' + type,
          created: Date.now(),
          data: {
            sessionID: 'ses_root',
            assistantMessageID: 'msg_one',
            id: 'call_one',
            ...extra,
          },
        });
      emit('tool.input.started', { name: 'subagent' });
      emit('tool.progress', {
        metadata: { sessionID: 'ses_child', status: 'running' },
      });
      emit('tool.success', {
        executed: false,
        metadata: { sessionID: 'ses_child', status: 'completed' },
      });
      await wait(() => lines.length === 3);
      assert.deepEqual(
        lines.map((x) => x.type),
        ['presence', 'attempted', 'returned'],
      );
      assert(lines.every((x) => !('input' in x) && !('content' in x)));
      emit('tool.input.started', { id: 'call_late', name: 'subagent' });
      emit('tool.progress', {
        id: 'call_late',
        metadata: { sessionID: 'ses_late', status: 'running' },
      });
      emit('tool.success', {
        id: 'call_late',
        executed: false,
        metadata: { sessionID: 'ses_late', status: 'completed' },
      });
      await new Promise((resolve) => setTimeout(resolve, 15));
      assert.equal(lines.length, 3, 'unhydrated identity is not guessed');
      parents.set('ses_late', 'ses_root');
      globalThis.__swarmviewPulseEffects[1](); // completed native metadata GET updates the reactive cache
      await wait(() => lines.length === 4);
      assert.equal(lines[3].type, 'returned');
      assert.equal(lines[3].child, 'ses_late');
      route = { type: 'session', sessionID: 'ses_switched' };
      globalThis.__swarmviewPulseEffect();
      await wait(() => lines.length === 5);
      assert.equal(lines[4].session, 'ses_switched');
      callbacks.get('server.connected')({ type: 'server.connected', data: {} });
      await wait(() => sockets.size === 0);
      const before = lines.length;
      emit('tool.progress', {
        metadata: { sessionID: 'ses_child', status: 'running' },
      });
      await new Promise((resolve) => setTimeout(resolve, 15));
      assert.equal(
        lines.length,
        before,
        'replacement feed must pause, not reuse stale endpoint identity',
      );
      cleanup();
      cleanup = null;
      assert.equal(callbacks.size, 0);
      assert.equal(globalThis.__swarmviewPulseEffect, undefined);

      // A socket with lax permissions is rejected without creating a connection.
      for (const [target, mode] of [
        [directory, 0o755],
        [path, 0o666],
      ]) {
        await chmod(target, mode);
        cleanup = plugin.setup(context);
        await new Promise((resolve) => setTimeout(resolve, 30));
        assert.equal(sockets.size, 0);
        cleanup();
        cleanup = null;
        await chmod(target, target === directory ? 0o700 : 0o600);
      }
      // Without a service identity the transport stays closed instead of timing out and reconnecting.
      cleanup = plugin.setup({
        ...context,
        client: {
          server: {
            async info() {
              throw new Error('unavailable');
            },
          },
        },
      });
      await new Promise((resolve) => setTimeout(resolve, 30));
      assert.equal(sockets.size, 0);
      cleanup();
      cleanup = null;
      assert.equal(
        plugin.setup({ ...context, app: { version: '1.9.0' } }),
        undefined,
      );
    } finally {
      cleanup?.();
      for (const socket of sockets) socket.destroy();
      await new Promise((resolve) => server.close(resolve));
      if (oldHome === undefined) delete process.env.HOME;
      else process.env.HOME = oldHome;
      if (oldCache === undefined) delete process.env.XDG_CACHE_HOME;
      else process.env.XDG_CACHE_HOME = oldCache;
      await rm(root, { recursive: true, force: true });
      hooks.deregister();
      delete globalThis.__swarmviewPulseEffect;
      delete globalThis.__swarmviewPulseEffects;
    }
  },
);
