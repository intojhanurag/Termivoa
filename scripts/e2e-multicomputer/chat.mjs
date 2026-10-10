// The Chat view, phone side (docs/chat-view.md, section 9), against a FAKE host
// chat: until the host side (M1a) lands, the page's own fetch and WebSocket are
// wrapped so the settings carry `chat` and every chat.open / chat.read gets a
// synthetic chat.state and chat.page (demo text only). The terminal, the
// session and the agent state come from a real mac stack, with the neutral
// demo agent from scripts/docs-screenshots/agent.js. Checks: the switch, the
// pull flow, N new, Open in Changes, the link confirm sheet, screen 4 and Why?,
// Terminal again, and the fit at 390 px.
//   PW=<playwright-core dir> node scripts/e2e-multicomputer/chat.mjs [--no-web-build] [--out <dir>]
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Env, REPO, interactive, sleep, until } from './lib.mjs';

const argv = process.argv.slice(2);
const option = (name) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : undefined);
const { chromium } = await import(join(process.env.PW, 'index.mjs'));

const results = [];
const check = (id, name, pass, evidence) => {
  results.push({ id, name, result: pass ? 'PASS' : 'FAIL', evidence: String(evidence) });
  console.log(`${pass ? 'PASS' : 'FAIL'} ${id} ${name}\n     ${evidence}`);
};
const CHECKS = 8;

const env = await Env.create({ buildWeb: !argv.includes('--no-web-build'), names: ['mac'] });
const { mac } = env.stacks;
const out = option('--out') ?? join(env.root, 'evidence');
mkdirSync(out, { recursive: true });
mkdirSync(join(mac.home, 'billing-api'), { recursive: true });
mkdirSync(join(mac.home, 'bin'), { recursive: true });
copyFileSync(join(REPO, 'scripts/docs-screenshots/agent.js'), join(mac.home, 'bin/agent'));
await mac.cli(['project', 'add', '--name', 'billing-api', join(mac.home, 'billing-api')]);
const browser = await chromium.launch({ proxy: { server: `http://${env.proxy}` } });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });

