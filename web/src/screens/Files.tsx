import { useVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactElement, type ReactNode } from 'react';
import type { FileEntry, FileKind } from '../generated/protocol';
import type { Connection, FilesFileView, FilesView } from '../terminal/Connection';
import { breakable, StateText } from './Changes';
import { folderName, formatSize, splitPath } from './folder';

export type ChangesTab = 'changes' | 'files';

/** Typing pauses this long before a search is sent. */
export const SEARCH_DEBOUNCE_MS = 250;

/** The viewer's line height in CSS pixels; it matches the diff's. */
const LINE_PX = 19;

/** Folders first, then the rest, each by name ignoring case. */
export function sortEntries(entries: ReadonlyArray<FileEntry>): FileEntry[] {
  const key = (entry: FileEntry) => entry.name.toLowerCase();
  return [...entries].sort((a, b) => Number(b.kind === 'dir') - Number(a.kind === 'dir')
    || (key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

/** The folders from the root down to path; the root is named after its folder. */
export function crumbs(root: string | undefined, path: string): Array<{ readonly name: string; readonly path: string }> {
  const parts = path === '' ? [] : path.split('/');
  return [
    { name: root === undefined ? 'Files' : folderName(root), path: '' },
    ...parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join('/') })),
  ];
}

/** The lines of a text file, without line ends; a last line break starts no extra line. */
export function fileLines(text: string): string[] {
  const lines = text.split('\n');
  if (lines.at(-1) === '') {
    lines.pop();
  }
  return lines.map((line) => (line.endsWith('\r') ? line.slice(0, -1) : line));
}

/**
 * The Changes | Files switch at the top of the Changes screen. A switch
 * replaces the screen, so the shown tab takes the focus.
 */
export function ChangesTabs({ tab, onTab }: { readonly tab: ChangesTab; readonly onTab: (tab: ChangesTab) => void }) {
  return (
    <div className="segmented" role="group" aria-label="Show">
      {(['changes', 'files'] as const).map((name) => (
        <button
          key={name}
          type="button"
          aria-pressed={tab === name}
          autoFocus={tab === name}
          onClick={() => { if (tab !== name) onTab(name); }}
        >
          {name === 'changes' ? 'Changes' : 'Files'}
        </button>
      ))}
    </div>
  );
}

/** Sends the search once typing pauses; a cleared field ends it at once. Returns the cancel. */
export function searchSoon(files: Pick<Connection, 'getFiles' | 'searchFiles'>, query: string): () => void {
  const wanted = query.trim();
  if (wanted === (files.getFiles()?.search?.query ?? '')) {
    return () => undefined;
  }
  const timer = setTimeout(() => files.searchFiles(wanted), wanted === '' ? 0 : SEARCH_DEBOUNCE_MS);
  return () => clearTimeout(timer);
}

/** Copies text; "failed" when the clipboard is missing or refuses. */
export async function copyText(text: string, clipboard: Pick<Clipboard, 'writeText'> | undefined = globalThis.navigator?.clipboard): Promise<'copied' | 'failed'> {
  if (clipboard === undefined) {
    return 'failed';
  }
  try {
    await clipboard.writeText(text);
    return 'copied';
  } catch {
    return 'failed';
  }
}

/** The result of Copy path; a failure is read out at once. */
export function CopyNote({ copied }: { readonly copied: 'copied' | 'failed' }) {
  return copied === 'copied'
    ? <p role="status" className="muted">Path copied.</p>
    : <p role="alert" className="error">Copy did not work. Select the path above and use your phone’s Copy action.</p>;
}

/** Read-only browsing of the session's project files. */
export function Files({ connection, switcher, onAsk, onReturn }: {
  readonly connection: Connection;
  readonly switcher?: ReactNode;
  readonly onAsk: (path: string) => void;
  readonly onReturn: () => void;
}) {
  const view = useSyncExternalStore(connection.subscribe, connection.getFiles);
  const [query, setQuery] = useState(() => view?.search?.query ?? '');

  useEffect(() => searchSoon(connection, query), [connection, query]);

  return (
    <FilesPanel
      view={view}
      query={query}
      switcher={switcher}
      onQuery={setQuery}
      onFolder={(path) => connection.openFilesFolder(path)}
      onOpen={(path, line) => connection.openFilesFile(path, line)}
      onSearch={(wanted) => connection.searchFiles(wanted)}
      onBack={() => connection.closeFilesFile()}
      onAsk={onAsk}
      onReturn={onReturn}
    />
  );
}

