// Draws the review pictures of the "Needs you" inbox into docs/evidence/needs-you-inbox/: the app's components
// (pages.tsx, demo data only) as static pages with the app's stylesheet, in headless Chrome at 390×844.
// It also checks that nothing is wider than the screen at 390 and 375 px. No server, no host, no network.
//   pnpm install --frozen-lockfile && CHROME=… node scripts/needs-you-pictures/capture.mjs
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, '../..');
const WEB = join(REPO, 'web');
const OUT = join(REPO, 'docs/evidence/needs-you-inbox');
const CHROME = process.env.CHROME ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const { createServer } = await import(pathToFileURL(createRequire(join(WEB, 'package.json')).resolve('vite')).href);
// The terminal itself is not drawn (it needs a browser): xterm, a CommonJS package, is left out of the server-side load.
const noXterm = { name: 'no-xterm', enforce: 'pre', resolveId: (id) => (id === '@xterm/xterm' ? '\0no-xterm' : undefined), load: (id) => (id === '\0no-xterm' ? 'export class Terminal {}' : undefined) };
const server = await createServer({
  root: WEB, configFile: join(WEB, 'vite.config.ts'), plugins: [noXterm], server: { middlewareMode: true }, appType: 'custom',
  resolve: { dedupe: ['react', 'react-dom'] }, ssr: { noExternal: ['@xterm/xterm'] }, logLevel: 'error',
});
const pages = (await server.ssrLoadModule(join(HERE, 'pages.tsx'))).html();
await server.close();

const css = readFileSync(join(WEB, 'src/styles/global.css'), 'utf8');
const work = mkdtempSync(join(tmpdir(), 'needs-you-'));
mkdirSync(OUT, { recursive: true });

// Headless Chrome over CDP, as scripts/e2e-multicomputer/lib.mjs does (that client is tied to its test network).
const chrome = spawn(CHROME, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${join(work, 'profile')}`, '--no-first-run', '--hide-scrollbars', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
const endpoint = await new Promise((done, fail) => {
  let seen = '';
  chrome.stderr.on('data', (chunk) => { seen += chunk; const found = /DevTools listening on (ws:\S+)/.exec(seen); if (found) done(found[1]); });
  setTimeout(() => fail(new Error('Chrome did not start')), 20000);
});
const socket = new WebSocket(endpoint);
await new Promise((done) => socket.addEventListener('open', done));
const pending = new Map();
socket.addEventListener('message', (event) => {
  const message = JSON.parse(event.data);
  const waiter = pending.get(message.id);
  if (waiter === undefined) return;
  pending.delete(message.id);
  if (message.error) waiter.fail(new Error(message.error.message)); else waiter.done(message.result);
});
let sent = 0;
const send = (method, params = {}, sessionId) => new Promise((done, fail) => {
  pending.set(++sent, { done, fail });
  socket.send(JSON.stringify({ id: sent, method, params, ...(sessionId ? { sessionId } : {}) }));
});
const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
const page = (method, params) => send(method, params, sessionId);
const evaluate = async (expression) => (await page('Runtime.evaluate', { expression, returnByValue: true })).result.value;
/** The right edge of the widest thing on the page: more than the screen means something sticks out. */
const WIDEST = `Math.max(document.documentElement.scrollWidth, ...[...document.querySelectorAll('.app *')].map((e) => Math.ceil(e.getBoundingClientRect().right)))`;

let failed = false;
for (const [name, body] of Object.entries(pages)) {
  const file = join(work, `${name}.html`);
  writeFileSync(file, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="dark"><style>${css}</style></head><body><div id="root">${body}</div></body></html>`);
  for (const width of [375, 390]) {
    await page('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 2, mobile: true });
    await page('Page.navigate', { url: pathToFileURL(file).href });
    for (let tries = 0; tries < 100 && await evaluate(`document.readyState === 'complete' && location.href.endsWith('${name}.html')`) !== true; tries++) await new Promise((done) => setTimeout(done, 50));
    const widest = await evaluate(WIDEST);
    const ok = widest <= width;
    failed ||= !ok;
    console.log(`${ok ? 'PASS' : 'FAIL'} ${name} at ${width} px: the widest element ends at ${widest} px`);
  }
  // The last load was at 390×844.
  writeFileSync(join(OUT, `${name}.png`), Buffer.from((await page('Page.captureScreenshot', { format: 'png' })).data, 'base64'));
}
await Promise.race([send('Browser.close'), new Promise((done) => setTimeout(done, 3000))]).catch(() => undefined);
chrome.kill();
rmSync(work, { recursive: true, force: true });
process.exit(failed ? 1 : 0);
