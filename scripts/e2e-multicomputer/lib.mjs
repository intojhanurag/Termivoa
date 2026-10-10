// Shared parts of the multi-computer e2e harness (README.md): two isolated
// dev stacks behind the HTTPS front (main.go), a fake Tailscale, curl through
// the front, and a small CDP client for headless Chrome. Node built-ins only.
import { spawn } from 'node:child_process';
import { appendFileSync, chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import net from 'node:net';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO = resolve(HERE, '../..');
/** Google Chrome; CHROME names another Chrome build (for example Chrome for Testing). */
export const CHROME = process.env.CHROME ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
export const TAILNET = 'tailx.ts.net';
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// Ports the harness must never use: the installed pterm and its previews.
const RESERVED = new Set([8787, 8788, 8789, 8443]);
// The longest a CDP call may stay without an answer (Browser.send).
const CDP_TIMEOUT_MS = 60000;
// Browser.close observes child exit, not just CDP acknowledgement or kill().
const BROWSER_GRACE_MS = 3000;
const BROWSER_TERM_MS = 1000;
const BROWSER_KILL_MS = 1000;
const BROWSER_SOCKET_MS = 1000;

// Every pterm process of the harness runs in this macOS sandbox. It hides
// the real Tailscale CLI (internal/tailnet.FindBinary checks these fixed
// paths before PATH, so the fake on PATH is used), and blocks the installed
// gateway port and launchctl, so nothing here can read or change the real
// Tailscale, the installed pterm or launchd. It also hides the owner's herdr
// (docs/herdr-provider.md, S16): its binaries, the Homebrew Cellar they link
// to, and the real ~/.config/herdr with its socket. The binaries answer "no
// such file" (not "not permitted"), so a stack without the fake is a computer
// where herdr is not installed, also on a Mac that has the real herdr: a
// refused binary would turn Install herdr off (docs/herdr-manage.md, 3.1).
// Sandbox rules match resolved paths, so every path is resolved first.
const REAL_TAILSCALE = ['/Applications/Tailscale.app/Contents/MacOS/Tailscale', '/usr/local/bin/tailscale',
  '/opt/homebrew/bin/tailscale', '/usr/bin/tailscale', '/usr/sbin/tailscale'];
const real = (path) => { try { return realpathSync(path); } catch { return path; } };
const REAL_HERDR = ['/opt/homebrew/bin/herdr', '/usr/local/bin/herdr', join(homedir(), '.local/bin/herdr'),
  join(homedir(), '.cargo/bin/herdr')];
const REAL_HERDR_TREES = ['/opt/homebrew/Cellar/herdr', '/usr/local/Cellar/herdr', join(homedir(), '.config/herdr')].map(real);
const SANDBOX = `(version 1)(allow default)
(deny file-read* process-exec ${REAL_TAILSCALE.map((p) => `(literal "${p}")`).join(' ')})
(deny file-read* process-exec ${[...new Set([...REAL_HERDR, ...REAL_HERDR.map(real)])].map((p) => `(literal "${p}")`).join(' ')} (with errno ENOENT))
(deny file-read* file-write* process-exec ${REAL_HERDR_TREES.map((p) => `(subpath "${p}")`).join(' ')})
(deny network-outbound ${REAL_HERDR_TREES.map((p) => `(remote unix-socket (subpath "${p}"))`).join(' ')})
(deny network-outbound (remote tcp "localhost:8787"))
(deny process-exec (literal "/bin/launchctl"))`;
/** argv that runs command inside the harness sandbox. */
export const sandboxed = (command, args) => ['/usr/bin/sandbox-exec', ['-p', SANDBOX, command, ...args]];

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

/**
 * Waits until fn returns a truthy value. One call of fn cannot outlast the
 * timeout either: a browser or process that never answers fails the check
 * with this error, it does not hang the run.
 */
export async function until(fn, { timeout = 20000, every = 200, what = 'condition' } = {}) {
  const end = Date.now() + timeout;
  const late = Symbol('late');
  let timer;
  const cut = new Promise((res) => { timer = setTimeout(res, timeout, late); });
  let last;
  try {
    while (Date.now() < end) {
      try {
        const value = await Promise.race([fn(), cut]);
        if (value === late) {
          last = new Error(`the last try did not return in ${timeout} ms`);
          break;
        }
        if (value) return value;
      } catch (error) {
        last = error;
      }
      await sleep(every);
    }
  } finally {
    clearTimeout(timer);
  }
  throw new Error(`timeout waiting for ${what}${last ? `: ${last.message}` : ''}`);
}

// ---------- fake Tailscale ----------

/** Demo tailnet: mac and vps share an owner; evil is another user's node. */
export const NODES = {
  mac: { ID: 'nMacE2E1CNTRL', user: 1, OS: 'macOS' },
  vps: { ID: 'nVpsE2E1CNTRL', user: 1, OS: 'linux' },
  evil: { ID: 'nEvilE2E1CNTRL', user: 2, OS: 'linux' },
};
const USERS = { 1: { LoginName: 'ada@example.com' }, 2: { LoginName: 'mallory@example.com' } };

function statusJSON(self, nodes) {
  const node = (name, index) => ({
    ID: nodes[name].ID, PublicKey: `nodekey:e2e${name}`, HostName: name, DNSName: `${name}.${TAILNET}.`,
    UserID: nodes[name].user, OS: nodes[name].OS, TailscaleIPs: [`100.64.0.${index + 1}`], Online: true,
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
    // Demo prompt: macOS /etc/zshrc would show the real user and host name.
    writeFileSync(join(this.home, '.zshrc'), "PROMPT='%1~ %# '\n");
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
      // The broker's offer to home goes through the front (peerdial_e2e.go, e2e builds only).
      PTERM_E2E_FRONT: this.env.proxy, PTERM_E2E_SPKI: this.env.spki,
      // The fake GitHub (scripts/fakegithub) for web, API and git; read only by an e2e build (internal/github).
      ...(this.env.github ? { PTERM_E2E_GITHUB: this.env.github, PTERM_E2E_GITHUB_CLIENT: 'Iv1.e2e', PTERM_E2E_GITHUB_APP: 'termivoa-e2e' } : {}),
    };
  }

  /** Picks the gateway and preview ports once; the front routes to them before the stack starts. */
  async ports() {
    this.port ??= await freePort();
    this.gateway = `127.0.0.1:${this.port}`;
    // What `tailscale serve --https=8443` exposes; the front routes <host>:8443 here.
    this.previewPort ??= await freePort();
    this.preview = `127.0.0.1:${this.previewPort}`;
    this.previewOrigin = `https://${this.host}:8443`;
  }

  /** preview false starts without the preview listener: a computer that does not share previews. */
  async start({ preview = true } = {}) {
    mkdirSync(join(this.dir, 'tmp'), { recursive: true });
    await this.ports();
    const log = join(this.env.root, `${this.name}.log`);
    const child = spawn(...sandboxed(this.env.pterm, ['up', '--dev', '--data-dir', this.dataDir, '--runtime-dir', this.runtimeDir,
      '--listen', this.gateway, '--preview-listen', preview ? this.preview : 'off']), { env: this.processEnv, cwd: this.home, stdio: ['ignore', 'pipe', 'pipe'] });
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
    if (this.child && this.child.exitCode === null) {
      const exited = new Promise((r) => this.child.once('exit', r));
      this.child.kill('SIGINT');
      await Promise.race([exited, sleep(10000).then(() => this.child.kill('SIGKILL'))]);
    }
    // The broker and gateway run in their own process groups; never leave one behind.
    await run('/usr/bin/pkill', ['-f', `runtime-dir ${this.runtimeDir}`]);
  }

  /** Runs an admin or local pterm command against this stack. */
  cli(args, options = {}) {
    return must(...sandboxed(this.env.pterm, [...args, '--dev', '--runtime-dir', this.runtimeDir]), { env: this.processEnv, ...options });
  }

  /**
   * `pterm doctor` on this stack, with HOME=doctorHome, in the sandbox: it
   * can never reach the installed gateway, launchd or the real Tailscale.
   * home true runs it with the stack's own HOME instead, where the fake
   * herdr lives (only its herdr line means anything then).
   */
  doctor({ home = false } = {}) {
    return run(...sandboxed(this.env.pterm, ['doctor']), { env: { ...this.processEnv, HOME: home ? this.home : this.doctorHome } });
  }

  // ---------- fake herdr (scripts/fakeherdr; docs/herdr-provider.md, S16) ----------

  /** The fake herdr's folder: <HOME>/.config/herdr, as herdr's own. */
  get herdrDir() { return join(this.home, '.config', 'herdr'); }

  /**
   * Installs the fake herdr as `herdr` first on this stack's PATH (the real
   * one is denied by the sandbox), writes its scenario and starts its server
   * with this stack's HOME. The broker finds it at its next binary lookup
   * (at most 30 s).
   */
  async installHerdr(scenario) {
    mkdirSync(this.herdrDir, { recursive: true, mode: 0o700 });
    chmodSync(join(this.home, '.config'), 0o700);
    writeFileSync(join(this.herdrDir, 'scenario.json'), JSON.stringify(scenario, null, 1));
    copyFileSync(join(this.env.bin, 'fakeherdr'), join(this.bin, 'herdr'));
    chmodSync(join(this.bin, 'herdr'), 0o755);
    this.herdr = spawn(...sandboxed(join(this.bin, 'herdr'), ['server']), { env: this.processEnv, cwd: this.home, stdio: 'ignore' });
    await until(() => readFileSync(join(this.herdrDir, 'fake.log'), 'utf8').includes('server '), { what: `fake herdr on ${this.name}` });
  }

  /** Ends the fake herdr's server process and leaves the binary: herdr installed and not running (Start herdr). */
  async stopHerdrServer() {
    if (this.herdr && this.herdr.exitCode === null) {
      const exited = new Promise((r) => this.herdr.once('exit', r));
      this.herdr.kill('SIGTERM');
      await Promise.race([exited, sleep(5000)]);
    }
    // A server that the broker started (Start herdr) is not this process's child.
    await run('/usr/bin/pkill', ['-f', `${join(this.bin, 'herdr')} server`]);
  }

  /** Stops the fake herdr and removes it from PATH: the broker drops the provider at its next poll. */
  async uninstallHerdr() {
    await this.stopHerdrServer();
    await run('/bin/rm', ['-f', join(this.bin, 'herdr')]);
  }

  /** The fake's live snapshot (state.json), or undefined while no server runs. */
  herdrState() {
    try { return JSON.parse(readFileSync(join(this.herdrDir, 'state.json'), 'utf8')); } catch { return undefined; }
  }

  /** One command on the fake's control socket ({op: status|close|hang|stop|start, …}); resolves with its answer line. */
  herdrControl(command) {
    return new Promise((res, rej) => {
      const socket = net.connect(join(this.herdrDir, 'control.sock'));
      let answer = '';
      socket.on('connect', () => socket.write(`${JSON.stringify(command)}\n`));
      socket.on('data', (d) => { answer += d; });
      socket.on('end', () => (answer.startsWith('ok') ? res(answer.trim()) : rej(new Error(`fake herdr ${JSON.stringify(command)}: ${answer.trim()}`))));
      socket.on('error', rej);
    });
  }

  /** The fake's log lines: server, control, attach sizes, input (hex), exits, worktree.create, agent.start. */
  herdrLog() {
    try { return readFileSync(join(this.herdrDir, 'fake.log'), 'utf8').split('\n').filter(Boolean); } catch { return []; }
  }
}

export class Env {
  /**
   * Builds pterm (tags webdist,e2e: the offer dial goes through the front)
   * and the front, then starts the front and both stacks. production also
   * builds pterm without the e2e tag, as bin/pterm-production.
   */
  static async create({ buildWeb = true, names = ['mac', 'vps'], production = false } = {}) {
    const env = new Env();
    env.keepDisplayOn();
    env.root = mkdtempSync('/tmp/mce2e.');
    env.bin = join(env.root, 'bin');
    mkdirSync(env.bin);
    env.pterm = join(env.bin, 'pterm');
    if (buildWeb) {
      await must('pnpm', ['build:web'], { timeout: 600000 });
      await must('sh', ['scripts/embed-web.sh']);
    }
    const builds = [
      must('go', ['build', '-tags', 'webdist,e2e', '-o', env.pterm, './cmd/pterm'], { timeout: 600000 }),
      must('go', ['build', '-o', join(env.bin, 'front'), './scripts/e2e-multicomputer'], { timeout: 600000 }),
      // The fake herdr; a stack gets it as `herdr` only through installHerdr.
      must('go', ['build', '-o', join(env.bin, 'fakeherdr'), './scripts/fakeherdr'], { timeout: 600000 }),
      must('go', ['build', '-o', join(env.bin, 'fakegithub'), './scripts/fakegithub'], { timeout: 600000 }),
    ];
    if (production) {
      env.production = join(env.bin, 'pterm-production');
      builds.push(must('go', ['build', '-tags', 'webdist', '-o', env.production, './cmd/pterm'], { timeout: 600000 }));
    }
    await Promise.all(builds);
    env.stacks = {};
    for (const name of names) env.stacks[name] = new Stack(env, name);
    await Promise.all(Object.values(env.stacks).map((s) => s.ports()));
    await env.startFront();
    await env.startGitHub();
    await Promise.all(Object.values(env.stacks).map((s) => s.start()));
    return env;
  }

  /**
   * macOS: holds a display-on and no-idle-sleep assertion for the life of
   * this process (README, "Display"). With the display off, headless Chrome
   * makes no frames: the terminal's rows stop and some CDP calls never
   * answer. -u turns on a display that is already off; -w ends caffeinate
   * with this process, also after a crash or a kill. Nothing on other systems.
   */
  keepDisplayOn() {
    if (process.platform !== 'darwin') return;
    this.caffeinate = spawn('/usr/bin/caffeinate', ['-d', '-i', '-u', '-w', String(process.pid)], { stdio: 'ignore' });
    this.caffeinate.on('error', (error) => console.log(`caffeinate did not start (${error.message}): keep the display on yourself`));
    this.caffeinate.unref();
  }

  async startFront() {
    this.frontLog = join(this.root, 'front.jsonl');
    // While this file names a host, the front answers its offer route as a home without it (check F-old).
    this.oldHome = join(this.root, 'old-home');
    const routes = Object.values(this.stacks).flatMap((s) => ['-route', `${s.host}=${s.gateway}`, '-preview', `${s.host}=${s.preview}`]);
    this.front = spawn(join(this.bin, 'front'), ['-log', this.frontLog, '-old-home', this.oldHome, ...routes], { stdio: ['ignore', 'pipe', 'inherit'] });
    const line = await new Promise((res) => this.front.stdout.once('data', (d) => res(String(d))));
    const [, address, spki] = /^proxy (\S+) spki (\S+)/.exec(line);
    this.proxy = address;
    this.spki = spki;
  }

  /** The fake GitHub, before the stacks start: they get its address (Stack.processEnv). */
  async startGitHub() {
    this.fakeGitHub = spawn(join(this.bin, 'fakegithub'), ['-port', '0', '-client', 'Iv1.e2e', '-dir', join(this.root, 'fakegithub')], { stdio: ['ignore', 'pipe', 'inherit'] });
    const line = await new Promise((res) => this.fakeGitHub.stdout.once('data', (d) => res(String(d))));
    this.github = /^listening (\S+)/.exec(line)[1];
  }

  /** One test control or API call on the fake GitHub; resolves with {status, json}. */
  async gitHub(path, { method = 'GET', token } = {}) {
    const r = await fetch(`${this.github}${path}`, { method, headers: token ? { Authorization: `Bearer ${token}` } : {} });
    const text = await r.text();
    let json;
    try { json = JSON.parse(text); } catch { /* not JSON */ }
    return { status: r.status, json, text };
  }

  /** Front log entries written after mark (a line count from logMark). */
  logSince(mark = 0) {
    let text = '';
    try { text = readFileSync(this.frontLog, 'utf8'); } catch { /* not created yet */ }
    return text.split('\n').filter(Boolean).slice(mark).map((l) => JSON.parse(l));
  }

  logMark() { return this.logSince(0).length; }

  /** One request through the front with curl. Returns status, lowercase headers and the body text. */
  async curl(url, { method = 'GET', headers = {}, body } = {}) {
    const args = ['-sS', '-k', '--suppress-connect-headers', '--proxy', `http://${this.proxy}`, '-D', '-', '-X', method];
    for (const [k, v] of Object.entries(headers)) args.push('-H', `${k}: ${v}`);
    if (body !== undefined) args.push('--data-binary', body);
    const { stdout } = await must('curl', [...args, url]);
    const end = stdout.indexOf('\r\n\r\n');
    const lines = stdout.slice(0, end).split('\r\n');
    const status = Number(/^HTTP\/[\d.]+ (\d+)/.exec(lines[0])?.[1]);
    const h = {};
    for (const line of lines.slice(1)) {
      const i = line.indexOf(':');
      if (i > 0) h[line.slice(0, i).toLowerCase()] = line.slice(i + 1).trim();
    }
    return { status, headers: h, body: stdout.slice(end + 4) };
  }

  async close() {
    await Promise.all(Object.values(this.stacks).map((s) => s.uninstallHerdr()));
    await Promise.all(Object.values(this.stacks).map((s) => s.stop()));
    this.front?.kill();
    this.fakeGitHub?.kill();
    this.caffeinate?.kill();
    // A failed step can leave a CLI under script(1) running (pterm setup has no runtime dir).
    await run('/usr/bin/pkill', ['-f', `${this.root}/bin/`]);
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
    Object.assign(this, { chrome, ws, product, id: 0, pending: new Map(), listeners: [], closing: false, disconnected: false });
    this.onMessage = (e) => {
      const m = JSON.parse(e.data);
      if (m.id && this.pending.has(m.id)) {
        const p = this.pending.get(m.id);
        this.pending.delete(m.id);
        clearTimeout(p.timer);
        if (m.error) p.rej(new Error(`${p.method}: ${m.error.message}`)); else p.res(m.result);
      } else if (m.method) {
        for (const l of this.listeners) l(m);
      }
    };
    this.onDisconnect = () => {
      this.disconnected = true;
      this.#rejectPending('Chrome connection closed');
    };
    ws.addEventListener('message', this.onMessage);
    ws.addEventListener('close', this.onDisconnect);
    ws.addEventListener('error', this.onDisconnect);
  }

  /**
   * One CDP call. Chrome does not answer some calls while it makes no frames
   * (Input.dispatchMouseEvent mouseMoved in a tab that is not drawn), and
   * never after it died: no answer in `timeout` ms is an error that names
   * the call.
   */
  send(method, params = {}, sessionId, timeout = CDP_TIMEOUT_MS) {
    if (this.closing || this.disconnected) return Promise.reject(new Error(`${method}: Chrome connection closed or closing`));
    return this.#request(method, params, sessionId, timeout);
  }

  #request(method, params, sessionId, timeout) {
    const id = ++this.id;
    return new Promise((res, rej) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        rej(new Error(`${method}: no answer from Chrome in ${timeout} ms`));
      }, timeout);
      this.pending.set(id, { res, rej, method, timer });
      try { this.ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) })); }
      catch (error) { clearTimeout(timer); this.pending.delete(id); rej(error); }
    });
  }

  #rejectPending(reason) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.rej(new Error(`${pending.method}: ${reason}`));
    }
    this.pending.clear();
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

  close() {
    if (this.closePromise === undefined) {
      this.closing = true;
      this.closePromise = Promise.resolve().then(() => this.#shutdown());
    }
    return this.closePromise;
  }

  async #shutdown() {
    try {
      if (this.chrome.exitCode !== null || this.chrome.signalCode !== null) return;
      // A close response or socket disappearance does not prove process exit.
      // Catch CDP failure here; only observed exit makes teardown successful.
      if (!this.disconnected) void this.#request('Browser.close', {}, undefined, BROWSER_GRACE_MS).catch(() => undefined);
      if (await waitForChildExit(this.chrome, BROWSER_GRACE_MS)) return;
      this.chrome.kill('SIGTERM');
      if (await waitForChildExit(this.chrome, BROWSER_TERM_MS)) return;
      this.chrome.kill('SIGKILL');
      if (!await waitForChildExit(this.chrome, BROWSER_KILL_MS)) throw new Error('Chrome child did not exit within bounded browser teardown');
    } finally {
      this.disconnected = true;
      this.#rejectPending('Chrome connection closed');
      this.listeners.length = 0;
      this.ws.removeEventListener('message', this.onMessage);
      this.ws.removeEventListener('close', this.onDisconnect);
      this.ws.removeEventListener('error', this.onDisconnect);
      await closeBrowserSocket(this.ws);
    }
  }
}

