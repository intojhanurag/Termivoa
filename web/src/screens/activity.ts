import type { BrokerRPCSessionRecord } from '../generated/protocol';

/** When this device last viewed each session generation (ms). Not secret. */
export type ViewedTimes = Readonly<Record<string, number>>;

export const VIEWED_KEY = 'pterm.lastViewed';
const MAX_VIEWED = 100;

type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;
type SessionRef = Pick<BrokerRPCSessionRecord, 'id' | 'generation' | 'lastOutputAt'>;

function storage(): StorageLike | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

export const viewedKey = (session: Pick<BrokerRPCSessionRecord, 'id' | 'generation'>) => `${session.id}/${session.generation}`;

export function readViewed(store: StorageLike | undefined = storage()): ViewedTimes {
  try {
    const parsed: unknown = JSON.parse(store?.getItem(VIEWED_KEY) ?? '{}');
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => typeof value === 'number' && Number.isFinite(value)));
  } catch {
    return {};
  }
}

/**
 * Records that this device is looking at the session now. Takes the later of
 * the phone clock and the session's own last output time, so output already
 * on screen never shows as new because the two clocks disagree.
 * ponytail: a phone clock behind the host can still flag output seen in the
 * last poll interval; a per-session output counter would remove that.
 */
export function markViewed(session: SessionRef, now = Date.now(), store: StorageLike | undefined = storage()): void {
  const output = Date.parse(session.lastOutputAt ?? '');
  const viewed: Record<string, number> = { ...readViewed(store) };
  viewed[viewedKey(session)] = Math.max(now, Number.isNaN(output) ? 0 : output);
  const kept = Object.entries(viewed).sort((a, b) => b[1] - a[1]).slice(0, MAX_VIEWED);
  try {
    store?.setItem(VIEWED_KEY, JSON.stringify(Object.fromEntries(kept)));
  } catch {
    // Private browsing may refuse storage; dots then only reflect this visit.
  }
}

/** True when the session printed something after this device last viewed it. */
export function hasNewOutput(session: SessionRef, viewed: ViewedTimes): boolean {
  if (session.lastOutputAt === undefined) return false;
  const output = Date.parse(session.lastOutputAt);
  if (Number.isNaN(output)) return false;
  const seen = viewed[viewedKey(session)];
  return seen === undefined || output > seen;
}

/** Compact time since `iso`: "12s", "2m", "1h 5m", "3d"; empty when unknown. */
export function since(iso: string | undefined, now: number): string {
  const then = Date.parse(iso ?? '');
  if (Number.isNaN(then)) return '';
  const seconds = Math.max(0, Math.floor((now - then) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours >= 24) return `${Math.floor(hours / 24)}d`;
  return minutes % 60 === 0 ? `${hours}h` : `${hours}h ${minutes % 60}m`;
}

type AgentRef = Pick<BrokerRPCSessionRecord, 'agent'>;

export interface AgentBadge {
  readonly label: 'Waiting' | 'Working' | 'Your turn';
  readonly tone: 'amber' | 'accent' | 'green';
}

/**
 * The status pill for a running session, or undefined for the neutral look.
 * The broker reports `idle` for any quiet program, so a plain shell at its
 * prompt is idle too. "Your turn" needs both `afterWork` (the broker saw this
 * idle end a working period, so it survives reloads) and output this device
 * has not viewed yet, so opening the session clears it.
 */
export function agentBadge(session: AgentRef, fresh: boolean): AgentBadge | undefined {
  switch (session.agent?.state) {
    case 'waiting': return { label: 'Waiting', tone: 'amber' };
    case 'working': return { label: 'Working', tone: 'accent' };
    case 'idle': return fresh && session.agent.afterWork === true ? { label: 'Your turn', tone: 'green' } : undefined;
    default: return undefined;
  }
}
