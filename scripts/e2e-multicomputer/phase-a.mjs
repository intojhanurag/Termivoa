// Phase A: checks that need no phone screens (README.md).
//   node scripts/e2e-multicomputer/phase-a.mjs [--no-web-build] [--out <dir>] [--keep]
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Browser, Env, NODES, interactive, pageFetch, pairDirect, sleep, until } from './lib.mjs';

const argv = process.argv.slice(2);
const flag = (name) => argv.includes(name);
const option = (name) => argv[argv.indexOf(name) + 1];

const results = [];
function check(id, name, pass, evidence) {
  results.push({ id, name, result: pass ? 'PASS' : 'FAIL', evidence });
  console.log(`${pass ? 'PASS' : 'FAIL'} ${id} ${name}\n     ${evidence}`);
}
const brief = (e) => e ? `${e.method} ${e.path} origin=${e.origin || '-'} sfs=${e.secFetchSite || '-'} sfm=${e.secFetchMode || '-'} cookie=${e.cookie} → ${e.status}${e.acao ? ` ACAO=${e.acao}` : ''}${e.setCookie ? ' Set-Cookie' : ''}` : 'no request reached the computer';
const refused = (r) => r.status === 403 && !r.headers['access-control-allow-origin'] && !r.headers['set-cookie'];

