import { api, client, isAuthFailure, type Client } from './api/http';
import { storedComputers, type Computer } from './computers';
import type { HTTPPushSubscribeRequest } from './generated/protocol';
import { storage } from './storage';

/** Set to '1' after "Not now" or after push is turned on, so the Sessions card shows once. */
export const PUSH_CARD_KEY = 'pterm.pushCard.dismissed';

export const PUSH_BLOCKED = 'Notifications are blocked. Allow them in Settings.';

export type PushSupport = 'available' | 'needs-home-screen' | 'unsupported';
/** `missing` settles (it never rejects) with the labels of the peers that could not be subscribed; home is on already. */
export type PushResult = { readonly ok: true; readonly missing: Promise<string[]> } | { readonly ok: false; readonly message: string };

type Peer = Pick<Computer, 'origin' | 'host' | 'label'>;

/**
 * A peer's push registration: /sw.js at this scope on home's origin (a
 * registration must be on the page's origin, and one subscription has one
 * VAPID key). The computer answers 404 for every path under /push/ and the
 * worker handles no fetch there, so the app never loads inside this scope.
 * The worker reads the host from its scope, never from a push payload.
 */
export const peerScope = (host: string): string => `/push/${host}/`;
const PEER_SCOPE = /^\/push\/([^/]+)\/$/u;

/** The confirmed peers in this phone's stored list. */
const storedPeers = (): Peer[] => storedComputers().filter((computer) => !computer.home);

/** The peer push registrations on this origin, by host. */
async function peerRegistrations(): Promise<Map<string, ServiceWorkerRegistration>> {
  const found = new Map<string, ServiceWorkerRegistration>();
  for (const registration of await navigator.serviceWorker.getRegistrations()) {
    const host = PEER_SCOPE.exec(new URL(registration.scope).pathname)?.[1];
    if (host !== undefined) found.set(host, registration);
  }
  return found;
}

/** Unsubscribes and unregisters one peer registration, in the browser only. */
async function drop(registration: ServiceWorkerRegistration | undefined): Promise<void> {
  if (registration === undefined) return;
  try {
    await (await registration.pushManager.getSubscription())?.unsubscribe();
  } finally {
    // Also when unsubscribe fails: a registration left behind would be a stray on the next load.
    await registration.unregister();
  }
}

/** Resolves when the registration has an active worker (subscribe needs one); rejects when its install fails. */
function activeWorker(registration: ServiceWorkerRegistration): Promise<ServiceWorkerRegistration> {
  const worker = registration.installing ?? registration.waiting;
  if (registration.active !== null || worker === null) return Promise.resolve(registration);
  return new Promise((resolve, reject) => {
    worker.addEventListener('statechange', () => {
      if (worker.state === 'activated') resolve(registration);
      else if (worker.state === 'redundant') reject(new Error('The service worker did not install.'));
    });
  });
}

/**
 * Whether this browser can take push. iOS and iPadOS give push only to a
 * Home Screen app, so a normal Safari tab there gets a hint instead.
 */
export function pushSupport(): PushSupport {
  if (typeof navigator === 'undefined') return 'unsupported';
  const agent = navigator.userAgent;
  const ios = /iP(hone|ad|od)/u.test(agent) || (/Macintosh/u.test(agent) && navigator.maxTouchPoints > 1);
  const standalone = (navigator as { standalone?: boolean }).standalone === true
    || globalThis.matchMedia?.('(display-mode: standalone)').matches === true;
  if (ios && !standalone) return 'needs-home-screen';
  return 'PushManager' in globalThis && 'serviceWorker' in navigator && 'Notification' in globalThis ? 'available' : 'unsupported';
}

/**
 * Home's registration, by its scope / and never by this page's address: a
 * page inside a peer's push scope would get that peer's registration.
 * getRegistration, not ready: ready never settles when no worker is registered (dev builds).
 */
const homeRegistration = (): Promise<ServiceWorkerRegistration | undefined> => navigator.serviceWorker.getRegistration('/');

async function currentSubscription(): Promise<PushSubscription | null> {
  return await (await homeRegistration())?.pushManager.getSubscription() ?? null;
}

/** True when permission is granted and this phone holds a push subscription. */
export async function pushEnabled(): Promise<boolean> {
  if (pushSupport() !== 'available' || Notification.permission !== 'granted') return false;
  return await currentSubscription() !== null;
}

export function pushCardDismissed(): boolean {
  return storage()?.getItem(PUSH_CARD_KEY) === '1';
}

export function dismissPushCard(): void {
  try {
    storage()?.setItem(PUSH_CARD_KEY, '1');
  } catch {
    // Private mode or full storage: the card simply shows again next time.
  }
}

