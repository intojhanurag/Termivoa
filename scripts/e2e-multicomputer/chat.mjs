// The Chat view end to end (docs/chat-view.md): the real host (M1a) and the
// phone (M1b) on one mac dev stack. The agent is scripts/fakeagent, a fake
// Claude Code that writes its own pid file and a synthetic transcript into
// the stack's temp HOME; the neutral demo agent (scripts/docs-screenshots/
// agent.js) runs in the foreground so the host's agent state sees work. No
// real agent, no real ~/.claude. Measures write-to-phone latency with
// `fakeagent -stamp` (budget p50 ≤ 400 ms, p95 ≤ 750 ms).
//   PW=<playwright-core dir> node scripts/e2e-multicomputer/chat.mjs [--no-web-build] [--out <dir>]
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Env, REPO, interactive, run, sleep, until } from './lib.mjs';

const argv = process.argv.slice(2);
const option = (name) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : undefined);
const { chromium } = await import(join(process.env.PW, 'index.mjs'));

const results = [];
const check = (id, name, pass, evidence) => {
  results.push({ id, name, result: pass ? 'PASS' : 'FAIL', evidence: String(evidence) });
  console.log(`${pass ? 'PASS' : 'FAIL'} ${id} ${name}\n     ${evidence}`);
};
const CHECKS = 9;
let latency = '';

const env = await Env.create({ buildWeb: !argv.includes('--no-web-build'), names: ['mac'] });
const { mac } = env.stacks;
const out = option('--out') ?? join(env.root, 'evidence');
mkdirSync(out, { recursive: true });
const project = join(mac.home, 'billing-api');
const bin = join(mac.home, 'bin');
mkdirSync(project, { recursive: true });
mkdirSync(bin, { recursive: true });
copyFileSync(join(REPO, 'scripts/docs-screenshots/agent.js'), join(bin, 'agent'));
const built = await run('go', ['build', '-o', join(bin, 'fakeagent'), './scripts/fakeagent'], { timeout: 600000 });
if (built.code !== 0) throw new Error(`fakeagent build failed: ${built.stderr}`);
// A git repository with app.go changed, so Open in Changes has the file the fake agent's edit names.
const gitEnv = { ...process.env, HOME: mac.home, GIT_CONFIG_NOSYSTEM: '1' };
const git = (...args) => run('git', ['-c', 'user.name=Demo', '-c', 'user.email=demo@example.invalid', ...args], { cwd: project, env: gitEnv });
writeFileSync(join(project, 'app.go'), 'package app\n\nfunc value() int {\n\treturn 1\n}\n');
await git('init', '-q');
await git('add', 'app.go');
await git('commit', '-q', '-m', 'init');
writeFileSync(join(project, 'app.go'), 'package app\n\nfunc value() int {\n\treturn 2\n}\n');
await mac.cli(['project', 'add', '--name', 'billing-api', project]);
async function chatOn() {
  const command = interactive(mac, ['chat', 'on']);
  await command.wait(/\[y\/N\]/u);
  command.end('y\n');
  if (await command.done !== 0) throw new Error(`pterm chat on failed: ${command.out}`);
}

