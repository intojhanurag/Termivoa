// Phase B core flow in WebKit (README.md): pair with mac, add vps by paste,
// one Sessions list, a vps session with an echo round-trip, its dev-server
// preview in the app, a foreign page that may not frame it, and the phone
// side of adding vps from Found on your network.
//   PW=<playwright-core dir> [WEBKIT=<browser executable>] node scripts/e2e-multicomputer/webkit.mjs [--no-web-build] [--out <dir>]
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Env, freePort, interactive, sleep, until } from './lib.mjs';

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
  const [, printed] = await pair.wait(/shows (?:code )?(\d{3}-\d{3})/);
  pair.end(printed === shown ? 'y\n' : 'n\n');
  await page.getByRole('heading', { name: 'Sessions' }).waitFor({ timeout: 30000 });
  check('W1', 'WebKit: pair with mac (home)', printed === shown, `code ${shown}, ${Date.now() - started} ms`);

  // Add vps by paste.
  started = Date.now();
  const via = interactive(vps, ['pair', '--via', 'mac']);
  const [addLink] = await via.wait(/https:\/\/vps\.tailx\.ts\.net\/add-computer#\S+/);
  await page.getByRole('button', { name: 'Settings' }).click();
  await page.getByRole('button', { name: '+ Add a computer' }).click();
  await page.getByRole('button', { name: 'Paste link' }).click();
  await page.fill('#add-link', addLink);
  await page.getByRole('button', { name: 'Check link' }).click();
  await page.getByRole('button', { name: 'Add vps' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByText('Does vps show this code?').waitFor();
  const addShown = await code();
  const [, addPrinted] = await via.wait(/shows (?:code )?(\d{3}-\d{3})/);
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
  await page.locator('section[aria-label="vps"] .session-row').first().waitFor({ timeout: 20000 });
  await page.screenshot({ path: join(out, 'webkit-sessions.png') });
  check('W3', 'WebKit: the Sessions list shows the vps session under the vps header', created === 200, `create ${created}; drawer pill ${await page.locator('.drawer-pill').innerText()}; computer headers ${await page.locator('h2.computer-group').allInnerTexts()}`);
  started = Date.now();
  await page.locator('section[aria-label="vps"] .session-row').first().click();
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

  // The vps session's dev server, previewed inside the mac app.
  const port = await freePort();
  await page.focus('.terminal-input');
  await page.keyboard.insertText(`${join(env.bin, 'front')} devserver -name vps -listen 127.0.0.1:${port}`);
  await page.keyboard.press('Enter');
  const mark = env.logMark();
  await page.getByRole('button', { name: `Open preview of port ${port}` }).click({ timeout: 30000 });
  const frame = page.frameLocator('.preview-frame');
  for (const part of ['dev site on vps', 'sub ok', 'fetch pong', 'ws echo']) await frame.getByText(part).waitFor({ timeout: 20000 });
  const bar = (await page.locator('.preview-bar').innerText()).replace(/\s+/g, ' ');
  await page.screenshot({ path: join(out, 'webkit-vps-preview.png') });
  const seen = (from) => env.logSince(from).filter((e) => e.host === `${vps.host}:8443`);
  const select = seen(mark).find((e) => e.path === '/__termivoa/select' && e.secFetchMode === 'navigate');
  const root = seen(mark).find((e) => e.path === '/');
  check('W5', 'WebKit: Preview ready, Open, the vps dev site in the frame (sub-resource, fetch, WebSocket) under "vps · untrusted page"',
    bar.includes('vps · untrusted page') && select?.status === 200 && select.referer === `${mac.origin}/` && select.cookie && root?.secFetchSite === 'same-origin',
    `bar: ${bar}; select ${select?.status} referer=${select?.referer} site=${select?.secFetchSite} cookie=${select?.cookie}; / ${root?.status} site=${root?.secFetchSite}; frame: ${(await frame.locator('body').innerText()).replace(/\s+/g, ' ')}`);
  const load = async () => Number((await frame.locator('#load').innerText()).replace('load ', ''));
  const before = await load();
  await page.getByRole('button', { name: 'Reload preview' }).click();
  const after = await until(async () => { const n = await load().catch(() => 0); return n > before && n; }, { what: 'a new load' });
  check('W6', 'WebKit: ↻ loads the preview again', true, `dev site load ${before} → ${after}`);

  // A page of another tailnet user frames the same select address: refused, though WebKit sends the cookie.
  const evil = await context.newPage();
  await evil.goto('https://evil.tailx.ts.net/');
  const mark2 = env.logMark();
  const src = await page.locator('.preview-frame').getAttribute('src');
  await evil.evaluate((address) => new Promise((done) => { const f = document.createElement('iframe'); f.referrerPolicy = 'origin'; f.src = address; f.onload = done; document.body.append(f); }), src);
  const refusal = (await evil.frameLocator('iframe').locator('body').innerText()).trim();
  const refused = seen(mark2).find((e) => e.path === '/__termivoa/select');
  await evil.close();
  check('W7', 'WebKit: evil.tailx.ts.net framing the vps select address is refused', refused?.status === 403 && refusal === 'Open the preview from the Termivoa app.',
    `${refused?.status} referer=${refused?.referer} site=${refused?.secFetchSite} cookie=${refused?.cookie}; the frame shows: ${refusal}`);

  // Found on your network, the phone side of F-add: vps waits in a real
  // terminal; Settings → Computers → Found: vps — Add → the sheet → code → y.
  const forget = await context.newPage();
  await forget.goto(`${mac.origin}/`);
  const removed = await forget.evaluate((host) => fetch(`/api/v1/computers/${host}`, { method: 'DELETE', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' }, body: '{}' }).then((r) => r.status), vps.host);
  await forget.close();
  started = Date.now();
  const offerCli = interactive(vps, ['pair', '--via', 'mac'], { pty: true });
  const [sent] = await offerCli.wait(/Sent to mac \(https:\/\/mac\.tailx\.ts\.net\)\./);
  await page.goto(`${mac.origin}/`);
  await page.getByRole('button', { name: 'Settings' }).click();
  await page.locator('.found-add[aria-label="Add vps"]').click({ timeout: 30000 });
  await page.getByText('Add this computer?').waitFor();
  await page.locator('[role=dialog] button.primary', { hasText: 'Add vps' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByText('Does vps show this code?').waitFor();
  const foundShown = await code();
  const phoneLine = await page.getByText('If it matches, type y in the terminal on vps.').isVisible();
  const [, foundPrinted] = await offerCli.wait(/wants to add this computer \(vps\)[\s\S]*?shows (\d{3}-\d{3}) and says "type y in the terminal on vps"/);
  offerCli.end(foundPrinted === foundShown && phoneLine ? 'y\n' : 'n\n');
  const foundAdded = await until(async () => ((await text()).includes('vps is added') && 'added') || page.evaluate(() => document.querySelector('[role=alert]')?.innerText ?? ''), { what: 'Found add outcome' });
  await page.screenshot({ path: join(out, 'webkit-found-added.png') });
  check('W8', 'WebKit: Found on your network: vps — Add → "Add this computer?" → code; vps\'s prompt names this computer and the code; y → "vps is added"',
    removed === 204 && foundPrinted === foundShown && phoneLine && foundAdded === 'added' && await offerCli.done === 0,
    `remove ${removed}; ${sent}; code ${foundShown}/${foundPrinted}; phone says "type y in the terminal on vps": ${phoneLine}; ${foundAdded}; ${Date.now() - started} ms`);
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
