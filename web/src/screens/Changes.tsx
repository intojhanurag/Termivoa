import { Fragment, useSyncExternalStore, type CSSProperties, type ReactElement } from 'react';
import type { ChangedFile, ChangeStatus, DiffLine } from '../generated/protocol';
import type { ChangesFileView, ChangesView, Connection } from '../terminal/Connection';
import { readDraft, saveDraft } from '../terminal/composer';
import { displayPath } from './folder';

const STATUS: Record<ChangeStatus, { readonly letter: string; readonly name: string }> = {
  modified: { letter: 'M', name: 'Modified' },
  added: { letter: 'A', name: 'Added' },
  deleted: { letter: 'D', name: 'Deleted' },
  renamed: { letter: 'R', name: 'Renamed' },
  untracked: { letter: 'U', name: 'Untracked' },
  typechange: { letter: 'T', name: 'Type changed' },
};

const SIGN: Record<DiffLine['kind'], string> = { hunk: '', context: ' ', add: '+', remove: '-' };

/** Adds "@path " to the session's composer draft. It never sends anything. */
export function askAboutFile(key: string, path: string): void {
  const draft = readDraft(key);
  const space = draft === '' || /\s$/u.test(draft) ? '' : ' ';
  saveDraft(key, `${draft}${space}@${path} `);
}

/** "src/app/" and "main.ts": the directory is shown muted, the name strong. */
export function splitPath(path: string): { readonly dir: string; readonly name: string } {
  const slash = path.lastIndexOf('/');
  return { dir: path.slice(0, slash + 1), name: path.slice(slash + 1) };
}

/** Read-only view of the uncommitted git changes in the session's project. */
export function Changes({ connection, cwd, onAsk, onReturn }: {
  readonly connection: Connection;
  /** The session's folder, shown when it is not in a repository. */
  readonly cwd: string | undefined;
  readonly onAsk: (path: string) => void;
  readonly onReturn: () => void;
}) {
  const view = useSyncExternalStore(connection.subscribe, connection.getChanges);
  return (
    <ChangesPanel
      view={view}
      cwd={cwd}
      onRefresh={() => connection.refreshChanges()}
      onOpen={(path) => connection.openChangesFile(path)}
      onBack={() => connection.closeChangesFile()}
      onAsk={onAsk}
      onReturn={onReturn}
    />
  );
}

export function ChangesPanel({ view, cwd, onRefresh, onOpen, onBack, onAsk, onReturn }: {
  readonly view: ChangesView | undefined;
  readonly cwd: string | undefined;
  readonly onRefresh: () => void;
  readonly onOpen: (path: string) => void;
  readonly onBack: () => void;
  readonly onAsk: (path: string) => void;
  readonly onReturn: () => void;
}) {
  const file = view?.file;
  return (
    <>
      {/* Hidden, not unmounted, so the list keeps its scroll position. */}
      <ChangeList view={view} cwd={cwd} hidden={file !== undefined} onRefresh={onRefresh} onOpen={onOpen} onReturn={onReturn} />
      {file !== undefined && (
        <FileDiff
          file={file}
          entry={view?.summary?.files.find((item) => item.path === file.path)}
          onRefresh={onRefresh}
          onBack={onBack}
          onAsk={onAsk}
        />
      )}
    </>
  );
}

function ChangeList({ view, cwd, hidden, onRefresh, onOpen, onReturn }: {
  readonly view: ChangesView | undefined;
  readonly cwd: string | undefined;
  readonly hidden: boolean;
  readonly onRefresh: () => void;
  readonly onOpen: (path: string) => void;
  readonly onReturn: () => void;
}) {
  const summary = view?.summary;
  const loading = view === undefined || view.state === 'loading';
  const failed = view?.state === 'unavailable' || summary?.state === 'unavailable';
  const files = !failed && summary?.state === 'ready' ? summary.files : [];
  const additions = files.reduce((sum, item) => sum + item.additions, 0);
  const deletions = files.reduce((sum, item) => sum + item.deletions, 0);
  const folder = summary?.root ?? cwd;
  const subtitle = summary?.state === 'ready' && !failed
    ? [
      folder === undefined ? undefined : displayPath(folder),
      summary.branch,
      `${files.length} ${files.length === 1 ? 'file' : 'files'}`,
      `+${additions} −${deletions}`,
    ].filter((part) => part !== undefined).join(' · ')
    : undefined;

  let state: ReactElement | undefined;
  if (failed) {
    state = <StateText text="Could not read changes." action="Retry" onAction={onRefresh} />;
  } else if (summary === undefined) {
    state = <StateText text="Loading changes…" />;
  } else if (summary.state === 'not_git') {
    state = <StateText text="This folder is not a git repository." detail={cwd === undefined ? undefined : displayPath(cwd)} />;
  } else if (files.length === 0) {
    state = <StateText text="No changes. The working tree is clean." />;
  }

  return (
    <section className="history changes" aria-labelledby="changes-title" hidden={hidden}>
      <header className="history-bar changes-bar">
        <div className="changes-heading">
          <h2 id="changes-title">Changes</h2>
          {subtitle !== undefined && <p className="subtitle changes-subtitle">{subtitle}</p>}
        </div>
        <div className="changes-tools">
          <button type="button" className="link" disabled={loading} onClick={onRefresh}>
            {loading && summary !== undefined ? 'Refreshing…' : 'Refresh'}
          </button>
          <button type="button" className="link strong" onClick={onReturn}>Done</button>
        </div>
      </header>
      <div className="changes-scroll">
        {state}
        {files.length > 0 && (
          <ul className="change-list" aria-label="Changed files">
            {files.map((item) => (
              <li key={item.path}>
                <ChangeRow file={item} onOpen={onOpen} />
              </li>
            ))}
          </ul>
        )}
        {files.length > 0 && summary?.truncated === true && (
          <p className="changes-note" role="status">Only the first {files.length} files are shown.</p>
        )}
      </div>
    </section>
  );
}

