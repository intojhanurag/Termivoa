import { ApiError, client, firstLabel } from '../api/http';
import { confirmComputer, forgetPending, recordPending, removeComputer, storedComputers, type Computer } from '../computers';
import type { HTTPComputerAddResponse } from '../generated/protocol';
import { parseHttpBody } from '../protocol/httpBody';
import { GRANT, PairingFlow, type HistoryLike, type LocationLike, type PendingPairing } from './pairing';

/**
 * Adding a computer, in order (docs/multi-computer.md, "Pairing a second computer"):
 *
 * 1. The peer CLI (`pterm pair --via`) prints
 *    `https://<peer-host>/add-computer#grant=<grant>&via=<app origin>`.
 *    parseAddComputerLink checks it against this app.
 * 2. prepareAddComputer records the peer as pending on home and returns the
 *    peer's tailnet node for the confirm sheet (full origin, machine, owner).
 * 3. On confirm, the app loads addComputerUrl(link): home's CSP after that
 *    load allows the pending origin. takeAddComputerFragment removes the
 *    fragment at once on that load.
 * 4. AddComputerFlow.redeem redeems the grant on the peer with credentials
 *    (show the verification code), then AddComputerFlow.finish polls until
 *    approved, makes the first authenticated `GET /api/v1/settings` on the
 *    peer (which also checks its protocol revision; a network error is
 *    retried for FIRST_READ_RETRY_MS) and confirms it on home. Cancel at any
 *    point wins: after approval it signs out on the peer and deletes the
 *    entry on home.
 *
 * The grant never goes to storage or a query string.
 */

export const ADD_COMPUTER_PATH = '/computers/add';
/** The peer page path in links printed by `pterm pair --via`; its own pair page never reads it. */
export const PEER_ADD_PATH = '/add-computer';

export interface AppLocation {
  /** This app's origin, for example `https://mac.tailx.ts.net`. */
  readonly origin: string;
  readonly hostname: string;
}

export interface AddComputerLink {
  /** The peer's canonical origin. */
  readonly origin: string;
  readonly grant: string;
  /** On the add page: the app path the add was started from (Back and the page's way out go there). */
  readonly back?: string;
}

/** An app path on this origin only: one leading slash, no scheme, host, query or fragment. */
const APP_PATH = /^\/(?!\/)[A-Za-z0-9\-._~%/:]{0,512}$/u;

/** A session's address (`/sessions/<id>` or `/c/<host>/sessions/<id>`): the add was started from its terminal. */
export const fromTerminal = (back: string | undefined): boolean => back !== undefined && /^\/(?:c\/[^/]+\/)?sessions\/[^/]+$/u.test(back);

export type LinkProblem = 'format' | 'tailnet' | 'self' | 'via' | 'grant';

/** Plain words for each problem, for the paste field. */
export const LINK_PROBLEMS: Readonly<Record<LinkProblem, string>> = {
  format: 'This is not an add-computer link. Copy the whole link that pterm pair --via printed.',
  tailnet: 'That computer is not in the same tailnet as this one.',
  self: 'That link is for this computer. Run pterm pair --via on the other computer.',
  via: 'That link was made for another Termivoa app. Run pterm pair --via with this computer\'s name.',
  grant: 'The link is damaged or incomplete. Copy it again.',
};

/** `mac.tailx.ts.net` → `tailx.ts.net`; undefined for a host with fewer than three labels. */
export function tailnetSuffix(hostname: string): string | undefined {
  const labels = hostname.split('.');
  return labels.length >= 3 ? labels.slice(1).join('.') : undefined;
}

/**
 * A peer origin this app may add: canonical (https, lowercase, default port,
 * no trailing dot), exactly one label under this app's tailnet suffix, and not
 * this app's own origin.
 */
function peerProblem(origin: string, app: AppLocation): LinkProblem | undefined {
  try {
    parseHttpBody('computer', { origin, label: 'x' });
  } catch {
    return 'format';
  }
  const hostname = new URL(origin).hostname;
  const suffix = tailnetSuffix(app.hostname);
  if (!app.origin.startsWith('https://') || suffix === undefined || hostname.slice(hostname.indexOf('.') + 1) !== suffix) {
    return 'tailnet';
  }
  if (origin === app.origin) return 'self';
  return undefined;
}

function single(params: URLSearchParams, name: string): string | undefined {
  const values = params.getAll(name);
  return values.length === 1 ? values[0] : undefined;
}

/**
 * Checks a pasted or tapped add-computer link. Only the exact canonical
 * spelling is accepted (no port, user, query, other path or upper case), so
 * a near-miss host cannot slip through normalisation.
 */
