#!/usr/bin/env node
// Manual coding-agent harness (P7). Not run by any test runner or CI.
// It plays the browser: HTTP pairing, session creation and one WebSocket
// with a snapshot/patch screen model. A small loopback control server lets a
// human (or script) drive input and read the reconstructed screen.
//
//   node tests/agents/driver.mjs pair            # pair a browser credential
//   node tests/agents/driver.mjs create          # create a zsh session
//   node tests/agents/driver.mjs serve [--control]
//   curl localhost:$CTRL_PORT/screen             # see docs/evidence/p7/coding-agents.md
//
// Env: PT_BASE (http://127.0.0.1:8797), PT_BIN, PT_RUNTIME, PT_STATE (json
// file), CTRL_PORT (8798), PT_PROJECT, PT_PROFILE.
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { readFileSync, writeFileSync, appendFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const BASE = process.env.PT_BASE ?? 'http://127.0.0.1:8797';
const BIN = process.env.PT_BIN ?? join(root, 'bin', 'pterm');
const RUNTIME = process.env.PT_RUNTIME ?? '/tmp/pt-agents/run';
const STATE = process.env.PT_STATE ?? '/tmp/pt-agents/state.json';
const CTRL_PORT = Number(process.env.CTRL_PORT ?? 8798);
const LOG = process.env.PT_LOG ?? '/tmp/pt-agents/logs/driver.jsonl';

const loadState = () => { try { return JSON.parse(readFileSync(STATE, 'utf8')); } catch { return {}; } };
const saveState = (patch) => writeFileSync(STATE, JSON.stringify({ ...loadState(), ...patch }, null, 2));
// Flags go after the command words and before any ID or URL.
const pterm = (words, rest = []) => execFileSync(BIN, [...words, '--dev', '--runtime-dir', RUNTIME, ...rest], { encoding: 'utf8' });

async function http(method, path, body, cookie) {
  const headers = { Origin: BASE };
  if (body !== undefined) Object.assign(headers, { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
  if (cookie) headers.Cookie = cookie;
  const res = await fetch(BASE + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text}`);
  return { json: text ? JSON.parse(text) : undefined, setCookie: res.headers.getSetCookie() };
}

async function pair() {
  const out = pterm(['pair'], ['--origin', BASE, '--no-qr']);
  const grant = out.match(/#([A-Za-z0-9_-]{43,128})/)?.[1];
  if (!grant) throw new Error('no grant in: ' + out);
  const { json: redeem } = await http('POST', '/api/v1/pair/redeem', { grant, deviceLabel: 'agents-harness' });
  console.log('verification code', redeem.verificationCode);
  const pending = pterm(['pair', 'pending']);
  if (!pending.includes(redeem.pendingId)) throw new Error('pending id not listed: ' + pending);
  pterm(['pair', 'approve'], [redeem.pendingId]);
  for (let i = 0; i < 20; i++) {
    const { json, setCookie } = await http('POST', '/api/v1/pair/poll', { pendingId: redeem.pendingId, pollSecret: redeem.pollSecret });
    if (json.status === 'approved') {
      const cookie = setCookie.map((c) => c.split(';')[0]).find((c) => c.startsWith('pterm_dev_session='));
      saveState({ cookie });
      console.log('paired', json.device?.id ?? '');
      return;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error('pairing never approved');
}

async function create() {
  const { cookie } = loadState();
  const { json } = await http('POST', '/api/v1/sessions', {
    operationId: randomUUID(), projectId: process.env.PT_PROJECT, shellProfileId: process.env.PT_PROFILE,
  }, cookie);
  saveState({ sessionId: json.session.id, generation: json.session.generation });
  console.log(JSON.stringify(json.session));
}

// ---------------------------------------------------------------- live view
const require = createRequire(join(root, 'web', 'package.json'));
const Ajv2020 = require('ajv/dist/2020').default;
const ajv = new Ajv2020({ allErrors: true, strict: false });
const validate = ajv.compile(JSON.parse(readFileSync(join(root, 'protocol/v1/envelope.schema.json'), 'utf8')));

const view = {
  ws: undefined, lease: undefined, seq: 1n, meta: undefined, rows: [], revision: undefined,
  pending: undefined, wantControl: false, events: [], frames: [], invalid: 0, log: [], lastHistory: undefined,
  connectedAt: 0, closed: true,
};

function record(kind, data) {
  const entry = { t: Date.now(), kind, ...data };
  view.log.push(entry);
  if (view.log.length > 500) view.log.shift();
  appendFileSync(LOG, JSON.stringify(entry) + '\n');
}

function send(type, payload, scoped = true) {
  const { sessionId, generation } = loadState();
  const msg = { version: 1, type, requestId: randomUUID(), ...(scoped ? { sessionId, generation } : {}), payload };
  if (!validate(msg)) record('client-invalid', { type, errors: validate.errors });
  view.ws.send(JSON.stringify(msg));
  return msg.requestId;
}

function input(type, fields) {
  if (!view.lease) throw new Error('no lease');
  const inputSequence = (view.seq++).toString(10);
  send(type, { lease: view.lease, inputSequence, ...fields });
  return inputSequence;
}

const MODS = { shift: false, alt: false, ctrl: false, meta: false };

function connect() {
  const { cookie } = loadState();
  const ws = new WebSocket(BASE.replace(/^http/, 'ws') + '/api/v1/ws', { headers: { Cookie: cookie, Origin: BASE } });
  view.ws = ws; view.lease = undefined; view.seq = 1n; view.closed = false; view.connectedAt = Date.now();
  ws.onopen = () => {
    record('open', {});
    send('hello', { clientBuild: 'agents-harness', clientKind: 'browser', supportedVersions: [1], capabilityProfile: 'phone-terminal-v1' }, false);
  };
  ws.onclose = (e) => { view.closed = true; record('close', { code: e.code, reason: e.reason }); };
  ws.onerror = (e) => record('ws-error', { message: String(e.message ?? e) });
  ws.onmessage = (e) => onMessage(String(e.data));
}

function onMessage(text) {
  const now = Date.now();
  let msg;
  try { msg = JSON.parse(text); } catch { record('bad-json', { text: text.slice(0, 200) }); return; }
  if (!validate(msg)) { view.invalid++; record('host-invalid', { type: msg.type, errors: validate.errors?.slice(0, 3) }); }
  view.frames.push({ t: now, type: msg.type, bytes: Buffer.byteLength(text), rows: msg.type === 'screen.patch' ? msg.payload.rows.length : undefined });
  if (view.frames.length > 200000) view.frames.splice(0, 100000);
  const p = msg.payload;
  switch (msg.type) {
    case 'welcome': send('session.attach', {}); break;
    case 'snapshot.begin':
      view.pending = { id: p.snapshotId, revision: p.revision, meta: p.screen, rows: new Array(p.screen.rows) };
      break;
    case 'snapshot.rows':
      if (view.pending?.id === p.snapshotId) p.rows.forEach((row, i) => { view.pending.rows[p.startRow + i] = row; });
      break;
    case 'snapshot.end': {
      const s = view.pending;
      if (!s || s.id !== p.snapshotId) break;
      view.meta = s.meta; view.rows = s.rows; view.revision = s.revision; view.pending = undefined;
      record('snapshot', { revision: s.revision, cols: s.meta.columns, rows: s.meta.rows, buffer: s.meta.buffer, filled: s.rows.filter(Boolean).length });
      send('snapshot.applied', { snapshotId: p.snapshotId, revision: s.revision });
      if (view.wantControl && !view.lease) send('control.acquire', {});
      break;
    }
    case 'screen.patch':
      if (view.pending) break;
      if (p.baseRevision !== view.revision) {
        record('patch-gap', { base: p.baseRevision, have: view.revision });
        send('screen.resync', { reason: 'base-mismatch', lastRevision: view.revision });
        break;
      }
      if (p.screen.rows !== view.rows.length) view.rows.length = p.screen.rows;
      for (const { index, row } of p.rows) view.rows[index] = row;
      view.meta = p.screen; view.revision = p.revision;
      break;
    case 'control.changed':
      if (p.lease) view.lease = p.lease;
      else if (view.lease && p.controllerConnectionId === null) view.lease = undefined;
      record('control.changed', { controller: p.controllerConnectionId, epoch: p.leaseEpoch, gotLease: !!p.lease, label: p.controllerLabel });
      break;
    case 'history.page':
      view.lastHistory = { ...p, text: p.rows.map(rowText) };
      record('history.page', { epoch: p.historyEpoch, revision: p.historyRevision, offset: p.offset, count: p.rows.length, next: p.nextOffset, complete: p.complete });
      break;
    case 'input.accepted': break;
    default:
      record(msg.type, { payload: p });
  }
}

function rowText(row) {
  if (!row) return '';
  let out = '';
  let at = 0; // display column reached so far (wide cells count 2)
  for (const run of row.runs) {
    if (run.column > at) { out += ' '.repeat(run.column - at); at = run.column; }
    for (const cell of run.cells) {
      if (cell.continuation) continue;
      out += cell.text === '' ? ' ' : cell.text;
      at += cell.width || 1;
    }
  }
  return out.replace(/\s+$/, '');
}

function screenText() {
  const m = view.meta;
  const lines = view.rows.map((r, i) => `${String(i).padStart(2, '0')}|${rowText(r)}`);
  return `rev=${view.revision} ${m?.columns}x${m?.rows} buffer=${m?.buffer} cursor=${m && `${m.cursor.row},${m.cursor.column} visible=${m.cursor.visible} ${m.cursor.style}`} lease=${!!view.lease} closed=${view.closed} width=${m?.widthMethod} history=${m?.historyEpoch}/${m?.historyRevision}\n${lines.join('\n')}\n`;
}

function styleSummary() {
  const colors = new Map();
  let wide = 0, emoji = 0, box = 0, cells = 0, bold = 0, reverse = 0;
  for (const row of view.rows) for (const run of row?.runs ?? []) {
    const s = run.style;
    const key = `fg=${s.foreground.r},${s.foreground.g},${s.foreground.b},${s.foreground.a} bg=${s.background.r},${s.background.g},${s.background.b},${s.background.a}`;
    colors.set(key, (colors.get(key) ?? 0) + run.cells.length);
    if (s.bold) bold += run.cells.length;
    if (s.reverse) reverse += run.cells.length;
    for (const c of run.cells) {
      cells++;
      if (c.width === 2) wide++;
      if (/\p{Extended_Pictographic}/u.test(c.text)) emoji++;
      if (/[─-╿▀-▟]/.test(c.text)) box++;
    }
  }
  return { cells, wide, emoji, box, bold, reverse, styles: Object.fromEntries([...colors].sort((a, b) => b[1] - a[1]).slice(0, 12)) };
}

function stats(since) {
  const frames = view.frames.filter((f) => f.t >= since);
  const byType = {};
  for (const f of frames) { byType[f.type] ??= { n: 0, bytes: 0 }; byType[f.type].n++; byType[f.type].bytes += f.bytes; }
  const span = frames.length ? (frames.at(-1).t - frames[0].t) / 1000 : 0;
  const patches = frames.filter((f) => f.type === 'screen.patch');
  const perSec = new Map();
  for (const f of patches) perSec.set(Math.floor(f.t / 1000), (perSec.get(Math.floor(f.t / 1000)) ?? 0) + 1);
  const bytes = frames.reduce((a, f) => a + f.bytes, 0);
  const patchRows = patches.reduce((a, f) => a + (f.rows ?? 0), 0);
  return { since, spanSeconds: span, patchRows, bytesPerPatchRow: patchRows ? Math.round(patches.reduce((a, f) => a + f.bytes, 0) / patchRows) : 0, frames: frames.length, bytes, byType, maxPatchesPerSecond: Math.max(0, ...perSec.values()), avgBytesPerSecond: span ? Math.round(bytes / span) : 0, invalidHostMessages: view.invalid };
}

async function serve(control) {
  view.wantControl = control;
  connect();
  setInterval(() => { if (!view.closed && view.lease !== undefined) send('heartbeat', { lease: view.lease }); else if (!view.closed && view.revision) send('heartbeat', {}); }, 5000);
  createServer(async (req, res) => {
    const url = new URL(req.url, 'http://x');
    let body = '';
    for await (const chunk of req) body += chunk;
    const args = body ? JSON.parse(body) : {};
    const reply = (value) => { res.setHeader('Content-Type', typeof value === 'string' ? 'text/plain' : 'application/json'); res.end(typeof value === 'string' ? value : JSON.stringify(value, null, 1) + '\n'); };
    try {
      switch (url.pathname) {
        case '/screen': return reply(screenText());
        case '/styles': return reply(styleSummary());
        case '/row': return reply(view.rows[Number(url.searchParams.get('i'))] ?? null);
        case '/meta': return reply(view.meta ?? null);
        case '/log': return reply(view.log.filter((e) => e.t >= Number(url.searchParams.get('since') ?? 0)));
        case '/stats': return reply(stats(Number(url.searchParams.get('since') ?? 0)));
        case '/now': return reply({ t: Date.now() });
        case '/text': return reply({ seq: input('input.text', { text: args.text }) });
        case '/mouse': return reply({ seq: input('input.mouse', { kind: args.kind ?? 'wheel', button: args.button ?? 'wheel-up', column: args.column ?? 10, row: args.row ?? 5, modifiers: MODS }) });
        case '/paste': return reply({ seq: input('input.paste', { text: args.text }) });
        case '/key': return reply({ seq: input('input.key', { key: args.key, ...(args.character ? { character: args.character } : {}), modifiers: { ...MODS, ...args.modifiers } }) });
        case '/resize': send('terminal.resize', { lease: view.lease, columns: args.columns, rows: args.rows }); return reply({ ok: true });
        case '/history': {
          view.lastHistory = undefined;
          send('history.read', { historyEpoch: view.meta.historyEpoch, historyRevision: view.meta.historyRevision, offset: args.offset ?? 0, limit: args.limit ?? 200 });
          for (let i = 0; i < 40 && !view.lastHistory; i++) await new Promise((r) => setTimeout(r, 50));
          return reply(view.lastHistory ?? { none: true, log: view.log.slice(-3) });
        }
        case '/close': view.ws.close(); return reply({ ok: true });
        case '/reconnect': {
          const before = screenText();
          if (!view.closed) view.ws.close();
          await new Promise((r) => setTimeout(r, Number(args.gapMs ?? 300)));
          view.revision = undefined; connect();
          for (let i = 0; i < 100 && view.revision === undefined; i++) await new Promise((r) => setTimeout(r, 50));
          return reply({ before, after: screenText() });
        }
        case '/acquire': send('control.acquire', {}); return reply({ ok: true });
        case '/detach': send('session.detach', {}); return reply({ ok: true });
        default: res.statusCode = 404; return reply('unknown');
      }
    } catch (error) { res.statusCode = 500; reply(String(error)); }
  }).listen(CTRL_PORT, '127.0.0.1', () => console.log('control on', CTRL_PORT));
}

const [command, flag] = process.argv.slice(2);
if (command === 'pair') await pair();
else if (command === 'create') await create();
else if (command === 'serve') await serve(flag === '--control');
else { console.error('usage: driver.mjs pair|create|serve [--control]'); process.exit(2); }
