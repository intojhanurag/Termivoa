// Phase B: the full phone flow through the app's screens (README.md).
//   node scripts/e2e-multicomputer/phase-b.mjs [--no-web-build] [--out <dir>] [--keep] [--only <ID prefix>]
// --only PV runs pairing, the first add (B1, B2) and the steps whose ID starts with PV.
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  Browser, Env, NODES, api, click, freePort, interactive, pairDirect, run, setInput, sleep, tapTerminalText, terminalText, typeLine, until,
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
 * Screenshots hide add-computer links and QR codes (rows of half blocks,
 * printed above the link) in the terminal first: the CLI asks never to put
 * the single-use grant in screenshots, even a used test one.
 */
const shot = async (name, page = app) => {
  await page.eval(`(() => { const rows = [...document.querySelectorAll('.xterm-rows > div')];
    rows.forEach((row) => { if (/[▀▄█]/.test(row.textContent) && /^[\\s▀▄█]*$/.test(row.textContent)) row.textContent = '[QR code hidden]'; });
    rows.forEach((row, i) => { if (row.textContent.includes('add-computer#grant=')) for (const r of rows.slice(i, i + 3)) if (!/^(Open|Phone|Approve)/.test(r.textContent)) r.textContent = '[add-computer link hidden]'; }); })()`).catch(() => undefined);
  await page.shot(join(out, `${name}.png`)).catch(() => undefined);
};

