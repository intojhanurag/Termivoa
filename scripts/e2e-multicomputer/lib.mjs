// Shared parts of the multi-computer e2e harness (README.md): two isolated
// dev stacks behind the HTTPS front (main.go), a fake Tailscale, curl through
// the front, and a small CDP client for headless Chrome. Node built-ins only.
import { spawn } from 'node:child_process';
import { appendFileSync, chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import net from 'node:net';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO = resolve(HERE, '../..');
export const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
export const TAILNET = 'tailx.ts.net';
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// Ports the harness must never use: the installed pterm and its previews.
const RESERVED = new Set([8787, 8788, 8789, 8443]);

export async function freePort() {
  for (;;) {
    const port = await new Promise((res, rej) => {
      const server = net.createServer().listen(0, '127.0.0.1', () => {
        const { port } = server.address();
        server.close(() => res(port));
      }).on('error', rej);
    });
    if (!RESERVED.has(port)) return port;
  }
}

/** Runs a command to completion. input is written to stdin. */
export function run(cmd, args, { env = process.env, input, cwd = REPO, timeout = 120000 } = {}) {
  return new Promise((res, rej) => {
    const child = spawn(cmd, args, { env, cwd, stdio: ['pipe', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    const timer = setTimeout(() => child.kill('SIGKILL'), timeout);
    child.stdout.on('data', (d) => { stdout += d; });
    child.stderr.on('data', (d) => { stderr += d; });
    child.on('error', rej);
    child.on('close', (code) => { clearTimeout(timer); res({ code, stdout, stderr }); });
    child.stdin.end(input ?? '');
  });
}

async function must(cmd, args, options) {
  const result = await run(cmd, args, options);
  if (result.code !== 0) throw new Error(`${cmd} ${args.join(' ')} failed (${result.code}):\n${result.stdout}${result.stderr}`);
  return result;
}

/** Waits until fn returns a truthy value. */
export async function until(fn, { timeout = 20000, every = 200, what = 'condition' } = {}) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try {
      const value = await fn();
      if (value) return value;
    } catch (error) {
      last = error;
    }
    await sleep(every);
  }
  throw new Error(`timeout waiting for ${what}${last ? `: ${last.message}` : ''}`);
}

// ---------- fake Tailscale ----------

/** Demo tailnet: mac and vps share an owner; evil is another user's node. */
export const NODES = {
  mac: { ID: 'nMacE2E1CNTRL', user: 1 },
  vps: { ID: 'nVpsE2E1CNTRL', user: 1 },
  evil: { ID: 'nEvilE2E1CNTRL', user: 2 },
};
const USERS = { 1: { LoginName: 'ada@example.com' }, 2: { LoginName: 'mallory@example.com' } };

function statusJSON(self, nodes) {
  const node = (name, index) => ({
    ID: nodes[name].ID, PublicKey: `nodekey:e2e${name}`, HostName: name, DNSName: `${name}.${TAILNET}.`,
    UserID: nodes[name].user, TailscaleIPs: [`100.64.0.${index + 1}`], Online: true,
  });
  const names = Object.keys(nodes);
  const peers = Object.fromEntries(names.filter((n) => n !== self).map((n) => [`nodekey:e2e${n}`, node(n, names.indexOf(n))]));
  return {
    Version: 'e2e-fake', BackendState: 'Running', MagicDNSSuffix: TAILNET,
    CurrentTailnet: { Name: 'ada@example.com', MagicDNSSuffix: TAILNET, MagicDNSEnabled: true },
    Self: node(self, names.indexOf(self)), Peer: peers, User: USERS,
  };
}

// ---------- the environment ----------

export class Stack {
  constructor(env, name) {
    this.env = env;
    this.name = name;
    this.host = `${name}.${TAILNET}`;
    this.origin = `https://${this.host}`;
    const dir = join(env.root, name);
    this.dir = dir;
    this.home = join(dir, 'home');
    // `pterm doctor` reads only the installed (production) paths, which
    // follow HOME. The dev stack lives exactly where doctor looks under
    // doctorHome, and doctor runs with HOME=doctorHome (README, "doctor").
    this.doctorHome = join(dir, 'dh');
    this.dataDir = join(this.doctorHome, 'Library', 'Application Support', 'Phone Terminal');
    this.runtimeDir = join(this.doctorHome, 'Library', 'Caches', 'Phone Terminal', 'runtime');
    this.tsDir = join(dir, 'tailscale');
    this.bin = join(dir, 'bin');
    this.nodes = structuredClone(NODES);
    for (const path of [this.home, this.bin, this.tsDir, dirname(this.dataDir), dirname(this.runtimeDir)]) mkdirSync(path, { recursive: true, mode: 0o700 });
    copyFileSync(join(HERE, 'tailscale'), join(this.bin, 'tailscale'));
    chmodSync(join(this.bin, 'tailscale'), 0o755);
    this.writeStatus();
  }

  /** Rewrites this computer's fake `tailscale status --json`. */
  writeStatus(nodes = this.nodes) {
    this.nodes = nodes;
    writeFileSync(join(this.tsDir, 'status.json'), JSON.stringify(statusJSON(this.name, nodes), null, 1));
    // What a correctly set-up computer reports: HTTPS 443 → the installed gateway.
    writeFileSync(join(this.tsDir, 'serve.json'), JSON.stringify({
      TCP: { 443: { HTTPS: true } },
      Web: { [`${this.host}:443`]: { Handlers: { '/': { Proxy: 'http://127.0.0.1:8787' } } } },
    }));
  }

  get processEnv() {
    return {
      PATH: `${this.bin}:${this.env.bin}:/usr/bin:/bin:/usr/sbin:/sbin`,
      HOME: this.home, SHELL: '/bin/zsh', TERM: 'xterm-256color', LANG: 'en_US.UTF-8',
      TMPDIR: join(this.dir, 'tmp'), PTERM_E2E_TAILSCALE_DIR: this.tsDir,
    };
  }

  async start() {
    mkdirSync(join(this.dir, 'tmp'), { recursive: true });
    this.port ??= await freePort();
    this.gateway = `127.0.0.1:${this.port}`;
    const log = join(this.env.root, `${this.name}.log`);
    const child = spawn(this.env.pterm, ['up', '--dev', '--data-dir', this.dataDir, '--runtime-dir', this.runtimeDir,
      '--listen', this.gateway, '--preview-listen', 'off'], { env: this.processEnv, cwd: this.home, stdio: ['ignore', 'pipe', 'pipe'] });
    this.child = child;
    let out = '';
    const sink = (d) => { out += d; appendFileSync(log, d); };
    child.stdout.on('data', sink);
    child.stderr.on('data', sink);
    await until(() => out.includes('Development gateway:') || child.exitCode !== null, { timeout: 30000, what: `${this.name} up` });
    if (child.exitCode !== null) throw new Error(`${this.name} exited:\n${out}`);
    await this.cli(['config', 'origin', this.origin]);
    return this;
  }

  /** Ctrl+C on `pterm up`: the gateway stops, then the broker. */
  async stop() {
    if (!this.child || this.child.exitCode !== null) return;
    const exited = new Promise((r) => this.child.once('exit', r));
    this.child.kill('SIGINT');
    await Promise.race([exited, sleep(10000).then(() => this.child.kill('SIGKILL'))]);
  }

  /** Runs an admin or local pterm command against this stack. */
  cli(args, options = {}) {
    return must(this.env.pterm, [...args, '--dev', '--runtime-dir', this.runtimeDir], { env: this.processEnv, ...options });
  }

  /**
   * `pterm doctor` on this stack. It runs with HOME=doctorHome and inside a
   * macOS sandbox that blocks 127.0.0.1:8787 and launchctl, so it can never
   * reach the installed gateway or read the real launchd jobs.
   */
  doctor() {
    const profile = '(version 1)(allow default)(deny network-outbound (remote tcp "localhost:8787"))(deny process-exec (literal "/bin/launchctl"))';
    return run('/usr/bin/sandbox-exec', ['-p', profile, this.env.pterm, 'doctor'], { env: { ...this.processEnv, HOME: this.doctorHome } });
  }
}

export class Env {
  /** Builds pterm and the front, then starts both stacks and the front. */
  static async create({ buildWeb = true, names = ['mac', 'vps'] } = {}) {
    const env = new Env();
    env.root = mkdtempSync('/tmp/mce2e.');
    env.bin = join(env.root, 'bin');
    mkdirSync(env.bin);
    env.pterm = join(env.bin, 'pterm');
    if (buildWeb) {
      await must('pnpm', ['build:web'], { timeout: 600000 });
      await must('sh', ['scripts/embed-web.sh']);
    }
    await must('go', ['build', '-tags', 'webdist', '-o', env.pterm, './cmd/pterm'], { timeout: 600000 });
    await must('go', ['build', '-o', join(env.bin, 'front'), './scripts/e2e-multicomputer'], { timeout: 600000 });
    env.stacks = {};
    for (const name of names) env.stacks[name] = new Stack(env, name);
    await Promise.all(Object.values(env.stacks).map((s) => s.start()));
    await env.startFront();
    return env;
  }

  async startFront() {
    this.frontLog = join(this.root, 'front.jsonl');
    const routes = Object.values(this.stacks).flatMap((s) => ['-route', `${s.host}=${s.gateway}`]);
    this.front = spawn(join(this.bin, 'front'), ['-log', this.frontLog, ...routes], { stdio: ['ignore', 'pipe', 'inherit'] });
    const line = await new Promise((res) => this.front.stdout.once('data', (d) => res(String(d))));
    const [, address, spki] = /^proxy (\S+) spki (\S+)/.exec(line);
    this.proxy = address;
    this.spki = spki;
  }

  /** Front log entries written after mark (a line count from logMark). */
  logSince(mark = 0) {
    let text = '';
    try { text = readFileSync(this.frontLog, 'utf8'); } catch { /* not created yet */ }
    return text.split('\n').filter(Boolean).slice(mark).map((l) => JSON.parse(l));
  }

  logMark() { return this.logSince(0).length; }

  /** One request through the front with curl. Returns status and lowercase headers. */
  async curl(url, { method = 'GET', headers = {}, body } = {}) {
    const args = ['-sS', '-k', '--suppress-connect-headers', '--proxy', `http://${this.proxy}`, '-o', '/dev/null', '-D', '-', '-X', method];
    for (const [k, v] of Object.entries(headers)) args.push('-H', `${k}: ${v}`);
    if (body !== undefined) args.push('--data-binary', body);
    const { stdout } = await must('curl', [...args, url]);
    const lines = stdout.split('\r\n');
    const status = Number(/^HTTP\/[\d.]+ (\d+)/.exec(lines[0])?.[1]);
    const h = {};
    for (const line of lines.slice(1)) {
      const i = line.indexOf(':');
      if (i > 0) h[line.slice(0, i).toLowerCase()] = line.slice(i + 1).trim();
    }
    return { status, headers: h };
  }

  async close() {
    await Promise.all(Object.values(this.stacks).map((s) => s.stop()));
    this.front?.kill();
  }
}

// ---------- headless Chrome over CDP ----------

export class Browser {
  static async launch(env, { profile = 'chrome', extraArgs = [] } = {}) {
    const port = await freePort();
    const dataDir = join(env.root, profile);
    const chrome = spawn(CHROME, ['--headless=new', `--remote-debugging-port=${port}`, `--user-data-dir=${dataDir}`,
      `--proxy-server=http://${env.proxy}`, `--ignore-certificate-errors-spki-list=${env.spki}`,
      '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update',
      '--disable-sync', '--disable-features=Translate,OptimizationHints,MediaRouter', ...extraArgs, 'about:blank'], { stdio: 'ignore' });
    const version = await until(() => fetch(`http://127.0.0.1:${port}/json/version`).then((r) => r.json()), { what: 'chrome' });
    const browser = new Browser(chrome, new WebSocket(version.webSocketDebuggerUrl), version.Browser);
    await new Promise((r) => browser.ws.addEventListener('open', r));
    return browser;
  }

  constructor(chrome, ws, product) {
    Object.assign(this, { chrome, ws, product, id: 0, pending: new Map(), listeners: [] });
    ws.addEventListener('message', (e) => {
      const m = JSON.parse(e.data);
      if (m.id && this.pending.has(m.id)) {
        const p = this.pending.get(m.id);
        this.pending.delete(m.id);
        if (m.error) p.rej(new Error(`${p.method}: ${m.error.message}`)); else p.res(m.result);
      } else if (m.method) {
        for (const l of this.listeners) l(m);
      }
    });
  }

  send(method, params = {}, sessionId) {
    const id = ++this.id;
    return new Promise((res, rej) => {
      this.pending.set(id, { res, rej, method });
      this.ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
  }

  /** A new 390×844 touch page. bypassCSP lets checks send requests the app's CSP would stop first. */
  async page({ bypassCSP = false } = {}) {
    const { targetId } = await this.send('Target.createTarget', { url: 'about:blank' });
    const { sessionId } = await this.send('Target.attachToTarget', { targetId, flatten: true });
    const page = new Page(this, sessionId, targetId);
    await page.send('Page.enable');
    await page.send('Runtime.enable');
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 });
    if (bypassCSP) await page.send('Page.setBypassCSP', { enabled: true });
    return page;
  }

  /** Every cookie in the profile, HttpOnly ones included. */
  async cookies() { return (await this.send('Storage.getCookies')).cookies; }

  async close() {
    try { await Promise.race([this.send('Browser.close'), sleep(3000)]); } catch { /* already gone */ }
    this.chrome.kill();
  }
}

export class Page {
  constructor(browser, sessionId, targetId) { Object.assign(this, { browser, sessionId, targetId }); }
  send(method, params) { return this.browser.send(method, params, this.sessionId); }

  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text);
    return r.result.value;
  }

  async go(url) {
    await this.send('Page.navigate', { url });
    await until(() => this.eval(`document.readyState === 'complete' && location.href.startsWith(${JSON.stringify(url.split('#')[0])})`), { what: `load ${url}` });
  }

  text() { return this.eval('document.body ? document.body.innerText : ""'); }
  waitText(t, timeout = 20000) { return until(async () => (await this.text()).includes(t), { timeout, what: `text "${t}"` }); }

  async shot(path) {
    const { data } = await this.send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(path, Buffer.from(data, 'base64'));
  }

  close() { return this.browser.send('Target.closeTarget', { targetId: this.targetId }); }
}

