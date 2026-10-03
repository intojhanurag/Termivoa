import { describe, expect, it, vi } from 'vitest';
import { ApiError, client } from '../api/http';
import { scheduler } from '../terminal/scheduler';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import {
  byChip, chipsFor, CONNECTING_MS, connectingRetry, forgetComputer, homeSettings, hosted, keyOf, load, mergeSessions, nextStatus, pruneStatuses, readNow, ReadFailure, sameSession,
  type ComputerEntry, type ComputerStatus,
} from './allSessions';

const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };
const settings = { projects: [], shellProfiles: [] } as unknown as HTTPSettingsResponse;
const record = (id: string, extra: Partial<BrokerRPCSessionRecord> = {}) => ({ id, generation: 'g', ...extra }) as BrokerRPCSessionRecord;
const waiting = { agent: { state: 'waiting', stateSince: '2026-09-27T10:00:00Z' } } as const;
const ok = (computer: Computer, sessions: BrokerRPCSessionRecord[]): ComputerEntry => ({
  computer, status: { kind: 'ok', settings, sessions: sessions.map((item) => hosted(item, computer)), at: 0 },
});

describe('session keys', () => {
  it('are host/id, so the same id on two computers never collides', () => {
    const a = hosted(record('1'), mac);
    const b = hosted(record('1'), vps);
    expect(keyOf(a)).toBe('mac.tailx.ts.net/1');
    expect(keyOf(b)).toBe('vps.tailx.ts.net/1');
    expect(sameSession(a, b)).toBe(false);
    expect(sameSession(a, hosted(record('1'), mac))).toBe(true);
  });
});

describe('mergeSessions', () => {
  it('puts every computer in one list, waiting first, then computer order', () => {
    const list = mergeSessions([
      ok(mac, [record('m1'), record('m2', waiting)]),
      { computer: { ...vps, host: 'gone' }, status: { kind: 'unreachable' } },
      ok(vps, [record('v1'), record('v2', waiting)]),
    ]);
    expect(list.map(keyOf)).toEqual(['mac.tailx.ts.net/m2', 'vps.tailx.ts.net/v2', 'mac.tailx.ts.net/m1', 'vps.tailx.ts.net/v1']);
  });
});

describe('chips', () => {
  it('are All plus one per computer, and none for a single computer', () => {
    expect(chipsFor([mac])).toEqual([]);
    expect(chipsFor([mac, vps]).map((chip) => chip.label)).toEqual(['All', 'mac', 'vps']);
    const list = mergeSessions([ok(mac, [record('m')]), ok(vps, [record('v')])]);
    expect(byChip(list, undefined)).toHaveLength(2);
    expect(byChip(list, vps.host).map(keyOf)).toEqual(['vps.tailx.ts.net/v']);
  });
});

describe('nextStatus', () => {
  const loaded = { ok: true as const, value: { settings, sessions: [record('1')] } };
  const fail = (error: unknown) => ({ ok: false as const, error });
  const network = new ApiError(0, 'network', 'Connection unavailable');

  it('shows each answer as it arrives, with its computer', () => {
    const next = nextStatus({ kind: 'loading' }, vps, loaded, false, 42);
    expect(next).toMatchObject({ kind: 'ok', at: 42 });
    expect(next.kind === 'ok' && next.sessions[0]?.computer).toBe(vps);
  });

  it('keeps the last answer until the scheduler counts the computer offline, then says can\'t reach with when it was last seen', () => {
    const seen = nextStatus({ kind: 'loading' }, vps, loaded, false, 42);
    expect(nextStatus(seen, vps, fail(network), false, 50)).toBe(seen);
    expect(nextStatus(seen, vps, fail(network), true, 50)).toEqual({ kind: 'unreachable', lastSeen: 42, network: true });
    const down: ComputerStatus = { kind: 'unreachable', lastSeen: 42 };
    expect(nextStatus(down, vps, fail(network), true, 60)).toEqual({ kind: 'unreachable', lastSeen: 42, network: true });
    expect(nextStatus({ kind: 'loading' }, vps, fail(network), true, 60)).toEqual({ kind: 'unreachable', lastSeen: undefined, network: true });
    // An HTTP error is an answer: not a network failure.
    expect(nextStatus(down, vps, fail(new ApiError(502, 'unknown', 'Bad gateway')), true, 60)).toEqual({ kind: 'unreachable', lastSeen: 42, network: false });
  });

  it('with one computer, says not reachable after the first failure, as before multi-computer', () => {
    const seen = nextStatus({ kind: 'loading' }, mac, loaded, false, 42);
    expect(nextStatus(seen, mac, fail(network), false, 50, true)).toEqual({ kind: 'unreachable', lastSeen: 42, network: true });
  });

  it('names an outdated computer by the user\'s label', () => {
    const renamed = { ...vps, label: 'Build box' };
    expect(nextStatus({ kind: 'loading' }, renamed, fail(new ApiError(0, 'computer_outdated', 'Update Termivoa on vps')), false, 0))
      .toEqual({ kind: 'outdated', message: 'Update Termivoa on Build box' });
  });

  it('turns a 401 into pair again, and an outdated revision into its update text', () => {
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(401, 'unauthorized', 'x')), false, 0)).toEqual({ kind: 'pair' });
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(0, 'computer_outdated', 'Update Termivoa on vps')), false, 0))
      .toEqual({ kind: 'outdated', message: 'Update Termivoa on vps' });
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(0, 'app_outdated', 'Update Termivoa on mac')), false, 0))
      .toEqual({ kind: 'outdated', message: 'Update Termivoa on mac' });
  });
});

