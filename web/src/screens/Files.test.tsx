import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { FilesContentPayload, FilesFoundPayload, FilesListedPayload, FileEntry } from '../generated/protocol';
import type { FilesFileView, FilesView } from '../terminal/Connection';
import { ChangesTabs, copyText, CopyNote, crumbs, fileLines, FilesPanel, SEARCH_DEBOUNCE_MS, searchSoon, sortEntries } from './Files';
import { formatSize } from './folder';

const noop = () => undefined;
const root = '/Users/me/app';

const dir = (name: string): FileEntry => ({ name, kind: 'dir' });
const file = (name: string, size = 10): FileEntry => ({ name, kind: 'file', size });
const entries: FileEntry[] = [
  file('README.md', 1536),
  dir('src'),
  file('Zed.txt', 0),
  { name: 'out', kind: 'link' },
  dir('docs'),
  { name: 'fifo', kind: 'other' },
];

const listed = (extra: Partial<FilesListedPayload> = {}): FilesListedPayload => ({
  state: 'ready', root, path: '', entries, truncated: false, ...extra,
});
const found = (extra: Partial<FilesFoundPayload> = {}): FilesFoundPayload => ({
  state: 'ready', query: 'q', paths: ['web/src/q.ts', 'quick.md'], truncated: false, ...extra,
});
const content = (extra: Partial<FilesContentPayload> = {}): FilesContentPayload => ({
  path: 'src/a.ts', state: 'ready', size: 20, text: 'const a = 1;\nconst b = 2;\n', truncated: false, ...extra,
});

function view(extra: Partial<FilesView> = {}): FilesView {
  return { root, folder: { path: '', state: 'ready', listed: listed() }, file: undefined, search: undefined, ...extra };
}
const opened = (extra: Partial<FilesFileView> = {}): FilesFileView => ({
  path: 'src/a.ts', line: undefined, state: 'ready', content: content(), ...extra,
});

function panel(files: FilesView | undefined, query = '', handlers: Partial<Parameters<typeof FilesPanel>[0]> = {}) {
  return (
    <FilesPanel
      view={files}
      query={query}
      onQuery={noop}
      onFolder={noop}
      onOpen={noop}
      onSearch={noop}
      onBack={noop}
      onAsk={noop}
      onReturn={noop}
      {...handlers}
    />
  );
}
const html = (files: FilesView | undefined, query = '') => renderToStaticMarkup(panel(files, query));

/** Host buttons by visible text; FolderList and EntryRow have no hooks, so they are called directly. */
function buttons(node: ReactNode, found = new Map<string, () => void>()): Map<string, () => void> {
  if (Array.isArray(node)) {
    node.forEach((child: ReactNode) => buttons(child, found));
  } else if (isValidElement(node)) {
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void }>;
    if (typeof node.type === 'function') {
      buttons((node.type as (props: unknown) => ReactNode)(props), found);
    } else if (node.type === 'button' && props.onClick !== undefined) {
      found.set(renderToStaticMarkup(<>{props.children}</>).replace(/<[^>]+>/gu, ''), props.onClick);
    }
    buttons(props.children, found);
  }
  return found;
}

describe('formatSize', () => {
  it('uses 1024-based units with one decimal below 10', () => {
    expect(formatSize(0)).toBe('0 B');
    expect(formatSize(900)).toBe('900 B');
    expect(formatSize(1023)).toBe('1023 B');
    expect(formatSize(1024)).toBe('1 KB');
    expect(formatSize(1536)).toBe('1.5 KB');
    expect(formatSize(12 * 1024)).toBe('12 KB');
    expect(formatSize(3.4 * 1024 * 1024)).toBe('3.4 MB');
    expect(formatSize(5 * 1024 ** 3)).toBe('5 GB');
    expect(formatSize(1024 ** 3)).toBe('1 GB');
  });
});