/**
 * A fetch from inside a page. Returns {ok, status, type, error, body}; a
 * refused CORS request comes back as error "TypeError: Failed to fetch".
 */
export function pageFetch(page, url, init = {}) {
  return page.eval(`fetch(${JSON.stringify(url)}, ${JSON.stringify(init)}).then(
    async (r) => ({ ok: r.ok, status: r.status, type: r.type, body: r.type === 'opaque' ? '' : await r.text() }),
    (e) => ({ error: String(e) }))`);
}

// ---------- pairing ----------

/** Starts a long-running pterm command on a stack; read its output with wait(). */
export function interactive(stack, args) {
  // Node gives children a socket, not a pipe, as stdin; pterm's prompt
  // accepts only a terminal or a pipe, so cat turns it into a pipe.
  const child = spawn('/bin/sh', ['-c', 'cat | "$0" "$@"', stack.env.pterm, ...args, '--dev', '--runtime-dir', stack.runtimeDir],
    { env: stack.processEnv, stdio: ['pipe', 'pipe', 'pipe'] });
  const self = {
    child, out: '',
    wait: (re, timeout = 30000) => until(() => re.exec(self.out), { timeout, what: `${re} in pterm ${args.join(' ')}` })
      .catch((error) => { throw new Error(`${error.message}\noutput so far:\n${self.out}`); }),
    write: (text) => child.stdin.write(text),
    end: (text) => child.stdin.end(text),
    done: new Promise((r) => child.on('close', (code) => r(code))),
  };
  child.stdout.on('data', (d) => { self.out += d; });
  child.stderr.on('data', (d) => { self.out += d; });
  return self;
}