export function parseAddComputerLink(text: string, app: AppLocation): AddComputerLink | { readonly problem: LinkProblem } {
  let url: URL;
  try {
    url = new URL(text.trim());
  } catch {
    return { problem: 'format' };
  }
  if (url.protocol !== 'https:' || text.trim() !== `${url.origin}${PEER_ADD_PATH}${url.hash}`) {
    return { problem: 'format' };
  }
  const problem = peerProblem(url.origin, app);
  if (problem !== undefined) return { problem };
  const params = new URLSearchParams(url.hash.slice(1));
  if (single(params, 'via') !== app.origin) return { problem: 'via' };
  const grant = single(params, 'grant');
  if (grant === undefined || !GRANT.test(grant)) return { problem: 'grant' };
  return { origin: url.origin, grant };
}

/** Step 2: records the peer as pending on home. Show `node` (owner or tags) in the confirm sheet. */
export function prepareAddComputer(link: AddComputerLink): Promise<HTTPComputerAddResponse> {
  return recordPending(link.origin);
}

/**
 * Step 3: the home URL to load next. The grant rides in the fragment, never
 * in a query. `back` is the app path the add starts from. It must be a full
 * load: home's CSP for this document does not allow the pending origin yet.
 */
export function addComputerUrl(link: AddComputerLink, back?: string): string {
  const params = new URLSearchParams({ grant: link.grant, origin: link.origin });
  if (back !== undefined && APP_PATH.test(back)) params.set('back', back);
  return `${ADD_COMPUTER_PATH}#${params.toString()}`;
}

/**
 * Step 3, on the load of ADD_COMPUTER_PATH: removes the fragment from the URL
 * at once and returns the link when it is still valid. Call it once at start,
 * before takeGrantFromFragment (which strips every fragment).
 *
 * A valid link also gets an entry before it in this document's history: its
 * `back` path (or `/`). So Back, and the page's own way out, stay in this
 * load and the add keeps running behind the banner; a new load would lose it.
 */
export function takeAddComputerFragment(
  location: LocationLike, history: HistoryLike & Partial<Pick<History, 'pushState'>>, app: AppLocation,
): AddComputerLink | undefined {
  if (location.pathname !== ADD_COMPUTER_PATH || location.hash.length <= 1) return undefined;
  const params = new URLSearchParams(location.hash.slice(1));
  const page = `${location.pathname}${location.search}`;
  history.replaceState(null, '', page);
  const origin = single(params, 'origin');
  const grant = single(params, 'grant');
  if (origin === undefined || grant === undefined || !GRANT.test(grant) || peerProblem(origin, app) !== undefined) {
    return undefined;
  }
  const asked = single(params, 'back');
  const back = asked !== undefined && APP_PATH.test(asked) && asked !== ADD_COMPUTER_PATH ? asked : '/';
  if (history.pushState !== undefined) {
    history.replaceState(null, '', back);
    history.pushState(null, '', page);
  }
  return { origin, grant, back };
}

/**
 * Right after approval the peer may not yet allow this app's origin (it
 * refreshes its CORS list on its own schedule), so the first settings read
 * can fail as a network error. Retry it for this long.
 */
export const FIRST_READ_RETRY_MS = 3_000;
const FIRST_READ_STEP_MS = 500;

export type AddOutcome =
  | { readonly kind: 'confirmed'; readonly computer: Computer }
  | { readonly kind: 'expired' }
  | { readonly kind: 'cancelled' };

const CANCELLED: AddOutcome = { kind: 'cancelled' };

/** Step 4. One flow per link; the grant and poll secret stay in memory only. */
export class AddComputerFlow {
  private grant: string | undefined;
  private cancelled = false;
  private readonly pairing: PairingFlow;
  /** Already in the list (the same link added again): a cancel never touches its entry or cookie. */
  private readonly listed: boolean;

  constructor(readonly origin: string, grant: string, private readonly label = firstLabel(origin)) {
    this.grant = grant;
    this.pairing = new PairingFlow(client(origin));
    this.listed = storedComputers().some((computer) => computer.origin === origin);
  }

  /** Redeems the grant on the peer once; show the returned verification code. */
  async redeem(deviceLabel: string): Promise<PendingPairing> {
    const grant = this.grant;
    if (grant === undefined) throw new Error('This add-computer link was already used.');
    this.grant = undefined;
    return this.pairing.redeem(grant, deviceLabel);
  }

