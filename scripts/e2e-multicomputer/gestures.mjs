// Touch gestures on the Live terminal (docs/gestures.md) in WebKit with touch:
// hold and slide, tap to place the cursor, pinch for text size, two-finger
// tap for Esc, swipe on the status line, the grey-suggestion guard, and the
// Settings switch off. Touches are synthetic touch events sent in the page; a
// count of the WebSocket messages the page sends proves which keys went out.
//   PW=<playwright-core dir> [WEBKIT=<browser executable>] node scripts/e2e-multicomputer/gestures.mjs [--no-web-build] [--out <dir>]
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Env, interactive, sleep, until } from './lib.mjs';

const argv = process.argv.slice(2);
const option = (name) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : undefined);
const { webkit } = await import(join(process.env.PW, 'index.mjs'));

const results = [];
const check = (id, name, pass, evidence) => {
  results.push({ id, name, result: pass ? 'PASS' : 'FAIL', evidence: String(evidence) });
  console.log(`${pass ? 'PASS' : 'FAIL'} ${id} ${name}\n     ${evidence}`);
};

const env = await Env.create({ buildWeb: !argv.includes('--no-web-build'), names: ['mac'] });
const { mac } = env.stacks;
const out = option('--out') ?? join(env.root, 'evidence');
mkdirSync(out, { recursive: true });
mkdirSync(join(mac.home, 'demo'), { recursive: true });
await mac.cli(['project', 'add', '--name', 'demo', join(mac.home, 'demo')]);
const browser = await webkit.launch({ proxy: { server: `http://${env.proxy}` }, ...(process.env.WEBKIT ? { executablePath: process.env.WEBKIT } : {}) });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
// Every message the page sends on a WebSocket: its type and, for keys, the key.
await context.addInitScript(() => {
  window.sent = [];
  const send = WebSocket.prototype.send;
  WebSocket.prototype.send = function (data) {
    try { const message = JSON.parse(data); window.sent.push({ type: message.type, key: message.payload?.key }); } catch { /* not JSON */ }
    return send.call(this, data);
  };
});
const page = await context.newPage();
const text = () => page.evaluate(() => document.body.innerText);
const terminal = () => page.evaluate(() => [...document.querySelectorAll('.terminal-host .xterm-rows')].map((e) => e.innerText).join('\n'));
const code = () => page.evaluate(() => document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', ''));
const mark = () => page.evaluate(() => window.sent.length);
const keysSince = (from) => page.evaluate((at) => window.sent.slice(at).filter((m) => m.type === 'input.key').map((m) => m.key), from);
const resizesSince = (from) => page.evaluate((at) => window.sent.slice(at).filter((m) => m.type === 'terminal.resize').length, from);
const pill = () => page.evaluate(() => document.querySelector('.gesture-pill')?.textContent ?? null);
const font = () => page.evaluate(() => localStorage.getItem('pterm.fontSize'));
/** The box of character `index` of the last terminal row that contains `line`. */
const charBox = (line, index) => page.evaluate(([want, at]) => {
  const rows = [...document.querySelectorAll('.terminal-host .xterm-rows > div')].filter((row) => row.textContent.includes(want));
  const row = rows.at(-1);
  if (row === undefined) return undefined;
  const offset = row.textContent.indexOf(want) + at;
  const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT);
  let seen = 0;
  for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
    if (offset < seen + node.length) {
      const range = document.createRange();
      range.setStart(node, offset - seen);
      range.setEnd(node, offset - seen + 1);
      // The text box can be taller than the row: take the height from the row.
      const box = range.getBoundingClientRect();
      const line = row.getBoundingClientRect();
      return { x: box.left + box.width / 2, y: line.top + line.height / 2, w: box.width };
    }
    seen += node.length;
  }
  return undefined;
}, [line, index]);
let touchKind;
/** One synthetic touch event on a selector; points are [id, x, y]. */
async function touch(selector, type, touches, changed = touches) {
  const { real, prevented } = await page.evaluate(([sel, kind, now, gone]) => {
    const target = document.querySelector(sel);
    // WebKit has TouchEvent but no Touch constructor: then a plain event carries the touch lists.
    let native = typeof TouchEvent === 'function' && typeof Touch === 'function';
    try { if (native) new Touch({ identifier: 0, target }); } catch { native = false; }
    const make = ([identifier, clientX, clientY]) => (native ? new Touch({ identifier, target, clientX, clientY }) : { identifier, target, clientX, clientY });
    let event;
    if (native) {
      event = new TouchEvent(kind, { bubbles: true, cancelable: true, touches: now.map(make), targetTouches: now.map(make), changedTouches: gone.map(make) });
    } else {
      event = new Event(kind, { bubbles: true, cancelable: true });
      Object.defineProperties(event, { touches: { value: now.map(make) }, changedTouches: { value: gone.map(make) } });
    }
    target.dispatchEvent(event);
    return { real: native, prevented: event.defaultPrevented };
  }, [selector, type, touches, changed]);
  touchKind = real ? 'native TouchEvent' : 'Event with touch lists';
  return prevented;
}
const AREA = '.terminal-area .xterm-screen';
async function type(line) {
  await page.focus('.terminal-input');
  await page.keyboard.insertText(line);
  await page.keyboard.press('Enter');
}
/** Hold one finger HOLD_MS+, then slide left by `cells` steps. */
async function holdAndSlide(x, y, steps, step) {
  await touch(AREA, 'touchstart', [[1, x, y]]);
  await sleep(600);
  const armed = await pill();
  // 12 px locks the axis (one step when a step is 10 to 12 px), then one step per move.
  let at = x - 12;
  await touch(AREA, 'touchmove', [[1, at, y]]);
  const sent = [Math.trunc(12 / step)];
  for (let move = sent[0]; move < steps; move++) {
    at -= step;
    await touch(AREA, 'touchmove', [[1, at, y]]);
  }
  return { armed, end: () => touch(AREA, 'touchend', [], [[1, at, y]]) };
}
async function twoFingerTap(x, y) {
  await touch(AREA, 'touchstart', [[1, x, y]]);
  await touch(AREA, 'touchstart', [[1, x, y], [2, x + 80, y]], [[2, x + 80, y]]);
  await sleep(80);
  await touch(AREA, 'touchend', [[2, x + 80, y]], [[1, x, y]]);
  return touch(AREA, 'touchend', [], [[2, x + 80, y]]);
}
async function pinch(x, y, from, to) {
  await touch(AREA, 'touchstart', [[1, x, y], [2, x + from, y]]);
  for (let step = 1; step <= 4; step++) await touch(AREA, 'touchmove', [[1, x, y], [2, x + from + ((to - from) * step) / 4, y]]);
  await sleep(200);
  const shown = await pill();
  await touch(AREA, 'touchend', [[2, x + to, y]], [[1, x, y]]);
  await touch(AREA, 'touchend', [], [[2, x + to, y]]);
  return shown;
}
async function swipe(fromX, toX, y) {
  await touch('.status-line', 'touchstart', [[1, fromX, y]]);
  await sleep(100);
  await touch('.status-line', 'touchend', [], [[1, toX, y]]);
}
console.log(`WebKit ${browser.version()}`);
try {
  // Pair with mac.
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

  // Two sessions, so a swipe has somewhere to go.
  const ids = await page.evaluate(async () => {
    const settings = await fetch('/api/v1/settings', { credentials: 'include' }).then((r) => r.json());
    const made = [];
    for (let n = 0; n < 2; n++) {
      const answer = await fetch('/api/v1/sessions', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' },
        body: JSON.stringify({ operationId: crypto.randomUUID(), projectId: settings.projects[0].id, shellProfileId: settings.shellProfiles[0].id }) }).then((r) => r.json());
      made.push(answer.session?.id ?? answer.id);
    }
    return made;
  });
  await page.goto(`${mac.origin}/sessions/${encodeURIComponent(ids[0])}`);
  await until(async () => /[%$#>]\s*$/m.test(await terminal()), { what: 'prompt', timeout: 30000 });
  if ((await text()).includes('Take control')) await page.getByRole('button', { name: 'Take control' }).click();
  await until(async () => (await text()).includes('Control'), { what: 'control' });
  await sleep(500);
  // A neutral prompt (no user or host name in screenshots), for sh, bash and zsh.
  await type("PS1='$ '; PROMPT='$ '; clear");
  await until(async () => /^\$\s*$/m.test(await terminal()), { what: 'neutral prompt' });

  // G1 hold and slide: three ← after "echo 12345", then X.
  await page.focus('.terminal-input');
  await page.keyboard.insertText('echo 12345');
  await until(async () => (await terminal()).includes('echo 12345'), { what: 'typed' });
  await page.evaluate(() => document.activeElement?.blur());
  const width = await page.evaluate(() => { const s = document.querySelector('.terminal-area .xterm-screen'); return s.clientWidth; });
  const columns = await page.evaluate(() => document.querySelector('.status-line')?.innerText.match(/(\d+)×\d+/)?.[1]);
  const step = Math.max(width / Number(columns), 10);
  let from = await mark();
  const slide = await holdAndSlide(200, 300, 3, step);
  await page.screenshot({ path: join(out, 'gesture-trackpad-pill.png') });
  const blocked = await slide.end();
  const slideKeys = await keysSince(from);
  const pillAfter = await pill();
  await page.focus('.terminal-input');
  await page.keyboard.insertText('X');
  await page.keyboard.press('Enter');
  const slid = await until(async () => /^12X345\s*$/m.test(await terminal()) && 'ok', { what: '12X345', timeout: 10000 }).catch(() => 'missing');
  check('GE1', 'Hold and slide: the pill shows, three ← go out, the click is taken, the pill goes', slid === 'ok' && slide.armed === '← → ↑ ↓' && pillAfter === null
    && slideKeys.join() === 'arrow-left,arrow-left,arrow-left' && blocked,
  `${touchKind}; step ${step.toFixed(1)} px; pill "${slide.armed}"; keys ${slideKeys.join(' ')}; touchend prevented ${blocked}; output 12X345: ${slid}`);

  // G5 two-finger tap: one Esc, seen by cat -v.
  await type('cat -v');
  await sleep(500);
  from = await mark();
  const tapTaken = await twoFingerTap(150, 300);
  await page.focus('.terminal-input');
  await page.keyboard.press('Enter');
  const esc = await until(async () => (await terminal()).includes('^['), { what: '^[', timeout: 10000 }).then(() => true).catch(() => false);
  const escKeys = await keysSince(from);
  await page.getByRole('button', { name: 'Control C' }).click();
  check('GE2', 'Two-finger tap sends exactly one Esc (cat -v shows ^[)', esc && escKeys.filter((k) => k === 'escape').length === 1 && tapTaken,
    `keys ${escKeys.join(' ')}; ^[ shown ${esc}; touchend prevented ${tapTaken}`);

  // G4 pinch: 100 → 200 px is 14 → 20 px, applied on lift, one resize.
  await sleep(500);
  const fontBefore = (await font()) ?? '14';
  from = await mark();
  const pinchPill = await pinch(100, 400, 100, 200);
  await sleep(1500);
  const fontAfter = await font();
  const resizes = await resizesSince(from);
  check('GE3', 'Pinch: a live "20 px" pill, the size set on lift, exactly one resize', pinchPill === '20 px' && fontAfter === '20' && resizes === 1 && await pill() === null,
    `font ${fontBefore} → ${fontAfter}; pill "${pinchPill}"; terminal.resize messages ${resizes}`);
  await touch(AREA, 'touchstart', [[1, 100, 400], [2, 220, 400]]);
  await touch(AREA, 'touchmove', [[1, 100, 400], [2, 260, 400]]);
  await page.screenshot({ path: join(out, 'gesture-pinch-pill.png') });
  await touch(AREA, 'touchcancel', [], [[1, 100, 400], [2, 260, 400]]);

  // G2 tap to place: with the keyboard up (a shorter viewport), a tap on "a" of "echo abc" sends three ←.
  await page.setViewportSize({ width: 390, height: 450 });
  await sleep(800);
  await page.focus('.terminal-input');
  await page.keyboard.insertText('echo abc');
  await until(async () => (await terminal()).includes('echo abc'), { what: 'typed abc' });
  await sleep(300);
  const a = await charBox('echo abc', 5);
  from = await mark();
  await page.mouse.click(a.x, a.y);
  await sleep(300);
  const tapKeys = await keysSince(from);
  const liveClass = await page.evaluate(() => document.querySelector('main.live')?.className);
  await page.keyboard.insertText('X');
  await page.keyboard.press('Enter');
  const placed = await until(async () => /^Xabc\s*$/m.test(await terminal()) && 'ok', { what: 'Xabc', timeout: 10000 }).catch(() => 'missing');
  check('GE4', 'Tap to place: a tap on "a" moves the cursor there; X makes Xabc', placed === 'ok' && tapKeys.join() === 'arrow-left,arrow-left,arrow-left',
    `${liveClass}; tap at ${a.x.toFixed(0)},${a.y.toFixed(0)}; cell ${a.w.toFixed(1)} px wide; keys ${tapKeys.join(' ')}; output Xabc: ${placed}`);

  // The guard: grey (dim) text right of the cursor, like an autosuggestion. A tap on it sends nothing; a tap on "b" one ←.
  await type("printf '$ ab\\033[2mcd\\033[0m\\033[2D'; read x");
  await sleep(800);
  // The row is "$ abcd" (a prompt, then text) with the cursor on "c"; "cd" is dim.
  const d = await charBox('abcd', 3);
  const b = await charBox('abcd', 1);
  from = await mark();
  await page.mouse.click(d.x, d.y);
  await sleep(300);
  const dimKeys = await keysSince(from);
  from = await mark();
  await page.mouse.click(b.x, b.y);
  await sleep(300);
  const leftKeys = await keysSince(from);
  await page.keyboard.press('Enter');
  // No shell prompt on the row (a y/N question read by `read`): a tap sends nothing, so no arrow lands as input.
  await type("printf 'Continue? [y/N] '; read x");
  await sleep(500);
  await page.keyboard.insertText('abc');
  await until(async () => (await terminal()).includes('[y/N] abc'), { what: 'typed answer' });
  const answer = await charBox('[y/N] abc', 6);
  from = await mark();
  await page.mouse.click(answer.x, answer.y);
  await sleep(300);
  const answerKeys = await keysSince(from);
  await page.keyboard.press('Enter');
  check('GE5', 'A tap on dim text right of the cursor sends nothing; a tap on the text to its left does', dimKeys.length === 0 && leftKeys.join() === 'arrow-left',
    `on the dim "d": keys [${dimKeys.join(' ')}]; on "b": keys [${leftKeys.join(' ')}]`);
  check('GE8', 'A tap on a line without a shell prompt (Continue? [y/N]) sends nothing', answerKeys.length === 0, `keys [${answerKeys.join(' ')}]`);

  // G3 swipe on the status line: to the next session.
  await page.setViewportSize({ width: 390, height: 844 });
  await sleep(800);
  const before = new URL(page.url()).pathname;
  const tabs = await page.evaluate(() => [...document.querySelectorAll('.tab-scroll .tab')].map((tab) => tab.className));
  await swipe(150, 300, await page.evaluate(() => { const b = document.querySelector('.status-line').getBoundingClientRect(); return b.top + b.height / 2; }));
  const after = await until(async () => { const path = new URL(page.url()).pathname; return path !== before && path; }, { what: 'switch', timeout: 10000 }).catch(() => before);
  check('GE6', 'A swipe right on the status line opens the session before (the tab to the left)', after === `/sessions/${encodeURIComponent(ids[1])}`, `tabs ${tabs.join('/')}; ${before} → ${after}`);

  // The switch: Settings → This phone → Terminal gestures.
  await page.goto(`${mac.origin}/`);
  await page.getByRole('button', { name: 'Settings' }).click();
  const toggle = page.getByRole('switch', { name: 'Terminal gestures' });
  await toggle.scrollIntoViewIfNeeded();
  const on = await toggle.getAttribute('aria-checked');
  await page.screenshot({ path: join(out, 'gesture-settings-switch.png') });
  await toggle.click();
  const off = await toggle.getAttribute('aria-checked');
  const stored = await page.evaluate(() => localStorage.getItem('pterm.gestures'));
  await page.goto(`${mac.origin}/sessions/${encodeURIComponent(ids[0])}`);
  await until(async () => /\$\s*$/m.test(await terminal()), { what: 'prompt again', timeout: 30000 });
  if ((await text()).includes('Take control')) await page.getByRole('button', { name: 'Take control' }).click();
  await until(async () => (await text()).includes('Control'), { what: 'control again' });
  await sleep(500);
  const fontOff = await font();
  from = await mark();
  const offSlide = await holdAndSlide(200, 300, 3, step);
  await offSlide.end();
  await twoFingerTap(150, 300);
  await pinch(100, 400, 100, 200);
  const path = new URL(page.url()).pathname;
  await swipe(300, 150, await page.evaluate(() => { const b = document.querySelector('.status-line').getBoundingClientRect(); return b.top + b.height / 2; }));
  await sleep(1500);
  const offKeys = await keysSince(from);
  check('GE7', 'Switch off: no pill, no keys, no text size change, no switch', on === 'true' && off === 'false' && stored === 'off' && offSlide.armed === null
    && offKeys.length === 0 && await font() === fontOff && new URL(page.url()).pathname === path,
  `switch ${on} → ${off} (stored ${stored}); pill ${offSlide.armed}; keys [${offKeys.join(' ')}]; font ${fontOff} → ${await font()}; path ${path} → ${new URL(page.url()).pathname}`);
} catch (error) {
  await page.screenshot({ path: join(out, 'gestures-fail.png') }).catch(() => undefined);
  check('ERR', 'Gestures harness error', false, `${error.message.split('\n')[0]} | ${(await terminal().catch(() => '')).replace(/\s+/g, ' ').slice(-300)}`);
} finally {
  const table = ['| ID | Check | Result | Evidence |', '| --- | --- | --- | --- |',
    ...results.map((r) => `| ${r.id} | ${r.name} | ${r.result} | ${r.evidence.replaceAll('|', '\\|')} |`)].join('\n');
  writeFileSync(join(out, 'gestures.md'), `WebKit ${browser.version()}\n\n${table}\n`);
  console.log(`\n${table}`);
  await browser.close();
  await env.close();
  process.exit(results.length === 8 && results.every((r) => r.result === 'PASS') ? 0 : 1);
}
