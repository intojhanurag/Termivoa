import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, client, isAuthFailure } from '../api/http';
import { sessionKey, type Computer } from '../computers';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { scheduler, type PollOutcome } from '../terminal/scheduler';

/**
 * One list across all computers (docs/multi-computer.md, "One list, one
 * Wall"). Every session carries the computer it runs on, so screens use that
 * computer's client and socket, and keys never collide between computers.
 * Session lists live in memory only; they are never stored.
 */
export interface HostedSession extends BrokerRPCSessionRecord {
  readonly computer: Computer;
}

export const hosted = (session: BrokerRPCSessionRecord, computer: Computer): HostedSession => ({ ...session, computer });

/** `host/id`: the session's key in lists, the Wall order and the scheduler. */
export const keyOf = (session: HostedSession): string => sessionKey(session.computer, session.id);

export const sameSession = (a: HostedSession, b: HostedSession): boolean =>
  a.id === b.id && a.generation === b.generation && a.computer.host === b.computer.host;

/** What the app knows about one computer right now. */
export type ComputerStatus =
  | { readonly kind: 'loading' }
  | { readonly kind: 'ok'; readonly settings: HTTPSettingsResponse; readonly sessions: ReadonlyArray<HostedSession>; readonly at: number }
  /** "vps · can't reach", with when it last answered in this visit. */
  | { readonly kind: 'unreachable'; readonly lastSeen?: number | undefined }
  /** A 401 from a peer: "vps · pair again". Never logs out home or other peers. */
  | { readonly kind: 'pair' }
  /** "Update Termivoa on vps" (or on home, when the app is the old one). */
  | { readonly kind: 'outdated'; readonly message: string };

/** "vps · can't reach". */
export const cantReach = (label: string): string => `${label} · can't reach`;
/** "vps · pair again". */
export const pairAgain = (label: string): string => `${label} · pair again`;

export interface ComputerEntry {
  readonly computer: Computer;
  readonly status: ComputerStatus;
}

interface Loaded {
  readonly settings: HTTPSettingsResponse;
  readonly sessions: ReadonlyArray<BrokerRPCSessionRecord>;
}

/**
 * The next status after one poll. A network failure only shows "can't reach"
 * once the scheduler counts the computer offline; before that the last
 * answer stays on screen.
 */
export function nextStatus(
  previous: ComputerStatus,
  computer: Computer,
  outcome: PollOutcome<Loaded>,
  offline: boolean,
  now: number,
): ComputerStatus {
  if (outcome.ok) {
    return { kind: 'ok', settings: outcome.value.settings, sessions: outcome.value.sessions.map((item) => hosted(item, computer)), at: now };
  }
  const { error } = outcome;
  if (isAuthFailure(error)) return { kind: 'pair' };
  if (error instanceof ApiError && (error.code === 'computer_outdated' || error.code === 'app_outdated')) {
    return { kind: 'outdated', message: error.message };
  }
  if (!offline) return previous;
  const lastSeen = previous.kind === 'ok' ? previous.at : previous.kind === 'unreachable' ? previous.lastSeen : undefined;
  return { kind: 'unreachable', lastSeen };
}

/** Waiting sessions first; otherwise computer order (home first), then each computer's own order (sort is stable). */
export function waitingFirst<T extends BrokerRPCSessionRecord>(sessions: ReadonlyArray<T>): T[] {
  const waiting = (session: T) => (session.agent?.state === 'waiting' ? 0 : 1);
  return [...sessions].sort((a, b) => waiting(a) - waiting(b));
}

/** Every answered computer's sessions in one list, waiting first. */
export function mergeSessions(entries: ReadonlyArray<ComputerEntry>): HostedSession[] {
  return waitingFirst(entries.flatMap((entry) => (entry.status.kind === 'ok' ? entry.status.sessions : [])));
}

/** "All" plus one chip per computer; none with a single computer. */
export function chipsFor(computers: ReadonlyArray<Computer>): ReadonlyArray<{ readonly host: string | undefined; readonly label: string; readonly color?: string }> {
  if (computers.length < 2) return [];
  return [{ host: undefined, label: 'All' }, ...computers.map((computer) => ({ host: computer.host, label: computer.label, color: computer.color }))];
}

/** The sessions and computers a chip shows; undefined is All. */
export function byChip<T extends { readonly computer: Pick<Computer, 'host'> }>(items: ReadonlyArray<T>, host: string | undefined): T[] {
  return host === undefined ? [...items] : items.filter((item) => item.computer.host === host);
}

/** The settings each computer answered, read again on each return to the foreground (the peer refreshes its cookie then). */
const settingsCache = new Map<string, HTTPSettingsResponse>();

export function forgetSettings(): void {
  settingsCache.clear();
}

async function load(origin: string, fresh: boolean): Promise<Loaded> {
  const http = client(origin);
  let settings = fresh ? undefined : settingsCache.get(origin);
  if (settings === undefined) {
    settings = await http.settings();
    settingsCache.set(origin, settings);
  }
  return { settings, sessions: (await http.sessions()).sessions };
}

export interface AllSessions {
  /** One entry per computer, in the computers' order. */
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** Reads these computers (all when omitted) now, settings included. */
  readonly reload: (origin?: string) => Promise<void>;
  /** The user pressed Retry. */
  readonly retry: (origin: string) => void;
}

/**
 * Polls every computer through the phone-wide scheduler while `active`, and
 * shows each computer's answer as soon as it arrives. A 401 from home calls
 * onHomeAuth (the app goes to Setup); a 401 from a peer only marks that peer.
 */
export function useAllSessions(active: boolean, computers: ReadonlyArray<Computer>, onHomeAuth: () => void): AllSessions {
  const [statuses, setStatuses] = useState<ReadonlyMap<string, ComputerStatus>>(new Map());
  const byOrigin = useRef(new Map<string, Computer>());
  byOrigin.current = new Map(computers.map((computer) => [computer.origin, computer]));
  const homeAuth = useRef(onHomeAuth);
  homeAuth.current = onHomeAuth;
  const origins = computers.map((computer) => computer.origin).join(' ');

  const publish = useCallback((origin: string, outcome: PollOutcome<Loaded>) => {
    const computer = byOrigin.current.get(origin);
    if (computer === undefined) return;
    if (!outcome.ok && computer.home && isAuthFailure(outcome.error)) {
      homeAuth.current();
      return;
    }
    setStatuses((current) => {
      const next = new Map(current);
      next.set(origin, nextStatus(current.get(origin) ?? { kind: 'loading' }, computer, outcome, scheduler.offline(origin), Date.now()));
      return next;
    });
  }, []);

  useEffect(() => {
    if (!active || origins === '') return undefined;
    return scheduler.poll(origins.split(' '), (origin) => load(origin, false), publish);
  }, [active, origins, publish]);

  const reload = useCallback(async (origin?: string) => {
    const list = origin === undefined ? [...byOrigin.current.keys()] : [origin];
    await Promise.all(list.map((item) => scheduler.read(() => load(item, true)).then(
      (value) => publish(item, { ok: true, value }),
      (error: unknown) => publish(item, { ok: false, error }),
    )));
  }, [publish]);

  const retry = useCallback((origin: string) => {
    settingsCache.delete(origin);
    scheduler.retry(origin);
  }, []);

  const entries = useMemo(
    () => computers.map((computer) => ({ computer, status: statuses.get(computer.origin) ?? { kind: 'loading' as const } })),
    [computers, statuses],
  );
  return { entries, reload, retry };
}
