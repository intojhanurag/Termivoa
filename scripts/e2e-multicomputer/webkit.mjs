// Phase B core flow in WebKit (README.md): pair with mac, add vps by paste,
// one Sessions list, a vps session with an echo round-trip.
//   PW=<playwright-core dir> [WEBKIT=<browser executable>] node scripts/e2e-multicomputer/webkit.mjs [--no-web-build] [--out <dir>]
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Env, interactive, sleep, until } from './lib.mjs';

const argv = process.argv.slice(2);
const option = (name) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : undefined);
const { webkit } = await import(join(process.env.PW, 'index.mjs'));

const results = [];
const check = (id, name, pass, evidence) => {
  results.push({ id, name, result: pass ? 'PASS' : 'FAIL', evidence: String(evidence) });
  console.log(`${pass ? 'PASS' : 'FAIL'} ${id} ${name}\n     ${evidence}`);
};

const env = await Env.create({ buildWeb: !argv.includes('--no-web-build') });
const { mac, vps } = env.stacks;
const out = option('--out') ?? join(env.root, 'evidence');
mkdirSync(out, { recursive: true });
for (const stack of [mac, vps]) {
  mkdirSync(join(stack.home, 'demo'), { recursive: true });
  await stack.cli(['project', 'add', '--name', 'demo', join(stack.home, 'demo')]);
}
const browser = await webkit.launch({ proxy: { server: `http://${env.proxy}` }, ...(process.env.WEBKIT ? { executablePath: process.env.WEBKIT } : {}) });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true });
const page = await context.newPage();
const text = () => page.evaluate(() => document.body.innerText);
const terminal = () => page.evaluate(() => [...document.querySelectorAll('.terminal-host .xterm-rows')].map((e) => e.innerText).join('\n'));
const code = () => page.evaluate(() => document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', ''));
console.log(`WebKit ${browser.version()}`);

try {
  // Pair with mac (home).
  let started = Date.now();
  const pair = interactive(mac, ['pair', '--no-qr', '--origin', mac.origin]);
  const [link] = await pair.wait(/https:\/\/mac\.tailx\.ts\.net\/pair#\S+/);
  await page.goto(link);
  await page.fill('#device-label', 'WebKit phone');
  await page.getByRole('button', { name: 'Pair this device' }).click();
  await page.getByText('Does your computer show this code?').waitFor();
  const shown = await code();
  const [, printed] = await pair.wait(/shows code (\d{3}-\d{3})/);
  pair.end(printed === shown ? 'y\n' : 'n\n');
  await page.getByRole('heading', { name: 'Sessions' }).waitFor({ timeout: 30000 });
  check('W1', 'WebKit: pair with mac (home)', printed === shown, `code ${shown}, ${Date.now() - started} ms`);

  // Add vps by paste.
  started = Date.now();
  const via = interactive(vps, ['pair', '--via', 'mac']);
  const [addLink] = await via.wait(/https:\/\/vps\.tailx\.ts\.net\/add-computer#\S+/);
  await page.getByRole('button', { name: 'Settings' }).click();
  await page.getByRole('button', { name: '+ Add a computer' }).click();
  await page.fill('#add-link', addLink);
  await page.getByRole('button', { name: 'Check link' }).click();
  await page.getByRole('button', { name: 'Add vps' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByText('Does vps show this code?').waitFor();
  const addShown = await code();
  const [, addPrinted] = await via.wait(/shows code (\d{3}-\d{3})/);
  via.end(addPrinted === addShown ? 'y\n' : 'n\n');
  const added = await until(async () => ((await text()).includes('vps is added') && 'added') || page.evaluate(() => document.querySelector('[role=alert]')?.innerText ?? ''), { what: 'add outcome' });
  await page.screenshot({ path: join(out, 'webkit-added.png') });
  check('W2', 'WebKit: add vps by paste ends with "vps is added"', added === 'added', `code ${addShown}; ${added}; ${Date.now() - started} ms`);

  // One list; open a vps session; echo.
  const tool = await context.newPage();
  await tool.goto(`${mac.origin}/`);
  const settings = await tool.evaluate((o) => fetch(`${o}/api/v1/settings`, { credentials: 'include' }).then((r) => r.json()), vps.origin);
  const created = await tool.evaluate(([o, p, s]) => fetch(`${o}/api/v1/sessions`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' },
    body: JSON.stringify({ operationId: crypto.randomUUID(), projectId: p, shellProfileId: s }) }).then((r) => r.status), [vps.origin, settings.projects[0].id, settings.shellProfiles[0].id]);
  await tool.close();
  await page.goto(`${mac.origin}/`);
  await page.locator('.session-row', { hasText: 'vps ·' }).first().waitFor({ timeout: 20000 });
  await page.screenshot({ path: join(out, 'webkit-sessions.png') });
  check('W3', 'WebKit: the Sessions list shows the vps session with its label', created === 200, `create ${created}; chips ${await page.locator('[aria-label=Computers] .chip').allInnerTexts()}`);
  started = Date.now();
  await page.locator('.session-row', { hasText: 'vps ·' }).first().click();
  await until(async () => /[%$#]\s*$/m.test(await terminal()), { what: 'prompt' });
  const openMs = Date.now() - started;
  if ((await text()).includes('Take control')) await page.getByRole('button', { name: 'Take control' }).click();
  await sleep(500);
  await page.focus('.terminal-input');
  await page.keyboard.insertText('echo webkit-$((6*7))');
  await page.keyboard.press('Enter');
  await until(async () => (await terminal()).includes('webkit-42'), { what: 'echo' });
  await page.screenshot({ path: join(out, 'webkit-vps-echo.png') });
  check('W4', 'WebKit: open a vps session and echo round-trip', true, `${new URL(page.url()).pathname}; open-to-live ${openMs} ms`);
} catch (error) {
  await page.screenshot({ path: join(out, 'webkit-fail.png') }).catch(() => undefined);
  check('ERR', 'WebKit harness error', false, `${error.message.split('\n')[0]} | ${(await text().catch(() => '')).replace(/\s+/g, ' ').slice(0, 300)}`);
} finally {
  const table = ['| ID | Check | Result | Evidence |', '| --- | --- | --- | --- |',
    ...results.map((r) => `| ${r.id} | ${r.name} | ${r.result} | ${r.evidence.replaceAll('|', '\\|')} |`)].join('\n');
  writeFileSync(join(out, 'webkit.md'), `WebKit ${browser.version()}\n\n${table}\n`);
  console.log(`\n${table}`);
  await browser.close();
  await env.close();
  process.exit(results.every((r) => r.result === 'PASS') ? 0 : 1);
}
