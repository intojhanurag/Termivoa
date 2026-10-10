import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clampGrid,
  Connection,
  DELIVERY_UNCERTAIN,
  FILES_TIMEOUT_MS,
  HEARTBEAT_MS,
  HISTORY_TIMEOUT_MS,
  RECONNECT_DELAYS_MS,
  RESUME_CHECK_MS,
  RESUME_PROBE_MS,
  ATTACH_TIMEOUT_MS,
  REMOVE_LOST,
  rowText,
  WRITE_LOST,
  type ConnectionOptions,
  type ScreenDisplay,
} from './Connection';
import { Scheduler } from './scheduler';
import { AppScroll, fitGrid, followCursor, MAX_WHEEL_STEPS, TerminalView, wheelSteps } from './TerminalView';
import type { CellStyle, ScreenMetadata, ScreenPatch, ScreenSnapshot, TerminalRow } from './FrameRenderer';

const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const sessionId = id(1);
const generation = id(2);
const brokerGeneration = id(3);
const lease = 'L'.repeat(43);
const none = { shift: false, alt: false, ctrl: false, meta: false };

const black = { r: 0, g: 0, b: 0, a: 255 };
const white = { r: 255, g: 255, b: 255, a: 255 };
const style: CellStyle = {
  foreground: white, background: black, bold: false, faint: false, italic: false,
  blink: false, reverse: false, conceal: false, strikethrough: false, underline: 'none',
};

function metadata(buffer: 'normal' | 'alternate' = 'normal', historyRevision = '0'): ScreenMetadata {
  return {
    columns: 20, rows: 5, buffer,
    cursor: { column: 0, row: 0, style: 'block', visible: true, blink: false },
    foreground: white, background: black, cursorColor: white,
    widthMethod: 'unicode-graphemes', historyEpoch: '1', historyRevision, contentClipped: false,
  };
}

function row(text = ''): TerminalRow {
  const cells = Array.from({ length: 20 }, (_, index) => ({
    text: text[index] ?? ' ', width: 1, continuation: false, contentClipped: false,
  }));
  return { runs: [{ column: 0, style, cells }] } as unknown as TerminalRow;
}

class FakeSocket {
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: Array<{ type: string; requestId: string; payload: Record<string, unknown>; sessionId?: string; generation?: string }> = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }

  send(data: string) {
    this.sent.push(JSON.parse(data) as FakeSocket['sent'][number]);
  }

  close() {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.onclose?.();
  }

  open() {
    this.readyState = 1;
    this.onopen?.();
  }

  receive(type: string, payload: object, identity: { sessionId?: string; generation?: string } | null = { sessionId, generation }, requestId = id(99)) {
    this.onmessage?.({ data: JSON.stringify({ version: 1, type, requestId, ...(identity ?? {}), payload }) });
  }

  /** Answers a sent request with its own requestId. */
  reply(request: FakeSocket['sent'][number] | undefined, type: string, payload: object) {
    this.receive(type, payload, { sessionId, generation }, request?.requestId);
  }

  types() {
    return this.sent.map((message) => message.type);
  }
}

function welcome(socket: FakeSocket, broker = brokerGeneration) {
  socket.receive('welcome', {
    hostBuild: 'test-host', brokerGeneration: broker, negotiatedVersion: 1, capabilityProfile: 'phone-terminal-v1',
    limits: {
      inboundMessageBytes: 262144, outboundMessageBytes: 1048576, outgoingQueueBytes: 2097152,
      maxTextBytes: 65536, maxPasteBytes: 65536, minColumns: 20, maxColumns: 400, minRows: 5, maxRows: 200,
      maxHistoryPageRows: 200,
    },
  }, null);
}

function snapshot(socket: FakeSocket, revision: string, lines: string[] = ['$ ls', '', '', '', ''], screen = metadata()) {
  const snapshotId = id(50 + Number(revision));
  socket.receive('snapshot.begin', { snapshotId, revision, screen });
  socket.receive('snapshot.rows', { snapshotId, revision, startRow: 0, rows: lines.map(row) });
  socket.receive('snapshot.end', { snapshotId, revision, rowCount: 5 });
}

function patch(socket: FakeSocket, baseRevision: string, revision: string, index: number, text: string) {
  socket.receive('screen.patch', {
    baseRevision, revision, screen: metadata(), rows: [{ index, row: row(text) }], historyReset: false,
  });
}

/** A gate that admits every attempt at once, like the active terminal's. */
const openGate = () => new Scheduler().connectGate('https://host.invalid', true);

function harness(extra: Partial<ConnectionOptions> = {}) {
  const painted: Array<ScreenSnapshot | ScreenPatch> = [];
  const display: ScreenDisplay = {
    paintSnapshot: async (value) => { painted.push(value); },
    applyPatch: async (value) => { painted.push(value); },
  };
  const connection = new Connection({
    url: 'wss://host.invalid/api/v1/ws',
    sessionId,
    generation,
    display,
    createSocket: (url) => new FakeSocket(url) as unknown as WebSocket,
    requestFrame: (callback) => queueMicrotask(callback),
    gate: openGate(),
    ...extra,
  });
  connection.start();
  const socket = () => FakeSocket.all.at(-1) as FakeSocket;
  return { connection, painted, socket };
}

async function attachLive(socket: FakeSocket, revision = '1') {
  socket.open();
  welcome(socket);
  snapshot(socket, revision);
  await vi.advanceTimersByTimeAsync(0);
}

async function takeControl(connection: Connection, socket: FakeSocket) {
  connection.takeControl();
  socket.receive('control.changed', { controllerConnectionId: id(7), leaseEpoch: '1', lease });
  await vi.advanceTimersByTimeAsync(0);
}

