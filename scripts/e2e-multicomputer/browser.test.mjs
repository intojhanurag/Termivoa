// Test-only browser ownership; Node built-ins, synthetic children and profiles.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { EventEmitter, once } from 'node:events';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { Browser, CHROME } from './lib.mjs';

class Socket extends EventTarget {
  readyState = 1;
  closeCount = 0;
  closeRequests = 0;
  constructor(onCloseRequest = () => undefined) { super(); this.onCloseRequest = onCloseRequest; }
  send(text) {
    if (this.readyState !== 1) throw new Error('Socket is closed');
    const message = JSON.parse(text);
    if (message.method !== 'Browser.close') return;
    this.closeRequests++;
    this.onCloseRequest();
    queueMicrotask(() => this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify({ id: message.id, result: {} }) })));
  }
  close() { this.closeCount++; this.readyState = 3; this.dispatchEvent(new Event('close')); }
}

const exited = (child) => child.exitCode !== null || child.signalCode !== null;
async function stopOwned(child) {
  if (exited(child)) return;
  const done = once(child, 'exit'); child.kill('SIGKILL'); await done;
}
// Also keep a failing baseline test from leaving its old 60-second timers.
function clearBaselinePending(browser) {
  for (const item of browser.pending.values()) { clearTimeout(item.timer); item.rej(new Error('Fixture cleanup')); }
  browser.pending.clear();
}
async function delayedChild(root, { ignoreTerm = false } = {}) {
  const program = `
    import { writeFileSync } from 'node:fs';
    let ending = false;
    const finish = () => { if (ending) return; ending = true;
      setTimeout(() => { writeFileSync(${JSON.stringify(join(root, 'last-write'))}, 'done'); process.exit(0); }, 200); };
    process.on('message', message => { if (message === 'close') finish(); });
    process.on('SIGTERM', ${ignoreTerm ? '() => undefined' : 'finish'});
    process.send('ready');
    setInterval(() => {}, 1000);
  `;
  const child = spawn(process.execPath, ['--input-type=module', '-e', program], { stdio: ['ignore', 'ignore', 'inherit', 'ipc'] });
  await once(child, 'message'); return child;
}

test('close waits for delayed real child exit and is shared by concurrent callers', { timeout: 10_000 }, async () => {
  const root = mkdtempSync(join(tmpdir(), 'termivoa-browser-delayed-'));
  const child = await delayedChild(root);
  const socket = new Socket(() => child.send('close'));
  const browser = new Browser(child, socket, 'fixture');
  try {
    await Promise.all([browser.close(), browser.close()]);
    assert.equal(child.exitCode, 0, 'close resolved before the child exited');
    assert.equal(readFileSync(join(root, 'last-write'), 'utf8'), 'done');
    assert.equal(socket.closeRequests, 1);
    assert.equal(socket.closeCount, 1);
    assert.equal(browser.pending.size, 0);
    rmSync(root, { recursive: true, force: true });
  } finally { clearBaselinePending(browser); await stopOwned(child); rmSync(root, { recursive: true, force: true }); }
});

test('close waits after SIGTERM when the graceful request did not end the child', { timeout: 10_000 }, async () => {
  const root = mkdtempSync(join(tmpdir(), 'termivoa-browser-term-'));
  const child = await delayedChild(root);
  const browser = new Browser(child, new Socket(), 'fixture');
  try {
    await browser.close();
    assert.equal(child.exitCode, 0, 'SIGTERM acknowledgement is not process exit');
    assert.equal(readFileSync(join(root, 'last-write'), 'utf8'), 'done');
  } finally { clearBaselinePending(browser); await stopOwned(child); rmSync(root, { recursive: true, force: true }); }
});

test('close escalates only its own child to SIGKILL when SIGTERM is ignored', { timeout: 10_000 }, async () => {
  const root = mkdtempSync(join(tmpdir(), 'termivoa-browser-kill-'));
  const child = await delayedChild(root, { ignoreTerm: true });
  const browser = new Browser(child, new Socket(), 'fixture');
  try {
    await browser.close(); assert.equal(child.signalCode, 'SIGKILL');
  } finally { clearBaselinePending(browser); await stopOwned(child); rmSync(root, { recursive: true, force: true }); }
});

