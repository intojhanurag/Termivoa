import { api, ApiError } from '../api/http';

const GRANT = /^[A-Za-z0-9_-]{43}$/u;
export const POLL_INTERVAL_MS = 2000;

interface LocationLike {
  readonly hash: string;
  readonly pathname: string;
  readonly search: string;
}

interface HistoryLike {
  replaceState(data: unknown, unused: string, url: string): void;
}

/**
 * Removes any URL fragment from the history entry before anything else can
 * observe it, and returns the pairing grant only on /pair. The app never uses
 * fragments, so a mistyped path must not leave a secret in the URL. Call once
 * at startup.
 */
export function takeGrantFromFragment(location: LocationLike, history: HistoryLike): string | undefined {
  const raw = location.hash.replace(/^#/u, '');
  if (raw === '') {
    return undefined;
  }
  history.replaceState(null, '', `${location.pathname}${location.search}`);
  if (location.pathname !== '/pair') {
    return undefined;
  }
  const grant = raw.startsWith('grant=') ? raw.slice('grant='.length) : raw;
  return GRANT.test(grant) ? grant : undefined;
}

/** In-memory holder for a pairing grant that has not been redeemed yet. */
export interface GrantSlot {
  value: string | undefined;
}

/**
 * Takes a grant from a pairing link opened in the tab that already runs the
 * app. Only the fragment changes then, so the page does not reload and the
 * startup read never sees it. Returns true when a new grant was taken.
 */
export function adoptFragmentGrant(slot: GrantSlot, location: LocationLike, history: HistoryLike): boolean {
  const grant = takeGrantFromFragment(location, history);
  if (grant === undefined) {
    return false;
  }
  slot.value = grant;
  return true;
}

/**
 * Hands out the grant once, only when pairing can start. An invalid device
 * name keeps the grant so the user can fix the name and try again.
 */
export function claimGrant(
  slot: GrantSlot, labelValid: boolean,
): { readonly secret: string } | { readonly problem: 'label' | 'missing' } {
  if (!labelValid) {
    return { problem: 'label' };
  }
  const secret = slot.value;
  if (secret === undefined) {
    return { problem: 'missing' };
  }
  slot.value = undefined;
  return { secret };
}

export interface PendingPairing {
  readonly pendingId: string;
  readonly verificationCode: string;
  readonly expiresAt: number;
}

export type PairingOutcome = 'approved' | 'expired' | 'cancelled';

/** Holds the poll secret in memory only, and only until polling ends. */
export class PairingFlow {
  private pollSecret: string | undefined;
  private cancelled = false;
  private wake: (() => void) | undefined;

  async redeem(grant: string, deviceLabel: string): Promise<PendingPairing> {
    const response = await api.pairRedeem({ grant, deviceLabel });
    this.pollSecret = response.pollSecret;
    const expiresAt = Date.parse(response.expiresAt);
    return {
      pendingId: response.pendingId,
      verificationCode: response.verificationCode,
      expiresAt: Number.isNaN(expiresAt) ? Date.now() + 2 * 60 * 1000 : expiresAt,
    };
  }

  async poll(pending: PendingPairing): Promise<PairingOutcome> {
    try {
      while (!this.cancelled) {
        if (Date.now() >= pending.expiresAt) {
          return 'expired';
        }
        await this.sleep(POLL_INTERVAL_MS);
        const pollSecret = this.pollSecret;
        if (this.cancelled || pollSecret === undefined) {
          return 'cancelled';
        }
        try {
          const response = await api.pairPoll({ pendingId: pending.pendingId, pollSecret });
          if (response.status === 'approved') {
            return 'approved';
          }
        } catch (error) {
          if (error instanceof ApiError && error.code === 'network') {
            continue;
          }
          if (error instanceof ApiError && (error.status === 410 || error.status === 401 || error.code === 'pairing_expired')) {
            return 'expired';
          }
          throw error;
        }
      }
      return 'cancelled';
    } finally {
      this.pollSecret = undefined;
    }
  }

  cancel(): void {
    this.cancelled = true;
    this.pollSecret = undefined;
    this.wake?.();
  }

  holdsSecret(): boolean {
    return this.pollSecret !== undefined;
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        this.wake = undefined;
        resolve();
      }, ms);
      this.wake = () => {
        clearTimeout(timer);
        resolve();
      };
    });
  }
}
