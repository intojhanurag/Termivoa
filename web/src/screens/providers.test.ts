import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/http';
import { Scheduler } from '../terminal/scheduler';
import { group, item, mac, okEntry, providerEntry, ready, settingsWith, snapshot, target, vps } from './providerFixtures';
import {
  attentionCount, findTarget, nextProviderStatus, providerKey, providerSources, providerState, providerTabs, rollUp, shown, wordSummary,
} from './providers';

describe('providerSources', () => {
  it('lists providers only of computers that answered at revision 4 or later', () => {
    const sources = providerSources([
      okEntry(mac),
      okEntry(vps, { protocolRevision: 3 }),
      { computer: { ...vps, host: 'pi' }, status: { kind: 'unreachable' } },
    ]);
    expect(sources.map((source) => `${source.computer.label} ${source.info.id}`)).toEqual(['mac herdr']);
    const { providers: _absent, ...without } = settingsWith();
    expect(providerSources([{ computer: mac, status: { kind: 'ok', settings: without, sessions: [], at: 0 } }, okEntry(vps, { providers: [] })])).toEqual([]);
  });

  it('reads each provider under its own key, never the computer\'s origin', () => {
    expect(providerKey(mac.origin, 'herdr')).toBe('https://mac.tailx.ts.net provider:herdr');
  });
});

describe('a provider failure stays its own', () => {
  it('counts failures under the provider key, so the computer never looks offline', async () => {
    const scheduler = new Scheduler({ retryBaseMs: 0 });
    const key = providerKey(mac.origin, 'herdr');
    for (let attempt = 0; attempt < 4; attempt++) {
      scheduler.reported(key, false);
    }
    expect(scheduler.offline(key)).toBe(true);
    expect(scheduler.offline(mac.origin)).toBe(false);
    // A hung provider read does not hold the computer's own reads.
    void scheduler.serial(key, () => new Promise(() => undefined));
    await expect(scheduler.serial(mac.origin, async () => 'sessions')).resolves.toBe('sessions');
  });

  it('shows "can\'t reach" at once before the first answer or when asked, else only once offline', () => {
    const error = new ApiError(0, 'network', 'x');
    const okStatus = { kind: 'ok', snapshot: snapshot(), at: 1 } as const;
    expect(nextProviderStatus({ kind: 'loading' }, { ok: false, error }, false, 2)).toEqual({ kind: 'unreachable' });
    expect(nextProviderStatus(okStatus, { ok: false, error }, false, 2)).toBe(okStatus);
    expect(nextProviderStatus(okStatus, { ok: false, error }, true, 2)).toEqual({ kind: 'unreachable' });
    expect(nextProviderStatus({ kind: 'unreachable' }, { ok: true, value: snapshot() }, false, 3)).toMatchObject({ kind: 'ok', at: 3 });
  });
});

describe('roll-up and counts', () => {
  it('rolls up to the most urgent word; unknown words rank as unknown', () => {
    expect(rollUp(['idle', 'working', 'done'])).toBe('done');
    expect(rollUp(['none', 'blocked'])).toBe('blocked');
    expect(rollUp(['none', 'snoozing'])).toBe('unknown');
    expect(rollUp([])).toBe('none');
  });

  it('counts blocked and done terminals over every computer, and leaves out a provider that is off', () => {
    const entries = [ready(mac), ready(vps, [group('g2', 'api', [item('w1', 'main', [target('w1:p1.t', 'blocked')])])])];
    expect(attentionCount(entries)).toBe(3);
    expect(wordSummary(entries)).toBe('2 blocked · 1 done · 2 working');
    const off = providerEntry(vps, { kind: 'ok', snapshot: snapshot([], { state: 'off' }), at: 0 });
    expect(shown(off)).toBe(false);
    expect(providerTabs([ready(mac), off])).toEqual([{ id: 'herdr', name: 'herdr', count: 2 }]);
    expect(providerTabs([off])).toEqual([]);
    expect(wordSummary([providerEntry(mac, { kind: 'loading' })])).toBeUndefined();
  });

  it('takes the state from the snapshot, not the settings it came with', () => {
    expect(providerState(providerEntry(mac, { kind: 'loading' }, { state: 'off' }))).toBe('off');
    expect(providerState(providerEntry(mac, { kind: 'ok', snapshot: snapshot([], { state: 'notRunning' }), at: 0 }, { state: 'ready' }))).toBe('notRunning');
  });

  it('finds a target with its group and item, or nothing when it left', () => {
    const found = findTarget(snapshot(), 'w8:p1.term_b1');
    expect([found?.group.name, found?.item.name, found?.target.agent]).toEqual(['shop', 'silver-meadow', 'claude']);
    expect(findTarget(snapshot(), 'w8:p1.term_gone')).toBeUndefined();
  });
});
