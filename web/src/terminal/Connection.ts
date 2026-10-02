import type {
  AgentStatus,
  BranchesListedPayload,
  ChangesCommittedPayload,
  ChangesPatchPayload,
  ChangesPushedPayload,
  ChangesSummaryPayload,
  ErrorCode,
  FilesContentPayload,
  FilesFoundPayload,
  FilesListedPayload,
  HistoryChangedPayload,
  HistoryPagePayload,
  InputMousePayload,
  KeyName,
  Modifiers,
  PreviewPort,
  ScreenResyncPayload,
  State as SessionLifecycle,
  TasksListedPayload,
  ThumbnailMode,
  WorktreeRemovedPayload,
} from '../generated/protocol';
import { CLIENT_BUILD, PEER_REQUEST_FAILED } from '../api/http';
import { parseHostMessage, type ValidatedHostMessage } from '../protocol/validate';
import {
  ResyncRequiredError,
  type ChangedRow,
  type ScreenMetadata,
  type ScreenPatch,
  type ScreenSnapshot,
  type TerminalRow,
} from './FrameRenderer';
import { DeliveryUncertainError, InputSender } from './InputSender';
import type { ConnectGate } from './scheduler';
import { SnapshotAssembler } from './SnapshotAssembler';

export const DELIVERY_UNCERTAIN = 'Delivery uncertain—check the terminal before sending again.';
/** A commit or push was sent but its reply can no longer arrive. It is never resent. */
export const WRITE_LOST = 'Connection lost — check the Changes list before trying again.';
/** A worktree remove was sent but its reply can no longer arrive. It is never resent. */
export const REMOVE_LOST = 'Connection lost — check the session list before trying again.';
export const HEARTBEAT_MS = 5000;
export const RECONNECT_DELAYS_MS = [250, 500, 1000, 2000, 3000, 5000] as const;
/**
 * Back after at least this long hidden, a live socket may be half-open (iOS
 * sleep, network switch): it looks open and delivers nothing. The gateway's
 * WebSocket pings are invisible to JS and heartbeat has no reply, so the
 * check is to reopen the socket. Live already releases control when hidden,
 * so reattaching as an observer costs the user nothing.
 */
export const RESUME_CHECK_MS = 10_000;

/**
 * After a long hide the socket is checked, not replaced: it asks for the screen
 * again and waits this long for any host message. A live socket answers well
 * inside it; silence means the socket is half-open, and only then it reconnects.
 */
export const RESUME_PROBE_MS = 2_000;

/**
 * A new socket that is not live by then is dropped and retried. Right after a
 * phone wakes, its network can take a moment to come back, and a connect
 * started in that moment can otherwise hang for a minute or more.
 */
export const ATTACH_TIMEOUT_MS = 5_000;

/**
 * A files request with no reply by then counts as failed (Retry), so a lost
 * reply does not hold back the next request until a reconnect.
 */
export const FILES_TIMEOUT_MS = 15_000;

/** WebSocket.OPEN, without depending on the global in tests. */
const SOCKET_OPEN = 1;
const HISTORY_PAGE_ROWS = 200;
const MAX_HISTORY_PAGES = 25;
// Continuous output bumps the history revision on every scrolled line.
const MAX_HISTORY_RESTARTS = 5;
const OPEN = 1;
const FRAME_FALLBACK_MS = 100;

export type LinkState = 'connecting' | 'attaching' | 'live' | 'reconnecting' | 'lost' | 'revoked' | 'closed';
export type ControlState = 'observer' | 'acquiring' | 'controller';

export interface ConnectionStatus {
  readonly link: LinkState;
  readonly control: ControlState;
  /** True when another connection currently holds control. */
  readonly otherController: boolean;
  readonly controllerLabel: string | undefined;
  readonly sessionState: SessionLifecycle | undefined;
  readonly exitCode: number | undefined;
  readonly buffer: 'normal' | 'alternate';
  readonly columns: number | undefined;
  readonly rows: number | undefined;
  readonly notice: string | undefined;
  readonly hostBuild: string | undefined;
  /** The running agent's status. Question text is terminal output: render it as text only. */
  readonly agent: AgentStatus | undefined;
  /** Ports this session's processes listen on; undefined when none. */
  readonly previews: ReadonlyArray<PreviewPort> | undefined;
  /** The session's live working folder. Display as text only; undefined when unknown. */
  readonly cwd: string | undefined;
  /** The one answer this phone sent for the shown question. */
  readonly answer: AnswerState | undefined;
}

export interface AnswerState {
  readonly questionId: string;
  readonly optionIndex: number;
  /** sent: accepted, waiting for the next session.state. changed: refused as question_changed. */
  readonly phase: 'pending' | 'sent' | 'changed';
}

export interface HistoryView {
  readonly state: 'loading' | 'ready' | 'unavailable';
  /** Retained normal history rows followed by the frozen current screen. */
  readonly lines: ReadonlyArray<string>;
  readonly historyRows: number;
  /** Live output advanced after this snapshot was frozen. */
  readonly newOutput: boolean;
}

export interface ChangesView {
  /** The summary request; a refresh keeps the last summary while it loads. */
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly summary: ChangesSummaryPayload | undefined;
  /** The open file; patch is set once state is ready. */
  readonly file: ChangesFileView | undefined;
}

export interface ChangesFileView {
  readonly path: string;
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly patch: ChangesPatchPayload | undefined;
}

/**
 * The Files view: one folder, the open file and a search, each with the reply
 * to its latest request. Read-only; any viewer may read.
 */
export interface FilesView {
  /** The files root, absolute with "/" separators, once a listing named it. */
  readonly root: string | undefined;
  readonly folder: FilesFolderView;
  readonly file: FilesFileView | undefined;
  readonly search: FilesSearchView | undefined;
}

/** A folder by its root-relative path ("" is the root); listed is set once state is ready. */
export interface FilesFolderView {
  readonly path: string;
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly listed: FilesListedPayload | undefined;
}

/** A file by its root-relative path; content is set once state is ready. */
export interface FilesFileView {
  readonly path: string;
  /** The 1-based line to show, from a tapped "path:line" in the terminal. */
  readonly line: number | undefined;
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly content: FilesContentPayload | undefined;
}

/** A path search; found is set once state is ready. */
export interface FilesSearchView {
  readonly query: string;
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly found: FilesFoundPayload | undefined;
}

/** The one files request in flight, by what it asked for. */
interface FilesRequest {
  readonly id: string;
  readonly kind: 'list' | 'read' | 'search';
  /** The path, or the query for a search. */
  readonly key: string;
  /** The session's cwd when it was sent; the host finds the files root from it. */
  readonly cwd: string | undefined;
}

/** The project's commands for the Snips sheet; listed is set once state is ready. */
export interface TasksView {
  /** unavailable: not sent, refused, or the connection closed before the reply. */
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly listed: TasksListedPayload | undefined;
}

/** The repository's branches for the New worktree sheet; listed is set once state is ready. */
export interface BranchesView {
  /** unavailable: not sent, refused, or the connection closed before the reply. */
  readonly state: 'loading' | 'ready' | 'unavailable';
  readonly listed: BranchesListedPayload | undefined;
}

/** The one worktree remove in flight, then its result until the next one. */
export interface WorktreeRemoval {
  /** error: the host refused it, or the connection closed before the reply. */
  readonly state: 'pending' | 'done' | 'error';
  readonly removed?: WorktreeRemovedPayload;
  readonly message?: string;
}

