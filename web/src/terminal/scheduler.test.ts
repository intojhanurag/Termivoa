import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_LIMITS, Scheduler, type PollOutcome, type TileState } from './scheduler';

const flush = () => vi.advanceTimersByTimeAsync(0);
const ORIGIN = 'https://h.t.ts.net';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

/** `count` tiles, each reported visible by its observer; changes recorded after that. */
async function tiles(scheduler: Scheduler, count: number) {
  const changes: Array<[string, TileState]> = [];
  const handles = Array.from({ length: count }, (_, index) => scheduler.requestLiveTile(`h/${index}`, {
    origin: ORIGIN,
    onChange: (state) => changes.push([`h/${index}`, state]),
  }));
  for (const handle of handles) handle.setVisible(true);
  await flush();
  changes.length = 0;
  const states = () => handles.map((handle) => handle.state());
  return { handles, changes, states };
}

describe('live tiles', () => {
  it('starts at 4 live thumbnails; the rest wait as Paused and go live when a slot opens', async () => {
    const scheduler = new Scheduler();
    expect(DEFAULT_LIMITS.liveTiles).toBe(4);
    const { handles, changes, states } = await tiles(scheduler, 6);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused', 'paused']);
    await flush();
    expect(changes).toEqual([]);
    handles[1]?.setVisible(false);
    expect(states()).toEqual(['live', 'paused', 'live', 'live', 'live', 'paused']);
    handles[0]?.release();
    await flush();
    expect(states().slice(1)).toEqual(['paused', 'live', 'live', 'live', 'live']);
    expect(changes).toEqual([['h/1', 'paused'], ['h/4', 'live'], ['h/5', 'live']]);
  });

  it('starts a new tile paused until its observer reports it visible', async () => {
    const scheduler = new Scheduler();
    const below = Array.from({ length: 5 }, (_, index) => scheduler.requestLiveTile(`h/${index}`, { origin: ORIGIN, onChange: () => undefined }));
    expect(below.map((handle) => handle.state())).toEqual(['paused', 'paused', 'paused', 'paused', 'paused']);
    below[4]?.setVisible(true);
    expect(below.map((handle) => handle.state())).toEqual(['paused', 'paused', 'paused', 'paused', 'live']);
  });

  it('suspends every tile while a terminal is active or the app is in the background', async () => {
    const scheduler = new Scheduler();
    const { states } = await tiles(scheduler, 2);
    scheduler.setActive('h/9');
    expect(states()).toEqual(['paused', 'paused']);
    scheduler.setActive(undefined);
    scheduler.setForeground(false);
    expect(states()).toEqual(['paused', 'paused']);
    scheduler.setForeground(true);
    expect(states()).toEqual(['live', 'live']);
  });

  it('pauses the noisiest tile over the bytes budget and resumes it after the pause', async () => {
    const scheduler = new Scheduler({ tileBytesPerSecond: 1000, bytePauseMs: 5000 });
    const { handles, states } = await tiles(scheduler, 5);
    handles[0]?.bytes(300);
    handles[2]?.bytes(600);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused']);
    handles[1]?.bytes(200);
    expect(states()).toEqual(['live', 'live', 'paused', 'live', 'live']);
    await vi.advanceTimersByTimeAsync(5000);
    // Back when a slot opens; live tiles keep theirs.
    expect(states()[2]).toBe('paused');
    handles[4]?.release();
    expect(states()[2]).toBe('live');
  });

  it('ignores a released or replaced handle', () => {
    const scheduler = new Scheduler({ liveTiles: 1 });
    const old = scheduler.requestLiveTile('k', { origin: ORIGIN, onChange: () => undefined });
    const fresh = scheduler.requestLiveTile('k', { origin: ORIGIN, onChange: () => undefined });
    fresh.setVisible(true);
    old.release();
    expect(fresh.state()).toBe('live');
  });
});

