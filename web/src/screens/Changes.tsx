import { Fragment, useState, useSyncExternalStore, type CSSProperties, type ReactElement, type ReactNode } from 'react';
import { homeOrigin } from '../api/http';
import type { ChangedFile, ChangeStatus, DiffLine } from '../generated/protocol';
import type { ChangesFileView, ChangesView, ChangesWrite, Connection } from '../terminal/Connection';
import { readDraft, saveDraft } from '../terminal/composer';
import { displayPath } from './folder';
import { Sheet, SheetHeader } from './Sheet';

const STATUS: Record<ChangeStatus, { readonly letter: string; readonly name: string }> = {
  modified: { letter: 'M', name: 'Modified' },
  added: { letter: 'A', name: 'Added' },
  deleted: { letter: 'D', name: 'Deleted' },
  renamed: { letter: 'R', name: 'Renamed' },
  untracked: { letter: 'U', name: 'Untracked' },
  typechange: { letter: 'T', name: 'Type changed' },
};

const SIGN: Record<DiffLine['kind'], string> = { hunk: '', context: ' ', add: '+', remove: '-' };

/**
 * Adds "@path " to the session's composer draft, or, with selected lines, a
 * quote of them to ask about. It never sends anything.
 */
export function askAboutFile(key: string, path: string, selected: ReadonlyArray<DiffLine> = []): void {
  const draft = readDraft(key);
  if (selected.length > 0) {
    saveDraft(key, `${draft}${draft === '' || draft.endsWith('\n') ? '' : '\n'}${linesDraft(path, selected)}`);
    return;
  }
  const space = draft === '' || /\s$/u.test(draft) ? '' : ' ';
  saveDraft(key, `${draft}${space}@${path} `);
}

/** Lines in one range, at most. */
export const MAX_SELECTED_LINES = 20;

/** Selected diff lines, by index into the patch lines they were picked from. */
export interface LineSelection {
  /** A new patch (a refresh) makes the selection stale. */
  readonly lines: ReadonlyArray<DiffLine>;
  readonly anchor: number;
  readonly end: number;
  /** Why a range was cut short. */
  readonly note?: string;
}

/**
 * The selection after a tap on line `index`: the first tap picks a line, a
 * second picks the range between them, and a tap on a picked line clears it.
 * A range stops at MAX_SELECTED_LINES and at a hunk header, which is never picked.
 */
export function tapLine(current: LineSelection | undefined, lines: ReadonlyArray<DiffLine>, index: number): LineSelection | undefined {
  const selection = current?.lines === lines ? current : undefined;
  if (lines[index] === undefined || lines[index].kind === 'hunk') {
    return selection;
  }
  if (selection === undefined || selection.anchor !== selection.end) {
    if (selection !== undefined && index >= Math.min(selection.anchor, selection.end) && index <= Math.max(selection.anchor, selection.end)) {
      return undefined;
    }
    return { lines, anchor: index, end: index };
  }
  const { anchor } = selection;
  if (index === anchor) {
    return undefined;
  }
  const step = index > anchor ? 1 : -1;
  let end = anchor;
  while (end !== index && lines[end + step]?.kind !== 'hunk' && Math.abs(end + step - anchor) < MAX_SELECTED_LINES) {
    end += step;
  }
  if (end === index) {
    return { lines, anchor, end };
  }
  const note = lines[end + step]?.kind === 'hunk'
    ? 'A range stays inside one part of the diff.'
    : `A range can have ${MAX_SELECTED_LINES} lines at most.`;
  return { lines, anchor, end, note };
}

/** The picked lines in file order; none when the selection is from another patch. */
export function selectedLines(selection: LineSelection | undefined, lines: ReadonlyArray<DiffLine>): ReadonlyArray<DiffLine> {
  if (selection === undefined || selection.lines !== lines) {
    return [];
  }
  return lines.slice(Math.min(selection.anchor, selection.end), Math.max(selection.anchor, selection.end) + 1);
}