/** The one commit or push in flight, then its result until dismissed. */
export interface ChangesWrite {
  readonly kind: 'commit' | 'push';
  /** error: the host refused it, or the connection closed before the reply. */
  readonly state: 'pending' | 'done' | 'error';
  readonly committed?: ChangesCommittedPayload;
  readonly pushed?: ChangesPushedPayload;
  readonly message?: string;
}

/** Plain words for the refusals a commit or push can get; others use the host's text. */
const WRITE_ERRORS: Readonly<Record<string, string>> = {
  not_controller: 'You no longer have control. Take control, then try again.',
  stale_lease: 'You no longer have control. Take control, then try again.',
  limit_exceeded: 'That is more than the computer allows. Select fewer files or shorten the message.',
  operation_conflict: 'Another commit, push or worktree remove is running. Wait for it, then try again.',
};

/**
 * A peer's own error text is never shown: it could be phishing inside this
 * app. Each code gets the app's own words instead.
 */
const PEER_ERRORS: Readonly<Record<ErrorCode, string>> = {
  unauthorized: 'This phone is no longer paired with that computer.',
  revoked: 'This phone is no longer paired with that computer.',
  pairing_expired: 'The pairing expired.',
  pairing_pending: 'The pairing is not approved yet.',
  protocol_mismatch: 'That computer runs another version of Termivoa. Update Termivoa on it.',
  session_missing: 'That session no longer exists.',
  generation_changed: 'That session was replaced.',
  session_lost: 'That session was lost.',
  not_controller: 'You no longer have control. Take control, then try again.',
  stale_lease: 'You no longer have control. Take control, then try again.',
  input_gap: 'Some input did not arrive. Take control, then try again.',
  limit_exceeded: 'That is more than the computer allows.',
  history_changed: 'The history changed. Open it again.',
  resync_required: 'The screen is out of date. It loads again now.',
  host_unavailable: 'That computer is busy. Try again.',
  internal_error: 'That computer had an internal error.',
  invalid_request: 'That computer refused the request.',
  operation_conflict: 'Another operation is running. Wait for it, then try again.',
  project_missing: 'That project is no longer on that computer.',
  shell_profile_missing: 'That shell is no longer on that computer.',
  question_changed: 'The question changed. Read it again.',
  preview_missing: 'That preview is gone.',
  worktree_exists: 'That worktree already exists.',
  not_a_repository: 'That folder is not in a git repository.',
  worktree_failed: 'git could not do this on that computer.',
};

/** The app's own words for a peer's error code. */
export function peerErrorText(code: string): string {
  return (PEER_ERRORS as Readonly<Record<string, string | undefined>>)[code] ?? PEER_REQUEST_FAILED;
}

/** FrameRenderer satisfies this; tests pass a fake. */
export interface ScreenDisplay {
  paintSnapshot(snapshot: ScreenSnapshot): Promise<void>;
  applyPatch(patch: ScreenPatch): Promise<void>;
}

export interface ConnectionOptions {
  /** The computer's live socket, `websocketUrl(origin)`; any computer's origin works. */
  readonly url: string;
  readonly sessionId: string;
  readonly generation: string;
  readonly display: ScreenDisplay;
  readonly createSocket?: (url: string) => WebSocket;
  readonly requestFrame?: (callback: () => void) => void;
  /**
   * Attach as a read-only thumbnail viewer whose screen frames the broker
   * sends at most this often (Wall tiles). Pass it only when
   * `client(origin).supports('thumbnail')`; an older computer would refuse
   * the attach. Take control and input never work in this mode.
   */
  readonly thumbnail?: ThumbnailMode;
  /** Decoded size of every host message, for the scheduler's bytes budget (TileHandle.bytes). */
  readonly onMessageBytes?: (bytes: number) => void;
  /** Admits each socket attempt (scheduler.connectGate); without it, attempts start at once as before. */
  readonly gate?: ConnectGate;
  /** True for a computer other than home: its error text is replaced by the app's own words (peerErrorText). */
  readonly peer?: boolean;
}

interface PendingSnapshot {
  readonly snapshotId: string;
  readonly snapshot: ScreenSnapshot;
}

interface HistoryState {
  epoch: string;
  revision: string;
  expectedOffset: number;
  pages: number;
  restarts: number;
  advanced: boolean;
  history: string[];
  screen: string[];
  view: HistoryView;
}

interface ChangesState {
  /** Only the reply to the latest request counts; older replies are stale. */
  listRequest: string | undefined;
  fileRequests: Map<string, string>;
  /** Patches by path, kept until refresh. */
  patches: Map<string, ChangesPatchPayload>;
  view: ChangesView;
}

export const INITIAL_STATUS: ConnectionStatus = {
  link: 'connecting',
  control: 'observer',
  otherController: false,
  controllerLabel: undefined,
  sessionState: undefined,
  exitCode: undefined,
  buffer: 'normal',
  columns: undefined,
  rows: undefined,
  notice: undefined,
  hostBuild: undefined,
  agent: undefined,
  previews: undefined,
  cwd: undefined,
  answer: undefined,
};

/**
 * Owns one session's WebSocket, reconnect state machine, frame application,
 * controller lease, and history reads. React only reads the small status
 * object; screen rows never enter React state.
 *
 * Reconnect contract: stop input, reconnect with backoff, hello, attach as an
 * observer, paint a validated snapshot, acknowledge it, apply patches, and
 * only then offer Take control. Control is never re-acquired automatically
 * and input is never replayed.
 */
export class Connection {
  private status: ConnectionStatus = INITIAL_STATUS;
  private readonly listeners = new Set<() => void>();
  private socket: WebSocket | undefined;
  private socketEpoch = 0;
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private heartbeatTimer: ReturnType<typeof setInterval> | undefined;
  private brokerGeneration: string | undefined;
  private sender: InputSender | undefined;
  private lease: string | undefined;
  private answerSequence: string | undefined;
  private readonly assembler = new SnapshotAssembler();
  private pendingSnapshot: PendingSnapshot | undefined;
  private pendingPatch: ScreenPatch | undefined;
  private frameScheduled = false;
  private screen: ScreenMetadata | undefined;
  private screenRows: TerminalRow[] = [];
  private history: HistoryState | undefined;
  private changes: ChangesState | undefined;
  private write: ChangesWrite | undefined;
  private tasks: TasksView | undefined;
  /** The request ID of the latest tasks.list; only its reply counts. */
  private tasksRequest: string | undefined;
  /** The request ID of the pending write; only its reply counts. */
  private writeRequest: string | undefined;
  private branches: BranchesView | undefined;
  /** The request ID of the latest branches.list; only its reply counts. */
  private branchesRequest: string | undefined;
  private removal: WorktreeRemoval | undefined;
  private files: FilesView | undefined;
  /**
   * The host takes one files request per viewer at a time, so the next one is
   * sent when its reply comes: whatever the view then still waits for.
   */
  private filesRequest: FilesRequest | undefined;
  private filesTimer: ReturnType<typeof setTimeout> | undefined;
  /** The last root a listing named, for the cwd the session had then. */
  private lastRoot: { readonly root: string; readonly cwd: string | undefined } | undefined;
  private rootWaiters: Array<(root: string | undefined) => void> = [];
  /** The request ID of the pending worktree.remove; only its reply counts. */
  private removalRequest: string | undefined;
  private stopped = false;
  private requestedSize: string | undefined;
  private hiddenAt: number | undefined;
  private probeTimer: ReturnType<typeof setTimeout> | undefined;
  private attachTimer: ReturnType<typeof setTimeout> | undefined;
  /** Cancels an attempt still waiting at the gate. */
  private gateCancel: (() => void) | undefined;
  /** An attempt the gate admitted that has not gone live or failed yet. */
  private gateHeld = false;

