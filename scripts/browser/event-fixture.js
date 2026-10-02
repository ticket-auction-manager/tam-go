import { test as base, expect } from '@playwright/test';
import { spawn } from 'node:child_process';
import { access, mkdtemp, rm } from 'node:fs/promises';
import { constants } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer, connect } from 'node:net';

export { expect };

async function listen(server) {
  await new Promise((ready, fail) => {
    server.once('error', fail);
    server.listen(0, '127.0.0.1', ready);
  });
  return server.address().port;
}

// A real TCP link to the real server lets one workstation lose its network
// while the other keeps working. No HTTP responses or API data are mocked.
async function networkLink(port) {
  const sockets = new Set();
  let online = true;
  let delay = 0;
  const listener = createServer((socket) => {
    if (!online) { socket.destroy(); return; }
    const upstream = connect(port, '127.0.0.1');
    for (const peer of [socket, upstream]) {
      sockets.add(peer);
      peer.on('error', () => { socket.destroy(); upstream.destroy(); });
      peer.on('close', () => { sockets.delete(peer); socket.destroy(); upstream.destroy(); });
    }
    socket.pipe(upstream);
    upstream.on('data', (data) => {
      if (delay) setTimeout(() => { if (!socket.destroyed) socket.write(data); }, delay);
      else socket.write(data);
    });
    upstream.on('end', () => socket.end());
  });
  const localPort = await listen(listener);
  return {
    port: localPort,
    setOnline(value) {
      online = value;
      if (!online) for (const socket of sockets) socket.destroy();
    },
    setDelay(milliseconds) { delay = milliseconds; },
    async close() {
      for (const socket of sockets) socket.destroy();
      await new Promise((done) => listener.close(done));
    }
  };
}

async function program(variable, args, directory, baseURL) {
  if (!process.env[variable]) throw new Error(`Set ${variable} to the built program.`);
  const binary = resolve(process.env[variable]);
  await access(binary, constants.X_OK);
  let log = '';
  const child = spawn(binary, args, {
    cwd: directory, env: { ...process.env, TAM_DATA_DIR: directory, TAM_PWD: 'browser-event-password' },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  const exited = new Promise((done) => child.once('close', done));
  let startupError;
  child.on('error', (error) => { startupError = error; });
  const collect = (chunk) => {
    log += chunk.toString();
    baseURL ||= log.match(/http:\/\/127\.0\.0\.1:\d+\//)?.[0];
  };
  child.stdout.on('data', collect);
  child.stderr.on('data', collect);
  const stop = () => child.kill('SIGTERM');
  process.once('SIGTERM', stop);
  process.once('SIGINT', stop);
  const close = async () => {
    process.removeListener('SIGTERM', stop);
    process.removeListener('SIGINT', stop);
    if (child.exitCode === null && child.signalCode === null) {
      stop();
      const timer = setTimeout(() => child.kill('SIGKILL'), 5_000);
      await exited;
      clearTimeout(timer);
    }
  };
  try {
    await expect.poll(async () => {
      if (startupError) throw startupError;
      if (child.exitCode !== null) throw new Error(`${variable} stopped: ${log}`);
      try { return baseURL ? (await fetch(new URL('/api', baseURL))).status : 0; }
      catch { return 0; }
    }, { timeout: 15_000, message: `${variable} starts` }).toBe(200);
    return { baseURL, log: () => log, close };
  } catch (error) {
    await close();
    throw new Error(`${error.message}\n${log}`);
  }
}

export const test = base.extend({
  event: async ({ request }, use, testInfo) => {
    const directories = [];
    const programs = [];
    const links = [];
    const directory = async () => {
      const path = await mkdtemp(join(tmpdir(), 'tam-browser-event-'));
      directories.push(path);
      return path;
    };
    try {
      // tam-server prints its configured address, so reserve an available port
      // before starting it; readiness below confirms the actual API listener.
      const reservation = createServer();
      const port = await listen(reservation);
      await new Promise((done) => reservation.close(done));
      const server = await program('TAM_SERVER_BIN', ['-addr', `127.0.0.1:${port}`, '-announce=false', '-tray=false'], await directory(), `http://127.0.0.1:${port}/`);
      programs.push(server);
      const clients = [];
      for (let index = 0; index < 2; index++) {
        const link = await networkLink(port);
        links.push(link);
        const client = await program('TAM_CLIENT_BIN', ['-addr', '127.0.0.1:0', '-open=false', '-tray=false'], await directory());
        programs.push(client);
        const paired = await request.post(new URL('/api/pair', client.baseURL).href, {
          data: { host: '127.0.0.1', port: String(link.port), password: 'browser-event-password' }
        });
        expect(paired.ok(), await paired.text()).toBeTruthy();
        clients.push({ ...client, link });
      }
      await expect.poll(async () => Promise.all(clients.map(async (client) => {
        const response = await request.get(new URL('/api/status', client.baseURL).href);
        const state = await response.json();
        return [state.state, state.pending, state.failed];
      })), { timeout: 15_000 }).toEqual([['connected', 0, 0], ['connected', 0, 0]]);
      await use({ a: clients[0], b: clients[1], server });
    } finally {
      if (testInfo.status !== testInfo.expectedStatus) {
        for (const [index, process] of programs.entries()) {
          await testInfo.attach(`tam-event-${index}.log`, { body: process.log(), contentType: 'text/plain' });
        }
      }
      for (const process of programs.reverse()) await process.close();
      for (const link of links) await link.close();
      for (const path of directories) await rm(path, { recursive: true, force: true });
    }
  }
});