test('close rejects outstanding calls, closes its socket and prevents new calls', async () => {
  const child = { exitCode: 0, signalCode: null, kill() { return false; } };
  const socket = new Socket(); const browser = new Browser(child, socket, 'fixture');
  const pending = browser.send('Runtime.never').then(() => null, error => error);
  try {
    await browser.close();
    assert.equal(browser.pending.size, 0);
    assert.match((await pending).message, /closed|closing/);
    assert.equal(socket.readyState, 3);
    await assert.rejects(browser.send('Runtime.afterClose'), /closed|closing/);
  } finally { clearBaselinePending(browser); }
});

test('socket close releases outstanding request timers without waiting for their deadline', async () => {
  const browser = new Browser({ exitCode: 0, signalCode: null }, new Socket(), 'fixture');
  const pending = browser.send('Runtime.never').then(() => null, error => error);
  try {
    browser.ws.close();
    assert.equal(browser.pending.size, 0);
    assert.match((await pending).message, /closed/);
  } finally { clearBaselinePending(browser); }
});

test('a synchronous send failure removes its pending timer', async () => {
  const socket = new Socket(); socket.send = () => { throw new Error('Fixture send failed'); };
  const browser = new Browser({ exitCode: 0, signalCode: null }, socket, 'fixture');
  try {
    await assert.rejects(browser.send('Runtime.fail'), /Fixture send failed/);
    assert.equal(browser.pending.size, 0);
  } finally { clearBaselinePending(browser); }
});

test('close observes a delayed owned WebSocket close event', async () => {
  const socket = new Socket();
  socket.close = () => {
    socket.closeCount++; socket.readyState = 2;
    setTimeout(() => { socket.readyState = 3; socket.dispatchEvent(new Event('close')); }, 100);
  };
  const browser = new Browser({ exitCode: 0, signalCode: null }, socket, 'fixture');
  await browser.close(); assert.equal(socket.readyState, 3); assert.equal(socket.closeCount, 1);
});

test('an unclosed WebSocket reports bounded teardown failure', { timeout: 3000 }, async () => {
  const socket = new Socket(); socket.close = () => { socket.readyState = 2; };
  const browser = new Browser({ exitCode: 0, signalCode: null }, socket, 'fixture');
  await assert.rejects(browser.close(), /WebSocket did not close/);
  assert.equal(browser.pending.size, 0);
});

test('an unreaped child reports bounded teardown failure instead of success', { timeout: 10_000 }, async () => {
  const child = Object.assign(new EventEmitter(), { exitCode: null, signalCode: null, signals: [], kill(signal) { this.signals.push(signal); return false; } });
  const browser = new Browser(child, new Socket(), 'fixture');
  try {
    await assert.rejects(browser.close(), /did not exit/);
    assert.deepEqual(child.signals, ['SIGTERM', 'SIGKILL']);
    assert.equal(browser.pending.size, 0);
    assert.equal(browser.ws.readyState, 3);
  } finally { clearBaselinePending(browser); }
});

test('installed Chromium can close then remove its owned profile immediately', { skip: !existsSync(CHROME) ? 'Set CHROME to an existing Chromium executable' : false, timeout: 15_000 }, async () => {
  const root = mkdtempSync(join(tmpdir(), 'termivoa-browser-installed-'));
  let browser;
  try {
    browser = await Browser.launch({ root, proxy: '127.0.0.1:1', spki: '' }, { extraArgs: ['--no-proxy-server'] });
    const version = await browser.send('Browser.getVersion'); assert.match(version.product, /Chrome|Chromium/);
    await browser.close();
    assert.equal(exited(browser.chrome), true);
    assert.equal(browser.pending.size, 0);
    assert.equal(browser.ws.readyState, 3);
    rmSync(root, { recursive: true, force: true });
    assert.equal(existsSync(root), false);
    console.log(`Chromium cleanup verified: ${version.product}`);
  } finally {
    if (browser) { clearBaselinePending(browser); await stopOwned(browser.chrome); }
    rmSync(root, { recursive: true, force: true });
  }
});