  private readonly createSocket: (url: string) => WebSocket;
  private readonly requestFrame: (callback: () => void) => void;

  constructor(private readonly options: ConnectionOptions) {
    this.createSocket = options.createSocket ?? ((url) => new WebSocket(url));
    this.requestFrame = options.requestFrame ?? ((callback) => { requestAnimationFrame(() => callback()); });
  }

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  readonly getStatus = (): ConnectionStatus => this.status;

  readonly getHistory = (): HistoryView | undefined => this.history?.view;

  readonly getChanges = (): ChangesView | undefined => this.changes?.view;

  readonly getChangesWrite = (): ChangesWrite | undefined => this.write;

  readonly getTasks = (): TasksView | undefined => this.tasks;

  readonly getBranches = (): BranchesView | undefined => this.branches;

  readonly getWorktreeRemoval = (): WorktreeRemoval | undefined => this.removal;

  readonly getFiles = (): FilesView | undefined => this.files;

  get sessionId(): string {
    return this.options.sessionId;
  }

  get generation(): string {
    return this.options.generation;
  }

  canSend(): boolean {
    return canSendWith(this.status) && this.sender !== undefined;
  }

  start(): void {
    this.stopped = false;
    this.listen(true);
    this.open();
  }

  /** Stop viewing without ending the process. */
  detach(): void {
    if (this.stopped) {
      return;
    }
    this.releaseControl();
    this.sendSession('session.detach', {});
    this.stop('closed');
  }

  takeControl(): void {
    if (this.options.thumbnail !== undefined || this.status.link !== 'live' || this.status.control !== 'observer') {
      return;
    }
    this.update({ control: 'acquiring', notice: undefined });
    this.sendSession('control.acquire', {});
  }

  releaseControl(): void {
    const lease = this.lease;
    if (lease === undefined) {
      return;
    }
    this.sendSession('control.release', { lease });
    this.dropControl(undefined);
  }

  dismissNotice(): void {
    this.update({ notice: undefined });
  }

  sendKey(key: KeyName, modifiers: Modifiers, character?: string): boolean {
    return this.withSender((sender) => sender.sendKey(key, modifiers, character));
  }

  sendText(text: string): boolean {
    return this.withSender((sender) => sender.sendText(text));
  }

  sendPaste(text: string): boolean {
    return this.withSender((sender) => sender.sendPaste(text));
  }

  /** Sends one answer for one tap. A second answer waits until the first is acknowledged. */
  sendAnswer(questionId: string, optionIndex: number): boolean {
    if (this.status.answer?.phase === 'pending') {
      return false;
    }
    let sequence: string | undefined;
    if (!this.withSender((sender) => { sequence = sender.sendAnswer(questionId, optionIndex); })) {
      return false;
    }
    this.answerSequence = sequence;
    this.update({ answer: { questionId, optionIndex, phase: 'pending' } });
    return true;
  }

  sendWheel(button: 'wheel-up' | 'wheel-down'): boolean {
    const screen = this.screen;
    if (screen === undefined) {
      return false;
    }
    const kind: InputMousePayload['kind'] = 'wheel';
    return this.withSender((sender) => sender.sendMouse(
      kind,
      button,
      screen.cursor.column,
      screen.cursor.row,
      { shift: false, alt: false, ctrl: false, meta: false },
    ));
  }

  /** Controller-only PTY resize. Bounds match the protocol limits. */
  resize(columns: number, rows: number): boolean {
    const lease = this.lease;
    if (!this.canSend() || lease === undefined) {
      return false;
    }
    const size = clampGrid(columns, rows);
    const key = `${size.columns}x${size.rows}`;
    if ((this.screen?.columns === size.columns && this.screen.rows === size.rows) || this.requestedSize === key) {
      return false;
    }
    // The resize snapshot clears this; until then an identical request is redundant.
    this.requestedSize = key;
    this.sendSession('terminal.resize', { lease, ...size });
    return true;
  }

  openHistory(): boolean {
    const screen = this.screen;
    if (this.status.link !== 'live' || screen === undefined) {
      return false;
    }
    this.history = {
      epoch: screen.historyEpoch,
      revision: screen.historyRevision,
      expectedOffset: 0,
      pages: 0,
      restarts: 0,
      advanced: false,
      history: [],
      screen: this.screenRows.map(rowText),
      view: { state: 'loading', lines: [], historyRows: 0, newOutput: false },
    };
    if (screen.buffer === 'alternate') {
      this.finishHistory();
    } else {
      this.readHistory(0);
    }
    this.emit();
    return true;
  }

  closeHistory(): void {
    this.history = undefined;
    this.emit();
  }

  /** Reads the uncommitted changes. Any viewer may read; no lease is needed. */
  openChanges(): boolean {
    if (this.status.link !== 'live') {
      return false;
    }
    this.changes = {
      listRequest: undefined,
      fileRequests: new Map(),
      patches: new Map(),
      view: { state: 'loading', summary: undefined, file: undefined },
    };
    this.readChanges();
    this.emit();
    return true;
  }

  /** Reads the list again, and the open file's diff; cached diffs are dropped. */
  refreshChanges(): void {
    const changes = this.changes;
    if (changes === undefined) {
      return;
    }
    changes.patches.clear();
    changes.fileRequests.clear();
    const path = changes.view.file?.path;
    changes.view = { ...changes.view, state: 'loading' };
    this.readChanges();
    if (path === undefined) {
      this.emit();
    } else {
      this.openChangesFile(path);
    }
  }

  /** Shows one file's diff, from the cache when it was read since the last refresh. */
  openChangesFile(path: string): void {
    const changes = this.changes;
    if (changes === undefined) {
      return;
    }
    const patch = changes.patches.get(path);
    if (patch !== undefined) {
      changes.view = { ...changes.view, file: { path, state: 'ready', patch } };
    } else {
      const requestId = this.sendSession('changes.file', { path });
      if (requestId !== undefined) {
        changes.fileRequests.set(requestId, path);
      }
      changes.view = {
        ...changes.view,
        file: { path, state: requestId === undefined ? 'unavailable' : 'loading', patch: undefined },
      };
    }
    this.emit();
  }

  closeChangesFile(): void {
    const changes = this.changes;
    if (changes?.view.file === undefined) {
      return;
    }
    changes.view = { ...changes.view, file: undefined };
    this.emit();
  }

  closeChanges(): void {
    this.changes = undefined;
    this.emit();
  }

  /** Lists one folder of the files root ("" is the root). Any viewer may read; no lease is needed. */
  openFilesFolder(path: string): boolean {
    if (this.status.link !== 'live') {
      return false;
    }
    this.files = {
      root: this.files?.root ?? this.knownFilesRoot(),
      folder: { path, state: 'loading', listed: undefined },
      file: undefined,
      search: this.files?.search,
    };
    this.pumpFiles();
    this.emit();
    return true;
  }

