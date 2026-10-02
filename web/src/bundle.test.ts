import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import react from '@vitejs/plugin-react';
import { build } from 'vite';
import { afterAll, describe, expect, it } from 'vitest';
import { protocolValidators, serviceWorker } from '../vite.config';

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
      plugins: [react(), serviceWorker(pathToFileURL(template)), protocolValidators()],
      build: { outDir, emptyOutDir: true, sourcemap: 'hidden' },
    });
    const sw = readFileSync(join(outDir, 'sw.js'), 'utf8');
    const bundle = JSON.parse(/self\.BUNDLE = (.*);/u.exec(sw)?.[1] ?? '') as { build: string; files: string[] };
    const onDisk = readdirSync(outDir, { recursive: true, encoding: 'utf8' })
      .filter((name) => name.includes('.') && !name.endsWith('.map') && name !== 'sw.js')
      .map((name) => `/${name}`)
      .sort();
    expect(bundle.files).toEqual(onDisk);
    expect(bundle.files).toContain('/index.html');
    expect(bundle.files).toContain('/manifest.webmanifest');
    expect(bundle.files.some((name) => name.startsWith('/assets/Wall-'))).toBe(true);
    expect(bundle.build).toMatch(/^[0-9a-f]{16}$/u);
    expect(sw).toContain(`pterm-assets-${bundle.build}`);
    expect(readFileSync(join(outDir, 'index.html'), 'utf8')).toContain(`<meta name="pterm-build" content="${bundle.build}" />`);
  }, 60_000);
});