export interface FilesHandlers {
  readonly onQuery: (query: string) => void;
  /** Lists a folder: a crumb, a folder row, Refresh or Retry. */
  readonly onFolder: (path: string) => void;
  readonly onOpen: (path: string, line?: number) => void;
  /** Sends the search again: Refresh or Retry. */
  readonly onSearch: (query: string) => void;
  readonly onBack: () => void;
  readonly onAsk: (path: string) => void;
  readonly onReturn: () => void;
}

export function FilesPanel({ view, query, switcher, ...handlers }: FilesHandlers & {
  readonly view: FilesView | undefined;
  /** The search field's text; a search shows once it is not blank. */
  readonly query: string;
  readonly switcher?: ReactNode;
}) {
  const file = view?.file;
  return (
    <>
      {/* Hidden, not unmounted, so the list keeps its scroll position. */}
      <FolderList view={view} query={query} switcher={switcher} hidden={file !== undefined} {...handlers} />
      {file !== undefined && <FileViewer file={file} {...handlers} />}
    </>
  );
}

const KIND_LABEL: Record<FileKind, string> = { dir: 'Folder', file: 'File', link: 'Link', other: 'Other' };

function FolderList({ view, query, switcher, hidden, onQuery, onFolder, onOpen, onSearch, onReturn }: FilesHandlers & {
  readonly view: FilesView | undefined;
  readonly query: string;
  readonly switcher: ReactNode;
  readonly hidden: boolean;
}) {
  const folder = view?.folder;
  const listed = folder?.state === 'ready' ? folder.listed : undefined;
  const wanted = query.trim();
  const search = wanted === '' ? undefined : view?.search;
  const found = search?.query === wanted && search.state === 'ready' ? search.found : undefined;
  const entries = wanted === '' && listed?.state === 'ready' ? sortEntries(listed.entries) : [];
  const paths = found?.state === 'ready' ? found.paths : [];
  const loading = wanted === '' ? folder?.state === 'loading' : found === undefined && search?.state !== 'unavailable';
  const path = folder?.path ?? '';
  const top = () => onFolder('');

  let state: ReactElement | undefined;
  if (wanted !== '') {
    if (search?.state === 'unavailable' || found?.state === 'unavailable') {
      state = <StateText alert text="Could not search the files." action="Retry" onAction={() => onSearch(wanted)} />;
    } else if (found === undefined) {
      state = <StateText text="Searching…" />;
    } else if (paths.length === 0) {
      state = <StateText text={`No file paths contain “${wanted}”.`} />;
    }
  } else if (view === undefined || folder?.state === 'unavailable' || listed?.state === 'unavailable') {
    state = <StateText alert text="Could not read this folder." action="Retry" onAction={() => onFolder(path)} />;
  } else if (listed === undefined) {
    state = <StateText text="Loading files…" />;
  } else if (listed.state === 'not_found' || listed.state === 'not_dir') {
    const text = listed.state === 'not_found' ? 'This folder is not there anymore.' : 'This is not a folder.';
    state = <StateText text={text} detail={path} action={`Go to ${crumbs(view.root, '')[0]?.name}`} onAction={top} />;
  } else if (entries.length === 0) {
    state = <StateText text="This folder is empty." />;
  }

  return (
    <section className="history changes" aria-labelledby="files-title" hidden={hidden}>
      <header className="history-bar changes-bar files-bar">
        <div className="changes-heading">
          <h2 id="files-title" className={switcher === undefined ? undefined : 'visually-hidden'}>Files</h2>
          {switcher}
        </div>
        <div className="changes-tools">
          <button type="button" className="link" disabled={loading} onClick={() => (wanted === '' ? onFolder(path) : onSearch(wanted))}>
            Refresh
          </button>
          <button type="button" className="link strong" onClick={onReturn}>Done</button>
        </div>
        <input
          type="search"
          className="files-search"
          aria-label="Search files by path"
          placeholder="Search files"
          maxLength={200}
          enterKeyHint="search"
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
          spellCheck={false}
          value={query}
          onChange={(event) => onQuery(event.target.value)}
        />
        {wanted === '' && (
          <nav className="crumbs" aria-label="Folder">
            <ol>
              {crumbs(view?.root, path).map((crumb, index, all) => (
                <li key={crumb.path}>
                  <button
                    type="button"
                    className="link"
                    {...(index === all.length - 1 ? { 'aria-current': 'location' as const } : {})}
                    onClick={() => onFolder(crumb.path)}
                  >
                    {crumb.name}
                  </button>
                </li>
              ))}
            </ol>
          </nav>
        )}
      </header>
      <div className="changes-scroll">
        {state}
        {entries.length > 0 && (
          <ul className="change-list" aria-label="Files in this folder">
            {entries.map((entry) => (
              <li key={entry.name}>
                <EntryRow entry={entry} path={path === '' ? entry.name : `${path}/${entry.name}`} onFolder={onFolder} onOpen={onOpen} />
              </li>
            ))}
          </ul>
        )}
        {entries.length > 0 && listed?.truncated === true && (
          <p className="changes-note" role="status">Only the first {entries.length} entries are shown.</p>
        )}
        {paths.length > 0 && (
          <ul className="change-list" aria-label="Matching files">
            {paths.map((match) => {
              const { dir, name } = splitPath(match);
              return (
                <li key={match}>
                  <button type="button" className="change-row file-row" onClick={() => onOpen(match)}>
                    <KindIcon kind="file" />
                    <span className="change-path">
                      <span className="change-dir">{breakable(dir)}</span>
                      <span className="change-name">{name}</span>
                    </span>
                    <Chevron />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {paths.length > 0 && found?.truncated === true && (
          <p className="changes-note" role="status">Only the first {paths.length} matches are shown.</p>
        )}
      </div>
    </section>
  );
}

function EntryRow({ entry, path, onFolder, onOpen }: {
  readonly entry: FileEntry;
  readonly path: string;
  readonly onFolder: (path: string) => void;
  readonly onOpen: (path: string) => void;
}) {
  const name = (
    <>
      <KindIcon kind={entry.kind} />
      <span className="change-path">
        <span className="visually-hidden">{KIND_LABEL[entry.kind]}: </span>
        <span className="change-name">{entry.name}</span>
        {entry.kind === 'link' && <span className="change-from">Link to outside the project, or broken</span>}
        {entry.kind === 'other' && <span className="change-from">Not a regular file</span>}
      </span>
    </>
  );
  if (entry.kind === 'link' || entry.kind === 'other') {
    return <div className="change-row file-row dim">{name}</div>;
  }
  return (
    <button type="button" className="change-row file-row" onClick={() => (entry.kind === 'dir' ? onFolder(path) : onOpen(path))}>
      {name}
      {entry.size !== undefined && <span className="file-size">{formatSize(entry.size)}</span>}
      <Chevron />
    </button>
  );
}

function Chevron() {
  return (
    <svg className="change-chevron" width="8" height="14" viewBox="0 0 8 14" aria-hidden="true">
      <path d="M1.5 1.5L6.5 7l-5 5.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

const KIND_PATH: Record<FileKind, string> = {
  dir: 'M1.75 4.25a1 1 0 0 1 1-1h3.5l1.5 1.75h5.5a1 1 0 0 1 1 1v6.25a1 1 0 0 1-1 1h-10.5a1 1 0 0 1-1-1z',
  file: 'M4.25 1.75h4.75l3.25 3.25v9a.5.5 0 0 1-.5.5h-7.5a.5.5 0 0 1-.5-.5v-11.75a.5.5 0 0 1 .5-.5zM9 1.75V5h3.25',
  link: 'M6.75 9.25l2.5-2.5M7.5 5.25l1-1a2.47 2.47 0 0 1 3.5 3.5l-1 1M8.5 10.75l-1 1a2.47 2.47 0 0 1-3.5-3.5l1-1',
  other: 'M8 2.25a5.75 5.75 0 1 1 0 11.5a5.75 5.75 0 0 1 0-11.5zM8 7.25v.01',
};

function KindIcon({ kind }: { readonly kind: FileKind }) {
  return (
    <svg className={`file-icon ${kind}`} width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      <path d={KIND_PATH[kind]} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function FileViewer({ file, onFolder, onOpen, onBack, onAsk }: Pick<FilesHandlers, 'onFolder' | 'onOpen' | 'onBack' | 'onAsk'> & {
  readonly file: FilesFileView;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const [copied, setCopied] = useState<'copied' | 'failed'>();
  const content = file.state === 'ready' ? file.content : undefined;
  const text = content?.state === 'ready' ? content.text ?? '' : undefined;
  const lines = useMemo(() => (text === undefined ? [] : fileLines(text)), [text]);
  // Columns of the longest line, a tab as four, so the sideways scroll width never jumps.
  const columns = useMemo(() => lines.reduce((most, line) => Math.max(most, line.length + 3 * (line.split('\t').length - 1)), 0), [lines]);
  const digits = String(lines.length).length;
  const target = file.line !== undefined && file.line <= lines.length ? file.line : undefined;
  // ponytail: a fixed row height, since lines never wrap; long lines scroll sideways.
  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => LINE_PX,
    overscan: 30,
    initialRect: { width: 0, height: 600 },
  });

  useEffect(() => {
    if (target !== undefined) {
      virtualizer.scrollToIndex(target - 1, { align: 'center' });
    }
    // Once per reply: later scrolling is the user's.
  }, [lines, target]);

  async function copy() {
    setCopied(await copyText(file.path));
  }

  const { name } = splitPath(file.path);
  let state: ReactElement | undefined;
  if (file.state === 'unavailable' || content?.state === 'unavailable') {
    state = <StateText alert text="Could not read this file." action="Retry" onAction={() => onOpen(file.path, file.line)} />;
  } else if (content === undefined) {
    state = <StateText text="Loading file…" />;
  } else if (content.state === 'binary') {
    state = <StateText text="This file is not text." />;
  } else if (content.state === 'too_large') {
    state = <StateText text="This file is over 50 MiB, too large to show." />;
  } else if (content.state === 'not_found') {
    const folder = file.path.slice(0, Math.max(0, file.path.lastIndexOf('/')));
    state = <StateText text="This file is not there anymore." action="Show its folder" onAction={() => onFolder(folder)} />;
  } else if (content.state === 'not_file') {
    state = <StateText text="This is not a regular file." />;
  } else if (lines.length === 0) {
    state = <StateText text="This file is empty." />;
  }

  return (
    <section className="history changes" aria-labelledby="files-file-title">
      <header className="history-bar changes-file-bar">
        {/* A new file takes the focus, so a screen reader starts at its header. */}
        <button type="button" className="link changes-back" aria-label="Back to files" autoFocus onClick={onBack}>
          <svg width="10" height="16" viewBox="0 0 10 16" aria-hidden="true">
            <path d="M8 2L2 8l6 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
        <div className="changes-heading">
          <h2 id="files-file-title" className="changes-file-name">{name}</h2>
          <p className="subtitle changes-subtitle">
            <span className="file-path">{file.path}</span>
            {content?.size !== undefined && ` · ${formatSize(content.size)}`}
          </p>
        </div>
        <button type="button" className="link file-copy" onClick={() => { void copy(); }}>Copy path</button>
      </header>
      <div
        ref={scroller}
        className="changes-scroll file-text"
        tabIndex={0}
        aria-label={`Text of ${name}`}
        style={{ '--digits': digits } as CSSProperties}
      >
        {state}
        {lines.length > 0 && (
          <div className="file-lines" style={{ height: virtualizer.getTotalSize(), width: `calc(${digits + columns}ch + 32px)` }}>
            {virtualizer.getVirtualItems().map((item) => (
              <div
                key={item.key}
                className={item.index + 1 === target ? 'file-line target' : 'file-line'}
                style={{ transform: `translateY(${item.start}px)` }}
              >
                <span className="file-num" aria-hidden="true">{item.index + 1}</span>
                <span className="file-code">{lines[item.index] || ' '}</span>
              </div>
            ))}
          </div>
        )}
        {content?.truncated === true && (
          <p className="changes-note" role="status">The file is cut short here (256 KiB). See the rest on your computer.</p>
        )}
      </div>
      <div className="history-actions">
        {copied !== undefined && <CopyNote copied={copied} />}
        <button type="button" className="primary wide" onClick={() => onAsk(file.path)}>
          <span aria-hidden="true">@</span>
          Ask agent about this file
        </button>
      </div>
    </section>
  );
}