  /** Reads one file; without an open Files view, its folder is listed too, for Back. */
  openFilesFile(path: string, line?: number): boolean {
    if (this.status.link !== 'live') {
      return false;
    }
    const files = this.files ?? {
      root: this.knownFilesRoot(),
      folder: { path: path.slice(0, Math.max(0, path.lastIndexOf('/'))), state: 'loading', listed: undefined },
      file: undefined,
      search: undefined,
    };
    this.files = { ...files, file: { path, line, state: 'loading', content: undefined } };
    this.pumpFiles();
    this.emit();
    return true;
  }

  closeFilesFile(): void {
    if (this.files?.file === undefined) {
      return;
    }
    this.files = { ...this.files, file: undefined };
    this.emit();
  }

  /** Searches paths in the files root; an empty query ends the search. */
  searchFiles(query: string): void {
    const files = this.files;
    if (files === undefined) {
      return;
    }
    this.files = { ...files, search: query === '' ? undefined : { query, state: 'loading', found: undefined } };
    this.pumpFiles();
    this.emit();
  }

  closeFiles(): void {
    this.files = undefined;
    this.emit();
  }

  /**
   * The files root for the session's current cwd: known from the last listing,
   * else asked for with a listing of the root. Undefined when it cannot be read.
   */
  filesRoot(): Promise<string | undefined> {
    const known = this.knownFilesRoot();
    if (known !== undefined || this.status.link !== 'live') {
      return Promise.resolve(known);
    }
    return new Promise((resolve) => {
      this.rootWaiters.push(resolve);
      this.pumpFiles();
    });
  }

  /** Controller only. Commits these paths from the latest list; the repository's hooks run. */
  commitChanges(message: string, paths: ReadonlyArray<string>): boolean {
    return paths.length > 0 && this.sendWrite('commit', 'changes.commit', (lease) => ({ lease, message, paths }));
  }

  /** Controller only. Pushes the current branch; the host never forces. */
  pushChanges(): boolean {
    return this.sendWrite('push', 'changes.push', (lease) => ({ lease }));
  }

  /**
   * Reads the commands the project defines, once per call. Any viewer may ask;
   * no lease is needed and nothing runs on the host.
   */
  listTasks(): boolean {
    this.tasksRequest = this.status.link === 'live' ? this.sendSession('tasks.list', {}) : undefined;
    this.tasks = { state: this.tasksRequest === undefined ? 'unavailable' : 'loading', listed: undefined };
    this.emit();
    return this.tasksRequest !== undefined;
  }

  /**
   * Reads the branches of the session folder's repository, once per call, to
   * pick the base of a new worktree. Any viewer may ask; no lease is needed.
   */
  listBranches(): boolean {
    this.branchesRequest = this.status.link === 'live' ? this.sendSession('branches.list', {}) : undefined;
    this.branches = { state: this.branchesRequest === undefined ? 'unavailable' : 'loading', listed: undefined };
    this.emit();
    return this.branchesRequest !== undefined;
  }

  /**
   * Controller only. Removes the Termivoa worktree this session started in;
   * the host keeps the branch and, on success, ends the session. One at a
   * time, with the current lease; never queued or resent.
   */
  removeWorktree(force: boolean): boolean {
    const lease = this.lease;
    if (this.removalRequest !== undefined || !canSendWith(this.status) || lease === undefined) {
      return false;
    }
    const requestId = this.sendSession('worktree.remove', { lease, force });
    if (requestId === undefined) {
      return false;
    }
    this.removalRequest = requestId;
    this.removal = { state: 'pending' };
    this.emit();
    return true;
  }

  /** Clears a shown result. A pending write stays until its reply or a lost connection. */
  dismissChangesWrite(): void {
    if (this.write === undefined || this.write.state === 'pending') {
      return;
    }
    this.write = undefined;
    this.emit();
  }

  private readonly onVisibility = (): void => {
    if (document.visibilityState === 'hidden') {
      this.hiddenAt ??= Date.now();
      return;
    }
    const hiddenFor = this.hiddenAt === undefined ? 0 : Date.now() - this.hiddenAt;
    this.hiddenAt = undefined;
    if (hiddenFor > RESUME_CHECK_MS) {
      this.verify();
    } else {
      this.resume(false);
    }
  };

  private readonly onPageShow = (event: PageTransitionEvent): void => this.resume(event.persisted);

  // Offline to online means the old network path is likely gone.
  private readonly onOnline = (): void => this.resume(true);

  private listen(on: boolean): void {
    if (typeof window === 'undefined' || typeof document === 'undefined') {
      return;
    }
    if (on) {
      document.addEventListener('visibilitychange', this.onVisibility);
      window.addEventListener('pageshow', this.onPageShow);
      window.addEventListener('online', this.onOnline);
    } else {
      document.removeEventListener('visibilitychange', this.onVisibility);
      window.removeEventListener('pageshow', this.onPageShow);
      window.removeEventListener('online', this.onOnline);
    }
  }

  /**
   * The page or network is back: skip a pending (possibly frozen) retry and
   * connect now. With `verify`, a live socket is replaced too, since it may be
   * half-open. Either way the new socket attaches as an observer; input is
   * never resent.
   */
  private resume(verify: boolean): void {
    const { link } = this.status;
    if (this.stopped || !(link === 'reconnecting' || (verify && link === 'live'))) {
      return;
    }
    const socket = this.socket;
    if (socket !== undefined) {
      // Same cleanup as a real close (stops input, drops control); the old
      // socket's own close event is then stale.
      this.handleClose(this.socketEpoch);
      socket.close(4002, 'reconnect');
    }
    this.open();
    this.reconnectAttempt = 0;
  }

  /** Keeps a socket that still answers; replaces one that stays silent. */
  private verify(): void {
    const socket = this.socket;
    if (this.stopped || this.status.link !== 'live' || socket === undefined || socket.readyState !== SOCKET_OPEN) {
      this.resume(true);
      return;
    }
    this.clearProbe();
    this.probeTimer = setTimeout(() => {
      this.probeTimer = undefined;
      this.resume(true);
    }, RESUME_PROBE_MS);
    // The broker answers any resync with a full snapshot; the reason is informational.
    this.requestResync('initial');
  }

  private clearProbe(): void {
    clearTimeout(this.probeTimer);
    this.probeTimer = undefined;
  }

  private open(): void {
    this.clearReconnect();
    this.cancelGate();
    const gate = this.options.gate;
    if (gate === undefined) {
      this.connect();
      return;
    }
    this.update({ link: this.reconnectAttempt === 0 ? 'connecting' : 'reconnecting' });
    this.gateCancel = gate.wait(() => {
      this.gateCancel = undefined;
      this.gateHeld = true;
      this.connect();
    });
  }

  private cancelGate(): void {
    this.gateCancel?.();
    this.gateCancel = undefined;
  }

  /** Tells the gate how the admitted attempt ended, once. */
  private settleGate(result: 'live' | 'failed' | 'cancelled'): void {
    if (this.gateHeld) {
      this.gateHeld = false;
      this.options.gate?.done(result);
    }
  }

