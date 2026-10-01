// Builds the demo HOME for the docs screenshots: shell prompt, git identity,
// three small demo repos with history, and the stand-in agent.
// Usage: node setup.mjs   (then start the stack, see capture.mjs)
import { execFileSync } from 'node:child_process';
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

export const HOME = '/tmp/demo-home';
// The repos live outside HOME, under /Users/<name>/, so the phone shows them as ~/code/...
// (it shortens only /Users/<name> and /home/<name>). A marker file guards the removal.
export const CODE = '/Users/Shared/code';
const MARKER = join(CODE, '.termivoa-demo');
export const PTERM = '/tmp/pterm-demo';
// capture.mjs writes the PID of the `up --dev` process it starts here (inside HOME, so setup() wipes it).
export const PIDFILE = join(HOME, '.capture-pid');

/**
 * Stops the demo stack that capture.mjs started: SIGINT to the one recorded PID, so `pterm up --dev`
 * stops its own gateway and broker. Never matches by name. The PID is only signalled when its command
 * line still contains `expect` (guards against a recycled PID). Waits up to waitMs, then SIGKILL.
 */
export async function stopStack(pidFile = PIDFILE, expect = `${PTERM} up --dev`, waitMs = 15000) {
  if (!existsSync(pidFile)) return;
  const pid = Number(readFileSync(pidFile, 'utf8').trim());
  rmSync(pidFile, { force: true });
  let command = '';
  try { command = execFileSync('ps', ['-p', String(pid), '-o', 'command='], { stdio: ['ignore', 'pipe', 'ignore'] }).toString(); } catch {}
  if (!Number.isInteger(pid) || pid <= 1 || !command.includes(expect)) return;
  const alive = () => { try { process.kill(pid, 0); return true; } catch { return false; } };
  process.kill(pid, 'SIGINT');
  for (let end = Date.now() + waitMs; alive() && Date.now() < end;) await new Promise((r) => setTimeout(r, 100));
  if (alive()) { console.log(`pid ${pid} ignored SIGINT for ${waitMs} ms; sending SIGKILL (its broker and gateway may still run)`); try { process.kill(pid, 'SIGKILL'); } catch {} }
}

/** Removes the demo repos, only when this script made the folder. */
export function removeCode() {
  if (!existsSync(CODE)) return;
  if (!existsSync(MARKER)) throw new Error(`${CODE} exists and was not made by setup.mjs; not touching it`);
  try { execFileSync('chmod', ['-R', 'u+w', CODE], { stdio: 'ignore' }); } catch {}
  rmSync(CODE, { recursive: true, force: true });
}
const ENV = { ...process.env, HOME, GIT_CONFIG_NOSYSTEM: '1' };
const here = dirname(new URL(import.meta.url).pathname);

function files(root, map) {
  for (const [name, text] of Object.entries(map)) {
    mkdirSync(dirname(join(root, name)), { recursive: true });
    writeFileSync(join(root, name), text.replace(/^\n/, ''));
  }
}
let day = 0;
function git(root, ...args) {
  const date = `2026-09-${String(14 + day).padStart(2, '0')}T10:${String(10 + day * 3).padStart(2, '0')}:00`;
  return execFileSync('git', ['-C', root, ...args], { env: { ...ENV, GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date } }).toString();
}
function commit(root, message, map) {
  files(root, map);
  git(root, 'add', '-A');
  git(root, 'commit', '-q', '-m', message);
  day++;
}