describe('sortEntries', () => {
  it('puts folders first, then sorts by name ignoring case', () => {
    expect(sortEntries([file('b.txt'), dir('Zoo'), file('A.txt'), dir('apple'), { name: 'x', kind: 'link' }]).map((entry) => entry.name))
      .toEqual(['apple', 'Zoo', 'A.txt', 'b.txt', 'x']);
  });

  it('breaks a tie of the same name in another case by the exact name, whatever the input order', () => {
    expect(sortEntries([file('a'), file('A')]).map((entry) => entry.name)).toEqual(['A', 'a']);
    expect(sortEntries([file('A'), file('a')]).map((entry) => entry.name)).toEqual(['A', 'a']);
  });

  it('does not change its input', () => {
    const input = [file('b'), dir('a')];
    sortEntries(input);
    expect(input.map((entry) => entry.name)).toEqual(['b', 'a']);
  });
});

describe('crumbs', () => {
  it('names the root "Files" when it is not known', () => {
    expect(crumbs(undefined, '')).toEqual([{ name: 'Files', path: '' }]);
  });

  it('names the root after its folder, POSIX or Windows', () => {
    expect(crumbs(root, '')).toEqual([{ name: 'app', path: '' }]);
    expect(crumbs('C:/Users/me/app', '')).toEqual([{ name: 'app', path: '' }]);
  });

  it('adds one crumb per folder of a nested path', () => {
    expect(crumbs(root, 'web/src/screens')).toEqual([
      { name: 'app', path: '' },
      { name: 'web', path: 'web' },
      { name: 'src', path: 'web/src' },
      { name: 'screens', path: 'web/src/screens' },
    ]);
  });
});

describe('fileLines', () => {
  it('splits lines and ends no extra line after a last line break', () => {
    expect(fileLines('a\nb\n')).toEqual(['a', 'b']);
    expect(fileLines('a\nb')).toEqual(['a', 'b']);
  });

  it('removes a carriage return before each line break', () => {
    expect(fileLines('a\r\nb\r\n')).toEqual(['a', 'b']);
  });

  it('keeps blank lines and gives none for empty text', () => {
    expect(fileLines('a\n\nb\n')).toEqual(['a', '', 'b']);
    expect(fileLines('')).toEqual([]);
    expect(fileLines('\n')).toEqual(['']);
  });
});

describe('ChangesTabs', () => {
  it('marks the active tab only', () => {
    const changes = renderToStaticMarkup(<ChangesTabs tab="changes" onTab={noop} />);
    expect(changes).toContain('role="group" aria-label="Show"');
    expect(changes).toContain('<button type="button" aria-pressed="true" autofocus="">Changes</button><button type="button" aria-pressed="false">Files</button>');
    const files = renderToStaticMarkup(<ChangesTabs tab="files" onTab={noop} />);
    expect(files).toContain('<button type="button" aria-pressed="false">Changes</button><button type="button" aria-pressed="true" autofocus="">Files</button>');
  });

  it('switches only to the other tab', () => {
    const onTab = vi.fn();
    const found = buttons(<ChangesTabs tab="changes" onTab={onTab} />);
    found.get('Changes')?.();
    expect(onTab).not.toHaveBeenCalled();
    found.get('Files')?.();
    expect(onTab).toHaveBeenCalledWith('files');
  });
});

