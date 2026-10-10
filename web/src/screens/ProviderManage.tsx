import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ApiError, client, manageTimeout } from '../api/http';
import type { Computer } from '../computers';
import {
  FEATURE_REVISION,
  type HTTPProviderGroup, type HTTPProviderItem, type HTTPProviderManageRequest, type HTTPProviderManageResponse, type HTTPSettingsResponse,
} from '../generated/protocol';
import { checkAgain } from './allSessions';
import type { ProviderTap } from './Herdr';
import { NewSessionHelp } from './NewSession';
import { droppedAgent, offeredAgent, PromptField, promptProblem, startNotice } from './ProviderCreate';
import type { ProviderEntry, ProviderSource } from './providers';
import { Sheet } from './Sheet';
import { useAgentKinds, type ReadAgentKinds } from './useAgentKinds';

/** From GitHub shows where that computer has a GitHub row (revision 7): the same gate as GitHub.tsx's githubInfo, kept here so this sheet does not load GitHub's code. */
export const githubShown = (settings: Pick<HTTPSettingsResponse, 'protocolRevision' | 'github'>): boolean =>
  settings.protocolRevision >= FEATURE_REVISION.github && settings.github !== undefined;
import { Button, ConfirmPair, confirmReady, Notice } from './ui';

/*
 * Manage a provider from the phone (docs/herdr-manage.md, sections 3 and 6):
 * the item menu (New tab, Close tab, Close workspace, Remove worktree), New workspace and
 * Start. The app asks before anything ends or is deleted; the request carries
 * the name the user confirmed, and the host checks it on a fresh list. A
 * failure shows the computer's own fixed words (a peer's text is never shown:
 * api/http.ts), never herdr's or git's.
 */

/** Said when the item left or was renamed between the list and the action: nothing changed. */
export const CHANGED = 'The list changed. Look again, then try again.';
/** Said when Close tab is refused for an item's only tab: the provider would close the whole workspace with it. */
export const LAST_TAB = 'This is the workspace\'s last tab. Close the workspace.';
/** Shown in place of the Close tab buttons while an item has one terminal. */
export const LAST_TAB_HINT = 'Close the workspace to end its last tab.';
/** LAST_TAB_HINT's place when the workspace must close its worktrees first. */
export const WORKTREES_FIRST_TAB_HINT = 'Close its worktrees first, then close this workspace to end its last tab.';
/** LAST_TAB_HINT's place when the workspace closes only together with the others at the main folder. */
export const TOGETHER_TAB_HINT = 'End this last tab by closing its workspace in herdr on the computer.';
/** Fixed app words, also for a peer whose error detail is deliberately hidden. */
export const CLOSE_CHANGED = 'The list changed. Look again before closing. Close its worktrees first; workspaces sharing the main folder must be closed in herdr on the computer.';

type Manage = (body: HTTPProviderManageRequest) => Promise<HTTPProviderManageResponse>;

/** What a sheet says after an action that did not end it. */
export type ManageNote =
  /** Removed nothing: the worktree has changes that are not committed. */
  | { readonly kind: 'uncommitted' }
  /** Made, but this provider version cannot open a terminal without an agent. */
  | { readonly kind: 'made'; readonly what: 'tab' | 'workspace' | 'worktree' }
  | { readonly kind: 'error'; readonly text: string };

export type ManageStep =
  /** A new terminal: open it in Live, as a tap does. `notice`: what did not start in a new tab with an agent (create's words). */
  | { readonly kind: 'open'; readonly targetId: string; readonly notice?: string }
  /** Done, and the list was read again. */
  | { readonly kind: 'done' }
  | { readonly kind: 'note'; readonly note: ManageNote }
  /** CHANGED (or `text`), and the list was read again. */
  | { readonly kind: 'changed'; readonly text?: string };

export const SLOW = 'This takes longer than the phone waits. Look at the list before you try again.';

/**
 * One manage action, start to end. After every success, and after "the list
 * changed", the provider's list is read again at once (a new terminal's read
 * is the caller's, with the move to Live). `reload` must not reject.
 */
