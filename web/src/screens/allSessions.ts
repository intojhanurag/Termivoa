import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, client, forgetClient, isAuthFailure, updateText } from '../api/http';
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
 * The next status after one poll. With several computers, a network failure
 * only shows "can't reach" once the scheduler counts the computer offline;
 * before that the last answer stays on screen. With one computer (`single`)
 * it shows after the first failure, as before multi-computer.
 */
export function nextStatus(
  previous: ComputerStatus,
  computer: Computer,
  outcome: PollOutcome<Loaded>,
  offline: boolean,
  now: number,
  single = false,
): ComputerStatus {
  if (outcome.ok) {
    return { kind: 'ok', settings: outcome.value.settings, sessions: outcome.value.sessions.map((item) => hosted(item, computer)), at: now };
  }
  const { error } = outcome;
  if (isAuthFailure(error)) return { kind: 'pair' };
  if (error instanceof ApiError && error.code === 'computer_outdated') return { kind: 'outdated', message: updateText(computer.label) };
  if (error instanceof ApiError && error.code === 'app_outdated') return { kind: 'outdated', message: error.message };
  if (!offline && !single) return previous;
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

/** Home's settings from its latest good answer, or undefined while it has none. */
export function homeSettings(entries: ReadonlyArray<ComputerEntry>): HTTPSettingsResponse | undefined {
  const home = entries.find((entry) => entry.computer.home)?.status;
  return home?.kind === 'ok' ? home.settings : undefined;
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

/** A computer left the list (removed, or gone from home's list): drop everything kept for its origin. */
export function forgetComputer(origin: string): void {
  settingsCache.delete(origin);
  scheduler.forget(origin);
  forgetClient(origin);
}

/** A read in reload() failed: the error and the computer it came from (a peer's 401 is not home's). */
export class ReadFailure extends Error {
  constructor(readonly computer: Computer, readonly error: unknown) {
    super(error instanceof Error ? error.message : 'Request failed');
    this.name = 'ReadFailure';
  }
}

/**
 * Reads each computer now (settings included), one read per computer at a
 * time through the scheduler, and publishes every result. Rejects with a
 * ReadFailure for the first computer that failed, after all have finished,
 * so "Check again" can say what went wrong.
 */
export async function readNow(
  computers: ReadonlyArray<Computer>,
  load: (origin: string) => Promise<Loaded>,
  publish: (origin: string, outcome: PollOutcome<Loaded>) => void,
): Promise<void> {
  const failures = await Promise.all(computers.map((computer) => scheduler.serial(computer.origin, () => load(computer.origin)).then(
    (value) => { publish(computer.origin, { ok: true, value }); return undefined; },
    (error: unknown) => { publish(computer.origin, { ok: false, error }); return new ReadFailure(computer, error); },
  )));
  const first = failures.find((failure) => failure !== undefined);
  if (first !== undefined) throw first;
}

/** Statuses only for the computers still listed. */
export function pruneStatuses(statuses: ReadonlyMap<string, ComputerStatus>, origins: ReadonlySet<string>): ReadonlyMap<string, ComputerStatus> {
  return [...statuses.keys()].every((origin) => origins.has(origin))
    ? statuses
    : new Map([...statuses].filter(([origin]) => origins.has(origin)));
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
  /** Reads these computers (all when omitted) now, settings included; rejects with a ReadFailure (readNow). */
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
    const single = byOrigin.current.size < 2;
    setStatuses((current) => {
      const next = new Map(current);
      next.set(origin, nextStatus(current.get(origin) ?? { kind: 'loading' }, computer, outcome, scheduler.offline(origin), Date.now(), single));
      return next;
    });
  }, []);

  // A computer that left the list leaves nothing behind (status, settings, backoff, client).
  const listed = useRef(new Set<string>());
  useEffect(() => {
    const now = new Set(origins === '' ? [] : origins.split(' '));
    for (const origin of listed.current) if (!now.has(origin)) forgetComputer(origin);
    listed.current = now;
    setStatuses((current) => pruneStatuses(current, now));
  }, [origins]);

  useEffect(() => {
    if (!active || origins === '') return undefined;
    return scheduler.poll(origins.split(' '), (origin) => load(origin, false), publish);
  }, [active, origins, publish]);

  const reload = useCallback((origin?: string) => {
    const list = [...byOrigin.current.values()].filter((computer) => origin === undefined || computer.origin === origin);
    return readNow(list, (item) => load(item, true), publish);
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
