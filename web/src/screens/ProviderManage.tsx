import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ApiError, client } from '../api/http';
import type { Computer } from '../computers';
import type {
  HTTPProviderGroup, HTTPProviderItem, HTTPProviderManageRequest, HTTPProviderManageResponse, HTTPSettingsResponse,
} from '../generated/protocol';
import { checkAgain } from './allSessions';
import type { ProviderTap } from './Herdr';
import { NewSessionHelp } from './NewSession';
import type { ProviderEntry, ProviderSource } from './providers';
import { Sheet } from './Sheet';
import { Button, Notice } from './ui';

/*
 * Manage a provider from the phone (docs/herdr-manage.md, sections 3 and 6):
 * the item menu (New tab, Close workspace, Remove worktree), New workspace and
 * Start. The app asks before anything ends or is deleted; the request carries
 * the name the user confirmed, and the host checks it on a fresh list. A
 * failure shows the computer's own fixed words (a peer's text is never shown:
 * api/http.ts), never herdr's or git's.
 */

/** Said when the item left or was renamed between the list and the action: nothing changed. */
export const CHANGED = 'The list changed. Look again, then try again.';

type Manage = (body: HTTPProviderManageRequest) => Promise<HTTPProviderManageResponse>;

/** What a sheet says after an action that did not end it. */
export type ManageNote =
  /** Removed nothing: the worktree has changes that are not committed. */
  | { readonly kind: 'uncommitted' }
  /** Made, but this provider version cannot open a terminal without an agent. */
  | { readonly kind: 'made'; readonly what: 'tab' | 'workspace' }
  | { readonly kind: 'error'; readonly text: string };

export type ManageStep =
  /** A new terminal: open it in Live, as a tap does. */
  | { readonly kind: 'open'; readonly targetId: string }
  /** Done, and the list was read again. */
  | { readonly kind: 'done' }
  | { readonly kind: 'note'; readonly note: ManageNote }
  /** CHANGED, and the list was read again. */
  | { readonly kind: 'changed' };

/**
 * One manage action, start to end. After every success, and after "the list
 * changed", the provider's list is read again at once (a new terminal's read
 * is the caller's, with the move to Live). `reload` must not reject.
 */
export async function manageStep(
  body: HTTPProviderManageRequest, manage: Manage, reload: () => Promise<void>, words: (error: unknown) => string,
): Promise<ManageStep> {
  let answer: HTTPProviderManageResponse;
  try {
    answer = await manage(body);
  } catch (error) {
    if (error instanceof ApiError && (error.code === 'operation_conflict' || error.code === 'session_missing')) {
      await reload();
      return { kind: 'changed' };
    }
    return { kind: 'note', note: { kind: 'error', text: words(error) } };
  }
  if (answer.problem === 'uncommitted') return { kind: 'note', note: { kind: 'uncommitted' } };
  if (answer.targetId !== undefined) return { kind: 'open', targetId: answer.targetId };
  await reload();
  if (body.action === 'newTab') return { kind: 'note', note: { kind: 'made', what: 'tab' } };
  if (body.action === 'newWorkspace') return { kind: 'note', note: { kind: 'made', what: 'workspace' } };
  return { kind: 'done' };
}

/** Close and remove carry the item's name as the user confirmed it. */
export function itemRequest(action: 'newTab' | 'closeItem' | 'removeItem', item: Pick<HTTPProviderItem, 'id' | 'name'>): HTTPProviderManageRequest {
  return action === 'newTab' ? { action, itemId: item.id } : { action, itemId: item.id, name: item.name };
}

/** A main checkout whose project has an open linked worktree: herdr would close it too (or refuse), so the worktrees go first (the host's rule). */
export const closeWorktreesFirst = (group: Pick<HTTPProviderGroup, 'items'>, item: Pick<HTTPProviderItem, 'linked'>): boolean =>
  item.linked !== true && group.items.some((other) => other.linked === true);