const browser = await chromium.launch({ proxy: { server: `http://${env.proxy}` } });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
// Count the chat messages the page sends (the host answers them; nothing is faked).
await context.addInitScript(() => {
  window.chatLog = [];
  const send = WebSocket.prototype.send;
  WebSocket.prototype.send = function (data) {
    try { const message = JSON.parse(data); if (message.type.startsWith('chat.')) window.chatLog.push(message.type); } catch { /* not JSON */ }
    return send.call(this, data);
  };
});
const page = await context.newPage();
const body = () => page.evaluate(() => document.body.innerText);
const code = () => page.evaluate(() => document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', ''));
const log = () => page.evaluate(() => window.chatLog);
const chatButton = () => page.getByRole('button', { name: 'Chat', exact: true });
async function newSession() {
  const id = await page.evaluate(async () => {
    const settings = await fetch('/api/v1/settings', { credentials: 'include' }).then((r) => r.json());
    const answer = await fetch('/api/v1/sessions', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' },
      body: JSON.stringify({ operationId: crypto.randomUUID(), projectId: settings.projects[0].id, shellProfileId: settings.shellProfiles[0].id }) }).then((r) => r.json());
    return answer.session?.id ?? answer.id;
  });
  await page.goto(`${mac.origin}/sessions/${encodeURIComponent(id)}`);
  await page.getByRole('button', { name: 'Take control' }).click({ timeout: 30000 });
  await until(async () => (await body()).includes('Control'), { what: 'control', timeout: 30000 });
}
async function typeLine(line) {
  await page.focus('.terminal-input');
  await page.keyboard.insertText(line);
  await page.keyboard.press('Enter');
}
const pct = (sorted, p) => sorted[Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1)];
console.log(`Chromium ${browser.version()}`);
try {
  const pair = interactive(mac, ['pair', '--no-qr', '--origin', mac.origin]);
  const [link] = await pair.wait(/https:\/\/mac\.tailx\.ts\.net\/pair#\S+/);
  await page.goto(link);
  await page.fill('#device-label', 'Demo phone');
  await page.getByRole('button', { name: 'Pair this device' }).click();
  await page.getByText('Does your computer show this code?').waitFor();
  const shown = await code();
  const [, printed] = await pair.wait(/shows (?:code )?(\d{3}-\d{3})/);
  pair.end(printed === shown ? 'y\n' : 'n\n');
  await page.getByRole('heading', { name: 'Sessions' }).waitFor({ timeout: 30000 });
  await newSession();

  // CH1: no switch while Chat is off; after `pterm chat on` the switch shows and the real host links the fake Claude Code.
  await sleep(1500);
  const early = await chatButton().count();
  await typeLine('~/bin/fakeagent -stamp -turns 80 -every 500ms > /dev/null & node ~/bin/agent work');
  await chatOn();
  await page.reload();
  await chatButton().waitFor({ timeout: 30000 });
  await chatButton().click();
  await page.locator('.chat-bubble', { hasText: /^stamp \d+$/u }).first().waitFor({ timeout: 20000 });
  check('CH1', 'No switch while Chat is off; on, the real host links Chat and shows the turns', early === 0 && (await log()).includes('chat.open'),
    `switch at the prompt ${early}; chat messages ${(await log()).join(', ')}; bubbles ${await page.locator('.chat-bubble').count()}`);
  await page.screenshot({ path: join(out, 'chat-real-1.png') });

  // CH2 and CH3: new turns arrive by chat.changed → chat.read; the latency from the agent's write to the phone's screen.
  await page.evaluate(() => {
    window.arrivals = new Map();
    const seen = new Set([...document.querySelectorAll('.chat-bubble')].map((e) => e.textContent));
    const note = () => {
      const now = Date.now();
      for (const element of document.querySelectorAll('.chat-bubble')) {
        const text = element.textContent;
        const match = /^stamp (\d+)$/u.exec(text);
        if (match !== null && !seen.has(text) && !window.arrivals.has(text)) window.arrivals.set(text, now - Number(BigInt(match[1]) / 1000000n));
      }
    };
    new MutationObserver(note).observe(document.querySelector('.chat-scroll'), { childList: true, subtree: true, characterData: true });
  });
  await until(() => page.evaluate(() => window.arrivals.size >= 40), { what: '40 stamped turns', timeout: 60000 });
  const samples = (await page.evaluate(() => [...window.arrivals.values()])).sort((a, b) => a - b);
  const p50 = pct(samples, 50);
  const p95 = pct(samples, 95);
  latency = `${samples.length} turns: p50 ${p50} ms, p95 ${p95} ms, min ${samples[0]} ms, max ${samples.at(-1)} ms`;
  const reads = (await log()).filter((t) => t === 'chat.read').length;
  check('CH2', 'Live turns arrive by chat.changed and chat.read', reads >= 10 && samples.length >= 40, `${reads} chat.read`);
  check('CH3', 'Write to phone: p50 ≤ 400 ms, p95 ≤ 750 ms', p50 <= 400 && p95 <= 750, latency);

  // CH4: the real blocks: Ran, Edit app.go, bold text; sticky at the bottom.
  const text = await body();
  const stuck = await page.evaluate(() => { const s = document.querySelector('.chat-scroll'); return s.scrollHeight - s.scrollTop - s.clientHeight; });
  const strong = await page.locator('.chat-text strong').count();
  check('CH4', 'Real blocks render (Ran, Edit app.go, markdown) and the list stays at the bottom', /Ran/u.test(text) && /app\.go/u.test(text) && strong > 0 && stuck < 48,
    `Ran ${/Ran/u.test(text)}; app.go ${/app\.go/u.test(text)}; bold ${strong}; ${stuck} px from the bottom`);

  // CH5: Edit opens to its hunk; Open in Changes opens app.go's diff in Changes, with no refusal.
  await page.getByRole('button', { name: /^Edit app\.go/u }).last().click();
  const hunk = await page.locator('.chat-hunk').last().innerText();
  await page.getByRole('button', { name: 'Open in Changes ›' }).last().click();
  await until(() => page.evaluate(() => document.querySelector('.diff') !== null), { what: 'the app.go diff', timeout: 15000 });
  const banner = await page.evaluate(() => document.querySelector('.banner')?.textContent ?? '');
  check('CH5', 'Edit hunk, then Open in Changes shows the file', hunk.includes('return 2') && banner === '', `banner "${banner}"`);
  // Back from the file, then Done: Chat is on screen again with a fresh page.
  await page.getByRole('button', { name: 'Back to changed files' }).click({ timeout: 10000 });
  await page.getByRole('button', { name: 'Done', exact: true }).click({ timeout: 10000 });
  await page.locator('.chat-bubble').first().waitFor({ timeout: 15000 });

  // CH6: 390 px: nothing wider than the phone.
  const wide = await page.evaluate(() => [...document.querySelectorAll('.live *')].filter((e) => e.getBoundingClientRect().right > 391).map((e) => e.className).slice(0, 5));
  check('CH6', 'Nothing wider than 390 px', wide.length === 0, wide.join(', ') || 'none');

  // CH7: a session with no Claude Code (only the demo agent): screen 4, Why?, Show terminal, then no switch.
  await newSession();
  await typeLine('node ~/bin/agent work');
  await chatButton().waitFor({ timeout: 30000 });
  await chatButton().click();
  await page.locator('.chat-none-title').waitFor({ timeout: 20000 });
  const lead = await page.locator('.chat-none-lead').innerText();
  await page.screenshot({ path: join(out, 'chat-real-4.png') });
  await page.getByRole('button', { name: 'Why?' }).click();
  const why = await page.locator('.chat-why').innerText();
  await page.getByRole('button', { name: 'Close' }).click();
  await page.getByRole('button', { name: 'Show terminal' }).click();
  await sleep(500);
  const after = await chatButton().count();
  check('CH7', 'Not available: the host reason, Why?, Show terminal, then no switch', why.length > 20 && after === 0, `${lead} | ${why}`);

  // CH8: Show terminal sends chat.close (this page load: the session above was opened with a new page).
  const closes = (await log()).filter((t) => t === 'chat.close').length;
  check('CH8', 'Leaving Chat sends chat.close', closes === 1, `${(await log()).join(', ')}`);

  // CH9: Settings → mac → Chat view: the switch turns Chat off on the computer (PATCH), and the row then shows the command.
  await page.goto(`${mac.origin}/settings`);
  await page.locator('.sc-card .sc-open').first().click();
  await page.getByRole('switch', { name: 'Chat view' }).click({ timeout: 15000 });
  await page.getByText('Turn it on at the computer:').waitFor({ timeout: 15000 });
  const status = (await mac.cli(['chat', 'status'])).stdout.trim();
  check('CH9', 'Settings turns Chat off on the computer', /off/iu.test(status), status);
} catch (error) {
  await page.screenshot({ path: join(out, 'chat-fail.png') }).catch(() => undefined);
  check('ERR', 'Chat harness error', false, error.message.split('\n')[0]);
} finally {
  const table = ['| ID | Check | Result | Evidence |', '| --- | --- | --- | --- |',
    ...results.map((r) => `| ${r.id} | ${r.name} | ${r.result} | ${r.evidence.replaceAll('|', '\\|')} |`)].join('\n');
  writeFileSync(join(out, 'chat.md'), `Chromium ${browser.version()}, real host and phone, scripts/fakeagent\n\nLatency (agent write to phone screen): ${latency}\n\n${table}\n`);
  console.log(`\n${table}`);
  await browser.close();
  await env.close();
  process.exit(results.length === CHECKS && results.every((r) => r.result === 'PASS') ? 0 : 1);
}