describe('Connection', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('attaches as an observer and acknowledges the painted snapshot before offering control', async () => {
    const { connection, painted, socket } = harness();
    expect(connection.getStatus().link).toBe('connecting');
    socket().open();
    expect(socket().types()).toEqual(['hello']);
    expect(socket().sent[0]?.payload).toMatchObject({ clientKind: 'browser', supportedVersions: [1] });
    welcome(socket());
    expect(socket().types()).toEqual(['hello', 'session.attach']);
    expect(connection.getStatus().link).toBe('attaching');

    snapshot(socket(), '4');
    expect(socket().types()).not.toContain('snapshot.applied');
    await vi.advanceTimersByTimeAsync(0);

    expect(painted).toHaveLength(1);
    expect(socket().sent.at(-1)).toMatchObject({ type: 'snapshot.applied', payload: { revision: '4' } });
    expect(connection.getStatus()).toMatchObject({ link: 'live', control: 'observer' });
    expect(socket().types()).not.toContain('control.acquire');
    expect(connection.sendText('ls')).toBe(false);
  });

  it('sends input only after an explicit Take control grants a lease', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    await takeControl(connection, socket());
    expect(connection.getStatus().control).toBe('controller');
    expect(connection.sendText('ls')).toBe(true);
    expect(connection.sendKey('enter', none)).toBe(true);
    expect(socket().sent.slice(-2)).toMatchObject([
      { type: 'input.text', sessionId, generation, payload: { text: 'ls', lease, inputSequence: '1' } },
      { type: 'input.key', payload: { key: 'enter', lease, inputSequence: '2' } },
    ]);
  });

  it('sends a wheel step at the given cell, clamped to the grid, and at the cursor without one', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    await takeControl(connection, socket());
    expect(connection.sendWheel('wheel-up', { row: 3, column: 14 })).toBe(true);
    expect(connection.sendWheel('wheel-down', { row: 99, column: -4 })).toBe(true);
    expect(connection.sendWheel('wheel-down')).toBe(true);
    expect(socket().sent.slice(-3).map((message) => message.payload)).toMatchObject([
      { kind: 'wheel', button: 'wheel-up', column: 14, row: 3 },
      { kind: 'wheel', button: 'wheel-down', column: 0, row: 4 },
      { kind: 'wheel', button: 'wheel-down', column: 0, row: 0 },
    ]);
  });

  it('reports uncertain delivery, reconnects as an observer, and never replays input or control', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    await takeControl(connection, first);
    connection.sendText('rm -i notes.txt');
    first.close();

    expect(connection.getStatus()).toMatchObject({ link: 'reconnecting', control: 'observer', notice: DELIVERY_UNCERTAIN });
    expect(connection.sendText('more')).toBe(false);
    await vi.advanceTimersByTimeAsync(249);
    expect(FakeSocket.all).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    const second = socket();
    expect(second).not.toBe(first);

    await attachLive(second, '9');
    expect(second.types()).toEqual(['hello', 'session.attach', 'snapshot.applied']);
    expect(connection.getStatus()).toMatchObject({ link: 'live', control: 'observer' });
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 3);
    expect(second.types().filter((type) => type.startsWith('input.') || type === 'control.acquire')).toEqual([]);
  });

  it('acknowledged input does not trigger the uncertain notice', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    await takeControl(connection, socket());
    connection.sendText('pwd');
    socket().receive('input.accepted', { inputSequence: '1' });
    socket().close();
    expect(connection.getStatus().notice).toBeUndefined();
  });

  it('drops the lease after a rejected input so later input cannot fall into a sequence gap', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    await takeControl(connection, socket());
    connection.sendText('a');
    connection.sendText('b');
    socket().receive('input.rejected', { inputSequence: '1', code: 'limit_exceeded', message: 'session resource limit reached' });

    expect(connection.getStatus()).toMatchObject({ control: 'observer', notice: 'session resource limit reached' });
    expect(socket().sent.at(-1)).toMatchObject({ type: 'control.release', payload: { lease } });
    expect(connection.sendText('c')).toBe(false);
    expect(socket().types().filter((type) => type === 'input.text')).toHaveLength(2);

    await takeControl(connection, socket());
    connection.sendText('d');
    expect(socket().sent.at(-1)).toMatchObject({ type: 'input.text', payload: { text: 'd', inputSequence: '1' } });
  });

  describe('answers', () => {
    const questionId = 'ab'.repeat(16);
    const waiting = (qid = questionId) => ({
      state: 'running',
      agent: { state: 'waiting', stateSince: '2026-09-27T10:00:00Z', question: { id: qid, text: 'Proceed?', options: [{ label: 'Yes' }, { label: 'No' }] } },
    });

    it('publishes the agent status from session.state to observers', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      socket().receive('session.state', waiting());
      expect(connection.getStatus().agent?.question?.id).toBe(questionId);
      expect(connection.sendAnswer(questionId, 0)).toBe(false);
      expect(socket().types()).not.toContain('input.answer');
    });

    it('maps session.state previews, clears them when absent and on reconnect', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      const previews = [{ port: 5173, since: '2026-09-27T10:00:00Z' }];
      socket().receive('session.state', { state: 'running', previews });
      expect(connection.getStatus().previews).toEqual(previews);
      socket().receive('session.state', { state: 'running' });
      expect(connection.getStatus().previews).toBeUndefined();
      socket().receive('session.state', { state: 'running', previews });
      socket().close();
      expect(connection.getStatus().previews).toBeUndefined();
    });

    it('maps session.state cwd, clears it when absent and on reconnect', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      socket().receive('session.state', { state: 'running', cwd: '/Users/me/src/app' });
      expect(connection.getStatus().cwd).toBe('/Users/me/src/app');
      socket().receive('session.state', { state: 'running' });
      expect(connection.getStatus().cwd).toBeUndefined();
      socket().receive('session.state', { state: 'running', cwd: '/Users/me/src/app' });
      socket().close();
      expect(connection.getStatus().cwd).toBeUndefined();
    });

    it('sends one answer with the lease and next sequence, then closes on input.accepted', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      await takeControl(connection, socket());
      socket().receive('session.state', waiting());
      connection.sendText('a');
      expect(connection.sendAnswer(questionId, 1)).toBe(true);
      expect(socket().sent.at(-1)).toMatchObject({
        type: 'input.answer', sessionId, generation, payload: { lease, inputSequence: '2', questionId, optionIndex: 1 },
      });
      expect(connection.getStatus().answer).toEqual({ questionId, optionIndex: 1, phase: 'pending' });
      // A second tap while the first is pending sends nothing.
      expect(connection.sendAnswer(questionId, 0)).toBe(false);
      expect(socket().types().filter((type) => type === 'input.answer')).toHaveLength(1);

      socket().receive('input.accepted', { inputSequence: '2' });
      expect(connection.getStatus().answer?.phase).toBe('sent');
      socket().receive('session.state', { state: 'running', agent: { state: 'working', stateSince: '2026-09-27T10:00:05Z' } });
      expect(connection.getStatus().answer).toBeUndefined();
      expect(connection.getStatus().control).toBe('controller');
    });

    it('keeps control after question_changed and reuses the refused sequence', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      await takeControl(connection, socket());
      socket().receive('session.state', waiting());
      connection.sendAnswer(questionId, 0);
      socket().receive('input.rejected', { inputSequence: '1', code: 'question_changed', message: 'the question on screen changed' });

      expect(connection.getStatus()).toMatchObject({ control: 'controller', notice: undefined, answer: { questionId, phase: 'changed' } });
      expect(socket().types()).not.toContain('control.release');
      // The old question is still shown: its options stay refused until a fresh state.
      socket().receive('session.state', waiting('cd'.repeat(16)));
      expect(connection.getStatus().answer?.phase).toBe('changed');
      expect(connection.sendAnswer('cd'.repeat(16), 1)).toBe(true);
      expect(socket().sent.at(-1)).toMatchObject({ type: 'input.answer', payload: { inputSequence: '1', questionId: 'cd'.repeat(16) } });
      socket().close();
      expect(connection.getStatus()).toMatchObject({ notice: DELIVERY_UNCERTAIN, answer: undefined, agent: undefined });
    });

    it('keeps control after invalid_request and shows the reason', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      await takeControl(connection, socket());
      connection.sendAnswer(questionId, 5);
      socket().receive('input.rejected', { inputSequence: '1', code: 'invalid_request', message: 'that answer is not offered' });
      expect(connection.getStatus()).toMatchObject({ control: 'controller', notice: 'that answer is not offered', answer: undefined });
      connection.sendText('n');
      expect(socket().sent.at(-1)).toMatchObject({ type: 'input.text', payload: { inputSequence: '1' } });
    });

    it('drops control as before when later input already used the next sequence', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      await takeControl(connection, socket());
      connection.sendAnswer(questionId, 0);
      connection.sendText('x');
      socket().receive('input.rejected', { inputSequence: '1', code: 'question_changed', message: 'changed' });
      expect(connection.getStatus()).toMatchObject({ control: 'observer', answer: undefined });
      expect(socket().sent.at(-1)).toMatchObject({ type: 'control.release' });
    });

    it('drops control on other answer rejections', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      await takeControl(connection, socket());
      connection.sendAnswer(questionId, 0);
      socket().receive('input.rejected', { inputSequence: '1', code: 'stale_lease', message: 'lease expired' });
      expect(connection.getStatus()).toMatchObject({ control: 'observer', notice: 'lease expired', answer: undefined });
    });
  });

  it('shows a lost session when the session generation changes', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    socket().receive('session.state', { state: 'running' }, { sessionId, generation: id(40) });
    expect(connection.getStatus().link).toBe('lost');
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it('treats a changed broker generation after reconnect as lost', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    socket().close();
    await vi.advanceTimersByTimeAsync(500);
    socket().open();
    welcome(socket(), id(41));
    expect(connection.getStatus().link).toBe('lost');
    expect(socket().types()).toEqual(['hello']);
  });

  it('maps generation errors to lost and access.revoked to revoked', async () => {
    const lost = harness();
    await attachLive(lost.socket());
    lost.socket().receive('error', { code: 'generation_changed', message: 'Session restarted' });
    expect(lost.connection.getStatus().link).toBe('lost');

    const revoked = harness();
    await attachLive(revoked.socket());
    revoked.socket().receive('access.revoked', { message: 'This device was revoked.' }, null);
    expect(revoked.connection.getStatus()).toMatchObject({ link: 'revoked', notice: 'This device was revoked.' });
  });

  it('never shows a peer\'s own error text: each code gets the app\'s words', async () => {
    const peer = harness({ peer: true });
    await attachLive(peer.socket());
    peer.socket().receive('error', { code: 'host_unavailable', message: 'Call +1 555 0100 to unlock' });
    expect(peer.connection.getStatus().notice).toBe('That computer is busy. Try again.');
    peer.socket().receive('access.revoked', { message: 'Re-pair at https://evil.example' }, null);
    expect(peer.connection.getStatus()).toMatchObject({ link: 'revoked', notice: 'This phone is no longer paired with that computer.' });

    const home = harness();
    await attachLive(home.socket());
    home.socket().receive('error', { code: 'host_unavailable', message: 'Broker restarting' });
    expect(home.connection.getStatus().notice).toBe('Broker restarting');
  });

  it('heartbeats every 5 s with the lease only while holding control', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    expect(socket().sent.at(-1)).toMatchObject({ type: 'heartbeat', payload: {} });
    await takeControl(connection, socket());
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    expect(socket().sent.at(-1)).toMatchObject({ type: 'heartbeat', payload: { lease } });
    connection.releaseControl();
    expect(socket().sent.at(-1)).toMatchObject({ type: 'control.release', payload: { lease } });
    expect(connection.getStatus().control).toBe('observer');
  });

  it('coalesces patches into one paint per frame and resyncs on a gap', async () => {
    const { painted, socket } = harness();
    await attachLive(socket());
    patch(socket(), '1', '2', 0, 'a');
    patch(socket(), '2', '3', 1, 'b');
    await vi.advanceTimersByTimeAsync(0);
    expect(painted).toHaveLength(2);
    expect(painted[1]).toMatchObject({ baseRevision: '1', revision: '3' });

    patch(socket(), '3', '4', 0, 'c');
    patch(socket(), '9', '10', 0, 'd');
    await vi.advanceTimersByTimeAsync(0);
    expect(painted).toHaveLength(2);
    expect(socket().sent.at(-1)).toMatchObject({ type: 'screen.resync', payload: { reason: 'base-mismatch' } });
  });

  it('returns to observer when the broker answers Take control with resync_required', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.takeControl();
    expect(connection.getStatus().control).toBe('acquiring');
    socket().receive('error', { code: 'resync_required', message: 'apply the current snapshot before taking control' });

    expect(socket().sent.at(-1)).toMatchObject({ type: 'screen.resync', payload: { reason: 'invalid-state' } });
    expect(connection.getStatus().control).toBe('observer');
    snapshot(socket(), '2');
    await vi.advanceTimersByTimeAsync(0);
    connection.takeControl();
    expect(socket().sent.at(-1)).toMatchObject({ type: 'control.acquire' });
  });

  it('keeps the exit screen when a late request reports session_lost after the shell exited', async () => {
    const { connection, painted, socket } = harness();
    await attachLive(socket());
    socket().receive('session.state', { state: 'exited', exitCode: 3 });
    socket().receive('error', { code: 'session_lost', message: 'session is gone' });

    expect(connection.getStatus()).toMatchObject({ sessionState: 'exited', exitCode: 3 });
    expect(connection.getStatus().link).not.toBe('lost');
    expect(painted).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it('still reports a lost session when the shell had not exited', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    socket().receive('error', { code: 'session_lost', message: 'session is gone' });
    expect(connection.getStatus().link).toBe('lost');
  });

  it('paints and acknowledges a snapshot when animation frames never run', async () => {
    const painted: Array<ScreenSnapshot | ScreenPatch> = [];
    const connection = new Connection({
      url: 'wss://host.invalid/api/v1/ws',
      sessionId,
      generation,
      display: {
        paintSnapshot: async (value) => { painted.push(value); },
        applyPatch: async (value) => { painted.push(value); },
      },
      createSocket: (url) => new FakeSocket(url) as unknown as WebSocket,
      requestFrame: () => undefined,
      gate: openGate(),
    });
    connection.start();
    const socket = FakeSocket.all.at(-1) as FakeSocket;
    socket.open();
    welcome(socket);
    snapshot(socket, '1');
    await vi.advanceTimersByTimeAsync(200);

    expect(painted).toHaveLength(1);
    expect(socket.sent.at(-1)).toMatchObject({ type: 'snapshot.applied', payload: { revision: '1' } });
    expect(connection.getStatus().link).toBe('live');

    patch(socket, '1', '2', 0, 'a');
    await vi.advanceTimersByTimeAsync(200);
    expect(painted).toHaveLength(2);
  });

  it('bounds controller resize and ignores observers', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    expect(connection.resize(80, 24)).toBe(false);
    await takeControl(connection, socket());
    expect(connection.resize(10, 1000)).toBe(true);
    expect(socket().sent.at(-1)).toMatchObject({ type: 'terminal.resize', payload: { lease, columns: 20, rows: 200 } });
    expect(connection.resize(20, 200)).toBe(false);
    expect(connection.resize(20, 5)).toBe(false);
    expect(fitGrid(390, 700, 8.4, 17)).toEqual({ columns: 46, rows: 41 });
    expect(fitGrid(100, 50, 8.4, 17)).toEqual({ columns: 20, rows: 5 });
    expect(fitGrid(390, 700, 0, 17)).toEqual({ columns: 20, rows: 5 });
    expect(clampGrid(1000, 1)).toEqual({ columns: 400, rows: 5 });
    expect(clampGrid(57.9, 31.2)).toEqual({ columns: 57, rows: 31 });
    expect(clampGrid(Number.NaN, Number.POSITIVE_INFINITY)).toEqual({ columns: 20, rows: 5 });
  });
});