describe('snapshot turns for tiles that were never live', () => {
  const after = (ms = DEFAULT_LIMITS.peekDelayMs) => vi.advanceTimersByTimeAsync(ms);

  it('gives the 2 tiles beyond the live limit one turn each, one at a time, and keeps the 4 live', async () => {
    const scheduler = new Scheduler();
    const { handles, states } = await tiles(scheduler, 6);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused', 'paused']);
    await after(DEFAULT_LIMITS.peekDelayMs - 1);
    expect(states()[4]).toBe('paused');
    await after(1);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'peek', 'paused']);
    // Never more than 4 live plus 1 turn open.
    expect(states().filter((state) => state !== 'paused').length).toBeLessThanOrEqual(DEFAULT_LIMITS.tileConnections);
    await after(5000);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'peek', 'paused']);
    handles[4]?.peekDone();
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused', 'paused']);
    await after();
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused', 'peek']);
    handles[5]?.peekDone();
    await after(60_000);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused', 'paused']);
  });

  it('does not start a turn for a tile that is offscreen, or while a terminal is active or the app is in the background', async () => {
    const scheduler = new Scheduler();
    const { handles, states } = await tiles(scheduler, 5);
    handles[4]?.setVisible(false);
    await after(10_000);
    expect(states()[4]).toBe('paused');
    handles[4]?.setVisible(true);
    scheduler.setForeground(false);
    await after(10_000);
    expect(states()).toEqual(['paused', 'paused', 'paused', 'paused', 'paused']);
    scheduler.setActive('h/9');
    scheduler.setForeground(true);
    await after(10_000);
    expect(states()).toEqual(['paused', 'paused', 'paused', 'paused', 'paused']);
    scheduler.setActive(undefined);
    expect(states()).toEqual(['live', 'live', 'live', 'live', 'paused']);
    await after();
    expect(states()[4]).toBe('peek');
  });

  it('ends a running turn when the tile goes offscreen, the app goes to the background, or a terminal opens', async () => {
    const scheduler = new Scheduler();
    const { handles, states } = await tiles(scheduler, 5);
    await after();
    expect(states()[4]).toBe('peek');
    handles[4]?.setVisible(false);
    expect(states()[4]).toBe('paused');
    handles[4]?.setVisible(true);
    await after();
    expect(states()[4]).toBe('peek');
    scheduler.setForeground(false);
    expect(states()[4]).toBe('paused');
    scheduler.setForeground(true);
    await after();
    expect(states()[4]).toBe('peek');
    scheduler.setActive('h/9');
    expect(states()).toEqual(['paused', 'paused', 'paused', 'paused', 'paused']);
  });

  it('leaves a failed tile as it is: a turn that is over is not repeated', async () => {
    const scheduler = new Scheduler();
    const { handles, changes, states } = await tiles(scheduler, 5);
    await after();
    handles[4]?.peekDone();
    await after(60_000);
    expect(states()[4]).toBe('paused');
    expect(changes.filter(([, state]) => state === 'peek')).toEqual([['h/4', 'peek']]);
    // Back on screen again: still no second turn.
    handles[4]?.setVisible(false);
    handles[4]?.setVisible(true);
    await after(60_000);
    expect(states()[4]).toBe('paused');
  });

  it('ends a turn that takes longer than peekMs or receives more than peekBytes', async () => {
    const scheduler = new Scheduler({ peekMs: 3000, peekBytes: 1000 });
    const { handles, states } = await tiles(scheduler, 6);
    await after();
    expect(states().slice(4)).toEqual(['peek', 'paused']);
    await after(2999);
    expect(states().slice(4)).toEqual(['peek', 'paused']);
    await after(1);
    expect(states().slice(4)).toEqual(['paused', 'paused']);
    await after();
    expect(states().slice(4)).toEqual(['paused', 'peek']);
    handles[5]?.bytes(600);
    handles[5]?.bytes(600);
    expect(states().slice(4)).toEqual(['paused', 'paused']);
    await after(60_000);
    expect(states().slice(4)).toEqual(['paused', 'paused']);
  });

  it('waits while any connection attempt runs or waits, so live tiles connect first', async () => {
    const scheduler = new Scheduler();
    const { states } = await tiles(scheduler, 5);
    const gate = scheduler.connectGate(ORIGIN);
    gate.wait(() => undefined);
    await after(10_000);
    expect(states()[4]).toBe('paused');
    gate.done('live');
    await after(DEFAULT_LIMITS.peekDelayMs - 1);
    expect(states()[4]).toBe('paused');
    await after(1);
    expect(states()[4]).toBe('peek');
  });

  it('skips a computer that is backing off, until it is reachable again', async () => {
    const scheduler = new Scheduler({}, { random: () => 1 });
    const down = 'https://down.t.ts.net';
    const gate = scheduler.connectGate(down);
    gate.wait(() => gate.done('failed'));
    const { states } = await tiles(scheduler, 4);
    const away = scheduler.requestLiveTile('down/1', { origin: down, onChange: () => undefined });
    const near = scheduler.requestLiveTile('h/9', { origin: ORIGIN, onChange: () => undefined });
    away.setVisible(true);
    near.setVisible(true);
    expect(states()).toEqual(['live', 'live', 'live', 'live']);
    await after();
    expect([away.state(), near.state()]).toEqual(['paused', 'peek']);
    near.peekDone();
    await after(100);
    expect(away.state()).toBe('paused');
    scheduler.retry(down);
    await after();
    expect(away.state()).toBe('peek');
  });

  it('ends the turn when the tile becomes live', async () => {
    const scheduler = new Scheduler();
    const { handles, states } = await tiles(scheduler, 5);
    await after();
    expect(states()[4]).toBe('peek');
    handles[0]?.release();
    expect(states().slice(1)).toEqual(['live', 'live', 'live', 'live']);
    await after(60_000);
    expect(states().slice(1)).toEqual(['live', 'live', 'live', 'live']);
  });
});

