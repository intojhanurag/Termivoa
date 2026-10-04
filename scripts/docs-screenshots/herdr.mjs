// Takes the herdr docs screenshots (guides/herdr.md) with two isolated dev
// stacks, mac and vps, from the e2e harness (scripts/e2e-multicomputer/lib.mjs)
// and the fake herdr (scripts/fakeherdr) in each stack's own HOME. Demo names
// only. It never touches the installed pterm, launchd, Tailscale or a real herdr.
//
//   node scripts/docs-screenshots/herdr.mjs [--web-build]
//
// Writes phone-herdr-*.webp (390×844) next to this file; copy them to
// site/src/assets/docs/.
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { herdrScenario } from '../e2e-multicomputer/herdr.mjs';
import { Browser, Env, click, interactive, pairDirect, setInput, sleep, until } from '../e2e-multicomputer/lib.mjs';

const DIR = new URL('.', import.meta.url).pathname;
const PNG = join(DIR, 'png-herdr');
rmSync(PNG, { recursive: true, force: true });
mkdirSync(PNG, { recursive: true });

// What the blocked agent's terminal shows: a neutral agent asking a numbered question.
const QUESTION = [
  '',
  ' \x1b[2m●\x1b[0m Read src/cart.ts \x1b[2m(48 lines)\x1b[0m',
  ' \x1b[2m●\x1b[0m Update src/cart.ts \x1b[32m+12\x1b[0m \x1b[31m−3\x1b[0m',
  '',
  ' The cart total now rounds once, at the end.',
  '',
  ' \x1b[1mRun the cart tests?\x1b[0m',
  ' \x1b[2mnpm test -- cart · 9 tests\x1b[0m',
  '',
  ' \x1b[36m❯ 1. Yes\x1b[0m',
  "   2. Yes, and don't ask again",
  '   3. No, tell the agent what to do (esc)',
  '',
].join('\n');

