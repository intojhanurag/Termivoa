import { client, firstLabel, homeOrigin, isAuthFailure } from './api/http';
import type { HTTPComputer, HTTPComputerAddResponse } from './generated/protocol';
import { parseHttpBody } from './protocol/httpBody';

/**
 * The computers this phone uses: the home computer (the origin that served
 * this app) and the peers confirmed in this device's list on home.
 *
 * - A computer's key is its canonical origin (`https://vps.tailx.ts.net`).
 * - A session's key is `host/sessionId` (sessionKey), so sessions of
 *   different computers never collide.
 * - The label defaults to the first DNS label and can be renamed; it comes
 *   from the origin the phone dialled (or the user), never from the peer.
 * - The colour is fixed per origin; always show it with the label.
 * - The peer list is kept in localStorage (origins and labels, not secret) and
 *   replaced on every load from home (refreshComputers). Pending entries are
 *   never listed or stored.
 */
export interface Computer {
  readonly origin: string;
  /** `URL.host` of the origin; the first part of a session key. */
  readonly host: string;
  readonly label: string;
  readonly color: string;
  readonly home: boolean;
}

export const COMPUTERS_KEY = 'pterm.computers';
/** Peers per device, as on home (pending and confirmed together). */
export const MAX_PEERS = 8;
/** Fixed colours on the dark theme, picked by origin hash. Never the only cue: show the label too. */
export const PALETTE = ['#64D8E8', '#F2B544', '#9B8CFF', '#5FD68A', '#FF7A8A', '#4FA3FF', '#FF9F5A', '#C7D36F'] as const;

type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

function defaultStorage(): StorageLike | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

/** FNV-1a over the origin, so a computer keeps its colour on every phone and load. */
export function colorFor(origin: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < origin.length; index++) {
    hash = Math.imul(hash ^ origin.charCodeAt(index), 0x01000193);
  }
  return PALETTE[(hash >>> 0) % PALETTE.length] ?? PALETTE[0];
}

export function toComputer(entry: Pick<HTTPComputer, 'origin' | 'label'>, home = false): Computer {
  return { origin: entry.origin, host: new URL(entry.origin).host, label: entry.label, color: colorFor(entry.origin), home };
}

export function homeComputer(): Computer {
  const origin = homeOrigin();
  return toComputer({ origin, label: firstLabel(origin) }, true);
}

export function sessionKey(computer: Pick<Computer, 'host'>, sessionId: string): string {
  return `${computer.host}/${sessionId}`;
}

/** Validated, de-duplicated peers without home, at most MAX_PEERS. */
function cleanPeers(entries: unknown): HTTPComputer[] {
  let list: HTTPComputer[];
  try {
    list = parseHttpBody('computerListResponse', { computers: Array.isArray(entries) ? entries.slice(0, MAX_PEERS) : [] }).computers;
  } catch {
    return [];
  }
  const seen = new Set([homeOrigin()]);
  return list.filter((entry) => !seen.has(entry.origin) && seen.add(entry.origin));
}

function readPeers(storage: StorageLike | undefined): HTTPComputer[] {
  try {
    return cleanPeers(JSON.parse(storage?.getItem(COMPUTERS_KEY) ?? '[]'));
  } catch {
    return [];
  }
}

function writePeers(peers: ReadonlyArray<HTTPComputer>, storage: StorageLike | undefined): void {
  try {
    storage?.setItem(COMPUTERS_KEY, JSON.stringify(peers.map(({ origin, label }) => ({ origin, label }))));
  } catch {
    // Private browsing may refuse storage; the list then lasts for this visit.
  }
}

/** Home first, then the stored peers. Use at start, before (or without) a home answer. */
export function storedComputers(storage = defaultStorage()): Computer[] {
  return [homeComputer(), ...readPeers(storage).map((peer) => toComputer(peer))];
}

/**
 * Reads this device's list from home and **replaces** the stored list with
 * it (a removed computer disappears; a compromised home's old entries do not
 * linger). Throws ApiError when home cannot answer; the stored list stays.
 */
export async function refreshComputers(storage = defaultStorage()): Promise<Computer[]> {
  const peers = cleanPeers((await client().computers()).computers);
  writePeers(peers, storage);
  return [homeComputer(), ...peers.map((peer) => toComputer(peer))];
}