/** One action at a time per sheet, also when two taps land before the re-render; an answer after the sheet left does nothing. */
function useAction<Busy>() {
  const [busy, setBusy] = useState<Busy>();
  const running = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  function run(what: Busy, work: () => Promise<ManageStep>, then: (step: ManageStep) => void) {
    if (running.current) return;
    running.current = true;
    setBusy(what);
    void work().then((step) => {
      if (!mounted.current) return;
      running.current = false;
      setBusy(undefined);
      then(step);
    });
  }
  return { busy, running, run };
}

/** Focus returns to the button that opened the sheet when it closes, if that button is still in the page. */
function useReturnFocus(): void {
  const [opener] = useState(() => globalThis.document?.activeElement);
  useEffect(() => () => { if (opener instanceof HTMLElement && opener.isConnected) opener.focus(); }, [opener]);
}

function NoteLine({ note, name, provider }: { readonly note: ManageNote; readonly name: string; readonly provider: string }) {
  switch (note.kind) {
    case 'uncommitted': return <Notice tone="warning" role="alert" title={<><bdi>{name}</bdi> has changes that are not committed. Commit or discard them first.</>} />;
    case 'made': return <Notice tone="neutral" role="status" title={<>The {note.what} is made. Update <bdi>{provider}</bdi> to open it from the phone.</>} />;
    case 'error': return <Notice tone="danger" role="alert" title={note.text} />;
  }
}

function Head({ id, caption, title, busy, onClose }: {
  readonly id: string;
  readonly caption: ReactNode;
  readonly title: ReactNode;
  readonly busy: boolean;
  readonly onClose: () => void;
}) {
  return (
    <div className="sheet-header create-head">
      <div className="create-title">
        <p className="caps">{caption}</p>
        <h2 id={id}>{title}</h2>
      </div>
      <button type="button" className="link" disabled={busy} onClick={onClose}>Close</button>
    </div>
  );
}

export type ItemMenuMode = 'menu' | 'close' | 'remove' | 'changed';
type ItemAction = 'newTab' | 'closeItem' | 'removeItem';

/** The item menu and its two confirmations. State comes from props, so each state renders on its own. */
export function ItemMenu({ entry, group, item, mode, busy, note, onMode, onAction, onClose }: {
  readonly entry: Pick<ProviderEntry, 'computer' | 'info'>;
  readonly group: HTTPProviderGroup;
  readonly item: HTTPProviderItem;
  readonly mode: ItemMenuMode;
  readonly busy: ItemAction | undefined;
  readonly note: ManageNote | undefined;
  readonly onMode: (mode: ItemMenuMode) => void;
  readonly onAction: (action: ItemAction) => void;
  readonly onClose: () => void;
}) {
  const { info, computer } = entry;
  const off = busy !== undefined;
  const first = closeWorktreesFirst(group, item);
  return (
    <div className="stack">
      <Head
        id="item-menu-title"
        caption={<><bdi>{info.name}</bdi>{group.name !== item.name && <> · <bdi>{group.name}</bdi></>} · on {computer.label}</>}
        title={<bdi>{item.name}</bdi>}
        busy={off}
        onClose={onClose}
      />
      {mode === 'changed' && <Notice tone="danger" role="alert" title={CHANGED} />}
      {mode !== 'changed' && note !== undefined && <NoteLine note={note} name={item.name} provider={info.name} />}
      {mode === 'menu' && (
        <ul className="menu-list">
          <li><Button busy={busy === 'newTab'} busyLabel="Opening…" disabled={off} onClick={() => onAction('newTab')}>New tab</Button></li>
          <li>
            <button type="button" className="danger" disabled={off || first} {...(first ? { 'aria-describedby': 'close-first' } : {})} onClick={() => onMode('close')}>Close workspace</button>
            {first && <p id="close-first" className="hint-text">Close its worktrees first.</p>}
          </li>
          {item.linked === true && (
            <li><button type="button" className="danger" disabled={off} onClick={() => onMode('remove')}>Remove worktree</button></li>
          )}
        </ul>
      )}
      {mode === 'close' && (
        <div role="alertdialog" aria-labelledby="manage-confirm" className="confirm stack">
          <p id="manage-confirm">Close <bdi>{item.name}</bdi>? Its terminals end. Files stay.</p>
          <div className="row">
            <Button className="danger" busy={busy === 'closeItem'} busyLabel="Closing…" onClick={() => onAction('closeItem')}>Close workspace</Button>
            <button type="button" disabled={off} onClick={() => onMode('menu')}>Cancel</button>
          </div>
        </div>
      )}
      {mode === 'remove' && (
        <div role="alertdialog" aria-labelledby="manage-confirm" className="confirm stack">
          <p id="manage-confirm">Remove the worktree <bdi>{item.name}</bdi>? Its folder is deleted, also files that git ignores (such as .env). The branch stays.</p>
          <div className="row">
            <Button className="danger" busy={busy === 'removeItem'} busyLabel="Removing…" onClick={() => onAction('removeItem')}>Remove worktree</Button>
            <button type="button" disabled={off} onClick={() => onMode('menu')}>Cancel</button>
          </div>
        </div>
      )}
    </div>
  );
}