describe('History/Selection', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('reads bounded pages of one token, freezes, flags new output, and returns to live', async () => {
    const { connection, painted, socket } = harness();
    await attachLive(socket());
    expect(connection.openHistory()).toBe(true);
    expect(socket().sent.at(-1)).toMatchObject({
      type: 'history.read', payload: { historyEpoch: '1', historyRevision: '0', offset: 0, limit: 200 },
    });
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '0', columns: 20, offset: 0, rows: [row('old 1')], nextOffset: 1, complete: false,
    });
    expect(socket().sent.at(-1)).toMatchObject({ type: 'history.read', payload: { offset: 1 } });
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '0', columns: 20, offset: 1, rows: [row('old 2')], complete: true,
    });
    const frozen = connection.getHistory();
    expect(frozen).toMatchObject({ state: 'ready', historyRows: 2, newOutput: false });
    expect(frozen?.lines).toEqual(['old 1', 'old 2', '$ ls', '', '', '', '']);

    patch(socket(), '1', '2', 0, '$ ls -la');
    await vi.advanceTimersByTimeAsync(0);
    expect(painted).toHaveLength(2);
    expect(connection.getHistory()?.lines).toEqual(frozen?.lines);
    expect(connection.getHistory()?.newOutput).toBe(true);

    connection.closeHistory();
    expect(connection.getHistory()).toBeUndefined();
  });

  it('loads byte-trimmed pages past 25 without joining a partial history to the screen', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    // The real retained styled-history fixture needs 40 pages: 39 x 41,
    // followed by 33 rows, because each wire page hits the byte budget.
    for (let page = 0; page < 40; page++) {
      const offset = page * 41;
      const count = page === 39 ? 33 : 41;
      expect(socket().sent.at(-1)).toMatchObject({ type: 'history.read', payload: { offset } });
      socket().receive('history.page', {
        historyEpoch: '1', historyRevision: '0', columns: 20, offset,
        rows: Array.from({ length: count }, (_, i) => row(`line ${offset + i}`)),
        ...(page === 39 ? {} : { nextOffset: offset + count }), complete: page === 39,
      });
      expect(connection.getHistory()?.state).toBe(page === 39 ? 'ready' : 'loading');
    }
    expect(connection.getHistory()).toMatchObject({ state: 'ready', historyRows: 1632 });
    expect(connection.getHistory()?.lines[1631]).toBe('line 1631');
    expect(connection.getHistory()?.lines[1632]).toBe('$ ls');
    connection.detach();
  });

  it('loads all 5,000 retained rows even when a page contains only one row', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    for (let offset = 0; offset < 5000; offset++) {
      socket().receive('history.page', {
        historyEpoch: '1', historyRevision: '0', columns: 20, offset, rows: [row('old')],
        ...(offset === 4999 ? {} : { nextOffset: offset + 1 }), complete: offset === 4999,
      });
    }
    expect(connection.getHistory()).toMatchObject({ state: 'ready', historyRows: 5000 });
    expect(socket().types().filter((type) => type === 'history.read')).toHaveLength(5000);
    connection.detach();
  });

  it('does not reset the request budget after the history changes', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    for (let offset = 0; offset < 4998; offset++) {
      socket().receive('history.page', {
        historyEpoch: '1', historyRevision: '0', columns: 20, offset, rows: [row('old')],
        nextOffset: offset + 1, complete: false,
      });
    }
    socket().receive('history.changed', { historyEpoch: '1', historyRevision: '1' });
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '1', columns: 20, offset: 0, rows: [row('new')],
      nextOffset: 1, complete: false,
    });
    expect(connection.getHistory()).toMatchObject({ state: 'unavailable', lines: [], historyRows: 0 });
    expect(socket().types().filter((type) => type === 'history.read')).toHaveLength(5000);
    connection.detach();
  });

  it('refuses a cursor that skips rows instead of claiming a complete transcript', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '0', columns: 20, offset: 0,
      rows: [row('old')], nextOffset: 2, complete: false,
    });
    expect(connection.getHistory()).toMatchObject({ state: 'unavailable', lines: [], historyRows: 0 });
    expect(socket().types().filter((type) => type === 'history.read')).toHaveLength(1);
    connection.detach();
  });

  it('uses one loading deadline across restarts and ignores replies after it expires', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    await vi.advanceTimersByTimeAsync(HISTORY_TIMEOUT_MS - 1);
    socket().receive('history.changed', { historyEpoch: '1', historyRevision: '1' });
    await vi.advanceTimersByTimeAsync(1);
    expect(connection.getHistory()?.state).toBe('unavailable');
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '1', columns: 20, offset: 0, rows: [row('late')], complete: true,
    });
    expect(connection.getHistory()).toMatchObject({ state: 'unavailable', lines: [] });
    connection.detach();
  });

  it('clears the deadline on completion, close, and reopening history', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    await vi.advanceTimersByTimeAsync(HISTORY_TIMEOUT_MS / 2);
    connection.closeHistory();
    connection.openHistory();
    await vi.advanceTimersByTimeAsync(HISTORY_TIMEOUT_MS / 2);
    expect(connection.getHistory()?.state).toBe('loading');
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '0', columns: 20, offset: 0, rows: [], complete: true,
    });
    await vi.advanceTimersByTimeAsync(HISTORY_TIMEOUT_MS);
    expect(connection.getHistory()?.state).toBe('ready');
    connection.detach();
  });

  it('drops incomplete text when its memory budget is exceeded', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    const largeRow = { runs: row().runs.map((run) => ({
      ...run, cells: run.cells.map((cell) => ({ ...cell, text: 'x'.repeat(1024) })),
    })) };
    // 10 rows per message remains below the 1 MiB wire bound. Text retained
    // across valid pages eventually exceeds the phone's 32 MiB budget.
    for (let offset = 0; offset < 1000 && connection.getHistory()?.state === 'loading'; offset += 10) {
      socket().receive('history.page', {
        historyEpoch: '1', historyRevision: '0', columns: 20, offset,
        rows: Array.from({ length: 10 }, () => largeRow), nextOffset: offset + 10, complete: false,
      });
    }
    expect(connection.getHistory()).toMatchObject({ state: 'unavailable', lines: [], historyRows: 0 });
    expect(socket().types().filter((type) => type === 'history.read').length).toBeLessThan(100);
    connection.detach();
  });

  it('bounds received page data across restarts even when flattened text is small', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    const linkedRow = { runs: row('old').runs.map((run) => ({
      ...run, cells: run.cells.map((cell) => ({ ...cell, link: `https://example.test/${'x'.repeat(2000)}` })),
    })) };
    let restarted = false;
    for (let offset = 0; offset < 2000 && connection.getHistory()?.state === 'loading'; offset += 10) {
      socket().receive('history.page', {
        historyEpoch: '1', historyRevision: '0', columns: 20, offset,
        rows: Array.from({ length: 10 }, () => linkedRow), nextOffset: offset + 10, complete: false,
      });
      if (offset === 990 && !restarted) {
        restarted = true;
        socket().receive('history.changed', { historyEpoch: '1', historyRevision: '0' });
        offset = -10;
      }
    }
    expect(connection.getHistory()).toMatchObject({ state: 'unavailable', lines: [], historyRows: 0 });
    expect(socket().types().filter((type) => type === 'history.read').length).toBeLessThan(200);
    connection.detach();
  });

  it('restarts from a new token after history.changed', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '0', columns: 20, offset: 0, rows: [row('stale')], nextOffset: 1, complete: false,
    });
    socket().receive('history.changed', { historyEpoch: '1', historyRevision: '5' });
    expect(socket().sent.at(-1)).toMatchObject({
      type: 'history.read', payload: { historyEpoch: '1', historyRevision: '5', offset: 0 },
    });
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '0', columns: 20, offset: 1, rows: [row('ignored')], complete: true,
    });
    expect(connection.getHistory()?.state).toBe('loading');
    socket().receive('history.page', {
      historyEpoch: '1', historyRevision: '5', columns: 20, offset: 0, rows: [row('fresh')], complete: true,
    });
    expect(connection.getHistory()?.lines[0]).toBe('fresh');
    expect(connection.getHistory()?.historyRows).toBe(1);
  });

  it('stops restarting when history keeps changing under continuous output', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openHistory();
    for (let revision = 1; revision <= 20; revision++) {
      socket().receive('history.changed', { historyEpoch: '1', historyRevision: String(revision) });
    }
    expect(socket().types().filter((type) => type === 'history.read').length).toBeLessThanOrEqual(6);
    expect(connection.getHistory()?.state).toBe('unavailable');
  });

  it('uses only the live grid for alternate-screen applications', async () => {
    const { connection, socket } = harness();
    socket().open();
    welcome(socket());
    snapshot(socket(), '1', ['vim', '', '', '', ''], metadata('alternate'));
    await vi.advanceTimersByTimeAsync(0);
    connection.openHistory();
    expect(socket().types()).not.toContain('history.read');
    expect(connection.getHistory()).toMatchObject({ state: 'ready', historyRows: 0 });
    expect(rowText(row('vim'))).toBe('vim');
  });
});

