// Takes the multi-computer docs screenshots (guides/computers.md) with two
// isolated dev stacks, mac and vps, from the e2e harness (scripts/e2e-multicomputer/lib.mjs):
// fake Tailscale, an HTTPS front on loopback, demo tailnet names only. It never
// touches the installed pterm, launchd, Tailscale or Tailscale Serve.
//
//   node scripts/docs-screenshots/computers.mjs [--web-build]
//
// --web-build runs pnpm build:web and embed-web.sh first; without it the
// embedded internal/webassets/dist is used. Writes phone-computers-*.webp (found, confirm, code, adding, sessions, drawer, offline)
// (390×844) next to this file; copy them to site/src/assets/docs/.
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Browser, Env, api, click, interactive, pairDirect, setInput, sleep, until } from '../e2e-multicomputer/lib.mjs';

const DIR = new URL('.', import.meta.url).pathname;
const PNG = join(DIR, 'png-computers');
rmSync(PNG, { recursive: true, force: true });
mkdirSync(PNG, { recursive: true });
const crops = {};

const env = await Env.create({ buildWeb: process.argv.includes('--web-build') });
const { mac, vps } = env.stacks;
let step = 'start';
let browser;
try {
  for (const [stack, name] of [[mac, 'acme-web'], [vps, 'billing-api']]) {
    mkdirSync(join(stack.home, 'code', name), { recursive: true });
    writeFileSync(join(stack.home, 'code', name, 'README.md'), `# ${name}\n`);
    await stack.cli(['project', 'add', '--name', name, join(stack.home, 'code', name)]);
  }
  browser = await Browser.launch(env);
  const app = await browser.page();
  browser.pages = [app];
  const tool = await browser.page({ bypassCSP: true }); // API calls for setup only (same cookies)
  const shot = async (name, crop) => {
    await sleep(600);
    await app.shot(join(PNG, `${name}.png`));
    if (crop) crops[name] = crop;
    console.log('shot', name);
  };
  const session = async (stack, title) => {
    const settings = (await api(tool, `${stack.origin}/api/v1/settings`)).json;
    const made = await api(tool, `${stack.origin}/api/v1/sessions`, 'POST', { operationId: crypto.randomUUID(), projectId: settings.projects[0].id, shellProfileId: settings.shellProfiles[0].id });
    const s = made.json.session ?? made.json;
    await api(tool, `${stack.origin}/api/v1/sessions/${s.id}`, 'PATCH', { operationId: crypto.randomUUID(), generation: s.generation, title });
  };
  const sessions = async () => {
    await app.front(); await app.go(`${mac.origin}/`); await app.waitText('Sessions');
    // The Home Screen hint pushes the list down; close it the way a user would.
    if (await app.eval(`document.body.innerText.includes('add Termivoa to your Home Screen')`)) await click(app, 'Not now');
  };

  step = 'pair';
  await app.go(`${mac.origin}/`);
  await pairDirect(app, mac, 'iPhone');
  await app.waitText('Sessions', 30000);
  await tool.go(`${mac.origin}/`);
  await click(app, 'Not now').catch(() => undefined);

  // vps runs `pterm pair --via mac` in a terminal: mac lists it under Found on your network.
  step = 'found';
  const via = interactive(vps, ['pair', '--via', 'mac'], { pty: true });
  await via.wait(/Waiting for your phone/);
  // Settings reads Found on open, then polls only while the page is visible; open it again until vps shows.
  await until(async () => {
    await sessions();
    await click(app, 'Settings');
    return until(() => app.eval(`!!document.querySelector('.found-add[aria-label="Add vps"]')`), { what: 'Found: vps — Add', timeout: 8000 });
  }, { what: 'Found: vps — Add', timeout: 60000 });
  await shot('computers-found');

  step = 'confirm';
  await app.eval(`document.querySelector('.found-add[aria-label="Add vps"]').click()`);
  await app.waitText('Add this computer?');
  await shot('computers-confirm');
  await app.eval(`[...document.querySelectorAll('[role=dialog] button.primary')].find((b) => b.innerText.trim() === 'Add vps').click()`);
  await app.waitText('Continue');
  await click(app, 'Continue');

  step = 'code';
  await app.waitText('Does vps show this code?');
  await shot('computers-code');

  // Leave the code screen: the bar "Adding vps… approve on vps" stays on top.
  step = 'adding';
  await click(app, 'Sessions');
  await app.waitText('approve on vps');
  await shot('computers-adding', { y: 0, h: 150 });
  const [, code] = await via.wait(/shows (\d{3}-\d{3})/);
  via.end('y\n');
  await app.waitText('vps is added', 30000);
  console.log('added vps, code', code);

  step = 'sessions';
  await session(mac, 'mac-1');
  await session(mac, 'mac-2');
  await session(vps, 'vps-1');
  await sessions();
  await app.waitText('vps-1', 20000);
  await sleep(2000);
  await shot('computers-sessions');

  // The side drawer: All computers, mac and vps, then Needs you, Wall, Settings, + Add a computer.
  step = 'drawer';
  await click(app, 'Computers and menu');
  await until(() => app.eval(`!!document.querySelector('.drawer[role=dialog]')`), { what: 'the drawer' });
  await shot('computers-drawer');
  await app.eval(`document.querySelector('.drawer').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);

  step = 'offline';
  await vps.stop();
  await sessions();
  await app.waitText("can't reach", 30000);
  await shot('computers-offline');

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

/** PNG (DPR 3) to WebP at 390 CSS px wide through a canvas; a crop is { y, h } in CSS px. */
async function convert(page) {
  for (const name of ['computers-found', 'computers-confirm', 'computers-code', 'computers-adding', 'computers-sessions', 'computers-drawer', 'computers-offline']) {
    let data;
    try { data = readFileSync(join(PNG, `${name}.png`)).toString('base64'); } catch { continue; }
    const c = crops[name] ?? { y: 0, h: 844 };
    const webp = await page.eval(`(async () => {
      const img = new Image(); img.src = 'data:image/png;base64,${data}'; await img.decode();
      const k = img.width / 390; const canvas = document.createElement('canvas'); canvas.width = 390; canvas.height = ${c.h};
      canvas.getContext('2d').drawImage(img, 0, ${c.y} * k, img.width, ${c.h} * k, 0, 0, 390, ${c.h});
      return canvas.toDataURL('image/webp', 0.85).split(',')[1]; })()`);
    writeFileSync(join(DIR, `phone-${name}.webp`), Buffer.from(webp, 'base64'));
    console.log('webp', `phone-${name}`);
  }
}
