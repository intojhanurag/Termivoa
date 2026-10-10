import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { gunzipSync } from 'node:zlib';
import react from '@vitejs/plugin-react';
import { build } from 'vite';
import { afterAll, describe, expect, it, vi } from 'vitest';
import { precompressAssets, protocolValidators, serviceWorker } from '../vite.config';

const root = new URL('..', import.meta.url).pathname;
const work = mkdtempSync(join(tmpdir(), 'pterm-bundle-'));
afterAll(() => rmSync(work, { recursive: true, force: true }));

describe('bundle file list for the service worker', () => {
  it('lists every built file but source maps and sw.js, with one build ID in sw.js and index.html', async () => {
    const template = join(work, 'sw.template.js');
    writeFileSync(template, "self.CACHE = 'pterm-assets-__BUILD__';\nself.BUNDLE = __BUNDLE__;\n");
    const outDir = join(work, 'dist');
    await build({
      root,
      configFile: false,
      logLevel: 'silent',
      plugins: [react(), serviceWorker(pathToFileURL(template)), protocolValidators(), precompressAssets()],
      build: { outDir, emptyOutDir: true, sourcemap: 'hidden' },
    });
    const sw = readFileSync(join(outDir, 'sw.js'), 'utf8');
    const bundle = JSON.parse(/self\.BUNDLE = (.*);/u.exec(sw)?.[1] ?? '') as { build: string; files: string[] };
    const onDisk = readdirSync(outDir, { recursive: true, encoding: 'utf8' })
      .filter((name) => name.includes('.') && !name.endsWith('.map') && !name.endsWith('.gz') && name !== 'sw.js')
      .map((name) => `/${name}`)
      .sort();
    expect(bundle.files).toEqual(onDisk);
    const compressed = readdirSync(join(outDir, 'assets')).filter((name) => name.endsWith('.gz'));
    expect(compressed.length).toBeGreaterThan(0);
    for (const name of compressed) {
      const encoded = readFileSync(join(outDir, 'assets', name));
      const original = readFileSync(join(outDir, 'assets', name.slice(0, -3)));
      expect(gunzipSync(encoded)).toEqual(original);
      expect(encoded.length).toBeLessThan(original.length);
      expect(bundle.files).not.toContain(`/assets/${name}`);
    }
    expect(readdirSync(outDir).some((name) => name.endsWith('.gz'))).toBe(false);
    expect(bundle.files).toContain('/index.html');
    expect(bundle.files).toContain('/manifest.webmanifest');
    expect(bundle.files.some((name) => name.startsWith('/assets/Wall-'))).toBe(true);
    // Lazy chunks are precached too, so they open with home offline.
    expect(bundle.files.some((name) => name.startsWith('/assets/Computers-'))).toBe(true);
    expect(bundle.files.some((name) => name.startsWith('/assets/AddComputer-'))).toBe(true);
    // The QR decoder for Scan QR on phones with no BarcodeDetector (iPhone).
    expect(bundle.files.some((name) => name.startsWith('/assets/decode-'))).toBe(true);
    expect(bundle.build).toMatch(/^[0-9a-f]{16}$/u);
    expect(sw).toContain(`pterm-assets-${bundle.build}`);
    expect(readFileSync(join(outDir, 'index.html'), 'utf8')).toContain(`<meta name="pterm-build" content="${bundle.build}" />`);
  }, 60_000);
});

interface ManifestEntry {
  readonly file: string;
  readonly imports?: ReadonlyArray<string>;
  readonly dynamicImports?: ReadonlyArray<string>;
  readonly css?: ReadonlyArray<string>;
}

/** Every file a manifest entry needs at once: its own file, its static imports and their CSS. */
function closure(manifest: Readonly<Record<string, ManifestEntry>>, key: string, found = new Set<string>()): Set<string> {
  const entry = manifest[key];
  if (entry === undefined || found.has(`/${entry.file}`)) return found;
  found.add(`/${entry.file}`);
  for (const css of entry.css ?? []) found.add(`/${css}`);
  for (const next of entry.imports ?? []) closure(manifest, next, found);
  return found;
}