/** Runs one step; a thrown error becomes a FAIL row with a screenshot. */
async function step(id, name, fn) {
  const only = option('--only');
  if (only && id !== 'B1' && id !== 'B2' && !id.startsWith(only)) return;
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
async function addByPaste(id, whilePending = async () => undefined) {
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
  await whilePending();
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
/** In-app navigation, as the app's own links do: no new page load. */
const inApp = (path) => app.eval(`history.pushState(null, '', ${JSON.stringify(path)}); dispatchEvent(new PopStateEvent('popstate'))`);

// ---------- dev-server preview (docs/peer-preview.md) ----------
const devPorts = {};
const previewLog = (mark, stack) => env.logSince(mark).filter((e) => e.host === `${stack.host}:8443`);
const briefly = (e) => (e ? `${e.method} ${e.path} ${e.status} site=${e.secFetchSite ?? '-'} dest=${e.secFetchDest ?? '-'} referer=${e.referer ?? '-'} origin=${e.origin ?? '-'} cookie=${e.cookie}` : 'no request');
/** The preview frame's text, or undefined while there is no frame on that computer's preview origin. */
const frameText = (stack, page = app) => page.frameEval(stack.previewOrigin, 'document.body.innerText').catch(() => undefined);
const frameReady = (stack, timeout = 30000) => until(async () => {
  const text = await frameText(stack);
  return text?.includes(`dev site on ${stack.name}`) && text.includes('sub ok') && text.includes('fetch pong') && text.includes('ws echo') && text;
}, { timeout, what: `the dev site of ${stack.name} in the frame` });
/** The browser's cookies for one computer (the preview origin shares them: cookies ignore ports), for curl. */
const jarOf = async (stack) => (await browser.cookies()).filter((c) => c.domain === stack.host).map((c) => `${c.name}=${c.value}`).join('; ');

/** A new session on stack that runs the harness dev site, opened in Live from the Sessions list (no page load). Returns the session. */
async function devSession(stack, title) {
  const session = await makeSession(stack, title);
  await inApp('/');
  await until(async () => (await rows()).some((x) => x.title === title), { what: `${title} row` });
  await click(app, title);
  await liveReady();
  devPorts[title] = await freePort();
  await typeLine(app, `${join(env.bin, 'front')} devserver -name ${stack.name} -listen 127.0.0.1:${devPorts[title]}`);
  await until(async () => (await app.text()).includes('Preview ready') && (await app.text()).includes(`:${devPorts[title]}`), { timeout: 30000, what: 'the Preview ready chip' });
  return session;
}
/** The policy home sends with the app document now, read by the app's own page. */
const policyNow = async () => {
  const csp = await app.eval(`fetch('/', { cache: 'no-store' }).then((r) => r.headers.get('content-security-policy'))`);
  const of = (name) => new RegExp(`${name} ([^;]*)`).exec(csp)?.[1].split(' ') ?? [];
  return { frame: of('frame-src'), connect: of('connect-src') };
};
let pendingPolicy;
/** After a restart of vps: its gateway learns the mac app origin again, then the app's problem row goes. */
async function vpsBack() {
  await until(() => settingsOf(vps), { what: 'vps answers the mac app', timeout: 40000, every: 500 });
  await inApp('/');
  await until(async () => { if ((await problems()).length) await click(app, 'Retry').catch(() => undefined); return (await problems()).length === 0; }, { what: 'no problem row', timeout: 40000, every: 1000 });
}
const openPreview = (title) => click(app, `Open preview of port ${devPorts[title]}`);

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
    const r = await addByPaste('b2', async () => { pendingPolicy = await policyNow(); });
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

  // ---- PV: dev-server preview, home and peer (docs/peer-preview.md section 11) ----
  let vpsDev;

  await step('PV-added', 'A computer added in this visit: Preview ready shows; Open reloads the app once, then frames the vps dev site', async () => {
    // vps was confirmed after this app document loaded, so the loaded policy does not name its preview origin yet.
    vpsDev = await devSession(vps, 'vps-dev');
    await shot('pv-chip-vps');
    check('PV-chip', 'The Preview ready chip shows for a vps session', true, `chip :${devPorts['vps-dev']} on ${await app.eval('location.pathname')}`);
    await app.eval(`window.__sameLoad = true; document.addEventListener('securitypolicyviolation', (e) => sessionStorage.setItem('e2e.blocked', e.effectiveDirective + ' ' + e.blockedURI))`);
    const mark = env.logMark();
    await openPreview('vps-dev');
    await frameReady(vps);
    const blocked = await app.eval(`sessionStorage.getItem('e2e.blocked')`);
    const reloaded = !(await app.eval('window.__sameLoad === true'));
    const guard = await app.eval(`sessionStorage.getItem('pterm.previewReload')`);
    const loads = env.logSince(mark).filter((e) => e.host === mac.host && e.secFetchMode === 'navigate').map((e) => `${e.method} ${e.path} ${e.status}`);
    const now = await policyNow();
    const pending = pendingPolicy ?? { frame: ['not read'], connect: [] };
    check('PV-pending', 'While vps was pending (B2) it was in connect-src for pairing, but its preview origin was in neither frame-src nor connect-src; confirmed, it is in both, never as wss',
      pending.connect.includes(vps.origin) && !pending.frame.includes(vps.previewOrigin) && !pending.connect.includes(vps.previewOrigin)
        && now.frame.includes(vps.previewOrigin) && now.connect.includes(vps.previewOrigin) && !now.connect.some((x) => x.startsWith('wss:') && x.endsWith(':8443')),
      `pending: frame-src ${pending.frame.join(' ')} | connect-src ${pending.connect.join(' ')}; confirmed: frame-src ${now.frame.join(' ')} | connect-src ${now.connect.join(' ')}`);
    check('PV-added', 'A computer added in this visit: the loaded policy blocks its preview origin, the app reloads once and then shows the preview',
      blocked?.includes(vps.previewOrigin) && reloaded && guard === null && loads.length === 1 && now.frame.includes(vps.previewOrigin),
      `blocked: ${blocked}; app reloaded: ${reloaded} (document loads: ${loads.join(', ')}); loop guard after the answer: ${guard}; frame-src now: ${now.frame.join(' ')}`);
  });

  await step('PV-vps', 'The vps preview in the app: bar "vps · untrusted page", sub-resource, fetch, WebSocket', async () => {
    const mark = env.logMark();
    await click(app, 'Reload preview'); // a fresh select with the final policy, so the log below is one clean load
    const text = await frameReady(vps);
    const bar = await app.eval(`document.querySelector('.preview-bar').innerText.replace(/\\s+/g, ' ').trim()`);
    const title = await app.eval(`document.querySelector('.preview-frame').title`);
    await shot('pv-vps-preview');
    check('PV-vps-bar', 'Over the vps page the bar shows "vps · untrusted page"', bar.includes('vps · untrusted page') && bar.includes(`localhost:${devPorts['vps-dev']}`), `bar: ${bar}; frame title: ${title}`);
    check('PV-vps-page', 'The framed vps dev site works: marker, sub-resource, fetch and WebSocket echo', true, text.replace(/\s+/g, ' '));
    await sleep(300);
    const log = previewLog(mark, vps);
    const probe = log.find((e) => e.path === '/__termivoa/select' && e.secFetchMode === 'no-cors');
    const select = log.find((e) => e.path === '/__termivoa/select' && e.secFetchDest === 'iframe');
    const root = log.find((e) => e.path === '/');
    check('PV-vps-probe', 'The app asks the preview origin first: a no-cors request without cookie; any answer counts', probe && !probe.cookie && probe.status === 403, briefly(probe));
    check('PV-vps-select', 'The select request: no Origin, Referer https://mac.tailx.ts.net/ (unchanged by the front), same-site, the Strict cookie, 200 with frame-ancestors of the mac app',
      select?.status === 200 && select.referer === `${mac.origin}/` && !select.origin && select.secFetchSite === 'same-site' && select.cookie, briefly(select));
    check('PV-vps-root', 'The move to / and every dev-site request come from the preview origin itself (same-origin)',
      root?.status === 200 && root.secFetchSite === 'same-origin' && !root.referer && log.filter((e) => e.path !== '/__termivoa/select').every((e) => e.status === 200 || e.status === 101),
      log.filter((e) => e.path !== '/__termivoa/select').map(briefly).join('; '));

    // ↻
    const load = () => app.frameEval(vps.previewOrigin, `Number(document.getElementById('load').textContent.replace('load ', ''))`).catch(() => undefined);
    const before = await load();
    const mark2 = env.logMark();
    await click(app, 'Reload preview');
    const after = await until(async () => { const n = await load(); return n > before && n; }, { what: 'a new load of the dev site' });
    await frameReady(vps);
    await sleep(300);
    const selects = previewLog(mark2, vps).filter((e) => e.secFetchDest === 'iframe' && e.path === '/__termivoa/select').map((e) => e.status);
    check('PV-vps-reload', '↻ loads the preview again through exactly one new select request', selects.length === 1 && selects[0] === 200, `dev site load ${before} → ${after}; select requests: ${selects.join(', ')}`);

    // The dev site (same site as both apps, so its requests carry their cookies) may not use an API.
    const mark3 = env.logMark();
    const tries = await app.frameEval(vps.previewOrigin, `Promise.all(${JSON.stringify([mac.origin, vps.origin])}.map((o) =>
      fetch(o + '/api/v1/settings', { credentials: 'include' }).then((r) => r.status, (e) => String(e))))`);
    await sleep(300);
    const seen = env.logSince(mark3).filter((e) => e.path === '/api/v1/settings' && e.origin === vps.previewOrigin);
    check('PV-vps-api', 'The framed vps dev site cannot call the mac API or the vps API', seen.length === 2 && seen.every((e) => e.status === 403 && !e.acao),
      `page sees ${JSON.stringify(tries)}; ${seen.map((e) => `${e.host} ${e.status} cookie=${e.cookie} ACAO=${e.acao ?? 'none'}`).join('; ')}`);
  });

  await step('PV-link', 'A link alone never opens a peer preview; a reload after a real tap keeps it', async () => {
    const address = `${mac.origin}/c/${vps.host}/sessions/${vpsDev.id}/preview?port=${devPorts['vps-dev']}`;
    // The preview is open from the tap in PV-added: a reload of that entry keeps it.
    await app.eval(`window.__sameLoad = true`);
    await app.eval('location.reload()');
    await until(async () => !(await app.eval('window.__sameLoad === true').catch(() => true)), { what: 'the reload' });
    await frameReady(vps);
    check('PV-link-reload', 'A reload of the preview that a tap opened shows the preview again', (await app.eval('location.href')) === address, `${await app.eval('location.pathname + location.search')}; history.state ${JSON.stringify(await app.eval('history.state'))}`);
    // The same address as a link from outside: a new tab (no history state), and a navigation of the app tab.
    const cold = await browser.page();
    try {
      await cold.front();
      const mark = env.logMark();
      await cold.go(`${mac.origin}/`); // the address below is replaced, so load something first
      await cold.send('Page.navigate', { url: address });
      await until(async () => (await terminalText(cold).catch(() => '')).includes('dev site on vps'), { what: 'the terminal of that session' });
      await sleep(1500);
      const asked = previewLog(mark, vps);
      const state = { path: await cold.eval('location.pathname + location.search'), frame: await cold.eval(`!!document.querySelector('.preview-frame, .preview-screen')`) };
      await shot('pv-link-cold', cold);
      check('PV-link-cold', 'A cold navigation to the peer preview address lands on the session\'s terminal: no frame, no request to vps:8443',
        state.path === `/c/${vps.host}/sessions/${vpsDev.id}` && !state.frame && asked.length === 0, `now ${state.path}; preview screen: ${state.frame}; requests to ${vps.host}:8443: ${asked.length}`);
      // A tap marker cannot be forged with a port the session does not list.
      const mark2 = env.logMark();
      await cold.eval(`history.pushState({ previewTap: true }, '', ${JSON.stringify(`/c/${vps.host}/sessions/${vpsDev.id}/preview?port=1`)}); dispatchEvent(new PopStateEvent('popstate'))`);
      await sleep(1500);
      check('PV-link-port', 'A preview address with a port the session does not list opens the terminal, also with the tap marker',
        (await cold.eval('location.pathname + location.search')) === `/c/${vps.host}/sessions/${vpsDev.id}` && previewLog(mark2, vps).length === 0, `now ${await cold.eval('location.pathname + location.search')}; requests to ${vps.host}:8443: ${previewLog(mark2, vps).length}`);
    } finally {
      await cold.close();
      await app.front();
    }
  });

  await step('PV-refuse', 'The vps preview listener refuses every request that is not the app frame or the preview origin itself', async () => {
    const jar = await jarOf(vps);
    const select = `${vps.previewOrigin}/__termivoa/select?session=${vpsDev.id}&port=${devPorts['vps-dev']}`;
    const frame = { Cookie: jar, 'Sec-Fetch-Site': 'same-site', 'Sec-Fetch-Mode': 'navigate', 'Sec-Fetch-Dest': 'iframe' };
    const say = (r) => `${r.status} ${r.body.trim().slice(0, 48)}`;
    const control = await env.curl(select, { headers: { ...frame, Referer: `${mac.origin}/` } });
    check('PV-refuse-control', 'Control: the select with the phone\'s cookie and Referer https://mac.tailx.ts.net/ works (curl)',
      control.status === 200 && control.headers['content-security-policy'] === `default-src 'none'; frame-ancestors ${mac.origin}`, `${control.status}; CSP ${control.headers['content-security-policy']}`);
    for (const [id, name, headers] of [
      ['evil', 'Referer https://evil.tailx.ts.net/', { Referer: 'https://evil.tailx.ts.net/' }],
      ['none', 'no Referer', {}],
      ['origin', 'an Origin header (with the right Referer)', { Referer: `${mac.origin}/`, Origin: mac.origin }],
      ['devsite', 'Referer of a dev site on mac (https://mac.tailx.ts.net:8443/)', { Referer: `${mac.previewOrigin}/` }],
      ['path', 'Referer with a path (https://mac.tailx.ts.net/x)', { Referer: `${mac.origin}/x` }],
      ['bare', 'Referer without the slash', { Referer: mac.origin }],
    ]) {
      const r = await env.curl(select, { headers: { ...frame, ...headers } });
      check(`PV-refuse-${id}`, `A select with ${name} is refused (curl, valid cookie)`, r.status === 403 && !r.headers['set-cookie'], say(r));
    }
    const twice = await run('curl', ['-sS', '-k', '--suppress-connect-headers', '--proxy', `http://${env.proxy}`, '-o', '/dev/null', '-w', '%{http_code}',
      ...Object.entries(frame).flatMap(([k, v]) => ['-H', `${k}: ${v}`]), '-H', `Referer: ${mac.origin}/`, '-H', `referer: ${mac.origin}/`, select]);
    check('PV-refuse-two', 'A select with two Referer headers is refused (curl, valid cookie)', twice.stdout === '403', twice.stdout);
    const root = `${vps.previewOrigin}/`;
    const own = await env.curl(root, { headers: { Cookie: jar, 'Sec-Fetch-Site': 'same-origin' } });
    const sameSite = await env.curl(root, { headers: { Cookie: jar, 'Sec-Fetch-Site': 'same-site' } });
    const typed = await env.curl(root, { headers: { Cookie: jar } });
    const foreign = await env.curl(root, { headers: { Cookie: jar, Origin: mac.origin } });
    check('PV-refuse-direct', 'GET / on the listener with the valid cookies: same-origin works; same-site, no Sec-Fetch-Site, and the mac app as Origin are refused',
      own.status === 200 && own.body.includes('dev site on vps') && [sameSite, typed, foreign].every((r) => r.status === 403 && r.body.includes('Origin not allowed')),
      `same-origin ${own.status}; same-site ${say(sameSite)}; none ${say(typed)}; Origin mac ${say(foreign)}`);
  });

  await step('PV-evil', 'A page on evil.tailx.ts.net cannot frame or load the vps preview', async () => {
    const evil = await browser.page();
    try {
      await evil.go('https://evil.tailx.ts.net/');
      const mark = env.logMark();
      const select = `${vps.previewOrigin}/__termivoa/select?session=${vpsDev.id}&port=${devPorts['vps-dev']}`;
      const result = await evil.eval(`(async () => {
        const frame = (src) => new Promise((done) => { const f = document.createElement('iframe'); f.referrerPolicy = 'origin'; f.src = src; f.onload = () => done('loaded'); document.body.append(f); setTimeout(() => done('timeout'), 5000); });
        const img = new Promise((done) => { const i = new Image(); i.onload = () => done('loaded'); i.onerror = () => done('error'); i.src = ${JSON.stringify(`${vps.previewOrigin}/app.js`)}; });
        return { select: await frame(${JSON.stringify(select)}), root: await frame(${JSON.stringify(`${vps.previewOrigin}/`)}), img: await img,
          fetch: await fetch(${JSON.stringify(`${vps.previewOrigin}/ping`)}, { mode: 'no-cors', credentials: 'include' }).then((r) => r.type, (e) => String(e)),
          ws: await new Promise((done) => { const w = new WebSocket(${JSON.stringify(`wss://${vps.host}:8443/ws`)}); w.onopen = () => done('open'); w.onerror = () => done('error'); }) }; })()`);
      await sleep(300);
      const texts = (await evil.send('Page.getFrameTree')).frameTree.childFrames?.length ?? 0;
      const shown = await evil.frameEval(`${vps.previewOrigin}/__termivoa/select`, 'document.body.innerText').catch(() => undefined);
      const log = previewLog(mark, vps);
      await shot('pv-evil', evil);
      check('PV-evil', 'From a top-level page on evil.tailx.ts.net: framing the select, framing /, an image, a no-cors fetch and a WebSocket are all refused, with the cookie present',
        log.length === 5 && log.every((e) => e.status === 403) && log.some((e) => e.cookie) && result.ws === 'error' && result.img === 'error',
        `${log.map(briefly).join('; ')}; page: ${JSON.stringify(result)}; ${texts} frames; the select frame shows: ${shown?.trim()}`);
    } finally {
      await evil.close();
      await app.front();
    }
  });

  await step('PV-home', 'A mac (home) preview: chip, frame, no untrusted mark', async () => {
    await devSession(mac, 'mac-dev');
    const mark = env.logMark();
    await openPreview('mac-dev');
    const text = await frameReady(mac);
    const bar = await app.eval(`document.querySelector('.preview-bar').innerText.replace(/\\s+/g, ' ').trim()`);
    await shot('pv-mac-preview');
    await sleep(300);
    const select = previewLog(mark, mac).find((e) => e.secFetchDest === 'iframe');
    check('PV-home', 'A mac (home) session: Preview ready, Open, the dev site in the frame with sub-resource, fetch and WebSocket; the bar has no "untrusted page"',
      !bar.includes('untrusted') && bar.includes(`localhost:${devPorts['mac-dev']}`) && select?.status === 200 && select.referer === `${mac.origin}/`,
      `bar: ${bar}; ${text.replace(/\s+/g, ' ')}; ${briefly(select)}`);
    const mark2 = env.logMark();
    const tries = await app.frameEval(mac.previewOrigin, `Promise.all(${JSON.stringify([mac.origin, vps.origin])}.map((o) =>
      fetch(o + '/api/v1/settings', { credentials: 'include' }).then((r) => r.status, (e) => String(e))))`);
    await sleep(300);
    const seen = env.logSince(mark2).filter((e) => e.path === '/api/v1/settings' && e.origin === mac.previewOrigin);
    check('PV-home-api', 'The framed mac dev site (same host as the app, another port) cannot call the mac API or the vps API', seen.length === 2 && seen.every((e) => e.status === 403 && !e.acao),
      `page sees ${JSON.stringify(tries)}; ${seen.map((e) => `${e.host} ${e.status} cookie=${e.cookie} ACAO=${e.acao ?? 'none'}`).join('; ')}`);
    // A select on mac's listener that names the vps app is refused: vps is not an app origin of a device on mac.
    const r = await env.curl(`${mac.previewOrigin}/__termivoa/select?session=${vpsDev.id}&port=1`, { headers: { Cookie: await jarOf(mac), 'Sec-Fetch-Site': 'same-site', Referer: `${vps.origin}/` } });
    check('PV-home-refuse', 'mac\'s listener refuses a select with the vps app as Referer', r.status === 403, `${r.status} ${r.body.trim()}`);
  });

  await step('PV-off', 'A computer that does not share previews: neutral text, no empty frame', async () => {
    await vps.stop();
    await vps.start({ preview: false });
    try {
      await vpsBack();
      await devSession(vps, 'vps-off');
      const mark = env.logMark();
      await openPreview('vps-off');
      await app.waitText("Can't open the preview on vps", 15000);
      const text = await app.eval(`document.querySelector('.preview-screen [role=alert]').innerText`);
      await shot('pv-off');
      const tried = env.logSince(mark).filter((e) => e.host === `${vps.host}:8443`);
      check('PV-off', 'With no preview listener on vps the app shows "Can\'t open the preview on vps…" and no frame',
        !(await app.eval(`!!document.querySelector('.preview-frame')`)) && tried.every((e) => e.status === 502), `${text}; front: ${tried.map((e) => `${e.method} ${e.status} ${e.note}`).join(', ')}`);
    } finally {
      await vps.stop();
      await vps.start();
    }
  });

  await step('PV-revoke', 'pterm revoke --via on vps stops an open preview and the app removes the frame', async () => {
    await vpsBack();
    const session = await devSession(vps, 'vps-dev2');
    await openPreview('vps-dev2');
    await frameReady(vps);
    const jar = await jarOf(vps);
    // A socket of the dev site held open outside the app, so only the computer can close it.
    const held = run('curl', ['-sS', '-k', '--suppress-connect-headers', '--proxy', `http://${env.proxy}`, '--http1.1', '-N', '--max-time', '40', '-o', '/dev/null', '-w', '%{http_code}',
      '-H', 'Connection: Upgrade', '-H', 'Upgrade: websocket', '-H', 'Sec-WebSocket-Version: 13', '-H', 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==',
      '-H', `Origin: ${vps.previewOrigin}`, '-H', `Cookie: ${jar}`, `${vps.previewOrigin}/ws`]);
    await sleep(1500);
    const revoke = interactive(vps, ['revoke', '--via', mac.origin]);
    await revoke.wait(/\[y\/N\]/);
    revoke.end('y\n');
    await revoke.done;
    const started = Date.now();
    const socket = await held;
    const socketMs = Date.now() - started;
    await until(() => app.eval(`!document.querySelector('.preview-frame')`), { what: 'the frame removed', timeout: 20000 });
    const frameMs = Date.now() - started;
    const alerts = () => app.eval(`[...document.querySelectorAll('.preview-screen [role=alert]')].map((e) => e.innerText.trim()).join(' | ')`);
    await until(async () => (await alerts()).includes('pair again'), { what: 'pair again text', timeout: 20000 }).catch(() => undefined);
    const text = await alerts();
    await shot('pv-revoked');
    time('revoke --via → open preview socket closed by vps', socketMs);
    time('revoke --via → the app removes the preview frame', frameMs);
    const after = await env.curl(`${vps.previewOrigin}/__termivoa/select?session=${session.id}&port=${devPorts['vps-dev2']}`, { headers: { Cookie: jar, 'Sec-Fetch-Site': 'same-site', Referer: `${mac.origin}/` } });
    const root = await env.curl(`${vps.previewOrigin}/`, { headers: { Cookie: jar, 'Sec-Fetch-Site': 'same-origin' } });
    check('PV-revoke-socket', 'vps closes the open preview socket within the recheck time (5 s) after revoke --via', socket.stdout === '101' && socket.code !== 28 && socketMs < 9000,
      `handshake ${socket.stdout}; closed by the computer ${socketMs} ms after the revoke (curl exit ${socket.code})`);
    check('PV-revoke-app', 'The app removes the frame and shows "vps · pair again", and nothing else',  text === 'vps · pair again', `${frameMs} ms after the revoke; text: ${text}`);
    check('PV-revoke-after', 'After the revoke the old cookies get 401 on the select and on /', after.status === 401 && root.status === 401, `select ${after.status} ${after.body.trim()}; / ${root.status}`);
  });

  await step('PV-readd', 'Add vps again after the revoke', async () => {
    const r = await addByPaste('pv-readd');
    check('PV-readd', 'Adding vps again after revoke --via works', r.outcome === 'added', `outcome ${r.outcome}`);
    if (r.outcome !== 'added') await confirmByApi();
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

  // ---- B3-push: notifications from a peer ----
  // Headless Chrome reaches no push service here, so the app cannot subscribe.
  // The harness calls vps's push routes as the app does, makes the peer
  // registration the app makes, and delivers the push with CDP.
  await step('B3-push', 'Push from vps: push routes over CORS, scoped registration, notification, link', async () => {
    const keys = await tool.eval(`(async () => {
      const b64 = (b) => btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\\+/g, '-').replace(/\\//g, '_').replace(/=+$/, '');
      const pair = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
      return { p256dh: b64(await crypto.subtle.exportKey('raw', pair.publicKey)), auth: b64(crypto.getRandomValues(new Uint8Array(16))) }; })()`);
    const endpoint = `https://fcm.googleapis.com/fcm/send/e2e-${crypto.randomUUID()}`;
    const mark = env.logMark();
    const config = await api(tool, `${vps.origin}/api/v1/push`);
    const subscribed = await api(tool, `${vps.origin}/api/v1/push/subscribe`, 'POST', { endpoint, keys });
    const unsubscribed = await api(tool, `${vps.origin}/api/v1/push/unsubscribe`, 'POST', { endpoint });
    const log = env.logSince(mark).filter((e) => e.host === vps.host && e.path?.startsWith('/api/v1/push'));
    check('B3-push-cors', 'vps answers the push routes for the mac app (bound origin, credentialed CORS)',
      config.status === 200 && /^B[\w-]{86}$/.test(config.json?.publicKey ?? '') && subscribed.status === 204 && unsubscribed.status === 204,
      `key ${config.status}, subscribe ${subscribed.status ?? subscribed.error}, unsubscribe ${unsubscribed.status ?? unsubscribed.error}; ${log.map((e) => `${e.method} ${e.path} ${e.status} ACAO=${e.acao ?? 'none'}`).join(', ')}`);

    const session = (await api(tool, `${vps.origin}/api/v1/sessions`)).json.sessions.find((s) => s.title === 'vps-1');
    await browser.send('Browser.grantPermissions', { origin: mac.origin, permissions: ['notifications'] });
    const ids = new Map();
    browser.listeners.push((m) => {
      if (m.method === 'ServiceWorker.workerRegistrationUpdated') for (const r of m.params.registrations) ids.set(r.scopeURL, r.registrationId);
    });
    await app.send('ServiceWorker.enable');
    const scope = `/push/${vps.host}/`;
    await openSessions();
    await app.eval(`navigator.serviceWorker.register('/sw.js', { scope: ${JSON.stringify(scope)} }).then((r) => new Promise((done) => {
      const w = r.installing ?? r.waiting;
      if (r.active || !w) done(true); else w.addEventListener('statechange', () => w.state === 'activated' && done(true)); }))`);
    const registrationId = await until(() => ids.get(`${mac.origin}${scope}`), { what: 'peer registration id' });
    // The payload names another host: the worker must use its scope's host.
    await app.send('ServiceWorker.deliverPushMessage', { origin: mac.origin, registrationId, data: JSON.stringify({
      title: 'vps-1 is waiting for you', body: 'demo · tap to answer', sessionId: session.id, kind: 'waiting', host: 'evil.tailx.ts.net', url: 'https://evil.tailx.ts.net/',
    }) });
    const shown = await until(() => app.eval(`navigator.serviceWorker.getRegistrations()
      .then((all) => all.find((r) => r.scope.endsWith(${JSON.stringify(scope)})).getNotifications())
      .then((n) => n.map((x) => ({ title: x.title, body: x.body, url: x.data?.url }))[0])`), { what: 'notification shown' });
    const want = `/c/${vps.host}/sessions/${session.id}`;
    check('B3-push-show', 'A push to the vps registration shows a notification that names vps and links to the vps session',
      shown.title === 'vps · vps-1 is waiting for you' && shown.body === 'vps · demo · tap to answer' && shown.url === want, JSON.stringify(shown));
    // No page loads inside the peer scope: the app there would get the peer's registration.
    const inside = await env.curl(`${mac.origin}${scope}x`);
    check('B3-push-scope', 'mac answers 404 under the push scope (no app page there)', inside.status === 404, `GET ${scope}x ${inside.status}`);
    // A tap with the app open on another screen. CDP has no notification
    // click, so the harness dispatches notificationclick inside the peer
    // worker with the shown notification. That event is not a user action:
    // its waitUntil is replaced (the real one throws) and the browser refuses
    // focus() for it, but the worker's message to the page, and the page's
    // routing, run as after a real tap.
    // One app window, as on a phone: the harness's second mac page leaves for the click.
    await tool.go('about:blank');
    await app.eval(`window.__sameLoad = true`);
    const workers = [];
    for (const target of (await browser.send('Target.getTargets')).targetInfos.filter((t) => t.type === 'service_worker')) {
      const { sessionId } = await browser.send('Target.attachToTarget', { targetId: target.targetId, flatten: true });
      const r = await browser.send('Runtime.evaluate', { awaitPromise: true, returnByValue: true, expression: `self.registration.scope.endsWith(${JSON.stringify(scope)})
        && self.registration.getNotifications().then(([n]) => { const e = new NotificationEvent('notificationclick', { notification: n });
          let done = Promise.resolve('no waitUntil');
          e.waitUntil = (work) => { done = work.then(() => 'done', (error) => String(error)); }; // the real waitUntil throws on an event the browser did not make
          self.dispatchEvent(e);
          return done; })` }, sessionId);
      if (r.result?.value !== false) workers.push(String(r.result?.value ?? r.exceptionDetails?.exception?.description));
      await browser.send('Target.detachFromTarget', { sessionId }).catch(() => undefined);
    }
    await until(() => app.eval(`location.pathname === ${JSON.stringify(want)}`), { what: `the open app routes to the vps session (peer worker: ${workers.join('; ') || 'not found'})` });
    await until(async () => /[%$#]\s*$/m.test(await terminalText(app)), { what: 'shell prompt of the vps session' });
    check('B3-push-open', 'A notification click in the vps registration moves the open mac app to the vps session, with no page load',
      await app.eval(`window.__sameLoad === true`) && (await terminalText(app)).includes(`${tokens.echo}-42`),
      `${await app.eval('location.pathname')}; same page load; terminal shows the echo typed in B3-echo; worker: ${workers.join('; ')} (the click is synthetic, so focus() is refused)`);
    await tool.go(`${mac.origin}/`);
    // With the app closed, the tap opens this address in a new window.
    await app.go(`${mac.origin}${shown.url}`);
    await until(async () => /[%$#]\s*$/m.test(await terminalText(app)), { what: 'shell prompt after a cold open' });
    check('B3-push-cold', 'The notification link opens the vps session on a fresh load', (await terminalText(app)).includes(`${tokens.echo}-42`), await app.eval('location.pathname'));
    // Push is off in this browser (no home subscription), so the app's own cleanup removes the peer registration.
    const left = await until(async () => {
      const scopes = await app.eval(`navigator.serviceWorker.getRegistrations().then((all) => all.map((r) => new URL(r.scope).pathname))`);
      return !scopes.includes(scope) && scopes;
    }, { what: 'peer registration removed', timeout: 15000 });
    check('B3-push-off', 'With notifications off, the app unregisters the peer registration on load', true, `registrations ${JSON.stringify(left)}`);
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
    await until(async () => (await terminalText(app)).replace(/\n/g, '').includes('Waiting for the phone'), { what: 'link in terminal' });
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
      // The same device on the preview listener, as the mac app's frame would ask (any session: the device is refused first).
      const jar = (await other.cookies()).filter((c) => c.domain === vps.host).map((c) => `${c.name}=${c.value}`).join('; ');
      const select = `${vps.previewOrigin}/__termivoa/select?session=${crypto.randomUUID()}&port=3000`;
      const framed = await env.curl(select, { headers: { Cookie: jar, 'Sec-Fetch-Site': 'same-site', Referer: `${mac.origin}/` } });
      const ownApp = await env.curl(select, { headers: { Cookie: jar, 'Sec-Fetch-Site': 'same-origin', Referer: `${vps.origin}/` } });
      check('PV-direct', 'A device paired directly with vps: a select from the mac app is refused (403); from the vps app it passes the device check',
        framed.status === 403 && framed.body.includes('This app may not use this computer') && !framed.headers['set-cookie'] && ownApp.status === 404,
        `Referer mac: ${framed.status} ${framed.body.trim()}; Referer vps: ${ownApp.status} ${ownApp.body.trim()}`);
      check('B7-direct', 'A device paired directly with vps cannot be driven from the mac app',
        own.status === 200 && fromMac.status !== 200 && sessions.status !== 200 && ws !== 'open',
        `own page ${own.status}; from mac origin: settings ${fromMac.status ?? fromMac.error}, sessions ${sessions.status ?? sessions.error}, WebSocket ${ws}; ${log.map((e) => `${e.method} ${e.path} ${e.status}`).join(', ')}`);
    } finally {
      await other.close();
    }
  });

  await step('B7-node', 'Changing mac\'s node ID on vps revokes the bound devices', async () => {
    // A preview socket of the bound device, held open across the change.
    let held;
    try {
      await openSessions();
      await devSession(vps, 'vps-node');
      await openPreview('vps-node');
      await frameReady(vps); // the selection sets the preview cookie the held socket needs
      held = run('curl', ['-sS', '-k', '--suppress-connect-headers', '--proxy', `http://${env.proxy}`, '--http1.1', '-N', '--max-time', '150', '-o', '/dev/null', '-w', '%{http_code}',
        '-H', 'Connection: Upgrade', '-H', 'Upgrade: websocket', '-H', 'Sec-WebSocket-Version: 13', '-H', 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==',
        '-H', `Origin: ${vps.previewOrigin}`, '-H', `Cookie: ${await jarOf(vps)}`, `${vps.previewOrigin}/ws`], { timeout: 160000 });
      await sleep(1000);
    } catch (error) {
      check('PV-node', 'An open preview socket closes when mac\'s node ID changes on vps', false, `setup: ${error.message.split('\n')[0]}`);
    }
    const before = (await vps.cli(['devices'])).stdout;
    const changed = Date.now();
    vps.writeStatus({ ...NODES, mac: { ...NODES.mac, ID: 'nMacE2E2CHANGED' } });
    if (held) {
      void held.then((socket) => check('PV-node', 'An open preview socket closes when mac\'s node ID changes on vps (next Tailscale snapshot plus one recheck)',
        socket.stdout === '101' && socket.code !== 28, `handshake ${socket.stdout}; closed by the computer ${Math.round((Date.now() - changed) / 1000)} s after the change (curl exit ${socket.code})`));
    }
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
    await held;
    await sleep(50);
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
