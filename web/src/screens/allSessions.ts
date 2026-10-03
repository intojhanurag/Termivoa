import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, client, forgetClient, isAuthFailure, updateText } from '../api/http';
import { sessionKey, type Computer } from '../computers';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { forgetReload, reloadOnce, storage } from '../storage';
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
  /** Added in this visit, and its first reads failed: "Connecting to vps…" (connectingText, JUST_ADDED). */
  | { readonly kind: 'connecting' }
  | { readonly kind: 'ok'; readonly settings: HTTPSettingsResponse; readonly sessions: ReadonlyArray<HostedSession>; readonly at: number }
  /**
   * "vps · can't reach", with when it last answered in this visit. `network`:
   * the last read failed with a network error (no answer at all), not with
   * an HTTP error or a reply the app cannot read.
   */
  | { readonly kind: 'unreachable'; readonly lastSeen?: number | undefined; readonly network?: boolean }
  /** A 401 from a peer: "vps · pair again". Never logs out home or other peers. */
  | { readonly kind: 'pair' }
  /** The app's own CSP blocks this computer even after one reload: "vps · can't reach" and OUT_OF_DATE. Kept until it answers. */
  | { readonly kind: 'blocked' }
  /** "Update Termivoa on vps" (or on home, when the app is the old one). */
  | { readonly kind: 'outdated'; readonly message: string };

/** "vps · can't reach". */
export const cantReach = (label: string): string => `${label} · can't reach`;
/**
 * Right after an add, the new computer's gateway may not allow this app's
 * origin yet (it refreshes that list about once a second), so its first
 * reads can fail. For this long after the add a failed read shows
 * "Connecting to vps…" and is retried quietly (CONNECTING_RETRY_MS), not
 * after the offline backoff; only then does it say "can't reach".
 */
export const CONNECTING_MS = 5_000;
/** The quiet retries after each failed read of a just-added computer; then one when CONNECTING_MS ends. */
export const CONNECTING_RETRY_MS: ReadonlyArray<number> = [500, 1_000, 2_000];
/** "Connecting to vps…". */
export const connectingText = (label: string): string => `Connecting to ${label}…`;
/** The line under connectingText. */
export const JUST_ADDED = 'Just added. Its sessions show in a moment.';

/**
 * When to read a computer added at `addedAt` again after its failed read
 * number `attempt` (0 first), or undefined once CONNECTING_MS is over. The
 * last retry falls when CONNECTING_MS ends, so "can't reach" shows on time.
 */
export function connectingRetry(attempt: number, addedAt: number, now: number): number | undefined {
  const left = addedAt + CONNECTING_MS - now;
  return left <= 0 ? undefined : Math.min(CONNECTING_RETRY_MS[attempt] ?? left, left);
}

/** "vps · pair again". */
export const pairAgain = (label: string): string => `${label} · pair again`;

/*
 * The app's own CSP. Its connect-src lists the computers of this device at
 * page load. A page from before an add, or an old app page that the service
 * worker served from its cache (its network wait ran out), may not list a
 * computer that is in the list now: every request to it is blocked in the
 * phone, and it shows "can't reach" while it never sees a byte. The page
 * then reloads once, past the cached page; if the policy still blocks, the
 * row says OUT_OF_DATE.
 */

/** sessionStorage: the computers this tab already reloaded for, space-joined (the loop guard, reloadOnce). */
export const POLICY_RELOAD_KEY = 'pterm.policyReload';
/** The query flag of that reload: the service worker waits up to 12 s for the network for this page load before its cached page. Removed at start. */
export const FRESH_PAGE = 'pterm-fresh';
/** Shown under "vps · can't reach" when the reload did not help. */
export const OUT_OF_DATE = 'This app page is out of date. Close the app and open it again.';

/** The same address with FRESH_PAGE, for a reload past the service worker's cached page. */
export function freshPageUrl(location: Pick<Location, 'pathname' | 'search'>): string {
  const params = new URLSearchParams(location.search);
  params.set(FRESH_PAGE, '1');
  return `${location.pathname}?${params.toString()}`;
}

/** Set by reloadFreshPage: the page is going, later blocks on it do nothing. */
let reloading = false;

/** Reloads this page past the service worker's cached page, once per page. history.state stays (a peer preview needs its tap mark). */
export function reloadFreshPage(): void {
  if (reloading) return;
  reloading = true;
  window.history.replaceState(window.history.state, '', freshPageUrl(window.location));
  window.location.reload();
}

/** The peer in the list whose address the app's own CSP blocked (connect-src, https or wss), or undefined. */
export function blockedPeer(
  event: Pick<SecurityPolicyViolationEvent, 'blockedURI' | 'effectiveDirective'>,
  computers: ReadonlyArray<Computer>,
): Computer | undefined {
  if (event.effectiveDirective !== 'connect-src') return undefined;
  let url: URL;
  try {
    url = new URL(event.blockedURI);
  } catch {
    return undefined;
  }
  if (url.protocol !== 'https:' && url.protocol !== 'wss:') return undefined;
  // `host` keeps a port, so a preview origin (:8443) is never the computer itself.
  return computers.find((computer) => !computer.home && computer.origin === `https://${url.host}`);
}

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
 * it shows after the first failure, as before multi-computer. `connecting`:
 * the computer was added less than CONNECTING_MS ago, so a failure that is
 * not a refusal shows "Connecting to vps…".
 */