/** Fills a React-controlled input. */
export function setInput(page, selector, value) {
  return page.eval(`(() => { const el = document.querySelector(${JSON.stringify(selector)});
    Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value').set.call(el, ${JSON.stringify(value)});
    el.dispatchEvent(new Event('input', { bubbles: true })); return true; })()`);
}

/** Clicks the last visible button or link whose text or aria-label is (or contains) text. */
export async function click(page, text) {
  const ok = await page.eval(`(() => { const t = ${JSON.stringify(text)}; const norm = (e) => e.innerText.replace(/\\s+/g, ' ').trim();
    const els = [...document.querySelectorAll('button, a, [role=button], [role=tab]')].filter((e) => e.offsetParent !== null || e.getClientRects().length);
    const exact = els.filter((e) => norm(e) === t || e.getAttribute('aria-label') === t);
    const el = (exact.length ? exact : els.filter((e) => norm(e).includes(t))).at(-1);
    if (!el) return false; el.click(); return true; })()`);
  if (!ok) throw new Error(`no button "${text}"`);
  await sleep(300);
}

/** Pairs the browser page with a stack directly, as today: pterm pair, the link, the code, y. */
export async function pairDirect(page, stack, label) {
  const started = Date.now();
  const cli = interactive(stack, ['pair', '--no-qr', '--origin', stack.origin]);
  const [link] = await cli.wait(new RegExp(`${stack.origin.replace(/\./g, '\\.')}/pair#\\S+`));
  await page.go(link);
  await page.waitText('Device name');
  await setInput(page, '#device-label', label);
  await click(page, 'Pair this device');
  await page.waitText('Does your computer show this code?');
  const shown = await page.eval(`document.querySelector('.code-tiles .visually-hidden')?.textContent.replace('Verification code ', '')`);
  const [, code] = await cli.wait(/shows code (\d{3}-\d{3})/);
  if (code !== shown) throw new Error(`code mismatch: page ${shown}, cli ${code}`);
  cli.end('y\n');
  if (await cli.done !== 0) throw new Error(`pterm pair failed:\n${cli.out}`);
  return { code, ms: Date.now() - started };
}