describe('connections', () => {
  it('admits 2 attempts at once; the active terminal never waits', () => {
    const scheduler = new Scheduler();
    const started: string[] = [];
    const gates = ['a', 'b', 'c'].map((name) => scheduler.connectGate(`https://${name}.t.ts.net`));
    gates.forEach((gate, index) => gate.wait(() => started.push(`bg${index}`)));
    scheduler.connectGate('https://d.t.ts.net', true).wait(() => started.push('active'));
    expect(started).toEqual(['bg0', 'bg1', 'active']);
    gates[0]?.done('live');
    expect(started).toEqual(['bg0', 'bg1', 'active']);
    gates[1]?.done('cancelled');
    expect(started).toEqual(['bg0', 'bg1', 'active', 'bg2']);
  });

  it('backs off with jitter, then cools down an offline computer; retry clears it', async () => {
    const scheduler = new Scheduler({}, { random: () => 0 });
    const origin = 'https://vps.t.ts.net';
    const gate = scheduler.connectGate(origin);
    let starts = 0;
    const attempt = () => gate.wait(() => { starts++; gate.done('failed'); });
    attempt();
    expect(starts).toBe(1);
    attempt();
    await vi.advanceTimersByTimeAsync(499);
    expect(starts).toBe(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(starts).toBe(2);
    attempt();
    await vi.advanceTimersByTimeAsync(1000);
    expect(starts).toBe(3);
    expect(scheduler.offline(origin)).toBe(true);
    attempt();
    await vi.advanceTimersByTimeAsync(14_999);
    expect(starts).toBe(3);
    scheduler.retry(origin);
    expect(starts).toBe(4);
  });
});

describe('polling across computers', () => {
  it('publishes each computer as soon as it answers, with at most 3 reads at once', async () => {
    const scheduler = new Scheduler();
    const origins = ['a', 'b', 'c', 'd', 'e'].map((name) => `https://${name}.t.ts.net`);
    const pending = new Map<string, () => void>();
    let open = 0;
    let most = 0;
    const published: string[] = [];
    scheduler.poll(origins, (origin) => new Promise<string>((resolve) => {
      open++;
      most = Math.max(most, open);
      pending.set(origin, () => { open--; resolve(origin); });
    }), (origin) => published.push(origin));
    await flush();
    expect([...pending.keys()]).toEqual(origins.slice(0, 3));
    pending.get(origins[2] as string)?.();
    await flush();
    expect(published).toEqual([origins[2]]);
    expect(pending.has(origins[3] as string)).toBe(true);
    expect(most).toBe(3);
  });

  it('keeps one outstanding poll per computer, retries a failing one later, and stops', async () => {
    const scheduler = new Scheduler({ pollIntervalMs: 1000 }, { random: () => 0.5 });
    const outcomes: Array<PollOutcome<number>> = [];
    let calls = 0;
    let fail = true;
    const stop = scheduler.poll(['https://a.t.ts.net'], async () => {
      calls++;
      if (fail) throw new Error('down');
      return calls;
    }, (_origin, outcome) => outcomes.push(outcome));
    await flush();
    expect(outcomes).toEqual([{ ok: false, error: new Error('down') }]);
    await vi.advanceTimersByTimeAsync(1000);
    expect(calls).toBe(2);
    // Two failures: backoff of 2 s beats the 1 s interval.
    await vi.advanceTimersByTimeAsync(1000);
    expect(calls).toBe(2);
    fail = false;
    await vi.advanceTimersByTimeAsync(1000);
    expect(outcomes.at(-1)).toEqual({ ok: true, value: 3 });
    stop();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls).toBe(3);
  });

  it('polls at most 9 computers and waits while in the background', async () => {
    const scheduler = new Scheduler({ concurrentReads: 20 });
    const seen = new Set<string>();
    scheduler.setForeground(false);
    scheduler.poll(Array.from({ length: 12 }, (_, index) => `https://c${index}.t.ts.net`), async (origin) => {
      seen.add(origin);
    }, () => undefined);
    await flush();
    expect(seen.size).toBe(0);
    scheduler.setForeground(true);
    await flush();
    expect(seen.size).toBe(9);
  });
});

