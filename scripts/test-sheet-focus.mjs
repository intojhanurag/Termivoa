// Development-only React effect regression, no broker, pairing or user data.
// pnpm install --frozen-lockfile && CHROME=<installed Chromium> node scripts/test-sheet-focus.mjs
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from '../web/node_modules/vite/dist/node/index.js';
import { Browser, CHROME, REPO, freePort, until } from './e2e-multicomputer/lib.mjs';

if (!existsSync(CHROME)) throw new Error('Set CHROME to an existing Chromium executable; this test installs nothing.');
const root = mkdtempSync(join(tmpdir(), 'termivoa-sheet-focus-'));
let server;
let browser;
try {
  const port = await freePort();
  server = await createServer({ root: join(REPO, 'web'), configFile: join(REPO, 'web/vite.config.ts'), server: { host: '127.0.0.1', port, strictPort: true } });
  await server.listen();
  browser = await Browser.launch({ root, proxy: '127.0.0.1:1', spki: '' }, { extraArgs: ['--no-proxy-server'] });
  const page = await browser.page();
  await page.go(`http://127.0.0.1:${port}/tests/sheet-focus/index.html`);
  await page.front();
  await until(() => page.eval('typeof window.runSheetFocusChecks === "function"'), { what: 'React fixture' });
  // A real browser keyboard event reproduces the user-facing defect before
  // the fixture checks close/unmount and edge cases through React effects.
  await page.eval('document.getElementById("opener").focus()');
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
  await until(() => page.eval('document.activeElement?.getAttribute("role") === "dialog"'), { what: 'sheet focus' });
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await until(() => page.eval('document.querySelector("[role=dialog]") === null'), { what: 'closed sheet' });
  const active = await page.eval('document.activeElement?.id || document.activeElement?.tagName');
  const results = await page.eval('window.runSheetFocusChecks()');
  console.log(JSON.stringify({ browser: browser.product, keyboardEscapeFocus: active, results }, null, 2));
  if (active !== 'opener' || results.some((result) => !result.passed)) process.exitCode = 1;
} finally {
  await browser?.close();
  await server?.close();
  rmSync(root, { recursive: true, force: true });
}
