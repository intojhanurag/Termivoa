import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import standaloneCode from 'ajv/dist/standalone';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import envelopeSchema from '../protocol/v1/envelope.schema.json' with { type: 'json' };
import { createProtocolAjv } from './src/protocol/ajv.ts';

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

const VALIDATORS = 'virtual:protocol-validators';

/**
 * Compiles the envelope schema to standalone JavaScript at build/test time.
 * The browser never runs Ajv's compiler, so no `Function`/eval is needed and
 * the gateway CSP can omit 'unsafe-eval'.
 */
function protocolValidators(): Plugin {
  return {
    name: 'pterm-protocol-validators',
    resolveId: (id) => (id === VALIDATORS ? `\0${VALIDATORS}` : undefined),
    load(id) {
      if (id !== `\0${VALIDATORS}`) {
        return undefined;
      }
      const ajv = createProtocolAjv({ standalone: true });
      ajv.addSchema(envelopeSchema, 'envelope');
      return standaloneCode(ajv, { validateEnvelope: 'envelope' });
    },
  };
}

export default defineConfig({
  plugins: [react(), serviceWorker(), protocolValidators()],
  build: {
    sourcemap: 'hidden',
  },
});
