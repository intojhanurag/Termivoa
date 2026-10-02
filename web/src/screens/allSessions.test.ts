import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/http';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { byChip, chipsFor, hosted, keyOf, mergeSessions, nextStatus, sameSession, type ComputerEntry, type ComputerStatus } from './allSessions';

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
    expect(nextStatus(seen, vps, fail(network), true, 50)).toEqual({ kind: 'unreachable', lastSeen: 42 });
    const down: ComputerStatus = { kind: 'unreachable', lastSeen: 42 };
    expect(nextStatus(down, vps, fail(network), true, 60)).toEqual({ kind: 'unreachable', lastSeen: 42 });
    expect(nextStatus({ kind: 'loading' }, vps, fail(network), true, 60)).toEqual({ kind: 'unreachable', lastSeen: undefined });
  });

  it('turns a 401 into pair again, and an outdated revision into its update text', () => {
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(401, 'unauthorized', 'x')), false, 0)).toEqual({ kind: 'pair' });
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(0, 'computer_outdated', 'Update Termivoa on vps')), false, 0))
      .toEqual({ kind: 'outdated', message: 'Update Termivoa on vps' });
    expect(nextStatus({ kind: 'loading' }, vps, fail(new ApiError(0, 'app_outdated', 'Update Termivoa on mac')), false, 0))
      .toEqual({ kind: 'outdated', message: 'Update Termivoa on mac' });
  });
});
