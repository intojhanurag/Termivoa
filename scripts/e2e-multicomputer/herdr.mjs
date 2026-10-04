// Phase B's herdr checks (H-*; docs/herdr-provider.md, section 15): the
// fake herdr (scripts/fakeherdr) on mac, later on vps, inside the harness
// sandbox with each stack's own HOME. Nothing here reaches the real herdr.
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { api, click, setInput, sleep, terminalText, typeLine, until } from './lib.mjs';

const enc = encodeURIComponent;

/**
 * A fake herdr scenario from a short spec: workspaces with tabs, one pane
 * per tab, an agent where the tab names one. Checkouts are real folders
 * under home/src. Returns the scenario and the target IDs by
 * "<workspace>/<tab>".
 */
export function herdrScenario(home, { attachAny, kinds = ['claude', 'codex'], workspaces }) {
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
        snapshot.agents.push({ pane_id: paneId, tab_id: tabId, workspace_id: id, terminal_id: terminal, agent: tab.agent, agent_status: status, state_change_seq: 1, cwd: path });
      }
      targets[`${spec.label}/${tab.label}`] = `${paneId}.${terminal}`;
    });
  });
  return { scenario: { version: '0.8.2', protocol: 20, attachAny, kinds, snapshot }, targets };
}

/** mac: two git projects (shop with a worktree, acme-web), one outside git, and a long project name with a worktree. herdr 0.8.2 attaches any pane. */
const MAC = {
  attachAny: true,
  workspaces: [
    { label: 'shop', repo: 'shop', tabs: [{ label: '1', agent: 'claude', status: 'idle' }, { label: 'logs' }] },
    { label: 'silver-meadow', repo: 'shop', tabs: [{ label: '1', agent: 'codex', status: 'working' }] },
    { label: 'acme-web', repo: 'acme-web', tabs: [{ label: '1', agent: 'claude', status: 'done' }] },
    { label: 'notes', tabs: [{ label: '1' }] },
    { label: 'storefront-redesign', repo: 'storefront-redesign', tabs: [{ label: '1' }] },
    { label: 'lucky-valley', repo: 'storefront-redesign', tabs: [{ label: '1', agent: 'claude', status: 'idle' }] },
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
  await step('H-none', 'No herdr anywhere: no herdr tab, no provider requests, no providers in settings', async () => {
    // A Termivoa session on mac, so the Sessions list has a row while herdr is in trouble (H-down, H-hang).
    const own = (await api(tool, `${mac.origin}/api/v1/settings`)).json;
    const made = await api(tool, `${mac.origin}/api/v1/sessions`, 'POST', { operationId: crypto.randomUUID(), projectId: own.projects[0].id, shellProfileId: own.shellProfiles[0].id });
    if (made.status !== 200 && made.status !== 201) throw new Error(`session on mac: ${made.status} ${made.body}`);
    await openSessions();
    await sleep(6000); // more than one provider poll would take
    const chip = await herdrChip();
    const settings = await api(tool, `${mac.origin}/api/v1/settings`);
    const requests = env.logSince(0).filter((e) => e.path?.startsWith('/api/v1/providers'));
    latencies.absent = await latency(tool, `${mac.origin}/api/v1/sessions`);
    time('GET /api/v1/sessions with herdr absent, p50', latencies.absent.p50);
    check('H-none', 'No herdr on either computer: no herdr tab, no provider request in the whole run, settings list no provider',
      chip === null && requests.length === 0 && settings.json?.providers === undefined,
      `chip ${JSON.stringify(chip)}; provider requests ${requests.length}; settings.providers ${JSON.stringify(settings.json?.providers)}; sessions read ${fmt(latencies.absent)}`);
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
    // The sandbox hides the real herdr: doctor may list it only as refused.
    const real = lines.filter((l) => l.includes('/opt/homebrew') || l.includes('/usr/local'));
    check('H-doctor', 'pterm doctor reports the fake herdr on this stack\'s PATH, its version and running server; the real herdr only as refused (sandbox)',
      line.includes(`${mac.bin}/herdr 0.8.2 (protocol 20)`) && line.includes('server running') && real.every((l) => l.includes('Termivoa will not run it')),
      lines.map((l) => l.replaceAll(env.root, '<root>').trim()).join(' / '));
  });

  // ---- H-tab, H-group ----
  await step('H-tab', 'The herdr tab shows with the count for mac; nothing for vps', async () => {
    await openSessions();
    const chip = await until(() => herdrChip(), { what: 'herdr tab', timeout: 20000 });
    await herdrTab({ load: false });
    const list = await until(async () => { const g = await groups(); return g.length >= 4 && g; }, { what: 'herdr groups' });
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