// The fake host chat. Every chat request the page sends is answered here and never reaches the real host.
await context.addInitScript(() => {
  const H = '0123456789abcdef';
  const cursor = (n) => `c${String(n).padStart(21, '0')}`;
  const ago = (s) => new Date(Date.now() - s * 1000).toISOString();
  const text = (id, kind, value, at) => ({ id, kind, text: value, ...(at === undefined ? {} : { at }) });
  const first = [
    text('u1', 'user_text', 'Move invoice totals to the new tax table and keep old invoices unchanged.', ago(240)),
    text('a1', 'assistant_text', 'I’ll read how totals are built today, switch new invoices to the tax table, and leave issued ones alone. See [the tax notes](https://example.com/tax).', ago(238)),
    { id: 't1', kind: 'tool_call', tool: { name: 'Read', status: 'ok', summary: 'src/invoices.ts' } },
    { id: 'e1', kind: 'edit', edit: { path: 'src/tax.ts', added: 18, removed: 7, hunk: '@@ rateFor · line 24\n- const rate = REGION_RATES[region]\n+ const rate = taxTable.rateFor(region)\n+ if (invoice.locked) return invoice.total' } },
    { id: 't2', kind: 'tool_call', tool: { name: 'Bash', status: 'running', summary: 'npm test -- invoices' } },
  ];
  window.chatLog = [];
  window.chatLink = 'linked';
  window.chatMore = [];
  const fetchFirst = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const response = await fetchFirst(input, init);
    if (!String(input).endsWith('/api/v1/settings')) return response;
    const body = await response.clone().json();
    return new Response(JSON.stringify({ ...body, protocolRevision: Math.max(8, body.protocolRevision), chat: { enabled: true } }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  };
  const Real = window.WebSocket;
  window.WebSocket = class extends Real {
    send(data) {
      const message = JSON.parse(data);
      if (!message.type.startsWith('chat.')) return super.send(data);
      window.chatLog.push({ type: message.type, payload: message.payload });
      const reply = (type, payload, requestId = message.requestId) => setTimeout(() => this.dispatchEvent(new MessageEvent('message', {
        data: JSON.stringify({ version: 1, type, requestId, sessionId: message.sessionId, generation: message.generation, payload }),
      })), 20);
      window.chatPush = (more) => {
        window.chatMore.push(...more);
        reply('chat.changed', { historyId: H }, crypto.randomUUID());
      };
      if (message.type === 'chat.open') {
        if (window.chatLink !== 'linked') {
          reply('chat.state', window.chatLink === 'none' ? { link: 'none', reason: 'cleared' } : { link: window.chatLink });
          return undefined;
        }
        reply('chat.state', { link: 'linked', agent: 'claude', historyId: H });
        reply('chat.page', { historyId: H, blocks: first, before: cursor(1), after: cursor(2), complete: true });
      } else if (message.type === 'chat.read' && message.payload.after !== undefined) {
        const blocks = window.chatMore.splice(0);
        reply('chat.page', { historyId: H, blocks, before: cursor(3), after: cursor(4 + window.chatLog.length), complete: true });
      } else if (message.type === 'chat.read') {
        reply('chat.page', { historyId: H, blocks: [text('u0', 'user_text', 'An earlier question.', ago(4000))], after: cursor(9), complete: true });
      }
      return undefined;
    }
  };
});
const page = await context.newPage();
const body = () => page.evaluate(() => document.body.innerText);
const code = () => page.evaluate(() => document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', ''));
const log = () => page.evaluate(() => window.chatLog.map((m) => m.type));
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
  const id = await page.evaluate(async () => {
    const settings = await fetch('/api/v1/settings', { credentials: 'include' }).then((r) => r.json());
    const answer = await fetch('/api/v1/sessions', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' },
      body: JSON.stringify({ operationId: crypto.randomUUID(), projectId: settings.projects[0].id, shellProfileId: settings.shellProfiles[0].id }) }).then((r) => r.json());
    return answer.session?.id ?? answer.id;
  });
  await page.goto(`${mac.origin}/sessions/${encodeURIComponent(id)}`);
  await page.getByRole('button', { name: 'Take control' }).click({ timeout: 30000 });
  await until(async () => (await body()).includes('Control'), { what: 'control', timeout: 30000 });
  // A plain shell is idle too: no switch until an agent works.
  const early = await page.getByRole('button', { name: 'Chat', exact: true }).count();
  await page.focus('.terminal-input');
  await page.keyboard.insertText('node ~/bin/agent work');
  await page.keyboard.press('Enter');

  // CH1: the switch appears once an agent runs, and Chat opens with one chat.open.
  await page.getByRole('button', { name: 'Chat', exact: true }).waitFor({ timeout: 30000 });
  await page.getByRole('button', { name: 'Chat', exact: true }).click();
  await page.locator('.chat-bubble', { hasText: 'Move invoice totals' }).waitFor();
  const bubbles = await page.locator('.chat-bubble').count();
  check('CH1', 'No switch at a plain prompt; with an agent it shows, and Chat opens with one chat.open', early === 0 && bubbles === 1 && (await log()).join(',') === 'chat.open' && (await body()).includes('Read'),
    `switch at the prompt ${early}; bubbles ${bubbles}; chat messages: ${(await log()).join(', ')}`);
  await page.screenshot({ path: join(out, 'chat-1-working.png') });

  // CH2: pull model: chat.changed → one chat.read after; the new block shows.
  await page.evaluate(() => window.chatPush([{ id: 'a2', kind: 'assistant_text', text: 'New invoices now use taxTable.rateFor().' }]));
  await page.locator('.chat-text', { hasText: 'New invoices now use' }).waitFor();
  const reads = (await log()).filter((type) => type === 'chat.read').length;
  check('CH2', 'chat.changed pulls one chat.read after, and the block shows', reads === 1, `reads ${reads}`);

  // CH3: an edit row opens to its hunk, and Open in Changes opens Changes.
  await page.getByRole('button', { name: /^Edit src\/tax\.ts/u }).click();
  const hunk = await page.locator('.chat-hunk').innerText();
  await page.getByRole('button', { name: 'Open in Changes ›' }).click();
  await page.getByRole('heading', { name: /Changes/u }).first().waitFor({ timeout: 10000 }).catch(() => undefined);
  await sleep(1500);
  const changes = await page.evaluate(() => document.querySelector('.changes') !== null);
  const banner = await page.evaluate(() => document.querySelector('.banner')?.textContent ?? '');
  check('CH3', 'Edit row shows its hunk; Open in Changes opens Changes, with no refusal', hunk.includes('taxTable.rateFor') && changes && banner === '',
    `hunk shown; changes ${changes}; banner "${banner}"`);
  await page.getByRole('button', { name: 'Done', exact: true }).click();

  // CH4: a link opens only after the confirm sheet, which shows the full address.
  await page.getByRole('button', { name: /^Link: the tax notes/u }).click();
  const sheet = await page.locator('.chat-link-url').innerText();
  await page.getByRole('button', { name: 'Cancel' }).click();
  check('CH4', 'Link asks first and shows the full address', sheet === 'https://example.com/tax', sheet);

  // CH5: N new while scrolled up (first enough messages to scroll).
  await page.evaluate(() => window.chatPush(Array.from({ length: 20 }, (_, n) => ({ id: `f${n}`, kind: 'assistant_text', text: `Step ${n}: checked one more invoice.` }))));
  await page.locator('.chat-text', { hasText: 'Step 19:' }).waitFor();
  const stuck = await page.evaluate(() => { const s = document.querySelector('.chat-scroll'); return s.scrollHeight - s.scrollTop - s.clientHeight; });
  await page.evaluate(() => { const s = document.querySelector('.chat-scroll'); s.scrollTop = 0; s.dispatchEvent(new Event('scroll')); });
  await sleep(200);
  await page.evaluate(() => window.chatPush([{ id: 'a3', kind: 'assistant_text', text: 'Checking rounding.' }, { id: 'a4', kind: 'assistant_text', text: 'Done.' }]));
  await page.getByRole('button', { name: /new messages?, go to the newest/u }).waitFor({ timeout: 10000 });
  const pill = await page.locator('.chat-new').innerText();
  check('CH5', 'Sticky at the bottom; N new ↓ shows while scrolled up', stuck < 48 && /^\d+ new/u.test(pill), `${stuck} px from the bottom after 20 new; pill "${pill}"`);

  // CH6: 390 px: nothing wider than the phone.
  const wide = await page.evaluate(() => [...document.querySelectorAll('.live *')].filter((e) => e.getBoundingClientRect().right > 391).map((e) => e.className).slice(0, 5));
  check('CH6', 'Nothing wider than 390 px', wide.length === 0, wide.join(', ') || 'none');

  // CH7: screen 4 with Why?, then Show terminal.
  await page.getByRole('button', { name: 'Terminal', exact: true }).click();
  await page.evaluate(() => { window.chatLink = 'none'; });
  await page.getByRole('button', { name: 'Chat', exact: true }).click();
  await page.locator('.chat-none-title').waitFor();
  await page.screenshot({ path: join(out, 'chat-4-not-available.png') });
  await page.getByRole('button', { name: 'Why?' }).click();
  const why = await page.locator('.chat-why').innerText();
  await page.getByRole('button', { name: 'Close' }).click();
  await page.getByRole('button', { name: 'Show terminal' }).click();
  const terminal = await page.evaluate(() => !document.querySelector('.terminal-area').hidden);
  check('CH7', 'Not available: Why? explains, Show terminal returns', why.includes('/clear') && terminal, why);

  // CH8: leaving Chat sends chat.close.
  const closes = (await log()).filter((type) => type === 'chat.close').length;
  check('CH8', 'Leaving Chat sends chat.close', closes >= 2, `closes ${closes}`);
} catch (error) {
  await page.screenshot({ path: join(out, 'chat-fail.png') }).catch(() => undefined);
  check('ERR', 'Chat harness error', false, error.message.split('\n')[0]);
} finally {
  const table = ['| ID | Check | Result | Evidence |', '| --- | --- | --- | --- |',
    ...results.map((r) => `| ${r.id} | ${r.name} | ${r.result} | ${r.evidence.replaceAll('|', '\\|')} |`)].join('\n');
  writeFileSync(join(out, 'chat.md'), `Chromium ${browser.version()} (fake host chat)\n\n${table}\n`);
  console.log(`\n${table}`);
  await browser.close();
  await env.close();
  process.exit(results.length === CHECKS && results.every((r) => r.result === 'PASS') ? 0 : 1);
}