function ChangeRow({ file, onOpen }: { readonly file: ChangedFile; readonly onOpen: (path: string) => void }) {
  const { dir, name } = splitPath(file.path);
  const status = STATUS[file.status];
  return (
    <button type="button" className="change-row" onClick={() => onOpen(file.path)}>
      <span className={`change-status ${file.status}`} title={status.name}>
        <span aria-hidden="true">{status.letter}</span>
        <span className="visually-hidden">{status.name}: </span>
      </span>
      <span className="change-path">
        <span className="change-dir">{breakable(dir)}</span>
        <span className="change-name">{name}</span>
        {file.oldPath !== undefined && <span className="change-from">from {file.oldPath}</span>}
      </span>
      {file.binary ? <span className="change-binary">binary</span> : <Counts file={file} />}
      <svg className="change-chevron" width="8" height="14" viewBox="0 0 8 14" aria-hidden="true">
        <path d="M1.5 1.5L6.5 7l-5 5.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </button>
  );
}

/** Long directories wrap after a slash, not inside a folder name. */
function breakable(dir: string) {
  return dir.split('/').slice(0, -1).map((part, index) => <Fragment key={index}>{part}/<wbr /></Fragment>);
}

function Counts({ file }: { readonly file: ChangedFile }) {
  return (
    <>
      <span className={file.additions === 0 ? 'change-add zero' : 'change-add'}>+{file.additions}</span>
      <span className={file.deletions === 0 ? 'change-del zero' : 'change-del'}>−{file.deletions}</span>
    </>
  );
}

function FileDiff({ file, entry, onRefresh, onBack, onAsk }: {
  readonly file: ChangesFileView;
  /** The file's row in the current summary, for its counts. */
  readonly entry: ChangedFile | undefined;
  readonly onRefresh: () => void;
  readonly onBack: () => void;
  readonly onAsk: (path: string) => void;
}) {
  const { dir, name } = splitPath(file.path);
  const patch = file.patch;
  const lines = patch?.state === 'ready' ? patch.lines : [];
  // Gutter width in digits, from the largest line number.
  const digits = String(lines.reduce((most, line) => Math.max(most, line.oldLine ?? 0, line.newLine ?? 0), 0)).length;

  let state: ReactElement | undefined;
  if (file.state === 'unavailable' || patch?.state === 'unavailable') {
    state = <StateText text="Could not read this file." action="Retry" onAction={onRefresh} />;
  } else if (patch === undefined) {
    state = <StateText text="Loading diff…" />;
  } else if (patch.state === 'binary') {
    state = <StateText text="Binary file — not shown." />;
  } else if (patch.state === 'missing') {
    state = <StateText text="This file has no changes anymore." action="Refresh" onAction={onRefresh} />;
  } else if (lines.length === 0) {
    state = <StateText text="No line changes in this file." />;
  }

  return (
    <section className="history changes" aria-labelledby="changes-file-title">
      <header className="history-bar changes-file-bar">
        <button type="button" className="link changes-back" aria-label="Back to changed files" onClick={onBack}>
          <svg width="10" height="16" viewBox="0 0 10 16" aria-hidden="true">
            <path d="M8 2L2 8l6 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
        <div className="changes-heading">
          <h2 id="changes-file-title" className="changes-file-name">{name}</h2>
          <p className="subtitle changes-subtitle">
            {dir !== '' && <span>{dir} · </span>}
            {entry !== undefined && !entry.binary && <Counts file={entry} />}
          </p>
        </div>
      </header>
      <div className="changes-scroll diff" style={{ '--digits': digits } as CSSProperties}>
        {state}
        {lines.map((line, index) => (
          <div key={index} className={`diff-line ${line.kind}`}>
            {line.kind === 'hunk' ? (
              <span className="diff-text">{line.text}</span>
            ) : (
              <>
                <span className="diff-num" aria-hidden="true">{line.oldLine}</span>
                <span className="diff-num" aria-hidden="true">{line.newLine}</span>
                <span className="diff-sign">{SIGN[line.kind]}</span>
                <span className="diff-text">{line.text}</span>
              </>
            )}
          </div>
        ))}
        {patch?.truncated === true && (
          <p className="changes-note" role="status">The diff is cut short here. See the rest on your computer.</p>
        )}
      </div>
      <div className="history-actions">
        <button type="button" className="primary wide" onClick={() => onAsk(file.path)}>
          <span aria-hidden="true">@</span>
          Ask agent about this file
        </button>
      </div>
    </section>
  );
}

function StateText({ text, detail, action, onAction }: {
  readonly text: string;
  readonly detail?: string | undefined;
  readonly action?: string;
  readonly onAction?: () => void;
}) {
  return (
    <div className="changes-state">
      <p role="status">{text}</p>
      {detail !== undefined && <p className="changes-detail">{detail}</p>}
      {action !== undefined && <button type="button" onClick={onAction}>{action}</button>}
    </div>
  );
}
