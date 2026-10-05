// Phase B's herdr checks (H-*; docs/herdr-provider.md, section 15, and
// docs/herdr-manage.md, sections 8 and 14): the fake herdr (scripts/fakeherdr) on
// mac, later on vps, inside the harness sandbox with each stack's own HOME.
// Nothing here reaches the real herdr.
import { existsSync, mkdirSync, realpathSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { api, click, run, setInput, sleep, terminalText, typeLine, until } from './lib.mjs';

// REVIEW_SHOTS=<folder>: the pictures a reviewer of docs/herdr-manage.md, section 14, looks at (demo data only).
const REVIEW_SHOTS = process.env.REVIEW_SHOTS;

const enc = encodeURIComponent;

/**
 * A fake herdr scenario from a short spec: workspaces with tabs, one pane
 * per tab, an agent where the tab names one. Checkouts are real folders
 * under home/src. Returns the scenario and the target IDs by
 * "<workspace>/<tab>".
 */
export function herdrScenario(home, { attachAny, kinds = ['claude', 'codex'], workspaces, dirty = [], closed = [] }) {
  const snapshot = { protocol: 20, version: '0.8.2', workspaces: [], tabs: [], panes: [], layouts: [], agents: [] };
  const targets = {};
  workspaces.forEach((spec, index) => {
    const number = index + 1;
    const id = `w${number}`;
    const path = join(home, 'src', spec.label);
    mkdirSync(path, { recursive: true });
    const workspace = { workspace_id: id, label: spec.label, number, active_tab_id: `${id}:t1`, agent_status: 'unknown', focused: false, tab_count: spec.tabs.length };
    if (spec.repo !== undefined) {
      const root = join(home, 'src', spec.repo);
      workspace.worktree = { repo_key: join(root, '.git'), repo_name: spec.repo, repo_root: root, checkout_path: path, is_linked_worktree: spec.repo !== spec.label };
    }
    snapshot.workspaces.push(workspace);
    spec.tabs.forEach((tab, t) => {
      const tabId = `${id}:t${t + 1}`;
      const paneId = `${id}:p${t + 1}`;
      const terminal = `term_e2e${number}0${t + 1}`;
      const status = tab.agent === undefined ? 'unknown' : tab.status;
      snapshot.tabs.push({ tab_id: tabId, workspace_id: id, label: tab.label, number: t + 1, focused: false, pane_count: 1, agent_status: status });
      snapshot.panes.push({ pane_id: paneId, tab_id: tabId, workspace_id: id, terminal_id: terminal, agent_status: status, focused: false, cwd: path });
      snapshot.layouts.push({ tab_id: tabId, workspace_id: id, focused_pane_id: paneId, panes: [{ pane_id: paneId, focused: true }] });
      if (tab.agent !== undefined) {
        snapshot.agents.push({ ...(tab.name === undefined ? {} : { name: tab.name }), pane_id: paneId, tab_id: tabId, workspace_id: id, terminal_id: terminal, agent: tab.agent, agent_status: status, state_change_seq: 1, cwd: path });
      }
      targets[`${spec.label}/${tab.label}`] = `${paneId}.${terminal}`;
    });
  });
  // dirty: labels of linked worktrees with changes that are not committed (worktree.remove needs force).
  const dirtyIds = dirty.map((label) => snapshot.workspaces.find((w) => w.label === label).workspace_id);
  // closed: linked worktrees of a repository that have no workspace (section 14, B2): { repo, branch }.
  const closedTrees = closed.map(({ repo, branch }) => ({ repo_key: join(home, 'src', repo, '.git'), branch }));
  return { scenario: { version: '0.8.2', protocol: 20, attachAny, kinds, snapshot, dirty: dirtyIds, closed: closedTrees }, targets };
}

/**
 * mac: two git projects (shop with a worktree, acme-web), one outside git, a long project name with a worktree
 * (lucky-valley, with changes that are not committed), and blog: a git repository that herdr reports with no
 * worktree record (docs/herdr-manage.md, A1). herdr 0.8.2 attaches any pane.
 * Section 14: five agent kinds run, all of them in lab (a kind is offered only while an agent of it runs, and the
 * checks remove worktrees), so New tab offers five; shop and
 * storefront-redesign each have a closed worktree. lab's amp agent has the name the host gives an amp agent in lab's
 * next tab (w8:p5), so agent.start there fails (agent_name_taken): the tab that is ready without its agent.
 */
const MAC = {
  attachAny: true,
  kinds: ['claude', 'codex', 'gemini', 'opencode', 'amp'],
  dirty: ['lucky-valley'],
  closed: [{ repo: 'shop', branch: 'old-idea' }, { repo: 'storefront-redesign', branch: 'rework-checkout-address-form-validation' }],
  workspaces: [
    { label: 'shop', repo: 'shop', tabs: [{ label: '1', agent: 'claude', status: 'idle' }, { label: 'logs' }] },
    { label: 'silver-meadow', repo: 'shop', tabs: [{ label: '1', agent: 'codex', status: 'working' }] },
    { label: 'acme-web', repo: 'acme-web', tabs: [{ label: '1', agent: 'claude', status: 'done' }] },
    { label: 'notes', tabs: [{ label: '1' }] },
    { label: 'storefront-redesign', repo: 'storefront-redesign', tabs: [{ label: '1' }] },
    { label: 'lucky-valley', repo: 'storefront-redesign', tabs: [{ label: '1', agent: 'claude', status: 'idle' }] },
    { label: 'blog', tabs: [{ label: '1' }] },
    { label: 'lab', tabs: [{ label: '1', agent: 'gemini', status: 'idle' }, { label: '2', agent: 'opencode', status: 'idle' }, { label: '3', agent: 'amp', status: 'idle', name: 'amp-w8-p5' }, { label: '4', agent: 'codex', status: 'idle' }] },
  ],
};
/** vps: one project; this herdr has no `terminal attach`, so its tab without an agent cannot open. */
const VPS = {
  attachAny: false,
  workspaces: [{ label: 'acme-web', repo: 'acme-web', tabs: [{ label: '1', agent: 'claude', status: 'working' }, { label: 'server' }] }],
};

/** p50 and p95 of n timed GETs from inside a page, in ms. */
async function latency(page, url, n = 30) {
  const times = await page.eval(`(async () => { const t = [];
    for (let i = 0; i < ${n}; i += 1) { const s = performance.now(); const r = await fetch(${JSON.stringify(url)}, { credentials: 'include', cache: 'no-store' }); await r.text(); if (!r.ok) throw new Error(r.status); t.push(performance.now() - s); }
    return t; })()`);
  times.sort((a, b) => a - b);
  const at = (q) => Math.round(times[Math.min(times.length - 1, Math.floor(q * times.length))] * 10) / 10;
  return { p50: at(0.5), p95: at(0.95) };
}

const fmt = (l) => `p50 ${l.p50} ms, p95 ${l.p95} ms`;

/** Runs the H-* steps. ctx holds the harness parts of phase-b.mjs. */
export async function herdrSteps(ctx) {
  const { env, mac, vps, app, tool, browser, step, check, shot, time, openSessions, inApp, jarOf } = ctx;
  const macHerdr = herdrScenario(mac.home, MAC);
  const vpsHerdr = herdrScenario(vps.home, VPS);
  const t = macHerdr.targets;
  const latencies = {};
  // blog is a real git repository; herdr (the scenario) says nothing about it.
  const made = await run('/usr/bin/git', ['init', '-q', join(mac.home, 'src', 'blog')], { env: { PATH: '/usr/bin:/bin', HOME: mac.home } });
  if (made.code !== 0) throw new Error(`git init: ${made.stderr}`);

  // ---- page helpers ----
  const herdrChip = () => app.eval(`(() => { const c = [...document.querySelectorAll('.chips .chip')].find((e) => e.innerText.trim().startsWith('herdr'));
    return c ? { text: c.innerText.replace(/\\s+/g, ' ').trim(), count: Number(c.querySelector('.chip-count')?.innerText ?? 0), pressed: c.getAttribute('aria-pressed') } : null; })()`);
  const groups = () => app.eval(`[...document.querySelectorAll('.provider-group')].map((g) => ({
    name: g.querySelector('.group-name')?.innerText.trim(),
    where: [...g.querySelectorAll('.group-where')].map((e) => e.innerText.trim().toLowerCase()),
    state: g.querySelector('.group-state')?.innerText.trim().toLowerCase() ?? '',
    open: g.querySelector('.group-toggle')?.getAttribute('aria-expanded'),
    items: [...g.querySelectorAll('.provider-item')].map((i) => ({ name: i.querySelector('.row-title')?.innerText.trim(), state: i.querySelector('.state-word')?.innerText.trim().toLowerCase() ?? '', meta: i.querySelector('.row-meta')?.innerText.trim() })) }))`);
  const problemsText = () => app.eval(`[...document.querySelectorAll('.provider-tree .notice')].map((e) => e.innerText.replace(/\\s+/g, ' ').trim())`);
  const statusLine = () => app.eval(`document.querySelector('.host-line')?.innerText.trim() ?? ''`);
  const sessionRows = () => app.eval(`[...document.querySelectorAll('.session-row:not(.provider-item)')].map((b) => b.querySelector('.row-title')?.firstChild?.textContent.trim())`);
  const toggleGroup = (name) => app.eval(`(() => { const b = [...document.querySelectorAll('.group-toggle')].find((e) => e.querySelector('.group-name')?.innerText.trim() === ${JSON.stringify(name)}); if (!b) return false; b.click(); return true; })()`);
  const tapItem = async (name) => {
    // Every project unfolded, then a worktree row first; a project row without worktrees (one row) after it.
    const ok = await app.eval(`(() => { const n = ${JSON.stringify(name)};
      document.querySelectorAll('.group-toggle[aria-expanded=false]').forEach((b) => b.click());
      const b = [...document.querySelectorAll('.provider-item')].find((e) => e.querySelector('.row-title')?.innerText.trim() === n)
        ?? [...document.querySelectorAll('.group-toggle:not([aria-expanded])')].find((e) => e.querySelector('.group-name')?.innerText.trim() === n);
      if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`no herdr row "${name}"`);
  };
  /** Sessions → the herdr tab (no page load when the app is already there). */
  async function herdrTab({ load = true } = {}) {
    if (load) await openSessions(); else await inApp('/');
    await until(() => herdrChip(), { what: 'the herdr tab', timeout: 20000 });
    if ((await herdrChip()).pressed !== 'true') await click(app, (await herdrChip()).text);
    await until(async () => (await app.eval(`document.querySelector('.provider-tree') !== null`)), { what: 'herdr projects' });
  }
  const attaches = (stack, pane) => stack.herdrLog().filter((l) => l.startsWith(`attach ${pane} size `)).length;
  const exits = (stack, pane) => stack.herdrLog().filter((l) => l.startsWith(`attach ${pane} exit `));
  const paneOf = (target) => target.split('.')[0];
  /** Waits until the fake logs an attach of pane after mark (a log length). */
  const attachedSince = (stack, pane, mark, timeout = 15000) => until(() => stack.herdrLog().slice(mark).find((l) => l.startsWith(`attach ${pane} size `)), { what: `attach of ${pane} on ${stack.name}`, timeout });
  const exitedSince = (stack, pane, mark, timeout = 20000) => until(() => stack.herdrLog().slice(mark).find((l) => l.startsWith(`attach ${pane} exit `)), { what: `attach of ${pane} on ${stack.name} ends`, timeout, every: 250 });
  const liveMarker = (pane) => until(async () => /FAKE-HERDR (\S+) (\d+)x(\d+)/.exec((await terminalText(app)).split('\n').find((l) => l.includes(`FAKE-HERDR ${pane} `)) ?? ''), { what: `fake terminal ${pane} in Live`, timeout: 20000 });
  const openApi = (stack, target, page = tool) => api(page, `${stack.origin}/api/v1/providers/herdr/open`, 'POST', { targetId: target, columns: 40, rows: 20 });
  /** Every provider session this device opened ends: no viewer for 10 s. */
  const settle = () => sleep(12000);

  // ---- H-none: a computer without herdr ----
  await step('H-none', 'No herdr anywhere: no herdr tab, no provider requests, settings list herdr as not installed', async () => {
    // A Termivoa session on mac, so the Sessions list has a row while herdr is in trouble (H-down, H-hang).
    const own = (await api(tool, `${mac.origin}/api/v1/settings`)).json;
    const made = await api(tool, `${mac.origin}/api/v1/sessions`, 'POST', { operationId: crypto.randomUUID(), projectId: own.projects[0].id, shellProfileId: own.shellProfiles[0].id });
    if (made.status !== 200 && made.status !== 201) throw new Error(`session on mac: ${made.status} ${made.body}`);
    await openSessions();
    await sleep(6000); // more than one provider poll would take
    const chip = await herdrChip();
    const listed = [];
    for (const stack of [mac, vps]) listed.push((await api(tool, `${stack.origin}/api/v1/settings`)).json?.providers);
    const requests = env.logSince(0).filter((e) => e.path?.startsWith('/api/v1/providers'));
    latencies.absent = await latency(tool, `${mac.origin}/api/v1/sessions`);
    time('GET /api/v1/sessions with herdr absent, p50', latencies.absent.p50);
    // docs/herdr-manage.md, 3.1: a computer without herdr lists it as notInstalled (before: no provider); the app shows no tab and never polls it.
    check('H-none', 'No herdr on either computer: no herdr tab, no provider request in the whole run; each computer\'s settings list herdr as notInstalled with canInstall',
      chip === null && requests.length === 0 && listed.every((p) => p?.length === 1 && p[0].id === 'herdr' && p[0].state === 'notInstalled' && p[0].canInstall === true && p[0].canStart === undefined),
      `chip ${JSON.stringify(chip)}; provider requests ${requests.length}; settings.providers mac ${JSON.stringify(listed[0])}, vps ${JSON.stringify(listed[1])}; sessions read ${fmt(latencies.absent)}`);
  });

  // ---- install the fake on mac; the broker finds it at its next lookup (≤ 30 s) ----
  await step('H-install', 'The fake herdr on mac is found without a restart', async () => {
    await mac.installHerdr(macHerdr.scenario);
    const started = Date.now();
    await until(async () => (await api(tool, `${mac.origin}/api/v1/settings`)).json?.providers?.[0]?.state === 'ready', { what: 'herdr ready on mac', timeout: 45000, every: 500 });
    time('herdr installed → listed ready in settings (lookup every 30 s)', Date.now() - started);
    const info = (await api(tool, `${mac.origin}/api/v1/settings`)).json.providers[0];
    check('H-install', 'herdr installed while Termivoa runs: mac lists it ready within the 30 s lookup', info.version === '0.8.2' && info.canCreate === true,
      `${Math.round((Date.now() - started) / 1000)} s; ${JSON.stringify(info)}`);
  });

  await step('H-doctor', 'pterm doctor reports the fake herdr (not the real one)', async () => {
    const doctor = await mac.doctor({ home: true });
    const lines = doctor.stdout.split('\n').filter((l) => l.includes('herdr'));
    const line = lines.find((l) => l.startsWith('  herdr: ')) ?? '';
    // The sandbox hides the real herdr ("no such file"): doctor does not list it, or lists it only as refused.
    const real = lines.filter((l) => l.includes('/opt/homebrew') || l.includes('/usr/local'));
    check('H-doctor', 'pterm doctor reports the fake herdr on this stack\'s PATH, its version and running server; the real herdr not at all, or only as refused (sandbox)',
      line.includes(`${mac.bin}/herdr 0.8.2 (protocol 20)`) && line.includes('server running') && real.every((l) => l.includes('Termivoa will not run it')),
      lines.map((l) => l.replaceAll(env.root, '<root>').trim()).join(' / '));
  });

  // ---- H-tab, H-group ----
  await step('H-tab', 'The herdr tab shows with the count for mac; nothing for vps', async () => {
    await openSessions();
    await until(() => herdrChip(), { what: 'herdr tab', timeout: 20000 });
    await herdrTab({ load: false });
    const list = await until(async () => { const g = await groups(); return g.length >= 4 && g; }, { what: 'herdr groups' });
    // The tab shows from the settings read; its count comes with the provider's list.
    const chip = await herdrChip();
    await shot('h-tab');
    // acme-web's claude is done: one terminal needs you.
    check('H-tab', 'The herdr tab shows with mac\'s count (1 done); every project is "on mac", none from vps',
      chip.count === 1 && list.every((g) => g.where.includes('on mac')) && !list.some((g) => g.where.includes('on vps')),
      `tab "${chip.text}"; groups ${JSON.stringify(list.map((g) => `${g.name} ${g.where.join(' ')} ${g.state}`))}`);
  });

  await step('H-group', 'A project row folds and unfolds; worktrees under it; roll-up word', async () => {
    await herdrTab({ load: false });
    const before = (await groups()).find((g) => g.name === 'shop');
    // shop runs (codex working in silver-meadow): open by default, roll-up WORKING.
    await toggleGroup('shop');
    const folded = (await groups()).find((g) => g.name === 'shop');
    await toggleGroup('shop');
    const unfolded = (await groups()).find((g) => g.name === 'shop');
    const notes = (await groups()).find((g) => g.name === 'notes');
    check('H-group', 'shop: open with main checkout then silver-meadow, roll-up "1 working"; folds to "2 worktrees" and unfolds; notes (outside git) is one row',
      before.open === 'true' && before.state.includes('1 working') && JSON.stringify(before.items.map((i) => i.name)) === '["shop","silver-meadow"]'
        && before.items[1].state.startsWith('working') && before.items[1].meta === 'codex · tab 1'
        && folded.open === 'false' && folded.items.length === 0 && folded.where.includes('2 worktrees')
        && unfolded.open === 'true' && unfolded.items.length === 2 && notes?.open === null,
      `before ${JSON.stringify(before)}; folded ${JSON.stringify(folded)}; notes ${JSON.stringify(notes)}`);
  });

  // ---- H-fit: the herdr tab at 390 and 375 px; a long project name keeps its room ----
  const overflow = (page = app) => page.eval(`(() => {
    const iw = innerWidth, keep = '.tab-scroll, .chips, .terminal-area';
    const name = (e) => e.tagName.toLowerCase() + (typeof e.className === 'string' && e.className ? '.' + e.className.trim().split(/\\s+/).join('.') : '') + ' "' + (e.textContent || '').trim().slice(0, 40) + '"';
    const all = [...document.querySelectorAll('body *')].filter((e) => !e.closest(keep));
    return [document.documentElement.scrollWidth > iw ? 'document ' + document.documentElement.scrollWidth : '',
      ...all.filter((e) => e.getBoundingClientRect().right > iw + 0.5).map((e) => name(e) + ' right ' + Math.round(e.getBoundingClientRect().right))].filter(Boolean).slice(0, 8);
  })()`);
  const nameFits = (name) => app.eval(`(() => { const e = [...document.querySelectorAll('.group-name')].find((x) => x.textContent.trim() === ${JSON.stringify(name)});
    return e ? { cut: e.scrollWidth > e.clientWidth + 1, width: Math.round(e.getBoundingClientRect().width), labels: [...e.closest('.group-toggle').querySelectorAll('.group-where')].map((w) => w.innerText.trim() + (w.getBoundingClientRect().width > 0 ? '' : ' (hidden)')) } : null; })()`);
  await step('H-fit', 'The herdr tab and the + sheet fit 375 and 390 px; a long project name is not cut while it fits', async () => {
    const seen = {};
    for (const width of [390, 375]) {
      await app.send('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 3, mobile: true });
      await herdrTab({ load: false });
      await until(async () => (await groups()).some((g) => g.name === 'storefront-redesign'), { what: 'storefront-redesign row' });
      seen[`tab${width}`] = await overflow();
      seen[`name${width}`] = await nameFits('storefront-redesign');
      await shot(`h-fit-${width}`);
      await click(app, 'New worktree in shop');
      await app.waitText('New worktree');
      seen[`sheet${width}`] = await overflow();
      await shot(`h-fit-sheet-${width}`);
      await click(app, 'Close');
    }
    await app.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    const names = [seen.name390, seen.name375];
    check('H-fit', 'Nothing wider than 375 or 390 px on the herdr tab and the + sheet; "storefront-redesign" shows whole with ON MAC and 2 WORKTREES beside or under it',
      ['tab390', 'tab375', 'sheet390', 'sheet375'].every((k) => seen[k].length === 0) && names.every((n) => n && !n.cut && n.labels.length === 2 && n.labels.every((l) => !l.endsWith('(hidden)'))),
      JSON.stringify(seen));
  });

  // ---- H-state ----
  await step('H-state', 'The fake turns an agent blocked: BLOCKED and the count within 10 s', async () => {
    await herdrTab({ load: false });
    const started = Date.now();
    await mac.herdrControl({ op: 'status', pane: paneOf(t['silver-meadow/1']), status: 'blocked' });
    await until(async () => {
      const shop = (await groups()).find((g) => g.name === 'shop');
      return shop?.state.includes('1 blocked') && shop.items.find((i) => i.name === 'silver-meadow')?.state.startsWith('blocked') && (await herdrChip()).count === 2;
    }, { what: 'BLOCKED on silver-meadow and count 2', timeout: 10000, every: 100 });
    const ms = Date.now() - started;
    time('herdr state change → BLOCKED on the phone (host poll 2 s + phone read 5 s)', ms);
    await shot('h-state-blocked');
    check('H-state', 'BLOCKED on silver-meadow, "1 blocked" on shop and the tab count 2, within 10 s', ms <= 10000,
      `${ms} ms; status line "${await statusLine()}"`);
  });

  // ---- H-open, H-type ----
  let opened;
  await step('H-open', 'Tap the blocked worktree: Live shows the fake terminal at the phone size, read-only; not in Sessions, the Wall or pterm list', async () => {
    await herdrTab({ load: false });
    const pane = paneOf(t['silver-meadow/1']);
    const mark = mac.herdrLog().length;
    const started = Date.now();
    await tapItem('silver-meadow');
    await until(() => app.eval(`location.pathname === ${JSON.stringify(`/p/herdr/${enc(t['silver-meadow/1'])}`)}`), { what: 'the /p/ address' });
    const [, , cols, rows] = await liveMarker(pane);
    time('tap a herdr row → the fake terminal painted in Live', Date.now() - started);
    const logged = /size (\d+)x(\d+)/.exec(await attachedSince(mac, pane, mark));
    const text = await app.text();
    await shot('h-open-live');
    const list = (await api(tool, `${mac.origin}/api/v1/sessions`)).json.sessions;
    const cli = (await mac.cli(['list'])).stdout;
    opened = { pane, cols: Number(cols), rows: Number(rows) };
    check('H-open', 'Live shows the fake terminal (agent attach of w2:p1) at the phone\'s grid, read-only with Take control and the size line; it is not in GET /api/v1/sessions or pterm list',
      logged[1] === cols && logged[2] === rows && Number(cols) >= 20 && Number(cols) <= 60 && text.includes('Take control')
        && text.includes('mac shows this terminal at phone size while it is open here.') && !list.some((s) => s.title.startsWith('herdr')) && !cli.includes('herdr'),
      `fake drew ${cols}x${rows}, logged ${logged[0]}; ${list.length} listed sessions, none herdr; status "${await app.eval(`document.querySelector('.status-line')?.innerText.replace(/\\s+/g, ' ')`)}"`);
  });

  await step('H-type', 'Take control, type and ctrl+b: the fake gets the text and one literal ctrl+b', async () => {
    const pane = opened?.pane ?? paneOf(t['silver-meadow/1']);
    if (!(await app.text()).includes('Take control')) throw new Error('not read-only in Live');
    await click(app, 'Take control');
    await until(async () => !(await app.text()).includes('Take control'), { what: 'control taken' });
    const mark = mac.herdrLog().length;
    await typeLine(app, 'hello-herdr');
    await app.eval(`document.querySelector('.terminal-input').focus()`);
    for (const type of ['keyDown', 'keyUp']) await app.send('Input.dispatchKeyEvent', { type, modifiers: 2, key: 'b', code: 'KeyB', windowsVirtualKeyCode: 66 });
    await typeLine(app, 'after');
    const hex = (s) => Buffer.from(s).toString('hex');
    const input = await until(() => { const lines = mac.herdrLog().slice(mark).filter((l) => l.startsWith(`input ${pane} `)).map((l) => l.split(' ')[2]).join(''); return lines.includes(hex('after')) && lines; }, { what: 'input in fake.log' });
    await shot('h-type');
    // ctrl+b reaches herdr doubled (02 02); its attach client passes one literal 02 and drops nothing after it.
    check('H-type', 'The fake logs "hello-herdr", Enter, one literal ctrl+b (0x02) and the next line whole: the broker doubled 0x02',
      input.includes(`${hex('hello-herdr')}0d`) && input.includes(`02${hex('after')}`) || input.includes(`${hex('hello-herdr')}0d02`),
      `input hex ${input}`);
  });

  await step('H-chips', 'Chips switch terminals; a tab without an agent opens on herdr 0.8.2 (terminal attach)', async () => {
    await herdrTab({ load: false });
    await tapItem('shop');
    await liveMarker(paneOf(t['shop/1']));
    const chipsNow = await app.eval(`[...document.querySelectorAll('.provider-chips .tab')].map((b) => ({ label: b.innerText.trim(), disabled: b.disabled, current: b.getAttribute('aria-current') }))`);
    const mark = mac.herdrLog().length;
    await click(app, 'logs');
    const pane = paneOf(t['shop/logs']);
    await until(() => app.eval(`location.pathname === ${JSON.stringify(`/p/herdr/${enc(t['shop/logs'])}`)}`), { what: 'the logs chip address' });
    await liveMarker(pane);
    await attachedSince(mac, pane, mark);
    await shot('h-chips');
    check('H-chips', 'shop shows chips "1" (current) and "logs" (enabled); tapping logs opens that tab\'s pane with terminal attach',
      chipsNow.length === 2 && chipsNow[0].current === 'page' && chipsNow[1].label === 'logs' && !chipsNow[1].disabled,
      `chips ${JSON.stringify(chipsNow)}; then ${await app.eval('location.pathname')}`);
  });

  await step('H-end', 'Leave Live: the attach ends about 10 s later', async () => {
    const pane = paneOf(t['shop/logs']);
    const mark = mac.herdrLog().length;
    const left = Date.now();
    await click(app, '‹ Sessions');
    const line = await exitedSince(mac, pane, mark, 20000);
    const ms = Date.now() - left;
    time('leave Live → herdr attach ends (10 s rule)', ms);
    check('H-end', 'After Live closes, the fake attach exits within 15 s (and not before 9 s): the laptop pane gets its size back',
      ms >= 9000 && ms <= 15000 && line.includes('exit 1 hangup'), `${line} after ${ms} ms`);
  });

  await step('H-hidden', 'Page hidden closes the stream; back in front it opens again', async () => {
    await settle();
    await herdrTab({ load: false });
    const pane = paneOf(t['acme-web/1']);
    await tapItem('acme-web');
    await liveMarker(pane);
    const mark = mac.herdrLog().length;
    const hidden = Date.now();
    await tool.front();
    const state = await app.eval('document.visibilityState');
    const line = await exitedSince(mac, pane, mark, 20000);
    const ms = Date.now() - hidden;
    const back = mac.herdrLog().length;
    await app.front();
    await attachedSince(mac, pane, back, 15000);
    await liveMarker(pane);
    check('H-hidden', 'With the app hidden (visibility hidden), the attach ends about 10 s later; in front again, it opens again by itself',
      state === 'hidden' && ms <= 15000, `visibility ${state}; ${line} after ${ms} ms; attached again`);
  });

  await step('H-reuse', 'Two opens of one target share one attach; the 5th terminal gets the limit', async () => {
    await inApp('/');
    await settle();
    const ids = [t['shop/1'], t['acme-web/1'], t['notes/1'], t['lucky-valley/1'], t['storefront-redesign/1']];
    const mark = mac.herdrLog().length;
    const first = await openApi(mac, ids[0]);
    const second = await openApi(mac, ids[0]);
    await sleep(1500);
    const attachCount = mac.herdrLog().slice(mark).filter((l) => l.startsWith(`attach ${paneOf(ids[0])} size `)).length;
    check('H-reuse', 'Two opens of one target: the same session, reused, one attach in the fake',
      first.status === 200 && second.status === 200 && second.json.reused === true && first.json.session.id === second.json.session.id && attachCount === 1,
      `first ${first.status} reused=${first.json?.reused}, second ${second.status} reused=${second.json?.reused}; same id ${first.json?.session.id === second.json?.session.id}; attaches ${attachCount}`);
    const answers = [];
    for (const id of ids.slice(1)) answers.push(await openApi(mac, id));
    const statuses = answers.map((a) => a.status);
    const limited = answers.find((a) => a.status === 429);
    // The app says it in its own words.
    await herdrTab({ load: false });
    await tapItem('storefront-redesign');
    const said = await until(async () => { const text = await app.text(); return text.includes('mac cannot open more herdr terminals just now') && text; }, { what: 'limit text', timeout: 10000 }).catch(() => '');
    await shot('h-cap');
    check('H-cap', 'With 4 herdr terminals open, the 5th open gets 429 limit_exceeded and the app says "mac cannot open more herdr terminals just now…"',
      JSON.stringify(statuses) === '[200,200,200,429]' && limited.json?.error?.code === 'limit_exceeded' && said !== '',
      `opens ${JSON.stringify(statuses)}; 5th ${limited?.status} ${limited?.json?.error?.code} "${limited?.json?.error?.message}"; app: ${said ? 'limit text shown' : 'no limit text'}`);
    await inApp('/');
    await settle();
  });

  await step('H-auth', 'Provider routes: no cookie 401, bad target 400, unknown target 404, another origin refused', async () => {
    const noCookie = await env.curl(`${mac.origin}/api/v1/providers/herdr`, { headers: { Origin: mac.origin } });
    const bad = await openApi(mac, '../etc');
    const unknown = await openApi(mac, 'w9:p9.term_gone');
    const evil = await env.curl(`${mac.origin}/api/v1/providers/herdr`, { headers: { Cookie: await jarOf(mac), Origin: 'https://evil.tailx.ts.net' } });
    const unknownProvider = await api(tool, `${mac.origin}/api/v1/providers/tmux`);
    check('H-auth', 'GET from the app origin without a cookie 401; open with a bad targetId 400 and with an unknown one 404; a provider this computer does not have 400; Origin evil 403',
      noCookie.status === 401 && bad.status === 400 && unknown.status === 404 && unknownProvider.status === 400 && evil.status === 403,
      `no cookie ${noCookie.status}; bad ${bad.status} ${bad.json?.error?.code}; unknown ${unknown.status} ${unknown.json?.error?.code} "${unknown.json?.error?.message}"; tmux ${unknownProvider.status}; evil ${evil.status}`);
  });

  await step('H-feature', 'History, rename, terminate and upload: not offered in Live and refused by the host', async () => {
    await herdrTab({ load: false });
    await tapItem('lucky-valley');
    const pane = paneOf(t['lucky-valley/1']);
    await liveMarker(pane);
    await click(app, 'Take control');
    await until(async () => !(await app.text()).includes('Take control'), { what: 'control taken' });
    const offered = await app.eval(`[...document.querySelectorAll('button, a')].filter((e) => e.getClientRects().length).map((e) => (e.getAttribute('aria-label') || e.innerText).replace(/\\s+/g, ' ').trim()).filter(Boolean)`);
    const banned = offered.filter((label) => /history|rename|terminate|end session|photo|upload|session menu|preview|worktree|start like this/i.test(label));
    const session = (await openApi(mac, t['lucky-valley/1'])).json.session;
    const rename = await api(tool, `${mac.origin}/api/v1/sessions/${session.id}`, 'PATCH', { operationId: crypto.randomUUID(), generation: session.generation, title: 'x' });
    const end = await api(tool, `${mac.origin}/api/v1/sessions/${session.id}/terminate`, 'POST', { operationId: crypto.randomUUID(), generation: session.generation });
    // A 1×1 PNG, with this device in control.
    const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';
    const upload = await api(tool, `${mac.origin}/api/v1/sessions/${session.id}/uploads`, 'POST', { generation: session.generation, data: png });
    await shot('h-feature-live');
    check('H-feature', 'Live for a herdr terminal offers no History, rename, terminate, photo, preview or worktree; the host refuses rename (404), terminate (404) and upload for it',
      banned.length === 0 && rename.status === 404 && end.status === 404 && upload.status >= 400 && upload.status < 500,
      `offered ${JSON.stringify(offered)}; rename ${rename.status} ${rename.json?.error?.code}; terminate ${end.status} ${end.json?.error?.code}; upload ${upload.status} ${upload.json?.error?.code}; history.read refused over the stream: Go TestProviderSessionRefusesFeatures`);
    await inApp('/');
  });

  // ---- H-link: an address alone shows Open; a notification tap opens at once ----
  await step('H-link', 'A plain /p/… link shows Open and never opens by itself; a notification tap opens at once', async () => {
    await settle();
    const target = t['acme-web/1'];
    const pane = paneOf(target);
    const path = `/p/herdr/${enc(target)}`;
    let mark = mac.herdrLog().length;
    await app.go(`${mac.origin}${path}`);
    await app.waitText('Open shows it here, read-only.');
    await sleep(5000);
    const quiet = mac.herdrLog().slice(mark).filter((l) => l.startsWith(`attach ${pane} `)).length;
    const facts = await app.eval(`document.querySelector('.provider-open')?.innerText.replace(/\\s+/g, ' ').trim()`);
    await shot('h-link-open-button');
    await click(app, 'Open');
    await attachedSince(mac, pane, mark);
    await liveMarker(pane);
    check('H-link', 'A loaded /p/herdr/<target> shows the terminal\'s names and Open, attaches nothing for 5 s, and opens on Open',
      quiet === 0 && facts.includes('acme-web') && facts.includes('claude'), `no attach for 5 s (${quiet}); screen "${facts}"; Open attached`);

    // The tap: a real provider push to the app's own worker, its notificationclick, then the address the worker navigates to.
    await inApp('/');
    await settle();
    await browser.send('Browser.grantPermissions', { origin: mac.origin, permissions: ['notifications'] });
    const ids = new Map();
    browser.listeners.push((m) => { if (m.method === 'ServiceWorker.workerRegistrationUpdated') for (const r of m.params.registrations) ids.set(r.scopeURL, r.registrationId); });
    // Off and on again: enable lists every registration (B3-push may have enabled it already).
    await app.send('ServiceWorker.disable').catch(() => undefined);
    await app.send('ServiceWorker.enable');
    const registrationId = await until(() => ids.get(`${mac.origin}/`), { what: 'home worker registration' });
    await app.send('ServiceWorker.deliverPushMessage', { origin: mac.origin, registrationId, data: JSON.stringify({
      title: 'herdr · acme-web', body: 'claude finished after 12 min.', kind: 'done', provider: 'herdr', target }) });
    const shown = await until(() => app.eval(`navigator.serviceWorker.getRegistration('/').then((r) => r.getNotifications()).then((n) => n.map((x) => ({ title: x.title, url: x.data?.url, tag: x.tag }))
      .find((x) => x.title === 'herdr · acme-web'))`), { what: 'provider notification' });
    const clicked = await clickNotification(browser, `${mac.origin}/`, 'herdr · acme-web');
    mark = mac.herdrLog().length;
    // The worker's client.navigate(url) after focus(): the browser refuses focus() for a synthetic click, so the harness loads the address.
    await app.go(`${mac.origin}${shown.url}`);
    await attachedSince(mac, pane, mark, 10000);
    await liveMarker(pane);
    await shot('h-link-tap');
    // A second load of the same address: the tap record is used up.
    await inApp('/');
    await settle();
    mark = mac.herdrLog().length;
    await app.go(`${mac.origin}${shown.url}`);
    await app.waitText('Open shows it here, read-only.');
    await sleep(3000);
    const again = mac.herdrLog().slice(mark).filter((l) => l.startsWith(`attach ${pane} `)).length;
    check('H-tap', 'A provider push shows "herdr · acme-web" with /p/herdr/<target> (no sessionId); after its notificationclick the address opens at once without Open; the same address again shows Open',
      shown.url === path && shown.tag === `p:herdr:${target}:done` && again === 0,
      `notification ${JSON.stringify(shown)}; worker click: ${clicked}; opened at once; second load attached ${again}`);
    await inApp('/');
  });

  // ---- H-down, H-hang: herdr in trouble, Sessions stays fast ----
  await step('H-down', 'herdr stopped: the herdr tab says so; Sessions stays healthy and fast', async () => {
    await settle();
    latencies.present = await latency(tool, `${mac.origin}/api/v1/sessions`);
    latencies.providerRead = await latency(tool, `${mac.origin}/api/v1/providers/herdr`);
    time('GET /api/v1/sessions with herdr running, p50', latencies.present.p50);
    time('GET /api/v1/providers/herdr, p50', latencies.providerRead.p50);
    await herdrTab({ load: false });
    const started = Date.now();
    await mac.herdrControl({ op: 'stop' });
    const said = await until(async () => (await problemsText()).find((p) => p.includes('herdr is not running on mac')), { what: 'not running', timeout: 15000, every: 200 });
    const ms = Date.now() - started;
    time('herdr stopped → "herdr is not running on mac"', ms);
    latencies.down = await latency(tool, `${mac.origin}/api/v1/sessions`);
    await click(app, 'All');
    const line = await statusLine();
    const sessions = await sessionRows();
    await shot('h-down');
    check('H-down', 'herdr stopped: "herdr is not running on mac" on the herdr tab; the Sessions list and its status line stay as before',
      ms <= 15000 && sessions.length > 0 && !/can't reach|herdr/.test(line),
      `${ms} ms; "${said}"; status line "${line}"; ${sessions.length} sessions; sessions read running ${fmt(latencies.present)}, stopped ${fmt(latencies.down)}`);
    await mac.herdrControl({ op: 'start' });
    await herdrTab({ load: false });
    await until(async () => (await groups()).length >= 4, { what: 'groups back', timeout: 20000 });
  });

  await step('H-hang', 'herdr socket hangs: Sessions unaffected and fast; herdr shows not running', async () => {
    await herdrTab({ load: false });
    const started = Date.now();
    await mac.herdrControl({ op: 'hang', on: true });
    const hungRead = await latency(tool, `${mac.origin}/api/v1/sessions`, 20);
    await until(async () => (await problemsText()).some((p) => p.includes('herdr is not running on mac')), { what: 'not running after 3 misses', timeout: 25000, every: 250 });
    const ms = Date.now() - started;
    latencies.hung = await latency(tool, `${mac.origin}/api/v1/sessions`);
    const providerHung = await latency(tool, `${mac.origin}/api/v1/providers/herdr`, 10);
    await click(app, 'All');
    const line = await statusLine();
    const sessions = await sessionRows();
    await shot('h-hang');
    const fast = latencies.hung.p95 < Math.max(50, latencies.present.p95 * 3) && hungRead.p95 < Math.max(50, latencies.present.p95 * 3);
    check('H-hang', 'The fake socket accepts and never answers: "herdr is not running on mac" after the 3 missed polls; the Sessions list and reads stay as fast',
      fast && sessions.length > 0 && !/can't reach|herdr/.test(line),
      `not running after ${ms} ms; sessions read while hanging ${fmt(hungRead)}, after ${fmt(latencies.hung)} (absent ${fmt(latencies.absent ?? {})}, running ${fmt(latencies.present)}); provider read ${fmt(providerHung)}; status line "${line}"`);
    time('GET /api/v1/sessions with herdr hung, p50', latencies.hung.p50);
    await mac.herdrControl({ op: 'hang', on: false });
    await herdrTab({ load: false });
    await until(async () => (await groups()).length >= 4, { what: 'groups back', timeout: 30000 });
  });

  // ---- H-settings ----
  await step('H-settings', 'Settings → Integrations: off hides mac\'s herdr, on brings it back; the buzz switches survive a reload', async () => {
    await inApp('/settings');
    // innerText follows text-transform: the caps headings and state words read in capitals.
    const card = () => app.eval(`(() => { const li = [...document.querySelectorAll('.computer-card')].find((e) => e.innerText.includes('herdr on mac'));
      return li ? { text: li.innerText.replace(/\\s+/g, ' ').trim().toLowerCase(), switches: [...li.querySelectorAll('[role=switch]')].map((s) => s.getAttribute('aria-checked')) } : null; })()`);
    const flip = (label) => app.eval(`(() => { const li = [...document.querySelectorAll('.computer-card')].find((e) => e.innerText.includes('herdr on mac'));
      const s = [...li.querySelectorAll('[role=switch]')].find((b) => document.getElementById(b.getAttribute('aria-labelledby'))?.innerText.trim() === ${JSON.stringify(label)}); s.click(); return true; })()`);
    const first = await until(() => card(), { what: 'herdr on mac card', timeout: 20000 });
    await shot('h-settings');
    await flip('Buzz when finished');
    await until(async () => (await card()).text.includes('saved.'), { what: 'saved' });
    await flip('On');
    await until(async () => (await card()).text.includes('off for every phone: mac does not show herdr or buzz for it.'), { what: 'Off' });
    await inApp('/');
    await sleep(6000);
    const hidden = await herdrChip();
    await inApp('/settings');
    await until(() => card(), { what: 'card' });
    await flip('On');
    const on = Date.now();
    await until(async () => (await card()).text.includes('running') && !(await card()).text.includes('not running'), { what: 'Running', timeout: 15000, every: 100 });
    const runningAfter = Date.now() - on;
    await app.go(`${mac.origin}/settings`);
    const reloaded = await until(() => card(), { what: 'card after reload' });
    await openSessions();
    const back = await until(() => herdrChip(), { what: 'herdr tab back', timeout: 20000 });
    await mac.herdrControl({ op: 'status', pane: paneOf(t['shop/1']), status: 'idle' }); // nothing changed in herdr meanwhile
    check('H-settings', 'Off: the herdr tab leaves (only mac had herdr); On: it returns; "Buzz when finished" off survives a reload; Integrations reads "Running" from the snapshot',
      first.text.includes('running') && runningAfter < 7000 && hidden === null && back !== null && JSON.stringify(reloaded.switches) === '["true","true","false"]',
      `before ${JSON.stringify(first)}; while off: tab ${JSON.stringify(hidden)}; on → Running after ${runningAfter} ms; after reload ${JSON.stringify(reloaded)}; tab back "${back?.text}"`);
    // Buzz when finished on again, as the other checks expect.
    await api(tool, `${mac.origin}/api/v1/providers/herdr`, 'PATCH', { notify: ['blocked', 'done'] });
  });

  // ---- H-create (M2) ----
  await step('H-create', '+ on shop: a new worktree with claude appears and opens; errors show fixed texts; a double tap creates once', async () => {
    await settle();
    await herdrTab({ load: false });
    await click(app, 'New worktree in shop');
    await app.waitText('New worktree');
    // Bad name and ESC in the prompt: refused in the app before sending.
    await setInput(app, '#create-name', 'Bad Name!');
    const badName = await app.eval(`document.querySelector('#create-name-help')?.innerText`);
    await setInput(app, '#create-name', 'fix-login');
    await setInput(app, '#create-prompt', 'hi\u001b[31m');
    const escape = await app.eval(`({ text: document.querySelector('#create-prompt-help')?.innerText, disabled: document.querySelector('.create-form button[type=submit]').disabled })`);
    await setInput(app, '#create-prompt', 'say hello');
    await shot('h-create-sheet');
    const mark = mac.herdrLog().length;
    const started = Date.now();
    // Two taps in one go: one create.
    await app.eval(`(() => { const b = document.querySelector('.create-form button[type=submit]'); b.click(); b.click(); return true; })()`);
    await until(() => app.eval(`location.pathname.startsWith('/p/herdr/')`), { what: 'opens the new worktree', timeout: 45000 });
    const created = mac.herdrLog().slice(mark);
    const newTarget = decodeURIComponent((await app.eval('location.pathname')).split('/').pop());
    await liveMarker(paneOf(newTarget));
    time('+ create worktree with claude → its terminal in Live', Date.now() - started);
    await shot('h-create-live');
    await inApp('/');
    await herdrTab({ load: false });
    const shop = await until(async () => (await groups()).find((g) => g.name === 'shop' && g.items.some((i) => i.name === 'fix-login')), { what: 'fix-login row' });
    check('H-create', 'The app refuses "Bad Name!" and an ESC in the prompt; Create and start makes one worktree (two taps), starts claude with the prompt, opens its terminal and lists fix-login under shop',
      /lowercase letters/.test(badName) && /control characters/.test(escape.text ?? '') && escape.disabled
        && created.filter((l) => l.startsWith('worktree.create ')).length === 1 && created.some((l) => l.startsWith('agent.start claude'))
        && created.some((l) => l.startsWith('agent.prompt') && l.includes('say hello')) && shop.items.find((i) => i.name === 'fix-login').meta.startsWith('claude'),
      `name "${badName}"; prompt "${escape.text}" (button disabled ${escape.disabled}); fake: ${created.filter((l) => !l.startsWith('attach') && !l.startsWith('input')).join(' | ')}; row ${JSON.stringify(shop.items.find((i) => i.name === 'fix-login'))}`);

    // The same name again: the fixed text, no herdr or git text.
    await click(app, 'New worktree in shop');
    await app.waitText('New worktree');
    await setInput(app, '#create-name', 'fix-login');
    await click(app, 'No agent');
    await click(app, 'Create');
    const exists = await until(() => app.eval(`document.querySelector('.create-form [role=alert]')?.innerText.trim()`), { what: 'exists text' });
    await shot('h-create-exists');
    await click(app, 'Close');
    // Not in git, and an ESC prompt, straight to the host (the app never offers +, or sends, either).
    const groupsNow = (await api(tool, `${mac.origin}/api/v1/providers/herdr`)).json.groups;
    const notes = groupsNow.find((g) => g.name === 'notes');
    const shopId = groupsNow.find((g) => g.name === 'shop').id;
    const notGit = await api(tool, `${mac.origin}/api/v1/providers/herdr/create`, 'POST', { groupId: notes.id, name: 'try-it' });
    const esc = await api(tool, `${mac.origin}/api/v1/providers/herdr/create`, 'POST', { groupId: shopId, name: 'esc-test', agent: 'claude', prompt: 'hi\u001b[31m' });
    const badHost = await api(tool, `${mac.origin}/api/v1/providers/herdr/create`, 'POST', { groupId: shopId, name: 'Bad Name!' });
    check('H-create-errors', 'Exists: "“fix-login” already exists in shop. Pick another name."; outside git the host answers not_a_repository with its fixed text; the host refuses a bad name and an ESC prompt (400)',
      exists === '“fix-login” already exists in shop. Pick another name.' && notGit.json?.error?.code === 'not_a_repository' && notGit.json?.error?.message === 'this project is not in a git repository'
        && esc.status === 400 && badHost.status === 400 && notes.canCreate === false,
      `exists "${exists}"; notes canCreate ${notes.canCreate}, create ${notGit.status} ${notGit.json?.error?.code} "${notGit.json?.error?.message}"; ESC ${esc.status}; bad name ${badHost.status}`);
    await inApp('/');
  });

  // ---- manage herdr from the phone (docs/herdr-manage.md, sections 3, 6 and 7) ----
  const ITEM = 'item-menu-title';
  const WORKSPACE = 'workspace-title';
  const CHANGED = 'The list changed. Look again, then try again.';
  const INSTALL = 'curl -fsSL https://herdr.dev/install.sh | sh';
  const manageUrl = `${mac.origin}/api/v1/providers/herdr/manage`;
  const manageApi = (body) => api(tool, manageUrl, 'POST', body);
  const providerGroups = async () => (await api(tool, `${mac.origin}/api/v1/providers/herdr`)).json?.groups ?? [];
  /** An item of mac's list by name, with its group. */
  const itemOf = async (name) => (await providerGroups()).flatMap((g) => g.items.map((i) => ({ ...i, group: g }))).find((i) => i.name === name);
  /** A workspace in the fake's live snapshot, by label. */
  const workspaceIn = (label) => mac.herdrState()?.workspaces.find((w) => w.label === label);
  const logged = (mark, prefix) => mac.herdrLog().slice(mark).filter((l) => l.startsWith(prefix));
  // A Button keeps its busy label in a hidden span: the shown label only.
  const LABEL = `const label = (b) => (b.querySelector('.btn-swap > span:not(.off)') ?? b).innerText.replace(/\\s+/g, ' ').trim();
    const text = (e) => (e ? e.innerText.replace(/\\s+/g, ' ').trim() : null);`;
  /** The open sheet: its menu buttons, the confirmation, the New tab chooser and every notice in it; null when it is closed. */
  const sheetOf = (title) => app.eval(`(() => { ${LABEL}
    const chooser = (c) => (c ? { title: text(c.querySelector('#new-tab-title')), kinds: [...c.querySelectorAll('.kind-chips button')].map((b) => ({ text: label(b), pressed: b.getAttribute('aria-pressed') })),
      prompt: c.querySelector('#create-prompt') !== null, help: text(c.querySelector('#create-prompt-help')),
      pair: [...c.querySelectorAll('.confirm-pair button')].map((b) => ({ text: label(b), primary: b.classList.contains('primary'), disabled: b.disabled })) } : null);
    const d = document.querySelector('[role="dialog"][aria-labelledby="${title}"]'); if (!d) return null;
    return { title: text(d.querySelector('#${title}')), buttons: [...d.querySelectorAll('.menu-list button')].map((b) => ({ text: label(b), disabled: b.disabled })),
      first: text(d.querySelector('#close-first')), confirm: text(d.querySelector('[role="alertdialog"] #manage-confirm')),
      confirmButtons: [...d.querySelectorAll('[role="alertdialog"] button')].map(label), notes: [...d.querySelectorAll('.notice')].map(text),
      tabs: [...d.querySelectorAll('.tab-rows li')].map((li) => ({ name: text(li.querySelector('.tab-row-name')), close: [...li.querySelectorAll('button')].map(label).join() })),
      tabHint: text(d.querySelector('.tab-rows .hint-text')), chooser: chooser(d.querySelector('[role="group"][aria-labelledby="new-tab-title"]')) }; })()`);
  /** Taps the enabled button with this exact label inside scope (a selector under the sheet). */
  const sheetTap = async (title, scope, name) => {
    const ok = await app.eval(`(() => { ${LABEL}
      const b = [...document.querySelectorAll('[role="dialog"][aria-labelledby="${title}"] ${scope} button')].find((e) => label(e) === ${JSON.stringify(name)});
      if (!b || b.disabled) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`no enabled button "${name}" in ${title} ${scope}`);
    await sleep(300);
  };
  /** The danger button of a confirmation. The app takes no tap on it for the first 400 ms (a wrong double tap). */
  const confirmTap = async (name) => { await sleep(600); await sheetTap(ITEM, '.confirm-pair', name); };
  /** Every folded project open, so its worktrees show. */
  const unfold = () => app.eval(`(() => { document.querySelectorAll('.group-toggle[aria-expanded=false]').forEach((b) => b.click()); return true; })()`);
  const closeSheet = async (title) => {
    await sheetTap(title, '.sheet-header', 'Close');
    await until(async () => (await sheetOf(title)) === null, { what: 'sheet closed' });
  };
  /** Taps the ⋯ of the row named name (every project unfolded first). */
  const openMenu = async (name) => {
    await unfold();
    await until(() => app.eval(`(() => { const b = document.querySelector(${JSON.stringify(`button.item-menu[aria-label="Menu for ${name}"]`)}); if (!b) return false; b.click(); return true; })()`), { what: `the menu button of ${name}` });
    return until(() => sheetOf(ITEM), { what: `the menu of ${name}` });
  };
  /** Every project and worktree name the herdr tab shows now. */
  const rowNames = async () => (await unfold(), await groups()).flatMap((g) => [g.name, ...g.items.map((i) => i.name)]);
  const labels = (sheet) => sheet.buttons.map((b) => `${b.text}${b.disabled ? ' (off)' : ''}`);
  /** Back to the herdr tab from anywhere in the app. */
  const backToTab = async () => { await inApp('/'); await herdrTab({ load: false }); };
  /** The Integrations card "herdr on <computer>". innerText follows text-transform: lower case here. */
  const integrationCard = (name) => app.eval(`(() => { ${LABEL}
    const li = [...document.querySelectorAll('.computer-card')].find((e) => text(e.querySelector('.row-title')) === ${JSON.stringify(`herdr on ${name}`)});
    if (!li) return null; const s = li.querySelector('.computer-state');
    return { state: text(s).toLowerCase(), muted: s.classList.contains('muted'), switches: li.querySelectorAll('[role=switch]').length,
      buttons: [...li.querySelectorAll('button:not([role=switch])')].map(label), text: text(li) }; })()`);
  const cardTap = (name, button) => app.eval(`(() => { ${LABEL}
    const li = [...document.querySelectorAll('.computer-card')].find((e) => text(e.querySelector('.row-title')) === ${JSON.stringify(`herdr on ${name}`)});
    const b = [...(li?.querySelectorAll('button') ?? [])].find((e) => label(e) === ${JSON.stringify(button)}); if (!b || b.disabled) return false; b.click(); return true; })()`);
  const openWorkspaceSheet = async () => {
    const ok = await app.eval(`(() => { const b = [...document.querySelectorAll('.provider-computer > button.secondary')].find((e) => e.innerText.trim().startsWith('New workspace')); if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error('no New workspace button');
    return until(() => sheetOf(WORKSPACE), { what: 'the New workspace sheet' });
  };
  // ---- section 14: the New tab chooser, closed worktrees, the menu in Live ----
  /** New tab in the open item menu → the chooser (a computer at revision 6 with an agent kind). */
  const openChooser = async () => {
    await sheetTap(ITEM, '.menu-list', 'New tab');
    return until(async () => (await sheetOf(ITEM))?.chooser, { what: 'the New tab chooser' });
  };
  /** Shell takes no tap for the chooser's first 400 ms. */
  const shellTap = async () => { await sleep(600); await sheetTap(ITEM, '.kind-chips', 'Shell'); };
  /** The closed worktree rows under a project (unfolded first); null when the project is not a group row. */
  const closedRows = async (name) => (await unfold(), app.eval(`(() => { ${LABEL}
    const g = [...document.querySelectorAll('.provider-group')].find((e) => e.querySelector('.group-name')?.innerText.trim() === ${JSON.stringify(name)});
    return g ? [...g.querySelectorAll('li.closed-line')].map((li) => ({ name: text(li.querySelector('.closed-name bdi')), word: text(li.querySelector('.state-word')).toLowerCase(),
      open: [...li.querySelectorAll('button')].map((b) => b.getAttribute('aria-label') + '=' + label(b)).join() })) : null; })()`));
  /** The ⋯ in a herdr terminal's chip row, or null. */
  const liveMenuButton = () => app.eval(`(() => { const b = document.querySelector('nav.provider-chips > button.item-menu'); if (!b) return null; const r = b.getBoundingClientRect();
    return { label: b.getAttribute('aria-label'), text: b.innerText.trim(), left: Math.round(r.left), right: Math.round(r.right), width: Math.round(r.width), height: Math.round(r.height) }; })()`);
  const openLiveMenu = async (name) => {
    await until(async () => (await liveMenuButton())?.label === `Menu for ${name}`, { what: `the menu button of ${name} in Live` });
    await app.eval(`document.querySelector('nav.provider-chips > button.item-menu').click()`);
    return until(() => sheetOf(ITEM), { what: `the menu of ${name} in Live` });
  };
  const livePath = () => app.eval('location.pathname');
  const liveTarget = async () => decodeURIComponent((await livePath()).split('/').pop());
  /** Waits for Live at a herdr terminal other than `not`, painted by the fake. */
  const liveAt = async (not = '') => {
    await until(async () => { const path = await livePath(); return path.startsWith('/p/herdr/') && path !== `/p/herdr/${enc(not)}`; }, { what: 'a herdr terminal in Live', timeout: 30000 });
    const target = await liveTarget();
    await liveMarker(paneOf(target));
    return target;
  };
  const banner = () => app.eval(`document.querySelector('.banner[role="status"] .banner-text')?.innerText.trim() ?? null`);
  /** Where the app is after it left Live: the address, which tab is pressed, and the "gone" message. */
  const landed = async () => ({ path: await livePath(), herdr: (await herdrChip())?.pressed, all: await app.eval(`[...document.querySelectorAll('.chips .chip')].find((e) => e.innerText.trim() === 'All')?.getAttribute('aria-pressed') ?? null`),
    tree: await app.eval(`document.querySelector('.provider-tree') !== null`), gone: (await app.text()).includes('That terminal is gone.') });
  const onHerdrTab = (now) => now.path === '/' && now.herdr === 'true' && now.all === 'false' && now.tree && !now.gone;
  const manageRequests = (mark) => env.logSince(mark).filter((e) => e.host === mac.host && e.method === 'POST' && e.path === '/api/v1/providers/herdr/manage');
  /** A picture for the reviewer: 390×844 at 2×, so the file stays small. */
  const review = async (name) => {
    if (!REVIEW_SHOTS) return;
    mkdirSync(REVIEW_SHOTS, { recursive: true });
    await app.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
    await sleep(400);
    await app.shot(join(REVIEW_SHOTS, `${name}.png`));
    await app.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  };

  await step('H-plus-git', '+ on a git project that herdr reports without worktree info; none outside git; a worktree from it', async () => {
    // The host allows 3 new worktrees a minute for one device; H-create used them.
    await sleep(61000);
    await backToTab();
    const before = await providerGroups();
    const blog = before.find((g) => g.name === 'blog');
    const notes = before.find((g) => g.name === 'notes');
    const plus = await app.eval(`[...document.querySelectorAll('.group-add')].map((b) => b.getAttribute('aria-label'))`);
    const raw = workspaceIn('blog');
    const mark = mac.herdrLog().length;
    await click(app, 'New worktree in blog');
    await app.waitText('New worktree');
    await setInput(app, '#create-name', 'try-idea');
    await click(app, 'No agent');
    await click(app, 'Create');
    await until(() => app.eval(`location.pathname.startsWith('/p/herdr/')`), { what: 'opens the new worktree', timeout: 45000 });
    const target = decodeURIComponent((await app.eval('location.pathname')).split('/').pop());
    await liveMarker(paneOf(target));
    await shot('h-plus-git-live');
    await backToTab();
    // The group's ID changes with its first worktree (ws-<id> → g<hash>): found by name.
    const row = await until(async () => (await unfold(), await groups()).find((g) => g.name === 'blog' && g.items.some((i) => i.name === 'try-idea')), { what: 'try-idea under blog' });
    await shot('h-plus-git');
    const after = (await providerGroups()).find((g) => g.name === 'blog');
    check('H-plus-git', 'blog (a git repository; the fake reports no worktree for it) has +, notes (outside git) has none; + makes the worktree try-idea, opens it and lists it under blog',
      raw.worktree === undefined && blog?.canCreate === true && notes?.canCreate === false && plus.includes('New worktree in blog') && !plus.includes('New worktree in notes')
        && logged(mark, 'worktree.create ').join() === `worktree.create ${raw.workspace_id} try-idea` && JSON.stringify(row.items.map((i) => i.name)) === '["blog","try-idea"]'
        && after.items.find((i) => i.name === 'try-idea')?.linked === true && after.items.find((i) => i.name === 'blog')?.linked === undefined,
      `fake snapshot: blog worktree ${JSON.stringify(raw.worktree)}; host: blog canCreate ${blog?.canCreate} (id ${blog?.id} → ${after?.id}), notes canCreate ${notes?.canCreate}; + buttons ${JSON.stringify(plus)}; fake: ${logged(mark, 'worktree.create ').join(' | ')}; rows ${JSON.stringify(row.items.map((i) => i.name))}`);
  });

  await step('H-menu', '⋯ opens the item menu: New tab and Close workspace; Remove worktree only for a linked worktree; a main checkout with an open worktree cannot close', async () => {
    await backToTab();
    const linked = await openMenu('silver-meadow');
    await shot('h-menu-linked');
    await closeSheet(ITEM);
    const plain = await openMenu('notes');
    await closeSheet(ITEM);
    const main = await openMenu('shop');
    await shot('h-menu-main');
    await closeSheet(ITEM);
    check('H-menu', 'silver-meadow (linked worktree): New tab, Close workspace, Remove worktree; notes: New tab and Close workspace only; shop (main checkout with open worktrees): Close workspace is off with "Close its worktrees first."',
      JSON.stringify(labels(linked)) === '["New tab","Close workspace","Remove worktree"]' && linked.title === 'silver-meadow' && linked.first === null
        && JSON.stringify(labels(plain)) === '["New tab","Close workspace"]' && plain.first === null
        && JSON.stringify(labels(main)) === '["New tab","Close workspace (off)"]' && main.first === 'Close its worktrees first.',
      `silver-meadow ${JSON.stringify(labels(linked))}; notes ${JSON.stringify(labels(plain))}; shop ${JSON.stringify(labels(main))} "${main.first}"`);
  });

  await step('H-manage-fit', 'The item menu, its confirmations, New workspace and the Integrations cards fit 375 and 390 px', async () => {
    const seen = {};
    for (const width of [390, 375]) {
      await app.send('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 3, mobile: true });
      await backToTab();
      await openMenu('silver-meadow');
      seen[`menu${width}`] = await overflow();
      await sheetTap(ITEM, '.menu-list', 'Close workspace');
      seen[`close${width}`] = await overflow();
      await shot(`h-manage-fit-close-${width}`);
      await sheetTap(ITEM, '[role="alertdialog"]', 'Cancel');
      await sheetTap(ITEM, '.menu-list', 'Remove worktree');
      seen[`remove${width}`] = await overflow();
      await shot(`h-manage-fit-remove-${width}`);
      await sheetTap(ITEM, '[role="alertdialog"]', 'Cancel');
      await closeSheet(ITEM);
      await openMenu('shop');
      seen[`tabs${width}`] = await overflow();
      await shot(`h-manage-fit-tabs-${width}`);
      await sheetTap(ITEM, '.tab-rows li:last-child', 'Close tab');
      seen[`tab${width}`] = await overflow();
      await sheetTap(ITEM, '[role="alertdialog"]', 'Cancel');
      await closeSheet(ITEM);
      await openWorkspaceSheet();
      seen[`workspace${width}`] = await overflow();
      await shot(`h-manage-fit-workspace-${width}`);
      await closeSheet(WORKSPACE);
      await inApp('/settings');
      await until(async () => (await integrationCard('vps'))?.buttons.includes('Install herdr') && await integrationCard('mac'), { what: 'the Integrations cards', timeout: 20000 });
      await app.eval(`document.querySelector('#integrations-heading').scrollIntoView()`);
      seen[`cards${width}`] = await overflow();
      await shot(`h-manage-fit-cards-${width}`);
    }
    await app.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    await inApp('/');
    check('H-manage-fit', 'Nothing wider than 375 or 390 px: the item menu with its Tabs list, the Close workspace, Remove worktree and Close tab confirmations, the New workspace sheet, and Integrations with the "Not installed" card and Install herdr',
      Object.keys(seen).length === 14 && Object.values(seen).every((list) => list.length === 0), JSON.stringify(seen));
  });

  await step('H-closetab', 'Close tab: the Tabs list, the confirmation, tab.close for that tab; never the last tab', async () => {
    await backToTab();
    const mark = mac.herdrLog().length;
    const target = t['shop/logs'];
    const tabId = mac.herdrState().panes.find((p) => p.pane_id === paneOf(target)).tab_id;
    const one = await openMenu('lucky-valley');
    await closeSheet(ITEM);
    const two = await openMenu('shop');
    await sheetTap(ITEM, '.tab-rows li:last-child', 'Close tab');
    const asked = await sheetOf(ITEM);
    await shot('h-closetab-confirm');
    await confirmTap('Close tab');
    const confirmed = Date.now();
    await until(async () => (await sheetOf(ITEM)) === null && (await unfold(), await groups()).find((g) => g.name === 'shop')?.items.find((i) => i.name === 'shop')?.meta === 'claude · tab 1',
      { what: 'shop with one tab in the list', timeout: 10000, every: 100 });
    const gone = Date.now() - confirmed;
    time('Close tab confirmed → gone from the list', gone);
    const after = await openMenu('shop');
    await shot('h-closetab-last');
    await closeSheet(ITEM);
    const shop = await itemOf('shop');
    const lucky = await itemOf('lucky-valley');
    const last = await manageApi({ action: 'closeTab', itemId: lucky.id, targetId: lucky.targets[0].id });
    const foreign = await manageApi({ action: 'closeTab', itemId: lucky.id, targetId: shop.targets[0].id });
    const code = (r) => `${r.status} ${r.json?.error?.code ?? ''}`.trim();
    check('H-closetab', 'shop has two tabs: the Tabs list shows "1 · claude" and "logs", each with Close tab; Close tab on logs asks "Close tab logs? Its terminals end."; confirming makes one tab.close of that tab, and shop shows one tab within 10 s; an item with one terminal shows "Close the workspace to end its last tab." and no Close tab; the API refuses the last tab (409) and a terminal of another item (404)',
      JSON.stringify(two.tabs) === '[{"name":"1 · claude","close":"Close tab"},{"name":"logs","close":"Close tab"}]' && two.tabHint === null
        && asked.confirm === 'Close tab logs? Its terminals end.' && JSON.stringify(asked.confirmButtons) === '["Cancel","Close tab"]'
        && logged(mark, 'tab.close').join() === `tab.close ${tabId}` && gone <= 10000 && !shop.targets.some((x) => x.id === target) && shop.targets.length === 1
        && JSON.stringify(one.tabs) === '[{"name":"1 · claude","close":""}]' && one.tabHint === 'Close the workspace to end its last tab.'
        && JSON.stringify(after.tabs) === '[{"name":"1 · claude","close":""}]' && after.tabHint === 'Close the workspace to end its last tab.'
        && code(last) === '409 operation_conflict' && code(foreign) === '404 session_missing' && workspaceIn('lucky-valley') !== undefined && workspaceIn('shop') !== undefined,
      `shop tabs ${JSON.stringify(two.tabs)}; confirmation "${asked.confirm}" ${JSON.stringify(asked.confirmButtons)}; fake: ${logged(mark, 'tab.close').join(' | ')}; gone after ${gone} ms; lucky-valley tabs ${JSON.stringify(one.tabs)} "${one.tabHint}"; last tab ${code(last)} "${last.json?.error?.message}"; another item's terminal ${code(foreign)}`);
  });

  let newTab;
  await step('H-newtab', 'New tab → Shell makes a plain tab in the workspace and opens it in Live', async () => {
    await backToTab();
    const id = workspaceIn('notes').workspace_id;
    const mark = mac.herdrLog().length;
    await openMenu('notes');
    // A second tap that lands on Shell as the chooser shows (within 400 ms) does nothing.
    const early = await app.eval(`(async () => { ${LABEL}
      const d = document.querySelector('[role="dialog"][aria-labelledby="${ITEM}"]');
      [...d.querySelectorAll('.menu-list button')].find((b) => label(b) === 'New tab').click();
      const start = performance.now(); let b;
      while (!(b = [...d.querySelectorAll('.new-tab .kind-chips button')].find((e) => label(e) === 'Shell')) && performance.now() - start < 2000) await new Promise((r) => setTimeout(r, 10));
      if (!b) return null; b.click(); return Math.round(performance.now() - start); })()`);
    await sleep(1500);
    const chooser = (await sheetOf(ITEM))?.chooser;
    const ignored = early !== null && early <= 150 && logged(mark, 'tab.create ').length === 0 && !(await livePath()).startsWith('/p/');
    const started = Date.now();
    await sheetTap(ITEM, '.kind-chips', 'Shell');
    await until(() => app.eval(`location.pathname.startsWith('/p/herdr/')`), { what: 'opens the new tab', timeout: 20000 });
    const target = decodeURIComponent((await app.eval('location.pathname')).split('/').pop());
    const pane = paneOf(target);
    await liveMarker(pane);
    time('New tab → its terminal in Live', Date.now() - started);
    await attachedSince(mac, pane, mark);
    await shot('h-newtab-live');
    const tabs = mac.herdrState().tabs.filter((tab) => tab.workspace_id === id).length;
    const item = await itemOf('notes');
    newTab = { target, pane, id };
    check('H-newtab', 'New tab on notes shows the chooser; a tap on Shell as it shows is ignored; Shell then makes one tab.create in the fake and no agent, which now has 2 tabs there; Live opens the new terminal (the fake attach of its pane); the list has both',
      chooser?.kinds[0]?.text === 'Shell' && ignored && logged(mark, 'tab.create ').join() === `tab.create ${id}` && logged(mark, 'agent.').length === 0
        && tabs === 2 && pane.startsWith(`${id}:`) && pane !== paneOf(t['notes/1']) && item.targets.some((x) => x.id === target && x.agent === undefined),
      `chooser ${JSON.stringify(chooser?.kinds.map((k) => k.text))}; Shell tap ${early} ms after it showed: ignored ${ignored}; fake: ${logged(mark, 'tab.create ').join(' | ')}, agent calls ${logged(mark, 'agent.').length}; ${tabs} tabs in ${id}; Live ${await app.eval('location.pathname')}; notes targets ${JSON.stringify(item.targets.map((x) => x.id))}`);
  });

  await step('H-close', 'Close workspace asks first; Cancel changes nothing; confirming closes it and its open terminal ends', async () => {
    if (newTab === undefined) throw new Error('H-newtab did not open a terminal');
    const { target, pane, id } = newTab;
    // The new tab is open in Live. Leave it: its attach would end by itself only 9 s or more later (H-end).
    const mark = mac.herdrLog().length;
    const left = Date.now();
    await backToTab();
    await openMenu('notes');
    // A second tap that lands on the danger button as the confirmation shows (within 400 ms) does nothing.
    const early = await app.eval(`(async () => { ${LABEL}
      const d = document.querySelector('[role="dialog"][aria-labelledby="${ITEM}"]');
      [...d.querySelectorAll('.menu-list button')].find((b) => label(b) === 'Close workspace').click();
      const start = performance.now(); let b = null;
      while (!(b = d.querySelector('.confirm-pair > button.danger')) && performance.now() - start < 2000) await new Promise((r) => setTimeout(r, 10));
      if (!b) return null; b.click(); return Math.round(performance.now() - start); })()`);
    await sleep(1500);
    const asked = await sheetOf(ITEM);
    const ignored = early !== null && early <= 150 && logged(mark, 'workspace.close').length === 0 && workspaceIn('notes') !== undefined && asked?.confirm !== null;
    await shot('h-close-confirm');
    await sheetTap(ITEM, '[role="alertdialog"]', 'Cancel');
    const cancelled = await sheetOf(ITEM);
    const untouched = logged(mark, 'workspace.close').length === 0 && workspaceIn('notes') !== undefined && exits(mac, pane).length === 0;
    await sheetTap(ITEM, '.menu-list', 'Close workspace');
    await confirmTap('Close workspace');
    const confirmed = Date.now();
    await until(async () => (await sheetOf(ITEM)) === null && !(await rowNames()).includes('notes'), { what: 'notes gone from the list', timeout: 10000, every: 100 });
    const gone = Date.now() - confirmed;
    time('Close workspace confirmed → gone from the list', gone);
    const exit = await exitedSince(mac, pane, mark, 8000);
    const sinceLeft = Date.now() - left;
    const again = await openApi(mac, target);
    await shot('h-close-gone');
    check('H-close', 'The app asks "Close notes? Its terminals end. Files stay." with Cancel first; a tap on the danger button as the confirmation shows is ignored; Cancel: the menu is back, the fake closed nothing; Close workspace (after 600 ms): one workspace.close, notes leaves the list within 10 s, the open terminal\'s attach ends (before the 10 s rule could end it) and its target is 404',
      asked.confirm === 'Close notes? Its terminals end. Files stay.' && JSON.stringify(asked.confirmButtons) === '["Cancel","Close workspace"]' && ignored
        && cancelled.confirm === null && cancelled.buttons.length === 2 && untouched
        && logged(mark, 'workspace.close').join() === `workspace.close ${id}` && workspaceIn('notes') === undefined && gone <= 10000
        && sinceLeft < 9000 && again.status === 404 && !(await providerGroups()).some((g) => g.name === 'notes'),
      `confirmation "${asked.confirm}" ${JSON.stringify(asked.confirmButtons)}; danger tap ${early} ms after it showed: ignored ${ignored}; after Cancel: menu ${JSON.stringify(labels(cancelled))}, nothing closed ${untouched}; fake: ${logged(mark, 'workspace.close').join(' | ')}; gone after ${gone} ms; "${exit}" ${sinceLeft} ms after Live closed; open again ${again.status} ${again.json?.error?.code}`);
  });

  await step('H-remove', 'Remove worktree: changes that are not committed stop it; a clean worktree is removed', async () => {
    await backToTab();
    const mark = mac.herdrLog().length;
    const dirtyId = workspaceIn('lucky-valley').workspace_id;
    const cleanId = workspaceIn('silver-meadow').workspace_id;
    await openMenu('lucky-valley');
    await sheetTap(ITEM, '.menu-list', 'Remove worktree');
    const asked = await sheetOf(ITEM);
    await shot('h-remove-confirm');
    await confirmTap('Remove worktree');
    const refused = await until(async () => { const now = await sheetOf(ITEM); return now?.notes.length > 0 && now.confirm === null && now; }, { what: 'the answer for lucky-valley', timeout: 15000 });
    await shot('h-remove-uncommitted');
    const kept = workspaceIn('lucky-valley') !== undefined && logged(mark, 'worktree.remove').length === 0 && (await rowNames()).includes('lucky-valley');
    await closeSheet(ITEM);
    await openMenu('silver-meadow');
    await sheetTap(ITEM, '.menu-list', 'Remove worktree');
    const askedClean = await sheetOf(ITEM);
    await confirmTap('Remove worktree');
    const confirmed = Date.now();
    await until(async () => (await sheetOf(ITEM)) === null && !(await rowNames()).includes('silver-meadow'), { what: 'silver-meadow gone from the list', timeout: 10000, every: 100 });
    const gone = Date.now() - confirmed;
    time('Remove worktree confirmed → gone from the list', gone);
    await shot('h-remove-gone');
    const text = (name) => `Remove the worktree ${name}? Its folder is deleted, also files that git ignores (such as .env). The branch stays.`;
    check('H-remove', 'lucky-valley (changes not committed in the scenario): the confirmation, then "lucky-valley has changes that are not committed. Commit or discard them first.", nothing removed; silver-meadow (clean): worktree.remove without force, gone from the list within 10 s',
      asked.confirm === text('lucky-valley') && JSON.stringify(asked.confirmButtons) === '["Cancel","Remove worktree"]'
        && JSON.stringify(refused.notes) === '["lucky-valley has changes that are not committed. Commit or discard them first."]' && kept
        && askedClean.confirm === text('silver-meadow') && logged(mark, 'worktree.remove').join() === `worktree.remove ${cleanId} force=false`
        && workspaceIn('silver-meadow') === undefined && workspaceIn('lucky-valley')?.workspace_id === dirtyId && gone <= 10000 && (await itemOf('silver-meadow')) === undefined,
      `confirmation "${asked.confirm}"; lucky-valley (${dirtyId}): ${JSON.stringify(refused.notes)}, kept ${kept}; silver-meadow: fake ${logged(mark, 'worktree.remove').join(' | ')}; gone after ${gone} ms`);
  });

  await step('H-stale', 'A manage request for an item that changed answers 409 and changes nothing; the API refusals', async () => {
    await backToTab();
    const acme = await itemOf('acme-web');
    const mark = mac.herdrLog().length;
    // The name the phone confirmed is not the item's name now.
    const wrong = await manageApi({ action: 'closeItem', itemId: acme.id, name: 'acme-web-old' });
    const wrongRemove = await manageApi({ action: 'removeItem', itemId: (await itemOf('fix-login')).id, name: 'fix-logout' });
    // In the app: the confirmation is open while the project gets a worktree; the main checkout may not close now.
    await openMenu('acme-web');
    await sheetTap(ITEM, '.menu-list', 'Close workspace');
    const late = await api(tool, `${mac.origin}/api/v1/providers/herdr/create`, 'POST', { groupId: acme.group.id, name: 'late-tree' });
    if (late.status !== 200) throw new Error(`late worktree in acme-web: ${late.status} ${late.body}`);
    await confirmTap('Close workspace');
    const said = await until(async () => { const now = await sheetOf(ITEM); return now?.notes.length > 0 && now; }, { what: 'the answer for acme-web', timeout: 15000 });
    await shot('h-stale');
    await closeSheet(ITEM);
    const listed = await until(async () => (await unfold(), await groups()).find((g) => g.name === 'acme-web' && g.items.some((i) => i.name === 'late-tree')), { what: 'the list read again: late-tree under acme-web', timeout: 10000 });
    const kept = logged(mark, 'workspace.close').length === 0 && logged(mark, 'worktree.remove').length === 0 && workspaceIn('acme-web') !== undefined && workspaceIn('fix-login') !== undefined;
    check('H-stale', 'closeItem and removeItem with a name that is not the item\'s: 409 operation_conflict; in the app, Close workspace on acme-web after a worktree opened in it: "The list changed. Look again, then try again." and the list is read again; the fake closed and removed nothing',
      wrong.status === 409 && wrong.json?.error?.code === 'operation_conflict' && wrongRemove.status === 409 && wrongRemove.json?.error?.code === 'operation_conflict'
        && late.status === 200 && JSON.stringify(said.notes) === JSON.stringify([CHANGED]) && said.buttons.length === 0 && said.confirm === null && kept,
      `wrong name: close ${wrong.status} ${wrong.json?.error?.code} "${wrong.json?.error?.message}", remove ${wrongRemove.status} ${wrongRemove.json?.error?.code}; late worktree ${late.status}; app ${JSON.stringify(said.notes)}; rows ${JSON.stringify(listed.items.map((i) => i.name))}; nothing closed or removed ${kept}`);

    const body = JSON.stringify({ action: 'newTab', itemId: acme.id });
    const headers = { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' };
    const refusals = logged(0, 'tab.create ').length;
    const noCookie = await env.curl(manageUrl, { method: 'POST', headers: { ...headers, Origin: mac.origin }, body });
    const evil = await env.curl(manageUrl, { method: 'POST', headers: { ...headers, Cookie: await jarOf(mac), Origin: 'https://evil.tailx.ts.net' }, body });
    const noHeader = await env.curl(manageUrl, { method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: await jarOf(mac), Origin: mac.origin }, body });
    const field = await manageApi({ action: 'newTab', itemId: acme.id, name: 'acme-web' });
    const start = await manageApi({ action: 'startServer', itemId: acme.id });
    const running = await manageApi({ action: 'startServer' });
    const mainRemove = await manageApi({ action: 'removeItem', itemId: acme.id, name: 'acme-web' });
    const unknown = await manageApi({ action: 'newTab', itemId: 'w99' });
    const unknownClose = await manageApi({ action: 'closeItem', itemId: 'w99', name: 'acme-web' });
    const project = await manageApi({ action: 'newWorkspace', projectId: crypto.randomUUID() });
    const code = (r) => `${r.status} ${r.json?.error?.code ?? ''}`.trim();
    check('H-manage-auth', 'POST …/manage: no cookie 401, Origin evil 403, no X-Pterm-Request refused; a field that does not belong to the action 400; startServer while herdr runs 400; removeItem on a main checkout 400; an unknown item 404; an unknown project 404; the fake got no call',
      noCookie.status === 401 && evil.status === 403 && noHeader.status >= 400 && noHeader.status < 500
        && code(field) === '400 invalid_request' && code(start) === '400 invalid_request' && code(running) === '400 invalid_request' && code(mainRemove) === '400 invalid_request'
        && code(unknown) === '404 session_missing' && code(unknownClose) === '404 session_missing' && code(project) === '404 project_missing'
        && logged(0, 'tab.create ').length === refusals && logged(mark, 'workspace.c').length === 0 && logged(mark, 'worktree.remove').length === 0 && logged(mark, 'server start').length === 0,
      `no cookie ${noCookie.status}; evil ${evil.status}; no X-Pterm-Request ${noHeader.status}; newTab with name ${code(field)}; startServer with itemId ${code(start)}; startServer while ready ${code(running)} "${running.json?.error?.message}"; removeItem main checkout ${code(mainRemove)} "${mainRemove.json?.error?.message}"; unknown item ${code(unknown)} / ${code(unknownClose)}; unknown project ${code(project)}`);
  });

  // ---- section 14 (protocol revision 6): an agent in a new tab, closed worktrees, the menu in a herdr terminal ----
  const code = (r) => `${r.status} ${r.json?.error?.code ?? ''}`.trim();
  const PROMPT = 'fix the login test';

  await step('H-agent-tab', 'New tab shows Shell and the agent kinds; an agent with a prompt makes one tab and starts it there; what did not start is said over the terminal', async () => {
    // A tab with an agent takes one of the device's 3 creates a minute; H-stale used one. This step uses 3.
    await sleep(61000);
    await backToTab();
    const kinds = (await api(tool, `${mac.origin}/api/v1/settings`)).json.providers[0].agentKinds;
    const shop = await itemOf('shop');
    await openMenu('shop');
    const first = await openChooser();
    await shot('h-agent-tab-chooser');
    await review('new-tab-chooser');
    await sheetTap(ITEM, '.kind-chips', 'codex');
    const picked = (await sheetOf(ITEM)).chooser;
    // An ESC in the prompt: the app says so and sends nothing.
    const frontMark = env.logMark();
    const mark = mac.herdrLog().length;
    await setInput(app, '#create-prompt', 'hi\u001b[31m');
    const refused = (await sheetOf(ITEM)).chooser;
    await app.eval(`(() => { const b = document.querySelector('.new-tab button.primary'); b.click(); b.click(); return true; })()`);
    await sleep(1500);
    const sent = manageRequests(frontMark).length + logged(mark, 'tab.create ').length;
    await setInput(app, '#create-prompt', PROMPT);
    const ready = (await sheetOf(ITEM)).chooser;
    await shot('h-agent-tab-prompt');
    await review('new-tab-agent-prompt');
    const started = Date.now();
    // Two taps in one go: one tab.
    await app.eval(`(() => { const b = document.querySelector('.new-tab button.primary'); b.click(); b.click(); return true; })()`);
    const target = await liveAt();
    const pane = paneOf(target);
    time('New tab with codex and a prompt → its terminal in Live', Date.now() - started);
    await attachedSince(mac, pane, mark);
    const said = await banner();
    await shot('h-agent-tab-live');
    await review('agent-tab-live');
    const requests = manageRequests(frontMark);
    const made = logged(mark, 'tab.create ');
    const starts = logged(mark, 'agent.start ');
    const prompts = logged(mark, 'agent.prompt ');
    const now = await until(async () => { const item = await itemOf('shop'); return item?.targets.some((x) => x.id === target && x.agent === 'codex') && item; }, { what: 'the codex tab in the list', timeout: 10000 });
    check('H-agent-tab', 'New tab on shop shows Shell and mac\'s 5 agent kinds, no prompt field and no Start; codex picked: First prompt and Start; an ESC in the prompt turns Start off with the create sheet\'s words and sends no request; Start with a prompt (two taps) sends one request, makes one tab.create, agent.start codex in the new pane and agent.prompt with the text; Live opens that terminal with no notice; the list has the tab with codex',
      first.kinds[0].text === 'Shell' && JSON.stringify(first.kinds.slice(1).map((k) => k.text).sort()) === JSON.stringify([...kinds].sort()) && kinds.length === 5 && !first.prompt && JSON.stringify(first.pair.map((b) => b.text)) === '["Cancel"]'
        && first.kinds.slice(1).every((k) => k.pressed === 'false') && picked.kinds.find((k) => k.text === 'codex').pressed === 'true' && picked.kinds.filter((k) => k.pressed === 'true').length === 1
        && picked.prompt && JSON.stringify(picked.pair.map((b) => b.text)) === '["Cancel","Start"]' && picked.pair[1].primary && !picked.pair[1].disabled
        && /control characters/.test(refused.help ?? '') && refused.pair[1].disabled && sent === 0 && ready.help === null && !ready.pair[1].disabled
        && requests.length === 1 && requests[0].status === 200 && made.join() === `tab.create ${shop.id}` && pane.startsWith(`${shop.id}:`)
        && starts.length === 1 && new RegExp(`^agent\\.start codex \\S+ ${pane}$`).test(starts[0]) && prompts.join() === `agent.prompt ${pane} ${JSON.stringify(PROMPT)}`
        && said === null && now.targets.length === shop.targets.length + 1,
      `chooser ${JSON.stringify(first.kinds.map((k) => k.text))} (agentKinds ${JSON.stringify(kinds)}), buttons ${JSON.stringify(first.pair.map((b) => b.text))}; codex picked: prompt field ${picked.prompt}, ${JSON.stringify(picked.pair.map((b) => b.text))}; ESC: "${refused.help}", Start off ${refused.pair[1].disabled}, requests and tabs ${sent}; requests ${requests.length} (${requests.map((r) => r.status).join()}); fake: ${[...made, ...starts, ...prompts].join(' | ')}; Live ${await livePath()}; notice ${JSON.stringify(said)}`);

    // lab's next pane is w8:p5, and an agent has the name the host gives an amp agent there: agent.start fails.
    await backToTab();
    const lab = await itemOf('lab');
    const failMark = mac.herdrLog().length;
    await openMenu('lab');
    await openChooser();
    await sheetTap(ITEM, '.kind-chips', 'amp');
    await setInput(app, '#create-prompt', 'say hello');
    await sheetTap(ITEM, '.confirm-pair', 'Start');
    const failTarget = await liveAt();
    const notice = await until(() => banner(), { what: 'the notice over the terminal', timeout: 10000 });
    await shot('h-agent-tab-notice');
    // Its own Dismiss: Live may show a notice of its own under it.
    await app.eval(`document.querySelector('.banner[role="status"] .banner-actions button').click()`);
    await sleep(300);
    const dismissed = await banner();
    const labNow = await until(async () => { const item = await itemOf('lab'); return item?.targets.some((x) => x.id === failTarget) && item; }, { what: 'the new tab of lab in the list', timeout: 10000 });
    check('H-agent-tab-notice', 'New tab with amp on lab, where herdr refuses the agent (its name is taken): one tab.create, no agent started, no prompt sent; Live opens the tab with "The tab is ready, but the agent did not start." once (Dismiss takes it away); the tab is in the list with no agent',
      logged(failMark, 'tab.create ').join() === `tab.create ${lab.id}` && paneOf(failTarget) === `${lab.id}:p5` && logged(failMark, 'agent.').length === 0
        && notice === 'The tab is ready, but the agent did not start.' && dismissed === null && labNow.targets.find((x) => x.id === failTarget).agent === undefined,
      `fake: ${logged(failMark, 'tab.create ').join(' | ')}, agent calls ${logged(failMark, 'agent.').length}; Live ${await livePath()}; notice "${notice}", after Dismiss ${JSON.stringify(dismissed)}; lab targets ${JSON.stringify(labNow.targets.map((x) => `${x.id}${x.agent ? ` ${x.agent}` : ''}`))}`);

    // Straight to the host: what the app never sends.
    await inApp('/');
    const apiMark = mac.herdrLog().length;
    const esc = await manageApi({ action: 'newTab', itemId: shop.id, agent: 'codex', prompt: 'hi\u001b[31m' });
    const noAgent = await manageApi({ action: 'newTab', itemId: shop.id, prompt: PROMPT });
    const onClose = await manageApi({ action: 'closeItem', itemId: shop.id, name: 'shop', agent: 'codex' });
    const onCloseTab = await manageApi({ action: 'closeTab', itemId: shop.id, targetId: target, agent: 'codex' });
    const badKind = await manageApi({ action: 'newTab', itemId: shop.id, agent: 'Bad Kind!' });
    // A kind herdr can start (the pattern passes) that mac does not offer: not in agentKinds.
    const notOffered = await manageApi({ action: 'newTab', itemId: shop.id, agent: 'aider' });
    const calls = logged(apiMark, '').filter((l) => !l.startsWith('attach') && !l.startsWith('input'));
    check('H-agent-tab-errors', 'POST …/manage: newTab with an ESC in the prompt 400; a prompt without an agent 400; an agent on closeItem and on closeTab 400; an agent that is not a kind name 400; a kind that mac does not offer 400 invalid_request; the fake got no call',
      [esc, noAgent, onClose, onCloseTab, badKind, notOffered].every((r) => code(r) === '400 invalid_request') && calls.length === 0 && workspaceIn('shop') !== undefined,
      `ESC prompt ${code(esc)}; prompt without agent ${code(noAgent)}; agent on closeItem ${code(onClose)}, on closeTab ${code(onCloseTab)}; bad kind ${code(badKind)}; kind not offered ${code(notOffered)} "${notOffered.json?.error?.message}"; fake calls ${calls.length}`);
  });

  await step('H-closed', 'A closed worktree shows under its project as CLOSED with Open; Open opens it again; a removed worktree does not come back', async () => {
    await inApp('/');
    await settle();
    await backToTab();
    const main = workspaceIn('shop').workspace_id;
    const before = { rows: await closedRows('shop'), chip: (await herdrChip()).count, line: await statusLine() };
    const mark = mac.herdrLog().length;
    await openMenu('fix-login');
    await sheetTap(ITEM, '.menu-list', 'Close workspace');
    await confirmTap('Close workspace');
    const confirmed = Date.now();
    const rows = await until(async () => { const list = await closedRows('shop'); return list?.some((row) => row.name === 'fix-login') && list; }, { what: 'fix-login as a closed row under shop', timeout: 15000, every: 100 });
    const ms = Date.now() - confirmed;
    time('Close workspace on a linked worktree → its CLOSED row', ms);
    const items = (await groups()).find((g) => g.name === 'shop').items.map((i) => i.name);
    const after = { chip: (await herdrChip()).count, line: await statusLine() };
    await shot('h-closed-row');
    // The picture: shop and its closed rows, with the long project under it folded.
    await toggleGroup('storefront-redesign');
    await review('closed-worktree-row');
    await toggleGroup('storefront-redesign');
    // Folded, a project counts its worktrees: the closed ones are not in that count.
    await toggleGroup('shop');
    const folded = (await groups()).find((g) => g.name === 'shop');
    await toggleGroup('shop');
    const listed = (await providerGroups()).find((g) => g.name === 'shop');
    const row = (name) => ({ name, word: 'closed', open: `Open ${name}=Open` });
    check('H-closed', 'shop has the closed worktree old-idea (the scenario); Close workspace on fix-login (a linked worktree): within 15 s shop shows fix-login as a CLOSED row with Open, after its items; the closed rows are no items, and the status line, the tab count and the folded project\'s worktree count do not count them',
      JSON.stringify(before.rows) === JSON.stringify([row('old-idea')]) && logged(mark, 'workspace.close ').length === 1 && ms <= 15000
        && JSON.stringify(rows) === JSON.stringify([row('fix-login'), row('old-idea')]) && !items.includes('fix-login') && !items.includes('old-idea')
        && after.chip === before.chip && after.line === before.line && folded.open === 'false' && !folded.where.some((w) => w.includes('worktree'))
        && JSON.stringify(listed.closed) === '["fix-login","old-idea"]' && !listed.items.some((i) => listed.closed.includes(i.name)),
      `before ${JSON.stringify(before)}; closed row after ${ms} ms: ${JSON.stringify(rows)}; items ${JSON.stringify(items)}; tab count ${after.chip}, status line "${after.line}"; folded labels ${JSON.stringify(folded.where)}; API closed ${JSON.stringify(listed.closed)}`);

    const openMark = mac.herdrLog().length;
    const frontMark = env.logMark();
    const started = Date.now();
    // Two taps in one go: one open.
    await app.eval(`(() => { const b = document.querySelector('button[aria-label="Open fix-login"]'); b.click(); b.click(); return true; })()`);
    const target = await liveAt();
    time('Open on a closed worktree → its terminal in Live', Date.now() - started);
    await shot('h-closed-live');
    const workspace = workspaceIn('fix-login');
    await backToTab();
    const back = await until(async () => (await rowNames()).includes('fix-login') && await closedRows('shop'), { what: 'fix-login as a normal row again', timeout: 10000 });
    const item = await itemOf('fix-login');
    check('H-closed-open', 'Open on fix-login (two taps): one request, one worktree.open for that branch on shop\'s main workspace; Live opens its terminal; fix-login is a normal row again and only old-idea is closed',
      manageRequests(frontMark).length === 1 && logged(openMark, 'worktree.open ').join() === `worktree.open ${main} fix-login` && workspace !== undefined && paneOf(target).startsWith(`${workspace.workspace_id}:`)
        && JSON.stringify(back) === JSON.stringify([row('old-idea')]) && item?.linked === true && item.group.name === 'shop' && item.targets.some((x) => x.id === target),
      `requests ${manageRequests(frontMark).length}; fake: ${logged(openMark, 'worktree.open ').join(' | ')}; Live ${target}; closed rows now ${JSON.stringify(back)}; fix-login ${JSON.stringify({ id: item?.id, linked: item?.linked, group: item?.group.name })}`);

    // The host: only a name that herdr lists as closed right now.
    const shop = (await providerGroups()).find((g) => g.name === 'shop');
    const apiMark = mac.herdrLog().length;
    const notClosed = await manageApi({ action: 'openItem', groupId: shop.id, worktree: 'fix-login' });
    const never = await manageApi({ action: 'openItem', groupId: shop.id, worktree: 'never-made' });
    const badName = await manageApi({ action: 'openItem', groupId: shop.id, worktree: 'Bad Name!' });
    const path = await manageApi({ action: 'openItem', groupId: shop.id, worktree: '../old-idea' });
    const noName = await manageApi({ action: 'openItem', groupId: shop.id });
    const withItem = await manageApi({ action: 'openItem', groupId: shop.id, worktree: 'old-idea', itemId: main });
    const noGroup = await manageApi({ action: 'openItem', groupId: 'g0000000000000000', worktree: 'old-idea' });
    // Remove worktree on an open worktree: gone for good.
    await backToTab();
    const removeMark = mac.herdrLog().length;
    const removedId = workspaceIn('try-idea').workspace_id;
    await openMenu('try-idea');
    await sheetTap(ITEM, '.menu-list', 'Remove worktree');
    await confirmTap('Remove worktree');
    await until(async () => (await sheetOf(ITEM)) === null && !(await rowNames()).includes('try-idea'), { what: 'try-idea gone from the list', timeout: 10000, every: 100 });
    // More than the 10 s between two reads of a project's worktrees.
    await sleep(13000);
    const blog = (await providerGroups()).find((g) => g.name === 'blog');
    const shown = await app.eval(`[...document.querySelectorAll('li.closed-line .closed-name')].map((e) => e.innerText.trim())`);
    await shot('h-closed-removed');
    check('H-closed-errors', 'openItem for a worktree that is open (404), that never was (404) and in an unknown project (404); a bad name, a path, no name or a field of another action (400): no worktree.open in the fake. Remove worktree on try-idea (open): worktree.remove, and 13 s later it is not a closed row',
      code(notClosed) === '404 session_missing' && code(never) === '404 session_missing' && code(noGroup) === '404 session_missing'
        && [badName, path, noName, withItem].every((r) => code(r) === '400 invalid_request') && logged(apiMark, 'worktree.open').length === 0
        && logged(removeMark, 'worktree.remove ').join() === `worktree.remove ${removedId} force=false` && (blog.closed ?? []).length === 0 && !shown.includes('try-idea') && !(await rowNames()).includes('try-idea'),
      `open already ${code(notClosed)} "${notClosed.json?.error?.message}"; never made ${code(never)}; unknown project ${code(noGroup)}; bad name ${code(badName)}; path ${code(path)}; no name ${code(noName)}; with itemId ${code(withItem)}; fake: ${logged(removeMark, 'worktree.remove ').join(' | ')}; blog closed ${JSON.stringify(blog.closed ?? [])}; closed rows on the tab ${JSON.stringify(shown)}`);
  });

  await step('H-live-menu', 'A herdr terminal has the ⋯ of its workspace; what ends the terminal on screen goes back to the herdr tab with no message; so does ‹ Sessions', async () => {
    await inApp('/');
    await settle();
    await backToTab();
    const pick = (sheet) => JSON.stringify({ title: sheet.title, buttons: sheet.buttons, first: sheet.first, tabs: sheet.tabs, tabHint: sheet.tabHint });
    const fromTab = await openMenu('shop');
    await closeSheet(ITEM);
    // From the All tab, so the way back shows which tab the app picks.
    await click(app, 'All');
    await until(async () => (await herdrChip()).pressed === 'false', { what: 'the All tab' });
    await tapItem('shop');
    const first = await liveAt();
    const button = await until(() => liveMenuButton(), { what: 'the menu button in Live' });
    await shot('h-live-menu-button');
    await review('live-menu-button');
    const sheet = await openLiveMenu('shop');
    await shot('h-live-menu-sheet');
    await review('live-menu-sheet');
    // New tab → Shell from the terminal: Live moves to the new tab.
    const mark = mac.herdrLog().length;
    await openChooser();
    await shellTap();
    const target = await liveAt(first);
    const pane = paneOf(target);
    await attachedSince(mac, pane, mark);
    const tabId = mac.herdrState().panes.find((p) => p.pane_id === pane).tab_id;
    check('H-live-menu', 'Live for shop (opened from All) has ⋯ "Menu for shop" at the right of the chip row, inside the screen, 44 px; it opens the sheet the herdr tab opens (the same title, actions and Tabs list); New tab → Shell there makes one tab.create and Live shows the new tab\'s terminal',
      button.label === 'Menu for shop' && button.text === '⋯' && button.right <= 390 && button.width >= 44 && button.height >= 44 && pick(sheet) === pick(fromTab) && sheet.title === 'shop'
        && logged(mark, 'tab.create ').join() === `tab.create ${paneOf(first).split(':')[0]}` && logged(mark, 'agent.').length === 0 && target !== first && pane.startsWith(`${paneOf(first).split(':')[0]}:`),
      `button ${JSON.stringify(button)}; sheet in Live ${pick(sheet)}; on the tab ${pick(fromTab)}; fake: ${logged(mark, 'tab.create ').join(' | ')}; Live ${first} → ${target}`);

    // Close tab of the tab on screen.
    const closeMark = mac.herdrLog().length;
    await until(async () => (await itemOf('shop'))?.targets.some((x) => x.id === target), { what: 'the new tab in the list' });
    const menu = await until(async () => { const now = await openLiveMenu('shop'); if (now.tabs.length === (await itemOf('shop')).targets.length) return now; await closeSheet(ITEM); return false; }, { what: 'the menu with the new tab', timeout: 15000, every: 500 });
    const index = (await itemOf('shop')).targets.findIndex((x) => x.id === target);
    await sheetTap(ITEM, `.tab-rows li:nth-child(${index + 1})`, 'Close tab');
    const asked = await sheetOf(ITEM);
    await confirmTap('Close tab');
    const confirmed = Date.now();
    await until(async () => (await livePath()) === '/', { what: 'the app leaves the closed tab', timeout: 10000, every: 50 });
    const ms = Date.now() - confirmed;
    time('Close tab of the tab on screen → the herdr tab', ms);
    const atOnce = await landed();
    await sleep(3000);
    const later = await landed();
    await shot('h-live-menu-closed-tab');
    check('H-live-close-tab', 'Close tab of the tab on screen (from its ⋯): one tab.close of that tab; the app goes to the herdr tab (herdr pressed, not All) with no "That terminal is gone.", also 3 s later',
      menu.tabs.length >= 3 && /^Close tab .+\? Its terminals end\.$/.test(asked.confirm ?? '') && logged(closeMark, 'tab.close ').join() === `tab.close ${tabId}`
        && onHerdrTab(atOnce) && onHerdrTab(later) && (await sheetOf(ITEM)) === null,
      `tabs ${JSON.stringify(menu.tabs.map((x) => x.name))}; asked "${asked.confirm}"; fake: ${logged(closeMark, 'tab.close ').join(' | ')}; after ${ms} ms ${JSON.stringify(atOnce)}; 3 s later ${JSON.stringify(later)}`);

    // Close workspace of the terminal on screen: late-tree, a linked worktree of acme-web.
    const wsMark = mac.herdrLog().length;
    const lateId = workspaceIn('late-tree').workspace_id;
    await click(app, 'All');
    await herdrTab({ load: false });
    await tapItem('late-tree');
    await liveAt();
    await openLiveMenu('late-tree');
    await sheetTap(ITEM, '.menu-list', 'Close workspace');
    await confirmTap('Close workspace');
    await until(async () => (await livePath()) === '/', { what: 'the app leaves the closed workspace', timeout: 10000, every: 50 });
    const closedNow = await landed();
    await sleep(3000);
    const closedLater = await landed();
    // ‹ Sessions, from a terminal that All opened.
    await click(app, 'All');
    await until(async () => (await herdrChip()).pressed === 'false', { what: 'the All tab' });
    await tapItem('shop');
    await liveAt();
    await click(app, '‹ Sessions');
    await until(async () => (await livePath()) === '/', { what: 'Sessions' });
    const sessions = await landed();
    await shot('h-live-menu-sessions');
    check('H-live-leave', 'Close workspace from the ⋯ of late-tree\'s terminal: one workspace.close; the herdr tab with no "gone" message, also 3 s later. ‹ Sessions in a herdr terminal that the All tab opened: the herdr tab is selected, not All',
      logged(wsMark, 'workspace.close ').join() === `workspace.close ${lateId}` && onHerdrTab(closedNow) && onHerdrTab(closedLater) && onHerdrTab(sessions),
      `fake: ${logged(wsMark, 'workspace.close ').join(' | ')}; after Close workspace ${JSON.stringify(closedNow)}, 3 s later ${JSON.stringify(closedLater)}; after ‹ Sessions ${JSON.stringify(sessions)}`);
  });

  await step('H-round2-fit', 'The New tab chooser, a project with closed rows and the Live chip row with ⋯ fit 375 and 390 px', async () => {
    await inApp('/');
    await settle();
    const seen = {};
    const facts = {};
    for (const width of [390, 375]) {
      await app.send('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 3, mobile: true });
      await backToTab();
      // Every project unfolded: the closed rows show, one with a long name.
      const rows = [...await closedRows('shop'), ...await closedRows('storefront-redesign')];
      seen[`closed${width}`] = await overflow();
      await app.eval(`[...document.querySelectorAll('li.closed-line')].at(-1)?.scrollIntoView({ block: 'center' })`);
      await shot(`h-round2-fit-closed-${width}`);
      await openMenu('shop');
      const chooser = await openChooser();
      await sheetTap(ITEM, '.kind-chips', 'opencode');
      await setInput(app, '#create-prompt', PROMPT);
      seen[`chooser${width}`] = await overflow();
      await shot(`h-round2-fit-chooser-${width}`);
      await sheetTap(ITEM, '.confirm-pair', 'Cancel');
      await closeSheet(ITEM);
      await tapItem('shop');
      await liveAt();
      const button = await until(() => liveMenuButton(), { what: 'the menu button in Live' });
      seen[`live${width}`] = await overflow();
      await shot(`h-round2-fit-live-${width}`);
      facts[width] = { kinds: chooser.kinds.length, closed: rows.map((r) => r.name), button };
      await inApp('/');
    }
    await app.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    check('H-round2-fit', 'Nothing wider than 375 or 390 px: the herdr tab with closed rows (one with a 39-letter name), the New tab chooser with Shell, 5 agent kinds, the prompt field and Start, and a herdr terminal with ⋯ at the end of its chip row',
      Object.keys(seen).length === 6 && Object.values(seen).every((list) => list.length === 0)
        && [390, 375].every((w) => facts[w].kinds === 6 && facts[w].closed.includes('rework-checkout-address-form-validation') && facts[w].closed.includes('old-idea') && facts[w].button.right <= w && facts[w].button.width >= 44),
      `${JSON.stringify(seen)}; ${JSON.stringify(facts)}`);
    await inApp('/');
    await settle();
  });

  await step('H-workspace', 'New workspace lists the computer\'s Termivoa projects; a tap makes a workspace in that folder and opens it', async () => {
    await backToTab();
    const projects = (await api(tool, `${mac.origin}/api/v1/settings`)).json.projects.map((p) => p.name);
    const mark = mac.herdrLog().length;
    const sheet = await openWorkspaceSheet();
    await shot('h-workspace-sheet');
    const started = Date.now();
    await sheetTap(WORKSPACE, '.menu-list', 'demo');
    await until(() => app.eval(`location.pathname.startsWith('/p/herdr/')`), { what: 'opens the new workspace', timeout: 20000 });
    const target = decodeURIComponent((await app.eval('location.pathname')).split('/').pop());
    const pane = paneOf(target);
    await liveMarker(pane);
    time('New workspace → its terminal in Live', Date.now() - started);
    await attachedSince(mac, pane, mark);
    await shot('h-workspace-live');
    const folder = realpathSync(join(mac.home, 'demo'));
    const made = workspaceIn('demo');
    const cwd = mac.herdrState().panes.find((p) => p.pane_id === pane)?.cwd;
    await backToTab();
    const row = await until(async () => (await groups()).find((g) => g.name === 'demo'), { what: 'the demo row' });
    check('H-workspace', 'New workspace on mac lists mac\'s projects; a tap on demo makes one workspace "demo" in that project\'s folder (the fake got that cwd), opens its terminal in Live and lists it',
      JSON.stringify(sheet.buttons.map((b) => b.text)) === JSON.stringify(projects) && projects.includes('demo')
        && logged(mark, 'workspace.create ').join() === `workspace.create demo ${folder}` && cwd === folder && pane.startsWith(`${made?.workspace_id}:`) && row.where.includes('on mac'),
      `sheet ${JSON.stringify(sheet.buttons.map((b) => b.text))}, projects ${JSON.stringify(projects)}; fake: ${logged(mark, 'workspace.create ').join(' | ').replaceAll(env.root, '<root>')}; pane ${pane} cwd ${String(cwd).replaceAll(env.root, '<root>')}`);
  });

  // ---- Install: vps has no herdr yet (H-peer installs the fake there) ----
  await step('H-addfolder', 'New workspace → Add a folder opens a session with "pterm project add " typed, not run', async () => {
    await backToTab();
    const projects = (await api(tool, `${mac.origin}/api/v1/settings`)).json.projects.length;
    const before = (await api(tool, `${mac.origin}/api/v1/sessions`)).json.sessions.map((s) => s.id);
    const sheet = await openWorkspaceSheet();
    const text = await app.eval(`document.querySelector('[role="dialog"][aria-labelledby="workspace-title"]').innerText.replace(/\\s+/g, ' ')`);
    await shot('h-addfolder-sheet');
    await sheetTap(WORKSPACE, '.sheet-actions', 'Add a folder');
    const squash = (x) => x.replace(/\s+/g, '');
    await until(async () => squash(await terminalText(app)).includes('ptermprojectadd'), { what: 'the command in Live', timeout: 30000 });
    const path = await app.eval('location.pathname');
    // Long enough for a command that ran to print its usage error, or for a new prompt.
    await sleep(4000);
    const screen = await terminalText(app);
    await shot('h-addfolder-typed');
    const prompts = (screen.match(/ % /g) ?? []).length;
    const session = (await api(tool, `${mac.origin}/api/v1/sessions`)).json.sessions.filter((s) => !before.includes(s.id));
    const after = (await api(tool, `${mac.origin}/api/v1/settings`)).json.projects.length;
    for (const s of session) await api(tool, `${mac.origin}/api/v1/sessions/${s.id}/terminate`, 'POST', { operationId: crypto.randomUUID(), generation: s.generation });
    check('H-addfolder', 'The New workspace sheet has Add a folder and Check again and says what the button does; a tap opens one new session on mac in Live with "pterm project add " on the prompt line, not run (one prompt, no output, no new project)',
      text.includes('Add a folder') && text.includes('Check again')
        && text.includes('This opens a terminal on mac with pterm project add typed. Type the folder, press Enter, then tap Check again here.')
        && session.length === 1 && path.startsWith('/sessions/') && prompts === 1 && squash(screen).endsWith('ptermprojectadd') && after === projects,
      `sheet ${JSON.stringify(sheet.title)}; Live ${path}; screen ${JSON.stringify(screen.trim())} (${prompts} prompt); new sessions ${session.length}; projects ${projects} → ${after}`);
    await inApp('/');
  });

  await step('H-install-herdr', 'Settings → Integrations on a computer without herdr: Not installed, Install herdr opens a session with the command typed, not run', async () => {
    await backToTab();
    const tabGroups = await groups();
    const polled = env.logSince(0).filter((e) => e.host === vps.host && e.path?.startsWith('/api/v1/providers'));
    const info = (await api(tool, `${vps.origin}/api/v1/settings`)).json.providers;
    const before = (await api(tool, `${vps.origin}/api/v1/sessions`)).json.sessions.map((s) => s.id);
    await inApp('/settings');
    const card = await until(async () => { const c = await integrationCard('vps'); return c?.buttons.includes('Install herdr') && c; }, { what: 'the herdr on vps card', timeout: 20000 });
    await app.eval(`document.querySelector('#integrations-heading').scrollIntoView()`);
    await shot('h-install-card');
    const frontMark = env.logMark();
    const started = Date.now();
    if (!(await cardTap('vps', 'Install herdr'))) throw new Error('no Install herdr button');
    const squash = (x) => x.replace(/\s+/g, '');
    await until(async () => squash(await terminalText(app)).includes(squash(INSTALL)), { what: 'the install command in Live', timeout: 30000 });
    time('Install herdr → a session on vps with the command typed', Date.now() - started);
    const path = await app.eval('location.pathname');
    // Long enough for a command that ran to print something, or for a new prompt.
    await sleep(5000);
    const screen = await terminalText(app);
    await shot('h-install-typed');
    const prompts = (screen.match(/ % /g) ?? []).length;
    const session = (await api(tool, `${vps.origin}/api/v1/sessions`)).json.sessions.filter((s) => !before.includes(s.id));
    const outside = env.logSince(frontMark).filter((e) => e.host?.includes('herdr.dev'));
    const installed = existsSync(join(vps.home, '.local', 'bin', 'herdr'));
    const ended = [];
    for (const s of session) ended.push((await api(tool, `${vps.origin}/api/v1/sessions/${s.id}/terminate`, 'POST', { operationId: crypto.randomUUID(), generation: s.generation })).status);
    const left = await until(async () => { const list = (await api(tool, `${vps.origin}/api/v1/sessions`)).json.sessions; return !list.some((s) => session.some((x) => x.id === s.id) && s.state === 'running') && list; }, { what: 'the install session ended', timeout: 15000 });
    check('H-install-herdr', 'vps has no herdr: the card "herdr on vps" says Not installed, has no switches, has Install herdr and its line; the herdr tab has nothing from vps and the app never asked vps for a provider; Install herdr opens one new session on vps whose terminal shows the install command on the prompt line, not run (one prompt, nothing after the command, no installer file, no request to herdr.dev)',
      info.length === 1 && info[0].state === 'notInstalled' && info[0].canInstall === true
        && card.state === 'not installed' && card.muted && card.switches === 0 && JSON.stringify(card.buttons) === '["Install herdr","Check again"]'
        && card.text.includes('Opens a terminal on vps with herdr\'s install command. Press Enter there to run it.')
        && !tabGroups.some((g) => g.where.includes('on vps')) && polled.length === 0
        && session.length === 1 && path.startsWith(`/c/${vps.host}/`) && prompts === 1 && squash(screen).endsWith(squash(INSTALL)) && outside.length === 0 && !installed
        && ended.every((status) => status === 200),
      `settings ${JSON.stringify(info)}; card ${JSON.stringify(card)}; vps provider requests ${polled.length}; Live ${path}; screen ${JSON.stringify(screen.trim())} (${prompts} prompt); new sessions ${session.length}; requests to herdr.dev ${outside.length}; ~/.local/bin/herdr on vps ${installed}; terminate ${JSON.stringify(ended)}, ${left.length} sessions left on vps`);
    await inApp('/');
  });

  // ---- Start: herdr installed and not running ----
  await step('H-start', 'herdr installed and not running: Start herdr on the herdr tab and in Integrations; a tap starts the server', async () => {
    await settle();
    await backToTab();
    await mac.stopHerdrServer();
    const notice = () => app.eval(`(() => { ${LABEL}
      const n = [...document.querySelectorAll('.provider-tree .notice')].find((e) => e.innerText.includes('herdr is not running on mac'));
      return n ? { text: text(n), buttons: [...n.querySelectorAll('.notice-action button')].map(label) } : null; })()`);
    const stopped = await until(async () => { const n = await notice(); return n?.buttons.includes('Start herdr') && n; }, { what: 'the not running notice with Start herdr', timeout: 25000 });
    await shot('h-start-notice');
    const info = (await api(tool, `${mac.origin}/api/v1/settings`)).json.providers[0];
    await inApp('/settings');
    const card = await until(async () => { const c = await integrationCard('mac'); return c?.buttons.includes('Start herdr') && c; }, { what: 'Start herdr on the Integrations card', timeout: 20000 });
    await app.eval(`document.querySelector('#integrations-heading').scrollIntoView()`);
    await shot('h-start-card');
    await backToTab();
    await until(async () => (await notice())?.buttons.includes('Start herdr'), { what: 'the notice again', timeout: 20000 });
    const mark = mac.herdrLog().length;
    const started = Date.now();
    await app.eval(`(() => { ${LABEL} [...document.querySelectorAll('.provider-tree .notice-action button')].find((b) => label(b) === 'Start herdr').click(); return true; })()`);
    const list = await until(async () => { const g = await groups(); return g.length >= 4 && g; }, { what: 'the projects back', timeout: 30000, every: 100 });
    const ms = Date.now() - started;
    time('Start herdr → the projects back on the herdr tab', ms);
    await shot('h-start-ready');
    const after = (await api(tool, `${mac.origin}/api/v1/settings`)).json.providers[0];
    const line = logged(mark, 'server start ')[0] ?? '';
    const again = await manageApi({ action: 'startServer' });
    check('H-start', 'The fake server ended, its binary stays: "herdr is not running on mac" with Start herdr on the herdr tab, "Not running" with Start herdr in Integrations (canStart); a tap starts `herdr server` once with this stack\'s PATH and TERM, the state is ready and the projects are back; a second start is refused',
      info.state === 'notRunning' && info.canStart === true && card.state === 'not running' && card.switches === 3
        && logged(mark, 'server start ').length === 1 && line.includes(`PATH=${mac.bin}:`) && line.includes('TERM=xterm-256color') && after.state === 'ready' && after.canStart === undefined
        && list.some((g) => g.name === 'shop') && again.status === 400,
      `notice "${stopped.text}" ${JSON.stringify(stopped.buttons)}; settings ${JSON.stringify(info)}; card ${JSON.stringify(card)}; fake: ${line.replaceAll(env.root, '<root>')}; ready and ${list.length} projects after ${ms} ms; second start ${again.status} ${again.json?.error?.code}`);
  });

  // ---- H-peer: herdr on vps, opened through vps from the mac app ----
  await step('H-peer', 'herdr on vps shows under the herdr tab and opens through vps; its tab without an agent cannot open', async () => {
    await settle();
    await vps.installHerdr(vpsHerdr.scenario);
    const started = Date.now();
    await until(async () => (await api(tool, `${vps.origin}/api/v1/settings`)).json?.providers?.[0]?.state === 'ready', { what: 'herdr ready on vps', timeout: 45000, every: 500 });
    // The app reads each computer's settings (its providers) again when it comes back to the front.
    await tool.front();
    await sleep(500);
    await app.front();
    await herdrTab({ load: false });
    await until(async () => (await groups()).some((g) => g.where.includes('on vps')), { what: 'vps projects', timeout: 20000, every: 500 });
    time('herdr installed on vps → its projects in the mac app', Date.now() - started);
    const list = await groups();
    await shot('h-peer-tab');
    const target = vpsHerdr.targets['acme-web/1'];
    const pane = paneOf(target);
    const mark = vps.herdrLog().length;
    const macMark = mac.herdrLog().length;
    // Two acme-web rows: the one on vps.
    await app.eval(`[...document.querySelectorAll('.provider-group')].find((e) => e.innerText.toLowerCase().includes('on vps')).querySelector('.group-toggle').click()`);
    await until(() => app.eval(`location.pathname === ${JSON.stringify(`/c/${vps.host}/p/herdr/${enc(target)}`)}`), { what: 'the vps /p/ address' });
    await liveMarker(pane);
    await attachedSince(vps, pane, mark);
    const chipsNow = await app.eval(`[...document.querySelectorAll('.provider-chips .tab')].map((b) => ({ label: b.innerText.trim(), disabled: b.disabled, title: b.title }))`);
    await shot('h-peer-live');
    const evil = await env.curl(`${vps.origin}/api/v1/providers/herdr`, { headers: { Cookie: await jarOf(vps), Origin: 'https://evil.tailx.ts.net' } });
    const fromMacApp = env.logSince(0).filter((e) => e.host === vps.host && e.path === '/api/v1/providers/herdr/open').at(-1);
    check('H-peer', 'vps\'s acme-web is listed "on vps"; tapping it opens vps\'s fake terminal through vps (mac\'s fake sees no attach); its "server" chip is off (no terminal attach there); evil\'s Origin gets 403',
      list.some((g) => g.name === 'acme-web' && g.where.includes('on vps')) && mac.herdrLog().slice(macMark).every((l) => !l.startsWith('attach'))
        && chipsNow.some((c) => c.label.startsWith('server') && c.disabled) && evil.status === 403 && fromMacApp?.origin === mac.origin,
      `groups ${JSON.stringify(list.map((g) => `${g.name} ${g.where.join(' ')}`))}; chips ${JSON.stringify(chipsNow)}; open request origin ${fromMacApp?.origin} → ${fromMacApp?.status}; evil ${evil.status}`);

    // A vps push tapped while the app is open: the peer worker tells the page, the page asks home's worker for the tap record, and opens at once.
    await inApp('/');
    await settle();
    await browser.send('Browser.grantPermissions', { origin: mac.origin, permissions: ['notifications'] });
    const ids = new Map();
    browser.listeners.push((m) => { if (m.method === 'ServiceWorker.workerRegistrationUpdated') for (const r of m.params.registrations) ids.set(r.scopeURL, r.registrationId); });
    // Off and on again: enable lists every registration (B3-push may have enabled it already).
    await app.send('ServiceWorker.disable').catch(() => undefined);
    await app.send('ServiceWorker.enable');
    const scope = `/push/${vps.host}/`;
    await app.eval(`navigator.serviceWorker.register('/sw.js', { scope: ${JSON.stringify(scope)} }).then((r) => new Promise((done) => {
      const w = r.installing ?? r.waiting; if (r.active || !w) done(true); else w.addEventListener('statechange', () => w.state === 'activated' && done(true)); }))`);
    const registrationId = await until(() => ids.get(`${mac.origin}${scope}`), { what: 'peer registration' });
    await app.send('ServiceWorker.deliverPushMessage', { origin: mac.origin, registrationId, data: JSON.stringify({
      title: 'herdr · acme-web', body: 'claude needs you. Tap to answer.', kind: 'waiting', provider: 'herdr', target }) });
    await until(() => app.eval(`navigator.serviceWorker.getRegistration(${JSON.stringify(scope)}).then((r) => r.getNotifications()).then((n) => n.length > 0)`), { what: 'vps notification' });
    const tapMark = vps.herdrLog().length;
    await app.eval('window.__sameLoad = true');
    const clicked = await clickNotification(browser, `${mac.origin}${scope}`, 'vps · herdr · acme-web');
    await until(() => app.eval(`location.pathname === ${JSON.stringify(`/c/${vps.host}/p/herdr/${enc(target)}`)}`), { what: `the app routes to the vps terminal (${clicked})` });
    await attachedSince(vps, pane, tapMark, 10000);
    await liveMarker(pane);
    await shot('h-peer-tap');
    check('H-peer-tap', 'A vps herdr push tapped with the app open: the vps worker tells the page, which opens the vps terminal at once (no Open, same page load)',
      await app.eval('window.__sameLoad === true') && !(await app.text()).includes('Open shows it here'), `worker: ${clicked}; ${await app.eval('location.pathname')}`);
    await app.eval(`navigator.serviceWorker.getRegistration(${JSON.stringify(scope)}).then((r) => r?.unregister())`);
    await inApp('/');
  });

  // ---- N-*: the "Needs you" inbox and its queue (docs/needs-you-inbox.md) ----
  // One Termivoa session on vps that waits on a question (a script that draws a numbered menu, as an agent does),
  // and one blocked herdr agent on mac (shop): two items on two computers.
  const ASK = 'Run the demo check?';
  // fit: what was wider than the screen, by page and width. made: the sessions these checks started, as [stack, session].
  const needs = { fit: {}, made: [] };
  /** Measures the page at 390 and 375 px into needs.fit; the 390 px viewport comes back also when a measure throws. */
  const measureFit = async (name) => {
    try {
      for (const width of [390, 375]) {
        await app.send('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 3, mobile: true });
        await sleep(300);
        needs.fit[`${name}${width}`] = await overflow();
      }
    } finally {
      await app.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    }
  };
  /** Ends every session these checks started; throws when a host does not answer 200. */
  const endNeedsSessions = async () => {
    const answers = [];
    for (const [stack, one] of needs.made.splice(0)) {
      const now = (await api(tool, `${stack.origin}/api/v1/sessions`)).json?.sessions?.find((s) => s.id === one.id);
      if (now === undefined || now.state === 'exited') continue;
      answers.push((await api(tool, `${stack.origin}/api/v1/sessions/${one.id}/terminate`, 'POST', { operationId: crypto.randomUUID(), generation: now.generation })).status);
    }
    if (answers.some((status) => status !== 200)) throw new Error(`terminate answered ${JSON.stringify(answers)}`);
    return answers;
  };
  const needsChip = () => app.eval(`(() => { const c = [...document.querySelectorAll('.chips .chip')].find((e) => e.innerText.trim().startsWith('Needs you'));
    return c ? { count: Number(c.querySelector('.chip-count')?.innerText ?? 0), pressed: c.getAttribute('aria-pressed') } : null; })()`);
  const inboxRows = () => app.eval(`(() => { const part = document.querySelector('section[aria-label="Needs you"]'); if (!part) return null;
    const label = part.querySelector('.list-label');
    return [...part.querySelectorAll('.inbox-row')].map((b) => ({ title: b.querySelector('.row-title').innerText.trim(), state: b.querySelector('.state-word').innerText.trim().toLowerCase(),
      meta: b.querySelector('.row-meta')?.innerText.replace(/\\s+/g, ' ').trim() ?? '', question: b.querySelector('.question')?.innerText.trim() ?? '',
      finished: label !== null && Boolean(label.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING), preview: b.querySelector('.preview') !== null })); })()`);
  const tapInboxRow = async (title) => {
    const ok = await app.eval(`(() => { const b = [...document.querySelectorAll('section[aria-label="Needs you"] .inbox-row')].find((e) => e.querySelector('.row-title').innerText.trim() === ${JSON.stringify(title)});
      if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`no Needs you row "${title}"`);
  };
  const queueButton = () => app.eval(`(() => { const b = document.querySelector('.queue-bar button'); return b ? { text: b.innerText.trim(), filled: b.classList.contains('primary'), height: Math.round(b.getBoundingClientRect().height) } : null; })()`);
  const waitingOn = async (stack) => (await api(tool, `${stack.origin}/api/v1/sessions`)).json.sessions.filter((one) => one.agent?.state === 'waiting');
  const blockedOn = async (stack) => ((await api(tool, `${stack.origin}/api/v1/providers/herdr`)).json?.groups ?? []).flatMap((g) => g.items).filter((i) => i.state === 'blocked' && i.openTarget !== undefined);
  const openNeedsYou = async () => {
    await inApp('/');
    await until(() => needsChip(), { what: 'the Needs you tab', timeout: 20000 });
    await click(app, 'Needs you');
    await until(async () => (await inboxRows()) !== null, { what: 'the Needs you list' });
  };

  try {
    await step('N-inbox', 'Needs you lists the waiting vps session and the blocked mac herdr agent, with the count 2', async () => {
      // With --only N the H-* steps did not run: install the fake on mac now.
      if ((await api(tool, `${mac.origin}/api/v1/settings`)).json?.providers?.[0]?.state !== 'ready') {
        await mac.installHerdr(macHerdr.scenario);
        await until(async () => (await api(tool, `${mac.origin}/api/v1/settings`)).json?.providers?.[0]?.state === 'ready', { what: 'herdr ready on mac', timeout: 45000, every: 500 });
        // The app keeps a computer's settings for the visit; it reads them again when it comes back to the front (as in H-peer).
        await tool.front();
        await sleep(500);
        await app.front();
      }
      // Exactly one blocked herdr agent: shop on mac. Every other blocked one goes idle.
      const shopPane = paneOf(t['shop/1']);
      for (const stack of [mac, vps]) {
        const groupsNow = (await api(tool, `${stack.origin}/api/v1/providers/herdr`)).json?.groups ?? [];
        for (const target of groupsNow.flatMap((g) => g.items).flatMap((i) => i.targets).filter((x) => x.state === 'blocked')) await stack.herdrControl({ op: 'status', pane: paneOf(target.id), status: 'idle' });
      }
      await mac.herdrControl({ op: 'status', pane: shopPane, status: 'blocked' });
      // The waiting session on vps: made through the API, its question drawn by a script typed in Live.
      const own = (await api(tool, `${vps.origin}/api/v1/settings`)).json;
      const made = await api(tool, `${vps.origin}/api/v1/sessions`, 'POST', { operationId: crypto.randomUUID(), projectId: own.projects[0].id, shellProfileId: own.shellProfiles[0].id });
      if (made.status !== 200 && made.status !== 201) throw new Error(`session on vps: ${made.status} ${made.body}`);
      const session = made.json.session ?? made.json;
      needs.made.push([vps, session]);
      const renamed = await api(tool, `${vps.origin}/api/v1/sessions/${session.id}`, 'PATCH', { operationId: crypto.randomUUID(), generation: session.generation, title: 'needs-you-vps' });
      if (renamed.status !== 200) throw new Error(`rename on vps: ${renamed.status} ${renamed.body}`);
      const script = join(vps.home, 'needs-you-ask.sh');
      // Clears the screen, hides the cursor (a visible cursor under a menu reads as a shell prompt, not a dialog), draws the
      // question with a marked option 1, then waits for one key (the host presses Enter for the marked option).
      writeFileSync(script, `printf '\\033[2J\\033[H\\033[?25l\\n ${ASK}\\n\\n\\342\\235\\257 1. Yes\\n  2. No\\n'\nread -n 1 -s key\nprintf '\\033[?25h\\nanswered\\n'\n`);
      needs.session = session;
      needs.path = `/c/${vps.host}/sessions/${session.id}`;
      await inApp(needs.path);
      await until(async () => /[%$#]\s*$/m.test(await terminalText(app)), { what: 'shell prompt on vps', timeout: 30000 });
      await click(app, 'Take control');
      await until(async () => !(await app.text()).includes('Take control'), { what: 'control taken' });
      await typeLine(app, `/bin/bash ${script}`);
      await until(async () => (await waitingOn(vps)).some((one) => one.id === session.id && one.agent.question?.text === ASK), { what: 'vps session waiting on the question', timeout: 20000 });
      await until(async () => (await blockedOn(mac)).length === 1 && (await blockedOn(vps)).length === 0, { what: 'one blocked herdr item (shop on mac)', timeout: 20000 });
      const expected = (await waitingOn(mac)).length + (await waitingOn(vps)).length + 1;
      needs.expected = expected;

      await inApp('/');
      const started = Date.now();
      await until(async () => (await needsChip())?.count === expected, { what: `Needs you ${expected}`, timeout: 20000 });
      time('a session waits and a herdr agent is blocked → Needs you count on Sessions', Date.now() - started);
      const order = await app.eval(`[...document.querySelectorAll('.chips .chip')].map((e) => e.innerText.replace(/\\s+/g, ' ').trim())`);
      await shot('n-sessions-entry');
      await click(app, 'Needs you');
      const rows = await until(async () => { const r = await inboxRows(); return r !== null && r.length >= expected && r; }, { what: 'Needs you rows' });
      await measureFit('inbox');
      await shot('n-inbox');
      const asked = rows.find((r) => r.title === 'needs-you-vps');
      const shop = rows.find((r) => r.title === 'shop');
      const line = (await statusLine()).toLowerCase();
      check('N-inbox', 'Sessions has the Needs you tab right after All with the count 2; on it: the vps session (waiting, its question, on vps) and mac\'s shop (blocked, herdr, on mac), nothing finished before them, no terminal preview line, and "2 need you"',
        expected === 2 && order[0] === 'All' && order[1].replace(/\s/g, '') === 'Needsyou2' && (await needsChip()).pressed === 'true'
          && asked?.state.startsWith('waiting') && asked.question === ASK && asked.meta === 'vps' && !asked.finished
          && shop?.state.startsWith('blocked') && shop.meta === 'mac · herdr' && !shop.finished && shop.question === ''
          && rows.filter((r) => !r.finished).length === expected && rows.every((r) => !r.preview) && line === `${expected} need you`,
        `tabs ${JSON.stringify(order)}; rows ${JSON.stringify(rows)}; status line "${line}"; host: ${expected} waits`);
    });

    await step('N-open', 'A tap on the waiting row opens it in Live, read-only, with the answer card and Next · 1 left', async () => {
      await openNeedsYou();
      await tapInboxRow('needs-you-vps');
      await until(() => app.eval(`location.pathname === ${JSON.stringify(needs.path)}`), { what: 'the session address' });
      const button = await until(() => queueButton(), { what: 'the Next control' });
      await app.waitText('Take control to answer');
      await measureFit('live');
      await shot('n-live-next');
      const text = await app.text();
      const badge = await app.eval(`document.querySelector('.status-line .badge-text')?.innerText.trim()`);
      check('N-open', 'Live for the vps session shows its question in the answer card with "Take control to answer" (opening took no control: READ-ONLY) and one "Next · 1 left ›" button, 36 px or more high',
        button.text === 'Next · 1 left ›' && !button.filled && button.height >= 36 && text.includes(ASK) && badge === 'READ-ONLY',
        `button ${JSON.stringify(button)}; badge ${badge}`);
    });

    await step('N-fit', 'Nothing wider than 375 or 390 px on the Needs you tab and in Live with the Next row', async () => {
      const keys = ['inbox390', 'inbox375', 'live390', 'live375'];
      check('N-fit', 'Nothing wider than 375 or 390 px: the Needs you tab with its rows, and Live with the Next row and the answer card',
        keys.every((k) => Array.isArray(needs.fit[k]) && needs.fit[k].length === 0), JSON.stringify(needs.fit));
    });

    await step('N-answer', 'An answer the host accepts fills the Next button; nothing moves by itself', async () => {
      if ((await app.eval('location.pathname')) !== needs.path) { await openNeedsYou(); await tapInboxRow('needs-you-vps'); await until(() => queueButton(), { what: 'the Next control' }); }
      await click(app, 'Take control to answer');
      await until(() => app.eval(`document.querySelector('.answer-option') !== null && !document.querySelector('.answer-option').disabled`), { what: 'answers enabled', timeout: 15000 });
      const sent = (await terminalText(app)).includes('answered');
      await app.eval(`document.querySelector('.answer-option').click()`);
      const button = await until(async () => { const b = await queueButton(); return b?.filled && b; }, { what: 'Next filled after the accepted answer', timeout: 15000 });
      await until(async () => (await terminalText(app)).includes('answered'), { what: 'the script got one key', timeout: 15000 });
      await until(async () => !(await waitingOn(vps)).some((one) => one.id === needs.session.id), { what: 'the vps session stops waiting', timeout: 15000 });
      await sleep(2000);
      const path = await app.eval('location.pathname');
      await shot('n-answered');
      check('N-answer', 'After "Take control to answer" and a tap on answer 1 the script gets its key once, the Next button is filled and still says "Next · 1 left ›" (the answered session is not counted), and the app stays on the session',
        !sent && button.text === 'Next · 1 left ›' && path === needs.path, `button ${JSON.stringify(button)}; still at ${path}`);
    });

    await step('N-next', 'Next opens the blocked herdr agent on mac; there it says Inbox', async () => {
      if ((await queueButton()) === null) { await openNeedsYou(); await tapInboxRow((await inboxRows()).find((r) => r.title === 'needs-you-vps') ? 'needs-you-vps' : 'shop'); await until(() => queueButton(), { what: 'the Next control' }); }
      const pane = paneOf(t['shop/1']);
      const mark = mac.herdrLog().length;
      if ((await app.eval('location.pathname')) === needs.path) await click(app, 'Next · 1 left ›');
      await until(() => app.eval(`location.pathname === ${JSON.stringify(`/p/herdr/${enc(t['shop/1'])}`)}`), { what: 'the shop terminal address' });
      await liveMarker(pane);
      await attachedSince(mac, pane, mark);
      const button = await until(async () => { const b = await queueButton(); return b?.text === 'Inbox ›' && b; }, { what: 'Inbox › on the last item' });
      await shot('n-next-herdr');
      const text = await app.text();
      check('N-next', 'Next goes straight to mac\'s shop terminal (one attach in the fake, opened at once, read-only with Take control); it is the last one, so the button says "Inbox ›"',
        button.text === 'Inbox ›' && text.includes('Take control') && !text.includes('Open shows it here'), `button ${JSON.stringify(button)}; ${await app.eval('location.pathname')}`);
    });

    await step('N-tap', 'A tapped session notification opens the session with Next; the same address loaded again has none', async () => {
      await inApp('/');
      await settle();
      // Any running session on mac serves: a full run has one (H-none); with --only N this step starts one.
      let session = (await api(tool, `${mac.origin}/api/v1/sessions`)).json.sessions.find((one) => one.state === 'running');
      if (session === undefined) {
        const own = (await api(tool, `${mac.origin}/api/v1/settings`)).json;
        const made = await api(tool, `${mac.origin}/api/v1/sessions`, 'POST', { operationId: crypto.randomUUID(), projectId: own.projects[0].id, shellProfileId: own.shellProfiles[0].id });
        if (made.status !== 200 && made.status !== 201) throw new Error(`session on mac: ${made.status} ${made.body}`);
        session = made.json.session ?? made.json;
        needs.made.push([mac, session]);
      }
      const path = `/sessions/${session.id}`;
      await browser.send('Browser.grantPermissions', { origin: mac.origin, permissions: ['notifications'] });
      const ids = new Map();
      browser.listeners.push((m) => { if (m.method === 'ServiceWorker.workerRegistrationUpdated') for (const r of m.params.registrations) ids.set(r.scopeURL, r.registrationId); });
      await app.send('ServiceWorker.disable').catch(() => undefined);
      await app.send('ServiceWorker.enable');
      const registrationId = await until(() => ids.get(`${mac.origin}/`), { what: 'home worker registration' });
      const title = 'needs-you-mac is waiting for you';
      await app.send('ServiceWorker.deliverPushMessage', { origin: mac.origin, registrationId, data: JSON.stringify({ title, body: 'demo · tap to answer', kind: 'waiting', sessionId: session.id }) });
      const shown = await until(() => app.eval(`navigator.serviceWorker.getRegistration('/').then((r) => r.getNotifications()).then((n) => n.map((x) => ({ title: x.title, url: x.data?.url }))
        .find((x) => x.title === ${JSON.stringify(title)}))`), { what: 'session notification' });
      const clicked = await clickNotification(browser, `${mac.origin}/`, title);
      // As in H-tap: the browser refuses focus() and navigate() for a synthetic click, so the harness loads the address.
      await app.go(`${mac.origin}${shown.url}`);
      const button = await until(() => queueButton(), { what: 'Next after the notification tap', timeout: 20000 });
      const tappedText = await until(async () => { const b = await queueButton(); return b?.text === 'Next · 1 left ›' && b.text; }, { what: 'Next · 1 left (shop still blocked)', timeout: 20000 });
      await shot('n-tap');
      // The record is used up: the same address again is a plain link.
      await app.go(`${mac.origin}${shown.url}`);
      await until(() => app.eval(`document.querySelector('.terminal-host') !== null`), { what: 'Live again' });
      await sleep(3000);
      const again = await queueButton();
      check('N-tap', 'A session push tapped (notificationclick in the worker), then its address loaded: Live shows "Next · 1 left ›" (the blocked shop agent); the address carries nothing; loaded a second time it shows no Next',
        shown.url === path && button !== null && tappedText === 'Next · 1 left ›' && again === null && (await app.eval('location.pathname + location.search + location.hash')) === path,
        `notification ${JSON.stringify(shown)}; worker click: ${clicked}; first load "${tappedText}"; second load ${JSON.stringify(again)}`);
    });

    await step('N-last', 'The last Next returns to the Needs you tab: "Nothing needs you."; the tab leaves when another tab is chosen', async () => {
      await openNeedsYou();
      await tapInboxRow('shop');
      await until(async () => (await queueButton())?.text === 'Inbox ›', { what: 'Inbox › in the shop terminal', timeout: 20000 });
      // Nothing waits any more: the herdr agent goes idle, and the sessions of these checks end.
      await mac.herdrControl({ op: 'status', pane: paneOf(t['shop/1']), status: 'idle' });
      const ended = await endNeedsSessions();
      await click(app, 'Inbox ›');
      await until(() => app.eval(`location.pathname === '/'`), { what: 'back on Sessions' });
      const chip = await until(async () => { const c = await needsChip(); return c?.pressed === 'true' && c; }, { what: 'the Needs you tab pressed' });
      await until(() => app.eval(`document.querySelector('section[aria-label="Needs you"]')?.innerText.includes('Nothing needs you.') === true`), { what: '"Nothing needs you."', timeout: 20000 });
      const left = (await inboxRows()).filter((r) => !r.finished);
      const empty = await needsChip();
      await shot('n-empty');
      await click(app, 'All');
      await until(async () => (await needsChip()) === null, { what: 'the Needs you tab leaves with nothing waiting', timeout: 10000 });
      check('N-last', '"Inbox ›" on the last item goes to Sessions on the Needs you tab; with nothing waiting it says "Nothing needs you." once, with no count and no waiting row; after a tap on All the tab is gone',
        chip.pressed === 'true' && empty.count === 0 && left.length === 0, `tab ${JSON.stringify(empty)}; waiting rows ${left.length}; after All: no Needs you tab; terminate ${JSON.stringify(ended)}`);
      await settle();
    });
  } finally {
    // Also when a step above failed before N-last: no session of these checks stays for the later steps.
    await endNeedsSessions().catch((error) => console.error(`N-*: ${error.message}`));
  }

  // ---- the end: no herdr again, so the later checks run as without it ----
  await mac.uninstallHerdr();
  await vps.uninstallHerdr();
  return latencies;
}

/**
 * Dispatches notificationclick for the notification titled title inside the
 * worker of scope. The event is synthetic: the browser refuses focus() and
 * openWindow() for it, but the worker's tap record and message run.
 */
async function clickNotification(browser, scope, title) {
  const outcomes = [];
  for (const target of (await browser.send('Target.getTargets')).targetInfos.filter((x) => x.type === 'service_worker')) {
    const { sessionId } = await browser.send('Target.attachToTarget', { targetId: target.targetId, flatten: true });
    const r = await browser.send('Runtime.evaluate', { awaitPromise: true, returnByValue: true, expression: `self.registration.scope === ${JSON.stringify(scope)}
      && self.registration.getNotifications().then((all) => { const n = all.find((x) => x.title === ${JSON.stringify(title)}); if (!n) return 'no notification';
        const e = new NotificationEvent('notificationclick', { notification: n });
        let done = Promise.resolve('no waitUntil');
        e.waitUntil = (work) => { done = work.then(() => 'done', (error) => String(error)); };
        self.dispatchEvent(e);
        return done; })` }, sessionId);
    if (r.result?.value !== false) outcomes.push(String(r.result?.value ?? r.exceptionDetails?.exception?.description));
    await browser.send('Target.detachFromTarget', { sessionId }).catch(() => undefined);
  }
  return outcomes.join('; ') || 'worker not found';
}

