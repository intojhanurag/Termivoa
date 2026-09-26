import type {
  HistoryChangedPayload,
  HistoryPagePayload,
  InputMousePayload,
  KeyName,
  Modifiers,
  ScreenResyncPayload,
  State as SessionLifecycle,
} from '../generated/protocol';
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
import { SnapshotAssembler } from './SnapshotAssembler';

export const DELIVERY_UNCERTAIN = 'Delivery uncertain—check the terminal before sending again.';
export const CLIENT_BUILD = 'phone-terminal-web/0.0.0-private';
export const HEARTBEAT_MS = 5000;
export const RECONNECT_DELAYS_MS = [500, 1000, 2000, 4000, 8000, 15000] as const;
const HISTORY_PAGE_ROWS = 200;
const MAX_HISTORY_PAGES = 25;
const OPEN = 1;

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
}

export interface HistoryView {
  readonly state: 'loading' | 'ready' | 'unavailable';
  /** Retained normal history rows followed by the frozen current screen. */
  readonly lines: ReadonlyArray<string>;
  readonly historyRows: number;
  /** Live output advanced after this snapshot was frozen. */
  readonly newOutput: boolean;
}

/** FrameRenderer satisfies this; tests pass a fake. */
export interface ScreenDisplay {
  paintSnapshot(snapshot: ScreenSnapshot): Promise<void>;
  applyPatch(patch: ScreenPatch): Promise<void>;
}

export interface ConnectionOptions {
  readonly url: string;
  readonly sessionId: string;
  readonly generation: string;
  readonly display: ScreenDisplay;
  readonly createSocket?: (url: string) => WebSocket;
  readonly requestFrame?: (callback: () => void) => void;
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
  advanced: boolean;
  history: string[];
  screen: string[];
  view: HistoryView;
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
  private readonly assembler = new SnapshotAssembler();
  private pendingSnapshot: PendingSnapshot | undefined;
  private pendingPatch: ScreenPatch | undefined;
  private frameScheduled = false;
  private screen: ScreenMetadata | undefined;
  private screenRows: TerminalRow[] = [];
  private history: HistoryState | undefined;
  private stopped = false;
  private requestedSize: string | undefined;

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
    if (this.status.link !== 'live' || this.status.control !== 'observer') {
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

  private open(): void {
    this.clearReconnect();
    const epoch = ++this.socketEpoch;
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
        this.sendSession('session.attach', {});
        this.startHeartbeat();
        return;
      }
      case 'access.revoked':
        this.stop('revoked', message.payload.message);
        return;
      case 'error':
        this.handleError(message.payload.code, message.payload.message);
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
        case 'input.accepted':
          this.sender?.accepted(message.payload.inputSequence);
          return;
        case 'input.rejected': {
          // The broker never advances its high-water mark for a rejected
          // sequence, so every later sequence on this lease is a gap. Stop
          // sending and require an explicit Take control for a fresh lease.
          const { code, inputSequence, message: text } = message.payload;
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
        case 'session.state':
          this.update({ sessionState: message.payload.state, exitCode: message.payload.exitCode });
          if (message.payload.state === 'lost') {
            this.lose();
          } else if (message.payload.state === 'exited' || message.payload.state === 'exiting') {
            this.dropControl(undefined);
          }
          return;
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

  private handleError(code: string, message: string): void {
    switch (code) {
      case 'unauthorized':
      case 'revoked':
        this.stop('revoked', message);
        return;
      case 'generation_changed':
      case 'session_lost':
      case 'session_missing':
        this.lose();
        return;
      case 'resync_required':
        this.requestResync('invalid-state');
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
    this.requestFrame(() => {
      this.frameScheduled = false;
      if (epoch === this.socketEpoch) {
        this.flushFrame();
      }
    });
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
    const message = uncertain.length > 0 ? DELIVERY_UNCERTAIN : notice;
    this.update({
      control: 'observer',
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
    this.stopHeartbeat();
    this.assembler.abort();
    this.pendingSnapshot = undefined;
    this.pendingPatch = undefined;
    this.frameScheduled = false;
    this.dropControl(undefined);
    if (this.history?.view.state === 'loading') {
      this.history.view = { ...this.history.view, state: 'unavailable' };
    }
    if (this.stopped) {
      return;
    }
    this.update({ link: 'reconnecting', otherController: false, controllerLabel: undefined });
    const delay = RECONNECT_DELAYS_MS[Math.min(this.reconnectAttempt, RECONNECT_DELAYS_MS.length - 1)];
    this.reconnectAttempt++;
    this.reconnectTimer = setTimeout(() => this.open(), delay);
  }

  private lose(): void {
    this.stop('lost');
  }

  private stop(link: 'lost' | 'revoked' | 'closed', notice?: string): void {
    this.stopped = true;
    this.clearReconnect();
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

  private sendSession(type: string, payload: object): void {
    this.send({ type, sessionId: this.options.sessionId, generation: this.options.generation, payload });
  }

  private send(message: { type: string; sessionId?: string; generation?: string; payload: object }): void {
    const socket = this.socket;
    if (socket === undefined || socket.readyState !== OPEN) {
      return;
    }
    socket.send(JSON.stringify({ version: 1, requestId: crypto.randomUUID(), ...message }));
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