export function setup() {
  try { execFileSync('chmod', ['-R', 'u+w', HOME], { stdio: 'ignore' }); } catch {}
  rmSync(HOME, { recursive: true, force: true });
  removeCode();
  mkdirSync(CODE, { recursive: true });
  writeFileSync(MARKER, 'Demo repos for the Termivoa docs screenshots. Safe to delete.\n');
  mkdirSync(join(HOME, 'bin'), { recursive: true });
  writeFileSync(join(HOME, '.zshrc'), [
    "PROMPT='%1~ ❯ '",
    'export CLICOLOR=1',
    'export PATH="$HOME/bin:$PATH"',
    'unsetopt BEEP',
    '',
  ].join('\n'));
  writeFileSync(join(HOME, '.gitconfig'), '[user]\n\tname = Demo\n\temail = demo@example.com\n[init]\n\tdefaultBranch = main\n[advice]\n\tdetachedHead = false\n');
  copyFileSync(join(here, 'agent.js'), join(HOME, 'bin', 'agent'));
  chmodSync(join(HOME, 'bin', 'agent'), 0o755);

  // acme-web: a small Node/TS web app with a branch and a bare origin.
  const web = join(CODE, 'acme-web');
  mkdirSync(web, { recursive: true });
  git(web, 'init', '-q');
  commit(web, 'chore: scaffold acme-web', {
    'package.json': `
{
  "name": "acme-web",
  "version": "0.4.2",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "tsx watch src/server.ts",
    "build": "tsc -p .",
    "start": "node dist/server.js",
    "test": "vitest run",
    "lint": "eslint src",
    "typecheck": "tsc --noEmit"
  },
  "dependencies": { "hono": "^4.6.0" },
  "devDependencies": { "tsx": "^4.19.0", "typescript": "^5.6.0", "vitest": "^2.1.0" }
}
`,
    'tsconfig.json': '{\n  "compilerOptions": { "target": "ES2022", "module": "NodeNext", "strict": true, "outDir": "dist" },\n  "include": ["src"]\n}\n',
    '.gitignore': 'node_modules\ndist\n.env\n',
    'README.md': '# acme-web\n\nThe Acme storefront API.\n',
    'src/server.ts': `
import { Hono } from 'hono';
import { health } from './routes/health.js';

const app = new Hono();
app.route('/health', health);

export default app;
`,
    'src/routes/health.ts': `
import { Hono } from 'hono';

export const health = new Hono();

health.get('/', (c) => c.json({ ok: true, uptime: process.uptime() }));
`,
  });
  commit(web, 'feat: add orders API', {
    'src/routes/orders.ts': `
import { Hono } from 'hono';
import { cached } from '../lib/cache.js';
import { findOrder, listOrders } from '../db/orders.js';

export const orders = new Hono();

orders.get('/', async (c) => c.json(await listOrders()));
orders.get('/:id', async (c) => {
  const order = await cached(\`order:\${c.req.param('id')}\`, () => findOrder(c.req.param('id')));
  return order ? c.json(order) : c.notFound();
});
`,
    'src/db/orders.ts': `
export interface Order { id: string; total: number; status: 'open' | 'paid' | 'shipped' }

const rows: Order[] = [
  { id: 'A-1001', total: 4200, status: 'paid' },
  { id: 'A-1002', total: 1850, status: 'open' },
];

export async function listOrders(): Promise<Order[]> { return rows; }
export async function findOrder(id: string): Promise<Order | undefined> { return rows.find((row) => row.id === id); }
`,
    'src/server.ts': `
import { Hono } from 'hono';
import { health } from './routes/health.js';
import { orders } from './routes/orders.js';

const app = new Hono();
app.route('/health', health);
app.route('/api/orders', orders);

export default app;
`,
  });
  commit(web, 'feat: cache order lookups', {
    'src/lib/cache.ts': `
type Entry = { value: unknown; expires: number };

const store = new Map<string, Entry>();
const TTL_MS = 30_000;

/** Returns the cached value for key, or loads and stores it. */
export async function cached<T>(key: string, load: () => Promise<T>): Promise<T> {
  const hit = store.get(key);
  if (hit && hit.expires > Date.now()) return hit.value as T;
  const value = await load();
  store.set(key, { value, expires: Date.now() + TTL_MS });
  return value;
}

export function clearCache(): void {
  store.clear();
}
`,
    'test/health.test.ts': `
import { expect, test } from 'vitest';
import app from '../src/server.js';

test('health answers ok', async () => {
  const res = await app.request('/health');
  expect(res.status).toBe(200);
});
`,
  });
  commit(web, 'docs: add local setup steps', { 'README.md': '# acme-web\n\nThe Acme storefront API.\n\n## Run it\n\n```sh\nnpm install\nnpm run dev\n```\n' });
  git(web, 'switch', '-q', '-c', 'feat/checkout-v2');
  commit(web, 'feat: start checkout v2 endpoint', { 'src/routes/checkout.ts': "import { Hono } from 'hono';\n\nexport const checkout = new Hono();\n\ncheckout.post('/', (c) => c.json({ next: 'payment' }, 201));\n" });
  git(web, 'switch', '-q', 'main');
  const bare = join(CODE, 'origin', 'acme-web.git');
  execFileSync('git', ['init', '-q', '--bare', bare], { env: ENV });
  git(web, 'remote', 'add', 'origin', bare);
  git(web, 'push', '-q', '-u', 'origin', 'main', 'feat/checkout-v2');

  // billing-api and docs-site: smaller repos for the list and the Wall.
  const billing = join(CODE, 'billing-api');
  mkdirSync(billing, { recursive: true });
  git(billing, 'init', '-q');
  commit(billing, 'chore: scaffold billing-api', {
    'package.json': '{\n  "name": "billing-api",\n  "private": true,\n  "type": "module",\n  "scripts": {\n    "dev": "tsx watch src/index.ts",\n    "test": "vitest run",\n    "db:migrate": "tsx scripts/migrate.ts"\n  }\n}\n',
    'src/index.ts': "export { invoiceTotal } from './invoices.js';\n",
    'src/invoices.ts': "import { taxFor } from './tax.js';\n\nexport function invoiceTotal(net: number, region: string): number {\n  return Math.round(net * (1 + taxFor(region)));\n}\n",
    'src/tax.ts': "const RATES: Record<string, number> = { EU: 0.2, UK: 0.2, US: 0.07 };\n\nexport const taxFor = (region: string) => RATES[region] ?? 0;\n",
  });
  commit(billing, 'feat: add invoice totals', { 'README.md': '# billing-api\n\nInvoices and tax.\n' });

  const docs = join(CODE, 'docs-site');
  mkdirSync(docs, { recursive: true });
  git(docs, 'init', '-q');
  commit(docs, 'chore: scaffold docs-site', {
    'package.json': '{\n  "name": "docs-site",\n  "private": true,\n  "scripts": { "dev": "astro dev", "build": "astro build" }\n}\n',
    'src/pages/index.md': '# Acme docs\n\nStart here.\n',
    'src/pages/api.md': '# API\n\n`GET /api/orders` lists orders.\n',
  });
  commit(docs, 'docs: describe the orders API', { 'src/pages/api.md': '# API\n\n`GET /api/orders` lists orders.\n\n`GET /api/orders/:id` returns one order.\n' });
}