  private connect(): void {
    const epoch = ++this.socketEpoch;
    clearTimeout(this.attachTimer);
    this.attachTimer = setTimeout(() => {
      if (epoch === this.socketEpoch && this.status.link !== 'live') {
        const stuck = this.socket;
        this.handleClose(epoch);
        stuck?.close(4003, 'attach timeout');
      }
    }, ATTACH_TIMEOUT_MS);
    let socket: WebSocket;
    try {
      socket = this.createSocket(this.options.url);
    } catch {
      this.handleClose(epoch);
      return;
    }
    this.socket = socket;
    this.update({ link: this.reconnectAttempt === 0 ? 'connecting' : 'reconnecting' });
    socket.onopen = () => {
      if (epoch !== this.socketEpoch) {
        return;
      }
      this.send({
        type: 'hello',
        payload: {
          clientBuild: CLIENT_BUILD,
          clientKind: 'browser',
          supportedVersions: [1],
          capabilityProfile: 'phone-terminal-v1',
        },
      });
    };
    socket.onmessage = (event: MessageEvent) => {
      if (epoch === this.socketEpoch) {
        this.clearProbe();
        this.options.onMessageBytes?.(typeof event.data === 'string' ? event.data.length : 0);
        this.handleMessage(event.data);
      }
    };
    socket.onclose = () => this.handleClose(epoch);
    socket.onerror = () => undefined;
  }

  private handleMessage(data: unknown): void {
    let message: ValidatedHostMessage;
    try {
      message = parseHostMessage(typeof data === 'string' ? data : '');
    } catch {
      // An invalid host message never reaches display state; reconnect cleanly.
      this.socket?.close(4000, 'invalid message');
      return;
    }
    switch (message.type) {
      case 'welcome': {
        const previous = this.brokerGeneration;
        if (previous !== undefined && previous !== message.payload.brokerGeneration) {
          this.lose();
          return;
        }
        this.brokerGeneration = message.payload.brokerGeneration;
        this.update({ link: 'attaching', hostBuild: message.payload.hostBuild });
        const { thumbnail } = this.options;
        this.sendSession('session.attach', thumbnail === undefined ? {} : { thumbnail });
        this.startHeartbeat();
        return;
      }
      case 'access.revoked':
        this.stop('revoked', this.errorText('revoked', message.payload.message));
        return;
      case 'error':
        if (message.requestId === this.writeRequest) {
          const { code, message: text } = message.payload;
          if (code === 'not_controller' || code === 'stale_lease') {
            this.dropControl(undefined);
          }
          this.finishWrite(message.requestId, { state: 'error', message: WRITE_ERRORS[code] ?? this.errorText(code, text) });
          return;
        }
        if (message.requestId === this.removalRequest) {
          const { code, message: text } = message.payload;
          if (code === 'not_controller' || code === 'stale_lease') {
            this.dropControl(undefined);
          }
          this.finishRemoval(message.requestId, { state: 'error', message: WRITE_ERRORS[code] ?? this.errorText(code, text) });
          return;
        }
        this.failChanges(message.requestId);
        this.failFiles(message.requestId);
        this.failTasks(message.requestId);
        this.failBranches(message.requestId);
        this.handleError(message.payload.code, this.errorText(message.payload.code, message.payload.message));
        return;
      default:
        break;
    }
    if (message.sessionId !== this.options.sessionId) {
      return;
    }
    if (message.generation !== this.options.generation) {
      this.lose();
      return;
    }
    const generation = this.options.generation;
    try {
      switch (message.type) {
        case 'snapshot.begin':
          this.assembler.begin({ generation, ...message.payload });
          return;
        case 'snapshot.rows':
          this.assembler.addRows({ generation, ...message.payload });
          return;
        case 'snapshot.end': {
          const snapshot = this.assembler.end({ generation, ...message.payload });
          this.pendingSnapshot = { snapshotId: message.payload.snapshotId, snapshot };
          this.pendingPatch = undefined;
          this.scheduleFrame();
          return;
        }
        case 'screen.patch':
          this.queuePatch({ generation, ...message.payload });
          return;
        case 'history.page':
          this.handleHistoryPage(message.payload);
          return;
        case 'history.changed':
          this.handleHistoryChanged(message.payload);
          return;
        case 'changes.summary':
          this.handleChangesSummary(message.requestId, message.payload);
          return;
        case 'changes.patch':
          this.handleChangesPatch(message.requestId, message.payload);
          return;
        case 'files.listed': {
          const request = this.takeFilesReply(message.requestId);
          if (request === undefined) {
            return;
          }
          const { root } = message.payload;
          if (root !== undefined) {
            this.lastRoot = { root, cwd: request.cwd };
          }
          // A root for an older cwd answers no one; the next request asks again.
          if (request.cwd === this.status.cwd) {
            this.resolveRoot(root);
          }
          const files = this.files;
          if (files !== undefined) {
            const { folder } = files;
            this.files = {
              ...files,
              root: root ?? files.root,
              folder: folder.state === 'loading' && request.kind === 'list' && folder.path === request.key
                ? { ...folder, state: 'ready', listed: message.payload }
                : folder,
            };
          }
          this.pumpFiles();
          this.emit();
          return;
        }
        case 'files.content': {
          const request = this.takeFilesReply(message.requestId);
          const file = this.files?.file;
          if (request !== undefined && this.files !== undefined && file?.state === 'loading' && request.kind === 'read' && file.path === request.key) {
            this.files = message.payload.state === 'not_file'
              // A tapped path can be a folder: list it instead.
              ? { ...this.files, file: undefined, folder: { path: file.path, state: 'loading', listed: undefined } }
              : { ...this.files, file: { ...file, state: 'ready', content: message.payload } };
          }
          this.pumpFiles();
          this.emit();
          return;
        }
        case 'files.found': {
          const request = this.takeFilesReply(message.requestId);
          const search = this.files?.search;
          if (request !== undefined && this.files !== undefined && search?.state === 'loading' && request.kind === 'search' && search.query === request.key) {
            this.files = { ...this.files, search: { ...search, state: 'ready', found: message.payload } };
          }
          this.pumpFiles();
          this.emit();
          return;
        }
        case 'tasks.listed':
          if (message.requestId === this.tasksRequest) {
            this.tasksRequest = undefined;
            this.tasks = { state: 'ready', listed: message.payload };
            this.emit();
          }
          return;
        case 'branches.listed':
          if (message.requestId === this.branchesRequest) {
            this.branchesRequest = undefined;
            this.branches = { state: 'ready', listed: message.payload };
            this.emit();
          }
          return;
        case 'worktree.removed':
          this.finishRemoval(message.requestId, { state: 'done', removed: message.payload });
          return;
        case 'changes.committed':
          this.finishWrite(message.requestId, { state: 'done', committed: message.payload });
          return;
        case 'changes.pushed':
          this.finishWrite(message.requestId, { state: 'done', pushed: message.payload });
          return;
        case 'control.changed': {
          const { lease, controllerConnectionId, controllerLabel } = message.payload;
          if (lease !== undefined) {
            this.grantControl(lease);
          } else {
            const hadControl = this.lease !== undefined || this.status.control === 'acquiring';
            this.dropControl(hadControl && controllerConnectionId !== null ? 'Another connection took control.' : undefined);
          }
          this.update({
            otherController: lease === undefined && controllerConnectionId !== null,
            controllerLabel: lease === undefined && controllerConnectionId !== null ? controllerLabel : undefined,
          });
          return;
        }
        case 'input.accepted': {
          const { inputSequence } = message.payload;
          this.sender?.accepted(inputSequence);
          const answer = this.status.answer;
          if (answer !== undefined && inputSequence === this.answerSequence) {
            this.answerSequence = undefined;
            this.update({ answer: { ...answer, phase: 'sent' } });
          }
          return;
        }
        case 'input.rejected': {
          // The broker never advances its high-water mark for a rejected
          // sequence, so every later sequence on this lease is a gap. Stop
          // sending and require an explicit Take control for a fresh lease.
          const { code, inputSequence } = message.payload;
          const text = this.errorText(code, message.payload.message);
          const answer = inputSequence !== undefined && inputSequence === this.answerSequence ? this.status.answer : undefined;
          if (answer !== undefined) {
            this.answerSequence = undefined;
          }
          // Exception: these are refused before the sequence is taken. If
          // nothing was sent after it, reuse the sequence and keep control.
          if ((code === 'question_changed' || code === 'invalid_request')
            && inputSequence !== undefined && this.sender?.rewind(inputSequence) === true) {
            this.update(code === 'question_changed' && answer !== undefined
              ? { answer: { ...answer, phase: 'changed' } }
              : { answer: undefined, notice: text });
            return;
          }
          this.sender?.rejected(inputSequence);
          const lease = this.lease;
          if (lease !== undefined && code !== 'stale_lease' && code !== 'not_controller') {
            this.sendSession('control.release', { lease });
          }
          this.sender?.disconnect();
          this.sender = undefined;
          this.dropControl(undefined);
          this.update({ notice: text });
          return;
        }
        case 'session.state': {
          const { agent, previews, cwd } = message.payload;
          // A fresh state ends a sent answer; a "question changed" note stays while a question is up.
          const answer = this.status.answer;
          const keep = answer?.phase === 'pending' || (answer?.phase === 'changed' && agent?.state === 'waiting');
          this.update({
            sessionState: message.payload.state,
            exitCode: message.payload.exitCode,
            agent,
            previews,
            cwd,
            answer: keep ? answer : undefined,
          });
          if (message.payload.state === 'lost') {
            this.lose();
          } else if (message.payload.state === 'exited' || message.payload.state === 'exiting') {
            this.dropControl(undefined);
          }
          return;
        }
        default:
          return;
      }
    } catch (error) {
      if (error instanceof ResyncRequiredError) {
        this.requestResync('interrupted-snapshot');
        return;
      }
      throw error;
    }
  }