describe('a computer added in this visit', () => {
  const network = new ApiError(0, 'network', 'Connection unavailable');
  const fail = (error: unknown) => ({ ok: false as const, error });

  it('shows a failed read as connecting, also one the user asked for, but a refusal as it is', () => {
    expect(nextStatus({ kind: 'loading' }, vps, fail(network), false, 10, false, true)).toEqual({ kind: 'connecting' });
    expect(nextStatus({ kind: 'loading' }, vps, fail(network), true, 10, false, true)).toEqual({ kind: 'connecting' });
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(401, 'unauthorized', 'x')), true, 10, false, true)).toEqual({ kind: 'pair' });
  });

  it('says can\'t reach at the first failure after the connecting time, without waiting for the offline count', () => {
    expect(nextStatus({ kind: 'connecting' }, vps, fail(network), false, CONNECTING_MS, false, false))
      .toEqual({ kind: 'unreachable', lastSeen: undefined, network: true });
    const loaded = { ok: true as const, value: { settings, sessions: [] } };
    expect(nextStatus({ kind: 'connecting' }, vps, loaded, false, 20, false, true)).toMatchObject({ kind: 'ok' });
  });

  it('retries quietly after 0.5, 1 and 2 s, then once when the connecting time ends, and never after', () => {
    expect(connectingRetry(0, 0, 100)).toBe(500);
    expect(connectingRetry(1, 0, 700)).toBe(1_000);
    expect(connectingRetry(2, 0, 1_800)).toBe(2_000);
    expect(connectingRetry(3, 0, 3_900)).toBe(CONNECTING_MS - 3_900);
    // A slow read: the retry never lands after the connecting time.
    expect(connectingRetry(0, 0, 4_800)).toBe(200);
    expect(connectingRetry(0, 0, CONNECTING_MS)).toBeUndefined();
    expect(connectingRetry(4, 0, 6_000)).toBeUndefined();
  });
});

describe('readNow (Check again, and reloads after a change)', () => {
  it('publishes every computer, then rejects with the first failure and its computer', async () => {
    const published: string[] = [];
    const down = new ApiError(0, 'network', 'Connection unavailable');
    const read = readNow(
      [mac, vps],
      async (origin) => { if (origin === vps.origin) throw down; return { settings, sessions: [] }; },
      (origin, outcome) => published.push(`${origin} ${outcome.ok}`),
    );
    const failure = await read.catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ReadFailure);
    expect(failure).toMatchObject({ computer: vps, error: down, message: 'Connection unavailable' });
    expect(published).toEqual([`${mac.origin} true`, `${vps.origin} false`]);
    await expect(readNow([mac], async () => ({ settings, sessions: [] }), () => undefined)).resolves.toBeUndefined();
  });

  it('never overlaps two reads of one computer', async () => {
    let open = 0;
    let most = 0;
    const load = vi.fn(async () => {
      most = Math.max(most, ++open);
      await Promise.resolve();
      open--;
      return { settings, sessions: [] };
    });
    await Promise.all([readNow([vps], load, () => undefined), readNow([vps], load, () => undefined)]);
    expect(load).toHaveBeenCalledTimes(2);
    expect(most).toBe(1);
  });
});

