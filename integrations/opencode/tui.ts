import { Plugin } from '@opencode/plugin/tui';
import { createEffect, createSignal } from 'solid-js';
import { connect, type Socket } from 'node:net';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { lstat } from 'node:fs/promises';
import { observeCommunication } from './observer.js';

export default Plugin.define({
  id: 'swarmview.live-communication',
  setup(context) {
    if (!['2.1.3', '2.1.4'].includes(context.app.version)) return;
    const cache =
      process.platform === 'darwin'
        ? join(homedir(), 'Library', 'Caches')
        : process.env.XDG_CACHE_HOME || join(homedir(), '.cache');
    const path = join(cache, 'swarmview', 'live.sock');
    let socket: Socket | undefined,
      sequence = 0,
      service = '',
      stopped = false,
      ready = false,
      connecting = false;
    let heartbeat: ReturnType<typeof setInterval> | undefined;
    const [metadataRevision, metadataChanged] = createSignal(0);
    const observer = observeCommunication(
      context,
      (message) => {
        if (!socket?.writable || !ready) return;
        const line = JSON.stringify({ ...message, seq: ++sequence }) + '\n';
        if (line.length > 1024 || socket.writableLength > 4096) {
          socket.destroy();
          return;
        }
        socket.write(line);
      },
      Date.now,
      () => metadataChanged((value) => value + 1),
      () => performance.now(),
    );
    function presence() {
      const route = context.ui.router.current();
      observer.presence(
        service,
        route.type === 'session' ? route.sessionID : '',
      );
    }
    async function identify() {
      service = '';
      observer.reset();
      try {
        const info = await context.client.server.info({
          signal: AbortSignal.timeout(2000),
        });
        if (!['2.1.3', '2.1.4'].includes(info.version)) {
          stopped = true;
          clearInterval(heartbeat);
        }
        if (stopped) return;
        // Namespace by the connected service descriptor, not PID alone across hosts.
        const identity = JSON.stringify([
          info.pid,
          info.version,
          [...info.urls].sort(),
          info.paths.tmp,
        ]);
        service = `${createHash('sha256').update(identity).digest('hex')}:${info.version}`;
        presence();
      } catch {
        /* A cosmetic observer must never block the TUI. */
      }
    }
    async function attach() {
      // Without a service identity the server would only time the connection out; identify first.
      if (socket || stopped || connecting || !service) return;
      connecting = true;
      try {
        const [directory, endpoint] = await Promise.all([
          lstat(join(cache, 'swarmview')),
          lstat(path),
        ]);
        const uid = process.getuid?.();
        if (
          stopped ||
          uid === undefined ||
          directory.uid !== uid ||
          endpoint.uid !== uid ||
          !directory.isDirectory() ||
          !endpoint.isSocket() ||
          (directory.mode & 0o777) !== 0o700 ||
          (endpoint.mode & 0o777) !== 0o600
        )
          return;
        const connection = connect(path);
        socket = connection;
        sequence = 0;
        connection.setTimeout(2000, () => connection.destroy());
        socket.on('connect', () => {
          connection.setTimeout(0);
          ready = true;
          observer.reset();
          presence();
        });
        socket.on('error', () => {});
        socket.on('close', () => {
          ready = false;
          socket = undefined;
          observer.reset();
        });
      } catch {
        /* Missing viewer: do not queue observations or alter agent execution. */
      } finally {
        connecting = false;
      }
    }
    // The app slot supplies a Solid owner; current() tracks route/session switches.
    const unslot = context.ui.slot({
      append: 'app',
      render() {
        createEffect(presence);
        createEffect(() => {
          metadataRevision();
          observer.reconcile();
        });
        return null;
      },
    });
    const types = [
      'session.tool.input.started',
      'session.tool.progress',
      'session.tool.success',
      'session.tool.failed',
    ] as const;
    const stops = types.map((type) =>
      context.data.on(type, (event) => {
        if (ready) observer.event(event);
      }),
    );
    stops.push(
      context.data.on('server.connected', () => {
        // Context.client is captured at setup; do not label a replacement feed with an old endpoint's identity.
        stopped = true;
        service = '';
        observer.presence('', '');
        socket?.destroy();
        clearInterval(heartbeat);
      }),
    );
    void identify().then(attach);
    if (!stopped)
      heartbeat = setInterval(() => {
        if (!service) void identify().then(attach);
        else void attach();
        if (ready) {
          presence();
          observer.reconcile();
        }
      }, 3000);
    return () => {
      stopped = true;
      clearInterval(heartbeat);
      stops.forEach((stop) => stop());
      unslot();
      if (socket && ready) {
        // Tell the viewer this is a deliberate quit, then flush the line before closing.
        observer.bye();
        const connection = socket;
        connection.end();
        connection.once('finish', () => connection.destroy());
      } else {
        socket?.destroy();
      }
      observer.reset();
    };
  },
});
