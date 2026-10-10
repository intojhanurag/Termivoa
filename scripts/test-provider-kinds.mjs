// Development-only React effect regression, no broker, pairing or user data.
// pnpm install --frozen-lockfile && CHROME=<installed Chromium> node scripts/test-provider-kinds.mjs
import { once } from 'node:events';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import { Browser, CHROME, REPO, freePort, until } from './e2e-multicomputer/lib.mjs';

if (!existsSync(CHROME)) throw new Error('Set CHROME to an existing Chromium executable; this test installs nothing.');
const root = mkdtempSync(join(tmpdir(), 'termivoa-provider-kinds-'));
let server;
let browser;
try {
  const port = await freePort();
  server = await createServer({ root: join(REPO, 'web'), configFile: join(REPO, 'web/vite.config.ts'), server: { host: '127.0.0.1', port, strictPort: true } });
  await server.listen();
  browser = await Browser.launch({ root, proxy: '127.0.0.1:1', spki: '' }, { extraArgs: ['--no-proxy-server'] });
  const page = await browser.page();
  await page.go(`http://127.0.0.1:${port}/tests/provider-kinds/index.html`);
  await page.front();
  await until(() => page.eval('typeof window.runProviderKindChecks === "function"'), { what: 'React fixture' });
  const results = await page.eval('window.runProviderKindChecks()');
  console.log(JSON.stringify({ browser: browser.product, results }, null, 2));
  if (results.some((one) => !one.passed)) throw new Error('Provider kind regression failed');
} finally {
  // The shared helper sends the close signal; this runner owns the profile
  // and waits for exit before removing it. A stopped child needs no wait.
  const exited = browser?.chrome.exitCode === null && browser.chrome.signalCode === null
    ? once(browser.chrome, 'exit').catch(() => undefined) : Promise.resolve();
  await browser?.close();
  let timer;
  try { await Promise.race([exited, new Promise((resolve) => { timer = setTimeout(resolve, 3000); })]); }
  finally { clearTimeout(timer); }
  await server?.close();
  rmSync(root, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
}
