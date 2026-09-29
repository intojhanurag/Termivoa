import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { ChangedFile, ChangesPatchPayload, ChangesSummaryPayload } from '../generated/protocol';
import type { ChangesView } from '../terminal/Connection';
import { readDraft, saveDraft } from '../terminal/composer';
import { askAboutFile, ChangesPanel, splitPath } from './Changes';

const noop = () => undefined;
const files: ChangedFile[] = [
  { path: 'web/src/screens/Live.tsx', status: 'modified', additions: 12, deletions: 3, binary: false },
  { path: 'README.md', status: 'untracked', additions: 1, deletions: 0, binary: false },
  { path: 'docs/new.md', oldPath: 'docs/old.md', status: 'renamed', additions: 0, deletions: 1, binary: false },
  { path: 'logo.png', status: 'added', additions: 0, deletions: 0, binary: true },
];
const summary = (extra: Partial<ChangesSummaryPayload> = {}): ChangesSummaryPayload => ({
  state: 'ready', root: '/Users/me/app', branch: 'main', files, truncated: false, ...extra,
});
const patch = (extra: Partial<ChangesPatchPayload> = {}): ChangesPatchPayload => ({
  path: 'web/src/screens/Live.tsx',
  state: 'ready',
  truncated: false,
  lines: [
    { kind: 'hunk', text: '@@ -9,3 +9,3 @@ export function Live' },
    { kind: 'context', text: 'const a = 1;', oldLine: 9, newLine: 9 },
    { kind: 'remove', text: 'const b = 2;', oldLine: 10 },
    { kind: 'add', text: 'const b = 3;', newLine: 10 },
  ],
  ...extra,
});

function panel(view: ChangesView | undefined, handlers: Partial<Parameters<typeof ChangesPanel>[0]> = {}) {
  return (
    <ChangesPanel
      view={view}
      cwd="/Users/me/app/web"
      onRefresh={noop}
      onOpen={noop}
      onBack={noop}
      onAsk={noop}
      onReturn={noop}
      {...handlers}
    />
  );
}

const list = (extra: Partial<ChangesView> = {}): ChangesView => ({ state: 'ready', summary: summary(), file: undefined, ...extra });
const html = (view: ChangesView | undefined) => renderToStaticMarkup(panel(view));

/** Host buttons by visible text or label; the panel's components have no hooks, so they are called directly. */
function buttons(node: ReactNode, found = new Map<string, () => void>()): Map<string, () => void> {
  if (Array.isArray(node)) {
    node.forEach((child: ReactNode) => buttons(child, found));
  } else if (isValidElement(node)) {
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void; 'aria-label'?: string }>;
    if (typeof node.type === 'function') {
      buttons((node.type as (props: unknown) => ReactNode)(props), found);
    } else if (node.type === 'button' && props.onClick !== undefined) {
      found.set(props['aria-label'] ?? renderToStaticMarkup(<>{props.children}</>).replace(/<[^>]+>/gu, ''), props.onClick);
    }
    buttons(props.children, found);
  }
  return found;
}

describe('changes list', () => {
  it('shows the folder, branch, file count and totals, and one row per file', () => {
    const markup = html(list());
    expect(markup).toContain('<h2 id="changes-title">Changes</h2>');
    expect(markup).toContain('~/app · main · 4 files · +13 −4');
    expect(markup).toContain('<span class="change-status modified" title="Modified"><span aria-hidden="true">M</span><span class="visually-hidden">Modified: </span></span>');
    expect(markup).toContain('<span class="change-dir">web/<wbr/>src/<wbr/>screens/<wbr/></span><span class="change-name">Live.tsx</span>');
    expect(markup).toContain('<span class="change-add">+12</span><span class="change-del">−3</span>');
    expect(markup).toMatch(/class="change-status untracked".*>U</u);
    expect(markup).toContain('<span class="change-add">+1</span><span class="change-del zero">−0</span>');
    expect(markup).toContain('<span class="change-from">from docs/old.md</span>');
    expect(markup).toContain('<span class="change-binary">binary</span>');
    expect(markup.match(/<button type="button" class="change-row">/gu)).toHaveLength(4);
    expect(markup).not.toContain('Only the first');
  });

  it('opens a file, refreshes and returns', () => {
    const onOpen = vi.fn();
    const onRefresh = vi.fn();
    const onReturn = vi.fn();
    const found = buttons(panel(list(), { onOpen, onRefresh, onReturn }));
    found.get('MModified: web/src/screens/Live.tsx+12−3')?.();
    expect(onOpen).toHaveBeenCalledWith('web/src/screens/Live.tsx');
    found.get('Refresh')?.();
    found.get('Done')?.();
    expect(onRefresh).toHaveBeenCalledOnce();
    expect(onReturn).toHaveBeenCalledOnce();
  });

  it('says each state in plain words', () => {
    expect(html(undefined)).toContain('<p role="status">Loading changes…</p>');
    expect(html(list({ summary: summary({ files: [] }) }))).toContain('<p role="status">No changes. The working tree is clean.</p>');
    expect(html(list({ summary: summary({ files: [] }) }))).toContain('0 files · +0 −0');

    const notGit = html(list({ summary: { state: 'not_git', files: [], truncated: false } }));
    expect(notGit).toContain('<p role="status">This folder is not a git repository.</p>');
    expect(notGit).toContain('<p class="changes-detail">~/app/web</p>');

    for (const view of [list({ summary: summary({ state: 'unavailable', files: [] }) }), list({ state: 'unavailable' })]) {
      const markup = html(view);
      expect(markup).toContain('<p role="status">Could not read changes.</p><button type="button">Retry</button>');
      expect(markup).not.toContain('change-row');
    }

    expect(html(list({ summary: summary({ truncated: true }) })))
      .toContain('<p class="changes-note" role="status">Only the first 4 files are shown.</p>');
  });

  it('keeps the last list while a refresh loads', () => {
    const markup = html(list({ state: 'loading' }));
    expect(markup).toContain('<button type="button" class="link" disabled="">Refreshing…</button>');
    expect(markup).toContain('change-row');
  });
});