describe('Changes', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const file = { path: 'src/a.ts', status: 'modified', additions: 1, deletions: 1, binary: false };
  const summary = (files: object[] = [file]) => ({ state: 'ready', root: '/Users/me/app', branch: 'main', files, truncated: false });
  const patch = (path = 'src/a.ts', text = 'new') => ({
    path, state: 'ready', truncated: false,
    lines: [{ kind: 'hunk', text: '@@ -1 +1 @@' }, { kind: 'remove', text: 'old', oldLine: 1 }, { kind: 'add', text, newLine: 1 }],
  });
  const last = (socket: FakeSocket, type: string) => socket.sent.filter((message) => message.type === type).at(-1);

  it('reads the list as an observer and shows the summary for its own request only', async () => {
    const { connection, socket } = harness();
    expect(connection.openChanges()).toBe(false);
    await attachLive(socket());
    expect(connection.getStatus().control).toBe('observer');
    expect(connection.openChanges()).toBe(true);
    const request = last(socket(), 'changes.list');
    expect(request).toMatchObject({ sessionId, generation, payload: {} });
    expect(connection.getChanges()).toEqual({ state: 'loading', summary: undefined, file: undefined });

    socket().receive('changes.summary', summary([]));
    expect(connection.getChanges()?.state).toBe('loading');
    socket().reply(request, 'changes.summary', summary());
    expect(connection.getChanges()).toMatchObject({ state: 'ready', summary: { branch: 'main', files: [file] } });

    connection.closeChanges();
    expect(connection.getChanges()).toBeUndefined();
  });

  it('keeps the last summary while a refresh loads and drops the reply to the older request', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openChanges();
    const first = last(socket(), 'changes.list');
    connection.refreshChanges();
    const second = last(socket(), 'changes.list');
    expect(second?.requestId).not.toBe(first?.requestId);
    socket().reply(first, 'changes.summary', summary([]));
    expect(connection.getChanges()?.state).toBe('loading');
    socket().reply(second, 'changes.summary', summary());
    expect(connection.getChanges()?.summary?.files).toHaveLength(1);

    connection.refreshChanges();
    expect(connection.getChanges()).toMatchObject({ state: 'loading', summary: { files: [file] } });
  });

  it('caches each patch until refresh and shows only the open file', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openChanges();
    connection.openChangesFile('src/a.ts');
    const request = last(socket(), 'changes.file');
    expect(request?.payload).toEqual({ path: 'src/a.ts' });
    expect(connection.getChanges()?.file).toEqual({ path: 'src/a.ts', state: 'loading', patch: undefined });
    socket().reply(request, 'changes.patch', patch());
    expect(connection.getChanges()?.file).toMatchObject({ path: 'src/a.ts', state: 'ready', patch: { lines: [{}, {}, { text: 'new' }] } });

    connection.closeChangesFile();
    expect(connection.getChanges()?.file).toBeUndefined();
    const sent = socket().sent.length;
    connection.openChangesFile('src/a.ts');
    expect(socket().sent).toHaveLength(sent);
    expect(connection.getChanges()?.file?.state).toBe('ready');

    // A late reply for a file the user left is cached, not shown.
    connection.openChangesFile('src/b.ts');
    const other = last(socket(), 'changes.file');
    connection.closeChangesFile();
    socket().reply(other, 'changes.patch', patch('src/b.ts'));
    expect(connection.getChanges()?.file).toBeUndefined();

    connection.openChangesFile('src/a.ts');
    connection.refreshChanges();
    const again = last(socket(), 'changes.file');
    expect(again?.requestId).not.toBe(request?.requestId);
    expect(connection.getChanges()?.file).toEqual({ path: 'src/a.ts', state: 'loading', patch: undefined });
    socket().reply(request, 'changes.patch', patch('src/a.ts', 'stale'));
    expect(connection.getChanges()?.file?.state).toBe('loading');
    socket().reply(again, 'changes.patch', patch('src/a.ts', 'fresh'));
    expect(connection.getChanges()?.file?.patch?.lines[2]?.text).toBe('fresh');
  });

  it('does not cache an unavailable patch, so opening the file again retries', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openChanges();
    connection.openChangesFile('src/a.ts');
    socket().reply(last(socket(), 'changes.file'), 'changes.patch', { path: 'src/a.ts', state: 'unavailable', lines: [], truncated: false });
    expect(connection.getChanges()?.file).toMatchObject({ state: 'ready', patch: { state: 'unavailable' } });
    const sent = socket().types().filter((type) => type === 'changes.file').length;
    connection.openChangesFile('src/a.ts');
    expect(socket().types().filter((type) => type === 'changes.file')).toHaveLength(sent + 1);
  });

  it('ends loading when the host refuses the list request and keeps the socket open', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    connection.openChanges();
    first.reply(last(first, 'changes.list'), 'error', { code: 'internal_error', message: 'git failed' });
    expect(connection.getChanges()?.state).toBe('unavailable');
    expect(first.readyState).toBe(1);
    // The host's message is shown, and the session stays live.
    expect(connection.getStatus()).toMatchObject({ link: 'live', notice: 'git failed' });
  });

  it('says nothing when the host refuses a read that nobody asked for (the away recap), and asks again on a tap', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    // A herdr terminal outside git: the host has no folder to read.
    const refusal = { code: 'invalid_request', message: 'this herdr terminal\'s folder cannot be read' };
    expect(connection.openChanges(true)).toBe(true);
    const quiet = last(first, 'changes.list');
    first.reply(quiet, 'error', refusal);
    expect(connection.getStatus()).toMatchObject({ link: 'live', notice: undefined });
    expect(connection.getChanges()).toBeUndefined();
    // The user taps Changes: the refusal shows.
    connection.openChanges();
    const asked = last(first, 'changes.list');
    expect(asked?.requestId).not.toBe(quiet?.requestId);
    first.reply(asked, 'error', refusal);
    expect(connection.getChanges()?.state).toBe('unavailable');
    expect(connection.getStatus().notice).toBe(refusal.message);
  });

  it('keeps a quiet read quiet when the user opens Changes before its refusal comes', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    connection.openChanges(true);
    const quiet = last(first, 'changes.list');
    connection.openChanges();
    const asked = last(first, 'changes.list');
    // The quiet read's refusal says nothing and leaves the user's read alone.
    first.reply(quiet, 'error', { code: 'invalid_request', message: 'refused' });
    expect(connection.getStatus().notice).toBeUndefined();
    expect(connection.getChanges()?.state).toBe('loading');
    // The second read is the user's own: its refusal shows.
    first.reply(asked, 'error', { code: 'invalid_request', message: 'refused' });
    expect(connection.getStatus().notice).toBe('refused');
    expect(connection.getChanges()?.state).toBe('unavailable');
  });

  it('says nothing when the host refuses the files root read-ahead that nobody asked for; a listing the user opened shows the refusal', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    const refusal = { code: 'invalid_request', message: 'this herdr terminal\'s folder cannot be read' };
    // Live reads the files root ahead when the terminal opens (for tapped paths).
    const root = connection.filesRoot();
    first.reply(last(first, 'files.list'), 'error', refusal);
    await expect(root).resolves.toBeUndefined();
    expect(connection.getStatus()).toMatchObject({ link: 'live', notice: undefined });
    // The user opens Files: that refusal shows.
    connection.openFilesFolder('');
    first.reply(last(first, 'files.list'), 'error', refusal);
    expect(connection.getFiles()?.folder.state).toBe('unavailable');
    expect(connection.getStatus().notice).toBe(refusal.message);
    connection.dismissNotice();
    // The read-ahead is on its way when the user opens Files: the user waits for it now, so its refusal shows.
    connection.closeFiles();
    void connection.filesRoot();
    const ahead = last(first, 'files.list');
    connection.openFilesFolder('');
    first.reply(ahead, 'error', refusal);
    expect(connection.getStatus().notice).toBe(refusal.message);
  });

  it('a switch to Changes while only the quiet read is on its way asks for the user, so a quiet refusal leaves no endless loading', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    expect(connection.needsChanges()).toBe(true);
    connection.openChanges(true);
    const quiet = last(first, 'changes.list');
    // The panel is open on Files; the user switches to Changes (Live's switchTab).
    expect(connection.needsChanges()).toBe(true);
    connection.openChanges();
    expect(connection.needsChanges()).toBe(false);
    first.reply(quiet, 'error', { code: 'invalid_request', message: 'refused' });
    expect(connection.getChanges()?.state).toBe('loading');
    first.reply(last(first, 'changes.list'), 'error', { code: 'invalid_request', message: 'refused' });
    expect(connection.getChanges()?.state).toBe('unavailable');
    // A read that the user asked for, or a quiet one that came, is not asked again.
    expect(connection.needsChanges()).toBe(false);
  });

  it('ends loading when the socket closes and ignores replies from the old socket', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    connection.openChanges();
    connection.openChangesFile('src/a.ts');
    const list = last(first, 'changes.list');
    const request = last(first, 'changes.file');
    first.close();
    expect(connection.getChanges()).toMatchObject({ state: 'unavailable', file: { state: 'unavailable' } });

    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    const second = socket();
    await attachLive(second, '2');
    first.reply(list, 'changes.summary', summary());
    second.reply(list, 'changes.summary', summary());
    second.reply(request, 'changes.patch', patch());
    expect(connection.getChanges()).toMatchObject({ state: 'unavailable', file: { state: 'unavailable' } });

    connection.refreshChanges();
    second.reply(last(second, 'changes.list'), 'changes.summary', summary());
    second.reply(last(second, 'changes.file'), 'changes.patch', patch());
    expect(connection.getChanges()).toMatchObject({ state: 'ready', file: { state: 'ready' } });
  });
});

