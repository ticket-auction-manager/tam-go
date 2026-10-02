import { test as base, expect } from '@playwright/test';
import { spawn } from 'node:child_process';
import { access, mkdtemp, rm } from 'node:fs/promises';
import { constants } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

export { expect };

export const test = base.extend({
  client: [async ({}, use) => {
    if (!process.env.TAM_CLIENT_BIN) {
      throw new Error('Set TAM_CLIENT_BIN to a built tam-client with the web app embedded.');
    }
    const binary = resolve(process.env.TAM_CLIENT_BIN);
    await access(binary, constants.X_OK);
    const directory = await mkdtemp(join(tmpdir(), 'tam-browser-'));
    let log = '';
    let child;
    let exited;
    const terminate = () => child?.kill('SIGTERM');
    process.once('SIGTERM', terminate);
    process.once('SIGINT', terminate);
    try {
      child = spawn(binary, ['-addr', '127.0.0.1:0', '-open=false', '-tray=false'], {
        cwd: directory,
        env: { ...process.env, TAM_DATA_DIR: directory },
        stdio: ['ignore', 'pipe', 'pipe']
      });
      exited = new Promise((done) => child.once('close', done));
      const baseURL = await new Promise((ready, fail) => {
        const timeout = setTimeout(() => fail(new Error(`TAM client did not start:\n${log}`)), 15_000);
        const collect = (chunk) => {
          log += chunk.toString();
          const address = log.match(/http:\/\/127\.0\.0\.1:\d+\//);
          if (address) {
            clearTimeout(timeout);
            ready(address[0]);
          }
        };
        child.stdout.on('data', collect);
        child.stderr.on('data', collect);
        child.once('error', (error) => {
          clearTimeout(timeout);
          fail(error);
        });
        child.once('exit', (code, signal) => {
          clearTimeout(timeout);
          fail(new Error(`TAM client stopped (${code ?? signal}):\n${log}`));
        });
      });
      await expect.poll(async () => {
        try {
          return (await fetch(new URL('/api', baseURL))).status;
        } catch {
          return 0;
        }
      }, { message: 'TAM client API becomes ready' }).toBe(200);
      await use({ baseURL, log: () => log });
    } finally {
      process.removeListener('SIGTERM', terminate);
      process.removeListener('SIGINT', terminate);
      if (child && child.exitCode === null && child.signalCode === null) {
        child.kill('SIGTERM');
        const killTimer = setTimeout(() => child.kill('SIGKILL'), 5_000);
        await exited;
        clearTimeout(killTimer);
      }
      await rm(directory, { recursive: true, force: true });
    }
  }, { scope: 'worker' }],
  baseURL: async ({ client }, use) => use(client.baseURL),
  clientLog: [async ({ client }, use, testInfo) => {
    await use();
    if (testInfo.status !== testInfo.expectedStatus) {
      await testInfo.attach('tam-client.log', { body: client.log(), contentType: 'text/plain' });
    }
  }, { auto: true }]
});
