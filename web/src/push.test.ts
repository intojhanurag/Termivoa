import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  disablePush, dismissPushCard, enablePush, fromBase64Url, PUSH_BLOCKED, PUSH_CARD_KEY, pushEnabled, pushSupport,
  shouldOfferPush, toBase64Url,
} from './push';

const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1';
const IPAD = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15';
const ANDROID = 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/130.0 Mobile Safari/537.36';

// A 65-byte uncompressed P-256 point, as the server sends it.
const KEY_BYTES = new Uint8Array(65).map((_, index) => (index === 0 ? 4 : index));
const PUBLIC_KEY = toBase64Url(KEY_BYTES.buffer);

function memoryStorage() {
  const values = new Map<string, string>();
  return { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value); } };
}

/** A browser with push: fake Notification, service worker and push manager. */
function browser({ agent = ANDROID, standalone = false, permission = 'granted', subscription = null as FakeSubscription | null } = {}) {
  const store = memoryStorage();
  const pushManager = {
    getSubscription: vi.fn(async () => subscription),
    subscribe: vi.fn(async () => fakeSubscription('https://push.example/new')),
  };
  const registration = { pushManager };
  vi.stubGlobal('navigator', {
    userAgent: agent,
    maxTouchPoints: agent === IPAD ? 5 : 0,
    standalone,
    serviceWorker: { ready: Promise.resolve(registration), getRegistration: vi.fn(async () => registration) },
  });
  vi.stubGlobal('matchMedia', () => ({ matches: false }));
  vi.stubGlobal('PushManager', function PushManager() {});
  vi.stubGlobal('Notification', { permission, requestPermission: vi.fn(async () => permission) });
  vi.stubGlobal('localStorage', store);
  return { pushManager, store };
}

type FakeSubscription = ReturnType<typeof fakeSubscription>;
function fakeSubscription(endpoint: string, key: Uint8Array = KEY_BYTES) {
  return {
    endpoint,
    expirationTime: null,
    options: { applicationServerKey: key.slice().buffer },
    getKey: (name: string) => (name === 'p256dh' ? KEY_BYTES.buffer : new Uint8Array(16).fill(0xfb).buffer),
    unsubscribe: vi.fn(async () => true),
  };
}

function json(status: number, body?: unknown) {
  return body === undefined ? new Response(null, { status }) : new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());

describe('pushSupport', () => {
  it('needs the Home Screen app on iPhone and iPad, and is available there once installed', () => {
    browser({ agent: IPHONE });
    expect(pushSupport()).toBe('needs-home-screen');
    browser({ agent: IPAD });
    expect(pushSupport()).toBe('needs-home-screen');
    browser({ agent: IPHONE, standalone: true });
    expect(pushSupport()).toBe('available');
    browser({ agent: IPAD });
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: query === '(display-mode: standalone)' }));
    expect(pushSupport()).toBe('available');
  });

  it('is available in a browser tab elsewhere, and unsupported without the push APIs', () => {
    browser();
    expect(pushSupport()).toBe('available');
    vi.stubGlobal('PushManager', undefined);
    Reflect.deleteProperty(globalThis, 'PushManager');
    expect(pushSupport()).toBe('unsupported');
  });
});

describe('base64url', () => {
  it('round-trips unpadded base64url with - and _', () => {
    expect(PUBLIC_KEY).toMatch(/^B[A-Za-z0-9_-]{86}$/u);
    expect(fromBase64Url(PUBLIC_KEY)).toEqual(KEY_BYTES);
    expect(toBase64Url(new Uint8Array([0xfb, 0xff]).buffer)).toBe('-_8');
  });
});

describe('enablePush', () => {
  it('stops at a denied permission without calling the server', async () => {
    browser({ permission: 'denied' });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(enablePush()).resolves.toEqual({ ok: false, message: PUSH_BLOCKED });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('asks permission before any network call, subscribes with the host key and sends only endpoint and keys', async () => {
    const { pushManager, store } = browser();
    const order: string[] = [];
    vi.mocked(Notification.requestPermission).mockImplementation(async () => { order.push('permission'); return 'granted'; });
    const fetch = vi.fn(async (path: string, _init?: RequestInit) => {
      order.push(path);
      return path === '/api/v1/push' ? json(200, { publicKey: PUBLIC_KEY }) : json(204);
    });
    vi.stubGlobal('fetch', fetch);

    await expect(enablePush()).resolves.toEqual({ ok: true });

    expect(order).toEqual(['permission', '/api/v1/push', '/api/v1/push/subscribe']);
    expect(pushManager.subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: KEY_BYTES });
    const init = fetch.mock.calls[1]?.[1] as unknown as RequestInit;
    expect(JSON.parse(init.body as string)).toEqual({
      endpoint: 'https://push.example/new',
      keys: { p256dh: PUBLIC_KEY, auth: '-_v7'.repeat(5) + '-w' },
    });
    expect(store.getItem(PUSH_CARD_KEY)).toBe('1');
  });

  it('replaces a subscription made under an old host key, and reuses one under the current key', async () => {
    const stale = fakeSubscription('https://push.example/old', new Uint8Array(65).fill(9));
    const first = browser({ subscription: stale });
    vi.stubGlobal('fetch', vi.fn(async (path: string) => (path === '/api/v1/push' ? json(200, { publicKey: PUBLIC_KEY }) : json(204))));
    await enablePush();
    expect(stale.unsubscribe).toHaveBeenCalledOnce();
    expect(first.pushManager.subscribe).toHaveBeenCalledOnce();

    const current = fakeSubscription('https://push.example/current');
    const second = browser({ subscription: current });
    const fetch = vi.fn(async (path: string, _init?: RequestInit) => (path === '/api/v1/push' ? json(200, { publicKey: PUBLIC_KEY }) : json(204)));
    vi.stubGlobal('fetch', fetch);
    await enablePush();
    expect(current.unsubscribe).not.toHaveBeenCalled();
    expect(second.pushManager.subscribe).not.toHaveBeenCalled();
    expect(JSON.parse((fetch.mock.calls[1]?.[1] as unknown as RequestInit).body as string).endpoint).toBe('https://push.example/current');
  });
});

describe('disablePush', () => {
  it('unsubscribes in the browser even when the host cannot be reached', async () => {
    const subscription = fakeSubscription('https://push.example/one');
    browser({ subscription });
    const fetch = vi.fn(async () => { throw new TypeError('offline'); });
    vi.stubGlobal('fetch', fetch);
    await disablePush();
    const init = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(init[0]).toBe('/api/v1/push/unsubscribe');
    expect(JSON.parse(init[1].body as string)).toEqual({ endpoint: 'https://push.example/one' });
    expect(subscription.unsubscribe).toHaveBeenCalledOnce();
  });
});

describe('shouldOfferPush', () => {
  it('offers the card only when push is available, not dismissed and not already on', async () => {
    browser({ permission: 'default' });
    await expect(shouldOfferPush()).resolves.toBe(true);
    dismissPushCard();
    await expect(shouldOfferPush()).resolves.toBe(false);

    browser({ subscription: fakeSubscription('https://push.example/on') });
    await expect(pushEnabled()).resolves.toBe(true);
    await expect(shouldOfferPush()).resolves.toBe(false);

    browser({ agent: IPHONE });
    await expect(shouldOfferPush()).resolves.toBe(false);
  });
});