describe('files folder list', () => {
  it('says each folder state in plain words', () => {
    const loading = html(view({ folder: { path: '', state: 'loading', listed: undefined } }));
    expect(loading).toContain('<p role="status">Loading files…</p>');
    expect(loading).not.toContain('change-list');

    for (const unavailable of [
      undefined,
      view({ folder: { path: '', state: 'unavailable', listed: undefined } }),
      view({ folder: { path: '', state: 'ready', listed: listed({ state: 'unavailable', entries: [] }) } }),
    ]) {
      const markup = html(unavailable);
      expect(markup).toContain('<p role="alert">Could not read this folder.</p>');
      expect(markup).toContain('<button type="button">Retry</button>');
    }

    const gone = html(view({ folder: { path: 'old', state: 'ready', listed: listed({ state: 'not_found', path: 'old', entries: [] }) } }));
    expect(gone).toContain('<p role="status">This folder is not there anymore.</p>');
    expect(gone).toContain('<p class="changes-detail">old</p>');
    expect(gone).toContain('<button type="button">Go to app</button>');

    const notDir = html(view({ folder: { path: 'a.txt', state: 'ready', listed: listed({ state: 'not_dir', path: 'a.txt', entries: [] }) } }));
    expect(notDir).toContain('<p role="status">This is not a folder.</p>');
    expect(notDir).toContain('<button type="button">Go to app</button>');

    const empty = html(view({ folder: { path: '', state: 'ready', listed: listed({ entries: [] }) } }));
    expect(empty).toContain('<p role="status">This folder is empty.</p>');
    expect(empty).not.toContain('Only the first');
  });

  it('retries the listing of the same folder', () => {
    const onFolder = vi.fn();
    buttons(panel(view({ folder: { path: 'web', state: 'unavailable', listed: undefined } }), '', { onFolder })).get('Retry')?.();
    expect(onFolder).toHaveBeenCalledWith('web');

    onFolder.mockClear();
    buttons(panel(undefined, '', { onFolder })).get('Retry')?.();
    expect(onFolder).toHaveBeenCalledWith('');

    onFolder.mockClear();
    const gone = view({ folder: { path: 'old', state: 'ready', listed: listed({ state: 'not_found', path: 'old', entries: [] }) } });
    buttons(panel(gone, '', { onFolder })).get('Go to app')?.();
    expect(onFolder).toHaveBeenCalledWith('');
  });

  it('lists folders first, with sizes, and no state text', () => {
    const markup = html(view());
    const names = [...markup.matchAll(/<span class="change-name">([^<]+)<\/span>/gu)].map((match) => match[1]);
    expect(names).toEqual(['docs', 'src', 'fifo', 'out', 'README.md', 'Zed.txt']);
    expect(markup).toContain('<ul class="change-list" aria-label="Files in this folder">');
    expect(markup).toContain('<span class="visually-hidden">Folder: </span>');
    expect(markup).toContain('<span class="file-size">1.5 KB</span>');
    expect(markup).toContain('<span class="file-size">0 B</span>');
    expect(markup).not.toContain('changes-state');
    expect(markup).not.toContain('Only the first');
  });

  it('shows a link or other entry dimmed, as no button, with its note', () => {
    const markup = html(view());
    expect(markup).toContain('<div class="change-row file-row dim">');
    expect(markup).toContain('<span class="change-from">Link to outside the project, or broken</span>');
    expect(markup).toContain('<span class="change-from">Not a regular file</span>');
    expect(markup.match(/class="change-row file-row dim"/gu)).toHaveLength(2);
    // Four rows are buttons: two folders and two files.
    expect(markup.match(/<button type="button" class="change-row file-row">/gu)).toHaveLength(4);
    const found = buttons(panel(view()));
    expect([...found.keys()].some((name) => name.includes('out'))).toBe(false);
  });

  it('opens a folder or a file by its path from the root', () => {
    const onFolder = vi.fn();
    const onOpen = vi.fn();
    const found = buttons(panel(view({ folder: { path: 'web', state: 'ready', listed: listed({ path: 'web' }) } }), '', { onFolder, onOpen }));
    found.get('Folder: src')?.();
    expect(onFolder).toHaveBeenCalledWith('web/src');
    found.get('File: README.md1.5 KB')?.();
    expect(onOpen).toHaveBeenCalledWith('web/README.md');

    onOpen.mockClear();
    buttons(panel(view(), '', { onOpen })).get('File: README.md1.5 KB')?.();
    expect(onOpen).toHaveBeenCalledWith('README.md');
  });

  it('says when the listing was cut', () => {
    const markup = html(view({ folder: { path: '', state: 'ready', listed: listed({ entries: [dir('a'), file('b')], truncated: true }) } }));
    expect(markup).toContain('<p class="changes-note" role="status">Only the first 2 entries are shown.</p>');
  });

  it('shows breadcrumbs with the last one as the current location', () => {
    const markup = html(view({ folder: { path: 'web/src', state: 'ready', listed: listed({ path: 'web/src' }) } }));
    expect(markup).toContain('<nav class="crumbs" aria-label="Folder">');
    expect(markup).toContain('<button type="button" class="link">app</button>');
    expect(markup).toContain('<button type="button" class="link">web</button>');
    expect(markup).toContain('<button type="button" class="link" aria-current="location">src</button>');
    expect(markup.match(/aria-current/gu)).toHaveLength(1);

    const onFolder = vi.fn();
    const found = buttons(panel(view({ folder: { path: 'web/src', state: 'ready', listed: listed({ path: 'web/src' }) } }), '', { onFolder }));
    found.get('web')?.();
    expect(onFolder).toHaveBeenCalledWith('web');
    found.get('app')?.();
    expect(onFolder).toHaveBeenCalledWith('');
  });

  it('names the root "Files" in the crumbs until a listing names it', () => {
    const markup = html(view({ root: undefined, folder: { path: '', state: 'loading', listed: undefined } }));
    expect(markup).toContain('<button type="button" class="link" aria-current="location">Files</button>');
  });

  it('has the title, search field, Refresh and Done', () => {
    const markup = html(view());
    expect(markup).toContain('<h2 id="files-title">Files</h2>');
    expect(markup).toContain('aria-label="Search files by path"');
    expect(markup).toContain('placeholder="Search files"');

    const onFolder = vi.fn();
    const onReturn = vi.fn();
    const found = buttons(panel(view({ folder: { path: 'web', state: 'ready', listed: listed({ path: 'web' }) } }), '', { onFolder, onReturn }));
    found.get('Refresh')?.();
    found.get('Done')?.();
    expect(onFolder).toHaveBeenCalledWith('web');
    expect(onReturn).toHaveBeenCalledOnce();
  });

  it('disables Refresh while loading', () => {
    expect(html(view({ folder: { path: '', state: 'loading', listed: undefined } }))).toContain('<button type="button" class="link" disabled="">Refresh</button>');
    expect(html(view())).toContain('<button type="button" class="link">Refresh</button>');
  });

  it('shows the switcher in place of the visible title', () => {
    const markup = renderToStaticMarkup(panel(view(), '', { switcher: <nav id="switch">switch</nav> }));
    expect(markup).toContain('<h2 id="files-title" class="visually-hidden">Files</h2><nav id="switch">switch</nav>');
  });

  it('hides the folder list while a file is open, and shows the viewer', () => {
    const markup = html(view({ file: opened() }));
    expect(markup).toContain('<section class="history changes" aria-labelledby="files-title" hidden="">');
    expect(markup).toContain('aria-labelledby="files-file-title"');
    expect(html(view())).not.toContain('hidden=""');
  });
});