  /** A host's error text: home's own, or for a peer the app's words for its code. */
  private errorText(code: string, text: string): string {
    return this.options.peer === true ? peerErrorText(code) : text;
  }

  private handleError(code: string, message: string): void {
    switch (code) {
      case 'unauthorized':
      case 'revoked':
        this.stop('revoked', message);
        return;
      case 'session_lost':
      case 'session_missing':
        // A late heartbeat or resize after the shell exited: keep the exit screen.
        if (this.status.sessionState === 'exited' || this.status.sessionState === 'exiting') {
          this.update({ sessionState: 'exited' });
          this.stop('closed');
          return;
        }
        this.lose();
        return;
      case 'generation_changed':
        this.lose();
        return;
      case 'resync_required':
        this.requestResync('invalid-state');
        // The broker refuses control.acquire until the new snapshot is applied.
        if (this.status.control === 'acquiring') {
          this.update({ control: 'observer' });
        }
        return;
      case 'not_controller':
      case 'stale_lease':
        this.dropControl(message);
        return;
      case 'protocol_mismatch':
        this.stop('closed', message);
        return;
      default:
        this.update({
          notice: message,
          ...(this.status.control === 'acquiring' ? { control: 'observer' as const } : {}),
        });
    }
  }

  private queuePatch(patch: ScreenPatch & { historyReset: boolean }): void {
    const pending = this.pendingPatch;
    if (pending === undefined) {
      this.pendingPatch = patch;
    } else if (
      patch.baseRevision === pending.revision
      && patch.screen.columns === pending.screen.columns
      && patch.screen.rows === pending.screen.rows
    ) {
      // Coalesce so at most one paint is queued per animation frame.
      const rows = new Map<number, ChangedRow>();
      for (const row of [...pending.rows, ...patch.rows]) {
        rows.set(row.index, row);
      }
      this.pendingPatch = {
        generation: patch.generation,
        baseRevision: pending.baseRevision,
        revision: patch.revision,
        screen: patch.screen,
        rows: [...rows.values()],
      };
    } else {
      this.pendingPatch = undefined;
      this.requestResync('base-mismatch');
      return;
    }
    this.scheduleFrame();
  }

  private scheduleFrame(): void {
    if (this.frameScheduled) {
      return;
    }
    this.frameScheduled = true;
    const epoch = this.socketEpoch;
    let ran = false;
    const run = () => {
      if (ran) {
        return;
      }
      ran = true;
      clearTimeout(fallback);
      this.frameScheduled = false;
      if (epoch === this.socketEpoch) {
        this.flushFrame();
      }
    };
    // Hidden tabs never run animation frames; the broker still needs snapshot.applied.
    const fallback = setTimeout(run, FRAME_FALLBACK_MS);
    this.requestFrame(run);
  }

  private flushFrame(): void {
    if (this.assembler.isAssembling()) {
      return;
    }
    const epoch = this.socketEpoch;
    const pendingSnapshot = this.pendingSnapshot;
    if (pendingSnapshot !== undefined) {
      this.pendingSnapshot = undefined;
      const { snapshot, snapshotId } = pendingSnapshot;
      this.paint(() => this.options.display.paintSnapshot(snapshot), epoch, () => {
        this.screen = snapshot.screen;
        this.screenRows = [...snapshot.rows];
        this.requestedSize = undefined;
        this.sendSession('snapshot.applied', { snapshotId, revision: snapshot.revision });
        this.reconnectAttempt = 0;
        this.settleGate('live');
        this.update({
          ...(this.status.link === 'attaching' ? { link: 'live' as const } : {}),
          buffer: snapshot.screen.buffer,
          columns: snapshot.screen.columns,
          rows: snapshot.screen.rows,
        });
        this.markNewOutput();
        if (this.pendingPatch !== undefined) {
          this.scheduleFrame();
        }
      }, 'invalid-state');
      return;
    }
    const patch = this.pendingPatch;
    if (patch === undefined) {
      return;
    }
    this.pendingPatch = undefined;
    this.paint(() => this.options.display.applyPatch(patch), epoch, () => {
      this.screen = patch.screen;
      for (const { index, row } of patch.rows) {
        this.screenRows[index] = row;
      }
      if (this.status.buffer !== patch.screen.buffer) {
        this.update({ buffer: patch.screen.buffer });
      }
      this.markNewOutput();
    }, 'base-mismatch');
  }

  private paint(
    work: () => Promise<void>,
    epoch: number,
    done: () => void,
    reason: ScreenResyncPayload['reason'],
  ): void {
    let painting: Promise<void>;
    try {
      painting = work();
    } catch {
      this.requestResync(reason);
      return;
    }
    painting.then(() => {
      if (epoch === this.socketEpoch && !this.stopped) {
        done();
      }
    }, () => {
      if (epoch === this.socketEpoch && !this.stopped) {
        this.requestResync(reason);
      }
    });
  }

  private requestResync(reason: ScreenResyncPayload['reason']): void {
    this.assembler.abort();
    this.pendingPatch = undefined;
    const lastRevision = this.pendingSnapshot?.snapshot.revision;
    this.sendSession('screen.resync', { reason, ...(lastRevision === undefined ? {} : { lastRevision }) });
  }

  private markNewOutput(): void {
    const history = this.history;
    if (history === undefined) {
      return;
    }
    history.advanced = true;
    if (history.view.state === 'ready' && !history.view.newOutput) {
      history.view = { ...history.view, newOutput: true };
      this.emit();
    }
  }

  private readHistory(offset: number): void {
    const history = this.history;
    if (history === undefined) {
      return;
    }
    history.expectedOffset = offset;
    this.sendSession('history.read', {
      historyEpoch: history.epoch,
      historyRevision: history.revision,
      offset,
      limit: HISTORY_PAGE_ROWS,
    });
  }

