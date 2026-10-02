// Phase B: the full phone flow through the app's screens (README.md).
//   node scripts/e2e-multicomputer/phase-b.mjs [--no-web-build] [--out <dir>] [--keep]
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  Browser, Env, NODES, api, click, interactive, pairDirect, setInput, sleep, tapTerminalText, terminalText, typeLine, until,
} from './lib.mjs';

const argv = process.argv.slice(2);
const flag = (name) => argv.includes(name);
const option = (name) => (argv.includes(name) && !argv[argv.indexOf(name) + 1]?.startsWith('--') ? argv[argv.indexOf(name) + 1] : undefined);

const results = [];
const timings = [];
function check(id, name, pass, evidence) {
  results.push({ id, name, result: pass ? 'PASS' : 'FAIL', evidence: String(evidence) });
  console.log(`${pass ? 'PASS' : 'FAIL'} ${id} ${name}\n     ${evidence}`);
}
const time = (what, ms) => { timings.push({ what, ms }); console.log(`time ${what}: ${ms} ms`); };

const env = await Env.create({ buildWeb: !flag('--no-web-build') });
const { mac, vps } = env.stacks;
const out = option('--out') ?? join(env.root, 'evidence');
mkdirSync(out, { recursive: true });
console.log(`root ${env.root}\nfront ${env.proxy}`);

// Demo projects on both computers.
for (const stack of [mac, vps]) {
  mkdirSync(join(stack.home, 'demo'), { recursive: true });
  writeFileSync(join(stack.home, 'demo', 'README.md'), `# demo project on ${stack.name}\n`);
  await stack.cli(['project', 'add', '--name', 'demo', join(stack.home, 'demo')]);
}

const browser = await Browser.launch(env);
console.log(browser.product);
const app = await browser.page(); // the phone app, with its real CSP
const tool = await browser.page({ bypassCSP: true }); // harness-only API calls (same cookie jar)
/**
 * Screenshots hide add-computer links in the terminal first: the CLI asks
 * never to put the single-use grant in screenshots, even a used test one.
 */
const shot = async (name, page = app) => {
  await page.eval(`(() => { const rows = [...document.querySelectorAll('.xterm-rows > div')];
    rows.forEach((row, i) => { if (row.textContent.includes('add-computer#grant=')) for (const r of rows.slice(i, i + 3)) if (!/^(Open|Phone|Approve)/.test(r.textContent)) r.textContent = '[add-computer link hidden]'; }); })()`).catch(() => undefined);
  await page.shot(join(out, `${name}.png`)).catch(() => undefined);
};

/** Runs one step; a thrown error becomes a FAIL row with a screenshot. */
async function step(id, name, fn) {
  try {
    await fn();
  } catch (error) {
    await shot(`fail-${id}`);
    const text = await app.text().catch(() => '');
    check(id, name, false, `${error.message.split('\n')[0]} | screen: ${text.replace(/\s+/g, ' ').slice(0, 300)}`);
  }
}

const rows = () => app.eval(`[...document.querySelectorAll('.session-row')].map((b) => ({
  title: b.querySelector('.row-title')?.firstChild?.textContent.trim(), meta: b.querySelector('.row-meta')?.innerText.trim() }))`);
const problems = () => app.eval(`[...document.querySelectorAll('.computer-problem')].map((e) => e.innerText.replace(/\\s+/g, ' ').trim())`);
const chips = () => app.eval(`[...document.querySelectorAll('[aria-label=Computers] .chip')].map((e) => e.innerText.trim())`);

async function settingsOf(stack) {
  const r = await api(tool, `${stack.origin}/api/v1/settings`);
  if (r.status !== 200) throw new Error(`settings on ${stack.name}: ${r.status ?? r.error}`);
  return r.json;
}