describe('Project tasks', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const listed = (state = 'ready', tasks: object[] = [{ name: 'test', command: 'pnpm test', source: 'package.json', kind: 'test' }]) =>
    ({ state, folder: '/Users/me/app', tasks });
  const last = (socket: FakeSocket) => socket.sent.filter((message) => message.type === 'tasks.list').at(-1);

  it('asks once per call as an observer and shows only the reply to the latest request', async () => {
    const { connection, socket } = harness();
    expect(connection.listTasks()).toBe(false);
    expect(connection.getTasks()).toEqual({ state: 'unavailable', listed: undefined });
    await attachLive(socket());
    expect(connection.getStatus().control).toBe('observer');

    expect(connection.listTasks()).toBe(true);
    const first = last(socket());
    expect(first).toMatchObject({ sessionId, generation, payload: {} });
    expect(connection.getTasks()).toEqual({ state: 'loading', listed: undefined });
    connection.listTasks();
    const second = last(socket());
    expect(socket().types().filter((type) => type === 'tasks.list')).toHaveLength(2);

    socket().reply(first, 'tasks.listed', listed('none', []));
    expect(connection.getTasks()?.state).toBe('loading');
    socket().reply(second, 'tasks.listed', listed());
    expect(connection.getTasks()).toMatchObject({ state: 'ready', listed: { state: 'ready', tasks: [{ command: 'pnpm test' }] } });
    socket().reply(second, 'tasks.listed', listed('none', []));
    expect(connection.getTasks()?.listed?.state).toBe('ready');

    connection.listTasks();
    socket().reply(last(socket()), 'tasks.listed', listed('unavailable', []));
    expect(connection.getTasks()).toMatchObject({ state: 'ready', listed: { state: 'unavailable' } });
  });

  it('ends loading when the host refuses the request or the socket closes, and ignores the old socket', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    connection.listTasks();
    first.reply(last(first), 'error', { code: 'internal_error', message: 'read failed' });
    expect(connection.getTasks()).toEqual({ state: 'unavailable', listed: undefined });

    connection.listTasks();
    const request = last(first);
    // A refusal of another request leaves this one loading.
    first.receive('error', { code: 'internal_error', message: 'other' }, { sessionId, generation }, id(98));
    expect(connection.getTasks()?.state).toBe('loading');
    expect(first.readyState).toBe(1);
    first.close();
    expect(connection.getTasks()?.state).toBe('unavailable');

    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    const second = socket();
    await attachLive(second, '2');
    second.reply(request, 'tasks.listed', listed());
    expect(connection.getTasks()?.state).toBe('unavailable');
    connection.listTasks();
    second.reply(last(second), 'tasks.listed', listed());
    expect(connection.getTasks()?.state).toBe('ready');
  });
});

describe('Worktrees', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const last = (socket: FakeSocket, type: string) => socket.sent.filter((message) => message.type === type).at(-1);
  const ready = {
    state: 'ready', repo: 'app', defaultBranch: 'main', current: 'main', truncated: false,
    branches: [{ name: 'main', remote: false }, { name: 'origin/main', remote: true }],
  };

  it('lists branches as an observer and shows only the reply to the latest request', async () => {
    const { connection, socket } = harness();
    expect(connection.listBranches()).toBe(false);
    expect(connection.getBranches()).toEqual({ state: 'unavailable', listed: undefined });
    await attachLive(socket());

    expect(connection.listBranches()).toBe(true);
    const first = last(socket(), 'branches.list');
    expect(first).toMatchObject({ sessionId, generation, payload: {} });
    expect(connection.getBranches()).toEqual({ state: 'loading', listed: undefined });
    connection.listBranches();
    const second = last(socket(), 'branches.list');

    socket().reply(first, 'branches.listed', { state: 'not_git', branches: [], truncated: false });
    expect(connection.getBranches()?.state).toBe('loading');
    socket().reply(second, 'branches.listed', ready);
    expect(connection.getBranches()).toEqual({ state: 'ready', listed: ready });

    connection.listBranches();
    socket().reply(last(socket(), 'branches.list'), 'error', { code: 'internal_error', message: 'git failed' });
    expect(connection.getBranches()).toEqual({ state: 'unavailable', listed: undefined });

    connection.listBranches();
    socket().close();
    expect(connection.getBranches()?.state).toBe('unavailable');
  });

  it('removes with the lease, one at a time, and shows the reply', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    expect(connection.removeWorktree(false)).toBe(false);
    await takeControl(connection, socket());

    expect(connection.removeWorktree(false)).toBe(true);
    const request = last(socket(), 'worktree.remove');
    expect(request).toMatchObject({ sessionId, generation, payload: { lease, force: false } });
    expect(connection.getWorktreeRemoval()).toEqual({ state: 'pending' });
    expect(connection.removeWorktree(true)).toBe(false);

    socket().receive('worktree.removed', { state: 'removed' });
    expect(connection.getWorktreeRemoval()?.state).toBe('pending');
    socket().reply(request, 'worktree.removed', { state: 'dirty' });
    expect(connection.getWorktreeRemoval()).toEqual({ state: 'done', removed: { state: 'dirty' } });

    expect(connection.removeWorktree(true)).toBe(true);
    const forced = last(socket(), 'worktree.remove');
    expect(forced?.payload).toEqual({ lease, force: true });
    socket().reply(forced, 'worktree.removed', { state: 'failed', output: 'fatal: locked' });
    expect(connection.getWorktreeRemoval()).toEqual({ state: 'done', removed: { state: 'failed', output: 'fatal: locked' } });

    connection.removeWorktree(false);
    socket().reply(last(socket(), 'worktree.remove'), 'error', { code: 'operation_conflict', message: 'busy' });
    expect(connection.getWorktreeRemoval()).toEqual({ state: 'error', message: 'Another commit, push or worktree remove is running. Wait for it, then try again.' });
    expect(connection.getStatus()).toMatchObject({ control: 'controller', notice: undefined });

    connection.removeWorktree(false);
    socket().reply(last(socket(), 'worktree.remove'), 'error', { code: 'stale_lease', message: 'stale' });
    expect(connection.getStatus().control).toBe('observer');
  });

  it('says the connection was lost while a remove was pending and never resends it', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    await takeControl(connection, first);
    connection.removeWorktree(false);
    const request = last(first, 'worktree.remove');
    first.close();
    expect(connection.getWorktreeRemoval()).toEqual({ state: 'error', message: REMOVE_LOST });

    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    const second = socket();
    await attachLive(second, '2');
    expect(second.types()).not.toContain('worktree.remove');
    second.reply(request, 'worktree.removed', { state: 'removed' });
    expect(connection.getWorktreeRemoval()?.state).toBe('error');
  });
});

describe('Commit and push', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const last = (socket: FakeSocket, type: string) => socket.sent.filter((message) => message.type === type).at(-1);
  const count = (socket: FakeSocket, type: string) => socket.types().filter((sent) => sent === type).length;

  it('needs control, sends with the lease, allows one write at a time, and refreshes the list after the reply', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    expect(connection.commitChanges('Update a.ts', ['src/a.ts'])).toBe(false);
    expect(connection.pushChanges()).toBe(false);
    expect(socket().types()).not.toContain('changes.commit');

    await takeControl(connection, socket());
    connection.openChanges();
    expect(connection.commitChanges('Update a.ts', [])).toBe(false);
    expect(connection.commitChanges('Update a.ts', ['src/a.ts'])).toBe(true);
    const request = last(socket(), 'changes.commit');
    expect(request).toMatchObject({ sessionId, generation, payload: { lease, message: 'Update a.ts', paths: ['src/a.ts'] } });
    expect(connection.getChangesWrite()).toEqual({ kind: 'commit', state: 'pending' });
    expect(connection.commitChanges('again', ['src/a.ts'])).toBe(false);
    expect(connection.pushChanges()).toBe(false);
    expect(count(socket(), 'changes.commit')).toBe(1);
    connection.dismissChangesWrite();
    expect(connection.getChangesWrite()?.state).toBe('pending');

    const lists = count(socket(), 'changes.list');
    socket().receive('changes.committed', { state: 'committed', commit: 'fffffff' });
    expect(connection.getChangesWrite()?.state).toBe('pending');
    socket().reply(request, 'changes.committed', { state: 'committed', commit: 'abc1234' });
    expect(connection.getChangesWrite()).toEqual({ kind: 'commit', state: 'done', committed: { state: 'committed', commit: 'abc1234' } });
    expect(count(socket(), 'changes.list')).toBe(lists + 1);

    expect(connection.pushChanges()).toBe(true);
    const push = last(socket(), 'changes.push');
    expect(push?.payload).toEqual({ lease });
    socket().reply(push, 'changes.pushed', { state: 'pushed', branch: 'main', url: 'https://github.com/o/r/pull/new/main' });
    expect(connection.getChangesWrite()).toMatchObject({ kind: 'push', state: 'done', pushed: { branch: 'main' } });
    connection.dismissChangesWrite();
    expect(connection.getChangesWrite()).toBeUndefined();
  });

  it('turns an error reply to the write into a clear message and keeps other errors apart', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    await takeControl(connection, socket());

    connection.commitChanges('m', ['a']);
    socket().reply(last(socket(), 'changes.commit'), 'error', { code: 'operation_conflict', message: 'busy' });
    expect(connection.getChangesWrite()).toEqual({ kind: 'commit', state: 'error', message: 'Another commit, push or worktree remove is running. Wait for it, then try again.' });
    expect(connection.getStatus()).toMatchObject({ control: 'controller', notice: undefined });

    connection.pushChanges();
    socket().reply(last(socket(), 'changes.push'), 'error', { code: 'internal_error', message: 'git is missing' });
    expect(connection.getChangesWrite()).toMatchObject({ state: 'error', message: 'git is missing' });

    connection.commitChanges('m', ['a']);
    socket().reply(last(socket(), 'changes.commit'), 'error', { code: 'stale_lease', message: 'stale' });
    expect(connection.getChangesWrite()).toMatchObject({ state: 'error', message: 'You no longer have control. Take control, then try again.' });
    expect(connection.getStatus()).toMatchObject({ control: 'observer', notice: undefined });
  });

  it('says the connection was lost while a write was pending, never resends it, and drops replies to the old socket', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    await takeControl(connection, first);
    connection.commitChanges('m', ['a']);
    const request = last(first, 'changes.commit');
    first.close();
    expect(connection.getChangesWrite()).toEqual({ kind: 'commit', state: 'error', message: WRITE_LOST });

    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    const second = socket();
    await attachLive(second, '2');
    expect(second.types()).not.toContain('changes.commit');
    first.reply(request, 'changes.committed', { state: 'committed', commit: 'abc1234' });
    second.reply(request, 'changes.committed', { state: 'committed', commit: 'abc1234' });
    expect(connection.getChangesWrite()).toMatchObject({ state: 'error', message: WRITE_LOST });
    // Control is never taken back on its own, so nothing can be sent until the user acts.
    expect(connection.commitChanges('m', ['a'])).toBe(false);
  });
});