describe('changes file', () => {
  const open = (file: ChangesView['file']) => list({ file });

  it('shows the name, counts and each line kind with old and new line numbers', () => {
    const markup = html(open({ path: 'web/src/screens/Live.tsx', state: 'ready', patch: patch() }));
    expect(markup).toContain('<section class="history changes" aria-labelledby="changes-title" hidden="">');
    expect(markup).toContain('<h2 id="changes-file-title" class="changes-file-name">Live.tsx</h2>');
    expect(markup).toContain('<span>web/src/screens/ · </span><span class="change-add">+12</span><span class="change-del">−3</span>');
    expect(markup).toContain('style="--digits:2"');
    expect(markup).toContain('<div class="diff-line hunk"><span class="diff-text">@@ -9,3 +9,3 @@ export function Live</span></div>');
    expect(markup).toContain('<div class="diff-line context"><span class="diff-num" aria-hidden="true">9</span><span class="diff-num" aria-hidden="true">9</span><span class="diff-sign"> </span><span class="diff-text">const a = 1;</span></div>');
    expect(markup).toContain('<div class="diff-line remove"><span class="diff-num" aria-hidden="true">10</span><span class="diff-num" aria-hidden="true"></span><span class="diff-sign">-</span><span class="diff-text">const b = 2;</span></div>');
    expect(markup).toContain('<div class="diff-line add"><span class="diff-num" aria-hidden="true"></span><span class="diff-num" aria-hidden="true">10</span><span class="diff-sign">+</span>');
    expect(markup).not.toContain('role="status"');
  });

  it('says each file state in plain words', () => {
    const path = 'logo.png';
    expect(html(open({ path, state: 'loading', patch: undefined }))).toContain('<p role="status">Loading diff…</p>');
    expect(html(open({ path, state: 'ready', patch: patch({ path, state: 'binary', lines: [] }) })))
      .toContain('<p role="status">Binary file — not shown.</p>');
    expect(html(open({ path, state: 'ready', patch: patch({ path, state: 'missing', lines: [] }) })))
      .toContain('<p role="status">This file has no changes anymore.</p><button type="button">Refresh</button>');
    expect(html(open({ path, state: 'unavailable', patch: undefined })))
      .toContain('<p role="status">Could not read this file.</p><button type="button">Retry</button>');
    expect(html(open({ path, state: 'ready', patch: patch({ state: 'unavailable', lines: [] }) })))
      .toContain('Could not read this file.');
    expect(html(open({ path: 'a.ts', state: 'ready', patch: patch({ truncated: true }) })))
      .toContain('<p class="changes-note" role="status">The diff is cut short here. See the rest on your computer.</p>');
  });

  it('goes back and asks the agent about the open file', () => {
    const onBack = vi.fn();
    const onAsk = vi.fn();
    const found = buttons(panel(open({ path: 'web/src/screens/Live.tsx', state: 'ready', patch: patch() }), { onBack, onAsk }));
    found.get('Back to changed files')?.();
    found.get('@Ask agent about this file')?.();
    expect(onBack).toHaveBeenCalledOnce();
    expect(onAsk).toHaveBeenCalledWith('web/src/screens/Live.tsx');
  });
});

describe('ask agent about a file', () => {
  it('adds an @path mention to the session draft and sends nothing', () => {
    const key = 'session/generation';
    askAboutFile(key, 'src/a.ts');
    expect(readDraft(key)).toBe('@src/a.ts ');
    askAboutFile(key, 'src/b.ts');
    expect(readDraft(key)).toBe('@src/a.ts @src/b.ts ');
    saveDraft(key, 'Why did this change?');
    askAboutFile(key, 'src/a.ts');
    expect(readDraft(key)).toBe('Why did this change? @src/a.ts ');
    saveDraft(key, '');
  });

  it('splits a path into its muted directory and its name', () => {
    expect(splitPath('src/app/main.ts')).toEqual({ dir: 'src/app/', name: 'main.ts' });
    expect(splitPath('README.md')).toEqual({ dir: '', name: 'README.md' });
  });
});