/** A session made through the API (harness setup), renamed to title. */
async function makeSession(stack, title) {
  const settings = await settingsOf(stack);
  const created = await api(tool, `${stack.origin}/api/v1/sessions`, 'POST', {
    operationId: crypto.randomUUID(), projectId: settings.projects[0].id, shellProfileId: settings.shellProfiles[0].id,
  });
  if (created.status !== 200 && created.status !== 201) throw new Error(`create on ${stack.name}: ${created.status} ${created.body}`);
  const session = created.json.session ?? created.json;
  const renamed = await api(tool, `${stack.origin}/api/v1/sessions/${session.id}`, 'PATCH', {
    operationId: crypto.randomUUID(), generation: session.generation, title,
  });
  if (renamed.status !== 200) throw new Error(`rename on ${stack.name}: ${renamed.status} ${renamed.body}`);
  return session;
}

/** Waits for the shell prompt in Live and takes control (a phone opens a session watching). */
async function liveReady(prompt = true) {
  await until(async () => (prompt ? /[%$#]\s*$/m : /\S/).test(await terminalText(app)), { what: prompt ? 'shell prompt' : 'terminal text' });
  if ((await app.text()).includes('Take control')) {
    await click(app, 'Take control');
    await until(async () => !(await app.text()).includes('Take control'), { what: 'control taken' });
  }
}

async function openSessions() {
  await app.front();
  await app.go(`${mac.origin}/`);
  await app.waitText('Sessions');
}

/** Adds vps through Settings → Computers → paste. Returns the outcome text. */
async function addByPaste(id) {
  const started = Date.now();
  const via = interactive(vps, ['pair', '--via', 'mac']);
  const [link] = await via.wait(/https:\/\/vps\.tailx\.ts\.net\/add-computer#\S+/);
  await openSessions();
  await click(app, 'Settings');
  await app.waitText('Add a computer');
  await click(app, '+ Add a computer');
  await setInput(app, '#add-link', link);
  await click(app, 'Check link');
  await app.waitText('Add this computer?');
  await app.waitText('Add vps');
  await shot(`${id}-confirm-sheet`);
  const sheet = await app.eval(`document.querySelector('.sheet, [role=dialog]')?.innerText ?? document.body.innerText`);
  await click(app, 'Add vps');
  await app.waitText('Continue');
  await click(app, 'Continue');
  await app.waitText('Does vps show this code?');
  await shot(`${id}-code`);
  const shown = await app.eval(`document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', '')`);
  const [, code] = await via.wait(/shows code (\d{3}-\d{3})/);
  via.end(code === shown ? 'y\n' : 'n\n');
  await via.done;
  const outcome = await until(async () => {
    const t = await app.text();
    return (t.includes('vps is added') && 'added') || (/role="alert"/.test('') ? '' : (await app.eval(`document.querySelector('[role=alert]')?.innerText ?? ''`)));
  }, { timeout: 20000, what: 'add outcome' });
  await shot(`${id}-outcome`);
  return { outcome, sheet: sheet.replace(/\s+/g, ' '), code, shown, ms: Date.now() - started };
}

/** Confirms vps on home through the API, as the app would after a good first read. */
async function confirmByApi() {
  await until(async () => (await api(tool, `${vps.origin}/api/v1/settings`)).status === 200, { what: 'vps settings 200' });
  const r = await api(tool, `${mac.origin}/api/v1/computers`, 'POST', { label: 'vps', origin: vps.origin, state: 'confirmed' });
  if (r.status !== 200) throw new Error(`confirm: ${r.status} ${r.body}`);
}

const tokens = {};

try {
  // ---- B1: pair with mac (home), as today ----
  await app.go(`${mac.origin}/`);
  await step('B1', 'Pair the phone with mac (home)', async () => {
    const r = await pairDirect(app, mac, 'Demo phone');
    await app.waitText('Sessions', 30000);
    time('pairing with home (link opened → Sessions)', r.ms);
    await shot('b1-home-paired');
    check('B1', 'Pair the phone with mac (home)', true, `code ${r.code} matched, ${r.ms} ms`);
  });
  await tool.go(`${mac.origin}/`);
  await app.front();

  // ---- B2: add vps by paste ----
  await step('B2', 'Add vps: paste link, confirm sheet, code, approve on vps CLI', async () => {
    const r = await addByPaste('b2');
    time('add computer by paste (pterm pair --via → outcome)', r.ms);
    const sheetOk = r.sheet.includes('https://vps.tailx.ts.net') && r.sheet.includes('ada@example.com');
    check('B2-sheet', 'Confirm sheet shows the full origin, machine and owner', sheetOk, r.sheet.slice(0, 240));
    check('B2-code', 'Same code on the app and on the vps CLI', r.code === r.shown, `app ${r.shown}, CLI ${r.code}`);
    check('B2', 'Add vps by paste ends with "vps is added"', r.outcome === 'added', `outcome: ${r.outcome}`);
    if (r.outcome !== 'added') {
      await confirmByApi();
      console.log('     (harness confirmed vps on home through the API to continue)');
    }
  });

  // ---- B3: one Sessions list, open a vps session, echo, new session on vps ----
  await step('B3', 'One Sessions list across mac and vps', async () => {
    await makeSession(mac, 'mac-1');
    await makeSession(vps, 'vps-1');
    const started = Date.now();
    await openSessions();
    await until(async () => { const r = await rows(); return r.some((x) => x.title === 'mac-1') && r.some((x) => x.title === 'vps-1'); }, { what: 'rows from both' });
    time('Sessions list load (navigation → rows from both computers)', Date.now() - started);
    const r = await rows();
    await shot('b3-sessions');
    const c = await chips();
    const labelled = r.find((x) => x.title === 'mac-1')?.meta.includes('mac') && r.find((x) => x.title === 'vps-1')?.meta.includes('vps');
    check('B3-list', 'Sessions list shows sessions from both computers with labels and chips All/mac/vps',
      labelled && ['All', 'mac', 'vps'].every((x) => c.includes(x)), `chips ${JSON.stringify(c)}; rows ${JSON.stringify(r)}`);
  });

  await step('B3-echo', 'Open a vps session from the mac app and echo round-trip', async () => {
    const started = Date.now();
    await click(app, 'vps-1');
    await until(() => app.eval(`location.pathname.startsWith('/c/vps.tailx.ts.net/sessions/')`), { what: 'peer route' });
    await until(async () => /[%$#]\s*$/m.test(await terminalText(app)), { what: 'shell prompt' });
    time('open vps session → live prompt', Date.now() - started);
    await liveReady();
    const path = await app.eval('location.pathname');
    tokens.echo = `e2e-${Date.now() % 100000}`;
    const typed = Date.now();
    await typeLine(app, `echo ${tokens.echo}-$((6*7))`);
    await until(async () => (await terminalText(app)).includes(`${tokens.echo}-42`), { what: 'echo output' });
    time('echo round-trip on vps (Enter → output painted)', Date.now() - typed);
    await shot('b3-vps-live-echo');
    check('B3-echo', 'Open a vps session from the mac app and echo round-trip', true, `${path}; output ${tokens.echo}-42`);
  });

  await step('B3-new', 'Start a new session on vps from the mac app', async () => {
    await openSessions();
    await app.waitText('new session on…');
    await click(app, 'new session on…');
    await app.waitText('New session');
    await app.eval(`[...document.querySelectorAll('[aria-label=Computers] .chip')].find((e) => e.innerText.trim() === 'vps').click()`);
    await app.waitText('Runs on vps');
    await shot('b3-new-session-vps');
    const before = (await vps.cli(['list'])).stdout;
    await click(app, 'start session');
    await until(() => app.eval(`location.pathname.startsWith('/c/vps.tailx.ts.net/sessions/')`), { what: 'new vps session route' });
    const id = (await app.eval('location.pathname')).split('/').pop();
    const after = (await vps.cli(['list'])).stdout;
    check('B3-new', 'Start a new session on vps from the mac app ("Runs on vps")', after.includes(id) && !before.includes(id), `new session ${id} is in vps pterm list`);
  });

  // ---- B8 (before the Wall is ever opened): home offline ----
  await step('B8', 'Home offline: cached bundle, unvisited Wall, vps still reachable', async () => {
    const sw = await app.eval(`navigator.serviceWorker.getRegistrations().then((r) => r.map((x) => x.active?.scriptURL ?? 'installing'))`);
    await sleep(2000); // let the worker finish its install
    await mac.stop();
    try {
    const started = Date.now();
    await app.go(`${mac.origin}/`);
    const loaded = Date.now() - started;
    await app.waitText('vps-1', 30000).catch(() => undefined);
    time('cold open with home offline (navigation → vps rows)', Date.now() - started);
    const list = await rows();
    const probs = await problems();
    await shot('b8-home-offline-sessions');
    check('B8-shell', 'The cached app opens while home is offline', (await app.text()).includes('Sessions'), `service workers ${JSON.stringify(sw)}; load ${loaded} ms; problems ${JSON.stringify(probs)}`);
    check('B8-vps', 'vps sessions still listed and reachable with home offline', list.some((x) => x.title === 'vps-1'), JSON.stringify(list));
    await app.go(`${mac.origin}/wall`);
    await until(() => app.eval(`document.querySelectorAll('.wall-tile').length > 0`), { what: 'wall tiles', timeout: 30000 });
    await shot('b8-home-offline-wall');
    const tiles = await app.eval(`[...document.querySelectorAll('.wall-tile')].map((t) => t.querySelector('.wall-name')?.innerText)`);
    check('B8-wall', 'An unvisited Wall opens from the cache with home offline', tiles.length > 0, `tiles ${JSON.stringify(tiles)}`);
    } finally {
      await mac.start();
    }
  });

  // ---- B4: the Wall ----
  await step('B4', 'Wall: tiles from both, Paused beyond 4, thumbnails correct', async () => {
    // Fresh sessions after the restart of mac: 3 on mac, 3 on vps (vps-1 and the new one exist).
    for (const t of ['mac-2', 'mac-3', 'mac-4']) await makeSession(mac, t);
    await makeSession(vps, 'vps-2');
    // A ticking session on vps: open it, start output, leave.
    await openSessions();
    await until(async () => (await rows()).some((x) => x.title === 'vps-2'), { what: 'vps-2 row' });
    await click(app, 'vps-2');
    await liveReady();
    await typeLine(app, 'for i in $(seq 1 900); do echo tick-$i; sleep 1; done');
    await app.go(`${mac.origin}/wall`);
    await until(() => app.eval(`document.querySelectorAll('.wall-tile').length >= 6`), { what: '6 tiles' });
    await sleep(4000);
    const tiles = () => app.eval(`[...document.querySelectorAll('li[data-tile]')].map((li) => ({ key: li.dataset.tile,
      name: li.querySelector('.wall-name')?.innerText, computer: li.querySelector('.wall-computer')?.innerText.trim(),
      paused: /\\bpaused\\b/i.test(li.querySelector('.wall-screen')?.innerText ?? ''), text: li.querySelector('.wall-host')?.innerText.replace(/\\s+/g, ' ').trim() }))`);
    const a = await tiles();
    await sleep(3000);
    const b = await tiles();
    await shot('b4-wall');
    const both = a.some((t) => t.key.includes('mac.tailx.ts.net') || t.computer === 'mac') && a.some((t) => t.computer === 'vps');
    const paused = a.filter((t) => t.paused).length;
    check('B4-both', 'Wall shows tiles from both computers', both, a.map((t) => `${t.name}@${t.computer}${t.paused ? ' (Paused)' : ''}`).join(', '));
    check('B4-paused', 'With more than 4 sessions, tiles beyond 4 show Paused', a.length > 4 && paused === a.length - 4, `${a.length} tiles, ${paused} Paused`);
    const tick = (t) => Number(/tick-(\d+)(?!.*tick-)/.exec(t?.text ?? '')?.[1] ?? NaN);
    const ta = a.find((t) => t.name === 'vps-2');
    const tb = b.find((t) => t.name === 'vps-2');
    const echoTile = a.find((t) => t.name === 'vps-1');
    check('B4-thumb', 'Thumbnails stay correct: a live tile follows its session, a paused tile stays still',
      ta && tb && (ta.paused ? tick(ta) === tick(tb) || Number.isNaN(tick(ta)) : tick(tb) > tick(ta)) && (echoTile?.paused || echoTile?.text.includes(`${tokens.echo}-42`)),
      `vps-2 ${ta?.paused ? 'Paused' : 'live'}: tick ${tick(ta)} → ${tick(tb)} in 3 s; vps-1 ${echoTile?.paused ? 'Paused' : `shows ${tokens.echo}-42: ${echoTile?.text.includes(`${tokens.echo}-42`)}`}`);
  });

  // ---- B5: vps offline, then back ----
  await step('B5', 'vps offline: mac still works, "vps · can\'t reach", recovery', async () => {
    await vps.stop();
    try {
    await openSessions();
    await until(async () => (await problems()).some((p) => p.includes("vps · can't reach")), { what: "vps · can't reach", timeout: 30000 });
    const probs = await problems();
    await shot('b5-vps-unreachable');
    await click(app, 'mac-2');
    await liveReady();
    await typeLine(app, 'echo mac-still-$((2+3))');
    await until(async () => (await terminalText(app)).includes('mac-still-5'), { what: 'mac echo' });
    check('B5-down', 'With vps stopped, mac sessions work and "vps · can\'t reach" shows', true, `${JSON.stringify(probs)}; mac echo ok`);
    } finally {
      await vps.start();
    }
    await openSessions();
    await until(async () => (await problems()).some((p) => p.includes("can't reach")), { what: 'still unreachable before Retry', timeout: 5000 }).catch(() => undefined);
    if ((await problems()).length) await click(app, 'Retry');
    const started = Date.now();
    await until(async () => (await problems()).length === 0 && (await rows()).some((x) => x.meta.includes('vps') || x.title.startsWith('vps')), { what: 'vps back', timeout: 30000 })
      .catch(async () => until(async () => (await problems()).length === 0, { what: 'no problems', timeout: 10000 }));
    time('vps recovery (Retry → no problem row)', Date.now() - started);
    await shot('b5-vps-recovered');
    check('B5-up', 'After vps restarts, Retry recovers it', (await problems()).length === 0, `problems ${JSON.stringify(await problems())}`);
  });

  // ---- B6: revoke --via on vps ----
  await step('B6', 'pterm revoke --via closes the vps stream and shows "pair again" for vps only', async () => {
    await makeSession(vps, 'vps-3');
    await openSessions();
    await until(async () => (await rows()).some((x) => x.title === 'vps-3'), { what: 'vps-3 row' });
    await click(app, 'vps-3');
    await liveReady();
    const mark = env.logMark();
    const revoke = interactive(vps, ['revoke', '--via', mac.origin]);
    await revoke.wait(/\[y\/N\]/);
    revoke.end('y\n');
    await revoke.done;
    const left = await until(() => app.eval(`!location.pathname.startsWith('/c/vps.tailx.ts.net/')`), { what: 'Live leaves the revoked session', timeout: 15000 }).catch(() => false);
    await until(async () => (await problems()).length > 0, { what: 'problem row', timeout: 15000 }).catch(() => undefined);
    const now = await problems();
    const list = await rows();
    await shot('b6-after-revoke');
    const first = env.logSince(mark).find((e) => e.host === vps.host && e.status === 401);
    check('B6-stream', 'The open vps stream closes on revoke --via (Live leaves the session)', left,
      `path now ${await app.eval('location.pathname')}; revoke said: ${revoke.out.trim().split('\n').pop()}`);
    check('B6-pair', '"vps · pair again" shows for vps only; mac sessions still listed', now.length === 1 && now[0].includes('vps · pair again') && list.some((x) => x.title.startsWith('mac')),
      `${JSON.stringify(now)}; mac rows ${list.filter((x) => x.title.startsWith('mac')).length}; first vps answer: ${first ? `${first.method} ${first.path} 401 ACAO=${first.acao} Set-Cookie ${first.setCookie}` : 'none'}`);
    const mark2 = env.logMark();
    await openSessions();
    await until(async () => (await problems()).length > 0, { what: 'problem row after reload', timeout: 20000 });
    await sleep(6000);
    const reloaded = await problems();
    await shot('b6-after-reload');
    const later = env.logSince(mark2).filter((e) => e.host === vps.host).map((e) => `${e.method} ${e.path} cookie=${e.cookie} ${e.status} ACAO=${e.acao || 'none'}`);
    check('B6-reload', '"vps · pair again" still shows after the app reloads', reloaded.some((p) => p.includes('vps · pair again')),
      `${JSON.stringify(reloaded)}; vps answers: ${[...new Set(later)].join('; ')}`);
  });

  // ---- B2b: add vps again through the link printed in a mac terminal ----
  const vpsCommand = () => {
    const e = vps.processEnv;
    // Stands in for `ssh vps` then `pterm pair --via mac`: vps's own pterm and environment.
    return `env -i HOME='${e.HOME}' PATH='${e.PATH}' PTERM_E2E_TAILSCALE_DIR='${e.PTERM_E2E_TAILSCALE_DIR}' TMPDIR='${e.TMPDIR}' ${env.pterm} pair --via mac --dev --runtime-dir '${vps.runtimeDir}'`;
  };
  const sheetOpen = () => app.eval(`!!document.querySelector('#add-computer-title')`);
  async function pasteLink(link) {
    await openSessions();
    await click(app, 'Settings');
    await app.waitText('Add a computer');
    await click(app, '+ Add a computer');
    await setInput(app, '#add-link', link);
    await click(app, 'Check link');
  }
  /** Runs pterm pair --via in the mac-ssh terminal, taps the link (paste when the tap does nothing), then Add vps and Continue. */
  async function terminalLinkToCode(id, title) {
    await makeSession(mac, title);
    await openSessions();
    await until(async () => (await rows()).some((x) => x.title === title), { what: `${title} row` });
    await click(app, title);
    await liveReady();
    const terminalPath = await app.eval('location.pathname');
    await typeLine(app, 'clear');
    await typeLine(app, vpsCommand());
    await until(async () => (await terminalText(app)).includes('Waiting for the phone'), { what: 'link in terminal' });
    const link = /https:\/\/vps\.tailx\.ts\.net\/add-computer#grant=[A-Za-z0-9_-]{43}&via=https%3A%2F%2Fmac\.tailx\.ts\.net/.exec((await terminalText(app)).replace(/\n/g, ''))?.[0];
    const started = Date.now();
    await tapTerminalText(app, 'https://vps.tailx.ts.net/add');
    const tapped = await until(sheetOpen, { timeout: 3000, what: 'sheet' }).catch(() => false);
    if (!tapped) await pasteLink(link);
    await until(sheetOpen, { what: 'confirm sheet' });
    await app.waitText('Add vps');
    await shot(`${id}-confirm-sheet`);
    await click(app, 'Add vps');
    await app.waitText('Continue');
    await click(app, 'Continue');
    await app.waitText('Does vps show this code?');
    const shown = await app.eval(`document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', '')`);
    return { shown, started, tapped, terminalPath };
  }

  const banner = () => app.eval(`document.querySelector('.banner')?.innerText.replace(/\\s+/g, ' ').trim() ?? ''`);
  /** In-app navigation, as the app's own links do: no new page load. */
  const inApp = (path) => app.eval(`history.pushState(null, '', ${JSON.stringify(path)}); dispatchEvent(new PopStateEvent('popstate'))`);

  /**
   * Adds vps from a link printed in a mac terminal and approves it with y in
   * that same terminal. back leaves the waiting add page: the browser's Back,
   * or the page's own "Back to terminal" button.
   */
  async function addFromTerminal(id, title, back) {
    const { shown, started, tapped, terminalPath } = await terminalLinkToCode(id, title);
    if (back === 'button') await click(app, 'Back to terminal');
    else await app.eval('history.back()');
    await until(() => app.eval(`!!document.querySelector('.terminal-input')`), { what: 'back in the terminal', timeout: 15000 });
    const path = await app.eval('location.pathname');
    await liveReady(false);
    await until(async () => (await banner()).includes('Adding vps'), { what: 'banner "Adding vps…"', timeout: 5000 }).catch(() => undefined);
    const waiting = await banner();
    await until(async () => (await terminalText(app)).replace(/\n/g, '').includes(`shows code ${shown}`), { what: 'prompt with the code', timeout: 15000 });
    await shot(`${id}-banner`);
    await typeLine(app, 'y');
    await until(async () => (await banner()).includes('vps is added'), { what: 'banner "vps is added"', timeout: 20000 }).catch(() => undefined);
    const done = await banner();
    await shot(`${id}-added`);
    time(`add from a terminal link, ${back === 'button' ? '"Back to terminal"' : 'Back'}, y in the same app (tap → "vps is added")`, Date.now() - started);
    await inApp('/');
    await until(async () => (await problems()).length === 0 && (await rows()).some((x) => x.meta.startsWith('vps')), { what: 'vps rows', timeout: 20000 }).catch(() => undefined);
    const probs = await problems();
    const vpsRows = (await rows()).filter((x) => x.meta.startsWith('vps')).length;
    const pass = path === terminalPath && waiting.includes('Adding vps') && done.includes('vps is added') && probs.length === 0 && vpsRows > 0;
    if (!done.includes('vps is added')) await confirmByApi().catch(() => undefined);
    return { tapped, pass, evidence: `back to ${path} (terminal ${terminalPath}); banner while waiting: ${waiting || 'none'}; after y: ${done || 'none'}; problems ${JSON.stringify(probs)}; vps rows ${vpsRows}` };
  }

  await step('B2b-back', 'Tap the terminal link, leave with Back, approve with y in the same app', async () => {
    const r = await addFromTerminal('b2b-back', 'term-back', 'history');
    check('B2b-tap', 'Tapping the add-computer link in the terminal opens the confirm sheet', r.tapped,
      r.tapped ? 'sheet opened from the tap' : 'the tap did not open the sheet; continued by pasting the link');
    check('B2b-back', 'Back from the waiting add page returns to the terminal, the add keeps running, y adds vps', r.pass, r.evidence);
  });

  await step('B2b-same', 'Tap the terminal link, "Back to terminal", approve with y in the same app', async () => {
    const r = await addFromTerminal('b2b', 'term-button', 'button');
    check('B2b-same', '"Back to terminal" returns to the terminal, the add keeps running, y adds vps', r.pass, r.evidence);
  });

  // ---- B7: security ----
  await step('B7-direct', 'A device paired directly with vps cannot be driven from the mac app', async () => {
    const other = await Browser.launch(env, { profile: 'chrome-direct' });
    try {
      const p = await other.page();
      await p.go(`${vps.origin}/`);
      await pairDirect(p, vps, 'Direct phone');
      await p.waitText('Sessions', 30000);
      const own = await api(p, `${vps.origin}/api/v1/settings`);
      const evilTool = await other.page({ bypassCSP: true });
      await evilTool.go(`${mac.origin}/`);
      const mark = env.logMark();
      const fromMac = await api(evilTool, `${vps.origin}/api/v1/settings`);
      const sessions = await api(evilTool, `${vps.origin}/api/v1/sessions`);
      const ws = await evilTool.eval(`new Promise((res) => { const w = new WebSocket('wss://vps.tailx.ts.net/api/v1/ws');
        w.onopen = () => res('open'); w.onerror = () => res('error'); w.onclose = (e) => res('closed ' + e.code); setTimeout(() => res('timeout'), 5000); })`);
      await sleep(300);
      const log = env.logSince(mark).filter((e) => e.host === vps.host);
      check('B7-direct', 'A device paired directly with vps cannot be driven from the mac app',
        own.status === 200 && fromMac.status !== 200 && sessions.status !== 200 && ws !== 'open',
        `own page ${own.status}; from mac origin: settings ${fromMac.status ?? fromMac.error}, sessions ${sessions.status ?? sessions.error}, WebSocket ${ws}; ${log.map((e) => `${e.method} ${e.path} ${e.status}`).join(', ')}`);
    } finally {
      await other.close();
    }
  });

  await step('B7-node', 'Changing mac\'s node ID on vps revokes the bound devices', async () => {
    const before = (await vps.cli(['devices'])).stdout;
    vps.writeStatus({ ...NODES, mac: { ...NODES.mac, ID: 'nMacE2E2CHANGED' } });
    // The broker re-reads status after 60 s and checks it on each request.
    const started = Date.now();
    const verdict = await until(async () => {
      const r = await api(tool, `${vps.origin}/api/v1/settings`);
      return r.status !== 200 && (r.status ?? r.error);
    }, { timeout: 120000, every: 2000, what: 'bound request refused' });
    const refusedAfter = Math.round((Date.now() - started) / 1000);
    const after = await until(async () => {
      const text = (await vps.cli(['devices'])).stdout;
      return !text.split('\n').some((l) => l.includes('https://mac.tailx.ts.net') && / active /.test(l)) && text;
    }, { timeout: 150000, every: 3000, what: 'bound device revoked' }).catch(async () => (await vps.cli(['devices'])).stdout);
    time('node ID change → bound device revoked', Date.now() - started);
    await openSessions();
    await until(async () => (await problems()).some((p) => p.includes('vps · pair again')), { what: 'pair again', timeout: 30000 }).catch(() => undefined);
    await shot('b7-node-changed');
    const boundLine = (text) => text.split('\n').filter((l) => l.includes('https://mac.tailx.ts.net')).map((l) => l.replace(/\s+/g, ' ')).join(' / ');
    const active = (text) => text.split('\n').filter((l) => l.includes('https://mac.tailx.ts.net') && / active /.test(l)).length;
    check('B7-node', 'Changing mac\'s node ID on vps revokes the bound devices', active(before) > 0 && active(after) === 0,
      `refused (${verdict}) ${refusedAfter} s and revoked ${Math.round((Date.now() - started) / 1000)} s after the change; before: ${boundLine(before)}; after: ${boundLine(after)}; app: ${JSON.stringify(await problems())}`);
  });

  await step('B7-readd', 'Re-add vps after the node ID change', async () => {
    const r = await addByPaste('b7-readd');
    time('re-add vps after a node change (pterm pair --via → outcome)', r.ms);
    await openSessions();
    await until(async () => (await problems()).length === 0 && (await rows()).some((x) => x.meta.startsWith('vps')), { what: 'vps rows again', timeout: 20000 }).catch(() => undefined);
    const probs = await problems();
    const vpsRows = (await rows()).filter((x) => x.meta.startsWith('vps')).length;
    await shot('b7-readd-sessions');
    const devices = (await vps.cli(['devices'])).stdout.split('\n').filter((l) => l.includes('https://mac.tailx.ts.net') && / active /.test(l)).length;
    check('B7-readd', 'Re-adding vps after the node ID change works (new node pinned, sessions back)',
      r.outcome === 'added' && probs.length === 0 && vpsRows > 0 && devices === 1,
      `outcome ${r.outcome}; problems ${JSON.stringify(probs)}; vps rows ${vpsRows}; active bound devices on vps ${devices}`);
  });
} catch (error) {
  check('ERR', 'harness error', false, error.stack);
} finally {
  const table = ['| ID | Check | Result | Evidence |', '| --- | --- | --- | --- |',
    ...results.map((r) => `| ${r.id} | ${r.name} | ${r.result} | ${r.evidence.replaceAll('|', '\\|').replaceAll('\n', ' ')} |`),
    '', '| Timing | ms |', '| --- | --- |', ...timings.map((t) => `| ${t.what} | ${t.ms} |`)].join('\n');
  writeFileSync(join(out, 'phase-b.md'), `${table}\n`);
  writeFileSync(join(out, 'phase-b.json'), JSON.stringify({ chrome: browser.product, results, timings }, null, 1));
  console.log(`\n${table}\n\nevidence: ${out}`);
  if (flag('--keep')) {
    console.log(`kept running; front ${env.proxy} spki ${env.spki}. Ctrl+C stops.`);
    await new Promise((r) => process.once('SIGINT', r));
  }
  await browser.close();
  await env.close();
  process.exit(results.every((r) => r.result === 'PASS') ? 0 : 1);
}