function waitForChildExit(child, timeout) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(true);
  return new Promise((resolve) => {
    const finish = (didExit) => {
      clearTimeout(timer);
      child.removeListener('exit', onExit);
      child.removeListener('error', onError);
      resolve(didExit);
    };
    const onExit = () => finish(true);
    const onError = () => finish(false);
    const timer = setTimeout(() => finish(false), timeout);
    child.once('exit', onExit);
    child.once('error', onError);
  });
}

function closeBrowserSocket(ws) {
  if (ws.readyState === 3) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const finish = (error) => {
      clearTimeout(timer);
      ws.removeEventListener('close', onClose);
      if (error) reject(error); else resolve();
    };
    const onClose = () => finish();
    const timer = setTimeout(() => finish(new Error('Chrome WebSocket did not close within bounded browser teardown')), BROWSER_SOCKET_MS);
    ws.addEventListener('close', onClose);
    try { ws.close(); } catch (error) { finish(error); }
  });
}

export class Page {
  constructor(browser, sessionId, targetId) { Object.assign(this, { browser, sessionId, targetId }); }
  send(method, params, timeout) { return this.browser.send(method, params, this.sessionId, timeout); }

  /**
   * Makes Chrome draw one frame now (a 1×1 screenshot). Without a lit
   * display Chrome makes no frames of its own, and what waits for a frame
   * waits with it: xterm draws the terminal's rows in requestAnimationFrame,
   * and a mouse move is delivered with a frame. Never fails: a reader that
   * gets no frame in 5 s reads the page as it is, and stderr gets one line.
   * One capture per page at a time: while Chrome has not answered (or
   * Browser.send has not given up on it), the next call waits for the same one.
   */
  frame() {
    this.capture ??= this.send('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width: 1, height: 1, scale: 1 } })
      .catch(() => undefined).finally(() => { this.capture = undefined; });
    let timer;
    const late = new Promise((r) => { timer = setTimeout(() => { console.error(`frame(): no frame in 5 s on page ${this.targetId}`); r(); }, 5000); });
    return Promise.race([this.capture, late]).finally(() => clearTimeout(timer));
  }

  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text);
    return r.result.value;
  }

  async go(url) {
    await this.send('Page.navigate', { url });
    await until(() => this.eval(`document.readyState === 'complete' && location.href.startsWith(${JSON.stringify(url.split('#')[0])})`), { what: `load ${url}` });
  }

  /** Makes this the visible tab: the app polls only while visible. */
  front() { return this.send('Page.bringToFront'); }

  text() { return this.eval('document.body ? document.body.innerText : ""'); }
  waitText(t, timeout = 20000) { return until(async () => (await this.text()).includes(t), { timeout, what: `text "${t}"` }); }

  async shot(path) {
    const { data } = await this.send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(path, Buffer.from(data, 'base64'));
  }

  close() { return this.browser.send('Target.closeTarget', { targetId: this.targetId }); }

  /**
   * Evaluates in the child frame whose address starts with prefix (the
   * preview frame is another origin, so the page cannot read it). Returns
   * undefined when there is no such frame.
   */
  async frameEval(prefix, expression) {
    const { frameTree } = await this.send('Page.getFrameTree');
    const frame = (frameTree.childFrames ?? []).map((child) => child.frame).find((f) => f.url.startsWith(prefix));
    if (!frame) return undefined;
    const { executionContextId } = await this.send('Page.createIsolatedWorld', { frameId: frame.id });
    const r = await this.send('Runtime.evaluate', { expression, contextId: executionContextId, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text);
    return r.result.value;
  }
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

/** Terminal output as plain text: no color codes, no carriage returns. */
export const plain = (text) => text.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '').replace(/\r/g, '');

