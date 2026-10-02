import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, sep } from 'node:path';
import standaloneCode from 'ajv/dist/standalone';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import brokerRPCSchema from '../protocol/v1/broker-rpc.schema.json' with { type: 'json' };
import envelopeSchema from '../protocol/v1/envelope.schema.json' with { type: 'json' };
import httpAPISchema from '../protocol/v1/http-api.schema.json' with { type: 'json' };
import { createProtocolAjv } from './src/protocol/ajv.ts';
import type { HttpSchema } from './src/protocol/httpBody.ts';

/**
 * Bundle contract with web/src/sw.js (track W2 precaches exactly this):
 *
 * - `files`: every file of this build as a root-relative URL path ("/index.html",
 *   "/assets/…", "/manifest.webmanifest", icons), sorted. Source maps and
 *   /sw.js itself are left out.
 * - `build`: a 16-hex ID, a hash over every listed file's path and content.
 *
 * Both reach the worker atomically inside /sw.js: the token `__BUILD__` becomes
 * the build ID and `__BUNDLE__` becomes the JSON `{"build":"…","files":[…]}`.
 * index.html carries the same ID as `<meta name="pterm-build" content="…">`, so
 * the worker can tell whether a fetched shell belongs to its own build.
 */
export function serviceWorker(template: URL = new URL('./src/sw.js', import.meta.url)): Plugin {
  let publicDir = '';
  return {
    name: 'pterm-service-worker',
    apply: 'build',
    // After Vite's own plugins, so index.html is already in the bundle.
    enforce: 'post',
    configResolved(config) {
      publicDir = config.publicDir;
    },
    generateBundle(_options, bundle) {
      const contents = new Map<string, string | Uint8Array>();
      for (const [name, output] of Object.entries(bundle)) {
        contents.set(name, output.type === 'chunk' ? output.code : output.source);
      }
      if (publicDir !== '' && existsSync(publicDir)) {
        for (const name of readdirSync(publicDir, { recursive: true, encoding: 'utf8' })) {
          const path = join(publicDir, name);
          if (statSync(path).isFile()) contents.set(name.split(sep).join('/'), readFileSync(path));
        }
      }
      const files = [...contents.keys()].filter((name) => !name.endsWith('.map') && name !== 'sw.js').sort();
      const hash = createHash('sha256');
      for (const name of files) hash.update(`${name}\0`).update(contents.get(name) ?? '').update('\0');
      const build = hash.digest('hex').slice(0, 16);
      const html = bundle['index.html'];
      if (html?.type !== 'asset' || typeof html.source !== 'string') {
        throw new Error('pterm-service-worker: index.html is missing from the bundle');
      }
      html.source = html.source.replace('<head>', `<head>\n    <meta name="pterm-build" content="${build}" />`);
      const manifest = JSON.stringify({ build, files: files.map((name) => `/${name}`) });
      this.emitFile({
        type: 'asset',
        fileName: 'sw.js',
        source: readFileSync(template, 'utf8').replaceAll('__BUILD__', build).replaceAll('__BUNDLE__', manifest),
      });
    },
  };
}

/** WebSocket envelope validator: loaded with Connection (a lazy chunk). */
const ENVELOPE_VALIDATORS = 'virtual:protocol-validators';
/** HTTP body validators: loaded at start by api/http.ts. */
const HTTP_VALIDATORS = 'virtual:http-validators';

const SCHEMA = 'https://phone-terminal.invalid/protocol/v1/';

/**
 * HTTP API bodies the app reads (`HttpBodies` in src/protocol/httpBody.ts), by
 * `$defs` name in http-api.schema.json. List items are separate validators so
 * one bad item is dropped without losing the list.
 */
const HTTP_NAMES = [
  'settingsResponse', 'namedOption', 'sessionListResponse', 'sessionMutationResponse', 'deviceSummary',
  'pairRedeemResponse', 'pairPollResponse', 'previewConfigResponse', 'pushConfigResponse', 'pushTestResponse',
  'uploadResponse', 'computerListResponse', 'computer', 'computerAddResponse', 'errorResponse',
] as const satisfies ReadonlyArray<HttpSchema>;

/** Every HttpSchema name to its schema ref; a name missing here fails the type check. */
const HTTP_SCHEMAS: Readonly<Record<HttpSchema, string>> = {
  ...(Object.fromEntries(HTTP_NAMES.map((name) => [name, `http-api.schema.json#/$defs/${name}`])) as Record<(typeof HTTP_NAMES)[number], string>),
  // The session record is defined once, in the broker RPC schema.
  sessionRecord: 'broker-rpc.schema.json#/$defs/sessionRecord',
};

/** Standalone ES module code for the given exports (name → schema id or ref). */
function standaloneModule(exports: Record<string, string>): string {
  const ajv = createProtocolAjv({ standalone: true, lenient: true });
  ajv.addSchema(envelopeSchema);
  ajv.addSchema(brokerRPCSchema);
  ajv.addSchema(httpAPISchema);
  // Even with `code.esm`, Ajv emits its runtime helpers (e.g. ucs2length for
  // maxLength) as CommonJS `require("ajv/dist/runtime/…").default`. The build
  // bundler resolves that, but the dev server serves this module to the
  // browser as-is (#123), so hoist each one into an ES import. The default
  // import is `module.exports` in dev but `exports.default` in the build, so
  // rebuild what `require()` returns in both cases.
  const imports: string[] = [];
  const code = standaloneCode(ajv, exports).replace(
    /require\(("[^"]+")\)/g,
    (_match, specifier: string) => {
      const name = `ajvRuntime${imports.length}`;
      imports.push(
        `import ${name}Import from ${specifier}; const ${name} = ${name}Import?.__esModule ? ${name}Import : { default: ${name}Import };`,
      );
      return name;
    },
  );
  if (code.includes('require(')) {
    throw new Error('protocol validators: Ajv emitted a require() this plugin does not convert');
  }
  return `${imports.join('\n')}\n${code}`;
}

/**
 * Compiles the one lenient validator set (WebSocket envelope and HTTP bodies,
 * for every computer, all from createProtocolAjv with `lenient`) to standalone
 * JavaScript at build/test time, as two modules so the large envelope
 * validator stays out of the start-up chunk. The browser never runs Ajv's
 * compiler, so no `Function`/eval is needed and the gateway CSP can omit
 * 'unsafe-eval'.
 */
export function protocolValidators(): Plugin {
  return {
    name: 'pterm-protocol-validators',
    resolveId: (id) => (id === ENVELOPE_VALIDATORS || id === HTTP_VALIDATORS ? `\0${id}` : undefined),
    load(id) {
      if (id === `\0${ENVELOPE_VALIDATORS}`) {
        return standaloneModule({ validateEnvelope: envelopeSchema.$id });
      }
      if (id === `\0${HTTP_VALIDATORS}`) {
        const exports = Object.fromEntries(Object.entries(HTTP_SCHEMAS).map(([name, ref]) => [`http_${name}`, `${SCHEMA}${ref}`]));
        const http = Object.keys(HTTP_SCHEMAS).map((name) => `${name}: http_${name}`).join(', ');
        return `${standaloneModule(exports)}\nexport const httpValidators = { ${http} };`;
      }
      return undefined;
    },
  };
}

export default defineConfig({
  plugins: [react(), serviceWorker(), protocolValidators()],
  build: {
    sourcemap: 'hidden',
  },
});