describe('Session tab switching', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('detaches the old session and attaches the next one read-only, carrying no control or input', async () => {
    const { connection: first, socket } = harness();
    const firstSocket = socket();
    await attachLive(firstSocket);
    await takeControl(first, firstSocket);
    first.sendText('npm test');

    // Live unmounts (detach) and remounts for the other tab with a new Connection.
    first.detach();
    expect(firstSocket.types().slice(-2)).toEqual(['control.release', 'session.detach']);
    expect(firstSocket.readyState).toBe(3);
    expect(first.sendText('more')).toBe(false);

    const otherId = id(11);
    const otherGeneration = id(12);
    const next = new Connection({
      url: 'wss://host.invalid/api/v1/ws',
      sessionId: otherId,
      generation: otherGeneration,
      display: { paintSnapshot: async () => undefined, applyPatch: async () => undefined },
      createSocket: (url) => new FakeSocket(url) as unknown as WebSocket,
      requestFrame: (callback) => queueMicrotask(callback),
      gate: openGate(),
    });
    next.start();
    const nextSocket = socket();
    expect(nextSocket).not.toBe(firstSocket);
    nextSocket.open();
    welcome(nextSocket);
    nextSocket.receive('snapshot.begin', { snapshotId: id(60), revision: '1', screen: metadata() }, { sessionId: otherId, generation: otherGeneration });
    nextSocket.receive('snapshot.rows', { snapshotId: id(60), revision: '1', startRow: 0, rows: ['', '', '', '', ''].map(row) }, { sessionId: otherId, generation: otherGeneration });
    nextSocket.receive('snapshot.end', { snapshotId: id(60), revision: '1', rowCount: 5 }, { sessionId: otherId, generation: otherGeneration });
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 2);

    expect(next.getStatus()).toMatchObject({ link: 'live', control: 'observer' });
    expect(nextSocket.types().filter((type) => type !== 'heartbeat')).toEqual(['hello', 'session.attach', 'snapshot.applied']);
    expect(nextSocket.sent.every((message) => message.sessionId === undefined || message.sessionId === otherId)).toBe(true);
    expect(JSON.stringify(nextSocket.sent)).not.toContain(lease);
    expect(next.sendText('npm test')).toBe(false);
  });
});

describe('Connection resume', () => {
  class FakeDocument extends EventTarget {
    visibilityState: 'visible' | 'hidden' = 'visible';
  }
  let page: FakeDocument;
  let win: EventTarget;
  const setVisibility = (state: 'visible' | 'hidden') => {
    page.visibilityState = state;
    page.dispatchEvent(new Event('visibilitychange'));
  };
  const pageshow = (persisted: boolean) => win.dispatchEvent(Object.assign(new Event('pageshow'), { persisted }));

  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
    page = new FakeDocument();
    win = new EventTarget();
    vi.stubGlobal('document', page);
    vi.stubGlobal('window', win);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  /** Fails `count` connection attempts in a row and returns the gap before each retry. */
  async function failTimes(count: number): Promise<number[]> {
    const gaps: number[] = [];
    for (let i = 0; i < count; i++) {
      const before = FakeSocket.all.length;
      (FakeSocket.all.at(-1) as FakeSocket).close();
      let waited = 0;
      while (FakeSocket.all.length === before) {
        await vi.advanceTimersByTimeAsync(50);
        waited += 50;
      }
      gaps.push(waited);
    }
    return gaps;
  }

  it('caps the reconnect backoff at 5 seconds', async () => {
    harness();
    expect(await failTimes(8)).toEqual([250, 500, 1000, 2000, 3000, 5000, 5000, 5000]);
    expect(RECONNECT_DELAYS_MS.at(-1)).toBe(5000);
  });

  it.each([
    ['visible', () => { setVisibility('hidden'); setVisibility('visible'); }],
    ['online', () => { win.dispatchEvent(new Event('online')); }],
    ['pageshow', () => { pageshow(false); }],
  ])('reconnects at once on %s and resets the backoff', async (_name, trigger) => {
    const { connection } = harness();
    await failTimes(5);
    const waiting = FakeSocket.all.length;
    (FakeSocket.all.at(-1) as FakeSocket).close();
    expect(connection.getStatus().link).toBe('reconnecting');

    trigger();
    expect(FakeSocket.all).toHaveLength(waiting + 1);
    expect(connection.getStatus().link).toBe('reconnecting');
    // The pending 5 s retry is gone, and the next failure retries at the shortest delay.
    expect(await failTimes(1)).toEqual([RECONNECT_DELAYS_MS[0]]);
  });

  it('abandons a stuck in-flight attempt when the page comes back', async () => {
    const { connection } = harness();
    await failTimes(2);
    const stuck = FakeSocket.all.at(-1) as FakeSocket;
    setVisibility('visible');
    expect(stuck.readyState).toBe(3);
    expect(FakeSocket.all.at(-1)).not.toBe(stuck);
    expect(connection.getStatus().link).toBe('reconnecting');
    // The stuck socket's own close scheduled nothing more.
    await vi.advanceTimersByTimeAsync(ATTACH_TIMEOUT_MS - 1000);
    expect(FakeSocket.all.at(-2)).toBe(stuck);
  });

  it('leaves a live socket alone after a short hide', async () => {
    const { socket } = harness();
    await attachLive(socket());
    setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(RESUME_CHECK_MS - 1000);
    setVisibility('visible');
    pageshow(false);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it('reopens a live socket after a long hide, as an observer, without resending input', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    await takeControl(connection, first);
    connection.sendText('rm -i notes.txt');
    setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(RESUME_CHECK_MS + 1000);
    setVisibility('visible');
    // First it checks the old socket; this one stays silent (half-open).
    expect(FakeSocket.all).toHaveLength(1);
    expect(first.types().at(-1)).toBe('screen.resync');
    await vi.advanceTimersByTimeAsync(RESUME_PROBE_MS);

    const second = socket();
    expect(second).not.toBe(first);
    expect(first.readyState).toBe(3);
    expect(connection.getStatus()).toMatchObject({ link: 'reconnecting', control: 'observer', notice: DELIVERY_UNCERTAIN });
    expect(connection.sendText('more')).toBe(false);
    await attachLive(second, '9');
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 3);
    expect(connection.getStatus()).toMatchObject({ link: 'live', control: 'observer' });
    expect(second.types().filter((type) => type.startsWith('input.') || type === 'control.acquire')).toEqual([]);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it('drops a connect that is not live in time and retries at once', async () => {
    const { connection } = harness();
    const hanging = FakeSocket.all.at(-1) as FakeSocket;
    await vi.advanceTimersByTimeAsync(ATTACH_TIMEOUT_MS - 1);
    expect(FakeSocket.all).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(hanging.readyState).toBe(3);
    expect(connection.getStatus().link).toBe('reconnecting');
    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it('does not retry while hidden and reconnects at once when visible', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    setVisibility('hidden');
    first.close();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeSocket.all).toHaveLength(1);
    expect(connection.getStatus().link).toBe('reconnecting');
    setVisibility('visible');
    expect(FakeSocket.all).toHaveLength(2);
  });

  it('keeps a live socket that answers the check after a long hide', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(RESUME_CHECK_MS + 1000);
    setVisibility('visible');
    expect(first.types().at(-1)).toBe('screen.resync');
    snapshot(first, '2');
    await vi.advanceTimersByTimeAsync(RESUME_PROBE_MS * 2);
    expect(FakeSocket.all).toHaveLength(1);
    expect(first.readyState).toBe(1);
    expect(connection.getStatus().link).toBe('live');
  });

  it('reopens a live socket on bfcache restore and on online', async () => {
    const { socket } = harness();
    await attachLive(socket());
    pageshow(true);
    expect(FakeSocket.all).toHaveLength(2);
    await attachLive(socket());
    win.dispatchEvent(new Event('online'));
    expect(FakeSocket.all).toHaveLength(3);
  });

  it('removes its listeners on detach', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.detach();
    setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(RESUME_CHECK_MS * 2);
    setVisibility('visible');
    pageshow(true);
    win.dispatchEvent(new Event('online'));
    expect(FakeSocket.all).toHaveLength(1);
    expect(connection.getStatus().link).toBe('closed');
  });
});

describe('AppScroll', () => {
  it('sends every step of a swipe at its start cell, even after the finger leaves the grid', () => {
    const sent: Array<[string, unknown]> = [];
    const send = (button: string, cell: unknown) => { sent.push([button, cell]); };
    const scroll = new AppScroll();
    scroll.start({ row: 3, column: 14 });
    scroll.move(40, send);
    scroll.move(-80, send);
    expect(sent).toEqual([
      ['wheel-up', { row: 3, column: 14 }],
      ['wheel-down', { row: 3, column: 14 }],
      ['wheel-down', { row: 3, column: 14 }],
    ]);
    sent.length = 0;
    scroll.wheel(-36, { row: 1, column: 2 }, send);
    expect(sent).toEqual([['wheel-down', { row: 1, column: 2 }]]);
  });
});

describe('TerminalView.cellAt', () => {
  // A 20x5 grid of 10x20 px cells at (100, 200), showing its base line.
  const view = Object.assign(Object.create(TerminalView.prototype) as object, {
    screen: () => ({ clientWidth: 200, clientHeight: 100, getBoundingClientRect: () => ({ left: 100, top: 200 }) }),
    terminal: { cols: 20, rows: 5, buffer: { active: { viewportY: 0, baseY: 0 } } },
  }) as unknown as TerminalView;

  it('gives the cell under a point, and the nearest cell for a point outside only with clamp', () => {
    expect(view.cellAt(245, 265)).toEqual({ row: 3, column: 14 });
    expect(view.cellAt(245, 400)).toBeUndefined();
    expect(view.cellAt(245, 400, true)).toEqual({ row: 4, column: 14 });
    expect(view.cellAt(50, 150, true)).toEqual({ row: 0, column: 0 });
  });
});