describe('release gate: open an unvisited Wall while home is offline', () => {
  it('the installed worker serves the /wall page and every chunk the Wall needs from its cache, with no network', async () => {
    const outDir = join(work, 'offline');
    await build({
      root,
      configFile: false,
      logLevel: 'silent',
      plugins: [react(), serviceWorker(), protocolValidators(), precompressAssets()],
      build: { outDir, emptyOutDir: true, sourcemap: 'hidden', manifest: true },
    });
    // The real built /sw.js in a fake worker global; the network is home's files until home goes offline.
    let online = true;
    const network = vi.fn(async (input: unknown) => {
      if (!online) throw new TypeError('offline');
      const path = String(input);
      return new Response(readFileSync(join(outDir, path)), {
        headers: { 'Content-Type': path.endsWith('.html') ? 'text/html; charset=utf-8' : 'application/octet-stream' },
      });
    });
    const store = new Map<string, Map<string, Response>>();
    const caches = {
      open: async (name: string) => {
        const entries = store.get(name) ?? new Map<string, Response>();
        store.set(name, entries);
        return { match: async (key: string) => entries.get(key)?.clone(), put: async (key: string, response: Response) => { entries.set(key, response); } };
      },
      keys: async () => [...store.keys()],
      delete: async (name: string) => store.delete(name),
    };
    const handlers = new Map<string, (event: Record<string, unknown>) => void>();
    const self = {
      location: { origin: 'https://home.example' },
      registration: { scope: 'https://home.example/' },
      addEventListener: (type: string, handler: (event: Record<string, unknown>) => void) => handlers.set(type, handler),
      skipWaiting: () => undefined,
      clients: { claim: async () => undefined },
    };
    new Function('self', 'caches', 'fetch', readFileSync(join(outDir, 'sw.js'), 'utf8'))(self, caches, network);
    let installed: Promise<unknown> = Promise.resolve();
    handlers.get('install')?.({ waitUntil: (work: Promise<unknown>) => { installed = work; } });
    await installed;

    // The user never opened the Wall: only the install fetched its chunks.
    online = false;
    const fetchHandler = handlers.get('fetch');
    const answer = (url: string, mode = 'no-cors') => {
      let reply: Promise<Response> | undefined;
      fetchHandler?.({
        request: mode === 'navigate' ? { method: 'GET', url, mode } : new Request(url),
        respondWith: (value: Promise<Response>) => { reply = value; },
        waitUntil: () => undefined,
      });
      return reply;
    };
    const page = await answer('https://home.example/wall', 'navigate');
    expect(page?.status).toBe(200);
    expect(await page?.text()).toBe(readFileSync(join(outDir, 'index.html'), 'utf8'));

    const manifest = JSON.parse(readFileSync(join(outDir, '.vite/manifest.json'), 'utf8')) as Record<string, ManifestEntry>;
    expect(manifest['index.html']?.dynamicImports).toContain('src/screens/Wall.tsx');
    const needed = closure(manifest, 'src/screens/Wall.tsx', closure(manifest, 'index.html'));
    expect([...needed].some((file) => file.startsWith('/assets/Wall-'))).toBe(true);
    // The QR decoder loads only on Scan QR: its own chunk, in neither the start-up code nor the Computers screen.
    const decoder = Object.values(manifest).find((entry) => entry.file.startsWith('assets/decode-'));
    expect(decoder).toBeDefined();
    expect(manifest['src/screens/Computers.tsx']?.dynamicImports?.some((key) => manifest[key] === decoder)).toBe(true);
    expect(closure(manifest, 'src/screens/Computers.tsx', closure(manifest, 'index.html')).has(`/${decoder?.file}`)).toBe(false);
    expect((await answer(`https://home.example/${decoder?.file}`))?.status).toBe(200);
    for (const file of needed) {
      const reply = await answer(`https://home.example${file}`);
      expect(reply?.status, file).toBe(200);
      expect(Buffer.from(await reply!.arrayBuffer()).equals(readFileSync(join(outDir, file))), file).toBe(true);
    }
  }, 60_000);
});