describe('one read in flight per computer', () => {
  it('runs serial reads of one computer one after another, other computers in parallel', async () => {
    const scheduler = new Scheduler();
    const open = new Map<string, number>();
    let most = 0;
    const load = (origin: string, ms: number) => () => {
      open.set(origin, (open.get(origin) ?? 0) + 1);
      most = Math.max(most, open.get(origin) ?? 0);
      return new Promise<string>((resolve) => setTimeout(() => {
        open.set(origin, (open.get(origin) ?? 1) - 1);
        resolve(origin);
      }, ms));
    };
    const done: string[] = [];
    const reads = [
      scheduler.serial('a', load('a', 100)).then(() => done.push('a1')),
      scheduler.serial('a', load('a', 10)).then(() => done.push('a2')),
      scheduler.serial('b', load('b', 50)).then(() => done.push('b1')),
    ];
    await vi.advanceTimersByTimeAsync(200);
    await Promise.all(reads);
    expect(most).toBe(1);
    expect(done).toEqual(['b1', 'a1', 'a2']);
  });

  it('keeps the order after a failed read, and poll shares the line with reloads', async () => {
    const scheduler = new Scheduler();
    const order: string[] = [];
    const failing = scheduler.serial('a', async () => { order.push('fail'); throw new Error('down'); });
    const next = scheduler.serial('a', async () => { order.push('next'); return 1; });
    await expect(failing).rejects.toThrow('down');
    await expect(next).resolves.toBe(1);
    let release!: () => void;
    const slow = scheduler.serial('c', () => new Promise<void>((resolve) => { release = resolve; }));
    const polled: string[] = [];
    const stop = scheduler.poll(['c'], async () => { polled.push('poll'); }, () => undefined);
    await flush();
    expect(polled).toEqual([]);
    release();
    await slow;
    await flush();
    expect(polled).toEqual(['poll']);
    stop();
    expect(order).toEqual(['fail', 'next']);
  });

  it('forgets a removed computer\'s failures', () => {
    const scheduler = new Scheduler({ retryBaseMs: 0, offlineCooldownMs: 0 });
    const gate = scheduler.connectGate('https://gone.t.ts.net');
    for (let index = 0; index < DEFAULT_LIMITS.offlineAfterFailures; index++) {
      gate.wait(() => undefined);
      gate.done('failed');
    }
    expect(scheduler.offline('https://gone.t.ts.net')).toBe(true);
    scheduler.forget('https://gone.t.ts.net');
    expect(scheduler.offline('https://gone.t.ts.net')).toBe(false);
  });
});

describe('reads', () => {
  it('frees the read slot when load throws at once', async () => {
    const scheduler = new Scheduler({ concurrentReads: 1 });
    const broken = scheduler.read((): Promise<number> => { throw new Error('sync'); });
    await expect(broken).rejects.toThrow('sync');
    await expect(scheduler.read(async () => 2)).resolves.toBe(2);
  });
});