describe('followCursor', () => {
  it('scrolls only as far as needed to show the cursor row', () => {
    expect(followCursor(0, 300, { top: 100, bottom: 117 })).toBe(0);
    expect(followCursor(0, 300, { top: 580, bottom: 597 })).toBe(297);
    expect(followCursor(297, 300, { top: 34, bottom: 51 })).toBe(34);
  });
});

describe('wheelSteps', () => {
  it('sends one step per step of travel and carries the rest', () => {
    expect(wheelSteps(0, 20, 36)).toEqual({ steps: 0, carry: 20 });
    expect(wheelSteps(20, 20, 36)).toEqual({ steps: 1, carry: 4 });
    expect(wheelSteps(0, -80, 36)).toEqual({ steps: -2, carry: -8 });
  });

  it('caps a flick and drops the overflow instead of queueing it', () => {
    expect(wheelSteps(0, 1000, 36)).toEqual({ steps: MAX_WHEEL_STEPS, carry: 0 });
    expect(wheelSteps(0, -1000, 36)).toEqual({ steps: -MAX_WHEEL_STEPS, carry: 0 });
  });
});

describe('Files', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const root = '/Users/me/app';
  const listed = (path = '', extra: object = {}) => ({
    state: 'ready', root, path, entries: [{ name: 'src', kind: 'dir' }, { name: 'a.ts', kind: 'file', size: 12 }], truncated: false, ...extra,
  });
  const content = (path = 'a.ts', text = 'const a = 1;\n') => ({ state: 'ready', path, size: text.length, text, truncated: false });
  const found = (query = 'q', paths: string[] = ['a.ts']) => ({ state: 'ready', query, paths, truncated: false });
  /** The files requests sent so far, as "type payload-value". */
  const asked = (socket: FakeSocket) => socket.sent
    .filter((message) => message.type.startsWith('files.'))
    .map((message) => `${message.type} ${String(message.payload.path ?? message.payload.query)}`);
  const last = (socket: FakeSocket, type: string) => socket.sent.filter((message) => message.type === type).at(-1);

  it('returns false when it is not live and sends nothing', async () => {
    const { connection, socket } = harness();
    expect(connection.openFilesFolder('')).toBe(false);
    expect(connection.openFilesFile('a.ts')).toBe(false);
    expect(connection.getFiles()).toBeUndefined();
    socket().open();
    expect(connection.openFilesFolder('')).toBe(false);
    expect(asked(socket())).toEqual([]);
  });

  it('lists the root as an observer and shows the reply for its own request only', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    expect(connection.getStatus().control).toBe('observer');
    expect(connection.openFilesFolder('')).toBe(true);
    const request = last(socket(), 'files.list');
    expect(request).toMatchObject({ sessionId, generation, payload: { path: '' } });
    expect(connection.getFiles()).toEqual({
      root: undefined, folder: { path: '', state: 'loading', listed: undefined }, file: undefined, search: undefined,
    });

    socket().receive('files.listed', listed());
    expect(connection.getFiles()?.folder.state).toBe('loading');
    expect(connection.getFiles()?.root).toBeUndefined();
    expect(asked(socket())).toEqual(['files.list ']);

    socket().reply(request, 'files.listed', listed());
    expect(connection.getFiles()).toMatchObject({ root, folder: { path: '', state: 'ready', listed: { entries: [{ name: 'src' }, { name: 'a.ts' }] } } });

    connection.closeFiles();
    expect(connection.getFiles()).toBeUndefined();
  });

  it('sends one files request at a time, then the next wanted one: file, search, folder', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    connection.openFilesFolder('web');
    connection.openFilesFile('web/a.ts', 3);
    connection.searchFiles('q');
    expect(asked(socket())).toEqual(['files.list ']);

    socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
    expect(asked(socket())).toEqual(['files.list ', 'files.read web/a.ts']);
    expect(connection.getFiles()?.folder).toMatchObject({ path: 'web', state: 'loading' });

    socket().reply(last(socket(), 'files.read'), 'files.content', content('web/a.ts'));
    expect(asked(socket())).toEqual(['files.list ', 'files.read web/a.ts', 'files.search q']);
    expect(connection.getFiles()?.file).toMatchObject({ path: 'web/a.ts', line: 3, state: 'ready', content: { text: 'const a = 1;\n' } });

    socket().reply(last(socket(), 'files.search'), 'files.found', found());
    expect(asked(socket())).toEqual(['files.list ', 'files.read web/a.ts', 'files.search q', 'files.list web']);
    expect(connection.getFiles()?.search).toMatchObject({ query: 'q', state: 'ready', found: { paths: ['a.ts'] } });

    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('web'));
    expect(connection.getFiles()?.folder).toMatchObject({ path: 'web', state: 'ready' });
    expect(asked(socket())).toHaveLength(4);
  });

  it('keeps only the latest file: a reply for an older path is dropped and the newer one is sent', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
    connection.openFilesFile('a.ts');
    const first = last(socket(), 'files.read');
    connection.openFilesFile('b.ts');
    expect(asked(socket())).toEqual(['files.list ', 'files.read a.ts']);

    socket().reply(first, 'files.content', content('a.ts', 'old'));
    expect(connection.getFiles()?.file).toEqual({ path: 'b.ts', line: undefined, state: 'loading', content: undefined });
    expect(asked(socket())).toEqual(['files.list ', 'files.read a.ts', 'files.read b.ts']);
    socket().reply(last(socket(), 'files.read'), 'files.content', content('b.ts', 'new'));
    expect(connection.getFiles()?.file).toMatchObject({ path: 'b.ts', state: 'ready', content: { text: 'new' } });
  });

  it('keeps only the latest search and the latest folder the same way', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
    connection.searchFiles('q');
    connection.searchFiles('qu');
    socket().reply(last(socket(), 'files.search'), 'files.found', found('q', ['old.ts']));
    expect(connection.getFiles()?.search).toEqual({ query: 'qu', state: 'loading', found: undefined });
    expect(asked(socket()).at(-1)).toBe('files.search qu');
    socket().reply(last(socket(), 'files.search'), 'files.found', found('qu', ['new.ts']));
    expect(connection.getFiles()?.search).toMatchObject({ query: 'qu', state: 'ready', found: { paths: ['new.ts'] } });

    connection.openFilesFolder('a');
    connection.openFilesFolder('b');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('a'));
    expect(connection.getFiles()?.folder).toMatchObject({ path: 'b', state: 'loading' });
    expect(asked(socket()).at(-1)).toBe('files.list b');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('b'));
    expect(connection.getFiles()?.folder).toMatchObject({ path: 'b', state: 'ready' });
  });

  it('ends a search with an empty query and forgets it', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
    connection.searchFiles('q');
    socket().reply(last(socket(), 'files.search'), 'files.found', found());
    connection.searchFiles('');
    expect(connection.getFiles()?.search).toBeUndefined();
    expect(asked(socket())).toHaveLength(2);
  });

  it('closes the open file without a request, and Back shows the folder', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('src');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('src'));
    connection.openFilesFile('src/a.ts');
    socket().reply(last(socket(), 'files.read'), 'files.content', content('src/a.ts'));
    connection.closeFilesFile();
    expect(connection.getFiles()).toMatchObject({ file: undefined, folder: { path: 'src', state: 'ready' } });
    expect(asked(socket())).toHaveLength(2);
  });

  it('lists the folder of a file opened without a Files view, and keeps the line', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    expect(connection.openFilesFile('src/a/b.ts', 7)).toBe(true);
    expect(last(socket(), 'files.read')?.payload).toEqual({ path: 'src/a/b.ts' });
    expect(connection.getFiles()).toEqual({
      root: undefined,
      folder: { path: 'src/a', state: 'loading', listed: undefined },
      file: { path: 'src/a/b.ts', line: 7, state: 'loading', content: undefined },
      search: undefined,
    });
    socket().reply(last(socket(), 'files.read'), 'files.content', content('src/a/b.ts'));
    expect(asked(socket())).toEqual(['files.read src/a/b.ts', 'files.list src/a']);
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('src/a'));
    expect(connection.getFiles()).toMatchObject({
      root,
      folder: { path: 'src/a', state: 'ready' },
      file: { path: 'src/a/b.ts', line: 7, state: 'ready' },
    });
  });

  it('lists the root for a file in the root', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFile('a.ts');
    expect(connection.getFiles()?.folder).toMatchObject({ path: '', state: 'loading' });
  });

  it('stores the root a listing names, even when it names no folder of the view', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('', { root: 'C:/Users/me/app' }));
    expect(connection.getFiles()?.root).toBe('C:/Users/me/app');
    // A listing that does not name the root keeps the known one.
    connection.openFilesFolder('src');
    socket().reply(last(socket(), 'files.list'), 'files.listed', { state: 'unavailable', path: 'src', entries: [], truncated: false });
    expect(connection.getFiles()).toMatchObject({ root: 'C:/Users/me/app', folder: { state: 'ready', listed: { state: 'unavailable' } } });
  });

  it('marks only the refused request unavailable, keeps the socket open and sends the next', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    connection.openFilesFolder('');
    connection.openFilesFile('a.ts');
    connection.searchFiles('q');
    expect(asked(first)).toEqual(['files.list ']);

    first.reply(last(first, 'files.list'), 'error', { code: 'limit_exceeded', message: 'busy' });
    expect(connection.getFiles()).toMatchObject({
      folder: { state: 'unavailable' }, file: { state: 'loading' }, search: { state: 'loading' },
    });
    expect(first.readyState).toBe(1);
    expect(connection.getStatus()).toMatchObject({ link: 'live', notice: 'busy' });
    expect(asked(first)).toEqual(['files.list ', 'files.read a.ts']);

    first.reply(last(first, 'files.read'), 'error', { code: 'internal_error', message: 'read failed' });
    expect(connection.getFiles()).toMatchObject({
      folder: { state: 'unavailable' }, file: { state: 'unavailable' }, search: { state: 'loading' },
    });
    expect(asked(first)).toEqual(['files.list ', 'files.read a.ts', 'files.search q']);

    first.reply(last(first, 'files.search'), 'error', { code: 'internal_error', message: 'search failed' });
    expect(connection.getFiles()).toMatchObject({
      folder: { state: 'unavailable' }, file: { state: 'unavailable' }, search: { state: 'unavailable' },
    });
    expect(first.readyState).toBe(1);
    expect(asked(first)).toHaveLength(3);
  });

  it('does not end loading for an error to another request', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    socket().receive('error', { code: 'internal_error', message: 'other' });
    expect(connection.getFiles()?.folder.state).toBe('loading');
  });

  it('can load again after a refused request', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    socket().reply(last(socket(), 'files.list'), 'error', { code: 'internal_error', message: 'x' });
    connection.openFilesFolder('');
    expect(asked(socket())).toEqual(['files.list ', 'files.list ']);
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
    expect(connection.getFiles()?.folder.state).toBe('ready');
  });

  it('ends loading when the socket closes and ignores replies from the old socket', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    connection.openFilesFolder('');
    connection.openFilesFile('a.ts');
    connection.searchFiles('q');
    const request = last(first, 'files.list');
    first.close();
    expect(connection.getFiles()).toMatchObject({
      folder: { state: 'unavailable' }, file: { state: 'unavailable' }, search: { state: 'unavailable' },
    });

    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    const second = socket();
    await attachLive(second, '2');
    first.reply(request, 'files.listed', listed());
    second.reply(request, 'files.listed', listed());
    expect(connection.getFiles()?.folder.state).toBe('unavailable');
    expect(connection.getFiles()?.root).toBeUndefined();
    expect(asked(second)).toEqual([]);

    connection.openFilesFolder('');
    second.reply(last(second, 'files.list'), 'files.listed', listed());
    expect(connection.getFiles()).toMatchObject({ root, folder: { state: 'ready' }, file: undefined });
  });

  describe('filesRoot', () => {
    it('asks for a root listing when the root is unknown and resolves with the root', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      const answer = connection.filesRoot();
      expect(last(socket(), 'files.list')).toMatchObject({ sessionId, generation, payload: { path: '' } });
      expect(connection.getFiles()).toBeUndefined();
      socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
      await expect(answer).resolves.toBe(root);
    });

    it('answers from the last listing without a request while the cwd is unchanged', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      socket().receive('session.state', { state: 'running', cwd: `${root}/web` });
      const first = connection.filesRoot();
      socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
      await expect(first).resolves.toBe(root);
      expect(asked(socket())).toHaveLength(1);

      await expect(connection.filesRoot()).resolves.toBe(root);
      socket().receive('session.state', { state: 'running', cwd: `${root}/web` });
      await expect(connection.filesRoot()).resolves.toBe(root);
      expect(asked(socket())).toHaveLength(1);
    });

    it('asks again after the session changes its cwd', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      socket().receive('session.state', { state: 'running', cwd: `${root}/web` });
      const first = connection.filesRoot();
      socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
      await first;

      socket().receive('session.state', { state: 'running', cwd: '/Users/me/other' });
      const second = connection.filesRoot();
      expect(asked(socket())).toHaveLength(2);
      socket().reply(last(socket(), 'files.list'), 'files.listed', listed('', { root: '/Users/me/other' }));
      await expect(second).resolves.toBe('/Users/me/other');
    });

    it('gives the known root to a view opened later', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      const answer = connection.filesRoot();
      socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
      await answer;
      connection.openFilesFolder('');
      expect(connection.getFiles()?.root).toBe(root);
    });

    it('waits for the request in flight, then asks for the root', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      connection.openFilesFile('a.ts');
      connection.closeFiles();
      const answer = connection.filesRoot();
      expect(asked(socket())).toEqual(['files.read a.ts']);
      socket().reply(last(socket(), 'files.read'), 'files.content', content());
      expect(asked(socket())).toEqual(['files.read a.ts', 'files.list ']);
      socket().reply(last(socket(), 'files.list'), 'files.listed', listed());
      await expect(answer).resolves.toBe(root);
    });

    it('resolves undefined when the host refuses the listing', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      const answer = connection.filesRoot();
      socket().reply(last(socket(), 'files.list'), 'error', { code: 'internal_error', message: 'x' });
      await expect(answer).resolves.toBeUndefined();
    });

    it('resolves undefined when the listing names no root', async () => {
      const { connection, socket } = harness();
      await attachLive(socket());
      const answer = connection.filesRoot();
      socket().reply(last(socket(), 'files.list'), 'files.listed', { state: 'unavailable', path: '', entries: [], truncated: false });
      await expect(answer).resolves.toBeUndefined();
    });

    it('resolves undefined when the socket closes, and when it is not live', async () => {
      const { connection, socket } = harness();
      await expect(connection.filesRoot()).resolves.toBeUndefined();
      await attachLive(socket());
      const answer = connection.filesRoot();
      socket().close();
      await expect(answer).resolves.toBeUndefined();
      await expect(connection.filesRoot()).resolves.toBeUndefined();
    });
  });

  it('fails a request with no reply after the timeout and sends the next one', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFolder('');
    connection.openFilesFile('a.ts', 4);
    expect(asked(socket())).toEqual(['files.list ']);
    await vi.advanceTimersByTimeAsync(FILES_TIMEOUT_MS - 1);
    expect(connection.getFiles()?.folder.state).toBe('loading');
    await vi.advanceTimersByTimeAsync(1);
    expect(connection.getFiles()?.folder.state).toBe('unavailable');
    expect(asked(socket())).toEqual(['files.list ', 'files.read a.ts']);
    // A late reply to the old request changes nothing.
    socket().reply(socket().sent.find((message) => message.type === 'files.list'), 'files.listed', listed());
    expect(connection.getFiles()?.folder.state).toBe('unavailable');
    socket().reply(last(socket(), 'files.read'), 'files.content', content());
    expect(connection.getFiles()?.file).toMatchObject({ state: 'ready', line: 4 });
    await vi.advanceTimersByTimeAsync(FILES_TIMEOUT_MS);
    expect(connection.getFiles()?.file?.state).toBe('ready');
  });

  it('lists a tapped path that turns out to be a folder', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    connection.openFilesFile('src', 3);
    socket().reply(last(socket(), 'files.read'), 'files.content', { state: 'not_file', path: 'src', truncated: false });
    expect(connection.getFiles()).toMatchObject({ file: undefined, folder: { path: 'src', state: 'loading' } });
    expect(asked(socket()).at(-1)).toBe('files.list src');
  });

  it('ties a root to the cwd the request was sent with', async () => {
    const { connection, socket } = harness();
    await attachLive(socket());
    socket().receive('session.state', { state: 'running', cwd: `${root}/web` });
    const answer = connection.filesRoot();
    const old = last(socket(), 'files.list');
    socket().receive('session.state', { state: 'running', cwd: '/Users/me/other' });
    socket().reply(old, 'files.listed', listed());
    // The reply is for the old cwd: not known for the new one, and asked again.
    expect(connection.knownFilesRoot()).toBeUndefined();
    expect(asked(socket())).toEqual(['files.list ', 'files.list ']);
    socket().reply(last(socket(), 'files.list'), 'files.listed', listed('', { root: '/Users/me/other' }));
    await expect(answer).resolves.toBe('/Users/me/other');
    expect(connection.knownFilesRoot()).toBe('/Users/me/other');
  });
});