const env = await Env.create({ buildWeb: process.argv.includes('--web-build') });
const { mac, vps } = env.stacks;
let step = 'start';
let browser;
try {
  for (const stack of [mac, vps]) {
    mkdirSync(join(stack.home, 'code', 'demo'), { recursive: true });
    await stack.cli(['project', 'add', '--name', 'demo', join(stack.home, 'code', 'demo')]);
  }
  const macHerdr = herdrScenario(mac.home, { attachAny: true, workspaces: [
    { label: 'shop', repo: 'shop', tabs: [{ label: '1', agent: 'claude', status: 'working' }, { label: 'logs' }] },
    { label: 'silver-meadow', repo: 'shop', tabs: [{ label: '1', agent: 'claude', status: 'blocked' }] },
    { label: 'lucky-valley', repo: 'shop', tabs: [{ label: '1', agent: 'codex', status: 'done' }] },
  ] });
  macHerdr.scenario.screens = { 'w2:p1': QUESTION };
  const vpsHerdr = herdrScenario(vps.home, { attachAny: true, workspaces: [
    { label: 'acme-web', repo: 'acme-web', tabs: [{ label: '1', agent: 'codex', status: 'working' }] },
  ] });
  await mac.installHerdr(macHerdr.scenario);
  await vps.installHerdr(vpsHerdr.scenario);

  browser = await Browser.launch(env);
  const app = await browser.page();
  browser.pages = [app];
  const shot = async (name) => { await sleep(800); await app.shot(join(PNG, `${name}.png`)); console.log('shot', name); };
  const sessions = async () => { await app.front(); await app.go(`${mac.origin}/`); await app.waitText('Sessions'); };

  step = 'pair';
  await app.go(`${mac.origin}/`);
  await pairDirect(app, mac, 'iPhone');
  await app.waitText('Sessions', 30000);
  await click(app, 'Not now').catch(() => undefined);

  step = 'add vps';
  const via = interactive(vps, ['pair', '--via', 'mac']);
  const [link] = await via.wait(/https:\/\/vps\.tailx\.ts\.net\/add-computer#\S+/);
  await click(app, 'Settings');
  await app.waitText('Add a computer');
  await click(app, '+ Add a computer');
  await click(app, 'Paste link');
  await setInput(app, '#add-link', link);
  await click(app, 'Check link');
  await app.waitText('Add vps');
  await click(app, 'Add vps');
  await app.waitText('Continue');
  await click(app, 'Continue');
  await via.wait(/shows (?:code )?(\d{3}-\d{3})/);
  via.end('y\n');
  await app.waitText('vps is added', 30000);

  step = 'tab';
  // Each broker finds its herdr within its 30 s lookup.
  await until(async () => {
    await sessions();
    const chip = await app.eval(`[...document.querySelectorAll('.chips .chip')].find((e) => e.innerText.trim().startsWith('herdr'))`).catch(() => null);
    if (chip === null) return false;
    await app.eval(`[...document.querySelectorAll('.chips .chip')].find((e) => e.innerText.trim().startsWith('herdr')).click()`);
    return until(() => app.eval(`document.body.innerText.includes('silver-meadow') && document.body.innerText.toLowerCase().includes('on vps')`), { timeout: 8000, what: 'mac and vps projects' });
  }, { what: 'the herdr tab with both computers', timeout: 90000 });
  await shot('herdr-tab');

  step = 'live';
  await app.eval(`[...document.querySelectorAll('.provider-item')].find((e) => e.querySelector('.row-title')?.innerText.trim() === 'silver-meadow').click()`);
  await app.waitText('Run the cart tests?', 20000);
  await shot('herdr-live');

  step = 'create';
  await click(app, '‹ Sessions');
  await app.waitText('Sessions');
  await app.eval(`[...document.querySelectorAll('.chips .chip')].find((e) => e.innerText.trim().startsWith('herdr')).click()`);
  await until(() => app.eval(`!!document.querySelector('[aria-label="New worktree in shop"]')`), { what: '+ on shop' });
  await click(app, 'New worktree in shop');
  await app.waitText('New worktree');
  await setInput(app, '#create-name', 'fix-login');
  await setInput(app, '#create-prompt', 'Fix the login redirect after sign-in.');
  await app.eval(`document.activeElement?.blur()`);
  await shot('herdr-create');
  await click(app, 'Close');

  step = 'integrations';
  await app.go(`${mac.origin}/settings`);
  await until(() => app.eval(`document.body.innerText.includes('herdr on mac') && document.body.innerText.includes('herdr on vps')`), { what: 'both Integrations cards', timeout: 20000 });
  await app.eval(`document.getElementById('integrations-heading').scrollIntoView({ block: 'start' }); scrollBy(0, -16)`);
  await shot('herdr-integrations');

  step = 'convert';
  await convert(await browser.page({ bypassCSP: true }));
} catch (error) {
  console.log('ERROR at', step, error.message);
  await browser?.pages?.[0]?.shot(join(PNG, 'fail.png')).catch(() => undefined);
  process.exitCode = 1;
} finally {
  await browser?.close().catch(() => undefined);
  await env.close();
}
process.exit(process.exitCode ?? 0);

/** PNG (DPR 3) to WebP at 390 × 844 CSS px through a canvas. */
async function convert(page) {
  for (const name of ['herdr-tab', 'herdr-live', 'herdr-create', 'herdr-integrations']) {
    const data = readFileSync(join(PNG, `${name}.png`)).toString('base64');
    const webp = await page.eval(`(async () => {
      const img = new Image(); img.src = 'data:image/png;base64,${data}'; await img.decode();
      const canvas = document.createElement('canvas'); canvas.width = 390; canvas.height = 844;
      canvas.getContext('2d').drawImage(img, 0, 0, img.width, img.height, 0, 0, 390, 844);
      return canvas.toDataURL('image/webp', 0.85).split(',')[1]; })()`);
    writeFileSync(join(DIR, `phone-${name}.webp`), Buffer.from(webp, 'base64'));
    console.log('webp', `phone-${name}`);
  }
}