/** The ⋯ of an item row, with its own state. `onOpened` opens a new tab's terminal; anything else stays here or closes. */
export function ItemMenuSheet({ entry, group, item, onOpened, onReload, onFailure, onClose, manage = (body) => client(entry.computer.origin).providerManage(entry.info.id, body) }: {
  readonly entry: ProviderEntry;
  readonly group: HTTPProviderGroup;
  readonly item: HTTPProviderItem;
  readonly onOpened: (tap: ProviderTap) => void;
  /** Reads this provider's list again; never rejects. */
  readonly onReload: () => Promise<void>;
  /** Words a failure of this computer (a peer's 401 says pair again; it never sends the user to Setup). */
  readonly onFailure: (error: unknown, from: Computer) => string;
  readonly onClose: () => void;
  readonly manage?: Manage;
}) {
  const [mode, setMode] = useState<ItemMenuMode>('menu');
  const [note, setNote] = useState<ManageNote>();
  const { busy, running, run } = useAction<ItemAction>();
  useReturnFocus();
  const close = () => { if (!running.current) onClose(); };

  function act(action: ItemAction) {
    setNote(undefined);
    run(action, () => manageStep(itemRequest(action, item), manage, onReload, (error) => onFailure(error, entry.computer)), (step) => {
      switch (step.kind) {
        case 'open': onOpened({ computer: entry.computer, provider: entry.info.id, targetId: step.targetId }); break;
        case 'done': onClose(); break;
        case 'changed': setMode('changed'); break;
        case 'note': setNote(step.note); setMode('menu'); break;
      }
    });
  }

  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="item-menu-title" onClose={close}>
        <ItemMenu
          entry={entry}
          group={group}
          item={item}
          mode={mode}
          busy={busy}
          note={note}
          onMode={(next) => { setNote(undefined); setMode(next); }}
          onAction={act}
          onClose={close}
        />
      </Sheet>
    </div>
  );
}