  private handleHistoryPage(page: HistoryPagePayload): void {
    const history = this.history;
    if (
      history === undefined
      || history.view.state !== 'loading'
      || page.historyEpoch !== history.epoch
      || page.historyRevision !== history.revision
      || page.offset !== history.expectedOffset
    ) {
      return;
    }
    history.history.push(...page.rows.map(rowText));
    history.pages++;
    const next = page.nextOffset;
    if (page.complete || next === undefined || next <= page.offset || history.pages >= MAX_HISTORY_PAGES) {
      this.finishHistory();
      this.emit();
      return;
    }
    this.readHistory(next);
  }

  /** The history moved under us: restart from a new consistent snapshot. */
  private handleHistoryChanged(changed: HistoryChangedPayload): void {
    const history = this.history;
    if (history === undefined || history.view.state !== 'loading') {
      return;
    }
    if (++history.restarts > MAX_HISTORY_RESTARTS) {
      history.view = { ...history.view, state: 'unavailable' };
      this.emit();
      return;
    }
    history.epoch = changed.historyEpoch;
    history.revision = changed.historyRevision;
    history.pages = 0;
    history.advanced = false;
    history.history = [];
    history.screen = this.screenRows.map(rowText);
    this.readHistory(0);
  }

  private finishHistory(): void {
    const history = this.history;
    if (history === undefined) {
      return;
    }
    history.view = {
      state: 'ready',
      lines: [...history.history, ...history.screen],
      historyRows: history.history.length,
      newOutput: history.advanced,
    };
  }

  private readChanges(): void {
    const changes = this.changes;
    if (changes === undefined) {
      return;
    }
    changes.listRequest = this.sendSession('changes.list', {});
    if (changes.listRequest === undefined) {
      changes.view = { ...changes.view, state: 'unavailable' };
    }
  }

  private handleChangesSummary(requestId: string, summary: ChangesSummaryPayload): void {
    const changes = this.changes;
    if (changes === undefined || requestId !== changes.listRequest) {
      return;
    }
    changes.listRequest = undefined;
    changes.view = { ...changes.view, state: 'ready', summary };
    this.emit();
  }

  private handleChangesPatch(requestId: string, patch: ChangesPatchPayload): void {
    const changes = this.changes;
    const path = changes?.fileRequests.get(requestId);
    if (changes === undefined || path === undefined) {
      return;
    }
    changes.fileRequests.delete(requestId);
    // unavailable is not cached, so opening the file again retries.
    if (patch.state !== 'unavailable') {
      changes.patches.set(path, patch);
    }
    if (changes.view.file?.path === path) {
      changes.view = { ...changes.view, file: { path, state: 'ready', patch } };
      this.emit();
    }
  }

  /**
   * Ends loading for a changes request the host refused, or for every pending
   * one when requestId is undefined because the socket closed.
   */
  private failChanges(requestId: string | undefined): void {
    const changes = this.changes;
    if (changes === undefined) {
      return;
    }
    let view = changes.view;
    if (requestId === undefined || requestId === changes.listRequest) {
      changes.listRequest = undefined;
      if (view.state === 'loading') {
        view = { ...view, state: 'unavailable' };
      }
    }
    const failed = requestId === undefined ? view.file?.path : changes.fileRequests.get(requestId);
    if (requestId === undefined) {
      changes.fileRequests.clear();
    } else {
      changes.fileRequests.delete(requestId);
    }
    if (view.file?.state === 'loading' && view.file.path === failed) {
      view = { ...view, file: { ...view.file, state: 'unavailable' } };
    }
    if (view !== changes.view) {
      changes.view = view;
      this.emit();
    }
  }

  /** The files root for the session's current cwd, when a listing named it; undefined until then. */
  knownFilesRoot(): string | undefined {
    return this.lastRoot?.cwd === this.status.cwd ? this.lastRoot?.root : undefined;
  }

  private resolveRoot(root: string | undefined): void {
    const waiters = this.rootWaiters;
    this.rootWaiters = [];
    for (const resolve of waiters) {
      resolve(root);
    }
  }

  /** Sends what the Files view waits for next, unless a files request is in flight. */
  private pumpFiles(): void {
    if (this.filesRequest !== undefined) {
      return;
    }
    const files = this.files;
    let next: Omit<FilesRequest, 'id' | 'cwd'> | undefined;
    if (files?.file?.state === 'loading') {
      next = { kind: 'read', key: files.file.path };
    } else if (files?.search?.state === 'loading') {
      next = { kind: 'search', key: files.search.query };
    } else if (files?.folder.state === 'loading') {
      next = { kind: 'list', key: files.folder.path };
    } else if (this.rootWaiters.length > 0) {
      next = { kind: 'list', key: '' };
    }
    if (next === undefined) {
      return;
    }
    const id = this.sendSession(`files.${next.kind}`, next.kind === 'search' ? { query: next.key } : { path: next.key });
    if (id === undefined) {
      this.failFiles(undefined);
      return;
    }
    this.filesRequest = { id, ...next, cwd: this.status.cwd };
    this.filesTimer = setTimeout(() => this.failFiles(id), FILES_TIMEOUT_MS);
  }

  /** The request this reply answers, or undefined for a stale one. */
  private takeFilesReply(requestId: string): FilesRequest | undefined {
    const request = this.filesRequest;
    if (request?.id !== requestId) {
      return undefined;
    }
    this.filesRequest = undefined;
    clearTimeout(this.filesTimer);
    return request;
  }

  /**
   * Ends loading for the files request the host refused, then sends the next;
   * or, when requestId is undefined because the socket closed, for all of them.
   */
  private failFiles(requestId: string | undefined): void {
    const request = this.filesRequest;
    if (requestId !== undefined && request?.id !== requestId) {
      return;
    }
    this.filesRequest = undefined;
    clearTimeout(this.filesTimer);
    const fails = (kind: FilesRequest['kind'], key: string) => requestId === undefined || (request?.kind === kind && request.key === key);
    const files = this.files;
    if (files !== undefined) {
      const { folder, file, search } = files;
      this.files = {
        ...files,
        folder: folder.state === 'loading' && fails('list', folder.path) ? { ...folder, state: 'unavailable' } : folder,
        file: file?.state === 'loading' && fails('read', file.path) ? { ...file, state: 'unavailable' } : file,
        search: search?.state === 'loading' && fails('search', search.query) ? { ...search, state: 'unavailable' } : search,
      };
    }
    if (requestId === undefined || request?.kind === 'list') {
      this.resolveRoot(undefined);
    }
    if (requestId !== undefined) {
      this.pumpFiles();
    }
    this.emit();
  }

  /** Ends loading for a tasks.list the host refused, or for any when requestId is undefined (socket closed). */
  private failTasks(requestId: string | undefined): void {
    if (this.tasksRequest === undefined || (requestId !== undefined && requestId !== this.tasksRequest)) {
      return;
    }
    this.tasksRequest = undefined;
    this.tasks = { state: 'unavailable', listed: undefined };
    this.emit();
  }

  /** Ends loading for a branches.list the host refused, or for any when requestId is undefined (socket closed). */
  private failBranches(requestId: string | undefined): void {
    if (this.branchesRequest === undefined || (requestId !== undefined && requestId !== this.branchesRequest)) {
      return;
    }
    this.branchesRequest = undefined;
    this.branches = { state: 'unavailable', listed: undefined };
    this.emit();
  }

