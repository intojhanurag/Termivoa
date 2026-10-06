// Takes the docs screenshots of the Termivoa phone app against an isolated dev stack.
//
// 1. Build the binary from main (normal HOME):
//      pnpm install --frozen-lockfile && pnpm build:web && ./scripts/embed-web.sh
//      go build -tags webdist -o /tmp/pterm-demo ./cmd/pterm
// 2. node capture.mjs   # rebuilds /tmp/demo-home and the repos in /Users/Shared/code (setup.mjs), restarts the dev stack on :8788
//                        # with HOME=/tmp/demo-home, adds the projects, takes PNGs in ./png,
//                        # then writes WebP next to this file
//    node capture.mjs convert                # only redo the WebP conversion
// 3. node setup.mjs clean   # stops the stack this script started (by the PID in /tmp/demo-home/.capture-pid),
//    then removes /Users/Shared/code and /tmp/demo-home
//
// The stand-in agent (agent.js) is neutral: no vendor name or logo.
import { execFileSync, spawn } from 'node:child_process';
import { copyFileSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
// One rule for Chrome (CHROME) and one sandbox for every pterm, shared with the e2e harness.
import { CHROME, sandboxed } from '../e2e-multicomputer/lib.mjs';
import { agentEdits, CODE, HOME, PIDFILE, PTERM, setup, stopStack } from './setup.mjs';

const BASE = 'http://127.0.0.1:8788';
const DIR = new URL('.', import.meta.url).pathname;
const PNG = `${DIR}png`;
const ENV = { ...process.env, HOME };
const crops = {};
// The PNG height in CSS px (PNG bytes 20..24 hold the pixel height; DPR is 2).
const innerHeightOf = (r) => Buffer.from(r.data, 'base64').readUInt32BE(20) / 2;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// Every pterm runs in the e2e harness sandbox: it hides a real herdr (and the real Tailscale CLI) on this computer.
const pterm = (...args) => execFileSync(...sandboxed(PTERM, [...args, '--dev']), { env: ENV }).toString();
let step = 'start';

async function browser(profile) {
  rmSync(profile, { recursive: true, force: true });
  const chrome = spawn(CHROME, ['--headless=new', '--remote-debugging-port=9333', `--user-data-dir=${profile}`, '--no-first-run', '--hide-scrollbars', 'about:blank'], { stdio: 'ignore' });
  let ws; let id = 0; const pending = new Map();
  for (let i = 0; i < 50 && !ws; i++) {
    try { const list = await (await fetch('http://127.0.0.1:9333/json/list')).json(); const page = list.find((t) => t.type === 'page'); if (page) ws = new WebSocket(page.webSocketDebuggerUrl); } catch {}
    await sleep(200);
  }
  if (ws.readyState !== 1) await new Promise((r) => ws.addEventListener('open', r));
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.rej(new Error(m.error.message)) : p.res(m.result); } });
  const send = (method, params = {}) => new Promise((res, rej) => { const n = ++id; pending.set(n, { res, rej }); ws.send(JSON.stringify({ id: n, method, params })); });
  const evaluate = async (expression) => (await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result?.value;
  const viewport = (height) => send('Emulation.setDeviceMetricsOverride', { width: 390, height, deviceScaleFactor: 2, mobile: true });
  const b = {
    chrome, send, evaluate, viewport,
    text: () => evaluate('document.body.innerText'),
    async waitText(t, ms = 20000) { const end = Date.now() + ms; while (Date.now() < end) { if (await evaluate(`document.body.innerText.toLowerCase().includes(${JSON.stringify(t.toLowerCase())})`)) return true; await sleep(250); } throw new Error(`timeout waiting for "${t}"`); },
    async waitGone(t, ms = 20000) { const end = Date.now() + ms; while (Date.now() < end) { if (!(await evaluate(`document.body.innerText.includes(${JSON.stringify(t)})`))) return true; await sleep(250); } throw new Error(`still showing "${t}"`); },
    // Clicks the last visible button or link whose text or aria-label is t (exact first, then contains).
    async click(t) {
      const ok = await evaluate(`(() => { const norm = (e) => e.innerText.replace(/\\s+/g,' ').trim(); const els = [...document.querySelectorAll('button, a')].filter(e => e.offsetParent !== null && (norm(e).includes(${JSON.stringify(t)}) || e.getAttribute('aria-label') === ${JSON.stringify(t)})); const exact = els.filter(e => norm(e) === ${JSON.stringify(t)} || e.getAttribute('aria-label') === ${JSON.stringify(t)}); const el = (exact.length ? exact : els).at(-1); if (!el) return false; el.click(); return true; })()`);
      if (!ok) throw new Error(`no button "${t}"`);
      await sleep(700);
    },
    async tryClick(t) { try { await b.click(t); return true; } catch { return false; } },
    // Opens a session row (list) by its exact title.
    async openRow(title) {
      const ok = await evaluate(`(() => { const t = [...document.querySelectorAll('.session-row .row-title')].find(e => e.firstChild?.textContent === ${JSON.stringify(title)}); t?.closest('button').click(); return !!t; })()`);
      if (!ok) throw new Error(`no row ${title}`);
      await sleep(2500);
    },
    async setField(sel, value, event = 'input') {
      await evaluate(`(() => { const el = document.querySelector(${JSON.stringify(sel)}); const proto = el.tagName === 'SELECT' ? HTMLSelectElement.prototype : el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, ${JSON.stringify(value)}); el.dispatchEvent(new Event(${JSON.stringify(event)}, { bubbles: true })); })()`);
      await sleep(300);
    },
    async selectByText(sel, text) {
      const value = await evaluate(`[...document.querySelector(${JSON.stringify(sel)}).options].find(o => o.textContent === ${JSON.stringify(text)})?.value`);
      await b.setField(sel, value, 'change');
    },
    // Types a command into the terminal through the hidden input, then Enter.
    async type(command) {
      await evaluate(`document.querySelector('.terminal-input').focus()`);
      await send('Input.insertText', { text: command });
      await sleep(200);
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' });
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
      await evaluate(`document.querySelector('.terminal-input').blur()`);
      await sleep(500);
    },
    // Taps text on the terminal screen, as a finger would (a path opens in Files).
    async tapTerminal(needle) {
      const point = await evaluate(`(() => { const walker = document.createTreeWalker(document.querySelector('.xterm-rows'), NodeFilter.SHOW_TEXT);
        for (let node = walker.nextNode(); node; node = walker.nextNode()) { const at = node.textContent.indexOf(${JSON.stringify(needle)}); if (at < 0) continue;
          const range = document.createRange(); range.setStart(node, at); range.setEnd(node, at + 8); const r = range.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; } return null; })()`);
      if (!point) throw new Error(`"${needle}" is not on the terminal screen`);
      await send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
      for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, button: 'left', clickCount: 1, ...point });
      await sleep(1500);
    },
    async control() { if (await b.tryClick('Take control')) await sleep(1200); },
    // crop: [selector, extra CSS px above, extra below] for a second, cropped image.
    async shot(name, crop) {
      // The tab strip does not scroll the open tab into view on its own; swipe it there like a user would.
      await evaluate(`document.querySelector('.tab.active')?.scrollIntoView({ inline: 'nearest', block: 'nearest' })`);
      await sleep(400);
      const r = await send('Page.captureScreenshot', { format: 'png' });
      writeFileSync(`${PNG}/${name}.png`, Buffer.from(r.data, 'base64'));
      if (crop) {
        const [selector, above = 0, below = 0, from = 'top'] = crop;
        const rect = await evaluate(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return { top: r.top, bottom: r.bottom }; })()`);
        const y = Math.max(0, Math.round((from === 'page' ? 0 : rect.top) - above));
        crops[name] = { y, h: Math.min(Math.round(rect.bottom + below), innerHeightOf(r)) - y };
        writeFileSync(`${PNG}/crops.json`, JSON.stringify(crops, null, 2));
      }
      console.log('shot', name);
    },
    async go(url) { await send('Page.navigate', { url }); await sleep(1500); },
  };
  await send('Page.enable'); await send('Runtime.enable');
  await viewport(844);
  return b;
}

async function pair(b, label, codeShot) {
  const link = pterm('pair', '--no-qr', '--no-wait').match(/http:\/\/\S+\/pair#\S+/)[0];
  await b.go(link);
  await b.setField('#device-label', label);
  await b.click('Pair this device');
  await sleep(1500);
  if (codeShot) { await b.waitText('Does your computer show this code?'); await b.shot(codeShot); }
  const id = pterm('pair', 'pending').split('\n')[1].split(/\s+/)[0];
  pterm('pair', 'approve', id);
  await b.waitText('Sessions');
  await sleep(1500);
  await b.tryClick('Not now');
}

async function newSession(b, project, runFirst) {
  await b.go(`${BASE}/new`);
  await b.waitText('start session');
  await b.selectByText('#project', project);
  if (runFirst) await b.setField('#run-first', runFirst);
  await b.click('start session');
  await sleep(3500);
}

// A clean demo HOME and dev stack with the three projects.
async function freshStack() {
  await stopStack(); // only the stack an earlier run of this script recorded
  setup();
  const up = spawn(...sandboxed(PTERM, ['up', '--dev']), { env: ENV, cwd: HOME, detached: true, stdio: 'ignore' });
  writeFileSync(PIDFILE, `${up.pid}\n`);
  up.unref();
  for (let i = 0; i < 50; i++) { try { await fetch(BASE); break; } catch { await sleep(200); } }
  await sleep(1000);
  for (const name of ['acme-web', 'billing-api', 'docs-site']) execFileSync(...sandboxed(PTERM, ['project', 'add', '--dev', '--name', name, `${CODE}/${name}`]), { env: ENV });
}

async function capture() {
  rmSync(PNG, { recursive: true, force: true });
  execFileSync('mkdir', ['-p', PNG]);
  writeFileSync(`${PNG}/crops.json`, '{}');

  // A second device starts three plain sessions, so the phone has a list right after pairing.
  step = 'helper device';
  const helper = await browser('/tmp/demo-chrome-helper');
  try {
    await pair(helper, 'iPad');
    // Demo data only (AGENTS.md rule 23): stop when the stack sees a provider (herdr) of this computer.
    // Fails closed: a read that is not 200, or lists no provider, proves nothing.
    const { status, providers } = await helper.evaluate(`fetch('/api/v1/settings', { credentials: 'include' }).then(async (r) => ({ status: r.status, providers: r.ok ? ((await r.json()).providers ?? []).map((p) => p.state) : [] }))`) ?? { status: 'no answer', providers: [] };
    if (status !== 200 || providers.length === 0) throw new Error(`the demo-data check could not read the providers (GET /api/v1/settings: HTTP ${status}, ${providers.length} providers); no screenshot taken`);
    if (providers.some((state) => state !== 'notInstalled')) throw new Error(`the demo stack sees a provider of this computer (${providers}); the sandbox must hide it`);
    await newSession(helper, 'docs-site', 'git log --oneline -3');
    await newSession(helper, 'billing-api', 'git status -sb');
    await newSession(helper, 'acme-web', 'git log --oneline -5');
    await helper.go(`${BASE}/`);
  } finally { helper.chrome.kill(); }
  await sleep(4000);

  const b = await browser('/tmp/demo-chrome-phone');
  try {
    step = 'pair-done';
    await pair(b, 'iPhone', 'pair-code');
    await b.waitText('connected');
    await b.shot('pair-done');

    step = 'project-buttons';
    await b.openRow('acme-web');
    await b.control();
    await b.click('snips');
    await b.waitText('This project');
    await sleep(1000);
    await b.shot('project-buttons', ['.sheet', 0, 0]);
    await b.click('Close');

    step = 'worktree-sheet';
    await b.click('Session menu');
    await b.click('New worktree…');
    await b.waitText('Repository');
    await b.setField('#worktree-name', 'fix-cache-headers');
    await sleep(600);
    await b.shot('worktree-sheet', ['.sheet', 0, 0]);
    await b.click('Create');
    await sleep(4000);
    await b.control();
    await b.type('git log --oneline -3');
    await sleep(1000);

    // Files in the worktree session: the list at the top, then a path tapped in the terminal.
    step = 'files';
    await b.click('changes');
    await b.click('Files');
    await b.waitText('package.json');
    await b.evaluate(`document.activeElement?.blur()`);
    await sleep(1000);
    await b.shot('files-list');
    await b.click('Done');
    await b.type('clear');
    await b.type('grep -rn --color=never TTL_MS src');
    await sleep(1000);
    await b.tapTerminal('src/lib/cache.ts:11');
    await b.waitText('Ask agent about this file');
    await b.evaluate(`document.activeElement?.blur()`);
    await sleep(1200);
    await b.shot('files-viewer');
    await b.click('Back to files');
    await b.click('Done');

    step = 'live-terminal';
    await b.go(`${BASE}/`);
    await b.openRow('billing-api');
    await b.control();
    await b.type('agent work');
    await sleep(6000);
    await b.shot('live-terminal');

    step = 'keyboard-row';
    await b.evaluate(`document.querySelector('.terminal-input').focus()`);
    await b.viewport(480);
    await sleep(2000);
    await b.shot('keyboard-row', ['.dock.compact', 12, 0]);
    await b.evaluate(`document.querySelector('.terminal-input').blur()`);
    await b.viewport(844);
    await sleep(1000);

    // The agent in acme-web works for 70 s while the phone is away, then asks.
    step = 'start asking agent';
    await b.go(`${BASE}/`);
    await b.openRow('acme-web');
    await b.control();
    await b.type('agent ask 70');
    await sleep(3000);
    await b.go(`${BASE}/`);
    agentEdits();
    await b.waitText('Waiting', 90000);
    await sleep(3000);

    step = 'sessions';
    await b.go(`${BASE}/`);
    await sleep(2000);
    await b.shot('sessions');

    step = 'wall';
    await b.click('Wall');
    await sleep(4000);
    await b.shot('wall');
    await b.click('Session list');

    step = 'recap';
    await b.openRow('acme-web');
    await b.waitText('Since you left', 10000);
    await sleep(800);
    await b.shot('recap', ['.recap-card', 0, 12, 'page']);

    step = 'answer-card';
    await b.click('Take control to answer');
    await b.waitGone('Since you left', 15000);
    await sleep(500);
    await b.shot('answer-card', ['.answer-card', 0, 0]);

    step = 'changes';
    await b.click('changes');
    await b.waitText('Commit 3 files');
    await sleep(800);
    await b.shot('changes-list');
    await b.evaluate(`[...document.querySelectorAll('.change-list button')].find(e => e.innerText.includes('cache.ts')).click()`);
    await sleep(1500);
    await b.shot('changes-diff');
    await b.click('Back to changed files');
    await b.click('Commit 3 files');
    await b.setField('#commit-message', 'feat: rate limit the /api routes');
    await b.evaluate(`document.activeElement?.blur()`);
    await b.shot('commit', ['.sheet', 0, 0]);
    await b.click('Cancel');
    await b.click('Done');

    step = 'read-only';
    await b.go(`${BASE}/`);
    await b.openRow('billing-api');
    await b.waitText('You are watching');
    await sleep(1500);
    await b.tryClick('Dismiss');
    await b.shot('read-only');

    // A dev server and a working agent in docs-site: the dev-server preview (landing page).
    step = 'preview';
    await b.go(`${BASE}/`);
    await b.openRow('docs-site');
    await b.control();
    await b.type('python3 -m http.server 4321 -b 127.0.0.1 -d public >/dev/null 2>&1 & agent work');
    await b.waitText('Preview ready', 30000);
    await b.click('Open preview of port 4321');
    await sleep(4000);
    await b.shot('preview');
  } catch (e) {
    console.log('ERROR at', step, e.message);
    await b.shot('fail').catch(() => {});
  } finally { b.chrome.kill(); }
}

// PNG to WebP (quality 0.82) through a canvas in headless Chrome; no other tools needed.
async function convert() {
  const CROPS = JSON.parse(readFileSync(`${PNG}/crops.json`, 'utf8'));
  const b = await browser('/tmp/demo-chrome-convert');
  try {
    for (const file of readdirSync(PNG).filter((f) => f.endsWith('.png') && f !== 'fail.png')) {
      const name = file.replace(/\.png$/, '');
      const data = readFileSync(`${PNG}/${file}`).toString('base64');
      const jobs = [[name, undefined]];
      if (CROPS[name]) jobs.push([`${name}-crop`, CROPS[name]]);
      for (const [out, crop] of jobs) {
        const webp = await b.evaluate(`(async () => {
          const img = new Image(); img.src = 'data:image/png;base64,${data}'; await img.decode();
          const c = ${JSON.stringify(crop ?? null)};
          const sx = 0, sy = c ? c.y * 2 : 0, w = img.width, h = c ? Math.min(c.h * 2, img.height - sy) : img.height;
          const canvas = document.createElement('canvas'); canvas.width = w; canvas.height = h;
          canvas.getContext('2d').drawImage(img, sx, sy, w, h, 0, 0, w, h);
          return canvas.toDataURL('image/webp', 0.82).split(',')[1];
        })()`);
        writeFileSync(`${DIR}${out}.webp`, Buffer.from(webp, 'base64'));
        console.log('webp', out);
      }
    }
  } finally { b.chrome.kill(); }
}

// The landing page pictures in site/public/assets: three app screens from this run, and
// the lock screen (_lock.html) and the social card (_og.html) rendered from the site itself.
async function landing() {
  const site = new URL('../../site/public/', import.meta.url).pathname;
  for (const [from, to] of [['answer-card', 'answer'], ['sessions', 'sessions'], ['preview', 'preview']]) copyFileSync(`${DIR}${from}.webp`, `${site}assets/${to}.webp`);
  const types = { html: 'text/html', css: 'text/css', woff2: 'font/woff2', webp: 'image/webp', svg: 'image/svg+xml' };
  const server = createServer((req, res) => {
    try { const path = decodeURIComponent(new URL(req.url, 'http://x').pathname); if (path.includes('..')) throw new Error('no');
      const body = readFileSync(site + path.slice(1)); res.writeHead(200, { 'Content-Type': types[path.split('.').pop()] ?? 'application/octet-stream' }); res.end(body); } catch { res.writeHead(404); res.end(); }
  }).listen(0, '127.0.0.1');
  await new Promise((r) => server.once('listening', r));
  const at = `http://127.0.0.1:${server.address().port}`;
  const b = await browser('/tmp/demo-chrome-landing');
  try {
    await b.go(`${at}/_lock.html`);
    await b.evaluate('document.fonts.ready.then(() => true)');
    const lock = (await b.send('Page.captureScreenshot', { format: 'webp', quality: 85 })).data;
    writeFileSync(`${site}assets/lock.webp`, Buffer.from(lock, 'base64'));
    await b.send('Emulation.setDeviceMetricsOverride', { width: 1200, height: 630, deviceScaleFactor: 1, mobile: false });
    await b.go(`${at}/_og.html`);
    await b.evaluate('Promise.all([document.fonts.ready, ...[...document.images].map((i) => i.decode())]).then(() => true)');
    const og = (await b.send('Page.captureScreenshot', { format: 'png' })).data;
    writeFileSync(`${site}assets/og.png`, Buffer.from(og, 'base64'));
    console.log('landing: answer, sessions, preview, lock, og');
  } finally { b.chrome.kill(); server.close(); }
}

if (process.argv[2] === 'landing') { await landing(); process.exit(0); }
if (process.argv[2] !== 'convert') { await freshStack(); await capture(); }
await convert();
process.exit(0);