describe('Connection for multi-computer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('attaches a read-only thumbnail viewer that never takes control', async () => {
    const { connection, socket } = harness({ thumbnail: { maxUpdatesPerSecond: 5 } });
    await attachLive(socket());
    const attach = socket().sent.find((message) => message.type === 'session.attach');
    expect(attach?.payload).toEqual({ thumbnail: { maxUpdatesPerSecond: 5 } });
    connection.takeControl();
    expect(socket().types()).not.toContain('control.acquire');
    expect(connection.sendText('ls')).toBe(false);
  });

  it('attaches a full viewer with an empty payload by default', async () => {
    const { socket } = harness();
    await attachLive(socket());
    expect(socket().sent.find((message) => message.type === 'session.attach')?.payload).toEqual({});
  });

  it('reports the size of every host message', async () => {
    const sizes: number[] = [];
    const { socket } = harness({ onMessageBytes: (bytes) => sizes.push(bytes) });
    await attachLive(socket());
    expect(sizes).toHaveLength(4);
    expect(sizes.every((size) => size > 50)).toBe(true);
  });

  it('opens sockets only when the gate admits them and reports how each attempt ended', async () => {
    const scheduler = new Scheduler({ concurrentConnects: 1 }, { random: () => 0.5 });
    const first = harness({ gate: scheduler.connectGate('https://a.tailx.ts.net') });
    const second = harness({ gate: scheduler.connectGate('https://b.tailx.ts.net') });
    expect(FakeSocket.all).toHaveLength(1);
    expect(second.connection.getStatus().link).toBe('connecting');
    await attachLive(first.socket());
    expect(first.connection.getStatus().link).toBe('live');
    // The first attempt went live, so the second may start.
    expect(FakeSocket.all).toHaveLength(2);
    second.socket().close();
    // A failed attempt marks the computer for a jittered backoff before the next background try.
    await vi.advanceTimersByTimeAsync(RECONNECT_DELAYS_MS[0]);
    expect(FakeSocket.all).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeSocket.all).toHaveLength(3);
  });

  it('cancels a waiting attempt on detach and frees a held slot', async () => {
    const scheduler = new Scheduler({ concurrentConnects: 1 });
    const first = harness({ gate: scheduler.connectGate('https://a.tailx.ts.net') });
    const second = harness({ gate: scheduler.connectGate('https://b.tailx.ts.net') });
    second.connection.detach();
    first.connection.detach();
    const third = harness({ gate: scheduler.connectGate('https://c.tailx.ts.net') });
    expect(FakeSocket.all).toHaveLength(2);
    expect(third.socket().url).toBe('wss://host.invalid/api/v1/ws');
  });
});