/** "line 42" or "lines 40–42": new-file numbers when any line has one, else old ones. */
export function linesLabel(selected: ReadonlyArray<DiffLine>): string {
  const useNew = selected.some((line) => line.newLine !== undefined);
  const numbers = selected.map((line) => (useNew ? line.newLine : line.oldLine)).filter((number) => number !== undefined);
  const first = numbers[0];
  const last = numbers.at(-1);
  return first === last ? `line ${first}` : `lines ${first}–${last}`;
}

/** "@path lines 40–42:", the lines quoted with their signs, then a blank line for the question. */
export function linesDraft(path: string, selected: ReadonlyArray<DiffLine>): string {
  const body = selected.map((line) => `${SIGN[line.kind]}${line.text}`).join('\n');
  // A fence longer than any backtick run in the lines, so the quote cannot end early.
  const fence = '`'.repeat(Math.max(3, ...[...body.matchAll(/`+/gu)].map((run) => run[0].length + 1)));
  return `@${path} ${linesLabel(selected)}:\n${fence}\n${body}\n${fence}\n\n`;
}

/** "src/app/" and "main.ts": the directory is shown muted, the name strong. */
export function splitPath(path: string): { readonly dir: string; readonly name: string } {
  const slash = path.lastIndexOf('/');
  return { dir: path.slice(0, slash + 1), name: path.slice(slash + 1) };
}

/**
 * A first commit message from the selected files: "Update app.js",
 * "Update app.js, util.js", "Update app.js and 4 more files"; "Add" when
 * every file is new.
 */
export function suggestMessage(files: ReadonlyArray<Pick<ChangedFile, 'path' | 'status'>>): string {
  const names = files.map((file) => splitPath(file.path).name);
  const verb = files.every((file) => file.status === 'added' || file.status === 'untracked') ? 'Add' : 'Update';
  if (names.length === 0) return '';
  return names.length <= 3 ? `${verb} ${names.join(', ')}` : `${verb} ${names[0]} and ${names.length - 1} more files`;
}

/**
 * The link only when it is a real https URL that is not this app; anything
 * else is dropped. A computer chooses this URL, so it must never open an app
 * route such as /computers/add#… on home.
 */
export function httpsUrl(url: string | undefined, app: string = homeOrigin()): string | undefined {
  try {
    const parsed = new URL(url ?? '');
    return parsed.protocol === 'https:' && parsed.origin !== app ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

/** The files a ready list shows; none while loading, failed or outside a repository. */
function listed(view: ChangesView | undefined): ReadonlyArray<ChangedFile> {
  return view?.state !== 'unavailable' && view?.summary?.state === 'ready' ? view.summary.files : [];
}

/** The uncommitted git changes in the session's project, with commit and push for the controller. */
export function Changes({ connection, cwd, switcher, onAsk, onReturn }: {
  readonly connection: Connection;
  /** The session's folder, shown when it is not in a repository. */
  readonly cwd: string | undefined;
  /** The Changes | Files switch, in place of the title. */
  readonly switcher?: ReactNode;
  readonly onAsk: (path: string, selected: ReadonlyArray<DiffLine>) => void;
  readonly onReturn: () => void;
}) {
  const view = useSyncExternalStore(connection.subscribe, connection.getChanges);
  const write = useSyncExternalStore(connection.subscribe, connection.getChangesWrite);
  const status = useSyncExternalStore(connection.subscribe, connection.getStatus);
  // Unticked paths, so every file, including one a refresh adds, starts ticked.
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(() => new Set());
  // A write still pending or unseen from an earlier visit shows its sheet again.
  const [step, setStep] = useState<ShipStep | undefined>(write?.kind);
  const [message, setMessage] = useState('');
  const [picked, setPicked] = useState<LineSelection>();
  const selected = listed(view).filter((file) => !excluded.has(file.path));
  const pending = write?.state === 'pending';
  const control = status.control === 'controller';

  function close() {
    connection.dismissChangesWrite();
    setStep(undefined);
  }

  return (
    <>
      <ChangesPanel
        view={view}
        cwd={cwd}
        switcher={switcher}
        onRefresh={() => connection.refreshChanges()}
        onOpen={(path) => { setPicked(undefined); connection.openChangesFile(path); }}
        onBack={() => { setPicked(undefined); connection.closeChangesFile(); }}
        onAsk={onAsk}
        selection={picked}
        onSelect={setPicked}
        onReturn={onReturn}
        commit={{
          control,
          excluded,
          pending,
          covered: step !== undefined,
          onToggle: (path) => setExcluded((current) => {
            const next = new Set(current);
            if (!next.delete(path)) next.add(path);
            return next;
          }),
          onCommit: () => {
            connection.dismissChangesWrite();
            setMessage(suggestMessage(selected));
            setStep('commit');
          },
        }}
      />
      {step !== undefined && (
        <Sheet labelledBy="ship-title" onClose={() => { if (!pending) close(); }}>
          <ShipFlow
            step={step}
            write={write}
            control={control}
            message={message}
            count={selected.length}
            branch={view?.summary?.branch}
            onMessage={setMessage}
            onCommit={() => connection.commitChanges(message.trim(), selected.map((file) => file.path))}
            onPush={() => setStep('push')}
            onConfirmPush={() => connection.pushChanges()}
            onClose={close}
          />
        </Sheet>
      )}
    </>
  );
}

/** Commit controls for the list; absent in a read-only list. */
export interface CommitControls {
  readonly control: boolean;
  readonly excluded: ReadonlySet<string>;
  /** A commit or push is in flight. */
  readonly pending: boolean;
  /** The commit sheet is open over the list. */
  readonly covered: boolean;
  readonly onToggle: (path: string) => void;
  readonly onCommit: () => void;
}

export function ChangesPanel({ view, cwd, switcher, commit, selection, onRefresh, onOpen, onBack, onAsk, onSelect, onReturn }: {
  readonly view: ChangesView | undefined;
  readonly cwd: string | undefined;
  readonly switcher?: ReactNode;
  readonly commit?: CommitControls;
  /** Picked lines in the open file. */
  readonly selection?: LineSelection | undefined;
  readonly onRefresh: () => void;
  readonly onOpen: (path: string) => void;
  readonly onBack: () => void;
  readonly onAsk: (path: string, selected: ReadonlyArray<DiffLine>) => void;
  readonly onSelect?: (selection: LineSelection | undefined) => void;
  readonly onReturn: () => void;
}) {
  const file = view?.file;
  return (
    <>
      {/* Hidden, not unmounted, so the list keeps its scroll position. */}
      <ChangeList view={view} cwd={cwd} switcher={switcher} hidden={file !== undefined} commit={commit} onRefresh={onRefresh} onOpen={onOpen} onReturn={onReturn} />
      {file !== undefined && (
        <FileDiff
          file={file}
          entry={view?.summary?.files.find((item) => item.path === file.path)}
          selection={selection}
          onRefresh={onRefresh}
          onBack={onBack}
          onAsk={onAsk}
          onSelect={onSelect ?? (() => undefined)}
        />
      )}
    </>
  );
}

function ChangeList({ view, cwd, switcher, hidden, commit, onRefresh, onOpen, onReturn }: {
  readonly view: ChangesView | undefined;
  readonly cwd: string | undefined;
  readonly switcher: ReactNode;
  readonly hidden: boolean;
  readonly commit: CommitControls | undefined;
  readonly onRefresh: () => void;
  readonly onOpen: (path: string) => void;
  readonly onReturn: () => void;
}) {
  const summary = view?.summary;
  const loading = view === undefined || view.state === 'loading';
  const failed = view?.state === 'unavailable' || summary?.state === 'unavailable';
  const files = listed(view);
  const ticking = commit?.control === true;
  const selected = files.filter((item) => !commit?.excluded.has(item.path)).length;
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
    <section className="history changes" aria-labelledby="changes-title" hidden={hidden} inert={commit?.covered === true}>
      <header className="history-bar changes-bar">
        <div className="changes-heading">
          <h2 id="changes-title" className={switcher === undefined ? undefined : 'visually-hidden'}>Changes</h2>
          {switcher}
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
              <li key={`${item.status}:${item.path}`}>
                {ticking && (
                  <label className="change-check">
                    <input
                      type="checkbox"
                      aria-label={`Include ${item.path}`}
                      checked={!commit.excluded.has(item.path)}
                      onChange={() => commit.onToggle(item.path)}
                    />
                  </label>
                )}
                <ChangeRow file={item} onOpen={onOpen} />
              </li>
            ))}
          </ul>
        )}
        {files.length > 0 && summary?.truncated === true && (
          <p className="changes-note" role="status">Only the first {files.length} files are shown.</p>
        )}
      </div>
      {commit !== undefined && files.length > 0 && (
        <div className="history-actions changes-commit">
          {ticking ? (
            <button type="button" className="primary wide" disabled={selected === 0 || commit.pending} onClick={commit.onCommit}>
              {commit.pending ? 'Working…' : `Commit ${selected} ${selected === 1 ? 'file' : 'files'}`}
            </button>
          ) : (
            <p className="hint-text">Take control to commit</p>
          )}
        </div>
      )}
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
export function breakable(dir: string) {
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

function FileDiff({ file, entry, selection, onRefresh, onBack, onAsk, onSelect }: {
  readonly file: ChangesFileView;
  /** The file's row in the current summary, for its counts. */
  readonly entry: ChangedFile | undefined;
  readonly selection: LineSelection | undefined;
  readonly onRefresh: () => void;
  readonly onBack: () => void;
  readonly onAsk: (path: string, selected: ReadonlyArray<DiffLine>) => void;
  readonly onSelect: (selection: LineSelection | undefined) => void;
}) {
  const { dir, name } = splitPath(file.path);
  const patch = file.patch;
  const lines = patch?.state === 'ready' ? patch.lines : [];
  const selected = selectedLines(selection, lines);
  const note = selected.length > 0 ? selection?.note : undefined;
  const from = selection === undefined ? 0 : Math.min(selection.anchor, selection.end);
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
        {lines.map((line, index) => {
          if (line.kind === 'hunk') {
            return <div key={index} className="diff-line hunk"><span className="diff-text">{line.text}</span></div>;
          }
          const picked = index >= from && index < from + selected.length;
          const tap = () => onSelect(tapLine(selection, lines, index));
          // A div, not a button, so the text can still be selected and copied.
          return (
            <div
              key={index}
              className={picked ? `diff-line ${line.kind} picked` : `diff-line ${line.kind}`}
              role="button"
              tabIndex={0}
              aria-pressed={picked}
              onClick={tap}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault();
                  tap();
                }
              }}
            >
              <span className="diff-num" aria-hidden="true">{line.oldLine}</span>
              <span className="diff-num" aria-hidden="true">{line.newLine}</span>
              <span className="diff-sign">{SIGN[line.kind]}</span>
              <span className="diff-text">{line.text}</span>
            </div>
          );
        })}
        {patch?.truncated === true && (
          <p className="changes-note" role="status">The diff is cut short here. See the rest on your computer.</p>
        )}
      </div>
      <div className="history-actions">
        {note !== undefined && <p className="changes-note" role="status">{note}</p>}
        <div className="ask-row">
          <button type="button" className="primary wide" onClick={() => onAsk(file.path, selected)}>
            <span aria-hidden="true">@</span>
            Ask agent about {selected.length > 0 ? linesLabel(selected) : 'this file'}
          </button>
          {selected.length > 0 && <button type="button" onClick={() => onSelect(undefined)}>Clear</button>}
        </div>
      </div>
    </section>
  );
}

export function StateText({ text, detail, action, alert = false, onAction }: {
  readonly text: string;
  readonly detail?: string | undefined;
  readonly action?: string;
  /** A failure: read out at once. */
  readonly alert?: boolean;
  readonly onAction?: () => void;
}) {
  return (
    <div className="changes-state">
      <p role={alert ? 'alert' : 'status'}>{text}</p>
      {detail !== undefined && <p className="changes-detail">{detail}</p>}
      {action !== undefined && <button type="button" onClick={onAction}>{action}</button>}
    </div>
  );
}

type ShipStep = ChangesWrite['kind'];

/**
 * The commit sheet's content: the message form, then the result, then an
 * optional push with its own confirm and result. It sends nothing on its own.
 */
export function ShipFlow({ step, write, control, message, count, branch, onMessage, onCommit, onPush, onConfirmPush, onClose }: {
  readonly step: ShipStep;
  readonly write: ChangesWrite | undefined;
  readonly control: boolean;
  readonly message: string;
  readonly count: number;
  readonly branch: string | undefined;
  readonly onMessage: (message: string) => void;
  readonly onCommit: () => void;
  readonly onPush: () => void;
  readonly onConfirmPush: () => void;
  readonly onClose: () => void;
}) {
  const mine = write?.kind === step ? write : undefined;
  const pending = mine?.state === 'pending';
  const name = branch ?? 'this branch';

  if (step === 'commit' && (mine === undefined || pending)) {
    return (
      <>
        <SheetHeader id="ship-title" title="Commit" />
        <label className="visually-hidden" htmlFor="commit-message">Commit message</label>
        <textarea
          id="commit-message"
          className="draft commit-message"
          value={message}
          maxLength={2000}
          rows={3}
          readOnly={pending}
          onChange={(event) => onMessage(event.target.value)}
        />
        <p className="hint-text">
          {count} {count === 1 ? 'file' : 'files'} selected{control ? '' : ' · Take control to commit'}
        </p>
        <div className="ship-actions">
          <button type="button" disabled={pending} onClick={onClose}>Cancel</button>
          <button type="button" className="primary" disabled={pending || !control || count === 0 || message.trim() === ''} onClick={onCommit}>
            {pending ? 'Committing…' : 'Commit'}
          </button>
        </div>
      </>
    );
  }

  if (step === 'push' && mine === undefined) {
    return (
      <>
        <SheetHeader id="ship-title" title="Push" />
        <p className="ship-text">Push <code>{name}</code> to its remote? This does not force.</p>
        <div className="ship-actions">
          <button type="button" onClick={onClose}>Cancel</button>
          <button type="button" className="primary" disabled={!control} onClick={onConfirmPush}>Push</button>
        </div>
      </>
    );
  }

  let text: ReactElement | string;
  let output: string | undefined;
  let next: ReactElement | undefined;
  const committed = mine?.committed;
  const pushed = mine?.pushed;
  if (pending) {
    text = 'Pushing…';
  } else if (mine?.state === 'error') {
    text = mine.message ?? 'That did not work.';
  } else if (committed !== undefined) {
    output = committed.state === 'failed' ? committed.output : undefined;
    if (committed.state === 'committed') {
      text = committed.commit === undefined ? 'Committed.' : <>Committed <code>{committed.commit}</code></>;
      next = <button type="button" className="primary" disabled={!control} onClick={onPush}>Push</button>;
    } else {
      text = committed.state === 'nothing'
        ? 'Nothing to commit. The selected files have no changes now.'
        : 'The commit did not go through.';
    }
  } else {
    output = pushed?.state === 'pushed' ? undefined : pushed?.output;
    const url = httpsUrl(pushed?.url);
    if (pushed?.state === 'pushed') {
      text = <>Pushed <code>{pushed.branch ?? name}</code></>;
      next = url === undefined ? undefined : (
        <a className="ship-link" href={url} target="_blank" rel="noopener noreferrer">Open pull request</a>
      );
    } else {
      text = pushed?.state === 'no_remote' ? 'This repository has no remote to push to.' : 'The push did not go through.';
    }
  }

  return (
    <>
      <SheetHeader id="ship-title" title={step === 'commit' ? 'Commit' : 'Push'} />
      <p className="ship-text" role="status">{text}</p>
      {output !== undefined && output !== '' && (
        <pre className="ship-output" tabIndex={0} aria-label="Output from git">{output}</pre>
      )}
      <div className="ship-actions">
        <button type="button" disabled={pending} onClick={onClose}>Close</button>
        {next}
      </div>
    </>
  );
}