describe('files search', () => {
  const searching = (search: FilesView['search']) => view({ search });

  it('says "Searching…" until a reply for this query arrives', () => {
    expect(html(view(), 'q')).toContain('<p role="status">Searching…</p>');
    expect(html(searching({ query: 'q', state: 'loading', found: undefined }), 'q')).toContain('Searching…');
    // A reply for an older query does not count.
    expect(html(searching({ query: 'qu', state: 'ready', found: found({ query: 'qu' }) }), 'q')).toContain('Searching…');
    expect(html(searching({ query: 'qu', state: 'ready', found: found({ query: 'qu' }) }), 'q')).not.toContain('Matching files');
  });

  it('does not show the folder entries while searching', () => {
    const markup = html(view(), 'q');
    expect(markup).not.toContain('Files in this folder');
    expect(markup).not.toContain('README.md');
  });

  it('hides the crumbs while searching and disables Refresh while it loads', () => {
    const markup = html(searching({ query: 'q', state: 'loading', found: undefined }), 'q');
    expect(markup).not.toContain('class="crumbs"');
    expect(markup).not.toContain('aria-current');
    expect(markup).toContain('<button type="button" class="link" disabled="">Refresh</button>');
    expect(html(searching({ query: 'q', state: 'ready', found: found() }), 'q')).toContain('<button type="button" class="link">Refresh</button>');
  });

  it('treats a blank query as no search', () => {
    const markup = html(searching({ query: 'q', state: 'ready', found: found() }), '   ');
    expect(markup).toContain('Files in this folder');
    expect(markup).toContain('class="crumbs"');
  });

  it('says when the search failed and sends it again on Retry', () => {
    const failed = searching({ query: 'q', state: 'unavailable', found: undefined });
    const markup = html(failed, 'q');
    expect(markup).toContain('<p role="alert">Could not search the files.</p>');
    expect(markup).toContain('<button type="button">Retry</button>');

    const onSearch = vi.fn();
    buttons(panel(failed, ' q ', { onSearch })).get('Retry')?.();
    expect(onSearch).toHaveBeenCalledWith('q');

    const reply = searching({ query: 'q', state: 'ready', found: found({ state: 'unavailable', paths: [] }) });
    expect(html(reply, 'q')).toContain('<p role="alert">Could not search the files.</p>');
  });

  it('says when no path matches', () => {
    const markup = html(searching({ query: 'q', state: 'ready', found: found({ paths: [] }) }), 'q');
    expect(markup).toContain('<p role="status">No file paths contain “q”.</p>');
    expect(markup).not.toContain('Matching files');
  });

  it('lists the matching paths and opens one', () => {
    const markup = html(searching({ query: 'q', state: 'ready', found: found() }), 'q');
    expect(markup).toContain('<ul class="change-list" aria-label="Matching files">');
    expect(markup).toContain('<span class="change-dir">web/<wbr/>src/<wbr/></span><span class="change-name">q.ts</span>');
    expect(markup).toContain('<span class="change-dir"></span><span class="change-name">quick.md</span>');
    expect(markup).not.toContain('Only the first');
    expect(markup).not.toContain('changes-state');
  });

  it('opens a matching path as a file from the root', () => {
    const onOpen = vi.fn();
    const rows = buttons(panel(searching({ query: 'q', state: 'ready', found: found() }), 'q', { onOpen }));
    rows.get('quick.md')?.();
    expect(onOpen).toHaveBeenCalledWith('quick.md');
    rows.get('web/src/q.ts')?.();
    expect(onOpen).toHaveBeenCalledWith('web/src/q.ts');
  });

  it('says when the matches were cut', () => {
    const markup = html(searching({ query: 'q', state: 'ready', found: found({ truncated: true }) }), 'q');
    expect(markup).toContain('<p class="changes-note" role="status">Only the first 2 matches are shown.</p>');
  });

  it('searches again on Refresh while searching', () => {
    const onSearch = vi.fn();
    buttons(panel(searching({ query: 'q', state: 'ready', found: found() }), 'q', { onSearch })).get('Refresh')?.();
    expect(onSearch).toHaveBeenCalledWith('q');
  });
});