  private finishRemoval(requestId: string, result: WorktreeRemoval): void {
    if (requestId !== this.removalRequest) {
      return;
    }
    this.removalRequest = undefined;
    this.removal = result;
    this.emit();
  }

  /** One write at a time, with the current lease; never queued or resent. */
  private sendWrite(kind: ChangesWrite['kind'], type: string, payload: (lease: string) => object): boolean {
    const lease = this.lease;
    if (this.writeRequest !== undefined || !canSendWith(this.status) || lease === undefined) {
      return false;
    }
    const requestId = this.sendSession(type, payload(lease));
    if (requestId === undefined) {
      return false;
    }
    this.writeRequest = requestId;
    this.write = { kind, state: 'pending' };
    this.emit();
    return true;
  }

  /** Shows the reply to the pending write, then reads the list again, since the tree may have changed. */
  private finishWrite(requestId: string, result: Omit<ChangesWrite, 'kind'>): void {
    const write = this.write;
    if (write === undefined || requestId !== this.writeRequest) {
      return;
    }
    this.writeRequest = undefined;
    this.write = { kind: write.kind, ...result };
    this.emit();
    this.refreshChanges();
  }

  private grantControl(lease: string): void {
    this.lease = lease;
    this.sender = new InputSender(
      {
        send: (message) => {
          if (this.socket === undefined || this.socket.readyState !== OPEN) {
            throw new Error('WebSocket is not open');
          }
          this.socket.send(message);
        },
        close: (reason) => this.socket?.close(4001, reason),
      },
      this.options.sessionId,
      this.options.generation,
      lease,
    );
    this.update({ control: 'controller', otherController: false, controllerLabel: undefined });
  }

  /** Drops the local lease. Unacknowledged input becomes "delivery uncertain". */
  private dropControl(notice: string | undefined): void {
    const uncertain = this.sender?.disconnect() ?? [];
    this.sender = undefined;
    this.lease = undefined;
    this.requestedSize = undefined;
    this.answerSequence = undefined;
    const message = uncertain.length > 0 ? DELIVERY_UNCERTAIN : notice;
    this.update({
      control: 'observer',
      answer: undefined,
      ...(message === undefined ? {} : { notice: message }),
    });
  }

  private withSender(send: (sender: InputSender) => unknown): boolean {
    const sender = this.sender;
    if (!this.canSend() || sender === undefined) {
      return false;
    }
    try {
      send(sender);
      return true;
    } catch (error) {
      if (error instanceof DeliveryUncertainError) {
        this.sender = undefined;
        this.lease = undefined;
        this.update({ control: 'observer', notice: error.message });
        this.socket?.close(4001, 'delivery uncertain');
      } else {
        this.update({ notice: error instanceof Error ? error.message : 'Input was not sent.' });
      }
      return false;
    }
  }

  private handleClose(epoch: number): void {
    if (epoch !== this.socketEpoch) {
      return;
    }
    this.socket = undefined;
    this.socketEpoch++;
    this.settleGate('failed');
    this.clearProbe();
    clearTimeout(this.attachTimer);
    this.stopHeartbeat();
    this.assembler.abort();
    this.pendingSnapshot = undefined;
    this.pendingPatch = undefined;
    this.frameScheduled = false;
    this.dropControl(undefined);
    if (this.history?.view.state === 'loading') {
      this.history.view = { ...this.history.view, state: 'unavailable' };
    }
    // Replies to this socket's requests can no longer arrive.
    this.failChanges(undefined);
    this.failFiles(undefined);
    this.failTasks(undefined);
    this.failBranches(undefined);
    if (this.removalRequest !== undefined) {
      // As with a write: the host may have removed it; never resend.
      this.removalRequest = undefined;
      this.removal = { state: 'error', message: REMOVE_LOST };
    }
    if (this.write !== undefined && this.writeRequest !== undefined) {
      // The host may or may not have run it: say so, and never resend.
      this.writeRequest = undefined;
      this.write = { kind: this.write.kind, state: 'error', message: WRITE_LOST };
    }
    if (this.stopped) {
      return;
    }
    // The next attach sends a fresh session.state; never answer from an old one.
    this.update({ link: 'reconnecting', otherController: false, controllerLabel: undefined, agent: undefined, previews: undefined, cwd: undefined });
    // A hidden page cannot keep a socket (phones freeze it), so it waits: the
    // visibility, pageshow and online listeners reconnect at once on return.
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
      return;
    }
    const delay = RECONNECT_DELAYS_MS[Math.min(this.reconnectAttempt, RECONNECT_DELAYS_MS.length - 1)];
    this.reconnectAttempt++;
    this.reconnectTimer = setTimeout(() => this.open(), delay);
  }

  private lose(): void {
    this.stop('lost');
  }

  private stop(link: 'lost' | 'revoked' | 'closed', notice?: string): void {
    this.stopped = true;
    this.listen(false);
    this.clearReconnect();
    this.cancelGate();
    this.settleGate('cancelled');
    this.clearProbe();
    clearTimeout(this.attachTimer);
    this.stopHeartbeat();
    this.dropControl(undefined);
    const socket = this.socket;
    this.socket = undefined;
    this.socketEpoch++;
    this.update({ link, ...(notice === undefined ? {} : { notice }) });
    socket?.close(1000, link);
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      const lease = this.lease;
      this.sendSession('heartbeat', lease === undefined ? {} : { lease });
    }, HEARTBEAT_MS);
  }

  private stopHeartbeat(): void {
    clearInterval(this.heartbeatTimer);
    this.heartbeatTimer = undefined;
  }

  private clearReconnect(): void {
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = undefined;
  }

  private sendSession(type: string, payload: object): string | undefined {
    return this.send({ type, sessionId: this.options.sessionId, generation: this.options.generation, payload });
  }

  /** Returns the request ID, or undefined when the socket is not open. */
  private send(message: { type: string; sessionId?: string; generation?: string; payload: object }): string | undefined {
    const socket = this.socket;
    if (socket === undefined || socket.readyState !== OPEN) {
      return undefined;
    }
    const requestId = crypto.randomUUID();
    socket.send(JSON.stringify({ version: 1, requestId, ...message }));
    return requestId;
  }

  private update(change: Partial<ConnectionStatus>): void {
    this.status = { ...this.status, ...change };
    this.emit();
  }

  private emit(): void {
    for (const listener of this.listeners) {
      listener();
    }
  }
}

/** Input and resize are allowed only for a live controller of a running session. */
export function canSendWith(status: ConnectionStatus): boolean {
  return status.link === 'live' && status.control === 'controller'
    && status.sessionState !== 'exited' && status.sessionState !== 'exiting' && status.sessionState !== 'lost';
}

export function clampGrid(columns: number, rows: number): { columns: number; rows: number } {
  const clamp = (value: number, min: number, max: number) => (
    Number.isFinite(value) ? Math.min(max, Math.max(min, Math.floor(value))) : min
  );
  return { columns: clamp(columns, 20, 400), rows: clamp(rows, 5, 200) };
}

/** Selectable text for one normalized row; continuation cells carry no text. */
export function rowText(row: TerminalRow | undefined): string {
  if (row === undefined) {
    return '';
  }
  let text = '';
  for (const run of row.runs) {
    for (const cell of run.cells) {
      if (!cell.continuation) {
        text += cell.text;
      }
    }
  }
  return text.trimEnd();
}