/** Whether the Sessions screen should offer the one-time card. Never rejects. */
export async function shouldOfferPush(): Promise<boolean> {
  try {
    return pushSupport() === 'available' && !pushCardDismissed() && !await pushEnabled();
  } catch {
    return false;
  }
}

export function toBase64Url(bytes: ArrayBuffer): string {
  let binary = '';
  for (const byte of new Uint8Array(bytes)) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/u, '');
}

export function fromBase64Url(text: string): Uint8Array<ArrayBuffer> {
  const binary = atob(text.replace(/-/gu, '+').replace(/_/gu, '/'));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

/** Only the fields the server accepts; strict decoding rejects expirationTime and the rest. */
export function subscribeBody(subscription: PushSubscription): HTTPPushSubscribeRequest {
  const key = (name: PushEncryptionKeyName) => {
    const value = subscription.getKey(name);
    if (value === null) throw new Error('The browser gave no push key.');
    return toBase64Url(value);
  };
  return { endpoint: subscription.endpoint, keys: { p256dh: key('p256dh'), auth: key('auth') } };
}

const sameBytes = (a: ArrayBuffer | null | undefined, b: Uint8Array) =>
  a != null && a.byteLength === b.byteLength && new Uint8Array(a).every((byte, index) => byte === b[index]);

/**
 * Subscribes one registration with one computer's key and registers it there.
 * The key is read first, so a computer that refuses gets no registration.
 */
async function subscribeOn(computer: Client, registration: () => Promise<ServiceWorkerRegistration>): Promise<void> {
  const { publicKey } = await computer.pushConfig();
  const applicationServerKey = fromBase64Url(publicKey);
  const { pushManager } = await registration();
  let subscription = await pushManager.getSubscription();
  if (subscription !== null && !sameBytes(subscription.options.applicationServerKey, applicationServerKey)) {
    // The computer has a new key: a subscription under the old one can no longer be sent to.
    await subscription.unsubscribe();
    subscription = null;
  }
  subscription ??= await pushManager.subscribe({ userVisibleOnly: true, applicationServerKey });
  await computer.pushSubscribe(subscribeBody(subscription));
}

/**
 * Subscribes each peer on its own scoped registration, all at once. One peer
 * that fails never stops the others. Returns the labels of the peers left
 * without notifications. A peer that answers "pair again" (401) loses its
 * registration.
 */
async function subscribePeers(peers: ReadonlyArray<Peer>): Promise<string[]> {
  const missing = await Promise.all(peers.map(async (peer) => {
    // Push is still on and the peer is still listed. A peer can hold its
    // answers for seconds; push turned off, a log out or a removal in that
    // time must win.
    const wanted = async () => await pushEnabled() && storedPeers().some((stored) => stored.host === peer.host);
    const forget = () => peerRegistrations().then((found) => drop(found.get(peer.host))).catch(() => undefined);
    try {
      await subscribeOn(client(peer.origin), async () => {
        if (!await wanted()) throw new Error('Notifications were turned off.');
        return activeWorker(await navigator.serviceWorker.register('/sw.js', { scope: peerScope(peer.host) }));
      });
      // Turned off while this registered or while the peer held the subscribe call.
      if (!await wanted()) await forget();
      return undefined;
    } catch (error) {
      if (isAuthFailure(error)) await forget();
      return peer.label;
    }
  }));
  return missing.filter((label) => label !== undefined);
}

/**
 * Asks for permission, subscribes this phone and registers it with home, then
 * with each peer. Call it straight from a click handler: iOS allows the
 * permission prompt only inside a user gesture, so nothing is awaited before
 * requestPermission. Home's network and API failures throw ApiError. The
 * peers are not waited for (one that cannot be reached takes up to 15 s):
 * `missing` settles later with the labels of the peers that failed.
 */
export async function enablePush(peers: ReadonlyArray<Peer> = storedPeers()): Promise<PushResult> {
  const permission = await Notification.requestPermission();
  if (permission === 'denied') return { ok: false, message: PUSH_BLOCKED };
  if (permission !== 'granted') return { ok: false, message: 'Notifications were not allowed.' };
  await subscribeOn(api, async () => {
    const registration = await homeRegistration();
    if (registration === undefined) throw new Error('The app is not ready for notifications. Reload it.');
    return activeWorker(registration);
  });
  dismissPushCard();
  return { ok: true, missing: subscribePeers(peers) };
}

/**
 * Makes the peer registrations match the list. With push on, every listed
 * peer is subscribed (also one added later; permission is already granted, so
 * there is no prompt). Every other peer registration is unsubscribed and
 * unregistered (a removed computer, log out, push off). Run it on every load
 * and when the list changes. Never rejects; returns the labels of the peers
 * left without notifications.
 */
export async function syncPeerPush(peers: ReadonlyArray<Peer> = storedPeers()): Promise<string[]> {
  try {
    const wanted = await pushEnabled() ? peers : [];
    for (const [host, registration] of await peerRegistrations()) {
      if (!wanted.some((peer) => peer.host === host)) await drop(registration).catch(() => undefined);
    }
    return await subscribePeers(wanted);
  } catch {
    return [];
  }
}

/**
 * Sends a test notification from every computer. `sent` is home's answer;
 * home's failures throw ApiError. The peers are not waited for: `missing`
 * settles later (it never rejects) with the labels of the peers that sent
 * none (not reachable, or no subscription there).
 */
export async function testPush(peers: ReadonlyArray<Peer> = storedPeers()): Promise<{ readonly sent: boolean; readonly missing: Promise<string[]> }> {
  const missing = Promise.all(peers.map((peer) => client(peer.origin).pushTest()
    .then(({ sent }) => (sent > 0 ? undefined : peer.label), () => peer.label)))
    .then((labels) => labels.filter((label) => label !== undefined));
  return { sent: (await api.pushTest()).sent > 0, missing };
}

/**
 * Removes this phone's subscription from every computer (best effort) and
 * from the browser, and unregisters the peer registrations.
 */
export async function disablePush(peers: ReadonlyArray<Peer> = storedPeers()): Promise<void> {
  const found = await peerRegistrations().catch(() => new Map<string, ServiceWorkerRegistration>());
  await Promise.all([...found].map(async ([host, registration]) => {
    try {
      const endpoint = (await registration.pushManager.getSubscription())?.endpoint;
      const origin = peers.find((peer) => peer.host === host)?.origin;
      // Not waited for: a peer that cannot be reached would hold the switch for 15 s.
      if (endpoint !== undefined && origin !== undefined) void client(origin).pushUnsubscribe({ endpoint }).catch(() => undefined);
      await drop(registration);
    } catch {
      // The next load removes it (syncPeerPush).
    }
  }));
  const subscription = await currentSubscription();
  if (subscription === null) return;
  try {
    await api.pushUnsubscribe({ endpoint: subscription.endpoint });
  } catch {
    // Still unsubscribe below; the host drops endpoints the push service rejects.
  }
  await subscription.unsubscribe();
}

/**
 * One of the app's own registrations: on this origin, at scope / or at the
 * exact push scope of a listed peer, and EVERY worker in it (installing,
 * waiting, active) is /sw.js. A fresh registration that has no worker yet
 * (the first install) is ours too.
 */
function ownWorker(registration: ServiceWorkerRegistration, origin: string, scopes: ReadonlySet<string>): boolean {
  try {
    const scope = new URL(registration.scope);
    if (scope.origin !== origin || !scopes.has(scope.pathname)) return false;
    return [registration.installing, registration.waiting, registration.active]
      .every((worker) => worker === null || worker === undefined || new URL(worker.scriptURL).href === `${origin}/sw.js`);
  } catch {
    return false;
  }
}

/**
 * Unregisters every service worker that is not the app's own, for example
 * one a compromised home left behind with its own push key. The allow-list
 * is /sw.js at scope / plus /sw.js at the exact push scope of each peer in
 * the stored list (`hosts`); a push scope of any other host is removed too.
 * Run it on every load. Never rejects; returns how many it removed.
 */
export async function removeStrayWorkers(
  container: ServiceWorkerContainer | undefined = globalThis.navigator?.serviceWorker,
  hosts: ReadonlyArray<string> = storedPeers().map((peer) => peer.host),
): Promise<number> {
  let removed = 0;
  try {
    if (container === undefined) return 0;
    const { origin } = globalThis.location;
    const scopes = new Set(['/', ...hosts.map(peerScope)]);
    for (const registration of await container.getRegistrations()) {
      if (ownWorker(registration, origin, scopes)) continue;
      try {
        if (await registration.unregister()) removed += 1;
      } catch {
        // Try the rest; the next load tries this one again.
      }
    }
  } catch {
    // No service worker support here, or the browser refused the list.
  }
  return removed;
}

/**
 * Runs removeStrayWorkers, then says whether this load may redeem a grant.
 * unregister() does not stop a worker that already controls the page: until
 * the next load it still sees every request, including a redeem body. So a
 * load is safe only when nothing was removed and the page is not controlled
 * by a worker other than /sw.js (no controller at all, as on the first
 * visit, is safe). Never rejects.
 */
export async function workersSafe(
  container: ServiceWorkerContainer | undefined = globalThis.navigator?.serviceWorker,
  hosts?: ReadonlyArray<string>,
): Promise<boolean> {
  const removed = await removeStrayWorkers(container, hosts);
  const script = container?.controller?.scriptURL;
  return removed === 0 && (script === undefined || script === `${globalThis.location.origin}/sw.js`);
}