describe('file viewer', () => {
  const viewer = (extra: Partial<FilesFileView> = {}) => html(view({ file: opened(extra) }));
  const bare = (state: FilesContentPayload['state']): FilesContentPayload => ({ path: 'src/a.ts', state, truncated: false });
  const withContent = (extra: Partial<FilesContentPayload>) => viewer({ content: content(extra) });

  it('says each state in plain words', () => {
    expect(viewer({ state: 'loading', content: undefined })).toContain('<p role="status">Loading file…</p>');

    for (const unavailable of [
      viewer({ state: 'unavailable', content: undefined }),
      viewer({ content: bare('unavailable') }),
    ]) {
      expect(unavailable).toContain('<p role="alert">Could not read this file.</p>');
      expect(unavailable).toContain('<button type="button">Retry</button>');
    }

    expect(viewer({ content: bare('binary') })).toContain('<p role="status">This file is not text.</p>');
    expect(viewer({ content: bare('too_large') })).toContain('<p role="status">This file is over 50 MiB, too large to show.</p>');
    const gone = viewer({ content: bare('not_found') });
    expect(gone).toContain('<p role="status">This file is not there anymore.</p>');
    expect(gone).toContain('<button type="button">Show its folder</button>');
    expect(viewer({ content: bare('not_file') })).toContain('<p role="status">This is not a regular file.</p>');
    expect(withContent({ text: '' })).toContain('<p role="status">This file is empty.</p>');
    expect(viewer({ content: bare('ready') })).toContain('<p role="status">This file is empty.</p>');
  });

  it('shows no lines for a file that is not ready', () => {
    expect(viewer({ content: bare('binary') })).not.toContain('file-lines');
    expect(viewer({ state: 'loading', content: undefined })).not.toContain('file-lines');
  });

  it('shows line numbers and text', () => {
    const markup = viewer();
    expect(markup).toContain('<span class="file-num" aria-hidden="true">1</span><span class="file-code">const a = 1;</span>');
    expect(markup).toContain('<span class="file-num" aria-hidden="true">2</span><span class="file-code">const b = 2;</span>');
    expect(markup).not.toContain('<span class="file-num" aria-hidden="true">3</span>');
    expect(markup).not.toContain('changes-state');
    expect(markup).not.toContain('The file is cut short');
    expect(markup.match(/class="file-line"/gu)).toHaveLength(2);
    expect(markup).toContain('--digits:1');
  });

  it('shows a blank line as a space and sizes the number column by the line count', () => {
    const markup = withContent({ text: `${'x\n'.repeat(11)}\n${'y\n'.repeat(3)}` });
    expect(markup).toContain('<span class="file-code"> </span>');
    expect(markup).toContain('--digits:2');
  });

  it('marks the target line', () => {
    const markup = viewer({ line: 2 });
    expect(markup).toContain('<div class="file-line target"');
    expect(markup.match(/file-line target/gu)).toHaveLength(1);
    expect(markup).toMatch(/file-line target"[^>]*><span class="file-num" aria-hidden="true">2<\/span>/u);
    expect(markup).toMatch(/class="file-line"[^>]*><span class="file-num" aria-hidden="true">1<\/span>/u);
  });

  it('marks no target for a line past the end', () => {
    expect(viewer({ line: 3 })).not.toContain('target');
  });

  it('says when the text was cut short', () => {
    expect(withContent({ truncated: true })).toContain('<p class="changes-note" role="status">The file is cut short here (256 KiB). See the rest on your computer.</p>');
  });

  it('shows the name, the path and the size in the header', () => {
    const markup = viewer({ path: 'web/src/a.ts' });
    expect(markup).toContain('<h2 id="files-file-title" class="changes-file-name">a.ts</h2>');
    expect(markup).toContain('<span class="file-path">web/src/a.ts</span> · 20 B');
    expect(markup).toContain('aria-label="Text of a.ts"');
    expect(withContent({ size: 2048 })).toContain(' · 2 KB');
    expect(viewer({ state: 'loading', content: undefined })).not.toContain(' · ');
  });

  it('has Back, Ask agent and Copy path', () => {
    const markup = viewer();
    expect(markup).toContain('aria-label="Back to files"');
    expect(markup).toContain('Ask agent about this file</button>');
    expect(markup).toContain('<button type="button" class="link file-copy">Copy path</button>');
    expect(markup).not.toContain('Path copied.');
  });
});

describe('searchSoon', () => {
  const target = (query?: string) => {
    const searchFiles = vi.fn();
    const view = query === undefined ? undefined : { search: { query } } as unknown as FilesView;
    return { searchFiles, getFiles: () => view };
  };

  it('sends the search once typing pauses, and a newer query cancels the older one', () => {
    vi.useFakeTimers();
    try {
      const files = target();
      const cancel = searchSoon(files, 'ap');
      vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS - 1);
      cancel();
      searchSoon(files, ' app ');
      vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS - 1);
      expect(files.searchFiles).not.toHaveBeenCalled();
      vi.advanceTimersByTime(1);
      expect(files.searchFiles.mock.calls).toEqual([['app']]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('ends a search at once when the field is cleared, and sends nothing for the shown query', () => {
    vi.useFakeTimers();
    try {
      const shown = target('app');
      searchSoon(shown, 'app ');
      searchSoon(shown, '');
      vi.advanceTimersByTime(0);
      expect(shown.searchFiles.mock.calls).toEqual([['']]);
      const idle = target();
      searchSoon(idle, '   ');
      vi.runAllTimers();
      expect(idle.searchFiles).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('Copy path', () => {
  it('copies, and fails when the clipboard refuses or is missing', async () => {
    const writeText = vi.fn(async () => undefined);
    await expect(copyText('src/a.ts', { writeText })).resolves.toBe('copied');
    expect(writeText).toHaveBeenCalledWith('src/a.ts');
    await expect(copyText('src/a.ts', { writeText: async () => { throw new Error('denied'); } })).resolves.toBe('failed');
    await expect(copyText('src/a.ts', undefined)).resolves.toBe('failed');
  });

  it('reads out a failure as an alert', () => {
    expect(renderToStaticMarkup(<CopyNote copied="copied" />)).toBe('<p role="status" class="muted">Path copied.</p>');
    expect(renderToStaticMarkup(<CopyNote copied="failed" />))
      .toBe('<p role="alert" class="error">Copy did not work. Select the path above and use your phone’s Copy action.</p>');
  });
});