  /**
   * Waits for approval on the peer, reads its settings with its new cookie,
   * then confirms it on home. Throws ApiError from the settings read (for
   * example `computer_outdated`) or from home; the peer then stays pending
   * on home and expires. After cancel the outcome is always `cancelled`, and
   * a cancel after approval undoes it (abandon).
   */
  async finish(pending: PendingPairing): Promise<AddOutcome> {
    let approved = false;
    try {
      const outcome = await this.pairing.poll(pending);
      if (outcome !== 'approved') return this.cancelled ? CANCELLED : { kind: outcome };
      approved = true;
      if (!this.cancelled) await this.firstRead();
      if (!this.cancelled) {
        const computer = await confirmComputer(this.origin, this.label);
        if (!this.cancelled) return { kind: 'confirmed', computer };
      }
    } catch (error) {
      if (!this.cancelled) throw error;
    }
    if (approved) await this.abandon();
    return CANCELLED;
  }

  cancel(): void {
    this.cancelled = true;
    this.grant = undefined;
    this.pairing.cancel();
  }

  /** The first settings read; a network error is retried for FIRST_READ_RETRY_MS, unless cancelled. */
  private async firstRead(): Promise<void> {
    const deadline = Date.now() + FIRST_READ_RETRY_MS;
    for (;;) {
      try {
        await client(this.origin).settings();
        return;
      } catch (error) {
        if (!(error instanceof ApiError && error.code === 'network') || Date.now() >= deadline) throw error;
        await new Promise((resolve) => { setTimeout(resolve, FIRST_READ_STEP_MS); });
        if (this.cancelled) return;
      }
    }
  }

  /**
   * Cancelled after the peer approved: this phone holds a new cookie there.
   * Signs out on the peer and deletes the entry on home (pending or, if the
   * confirm already went through, confirmed). Best effort: a pending entry
   * also expires on home, and the peer's device can be revoked there.
   */
  private async abandon(): Promise<void> {
    if (this.listed) return;
    await removeComputer({ origin: this.origin, host: new URL(this.origin).host, label: this.label }).catch(() => undefined);
  }
}

/** Where an add stands. */
export type AddPhase =
  | { readonly kind: 'redeeming' }
  | { readonly kind: 'waiting'; readonly pending: PendingPairing }
  | { readonly kind: 'done'; readonly computer: Computer }
  | { readonly kind: 'expired' | 'cancelled' }
  | { readonly kind: 'error'; readonly error: unknown };

export interface AddTask {
  readonly origin: string;
  readonly label: string;
  readonly phase: AddPhase;
  /** The user closed its banner after it ended. */
  readonly dismissed?: boolean;
}

/*
 * The add of this load, kept outside any screen: the user may leave the add
 * page to type y in a terminal of this same app (ssh to the new computer),
 * and the poll must go on. It stops only on Cancel, expiry or log out. An add
 * link always arrives with a full load, so there is at most one per load.
 */
let task: AddTask | undefined;
let running: AddComputerFlow | undefined;
const listeners = new Set<() => void>();

function publish(next: AddTask | undefined): void {
  task = next;
  for (const listener of listeners) listener();
}

/** The add of this load, running or ended; undefined until one starts. For useSyncExternalStore. */
export const currentAdd = (): AddTask | undefined => task;

export function subscribeAdd(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/**
 * Redeems the link, shows the code, waits for approval and confirms, in the
 * background. Once per load: the grant works once. Outcomes after a Cancel
 * are ignored; the flow, its grant and its poll secret are dropped when it
 * ends.
 */
export async function startAdd(link: AddComputerLink, deviceLabel: string): Promise<void> {
  if (task !== undefined) return;
  const flow = new AddComputerFlow(link.origin, link.grant);
  running = flow;
  const label = firstLabel(link.origin);
  const update = (phase: AddPhase) => { if (running === flow) publish({ origin: link.origin, label, phase }); };
  update({ kind: 'redeeming' });
  try {
    const pending = await flow.redeem(deviceLabel);
    update({ kind: 'waiting', pending });
    const outcome = await flow.finish(pending);
    update(outcome.kind === 'confirmed' ? { kind: 'done', computer: outcome.computer } : { kind: outcome.kind });
  } catch (error) {
    update({ kind: 'error', error });
  } finally {
    if (running === flow) running = undefined;
  }
}

/**
 * Cancel (the user, or log out): stops the running add and deletes its
 * pending entry on home; after approval the flow also signs out on the new
 * computer (AddComputerFlow.finish). Does nothing when no add runs.
 */
export function cancelAdd(): void {
  const flow = running;
  if (flow === undefined || task === undefined) return;
  running = undefined;
  flow.cancel();
  void forgetPending(flow.origin);
  publish({ ...task, phase: { kind: 'cancelled' } });
}

/** Hides the banner of an add that ended. A running add keeps its banner. */
export function dismissAdd(): void {
  if (running === undefined && task !== undefined && task.dismissed !== true) publish({ ...task, dismissed: true });
}