/** The agent's edits in acme-web, for Changes and the recap. */
export function agentEdits() {
  const web = join(CODE, 'acme-web');
  files(web, {
    'src/lib/rate-limit.ts': `
import type { MiddlewareHandler } from 'hono';

const WINDOW_MS = 60_000;
const LIMIT = 60;
const hits = new Map<string, { count: number; reset: number }>();

/** Allows LIMIT requests per client per minute; then answers 429. */
export const rateLimit: MiddlewareHandler = async (c, next) => {
  const client = c.req.header('x-forwarded-for') ?? 'local';
  const now = Date.now();
  const entry = hits.get(client);
  if (!entry || entry.reset < now) {
    hits.set(client, { count: 1, reset: now + WINDOW_MS });
    return next();
  }
  if (entry.count >= LIMIT) {
    c.header('Retry-After', String(Math.ceil((entry.reset - now) / 1000)));
    return c.json({ error: 'rate_limited' }, 429);
  }
  entry.count++;
  return next();
};
`,
    'src/server.ts': `
import { Hono } from 'hono';
import { rateLimit } from './lib/rate-limit.js';
import { health } from './routes/health.js';
import { orders } from './routes/orders.js';

const app = new Hono();
app.route('/health', health);
app.use('/api/*', rateLimit);
app.route('/api/orders', orders);

export default app;
`,
    'src/lib/cache.ts': `
type Entry = { value: unknown; expires: number };

const store = new Map<string, Entry>();
const TTL_MS = 60_000;
const MAX_ENTRIES = 1_000;

/** Returns the cached value for key, or loads and stores it. */
export async function cached<T>(key: string, load: () => Promise<T>): Promise<T> {
  const hit = store.get(key);
  if (hit && hit.expires > Date.now()) return hit.value as T;
  const value = await load();
  if (store.size >= MAX_ENTRIES) store.delete(store.keys().next().value!);
  store.set(key, { value, expires: Date.now() + TTL_MS });
  return value;
}

export function clearCache(): void {
  store.clear();
}
`,
  });
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  await stopStack(); // setup() wipes HOME, and the PID file with it: stop the old stack first
  if (process.argv[2] === 'clean') { removeCode(); try { execFileSync('chmod', ['-R', 'u+w', HOME], { stdio: 'ignore' }); } catch {} rmSync(HOME, { recursive: true, force: true }); } else setup();
}