function savePeer(peer: HTTPComputer, storage: StorageLike | undefined): void {
  const peers = readPeers(storage);
  const index = peers.findIndex((entry) => entry.origin === peer.origin);
  writePeers(index < 0 ? [...peers, peer] : peers.map((entry, at) => (at === index ? peer : entry)), storage);
}

/**
 * Asks home to record a pending computer before pairing with it, so home's
 * CSP lets the app reach it after the next load. Home checks the host in its
 * own tailscale status; the answer's `node` is for the confirm sheet. Pending
 * entries are never shown and expire on home.
 */
export function recordPending(origin: string, label = firstLabel(origin)): Promise<HTTPComputerAddResponse> {
  return client().saveComputer({ origin, label, state: 'pending' });
}

/**
 * Cancel before the computer was added: deletes its pending entry on home, so
 * home's CSP stops allowing it. A computer already in the list is never
 * touched (home keeps a confirmed entry when the same link is added again).
 * Best effort: a pending entry also expires on home.
 */
export async function forgetPending(origin: string, storage = defaultStorage()): Promise<void> {
  if (readPeers(storage).some((entry) => entry.origin === origin)) return;
  try {
    await client().deleteComputer(new URL(origin).host);
  } catch {
    // It expires on home.
  }
}

/** Confirms a computer on home (after its first authenticated settings read) and stores it. */
export async function confirmComputer(origin: string, label = firstLabel(origin), storage = defaultStorage()): Promise<Computer> {
  const saved = await client().saveComputer({ origin, label, state: 'confirmed' });
  savePeer(saved.computer, storage);
  return toComputer(saved.computer);
}

/** Renames a confirmed computer on home and here. Check the label with deviceLabelProblem first (same rules). */
export const renameComputer = confirmComputer;

export type RemoveResult =
  | { readonly loggedOut: true }
  /** The peer could not be reached (or refused): the phone may still be signed in there. */
  | { readonly loggedOut: false; readonly hint: string };

/**
 * Removes a peer: first signs this phone out on the peer (`POST
 * /api/v1/logout`, which revokes this device's credential there), then
 * removes it from this device's list on home and here. A 401 from the peer
 * means it is already signed out. If the peer cannot confirm, the result says
 * to run `pterm revoke` there. Throws ApiError when home cannot remove it;
 * nothing is removed then, and a retry is safe.
 */
export async function removeComputer(computer: Pick<Computer, 'origin' | 'host' | 'label'>, storage = defaultStorage()): Promise<RemoveResult> {
  let loggedOut: boolean;
  try {
    await client(computer.origin).logout();
    loggedOut = true;
  } catch (error) {
    loggedOut = isAuthFailure(error);
  }
  await client().deleteComputer(computer.host);
  writePeers(readPeers(storage).filter((entry) => entry.origin !== computer.origin), storage);
  return loggedOut ? { loggedOut } : { loggedOut, hint: `Run pterm revoke on ${computer.label} to sign this phone out there.` };
}

/**
 * Log out, before home: signs this phone out on each peer (best effort, all
 * at once). A peer that is not reachable now is skipped, so log out never
 * waits for a timeout. A 401 means it is already signed out. Returns the
 * labels of the peers where this phone may still be signed in.
 */
export async function logoutPeers(peers: ReadonlyArray<{ readonly computer: Computer; readonly reachable: boolean }>): Promise<string[]> {
  const left = await Promise.all(peers.map(async ({ computer, reachable }) => {
    if (computer.home) return undefined;
    if (!reachable) return computer.label;
    try {
      await client(computer.origin).logout();
      return undefined;
    } catch (error) {
      return isAuthFailure(error) ? undefined : computer.label;
    }
  }));
  return left.filter((label) => label !== undefined);
}

/** What Log out says: signed out, plus the peers to revoke by hand. */
export function logoutNote(left: ReadonlyArray<string>): string {
  const done = 'You logged out. This phone no longer has access.';
  if (left.length === 0) return done;
  return `${done} Could not sign out of ${left.join(', ')}. Run pterm revoke there to remove this phone.`;
}
