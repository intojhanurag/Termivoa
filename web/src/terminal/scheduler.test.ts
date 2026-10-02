import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_LIMITS, Scheduler, type PollOutcome, type TileState } from './scheduler';

const flush = () => vi.advanceTimersByTimeAsync(0);

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function tiles(scheduler: Scheduler, count: number, fullRate: (index: number) => boolean = () => false) {
  const changes: Array<[string, TileState]> = [];
  const handles = Array.from({ length: count }, (_, index) => scheduler.requestLiveTile(`h/${index}`, {
    fullRate: fullRate(index),
    onChange: (state) => changes.push([`h/${index}`, state]),
  }));
  const states = () => handles.map((handle) => handle.state());
  return { handles, changes, states };
}

describe('live tiles', () => {
  it('starts at 4 live thumbnails; the rest wait as Paused and go live when a slot opens', async () => {
    const scheduler = new Scheduler();
    expect(DEFAULT_LIMITS.liveTiles).toBe(4);
    const { handles, changes, states } = tiles(scheduler, 6);
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

  it('counts a tile on a computer without thumbnail mode as full rate', () => {
    const { states } = tiles(new Scheduler(), 4, (index) => index === 0);
    expect(states()).toEqual(['live', 'live', 'live', 'paused']);
  });

  it('suspends every tile while a terminal is active or the app is in the background', async () => {
    const scheduler = new Scheduler();
    const { states } = tiles(scheduler, 2);
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
    const { handles, states } = tiles(scheduler, 5);
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
    const old = scheduler.requestLiveTile('k', { onChange: () => undefined });
    const fresh = scheduler.requestLiveTile('k', { onChange: () => undefined });
    old.release();
    expect(fresh.state()).toBe('live');
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