export async function manageStep(
  body: HTTPProviderManageRequest, manage: Manage, reload: () => Promise<void>, words: (error: unknown) => string, clock: () => number = Date.now,
): Promise<ManageStep> {
  let answer: HTTPProviderManageResponse;
  const started = clock();
  try {
    answer = await manage(body);
  } catch (error) {
    // The phone's own wait ran out: the computer may still finish, so this is not "can't reach" (and no Offline screen),
    // and the list says what exists before the user tries again.
    if (error instanceof ApiError && error.code === 'network' && clock() - started >= manageTimeout(body) - 1000) {
      await reload();
      return { kind: 'note', note: { kind: 'error', text: SLOW } };
    }
    // Nothing changed: the tab is the item's last one.
    if (error instanceof ApiError && error.code === 'operation_conflict' && body.action === 'closeTab') return { kind: 'note', note: { kind: 'error', text: LAST_TAB } };
    if (error instanceof ApiError && (error.code === 'operation_conflict' || error.code === 'session_missing')) {
      await reload();
      if (error.code === 'operation_conflict' && body.action === 'closeItem') return { kind: 'changed', text: CLOSE_CHANGED };
      return { kind: 'changed' };
    }
    return { kind: 'note', note: { kind: 'error', text: words(error) } };
  }
  if (answer.problem === 'uncommitted') return { kind: 'note', note: { kind: 'uncommitted' } };
  if (answer.targetId !== undefined) {
    // Also when the agent started and only the prompt was not sent (agentStarted is then true).
    const notice = startNotice({ problem: answer.agentProblem }, body.prompt !== undefined, 'tab');
    return { kind: 'open', targetId: answer.targetId, ...(notice === undefined ? {} : { notice }) };
  }
  await reload();
  // No terminal to say it over (this herdr cannot open the tab): say here what the agent did.
  const problem = startNotice({ problem: answer.agentProblem }, body.prompt !== undefined, 'tab');
  if (problem !== undefined) return { kind: 'note', note: { kind: 'error', text: problem } };
  if (body.action === 'newTab') return { kind: 'note', note: { kind: 'made', what: 'tab' } };
  if (body.action === 'openItem') return { kind: 'note', note: { kind: 'made', what: 'worktree' } };
  if (body.action === 'newWorkspace') return { kind: 'note', note: { kind: 'made', what: 'workspace' } };
  return { kind: 'done' };
}

/** Close tab names one of the item's terminals: its whole tab ends. */
export interface CloseTab { readonly closeTab: string }
/** New tab with an agent (section 14, B1): one of the provider's agent kinds, and the first prompt as typed. */
export interface AgentTab { readonly agent: string; readonly prompt: string }
export type ItemAction = 'newTab' | 'closeItem' | 'removeItem' | CloseTab | AgentTab;

/** Close and remove carry the item's name as the user confirmed it; Close tab carries the terminal; a blank prompt is left out, as in create. */
export function itemRequest(action: ItemAction, item: Pick<HTTPProviderItem, 'id' | 'name'>): HTTPProviderManageRequest {
  if (typeof action === 'object') {
    return 'closeTab' in action
      ? { action: 'closeTab', itemId: item.id, targetId: action.closeTab }
      : { action: 'newTab', itemId: item.id, agent: action.agent, ...(action.prompt.trim() === '' ? {} : { prompt: action.prompt }) };
  }
  return action === 'newTab' ? { action, itemId: item.id } : { action, itemId: item.id, name: item.name };
}

/** Open a closed worktree again (section 14, B2): the group and one of its `closed` names; never a path. */
export const openRequest = (group: Pick<HTTPProviderGroup, 'id'>, worktree: string): HTTPProviderManageRequest =>
  ({ action: 'openItem', groupId: group.id, worktree });

/** New tab asks what to start only where the computer can start an agent in a tab and has an agent kind; else it makes the tab at once. */
export const tabChooser = (entry: Pick<ProviderEntry, 'agentTab' | 'info'>): boolean => entry.agentTab && entry.info.agentKinds.length > 0;

/**
 * The action ends the terminal on screen (Live, B3): its workspace closed or removed, or its own tab closed.
 * ponytail: a tab is known by its label (the list has no tab ID); two tabs with one label count as one here.
 */
export function endsTerminal(action: ItemAction, item: Pick<HTTPProviderItem, 'targets'>, current: string): boolean {
  if (action === 'closeItem' || action === 'removeItem') return true;
  if (typeof action !== 'object' || !('closeTab' in action)) return false;
  const label = (id: string) => item.targets.find((target) => target.id === id)?.label;
  return action.closeTab === current || (label(current) !== undefined && label(action.closeTab) === label(current));
}

