import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clampGrid,
  Connection,
  DELIVERY_UNCERTAIN,
  HEARTBEAT_MS,
  rowText,
  type ScreenDisplay,
} from './Connection';
import { fitGrid, followCursor } from './TerminalView';
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
  sent: Array<{ type: string; payload: Record<string, unknown>; sessionId?: string; generation?: string }> = [];
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

  receive(type: string, payload: object, identity: { sessionId?: string; generation?: string } | null = { sessionId, generation }) {
    this.onmessage?.({ data: JSON.stringify({ version: 1, type, requestId: id(99), ...(identity ?? {}), payload }) });
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

function harness() {
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

  it('reports uncertain delivery, reconnects as an observer, and never replays input or control', async () => {
    const { connection, socket } = harness();
    const first = socket();
    await attachLive(first);
    await takeControl(connection, first);
    connection.sendText('rm -i notes.txt');
    first.close();

    expect(connection.getStatus()).toMatchObject({ link: 'reconnecting', control: 'observer', notice: DELIVERY_UNCERTAIN });
    expect(connection.sendText('more')).toBe(false);
    await vi.advanceTimersByTimeAsync(499);
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

describe('followCursor', () => {
  it('scrolls only as far as needed to show the cursor row', () => {
    expect(followCursor(0, 300, { top: 100, bottom: 117 })).toBe(0);
    expect(followCursor(0, 300, { top: 580, bottom: 597 })).toBe(297);
    expect(followCursor(297, 300, { top: 34, bottom: 51 })).toBe(34);
  });
});