/**
 * Starts a long-running pterm command on a stack; read its output with wait().
 * pty runs it in a real terminal (script(1)), as a person types it: only
 * then does `pterm pair --via` offer this computer to home. profile false
 * leaves out --dev --runtime-dir (pterm setup has no dev profile); env
 * replaces the stack's environment.
 */
export function interactive(stack, args, { pty = false, profile = true, env = stack.processEnv } = {}) {
  // Node gives children a socket, not a pipe, as stdin; pterm's prompt
  // accepts only a terminal or a pipe, so cat turns it into a pipe (and
  // script(1) needs a pipe or terminal to read from).
  const run = pty ? 'cat | /usr/bin/script -q /dev/null "$0" "$@"' : 'cat | "$0" "$@"';
  const child = spawn(...sandboxed('/bin/sh', ['-c', run, stack.env.pterm, ...args, ...(profile ? ['--dev', '--runtime-dir', stack.runtimeDir] : [])]),
    { env, stdio: ['pipe', 'pipe', 'pipe'] });
  const self = {
    child, out: '',
    wait: (re, timeout = 30000) => until(() => re.exec(plain(self.out)), { timeout, what: `${re} in pterm ${args.join(' ')}` })
      .catch((error) => { throw new Error(`${error.message}\noutput so far:\n${plain(self.out)}`); }),
    write: (text) => child.stdin.write(text),
    end: (text) => child.stdin.end(text),
    done: new Promise((r) => child.on('close', (code) => r(code))),
  };
  // Whole characters: a chunk may end inside one (QR blocks, arrows).
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
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

/** In-app navigation, as the app's own links do: no new page load. */
export const pushPath = (page, path) => page.eval(`history.pushState(null, '', ${JSON.stringify(path)}); dispatchEvent(new PopStateEvent('popstate'))`);

/**
 * The computer of a list element: the label of its All computers header
 * (.computer-group), else the computer chosen in the drawer bar, else ''
 * (one computer: no bar). A page-side function, for page.eval.
 */
export const COMPUTER_OF = `const computerOf = (e) => {
  const box = e.closest('.provider-computer, section[aria-label]:not([aria-label="Your sessions"])');
  const head = box?.querySelector('.computer-group bdi') ?? document.querySelector('.drawer-pill bdi');
  return head?.innerText.trim() ?? ''; };`;

/** The Sessions drawer bar: the pill's text, the amber pill, and the All computers headers; null with one computer. */
export const drawerBar = (page) => page.eval(`(() => { if (!document.querySelector('.drawer-bar')) return null;
  const t = (e) => e?.innerText.replace(/\\s+/g, ' ').trim() ?? null;
  return { pill: t(document.querySelector('.drawer-pill')).replace(/ ?▾$/, ''), amber: t(document.querySelector('.drawer-amber')),
    headers: [...document.querySelectorAll('h2.computer-group bdi')].map(t) }; })()`);

/** Opens the drawer (menu button "Computers and menu") and reads its rows; throws when there is none. */
export async function openDrawer(page) {
  if (!await page.eval(`document.querySelector('.drawer') !== null`)) await click(page, 'Computers and menu');
  return until(() => page.eval(`(() => { const d = document.querySelector('dialog.drawer[open]'); if (!d) return null;
    const t = (e) => e?.innerText.replace(/\\s+/g, ' ').trim() ?? '';
    return { focused: d.contains(document.activeElement), rows: [...d.querySelectorAll('.drawer-row')].map((b) => ({ label: t(b.querySelector('.drawer-label')),
      state: t(b.querySelector('.drawer-state')).replace(/^, /, ''), count: Number(b.querySelector('.drawer-count')?.innerText ?? 0), current: b.getAttribute('aria-current') === 'true' })) }; })()`),
  { what: 'the drawer' });
}

/** Chooses a computer in the drawer by label ("All computers" for all); waits until the pill says it. */
export async function chooseComputer(page, label) {
  await openDrawer(page);
  const ok = await page.eval(`(() => { const b = [...document.querySelectorAll('.drawer .drawer-row')].find((e) => e.querySelector('.drawer-label')?.innerText.trim() === ${JSON.stringify(label)});
    if (!b) return false; b.click(); return true; })()`);
  if (!ok) throw new Error(`no drawer row "${label}"`);
  await until(async () => (await drawerBar(page))?.pill === label && !(await page.eval(`document.querySelector('.drawer') !== null`)), { what: `the pill "${label}"` });
}

/** Settings → the card of a computer → its page (docs/ui-revamp.md, section 5); from anywhere in the app. */
export async function settingsComputer(page, label) {
  await pushPath(page, '/settings');
  await until(() => page.eval(`(() => { const b = [...document.querySelectorAll('.sc-card .sc-open')].find((e) => e.querySelector('.computer-name')?.innerText.trim() === ${JSON.stringify(label)});
    if (!b) return false; b.click(); return true; })()`), { what: `the Settings card of ${label}`, timeout: 20000 });
  await until(() => page.eval(`document.querySelector('.sc-page-head h1')?.innerText.trim() === ${JSON.stringify(label)}`), { what: `the Settings page of ${label}` });
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

/** A JSON API call from inside a page, with credentials. */
export async function api(page, url, method = 'GET', body) {
  const init = { method, credentials: 'include' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' };
    init.body = JSON.stringify(body);
  }
  const r = await pageFetch(page, url, init);
  try { r.json = JSON.parse(r.body); } catch { /* not JSON */ }
  return r;
}

/** Types text into the Live terminal and presses Enter. */
export async function typeLine(page, text) {
  await page.eval(`document.querySelector('.terminal-input').focus()`);
  await page.send('Input.insertText', { text });
  for (const type of ['keyDown', 'keyUp']) {
    await page.send('Input.dispatchKeyEvent', { type, key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, ...(type === 'keyDown' ? { text: '\r' } : {}) });
  }
}

/** The visible text of the Live terminal, after a frame: xterm draws its rows only in a frame (Page.frame). */
export async function terminalText(page) {
  await page.frame();
  return page.eval(`[...document.querySelectorAll('.terminal-host .xterm-rows')].map((e) => e.innerText).join('\\n')`);
}

/** Taps the first place where needle starts in the Live terminal (xterm link providers act on mouse events). */
export async function tapTerminalText(page, needle) {
  const point = await page.eval(`(() => {
    const walker = document.createTreeWalker(document.querySelector('.terminal-host .xterm-rows'), NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const at = node.textContent.indexOf(${JSON.stringify(needle)});
      if (at < 0) continue;
      const range = document.createRange();
      range.setStart(node, at);
      range.setEnd(node, at + Math.min(8, ${JSON.stringify(needle)}.length));
      const r = range.getBoundingClientRect();
      return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
    }
    return null; })()`);
  if (!point) throw new Error(`"${needle}" is not on the terminal screen`);
  // Chrome answers a mouse move only with a frame (Page.frame).
  const moved = page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
  moved.catch(() => {}); // no unhandled rejection during frame(); `await moved` still throws
  await page.frame();
  await moved;
  await sleep(200);
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
  await sleep(300);
}
