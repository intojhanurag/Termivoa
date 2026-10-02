import { client, firstLabel } from '../api/http';
import { confirmComputer, recordPending, type Computer } from '../computers';
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
 *    peer (which also checks its protocol revision) and confirms it on home.
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
}

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

/** Step 3: the home URL to load next. The grant rides in the fragment, never in a query. */
export function addComputerUrl(link: AddComputerLink): string {
  return `${ADD_COMPUTER_PATH}#${new URLSearchParams({ grant: link.grant, origin: link.origin }).toString()}`;
}

/**
 * Step 3, on the load of ADD_COMPUTER_PATH: removes the fragment from the URL
 * at once and returns the link when it is still valid. Call it once at start,
 * before takeGrantFromFragment (which strips every fragment).
 */
export function takeAddComputerFragment(
  location: LocationLike, history: HistoryLike, app: AppLocation,
): AddComputerLink | undefined {
  if (location.pathname !== ADD_COMPUTER_PATH || location.hash.length <= 1) return undefined;
  const params = new URLSearchParams(location.hash.slice(1));
  history.replaceState(null, '', `${location.pathname}${location.search}`);
  const origin = single(params, 'origin');
  const grant = single(params, 'grant');
  if (origin === undefined || grant === undefined || !GRANT.test(grant) || peerProblem(origin, app) !== undefined) {
    return undefined;
  }
  return { origin, grant };
}

export type AddOutcome =
  | { readonly kind: 'confirmed'; readonly computer: Computer }
  | { readonly kind: 'expired' }
  | { readonly kind: 'cancelled' };

/** Step 4. One flow per link; the grant and poll secret stay in memory only. */
export class AddComputerFlow {
  private grant: string | undefined;
  private readonly pairing: PairingFlow;

  constructor(readonly origin: string, grant: string, private readonly label = firstLabel(origin)) {
    this.grant = grant;
    this.pairing = new PairingFlow(client(origin));
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
   * on home and expires.
   */
  async finish(pending: PendingPairing): Promise<AddOutcome> {
    const outcome = await this.pairing.poll(pending);
    if (outcome !== 'approved') return { kind: outcome };
    await client(this.origin).settings();
    return { kind: 'confirmed', computer: await confirmComputer(this.origin, this.label) };
  }

  cancel(): void {
    this.grant = undefined;
    this.pairing.cancel();
  }
}