describe('readNow and the computer\'s health', () => {
  const origin = 'https://health.tailx.ts.net';
  const peer: Computer = { ...vps, origin, host: 'health.tailx.ts.net' };
  const down = new ApiError(0, 'network', 'Connection unavailable');
  const reads = (fail: boolean, published: unknown[] = []) => readNow(
    [peer],
    async () => { if (fail) throw down; return { settings, sessions: [] }; },
    (...args) => published.push(args),
  ).catch(() => undefined);

  it('counts a failed read, and an answer clears the backoff and starts a waiting connect', async () => {
    scheduler.forget(origin);
    const gate = scheduler.connectGate(origin);
    for (let index = 0; index < 3; index++) await reads(true);
    expect(scheduler.offline(origin)).toBe(true);
    const started = vi.fn();
    gate.wait(started);
    expect(started).not.toHaveBeenCalled();
    await reads(false);
    expect(scheduler.offline(origin)).toBe(false);
    expect(started).toHaveBeenCalledTimes(1);
    gate.done('cancelled');
  });

  it('asks the status to show a failed read at once, even before the computer counts as offline', async () => {
    scheduler.forget(origin);
    const published: unknown[][] = [];
    await reads(true, published);
    expect(published[0]?.[2]).toBe(true);
    const next = nextStatus({ kind: 'ok', settings, sessions: [], at: 1 }, peer, { ok: false, error: down }, true, 2);
    expect(next).toEqual({ kind: 'unreachable', lastSeen: 1, network: true });
    scheduler.forget(origin);
  });
});

describe('load and the settings cache', () => {
  it('keeps nothing for a computer that was removed while it was read', async () => {
    const body = (url: string) => (url.endsWith('/settings')
      ? { device: { id: '00000000-0000-4000-8000-000000000020', label: 'Phone' }, projects: [], shellProfiles: [], hostBuild: 'b', brokerGeneration: '00000000-0000-4000-8000-000000000021', protocolRevision: 1, minProtocolRevision: 1 }
      : { sessions: [] });
    const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(body(url)), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);
    const origin = 'https://cache.tailx.ts.net';
    const settingsReads = () => fetch.mock.calls.filter(([url]) => url.endsWith('/settings')).length;
    await load(origin, true, () => false);
    await load(origin, false, () => true);
    expect(settingsReads()).toBe(2);
    await load(origin, false, () => true);
    expect(settingsReads()).toBe(2);
    forgetComputer(origin);
    vi.unstubAllGlobals();
  });
});

describe('pruneStatuses', () => {
  it('drops a removed computer\'s status, so adding it again starts fresh', () => {
    const statuses = new Map<string, ComputerStatus>([[mac.origin, { kind: 'loading' }], [vps.origin, { kind: 'pair' }]]);
    expect([...pruneStatuses(statuses, new Set([mac.origin])).keys()]).toEqual([mac.origin]);
    expect(pruneStatuses(statuses, new Set([mac.origin, vps.origin]))).toBe(statuses);
  });
});

describe('homeSettings (home comes back during the visit)', () => {
  it('gives home\'s settings once home answers, whatever the peers do', () => {
    expect(homeSettings([{ computer: mac, status: { kind: 'unreachable' } }, ok(vps, [])])).toBeUndefined();
    expect(homeSettings([ok(mac, []), { computer: vps, status: { kind: 'pair' } }])).toBe(settings);
  });
});

describe('forgetComputer', () => {
  it('drops the removed computer\'s client and backoff', () => {
    const origin = 'https://removed.tailx.ts.net';
    const before = client(origin);
    const gate = scheduler.connectGate(origin);
    gate.wait(() => undefined);
    gate.done('failed');
    forgetComputer(origin);
    expect(client(origin)).not.toBe(before);
    let started = false;
    scheduler.connectGate(origin).wait(() => { started = true; });
    expect(started).toBe(true);
  });
});