const env = await Env.create({ buildWeb: !flag('--no-web-build') });
const { mac, vps } = env.stacks;
console.log(`root ${env.root}\nfront ${env.proxy}\nmac → ${mac.gateway}\nvps → ${vps.gateway}`);
const browser = await Browser.launch(env);
console.log(browser.product);
const out = option('--out') && !option('--out').startsWith('--') ? option('--out') : join(env.root, 'evidence');
mkdirSync(out, { recursive: true });
const SETTINGS = `${vps.origin}/api/v1/settings`;
const json = { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' };

try {
  // ---- A1: preflight from mac to vps before any pairing ----
  const macPage = await browser.page({ bypassCSP: true });
  await macPage.go(`${mac.origin}/`);
  await macPage.shot(join(out, 'a-mac-unpaired.png'));
  let mark = env.logMark();
  const pre1 = await pageFetch(macPage, SETTINGS, { credentials: 'include', headers: { 'X-Pterm-Request': '1' } });
  const pre2 = await pageFetch(macPage, `${vps.origin}/api/v1/pair/redeem`, { method: 'POST', credentials: 'include', headers: json, body: '{"grant":"x","deviceLabel":"x"}' });
  await sleep(300);
  const options = env.logSince(mark).filter((e) => e.host === vps.host && e.method === 'OPTIONS');
  check('A1', 'Preflight mac → vps before pairing is refused (Chrome)',
    pre1.error && pre2.error && options.length === 2 && options.every((e) => e.status === 403 && !e.acao),
    `fetch: ${pre1.error ?? pre1.status} / ${pre2.error ?? pre2.status}; ${options.map(brief).join('; ')}`);
  const curlPre = await env.curl(SETTINGS, { method: 'OPTIONS', headers: { Origin: mac.origin, 'Access-Control-Request-Method': 'GET', 'Access-Control-Request-Headers': 'x-pterm-request' } });
  check('A1b', 'Preflight mac → vps before pairing is refused (curl)', refused(curlPre), `OPTIONS → ${curlPre.status}, ACAO=${curlPre.headers['access-control-allow-origin'] ?? 'none'}`);

  // ---- pair the browser with mac (home), as today ----
  const home = await pairDirect(macPage, mac, 'e2e phone');
  await macPage.waitText('Sessions', 30000);
  await macPage.shot(join(out, 'a-mac-paired.png'));
  check('A2', 'Pair the browser with mac (home) as today', true, `code ${home.code}, ${home.ms} ms`);

  // ---- bound pairing of vps through the API (the screens drive this in Phase B) ----
  const started = Date.now();
  const via = interactive(vps, ['pair', '--via', 'mac']);
  const [link, grant] = await via.wait(/https:\/\/vps\.tailx\.ts\.net\/add-computer#grant=([^&\s]+)&via=\S+/);
  await sleep(1500); // a person needs longer; the gateway learns pairing origins on its 1 s probe
  const pending = await pageFetch(macPage, `${mac.origin}/api/v1/computers`, { method: 'POST', credentials: 'include', headers: json, body: JSON.stringify({ label: 'vps', origin: vps.origin, state: 'pending' }) });
  const redeem = await pageFetch(macPage, `${vps.origin}/api/v1/pair/redeem`, { method: 'POST', credentials: 'include', headers: json, body: JSON.stringify({ grant, deviceLabel: 'e2e phone' }) });
  const redeemed = JSON.parse(redeem.body || '{}');
  const [, code] = await via.wait(/shows code (\d{3}-\d{3})/);
  const prompt = via.out.slice(via.out.indexOf('Phone'));
  via.end(code === redeemed.verificationCode ? 'y\n' : 'n\n');
  const poll = await until(async () => {
    const r = await pageFetch(macPage, `${vps.origin}/api/v1/pair/poll`, { method: 'POST', credentials: 'include', headers: json, body: JSON.stringify({ pendingId: redeemed.pendingId, pollSecret: redeemed.pollSecret }) });
    return JSON.parse(r.body || '{}').status === 'approved' && r;
  }, { what: 'approved poll' });
  const approvedAt = Date.now();
  const first = await pageFetch(macPage, SETTINGS, { credentials: 'include' });
  // The app (addComputer.ts finish) reads settings once, at once. Measure
  // how long the peer's CORS lags the approval.
  const firstOk = first.status === 200 ? first : await until(async () => {
    const r = await pageFetch(macPage, SETTINGS, { credentials: 'include' });
    return r.status === 200 && r;
  }, { timeout: 10000, every: 100, what: 'first settings 200' });
  const lag = Date.now() - approvedAt;
  const confirm = await pageFetch(macPage, `${mac.origin}/api/v1/computers`, { method: 'POST', credentials: 'include', headers: json, body: JSON.stringify({ label: 'vps', origin: vps.origin, state: 'confirmed' }) });
  check('A3', 'Bound pairing mac app → vps over real CORS (API level, no screens)',
    pending.status === 200 && redeem.status === 200 && poll.status === 200 && firstOk.status === 200 && confirm.status === 200 && await via.done === 0,
    `link ${link.replace(/#.*/, '#…')}; pending ${pending.status}, redeem ${redeem.status}, approve prompt "${prompt.split('\n')[0].trim()}", poll ${poll.status}, settings 200 after ${lag} ms, confirm ${confirm.status}; ${Date.now() - started} ms`);
  check('A3-lag', 'The first settings read right after approval works (what the app does)', first.status === 200,
    `first read: ${first.error ?? first.status}; 200 only ${lag} ms after the approved poll`);

  // ---- A4: no-cors and cors GETs from unbound same-site pages ----
  const evil = await browser.page();
  await evil.go(`https://evil.${'tailx.ts.net'}/`);
  mark = env.logMark();
  const nc = await pageFetch(evil, SETTINGS, { mode: 'no-cors', credentials: 'include' });
  const cors = await pageFetch(evil, SETTINGS, { credentials: 'include' });
  await sleep(300);
  const [ncLog, corsLog] = env.logSince(mark).filter((e) => e.host === vps.host);
  check('A4', 'no-cors GET from an unbound same-site page (evil) is refused, no cookie refresh',
    ncLog?.cookie && ncLog.status === 403 && !ncLog.setCookie && nc.type === 'opaque',
    `page sees ${nc.type ?? nc.error}; ${brief(ncLog)}`);
  check('A4b', 'cors GET from an unbound same-site page (evil) is refused, no cookie refresh',
    corsLog?.cookie && corsLog.status === 403 && !corsLog.setCookie && !corsLog.acao && cors.error,
    `page sees ${cors.error ?? cors.status}; ${brief(corsLog)}`);
  mark = env.logMark();
  const ncBound = await pageFetch(macPage, SETTINGS, { mode: 'no-cors', credentials: 'include' });
  const ok = await pageFetch(macPage, SETTINGS, { credentials: 'include' });
  await sleep(300);
  const [ncBoundLog, okLog] = env.logSince(mark).filter((e) => e.host === vps.host);
  check('A4c', 'no-cors GET from the bound app (mac) is refused too, no cookie refresh',
    ncBoundLog?.status === 403 && !ncBoundLog.setCookie, `${brief(ncBoundLog)}`);
  check('A4d', 'Control: cors GET from the bound app (mac) works and refreshes the cookie',
    ok.status === 200 && okLog?.status === 200 && okLog.acao === mac.origin && !!okLog.setCookie, `${brief(okLog)} (${okLog?.setCookie})`);

  // ---- A5: near-miss and null origins ----
  for (const host of ['xmac.tailx.ts.net', 'mac.tailx.ts.net.evil.com', 'mac-tailx.ts.net']) {
    const p = await browser.page();
    await p.go(`https://${host}/`);
    mark = env.logMark();
    const r = await pageFetch(p, SETTINGS, { credentials: 'include' });
    await sleep(200);
    const e = env.logSince(mark).find((x) => x.host === vps.host);
    check(`A5-${host}`, `Near-miss page https://${host} is refused (Chrome)`, r.error && e?.status === 403 && !e.acao && !e.setCookie, `page sees ${r.error ?? r.status}; ${brief(e)}`);
    await p.close();
  }
  mark = env.logMark();
  const nullResult = await evil.eval(`new Promise((res) => { const f = document.createElement('iframe'); f.sandbox = 'allow-scripts';
    f.srcdoc = "<script>fetch('${SETTINGS}', { credentials: 'include' }).then((r) => parent.postMessage('status ' + r.status, '*'), (e) => parent.postMessage(String(e), '*'))<\/script>";
    addEventListener('message', (e) => res(e.data)); document.body.append(f); })`);
  await sleep(200);
  const nullLog = env.logSince(mark).find((x) => x.host === vps.host);
  check('A5-null-chrome', 'Origin null (sandboxed iframe) is refused (Chrome)', nullLog?.origin === 'null' && nullLog.status === 403 && !nullLog.acao, `page sees ${nullResult}; ${brief(nullLog)}`);

  const cookie = (await browser.cookies()).find((c) => c.domain === vps.host);
  const jar = `${cookie.name}=${cookie.value}`;
  const browserLike = { 'Sec-Fetch-Site': 'same-site', 'Sec-Fetch-Mode': 'cors', Cookie: jar };
  const exact = await env.curl(SETTINGS, { headers: { ...browserLike, Origin: mac.origin } });
  check('A5-control', 'Control: exact bound origin https://mac.tailx.ts.net with the vps cookie works (curl)',
    exact.status === 200 && exact.headers['access-control-allow-origin'] === mac.origin, `${exact.status}, ACAO=${exact.headers['access-control-allow-origin']}`);
  const nearMisses = ['null', 'https://xmac.tailx.ts.net', 'https://mac.tailx.ts.net.evil.com', 'https://mac.tailx.ts.net:443',
    'https://mac.tailx.ts.net.', 'http://mac.tailx.ts.net', 'https://MAC.tailx.ts.net', 'https://mac.tailx.ts.net/',
    'https://evil.tailx.ts.net', 'https://mac.taily.ts.net'];
  for (const origin of nearMisses) {
    const get = await env.curl(SETTINGS, { headers: { ...browserLike, Origin: origin } });
    const opt = await env.curl(SETTINGS, { method: 'OPTIONS', headers: { Origin: origin, 'Access-Control-Request-Method': 'GET' } });
    check(`A5 ${JSON.stringify(origin)}`, `Forged Origin ${JSON.stringify(origin)} with the vps cookie is refused (curl)`,
      refused(get) && refused(opt), `GET ${get.status} ACAO=${get.headers['access-control-allow-origin'] ?? 'none'} Set-Cookie=${get.headers['set-cookie'] ? 'yes' : 'no'}; OPTIONS ${opt.status}`);
  }
  const twice = await env.curl(SETTINGS, { headers: { ...browserLike, Origin: mac.origin, 'origin': mac.origin } });
  const dup = await env.curl(SETTINGS, { headers: { ...browserLike, Origin: `${mac.origin}, ${mac.origin}` } });
  check('A5-two', 'Two Origin headers, or one with a list of two, are refused (curl)', refused(dup) && refused(twice), `two headers ${twice.status}; list ${dup.status}`);

  // A bound grant is refused from any other origin.
  const other = interactive(vps, ['pair', '--via', 'mac', '--no-wait']);
  const [, grant2] = await other.wait(/add-computer#grant=([^&\s]+)/);
  other.end();
  await other.done;
  await sleep(1500);
  mark = env.logMark();
  const evilRedeem = await pageFetch(evil, `${vps.origin}/api/v1/pair/redeem`, { method: 'POST', credentials: 'include', headers: json, body: JSON.stringify({ grant: grant2, deviceLabel: 'evil' }) });
  await sleep(200);
  const evilPre = env.logSince(mark).find((x) => x.host === vps.host);
  const forgedRedeem = await env.curl(`${vps.origin}/api/v1/pair/redeem`, { method: 'POST', headers: { Origin: 'https://evil.tailx.ts.net', 'Sec-Fetch-Site': 'same-site', 'Sec-Fetch-Mode': 'cors', ...json }, body: JSON.stringify({ grant: grant2, deviceLabel: 'evil' }) });
  check('A5-grant', 'A grant bound to mac is refused from evil.tailx.ts.net (Chrome preflight and forged curl POST)',
    evilRedeem.error && evilPre?.status === 403 && !evilPre.acao && forgedRedeem.status === 403,
    `page sees ${evilRedeem.error ?? evilRedeem.status}; ${brief(evilPre)}; curl POST ${forgedRedeem.status}`);

  // ---- A6: /api without Sec-Fetch-Site and without a bound Origin ----
  const bare = await env.curl(SETTINGS, { headers: { Cookie: jar } });
  const macCookie = (await browser.cookies()).find((c) => c.domain === mac.host);
  const bareMac = await env.curl(`${mac.origin}/api/v1/settings`, { headers: { Cookie: `${macCookie.name}=${macCookie.value}` } });
  const sameOrigin = await env.curl(`${mac.origin}/api/v1/settings`, { headers: { Cookie: `${macCookie.name}=${macCookie.value}`, 'Sec-Fetch-Site': 'same-origin' } });
  check('A6', '/api with a valid cookie but no Sec-Fetch-Site and no Origin → 403 (vps and mac)',
    refused(bare) && refused(bareMac), `vps ${bare.status}, mac ${bareMac.status}; control with Sec-Fetch-Site: same-origin on mac → ${sameOrigin.status}`);

  // ---- A7: pterm doctor on both ----
  for (const stack of [mac, vps]) {
    const d = await stack.doctor();
    const text = d.stdout + d.stderr;
    writeFileSync(join(out, `a-doctor-${stack.name}.txt`), text.replaceAll(`/private${env.root}`, '<root>').replaceAll(env.root, '<root>'));
    const wantCors = stack === vps ? 'multi-computer: CORS list (bound app origins): https://mac.tailx.ts.net' : 'multi-computer: no bound app origins (CORS off)';
    const good = text.includes(`tailscale: Running, host ${stack.host}`) && text.includes(wantCors) &&
      text.includes('broker: running') && text.includes('protocol revision') && !text.includes('! multi-computer');
    check(`A7-${stack.name}`, `pterm doctor on ${stack.name} reports tailnet, CORS list and protocol revision`, good,
      text.split('\n').filter((l) => /tailscale:|multi-computer|protocol revision|broker:|gateway:/.test(l)).map((l) => l.trim()).join(' | '));
  }

  // Doctor must also report problems: an owner mismatch of the bound app
  // node (vps) and another Serve handler on the home origin (mac).
  vps.writeStatus({ ...vps.nodes, mac: { ...vps.nodes.mac, user: 2 } });
  const owner = await vps.doctor();
  vps.writeStatus(NODES);
  check('A7-owner', 'pterm doctor on vps reports an owner mismatch of the bound app computer',
    (owner.stdout + owner.stderr).includes('! multi-computer: https://mac.tailx.ts.net: owner mismatch ("mallory@example.com"; this computer: "ada@example.com")'),
    (owner.stdout + owner.stderr).split('\n').filter((l) => l.includes('multi-computer')).map((l) => l.trim()).join(' | '));
  const serve = join(mac.tsDir, 'serve.json');
  const saved = readFileSync(serve, 'utf8');
  const extra = JSON.parse(saved);
  extra.Web['mac.tailx.ts.net:443'].Handlers['/files'] = { Proxy: 'http://127.0.0.1:3000' };
  writeFileSync(serve, JSON.stringify(extra));
  const handlers = await mac.doctor();
  writeFileSync(serve, saved);
  const handlerLine = (handlers.stdout + handlers.stderr).split('\n').find((l) => l.includes('other handlers share')) ?? 'no warning';
  check('A7-serve', 'pterm doctor on mac warns about another Serve handler on the home origin', handlerLine.includes('/files'), handlerLine.trim());
} catch (error) {
  check('ERR', 'harness error', false, error.stack);
} finally {
  const table = ['| ID | Check | Result | Evidence |', '| --- | --- | --- | --- |',
    ...results.map((r) => `| ${r.id} | ${r.name} | ${r.result} | ${String(r.evidence).replaceAll('|', '\\|').replaceAll('\n', ' ')} |`)].join('\n');
  writeFileSync(join(out, 'phase-a.md'), `${table}\n`);
  writeFileSync(join(out, 'phase-a.json'), JSON.stringify({ chrome: browser.product, results }, null, 1));
  console.log(`\n${table}\n\nevidence: ${out}`);
  if (flag('--keep')) {
    console.log(`kept running; front ${env.proxy} spki ${env.spki}. Ctrl+C stops.`);
    await new Promise((r) => process.once('SIGINT', r));
  }
  await browser.close();
  await env.close();
  process.exit(results.every((r) => r.result === 'PASS') ? 0 : 1);
}
