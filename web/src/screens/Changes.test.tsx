import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { ChangedFile, ChangesPatchPayload, ChangesSummaryPayload } from '../generated/protocol';
import type { ChangesView, ChangesWrite } from '../terminal/Connection';
import { readDraft, saveDraft } from '../terminal/composer';
import { askAboutFile, ChangesPanel, httpsUrl, ShipFlow, splitPath, suggestMessage, type CommitControls } from './Changes';

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
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void; onChange?: () => void; 'aria-label'?: string }>;
    if (typeof node.type === 'function') {
      buttons((node.type as (props: unknown) => ReactNode)(props), found);
    } else if (node.type === 'button' && props.onClick !== undefined) {
      found.set(props['aria-label'] ?? renderToStaticMarkup(<>{props.children}</>).replace(/<[^>]+>/gu, ''), props.onClick);
    } else if (node.type === 'input' && props.onChange !== undefined && props['aria-label'] !== undefined) {
      found.set(props['aria-label'], props.onChange);
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

describe('commit and push', () => {
  const controls = (extra: Partial<CommitControls> = {}): CommitControls => ({
    control: true, excluded: new Set(), pending: false, covered: false, onToggle: noop, onCommit: noop, ...extra,
  });
  const listHtml = (commit: CommitControls, view = list()) => renderToStaticMarkup(panel(view, { commit }));

  it('suggests a message from the selected files', () => {
    const file = (path: string, status: ChangedFile['status'] = 'modified') => ({ path, status });
    expect(suggestMessage([file('src/app.js')])).toBe('Update app.js');
    expect(suggestMessage([file('src/app.js'), file('lib/util.js')])).toBe('Update app.js, util.js');
    expect(suggestMessage([file('a.js'), file('b.js'), file('c.js')])).toBe('Update a.js, b.js, c.js');
    expect(suggestMessage(['app.js', 'b', 'c', 'd', 'e'].map((path) => file(path)))).toBe('Update app.js and 4 more files');
    expect(suggestMessage([file('new.ts', 'untracked'), file('logo.png', 'added')])).toBe('Add new.ts, logo.png');
    expect(suggestMessage([file('new.ts', 'untracked'), file('app.js')])).toBe('Update new.ts, app.js');
    expect(suggestMessage([])).toBe('');
  });

  it('ticks every file by default and counts the ticked ones on the commit button', () => {
    const markup = listHtml(controls());
    expect(markup.match(/<input type="checkbox" aria-label="Include [^"]+" checked=""\/>/gu)).toHaveLength(4);
    expect(markup).toContain('<button type="button" class="primary wide">Commit 4 files</button>');

    const some = listHtml(controls({ excluded: new Set(['README.md', 'logo.png', 'docs/new.md']) }));
    expect(some).toContain('<input type="checkbox" aria-label="Include README.md"/>');
    expect(some).toContain('Commit 1 file</button>');
    const none = listHtml(controls({ excluded: new Set(files.map((item) => item.path)) }));
    expect(none).toContain('<button type="button" class="primary wide" disabled="">Commit 0 files</button>');
    expect(listHtml(controls({ pending: true }))).toContain('disabled="">Working…</button>');
    expect(listHtml(controls({ covered: true }))).toContain('aria-labelledby="changes-title" inert=""');

    const onToggle = vi.fn();
    const onCommit = vi.fn();
    const found = buttons(panel(list(), { commit: controls({ onToggle, onCommit }) }));
    found.get('Include README.md')?.();
    found.get('Commit 4 files')?.();
    expect(onToggle).toHaveBeenCalledWith('README.md');
    expect(onCommit).toHaveBeenCalledOnce();
  });

  it('shows a hint instead of checkboxes and the button without control, and nothing for an empty list', () => {
    const markup = listHtml(controls({ control: false }));
    expect(markup).not.toContain('type="checkbox"');
    expect(markup).not.toContain('Commit 4');
    expect(markup).toContain('<p class="hint-text">Take control to commit</p>');
    expect(listHtml(controls(), list({ summary: summary({ files: [] }) }))).not.toContain('changes-commit');
    expect(html(list())).not.toContain('changes-commit');
  });

  const flow = (props: Partial<Parameters<typeof ShipFlow>[0]> = {}) => (
    <ShipFlow
      step="commit"
      write={undefined}
      control
      message="Update app.js"
      count={2}
      branch="main"
      onMessage={noop}
      onCommit={noop}
      onPush={noop}
      onConfirmPush={noop}
      onClose={noop}
      {...props}
    />
  );
  const flowHtml = (props: Partial<Parameters<typeof ShipFlow>[0]> = {}) => renderToStaticMarkup(flow(props));
  const done = (extra: Partial<ChangesWrite>): ChangesWrite => ({ kind: 'commit', state: 'done', ...extra });

  it('shows the message, the file count, Commit and Cancel, and waits while pending', () => {
    const markup = flowHtml();
    expect(markup).toContain('<label class="visually-hidden" for="commit-message">Commit message</label>');
    expect(markup).toContain('>Update app.js</textarea>');
    expect(markup).toContain('2 files selected');
    expect(markup).toContain('<button type="button">Cancel</button><button type="button" class="primary">Commit</button>');
    expect(flowHtml({ message: '  ' })).toContain('class="primary" disabled="">Commit</button>');
    expect(flowHtml({ control: false })).toContain('Take control to commit');

    const pending = flowHtml({ write: { kind: 'commit', state: 'pending' } });
    expect(pending).toContain('<button type="button" disabled="">Cancel</button><button type="button" class="primary" disabled="">Committing…</button>');

    const onCommit = vi.fn();
    const onClose = vi.fn();
    const found = buttons(flow({ onCommit, onClose }));
    found.get('Commit')?.();
    found.get('Cancel')?.();
    expect(onCommit).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('says each commit result, shows failure output, and offers Push after a commit', () => {
    const committed = flowHtml({ write: done({ committed: { state: 'committed', commit: 'abc1234' } }) });
    expect(committed).toContain('<p class="ship-text" role="status">Committed <code>abc1234</code></p>');
    expect(committed).toContain('<button type="button">Close</button><button type="button" class="primary">Push</button>');

    const nothing = flowHtml({ write: done({ committed: { state: 'nothing' } }) });
    expect(nothing).toContain('role="status">Nothing to commit. The selected files have no changes now.</p>');
    expect(nothing).not.toContain('Push');

    const failed = flowHtml({ write: done({ committed: { state: 'failed', output: 'husky - pre-commit hook exited with code 1' } }) });
    expect(failed).toContain('role="status">The commit did not go through.</p>');
    expect(failed).toContain('<pre class="ship-output" tabindex="0" aria-label="Output from git">husky - pre-commit hook exited with code 1</pre>');
    expect(failed).toContain('<button type="button">Close</button></div>');

    const refused = flowHtml({ write: { kind: 'commit', state: 'error', message: 'Connection lost — check the Changes list before trying again.' } });
    expect(refused).toContain('role="status">Connection lost — check the Changes list before trying again.</p>');

    const onPush = vi.fn();
    buttons(flow({ write: done({ committed: { state: 'committed', commit: 'abc1234' } }), onPush })).get('Push')?.();
    expect(onPush).toHaveBeenCalledOnce();
  });

  it('confirms before a push, then says each push result', () => {
    const commitDone = done({ committed: { state: 'committed', commit: 'abc1234' } });
    const confirm = flowHtml({ step: 'push', write: commitDone });
    expect(confirm).toContain('<p class="ship-text">Push <code>main</code> to its remote? This does not force.</p>');
    expect(confirm).toContain('<button type="button">Cancel</button><button type="button" class="primary">Push</button>');
    const onConfirmPush = vi.fn();
    buttons(flow({ step: 'push', write: commitDone, onConfirmPush })).get('Push')?.();
    expect(onConfirmPush).toHaveBeenCalledOnce();

    expect(flowHtml({ step: 'push', write: { kind: 'push', state: 'pending' } }))
      .toContain('role="status">Pushing…</p><div class="ship-actions"><button type="button" disabled="">Close</button>');

    const pushed = flowHtml({ step: 'push', write: { kind: 'push', state: 'done', pushed: { state: 'pushed', branch: 'feat/x', url: 'https://github.com/o/r/pull/new/feat/x' } } });
    expect(pushed).toContain('role="status">Pushed <code>feat/x</code></p>');
    expect(pushed).toContain('<a class="ship-link" href="https://github.com/o/r/pull/new/feat/x" target="_blank" rel="noopener noreferrer">Open pull request</a>');

    const noRemote = flowHtml({ step: 'push', write: { kind: 'push', state: 'done', pushed: { state: 'no_remote', output: 'fatal: no remote' } } });
    expect(noRemote).toContain('This repository has no remote to push to.');
    expect(noRemote).toContain('>fatal: no remote</pre>');
    const failed = flowHtml({ step: 'push', write: { kind: 'push', state: 'done', pushed: { state: 'failed', output: '! [rejected] main -> main (non-fast-forward)' } } });
    expect(failed).toContain('The push did not go through.');
    expect(failed).toContain('[rejected]');
  });

  it('ignores a pull request link that is not https', () => {
    for (const url of ['http://github.com/o/r/pull/new/x', 'javascript:alert(1)', 'https//nope']) {
      const markup = flowHtml({ step: 'push', write: { kind: 'push', state: 'done', pushed: { state: 'pushed', branch: 'x', url } } });
      expect(markup).toContain('Pushed <code>x</code>');
      expect(markup).not.toContain('<a ');
    }
    expect(httpsUrl('https://github.com/o/r/pull/1')).toBe('https://github.com/o/r/pull/1');
    expect(httpsUrl(undefined)).toBeUndefined();
  });
});
