import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { AppErrorBoundary, ChunkErrorBoundary, ChunkLoadError, loadChunk, UpdateRequired } from './chunks';

const src = resolve(dirname(fileURLToPath(import.meta.url)), '..');

/** Modules reachable from `entry` through static (non-type) imports. */
function staticImports(entry: string): Set<string> {
  const seen = new Set<string>();
  const visit = (file: string) => {
    if (seen.has(file)) return;
    seen.add(file);
    if (!file.startsWith(src)) return;
    const code = readFileSync(file, 'utf8');
    for (const match of code.matchAll(/^import\s+(?!type\s)(?:[^'"]*?\sfrom\s+)?['"]([^'"]+)['"]/gmu)) {
      const spec = match[1] ?? '';
      if (!spec.startsWith('.')) {
        seen.add(spec);
        continue;
      }
      const base = resolve(dirname(file), spec);
      const found = ['', '.ts', '.tsx'].map((ext) => base + ext).find((path) => {
        try { return readFileSync(path) !== undefined; } catch { return false; }
      });
      if (found !== undefined) visit(found);
    }
  };
  visit(entry);
  return seen;
}

describe('first-load split', () => {
  it('keeps xterm, the terminal controllers and Live out of the entry chunk', () => {
    const reachable = [...staticImports(resolve(src, 'main.tsx'))];
    expect(reachable).toContain(resolve(src, 'screens/Sessions.tsx'));
    expect(reachable.filter((item) => item.startsWith('@xterm'))).toEqual([]);
    expect(reachable).not.toContain(resolve(src, 'screens/Live.tsx'));
    expect(reachable).not.toContain(resolve(src, 'terminal/Connection.ts'));
    expect(reachable).not.toContain('@tanstack/react-virtual');
  });

  it('keeps the v2 terminal dock, sheets and run-first code in the Live chunk', () => {
    const reachable = [...staticImports(resolve(src, 'main.tsx'))];
    expect(reachable).toContain(resolve(src, 'screens/NewSession.tsx'));
    for (const file of ['screens/Dock.tsx', 'screens/Snippets.tsx', 'screens/SessionMenu.tsx', 'screens/Composer.tsx',
      'screens/History.tsx', 'terminal/agentKeys.ts', 'terminal/snippets.ts', 'terminal/runFirst.ts']) {
      expect(reachable).not.toContain(resolve(src, file));
    }
  });
});

describe('chunk load failures', () => {
  it('turns a failed dynamic import into ChunkLoadError and passes success through', async () => {
    const missing = new TypeError('Failed to fetch dynamically imported module: /assets/Live-old.js');
    const failure = await loadChunk(() => Promise.reject(missing)).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ChunkLoadError);
    expect((failure as Error).cause).toBe(missing);
    await expect(loadChunk(() => Promise.resolve(42))).resolves.toBe(42);
  });

  it('shows the reload screen for a chunk error instead of a blank page', () => {
    const boundary = new ChunkErrorBoundary({ children: <p>live</p> });
    expect(renderToStaticMarkup(<>{boundary.render()}</>)).toBe('<p>live</p>');

    boundary.state = ChunkErrorBoundary.getDerivedStateFromError(new ChunkLoadError(new Error('404')));
    const html = renderToStaticMarkup(<>{boundary.render()}</>);
    expect(html).toContain('role="alert"');
    expect(html).toContain('A new version is available');
    expect(html).toContain('>Reload</button>');
  });

  it('does not hide other render errors behind the reload screen', () => {
    const boundary = new ChunkErrorBoundary({ children: null });
    const bug = new Error('bug');
    boundary.state = ChunkErrorBoundary.getDerivedStateFromError(bug);
    expect(() => boundary.render()).toThrow(bug);
  });

  it('renders the update screen with an accessible Reload button', () => {
    expect(renderToStaticMarkup(<UpdateRequired onReload={() => undefined} />))
      .toContain('<button type="button" class="primary">Reload</button>');
  });
});

describe('app error boundary', () => {
  it('shows the error and a reload instead of a blank page', () => {
    const boundary = new AppErrorBoundary({ children: <p>app</p> });
    expect(renderToStaticMarkup(<>{boundary.render()}</>)).toBe('<p>app</p>');
    boundary.state = AppErrorBoundary.getDerivedStateFromError(new TypeError('x is undefined'));
    const html = renderToStaticMarkup(<>{boundary.render()}</>);
    expect(html).toContain('Something went wrong');
    expect(html).toContain('TypeError: x is undefined');
    expect(html).toContain('>Reload</button>');
  });

  it('still asks for an update when screen code could not load', () => {
    const boundary = new AppErrorBoundary({ children: null });
    boundary.state = AppErrorBoundary.getDerivedStateFromError(new ChunkLoadError(new Error('404')));
    expect(renderToStaticMarkup(<>{boundary.render()}</>)).toContain('A new version is available');
  });
});
