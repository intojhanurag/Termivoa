import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

/** Emits /sw.js with a cache name derived from this build's hashed assets. */
function serviceWorker(): Plugin {
  return {
    name: 'pterm-service-worker',
    apply: 'build',
    generateBundle(_options, bundle) {
      const assets = Object.keys(bundle).filter((name) => name.startsWith('assets/')).sort();
      const build = createHash('sha256').update(assets.join('\n')).digest('hex').slice(0, 16);
      const template = readFileSync(new URL('./src/sw.js', import.meta.url), 'utf8');
      this.emitFile({ type: 'asset', fileName: 'sw.js', source: template.replace('__BUILD__', build) });
    },
  };
}

export default defineConfig({
  plugins: [react(), serviceWorker()],
  build: {
    sourcemap: true,
  },
});
