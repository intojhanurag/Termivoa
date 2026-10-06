// Phase B's GitHub checks (G*; docs/github-integration.md, sections 2, 6, 8
// and 9): the fake GitHub (scripts/fakegithub) is the web, API and git host
// of both stacks (PTERM_E2E_GITHUB, e2e builds only). Nothing here reaches
// github.com, and no git command here reads a real credential: each runs
// with a demo or temp HOME and GIT_CONFIG_NOSYSTEM=1.
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { Browser, api, click, pairDirect, run, sandboxed, setInput, sleep, terminalText, typeLine, until } from './lib.mjs';

const APP = 'termivoa-e2e';
const DEVICE_URL = 'https://github.com/login/device';
const INSTALL_URL = `https://github.com/apps/${APP}/installations/new`;
const NOT_CONNECTED = 'Termivoa is not connected to GitHub on this computer.';
const squash = (x) => x.replace(/\s+/g, ' ').trim();

/** Runs the G* steps. ctx holds the harness parts of phase-b.mjs. */
export async function githubSteps(ctx) {
  const { env, mac, vps, app, tool, step, check, shot, time, inApp } = ctx;
  const gh = (path, method = 'GET') => env.gitHub(path, { method });
  const state = async (stack) => (await api(tool, `${stack.origin}/api/v1/github`)).json;
  const action = (stack, body) => api(tool, `${stack.origin}/api/v1/github`, 'POST', body);
  const repoUrl = (full) => `${env.github}/${full}.git`;
  const gitAuths = async () => (await gh('/_test/gitauth')).json ?? [];
  const vpsStates = [];
  const watchVps = async (when) => vpsStates.push(`${when}: ${(await state(vps))?.state} / settings ${(await api(tool, `${vps.origin}/api/v1/settings`)).json?.github?.state}`);
  // Git as the harness runs it: never the owner's HOME, system or global config.
  const scratch = mkdtempSync(join(env.root, 'gh-git-'));
  const gitEnv = { PATH: '/usr/bin:/bin', HOME: scratch, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_TERMINAL_PROMPT: '0', LC_ALL: 'C' };
  const git = (args, options = {}) => run('/usr/bin/git', args, { env: gitEnv, ...options });
  const made = []; // folders this group made in mac's home
  const sessions = []; // sessions this group made on mac
  let harnessPair; // the pair the harness got itself (for the shop-api clone): not the broker's

  // ---- page helpers (the GitHub row is a .computer-card like "herdr on mac") ----
  const LABEL = `const text = (e) => (e ? e.innerText.replace(/\\s+/g, ' ').trim() : '');
    const label = (b) => (b.querySelector('.btn-swap > span:not(.off)') ?? b).innerText.replace(/\\s+/g, ' ').trim() || b.getAttribute('aria-label') || '';
    const seen = (e) => e.offsetParent !== null || e.getClientRects().length > 0;`;
  /** The Integrations row "GitHub on <name>"; innerText follows text-transform, so state is lower case. */
  const card = (name) => app.eval(`(() => { ${LABEL}
    const li = [...document.querySelectorAll('.computer-card')].find((e) => text(e.querySelector('.row-title')) === ${JSON.stringify(`GitHub on ${name}`)});
    if (!li) return null;
    const head = li.querySelector('.computer-head');
    return { state: (text(head.querySelector('.computer-state')) || text(head.querySelector('.row-meta'))).toLowerCase(), meta: text(head.querySelector('.row-meta')), text: text(head), buttons: [...li.querySelectorAll(':scope > .computer-head button, :scope > .gh-links button, :scope > [role=alertdialog] button')].filter(seen).map(label) }; })()`);
  const cardTap = async (name, button) => {
    const ok = await app.eval(`(() => { ${LABEL}
      const li = [...document.querySelectorAll('.computer-card')].find((e) => text(e.querySelector('.row-title')) === ${JSON.stringify(`GitHub on ${name}`)});
      const b = [...(li?.querySelectorAll('button') ?? [])].find((e) => label(e) === ${JSON.stringify(button)}); if (!b || b.disabled) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`no enabled button "${button}" on GitHub on ${name}`);
    await sleep(300);
  };
  /** The open sheet: its text, links, buttons, the visible list rows; null when no sheet is open. */
  const sheet = () => app.eval(`(() => { ${LABEL}
    const d = [...document.querySelectorAll('[role=dialog], [role=alertdialog]')].filter(seen).at(-1); if (!d) return null;
    return { text: text(d), links: [...d.querySelectorAll('a[href]')].map((a) => ({ href: a.getAttribute('href'), text: text(a), target: a.target, rel: a.rel })),
      buttons: [...d.querySelectorAll('button')].filter(seen).map((b) => ({ text: label(b), disabled: b.disabled })),
      progress: d.querySelector('progress, [role=progressbar]') !== null,
      rows: [...d.querySelectorAll('li')].filter(seen).map((li) => ({ text: text(li),
        tappable: [...li.querySelectorAll('button')].some((b) => !b.disabled) || (li.matches('[role=button]') && li.getAttribute('aria-disabled') !== 'true') })) }; })()`);
  const sheetTap = async (name) => {
    const ok = await app.eval(`(() => { ${LABEL}
      const d = [...document.querySelectorAll('[role=dialog], [role=alertdialog]')].filter(seen).at(-1);
      const b = [...(d?.querySelectorAll('button, a') ?? [])].filter(seen).find((e) => label(e) === ${JSON.stringify(name)}); if (!b || b.disabled) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`no enabled "${name}" in the sheet`);
    await sleep(300);
  };
  /** Taps the list row of repo (its own button, or the row). */
  const rowTap = async (repo) => {
    const ok = await app.eval(`(() => { ${LABEL}
      const d = [...document.querySelectorAll('[role=dialog]')].filter(seen).at(-1);
      const li = [...(d?.querySelectorAll('li') ?? [])].filter(seen).find((e) => text(e).includes(${JSON.stringify(repo)}));
      const b = li && ([...li.querySelectorAll('button')].find((e) => !e.disabled) ?? (li.matches('[role=button]') ? li : null)); if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`no tappable row ${repo}`);
    await sleep(300);
  };
  const rowOf = async (repo) => (await sheet())?.rows.find((r) => r.text.includes(repo));
  const closeSheets = async () => {
    for (let i = 0; i < 4 && await app.eval(`document.querySelector('[role=dialog]') !== null`); i += 1) {
      await sheetTap('Done').catch(() => sheetTap('Close')).catch(() => sheetTap('Later')).catch(() => sheetTap('Cancel')).catch(() => app.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 }));
      await sleep(300);
    }
  };
  const integrations = async () => {
    await closeSheets();
    await inApp('/settings');
    await until(() => card('mac'), { what: 'the GitHub on mac row', timeout: 20000 });
    await app.eval(`document.querySelector('#integrations-heading')?.scrollIntoView()`);
  };
  /** Integrations → Set up a repository → the list (a kept set-up result opens first: Close goes back to the list). */
  const openList = async () => {
    await integrations();
    await cardTap('mac', 'Set up a repository');
    return until(async () => {
      const s = await sheet();
      if (s?.rows.some((r) => r.text.includes('pdf-editor'))) return s;
      if (s && !/Set up a repository/.test(s.text) && s.buttons.some((b) => b.text === 'Close')) await sheetTap('Close');
      return false;
    }, { what: 'the repository list', timeout: 30000, every: 500 });
  };
  /** The code the open sheet shows, as one string. */
  const shownCode = async () => {
    const s = await sheet();
    return s && (/\b([A-Z0-9]{4}-[A-Z0-9]{4})\b/.exec(s.text)?.[1] ?? /([A-Z0-9]{4}-[A-Z0-9]{4})/.exec(s.text.replace(/\s/g, ''))?.[1]);
  };
  /** Waits for the shell prompt in Live and takes control (a phone opens a session watching). */
  const liveReady = async (re = /[%$#]\s*$/m) => {
    await until(async () => re.test(await terminalText(app)), { what: `prompt ${re}`, timeout: 30000 });
    if ((await app.text()).includes('Take control')) {
      await click(app, 'Take control');
      await until(async () => !(await app.text()).includes('Take control'), { what: 'control taken' });
    }
  };
  const projects = async () => (await api(tool, `${mac.origin}/api/v1/settings`)).json?.projects ?? [];
  /** The fake's live pairs that the broker holds (the harness's own pair left out). */
  const brokerPairs = async () => ((await gh('/_test/tokens')).json ?? []).filter((p) => p.n !== harnessPair?.n);
  /** pids of mac's broker and everything under it (its git and the helper). Only these are ever read with ps. */
  const brokerTree = async () => {
    const roots = (await run('/usr/bin/pgrep', ['-f', `runtime-dir ${mac.runtimeDir}`])).stdout.split('\n').filter(Boolean);
    const table = (await run('/bin/ps', ['-A', '-o', 'pid=,ppid='])).stdout.trim().split('\n').map((l) => l.trim().split(/\s+/));
    const tree = new Set(roots);
    for (let grew = true; grew;) { grew = false; for (const [pid, ppid] of table) if (tree.has(ppid) && !tree.has(pid)) { tree.add(pid); grew = true; } }
    return [...tree];
  };

  try {
    // ---- before the list is ever read (it is cached for 60 s): notes is a plain folder, shop-api a real clone ----
    await step('G0', 'Fixtures on mac: ~/notes a plain folder, ~/shop-api a clone of the fake\'s shop-api', async () => {
      await watchVps('start');
      mkdirSync(join(mac.home, 'notes'));
      made.push('notes');
      // The harness's own login at the fake (its own pair), for the shop-api clone only.
      const device = await (await fetch(`${env.github}/login/device/code`, { method: 'POST', headers: { Accept: 'application/json' }, body: new URLSearchParams({ client_id: 'Iv1.e2e' }) })).json();
      await gh(`/_test/authorize?code=${device.user_code}`, 'POST');
      const tokens = await (await fetch(`${env.github}/login/oauth/access_token`, { method: 'POST', headers: { Accept: 'application/json' },
        body: new URLSearchParams({ client_id: 'Iv1.e2e', device_code: device.device_code, grant_type: 'urn:ietf:params:oauth:grant-type:device_code' }) })).json();
      harnessPair = (await gh('/_test/tokens')).json.find((p) => p.access === tokens.access_token);
      const authed = new URL(repoUrl('octo-demo/shop-api'));
      authed.username = 'octo-demo';
      authed.password = tokens.access_token;
      const cloned = await git(['clone', '-q', authed.href, join(mac.home, 'shop-api')]);
      if (cloned.code !== 0) throw new Error(`clone shop-api: ${cloned.stderr.replaceAll(tokens.access_token, '<token>')}`);
      made.push('shop-api');
      await git(['-C', join(mac.home, 'shop-api'), 'remote', 'set-url', 'origin', repoUrl('octo-demo/shop-api')]);
      const origin = (await git(['-C', join(mac.home, 'shop-api'), 'remote', 'get-url', 'origin'])).stdout.trim();
      check('G0', 'Fixtures on mac: ~/notes is a plain folder; ~/shop-api is a clone of the fake\'s shop-api with a clean origin (made with the harness\'s own login at the fake)',
        statSync(join(mac.home, 'notes')).isDirectory() && origin === repoUrl('octo-demo/shop-api') && harnessPair?.n > 0, `origin ${origin}; harness pair ${harnessPair?.n}`);
    });

    await step('G1', 'Settings → Integrations shows "GitHub on mac" and "GitHub on vps", not connected, with Connect (no herdr on either)', async () => {
      await integrations();
      const rows = { mac: await card('mac'), vps: await until(() => card('vps'), { what: 'the GitHub on vps row' }) };
      await shot('g1-integrations');
      const providers = [];
      for (const stack of [mac, vps]) providers.push((await api(tool, `${stack.origin}/api/v1/settings`)).json?.providers?.[0]?.state);
      const api0 = { mac: await state(mac), vps: await state(vps) };
      check('G1', 'Both rows read "not connected" with Connect; neither computer has herdr (the rows show without it); GET /api/v1/github says notConnected with app termivoa-e2e',
        Object.values(rows).every((r) => r.state === 'not connected' && r.buttons.includes('Connect')) && providers.every((p) => p !== 'ready')
          && Object.values(api0).every((s) => s?.state === 'notConnected' && s.app === APP && !s.connect),
        `mac ${JSON.stringify(rows.mac)}; vps ${JSON.stringify(rows.vps)}; herdr ${JSON.stringify(providers)}; api ${JSON.stringify(api0)}`);
    });

    await step('G2', 'Connect on mac: the sheet shows the code, the link to github.com/login/device and "Waiting for GitHub…"; a second phone gets no code', async () => {
      await integrations();
      await cardTap('mac', 'Connect');
      const code = await until(() => shownCode(), { what: 'a code in the sheet', timeout: 20000 });
      await until(async () => (await sheet()).text.includes('Waiting for GitHub…'), { what: 'Waiting for GitHub…', timeout: 10000 });
      const s = await sheet();
      await shot('g2-code');
      const own = await state(mac);
      // A second phone paired with mac: its own browser profile, its own device credential.
      const second = await Browser.launch(env, { profile: 'chrome-github' });
      let other;
      let otherText = '';
      try {
        const page = await second.page();
        await page.go(`${mac.origin}/`);
        await pairDirect(page, mac, 'GitHub phone');
        await page.waitText('Sessions', 30000);
        other = (await api(page, `${mac.origin}/api/v1/github`)).json;
        await page.eval(`history.pushState(null, '', '/settings'); dispatchEvent(new PopStateEvent('popstate'))`);
        otherText = await until(async () => { const t = await page.text(); return t.includes('GitHub on mac') && t; }, { what: 'the second phone\'s GitHub row', timeout: 20000 }).catch(() => '');
      } finally {
        await second.close();
      }
      const link = s.links.find((l) => l.href === DEVICE_URL);
      check('G2', 'The sheet shows a code like ABCD-1234 (the one GitHub gave), the link https://github.com/login/device (new tab, noopener) and "Waiting for GitHub…"; this phone\'s GET has the code; a second phone\'s GET says connecting with no code, and its row says another phone connects',
        /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(code) && own?.state === 'connecting' && own.connect?.userCode === code && !!link && link.target === '_blank' && link.rel.includes('noopener')
          && other?.state === 'connecting' && other.connect === undefined && otherText.includes('Connecting from another phone'),
        `code ${code}; links ${JSON.stringify(s.links)}; this phone ${JSON.stringify(own)}; second phone ${JSON.stringify(other)}, "${squash(otherText).match(/GitHub on mac.{0,80}/)?.[0] ?? ''}"`);
    });

    await step('G3', 'Authorize with no installation → "Choose" with the install link; one installation → the sheet moves on by itself, the row says CONNECTED with octo-demo', async () => {
      const code = (await state(mac))?.connect?.userCode ?? await shownCode();
      const authorized = await gh(`/_test/authorize?code=${code}`, 'POST');
      await watchVps('after mac authorized');
      const choose = await until(async () => { const s = await sheet(); return s?.links.some((l) => l.href === INSTALL_URL) && s; }, { what: 'the install link', timeout: 20000 });
      const needs = await state(mac);
      await shot('g3-choose');
      await gh('/_test/install?count=1', 'POST');
      const installed = Date.now();
      const left = await until(async () => { const s = await sheet(); return !/step 2 of 2/i.test(s?.text ?? '') && (s ?? { text: '(closed)' }); }, { what: 'the sheet leaves the choose step', timeout: 20000, every: 250 });
      const ms = Date.now() - installed;
      time('install on GitHub → the sheet moves on (no tap)', ms);
      await closeSheets();
      const row = await until(async () => { const c = await card('mac'); return c?.state === 'connected' && c; }, { what: 'CONNECTED', timeout: 10000 });
      await shot('g3-connected');
      const ready = await state(mac);
      check('G3', 'needsInstall: the sheet "Choose repositories" says Signed in as octo-demo and links to https://github.com/apps/termivoa-e2e/installations/new; after the install the sheet moves on in under 10 s with no tap; the row reads CONNECTED, octo-demo, Set up a repository and Disconnect',
        authorized.status === 200 && needs?.state === 'needsInstall' && choose.text.includes('Choose repositories') && choose.text.includes('Signed in as octo-demo') && ms < 10000
          && ready?.state === 'ready' && ready.login === 'octo-demo' && row.meta === 'octo-demo' && row.buttons.includes('Set up a repository') && row.buttons.includes('Disconnect'),
        `choose "${choose.text.slice(0, 160)}"; state ${needs?.state} → ${ready?.state} ${ready?.login}; moved on after ${ms} ms to "${left.text.slice(0, 80)}"; row ${JSON.stringify(row)}`);
    });

    await step('G4', 'Set up a repository: .ssh-keys is not offered, the large one shows its size, two installations and 120 more are all listed, "pdf" leaves one row', async () => {
      await gh('/_test/install?count=2&extra=120', 'POST');
      await integrations();
      await cardTap('mac', 'Set up a repository');
      await until(async () => { const s = await sheet(); return s?.rows.some((r) => r.text.includes('pdf-editor')) && s; }, { what: 'the repository list', timeout: 20000 });
      // The broker caches the list for 60 s (design 6.3), and it read it when the sheet moved on in G3.
      const repos = await until(async () => { const r = (await api(tool, `${mac.origin}/api/v1/github/repos`)).json?.repos; return r?.length === 125 && r; },
        { what: '125 repositories after the cache', timeout: 75000, every: 3000 }).catch(async () => (await api(tool, `${mac.origin}/api/v1/github/repos`)).json?.repos ?? []);
      await closeSheets();
      await cardTap('mac', 'Set up a repository');
      const list = await until(async () => { const s = await sheet(); return s?.rows.some((r) => r.text.includes('pdf-editor')) && s; }, { what: 'the repository list again', timeout: 20000 });
      const big = list.rows.find((r) => r.text.includes('game-assets'));
      await shot('g4-list');
      await setInput(app, '[role=dialog] input', 'pdf');
      const one = await until(async () => { const s = await sheet(); return s.rows.length === 1 && s; }, { what: 'one row for pdf', timeout: 5000 }).catch(async () => sheet());
      await setInput(app, '[role=dialog] input', '');
      check('G4', 'The list (and the API) leave out octo-demo/.ssh-keys; game-assets (1.3 GB) shows a size in GB; the API has all 125 (two installations, paged); the search "pdf" leaves one row, pdf-editor',
        !list.text.includes('.ssh-keys') && !repos.some((r) => r.repo.includes('/.')) && repos.length === 125 && repos.some((r) => r.repo === 'acme-labs/billing-service')
          && /\d(\.\d+)?\s?GB/i.test(big?.text ?? '') && one.rows.length === 1 && one.rows[0].text.includes('pdf-editor'),
        `rows ${list.rows.length}; api ${repos.length} repos, truncated ${JSON.stringify((await api(tool, `${mac.origin}/api/v1/github/repos`)).json?.truncated)}; big "${big?.text}"; after "pdf": ${JSON.stringify(one.rows.map((r) => r.text))}`);
    });

    await step('G5', 'notes is a plain folder: "name taken", not tappable; shop-api is a real clone: "on mac"', async () => {
      if (!(await sheet())?.rows.length) await openList();
      const notes = await until(() => rowOf('octo-demo/notes').then((r) => r ?? rowOf('notes')), { what: 'the notes row', timeout: 20000 });
      const shop = await rowOf('shop-api');
      const repos = (await api(tool, `${mac.origin}/api/v1/github/repos`)).json?.repos ?? [];
      const api5 = Object.fromEntries(['octo-demo/notes', 'octo-demo/shop-api'].map((n) => [n, repos.find((r) => r.repo === n)]));
      await app.eval(`[...document.querySelectorAll('[role=dialog] li')].find((e) => e.innerText.includes('notes'))?.scrollIntoView({ block: 'center' })`);
      await shot('g5-taken-here');
      check('G5', 'The notes row says "name taken" and cannot be tapped (taken in the API); the shop-api row says "on mac" (here in the API)',
        notes.text.toLowerCase().includes('name taken') && !notes.tappable && api5['octo-demo/notes']?.taken === true
          && shop?.text.toLowerCase().includes('on mac') && api5['octo-demo/shop-api']?.here === true,
        `notes ${JSON.stringify(notes)}; shop-api ${JSON.stringify(shop)}; api ${JSON.stringify(api5)}`);
    });

    await step('G6', 'Set up pdf-editor: progress, then "Ready on mac" with New session; the folder, its helper lines, the project, a token-auth clone, no token in ps', async () => {
      if (!(await rowOf('pdf-editor'))) await openList();
      await until(() => rowOf('pdf-editor'), { what: 'the pdf-editor row' });
      const authsBefore = (await gitAuths()).length;
      await gh('/_test/gitdelay?ms=2000', 'POST'); // a clone that lasts long enough to watch
      const started = Date.now();
      made.push('pdf-editor');
      await rowTap('pdf-editor');
      // A confirmation first, when the app asks for one.
      if (await app.eval(`document.querySelector('#gh-confirm') !== null`)) await sheetTap('Set up');
      // While it clones: the progress view, and the arguments of the broker's own processes (never others').
      // Their environment cannot be read here: macOS 26 ps prints none with -E or e, also for the user's own processes.
      const fake = (await gh('/_test/tokens')).json.flatMap((p) => [p.access, p.refresh]);
      let samples = 0;
      let gitSeen = 0;
      let leaked = 0;
      let progress = false;
      await until(async () => {
        const s = await sheet();
        progress ||= !!s?.progress || /Stop/.test(s?.buttons.map((b) => b.text).join() ?? '');
        const pids = await brokerTree();
        const ps = (await run('/bin/ps', ['-ww', '-o', 'command=', '-p', pids.join(',')])).stdout;
        samples += 1;
        gitSeen += ps.split('\n').filter((l) => /\bgit(-remote-https?)?\b.*(clone|remote-http|fetch-pack|index-pack)|github-credential/.test(l)).length > 0 ? 1 : 0;
        const live = (await gh('/_test/tokens')).json.flatMap((p) => [p.access, p.refresh]);
        leaked += [...new Set([...fake, ...live])].filter((t) => ps.includes(t)).length;
        return s?.text.includes('Ready on mac') && s;
      }, { what: 'Ready on mac', timeout: 90000, every: 300 }).catch(async (error) => {
        throw new Error(`${error.message}; sheet "${(await sheet())?.text.slice(0, 300)}"; setup ${JSON.stringify((await state(mac))?.setup)}; git auths ${JSON.stringify((await gitAuths()).slice(authsBefore).map((a) => [a.path, a.n, a.ok]))}`);
      });
      time('Set up → Ready on mac (git requests delayed 2 s each)', Date.now() - started);
      await gh('/_test/gitdelay?ms=0', 'POST');
      const ready = await sheet();
      await shot('g6-ready');
      const setup = (await state(mac))?.setup;
      const dir = join(mac.home, 'pdf-editor');
      const helpers = await git(['config', '--file', join(dir, '.git', 'config'), '--get-all', `credential.${repoUrl('octo-demo/pdf-editor')}.helper`]);
      const lines = helpers.stdout.split('\n').slice(0, -1);
      const origin = (await git(['config', '--file', join(dir, '.git', 'config'), '--get', 'remote.origin.url'])).stdout.trim();
      const temp = readdirSync(mac.home).filter((n) => n.startsWith('.termivoa-clone-'));
      const project = (await projects()).find((p) => JSON.stringify(p).includes('pdf-editor'));
      const ours = (await brokerPairs()).map((p) => p.n);
      const auths = (await gitAuths()).slice(authsBefore).filter((a) => a.path.startsWith('/octo-demo/pdf-editor.git/'));
      const good = auths.filter((a) => a.ok && ours.includes(a.n));
      check('G6', 'Progress shows while it clones; then "Ready on mac" with New session and the Git LFS line; ~/pdf-editor/.git exists; its config has the reset line, then "!… github-credential", for that address only; origin is the fake\'s URL; no .termivoa-clone-* left; pdf-editor is a project; the fake saw the clone authenticate with the broker\'s access token; no token in the arguments of the broker, git or the helper (macOS shows no process environment to ps)',
        progress && ready.buttons.some((b) => b.text === 'New session') && ready.text.includes('Large files were not downloaded.')
          && statSync(join(dir, '.git')).isDirectory() && lines.length === 2 && lines[0] === '' && /^!.*\bgithub-credential\b/.test(lines[1])
          && origin === repoUrl('octo-demo/pdf-editor') && temp.length === 0 && !!project && setup?.phase === 'finished' && setup.lfs === true
          && good.length > 0 && auths.every((a) => a.ok) && gitSeen > 0 && leaked === 0,
        `progress ${progress}; ready "${ready.text.slice(0, 160)}" ${JSON.stringify(ready.buttons)}; setup ${JSON.stringify(setup)}; helper lines ${JSON.stringify(lines.map((l) => l.replace(env.root, '<root>')))}; origin ${origin}; temp folders ${temp.length}; project ${!!project}; git auths ${auths.map((a) => `${a.user}:pair${a.n}:${a.ok ? 'ok' : 'refused'}`).join(',')}; ps: ${samples} samples, git or helper seen in ${gitSeen}, token hits ${leaked}`);
    });

    let liveSession;
    await step('G7', 'New session opens Live in pdf-editor (the prompt shows pdf-editor)', async () => {
      const before = (await api(tool, `${mac.origin}/api/v1/sessions`)).json.sessions.map((s) => s.id);
      if (!(await sheet())?.text.includes('Ready on mac')) throw new Error('the Ready sheet is not open');
      await sheetTap('New session');
      await liveReady(/pdf-editor %\s*$/m);
      const path = await app.eval('location.pathname');
      liveSession = (await api(tool, `${mac.origin}/api/v1/sessions`)).json.sessions.find((s) => !before.includes(s.id));
      if (liveSession) sessions.push(liveSession);
      await shot('g7-live');
      check('G7', 'One new session on mac, open in Live, whose prompt is "pdf-editor %"',
        !!liveSession && path === `/sessions/${liveSession.id}` && /pdf-editor %/.test(await terminalText(app)),
        `Live ${path}; session ${liveSession?.id}; screen ${JSON.stringify((await terminalText(app)).trim().slice(-80))}`);
    });

    await step('G8', 'shop-api "on mac": a tap answers "Already on mac." with New session, and nothing is cloned', async () => {
      const authsBefore = (await gitAuths()).length;
      await openList();
      await until(() => rowOf('shop-api'), { what: 'the shop-api row', timeout: 20000 });
      await rowTap('shop-api');
      if (await app.eval(`document.querySelector('#gh-confirm') !== null`)) await sheetTap('Set up');
      const s = await until(async () => { const x = await sheet(); return x?.text.includes('Already on mac.') && x; }, { what: 'Already on mac.', timeout: 20000 });
      await shot('g8-already');
      const setup = (await state(mac))?.setup;
      const project = (await projects()).find((p) => JSON.stringify(p).includes('shop-api'));
      const auths = (await gitAuths()).slice(authsBefore);
      const helper = (await git(['config', '--file', join(mac.home, 'shop-api', '.git', 'config'), '--get-all', `credential.${repoUrl('octo-demo/shop-api')}.helper`])).stdout;
      check('G8', '"Already on mac." with New session; setup finished with already; shop-api is a project; no git request reached the fake; the folder\'s config is not changed (no helper line)',
        s.buttons.some((b) => b.text === 'New session') && setup?.phase === 'finished' && setup.already === true && !!project && auths.length === 0 && helper === '',
        `sheet "${s.text.slice(0, 120)}"; setup ${JSON.stringify(setup)}; project ${!!project}; git requests ${auths.length}; helper lines ${JSON.stringify(helper)}`);
      await closeSheets();
    });

    /** A session on mac in pdf-editor, open in Live: G7's, else a new one. */
    const pdfLive = async () => {
      if (!liveSession) {
        const settings = (await api(tool, `${mac.origin}/api/v1/settings`)).json;
        const project = settings.projects.find((p) => JSON.stringify(p).includes('pdf-editor'));
        if (!project) throw new Error('pdf-editor is not a project');
        const created = await api(tool, `${mac.origin}/api/v1/sessions`, 'POST', { operationId: crypto.randomUUID(), projectId: project.id, shellProfileId: settings.shellProfiles[0].id });
        liveSession = created.json.session ?? created.json;
        sessions.push(liveSession);
      }
      await closeSheets();
      await inApp(`/sessions/${liveSession.id}`);
      await liveReady(/pdf-editor %\s*$/m);
    };

    await step('G9', 'git pull in pdf-editor in a session on mac works through the helper', async () => {
      await pdfLive();
      const authsBefore = (await gitAuths()).length;
      // GIT_CONFIG_NOSYSTEM: the session's git never asks the owner's system helpers (Keychain) for anything.
      await typeLine(app, 'GIT_CONFIG_NOSYSTEM=1 git pull; echo "PULL-EXIT=$?"');
      const exit = await until(async () => /PULL-EXIT=(\d+)/.exec(squash(await terminalText(app)))?.[1], { what: 'PULL-EXIT', timeout: 30000 });
      const screen = await terminalText(app);
      const ours = (await brokerPairs()).map((p) => p.n);
      const auths = (await gitAuths()).slice(authsBefore);
      check('G9', 'git pull exits 0 ("Already up to date."); the fake recorded the pull authenticating with a live access token of the broker (the same pair or a refreshed one)',
        exit === '0' && auths.some((a) => a.ok && ours.includes(a.n)) && auths.every((a) => a.ok),
        `exit ${exit}; auths ${auths.map((a) => `${a.user}:pair${a.n}:${a.ok ? 'ok' : 'refused'}`).join(',')}; screen ${JSON.stringify(squash(screen).slice(-160))}`);
    });

    await step('G10', 'Disconnect: the row says not connected, both tokens are revoked at GitHub, git pull then fails with the helper\'s line', async () => {
      const pairs = await brokerPairs();
      const latest = pairs.at(-1);
      await integrations();
      await cardTap('mac', 'Disconnect');
      // The confirmation, when there is one.
      await sleep(300);
      if ((await sheet())?.buttons.some((b) => b.text === 'Disconnect' && !b.disabled)) await sheetTap('Disconnect');
      const row = await until(async () => { const c = await card('mac'); return c?.state === 'not connected' && c; }, { what: 'not connected', timeout: 20000 });
      await shot('g10-disconnected');
      const revoked = (await gh('/_test/revoked')).json ?? [];
      const pull = await run(...sandboxed('/usr/bin/git', ['-C', join(mac.home, 'pdf-editor'), 'pull']),
        { env: { ...mac.processEnv, GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0' }, timeout: 30000 });
      check('G10', 'After Disconnect and its confirmation: the row reads "not connected" with Connect; the fake\'s revoked list has the broker\'s access and refresh token; git pull in ~/pdf-editor exits non-zero and its stderr has "Termivoa is not connected to GitHub on this computer."',
        row.buttons.includes('Connect') && !!latest && revoked.includes(latest.access) && revoked.includes(latest.refresh)
          && pull.code !== 0 && pull.stderr.includes(NOT_CONNECTED),
        `row ${JSON.stringify(row)}; broker pairs ${pairs.length}, the last one revoked: access ${revoked.includes(latest?.access)}, refresh ${revoked.includes(latest?.refresh)}; pull exit ${pull.code}, stderr ${JSON.stringify(pull.stderr.trim().replaceAll(env.root, '<root>').slice(0, 300))}`);
      // The same in Live, as a person types it: git must not stop at a "Username for" prompt.
      try {
      await pdfLive();
      await typeLine(app, 'GIT_CONFIG_NOSYSTEM=1 git pull; echo "PULL2-EXIT=$?"');
      const done = await until(async () => { const t = squash(await terminalText(app)); return (/PULL2-EXIT=\d+/.test(t) || /Username for/.test(t)) && t; }, { what: 'pull ends or prompts', timeout: 30000 }).catch(() => '');
      const prompted = /Username for/.test(done);
      if (prompted) await app.send('Input.insertText', { text: '\x03' }).catch(() => undefined);
      check('G10-live', 'In Live, git pull after Disconnect shows the helper\'s line and ends; it does not stop at a "Username for" prompt',
        !prompted && done.replace(/\s/g, '').includes(NOT_CONNECTED.replace(/\s/g, '')) && /PULL2-EXIT=[1-9]/.test(done), `screen ${JSON.stringify(done.slice(-240))}`);
      } catch (error) {
        check('G10-live', 'In Live, git pull after Disconnect shows the helper\'s line and ends', false, error.message.split('\n')[0]);
      }
    });

    await step('G11', 'Connect again: an expired code says "The code ran out." with Get a new code; a denied one says "GitHub did not connect."', async () => {
      await integrations();
      await cardTap('mac', 'Connect');
      const first = await until(() => shownCode(), { what: 'a code', timeout: 20000 });
      await gh(`/_test/expire?code=${first}`, 'POST');
      const ran = await until(async () => { const s = await sheet(); return s?.text.includes('The code ran out.') && s; }, { what: 'The code ran out.', timeout: 20000 });
      await shot('g11-expired');
      await sheetTap('Get a new code');
      const second = await until(async () => { const c = await shownCode(); return c && c !== first && c; }, { what: 'a new code', timeout: 20000 });
      await gh(`/_test/deny?code=${second}`, 'POST');
      const denied = await until(async () => { const s = await sheet(); return s?.text.includes('GitHub did not connect.') && s; }, { what: 'GitHub did not connect.', timeout: 20000 });
      await shot('g11-denied');
      const after = await state(mac);
      check('G11', '"The code ran out." with Get a new code; a new code; after Cancel on GitHub "GitHub did not connect." with Try again; still not connected',
        ran.buttons.some((b) => b.text === 'Get a new code') && second !== first && denied.buttons.some((b) => b.text === 'Try again') && after?.state === 'notConnected',
        `expired ${first}: "${ran.text.slice(0, 100)}" ${JSON.stringify(ran.buttons.map((b) => b.text))}; denied ${second}: "${denied.text.slice(0, 100)}" ${JSON.stringify(denied.buttons.map((b) => b.text))}; state ${after?.state} ${after?.problem ?? ''}`);
      await closeSheets();
    });

    await step('G12', 'vps stayed "not connected" through all of it (each computer has its own login)', async () => {
      await watchVps('end');
      await integrations();
      const row = await card('vps');
      check('G12', 'GET /api/v1/github and the settings field on vps read notConnected at the start, after mac connected and at the end; the GitHub on vps row reads "not connected"',
        vpsStates.length >= 3 && vpsStates.every((s) => s.endsWith('notConnected / settings notConnected')) && row?.state === 'not connected',
        `${vpsStates.join('; ')}; row ${JSON.stringify(row)}`);
    });
  } finally {
    // Later steps run as without GitHub: no login, no running Connect, no folder or session of these checks.
    await closeSheets().catch(() => undefined);
    await gh('/_test/gitdelay?ms=0', 'POST').catch(() => undefined);
    for (const stack of [mac, vps]) {
      await action(stack, { action: 'cancel' }).catch(() => undefined);
      await action(stack, { action: 'disconnect' }).catch(() => undefined);
    }
    for (const s of sessions) await api(tool, `${mac.origin}/api/v1/sessions/${s.id}/terminate`, 'POST', { operationId: crypto.randomUUID(), generation: s.generation }).catch(() => undefined);
    for (const name of made) rmSync(join(mac.home, name), { recursive: true, force: true });
    rmSync(scratch, { recursive: true, force: true });
    if (existsSync(join(mac.home, 'pdf-editor'))) console.error('G: ~/pdf-editor is still on mac');
  }
}
