import { api } from './api/http';
import type { HTTPPushSubscribeRequest } from './generated/protocol';

/** Set to '1' after "Not now" or after push is turned on, so the Sessions card shows once. */
export const PUSH_CARD_KEY = 'pterm.pushCard.dismissed';

export const PUSH_BLOCKED = 'Notifications are blocked. Allow them in Settings.';

export type PushSupport = 'available' | 'needs-home-screen' | 'unsupported';
export type PushResult = { readonly ok: true } | { readonly ok: false; readonly message: string };

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

async function currentSubscription(): Promise<PushSubscription | null> {
  // getRegistration, not ready: ready never settles when no worker is registered (dev builds).
  const registration = await navigator.serviceWorker.getRegistration();
  return await registration?.pushManager.getSubscription() ?? null;
}

/** True when permission is granted and this phone holds a push subscription. */
export async function pushEnabled(): Promise<boolean> {
  if (pushSupport() !== 'available' || Notification.permission !== 'granted') return false;
  return await currentSubscription() !== null;
}

function storage(): Pick<Storage, 'getItem' | 'setItem'> | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
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
 * Asks for permission, subscribes this phone and registers it with the host.
 * Call it straight from a click handler: iOS allows the permission prompt only
 * inside a user gesture, so nothing is awaited before requestPermission.
 * Network and API failures throw ApiError.
 */
export async function enablePush(): Promise<PushResult> {
  const permission = await Notification.requestPermission();
  if (permission === 'denied') return { ok: false, message: PUSH_BLOCKED };
  if (permission !== 'granted') return { ok: false, message: 'Notifications were not allowed.' };
  const { publicKey } = await api.pushConfig();
  const applicationServerKey = fromBase64Url(publicKey);
  const { pushManager } = await navigator.serviceWorker.ready;
  let subscription = await pushManager.getSubscription();
  if (subscription !== null && !sameBytes(subscription.options.applicationServerKey, applicationServerKey)) {
    // The host has a new key: a subscription under the old one can no longer be sent to.
    await subscription.unsubscribe();
    subscription = null;
  }
  subscription ??= await pushManager.subscribe({ userVisibleOnly: true, applicationServerKey });
  await api.pushSubscribe(subscribeBody(subscription));
  dismissPushCard();
  return { ok: true };
}

/** Removes this phone's subscription from the host (best effort) and from the browser. */
export async function disablePush(): Promise<void> {
  const subscription = await currentSubscription();
  if (subscription === null) return;
  try {
    await api.pushUnsubscribe({ endpoint: subscription.endpoint });
  } catch {
    // Still unsubscribe below; the host drops endpoints the push service rejects.
  }
  await subscription.unsubscribe();
}