/** The most items a group lists; at this count the list may be cut, and a worktree may be out of sight. */
const GROUP_ITEMS_MAX = 64;

/**
 * A main checkout whose project has an open linked worktree: herdr would close it too (or refuse), so the worktrees go
 * first. The host's rule (docs/herdr-manage.md, section 13): also when the group is full, where a worktree may be cut off.
 */
export const closeWorktreesFirst = (group: Pick<HTTPProviderGroup, 'items'>, item: Pick<HTTPProviderItem, 'linked'>): boolean =>
  item.linked !== true && (group.items.length >= GROUP_ITEMS_MAX || group.items.some((other) => other.linked === true));

/**
 * A main checkout whose project has another workspace at the same main checkout, and no worktree open: herdr closes
 * them only together (0.8.2 closes both for one close; 0.9.3 refuses each), and a close from the phone ends one
 * workspace. The host's rule (docs/herdr-manage.md, section 13).
 */
export const closesTogether = (group: Pick<HTTPProviderGroup, 'items'>, item: Pick<HTTPProviderItem, 'linked'>): boolean =>
  item.linked !== true && group.items.length > 1 && !closeWorktreesFirst(group, item);

/** One action at a time per sheet, also when two taps land before the re-render; an answer after the sheet left does nothing. */
export function useAction<Busy>() {
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

export function NoteLine({ note, name, provider }: { readonly note: ManageNote; readonly name: string; readonly provider: string }) {
  switch (note.kind) {
    case 'uncommitted': return <Notice tone="warning" role="alert" title={<><bdi>{name}</bdi> has changes that are not committed. Commit or discard them first.</>} />;
    case 'made': return <Notice tone="neutral" role="status" title={<>The {note.what} is {note.what === 'worktree' ? 'open' : 'made'}. Update <bdi>{provider}</bdi> to open it from the phone.</>} />;
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

/** `newTab`: the chooser of New tab (Shell or an agent). */
export type ItemMenuMode = 'menu' | 'newTab' | 'close' | 'remove' | 'changed' | CloseTab;
/** The action that runs now. */
type ItemBusy = 'newTab' | 'closeItem' | 'removeItem' | 'closeTab';

/** The item menu and its confirmations. State comes from props, so each state renders on its own. */
export function ItemMenu({ entry, group, item, mode, busy, note, agent, dropped, checkingKinds = false, prompt = '', onMode, onAction, onAgent = () => undefined, onPrompt = () => undefined, onClose }: {
  readonly entry: Pick<ProviderEntry, 'computer' | 'info' | 'agentTab'>;
  readonly group: HTTPProviderGroup;
  readonly item: HTTPProviderItem;
  readonly mode: ItemMenuMode;
  readonly busy: ItemBusy | undefined;
  readonly note: ManageNote | undefined;
  /** The chooser: the agent kind picked (undefined: none yet) and its first prompt. */
  readonly agent?: string | undefined;
  /** droppedAgent's words. */
  readonly dropped?: string | undefined;
  readonly checkingKinds?: boolean;
  readonly prompt?: string;
  readonly onMode: (mode: ItemMenuMode) => void;
  readonly onAction: (action: ItemAction) => void;
  readonly onAgent?: (agent: string | undefined) => void;
  readonly onPrompt?: (prompt: string) => void;
  readonly onClose: () => void;
}) {
  const { info, computer } = entry;
  const off = busy !== undefined;
  const first = closeWorktreesFirst(group, item);
  const together = closesTogether(group, item);
  const closing = typeof mode === 'object' ? item.targets.find((target) => target.id === mode.closeTab) : undefined;
  // Focus stays in the sheet (Escape closes it) through every change: on the confirmation when one shows, never on its
  // danger button (Enter must not confirm), else on the menu; again when a busy action ends and its button comes back.
  const menu = useRef<HTMLDivElement>(null);
  const confirmation = useRef<HTMLDivElement>(null); // also the New tab chooser
  const shownAt = useRef(0); // the chooser's Shell
  useEffect(() => { shownAt.current = Date.now(); }, [mode]);
  useEffect(() => { if (!off) (confirmation.current ?? menu.current)?.focus(); }, [mode, off]);

  // Cancel is first, where the finger is after a tap on a menu row; the danger button takes no tap at first (ConfirmPair).
  const confirm = (question: ReactNode, action: ItemAction, label: string, busyLabel: string) => (
    <div ref={confirmation} role="alertdialog" aria-labelledby="manage-confirm" tabIndex={-1} className="confirm stack">
      <p id="manage-confirm">{question}</p>
      <ConfirmPair confirm={label} busy={off} busyLabel={busyLabel} onCancel={() => onMode('menu')} onConfirm={() => onAction(action)} />
    </div>
  );

  return (
    <div ref={menu} tabIndex={-1} className="stack item-menu-body">
      <Head
        id="item-menu-title"
        caption={<><bdi>{info.name}</bdi>{group.name !== item.name && <> · <bdi>{group.name}</bdi></>} · on {computer.label}</>}
        title={<bdi>{item.name}</bdi>}
        busy={off}
        onClose={onClose}
      />
      {/* An error note in this mode carries the changed words of that action (CLOSE_CHANGED). */}
      {mode === 'changed' && <Notice tone="danger" role="alert" title={note?.kind === 'error' ? note.text : CHANGED} />}
      {mode !== 'changed' && note !== undefined && <NoteLine note={note} name={item.name} provider={info.name} />}
      {mode === 'menu' && (
        <>
          <ul className="menu-list">
            <li>
              {tabChooser(entry)
                ? <button type="button" disabled={off} onClick={() => onMode('newTab')}>New tab</button>
                : <Button busy={busy === 'newTab'} busyLabel="Opening…" disabled={off} onClick={() => onAction('newTab')}>New tab</Button>}
            </li>
          </ul>
          <section className="tab-rows" aria-labelledby="item-tabs">
            <h3 id="item-tabs" className="caps">Tabs</h3>
            <ul>
              {item.targets.map((target) => (
                <li key={target.id}>
                  <span className="tab-row-name"><bdi>{target.label}</bdi>{target.agent !== undefined && <> · <bdi>{target.agent}</bdi></>}</span>
                  {item.targets.length > 1 && <button type="button" className="link danger-text" disabled={off} onClick={() => onMode({ closeTab: target.id })}>Close tab</button>}
                </li>
              ))}
            </ul>
            {item.targets.length < 2 && <p className="hint-text">{first ? WORKTREES_FIRST_TAB_HINT : together ? TOGETHER_TAB_HINT : LAST_TAB_HINT}</p>}
          </section>
          <ul className="menu-list">
            <li>
              <button type="button" className="danger" disabled={off || first || together} {...(first || together ? { 'aria-describedby': 'close-first' } : {})} onClick={() => onMode('close')}>Close workspace</button>
              {first && <p id="close-first" className="hint-text">Close its worktrees first.</p>}
              {together && <p id="close-first" className="hint-text"><bdi>{info.name}</bdi> closes the workspaces at this project's main folder only together. Close them in <bdi>{info.name}</bdi> on {computer.label}.</p>}
            </li>
            {item.linked === true && (
              <li><button type="button" className="danger" disabled={off} onClick={() => onMode('remove')}>Remove worktree</button></li>
            )}
          </ul>
        </>
      )}
      {mode === 'newTab' && (
        // Shell makes the tab at once, so it takes no tap at first either (a double tap on New tab lands here).
        <div ref={confirmation} role="group" aria-labelledby="new-tab-title" tabIndex={-1} className="stack new-tab">
          <p id="new-tab-title" className="caps">New tab</p>
          <div className="kind-chips">
            <Button busy={busy === 'newTab' && agent === undefined} busyLabel="Opening…" disabled={off} onClick={() => { if (confirmReady(shownAt.current, Date.now())) { onAgent(undefined); onAction('newTab'); } }}>Shell</Button>
            {info.agentKinds.map((kind) => (
              <button key={kind} type="button" aria-pressed={agent === kind} disabled={off} onClick={() => onAgent(kind)}><bdi>{kind}</bdi></button>
            ))}
          </div>
          {checkingKinds && <p role="status" className="hint-text">Checking available agents…</p>}
          {dropped !== undefined && <p role="status" className="hint-text">{dropped}</p>}
          {agent !== undefined && <PromptField prompt={prompt} busy={off} onPrompt={onPrompt} />}
          <div className="row confirm-pair">
            <button type="button" disabled={off} onClick={() => onMode('menu')}>Cancel</button>
            {agent !== undefined && (
              <Button className="primary" busy={busy === 'newTab'} busyLabel="Starting…" disabled={checkingKinds || promptProblem(prompt) !== undefined} onClick={() => onAction({ agent, prompt })}>Start</Button>
            )}
          </div>
        </div>
      )}
      {mode === 'close' && confirm(<>Close <bdi>{item.name}</bdi>? Its terminals end. Files stay.</>, 'closeItem', 'Close workspace', 'Closing…')}
      {mode === 'remove' && confirm(
        <>Remove the worktree <bdi>{item.name}</bdi>? Its folder is deleted, also files that git ignores (such as .env). The branch stays.</>,
        'removeItem', 'Remove worktree', 'Removing…',
      )}
      {typeof mode === 'object' && confirm(<>Close tab <bdi>{closing?.label}</bdi>? Its terminals end.</>, mode, 'Close tab', 'Closing…')}
    </div>
  );
}

/** The ⋯ of an item row (and of its terminal in Live), with its own state. `onOpened` opens a new tab's terminal; anything else stays here or closes. */
export function ItemMenuSheet({ entry, group, item, onOpened, onReload, onKinds, onFailure, onClose, onDone = onClose, manage = (body) => client(entry.computer.origin).providerManage(entry.info.id, body) }: {
  readonly entry: ProviderEntry;
  readonly group: HTTPProviderGroup;
  readonly item: HTTPProviderItem;
  /** `notice`: what did not start in a new tab with an agent; said once over its terminal. */
  readonly onOpened: (tap: ProviderTap, notice: string | undefined) => void;
  /** Reads this provider's list again; never rejects. */
  readonly onReload: () => Promise<void>;
  /** Reads that computer's settings again, quietly: `entry` then has the agent kinds it offers now. */
  readonly onKinds?: ReadAgentKinds | undefined;
  /** Words a failure of this computer (a peer's 401 says pair again; it never sends the user to Setup). */
  readonly onFailure: (error: unknown, from: Computer) => string;
  readonly onClose: () => void;
  /** A close or a remove is done (Live leaves a terminal that ended with it); absent: the sheet closes. */
  readonly onDone?: (action: ItemAction) => void;
  readonly manage?: Manage;
}) {
  const [mode, setMode] = useState<ItemMenuMode>('menu');
  const [note, setNote] = useState<ManageNote>();
  const [picked, setAgent] = useState<string>();
  const agent = offeredAgent(picked, entry.info.agentKinds);
  const [prompt, setPrompt] = useState('');
  const { busy, running, run } = useAction<ItemBusy>();
  // The kinds are from the app's last settings read; installed commands can change:
  // read them again when the menu opens (New tab asks only when there is a kind) and when the chooser opens.
  const chooser = mode === 'newTab';
  const checkingKinds = useAgentKinds(onKinds, chooser);
  const close = () => { if (!running.current) onClose(); };

  function act(action: ItemAction) {
    const agentTab = typeof action === 'object' && 'agent' in action;
    if (agentTab && (checkingKinds || promptProblem(action.prompt) !== undefined)) return;
    setNote(undefined);
    run(typeof action !== 'object' ? action : agentTab ? 'newTab' : 'closeTab', () => manageStep(itemRequest(action, item), manage, onReload, (error) => onFailure(error, entry.computer)), (step) => {
      switch (step.kind) {
        case 'open': onOpened({ computer: entry.computer, provider: entry.info.id, targetId: step.targetId }, step.notice); break;
        case 'done': onDone(action); break;
        case 'changed': if (step.text !== undefined) setNote({ kind: 'error', text: step.text }); setMode('changed'); break;
        // A refused agent tab stays in the chooser, with its prompt.
        case 'note': setNote(step.note); setMode(agentTab ? 'newTab' : 'menu'); break;
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
          checkingKinds={checkingKinds}
          agent={agent}
          dropped={droppedAgent(picked, entry.info.agentKinds, entry.info.name)}
          prompt={prompt}
          onMode={(next) => { setNote(undefined); setMode(next); }}
          onAction={act}
          onAgent={setAgent}
          onPrompt={setPrompt}
          onClose={close}
        />
      </Sheet>
    </div>
  );
}

/** New workspace: the computer's Termivoa projects, or how to add one, as New session says. */
/** Add a folder's own busy key: no project has this ID. */
export const ADDING = 'add-folder';
export const NO_SESSION = 'A terminal could not be opened there. Open a session on that computer and run `pterm project add <folder>`.';

export function WorkspaceList({ entry, settings, busy, checking, note, onPick, onCheck, onAddFolder, onFromGitHub, onClose }: {
  readonly entry: Pick<ProviderEntry, 'computer' | 'info'>;
  /** The settings of that computer: its projects are the ones New session lists. */
  readonly settings: HTTPSettingsResponse;
  /** The project whose workspace is being made. */
  readonly busy: string | undefined;
  readonly checking: boolean;
  readonly note: ManageNote | undefined;
  readonly onPick: (projectId: string) => void;
  readonly onCheck: () => void;
  /** Opens a session with `pterm project add ` typed; absent: not offered. */
  readonly onAddFolder?: (() => void) | undefined;
  /** Opens the GitHub repository list of that computer; shown only where `settings` offers GitHub (githubShown). */
  readonly onFromGitHub?: (() => void) | undefined;
  readonly onClose: () => void;
}) {
  const { info, computer } = entry;
  const off = busy !== undefined;
  const fromGitHub = onFromGitHub === undefined || !githubShown(settings) ? undefined : <button type="button" className="link accent" disabled={off} onClick={onFromGitHub}>From GitHub</button>;
  return (
    <div className="stack">
      <Head id="workspace-title" caption={<><bdi>{info.name}</bdi> · on {computer.label}</>} title="New workspace" busy={off} onClose={onClose} />
      {note !== undefined && <NoteLine note={note} name="" provider={info.name} />}
      {settings.projects.length === 0 ? (
        <>
          <NewSessionHelp settings={settings} />
          <Button className="secondary" busy={checking} busyLabel="Checking…" onClick={onCheck}>Check again</Button>
          {fromGitHub}
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
          {onAddFolder !== undefined && (
            <>
              <p className="hint-text">Another folder? This opens a terminal on {computer.label} with <code>pterm project add</code> typed. Type the folder, press Enter, then tap Check again here.</p>
              <div className="sheet-actions">
                <Button className="secondary" busy={busy === ADDING} busyLabel="Opening…" disabled={off} onClick={onAddFolder}>Add a folder</Button>
                <Button className="secondary" busy={checking} busyLabel="Checking…" disabled={off} onClick={onCheck}>Check again</Button>
                {fromGitHub}
              </div>
            </>
          )}
          {onAddFolder === undefined && fromGitHub}
        </>
      )}
    </div>
  );
}

/** Add a folder, start to end: false from `open` (no project or shell there) and a failure both become words. */
export async function addFolderStep(open: () => Promise<boolean>, words: (error: unknown) => string): Promise<ManageStep> {
  try {
    return await open() ? { kind: 'done' } : { kind: 'note', note: { kind: 'error', text: NO_SESSION } };
  } catch (error) {
    return { kind: 'note', note: { kind: 'error', text: words(error) } };
  }
}

export function WorkspaceSheet({ entry, settings, onOpened, onReload, onCheck, onAddFolder, onFromGitHub, onFailure, onClose, manage = (body) => client(entry.computer.origin).providerManage(entry.info.id, body) }: {
  readonly entry: ProviderEntry;
  readonly settings: HTTPSettingsResponse;
  readonly onOpened: (tap: ProviderTap) => void;
  /** Reads this provider's list again; never rejects. */
  readonly onReload: () => Promise<void>;
  /** Reads that computer again, settings included (Check again). */
  readonly onCheck: () => Promise<void>;
  /** Starts a session on that computer and goes to Live with the command typed; false: it has no project or shell. */
  readonly onAddFolder?: (() => Promise<boolean>) | undefined;
  /** Opens the GitHub repository list instead; shown only where `settings` offers GitHub. */
  readonly onFromGitHub?: (() => void) | undefined;
  readonly onFailure: (error: unknown, from: Computer) => string;
  readonly onClose: () => void;
  readonly manage?: Manage;
}) {
  const [note, setNote] = useState<ManageNote>();
  const [checking, setChecking] = useState(false);
  const { busy, running, run } = useAction<string>();
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

  function addFolder(open: () => Promise<boolean>) {
    setNote(undefined);
    run(ADDING, () => addFolderStep(open, words), (step) => { if (step.kind === 'note') setNote(step.note); });
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
        <WorkspaceList entry={entry} settings={settings} busy={busy} checking={checking} note={note} onPick={pick} onCheck={check} onAddFolder={onAddFolder === undefined ? undefined : () => addFolder(onAddFolder)} onFromGitHub={onFromGitHub} onClose={close} />
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