/** New workspace: the computer's Termivoa projects, or how to add one, as New session says. */
export function WorkspaceList({ entry, settings, busy, checking, note, onPick, onCheck, onClose }: {
  readonly entry: Pick<ProviderEntry, 'computer' | 'info'>;
  /** The settings of that computer: its projects are the ones New session lists. */
  readonly settings: HTTPSettingsResponse;
  /** The project whose workspace is being made. */
  readonly busy: string | undefined;
  readonly checking: boolean;
  readonly note: ManageNote | undefined;
  readonly onPick: (projectId: string) => void;
  readonly onCheck: () => void;
  readonly onClose: () => void;
}) {
  const { info, computer } = entry;
  const off = busy !== undefined;
  return (
    <div className="stack">
      <Head id="workspace-title" caption={<><bdi>{info.name}</bdi> · on {computer.label}</>} title="New workspace" busy={off} onClose={onClose} />
      {note !== undefined && <NoteLine note={note} name="" provider={info.name} />}
      {settings.projects.length === 0 ? (
        <>
          <NewSessionHelp settings={settings} />
          <Button className="secondary" busy={checking} busyLabel="Checking…" onClick={onCheck}>Check again</Button>
        </>
      ) : (
        <>
          <p className="hint-text">Pick a project. <bdi>{info.name}</bdi> opens a workspace in its folder.</p>
          <ul className="menu-list">
            {settings.projects.map((project) => (
              <li key={project.id}>
                <Button busy={busy === project.id} busyLabel="Opening…" disabled={off} onClick={() => onPick(project.id)}><bdi>{project.name}</bdi></Button>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

export function WorkspaceSheet({ entry, settings, onOpened, onReload, onCheck, onFailure, onClose, manage = (body) => client(entry.computer.origin).providerManage(entry.info.id, body) }: {
  readonly entry: ProviderEntry;
  readonly settings: HTTPSettingsResponse;
  readonly onOpened: (tap: ProviderTap) => void;
  /** Reads this provider's list again; never rejects. */
  readonly onReload: () => Promise<void>;
  /** Reads that computer again, settings included (Check again). */
  readonly onCheck: () => Promise<void>;
  readonly onFailure: (error: unknown, from: Computer) => string;
  readonly onClose: () => void;
  readonly manage?: Manage;
}) {
  const [note, setNote] = useState<ManageNote>();
  const [checking, setChecking] = useState(false);
  const { busy, running, run } = useAction<string>();
  useReturnFocus();
  const close = () => { if (!running.current) onClose(); };
  const words = (error: unknown) => onFailure(error, entry.computer);

  function pick(projectId: string) {
    setNote(undefined);
    run(projectId, () => manageStep({ action: 'newWorkspace', projectId }, manage, onReload, words), (step) => {
      switch (step.kind) {
        case 'open': onOpened({ computer: entry.computer, provider: entry.info.id, targetId: step.targetId }); break;
        case 'done': onClose(); break;
        case 'changed': setNote({ kind: 'error', text: CHANGED }); break;
        case 'note': setNote(step.note); break;
      }
    });
  }

  function check() {
    setChecking(true);
    setNote(undefined);
    void checkAgain(onCheck(), words).then((problem) => {
      if (problem !== undefined) setNote({ kind: 'error', text: problem });
      setChecking(false);
    });
  }

  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="workspace-title" onClose={close}>
        <WorkspaceList entry={entry} settings={settings} busy={busy} checking={checking} note={note} onPick={pick} onCheck={check} onClose={close} />
      </Sheet>
    </div>
  );
}

/** Start herdr: on its Integrations card and on the provider tab's "not running" notice. */
export function StartButton({ source, className = 'secondary', onStarted, onFailure, manage = (body) => client(source.computer.origin).providerManage(source.info.id, body) }: {
  readonly source: Pick<ProviderSource, 'computer' | 'info'>;
  readonly className?: string;
  /** Reads the provider again, so its state shows; never rejects. */
  readonly onStarted: () => Promise<void>;
  readonly onFailure: (error: unknown, from: Computer) => string;
  readonly manage?: Manage;
}) {
  const [error, setError] = useState<string>();
  const { busy, run } = useAction<true>();
  const start = () => {
    setError(undefined);
    run(true, () => manageStep({ action: 'startServer' }, manage, onStarted, (failure) => onFailure(failure, source.computer)), (step) => {
      setError(step.kind === 'note' && step.note.kind === 'error' ? step.note.text : step.kind === 'changed' ? CHANGED : undefined);
    });
  };
  return (
    <>
      <Button className={className} busy={busy === true} busyLabel="Starting…" onClick={start}><span>Start <bdi>{source.info.name}</bdi></span></Button>
      {error !== undefined && <p role="alert" className="error">{error}</p>}
    </>
  );
}