export function nextStatus(
  previous: ComputerStatus,
  computer: Computer,
  outcome: PollOutcome<Loaded>,
  offline: boolean,
  now: number,
  single = false,
  connecting = false,
): ComputerStatus {
  if (outcome.ok) {
    return { kind: 'ok', settings: outcome.value.settings, sessions: outcome.value.sessions.map((item) => hosted(item, computer)), at: now };
  }
  const { error } = outcome;
  // Every request stays blocked in the phone until the app page changes.
  if (previous.kind === 'blocked') return previous;
  if (isAuthFailure(error)) return { kind: 'pair' };
  if (error instanceof ApiError && error.code === 'computer_outdated') return { kind: 'outdated', message: updateText(computer.label) };
  if (error instanceof ApiError && error.code === 'app_outdated') return { kind: 'outdated', message: error.message };
  if (connecting) return { kind: 'connecting' };
  if (!offline && !single && previous.kind !== 'connecting') return previous;
  const lastSeen = previous.kind === 'ok' ? previous.at : previous.kind === 'unreachable' ? previous.lastSeen : undefined;
  return { kind: 'unreachable', lastSeen, network: error instanceof ApiError && error.code === 'network' };
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
  /** `asked` is true: the user or a change asked for this read, so a failure shows at once ("can't reach"), not after three. */
  publish: (origin: string, outcome: PollOutcome<Loaded>, asked?: boolean) => void,
): Promise<void> {
  const failures = await Promise.all(computers.map((computer) => scheduler.serial(computer.origin, () => load(computer.origin)).then(
    (value) => { scheduler.reported(computer.origin, true); publish(computer.origin, { ok: true, value }, true); return undefined; },
    (error: unknown) => { scheduler.reported(computer.origin, false); publish(computer.origin, { ok: false, error }, true); return new ReadFailure(computer, error); },
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

function forgetPolicyReload(origin: string): void {
  forgetReload(POLICY_RELOAD_KEY, origin, storage('sessionStorage'));
}

/** Exported for tests. */
export async function load(origin: string, fresh: boolean, listed: () => boolean): Promise<Loaded> {
  const http = client(origin);
  let settings = fresh ? undefined : settingsCache.get(origin);
  if (settings === undefined) {
    settings = await http.settings();
    // A computer removed while this read ran keeps nothing behind.
    if (listed()) settingsCache.set(origin, settings);
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
export function useAllSessions(
  active: boolean,
  computers: ReadonlyArray<Computer>,
  onHomeAuth: () => void,
  /** The origin of the computer added in this visit, once its add is done. */
  added?: string,
): AllSessions {
  const [statuses, setStatuses] = useState<ReadonlyMap<string, ComputerStatus>>(new Map());
  const byOrigin = useRef(new Map<string, Computer>());
  byOrigin.current = new Map(computers.map((computer) => [computer.origin, computer]));
  const homeAuth = useRef(onHomeAuth);
  homeAuth.current = onHomeAuth;
  const origins = computers.map((computer) => computer.origin).join(' ');
  // A computer added in this visit: when, and how many quiet retries it had (CONNECTING_MS).
  const fresh = useRef(new Map<string, { readonly at: number; attempt: number }>());
  const retries = useRef(new Set<ReturnType<typeof setTimeout>>());
  useEffect(() => {
    if (added !== undefined) fresh.current.set(added, { at: Date.now(), attempt: 0 });
  }, [added]);
  useEffect(() => () => { for (const timer of retries.current) clearTimeout(timer); }, []);

  const publish = useCallback((origin: string, outcome: PollOutcome<Loaded>, asked = false) => {
    const computer = byOrigin.current.get(origin);
    if (computer === undefined) return;
    if (!outcome.ok && computer.home && isAuthFailure(outcome.error)) {
      homeAuth.current();
      return;
    }
    const single = byOrigin.current.size < 2;
    const now = Date.now();
    // It answers under this page's policy: a later block may reload again.
    if (outcome.ok) forgetPolicyReload(origin);
    const added = fresh.current.get(origin);
    if (outcome.ok) fresh.current.delete(origin);
    const wait = outcome.ok || added === undefined ? undefined : connectingRetry(added.attempt, added.at, now);
    if (added !== undefined && wait !== undefined) {
      added.attempt += 1;
      // retry() also clears the failure count, so no offline backoff builds up meanwhile.
      const timer = setTimeout(() => { retries.current.delete(timer); scheduler.retry(origin); }, wait);
      retries.current.add(timer);
    }
    setStatuses((current) => {
      const next = new Map(current);
      next.set(origin, nextStatus(current.get(origin) ?? { kind: 'loading' }, computer, outcome, asked || scheduler.offline(origin), now, single, wait !== undefined));
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
    return scheduler.poll(origins.split(' '), (origin) => load(origin, false, () => byOrigin.current.has(origin)), publish);
  }, [active, origins, publish]);

  // The app's own CSP blocked a listed peer: reload once past the cached page, else say the page is out of date.
  useEffect(() => {
    const onBlocked = (event: SecurityPolicyViolationEvent) => {
      const computer = blockedPeer(event, [...byOrigin.current.values()]);
      if (computer === undefined || reloading) return;
      if (reloadOnce(POLICY_RELOAD_KEY, computer.origin, storage('sessionStorage'))) {
        reloadFreshPage();
        return;
      }
      setStatuses((current) => new Map(current).set(computer.origin, { kind: 'blocked' }));
    };
    document.addEventListener('securitypolicyviolation', onBlocked);
    return () => document.removeEventListener('securitypolicyviolation', onBlocked);
  }, []);

  const reload = useCallback((origin?: string) => {
    const list = [...byOrigin.current.values()].filter((computer) => origin === undefined || computer.origin === origin);
    return readNow(list, (item) => load(item, true, () => byOrigin.current.has(item)), publish);
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
