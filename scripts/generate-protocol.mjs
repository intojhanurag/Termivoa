import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const schema = path.join(root, 'protocol/v1/envelope.schema.json');
const outputs = [
  ['go', path.join(root, 'internal/protocol/generated.go'), ['--package', 'protocol']],
  ['typescript', path.join(root, 'web/src/generated/protocol.ts'), []],
];

for (const [language, output, extra] of outputs) {
  mkdirSync(path.dirname(output), { recursive: true });
  const result = spawnSync(
    'pnpm',
    [
      'exec',
      'quicktype',
      '--src-lang',
      'schema',
      '--lang',
      language,
      '--src',
      schema,
      '--out',
      output,
      '--top-level',
      'Envelope',
      ...extra,
    ],
    { cwd: root, encoding: 'utf8', stdio: 'inherit' },
  );
  if (result.error !== undefined) {
    throw result.error;
  }
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
