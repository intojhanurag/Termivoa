import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  disablePush, dismissPushCard, enablePush, fromBase64Url, peerScope, PUSH_BLOCKED, PUSH_CARD_KEY, pushEnabled, pushSupport,
  removeStrayWorkers, shouldOfferPush, syncPeerPush, testPush, toBase64Url, workersSafe,
} from './push';
import { COMPUTERS_KEY, refreshComputers } from './computers';

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

const HOME = 'https://mac.tailx.ts.net';
const VPS = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps' };
const PI = { origin: 'https://pi.tailx.ts.net', host: 'pi.tailx.ts.net', label: 'pi' };

/** A peer registration at a scope path, with its own push manager. */
function fakeRegistration(path: string, subscription: FakeSubscription | null = null) {
  let current = subscription;
  return {
    scope: `${HOME}${path}`,
    active: { scriptURL: `${HOME}/sw.js` },
    installing: null,
    waiting: null,
    pushManager: {
      getSubscription: vi.fn(async () => current),
      subscribe: vi.fn(async ({ applicationServerKey }: { applicationServerKey: Uint8Array }) => {
        current = fakeSubscription(`https://push.example${path}new`, applicationServerKey);
        return current;
      }),
    },
    unregister: vi.fn(async () => true),
  };
}

/**
 * A browser with push: fake Notification, service worker and push manager.
 * `peers` is the stored computer list; `scoped` are the peer registrations
 * that already exist (register() adds more).
 */
function browser({
  agent = ANDROID, standalone = false, permission = 'granted', subscription = null as FakeSubscription | null,
  peers = [] as Array<typeof VPS>, scoped = [] as Array<ReturnType<typeof fakeRegistration>>,
} = {}) {
  const store = memoryStorage();
  store.setItem(COMPUTERS_KEY, JSON.stringify(peers.map(({ origin, label }) => ({ origin, label }))));
  // Home's subscription: kept after subscribe, gone after its unsubscribe.
  let current = subscription;
  const pushManager = {
    getSubscription: vi.fn(async () => (current !== null && current.unsubscribe.mock.calls.length === 0 ? current : null)),
    subscribe: vi.fn(async () => (current = fakeSubscription('https://push.example/new'))),
  };
  const registration = { scope: `${HOME}/`, pushManager };
  const registrations = [...scoped];
  const register = vi.fn(async (_script: string, { scope }: { scope: string }) => {
    let found = registrations.find((item) => item.scope === `${HOME}${scope}`);
    if (found === undefined) registrations.push(found = fakeRegistration(scope));
    return found;
  });
  vi.stubGlobal('location', { origin: HOME });
  vi.stubGlobal('navigator', {
    userAgent: agent,
    maxTouchPoints: agent === IPAD ? 5 : 0,
    standalone,
    serviceWorker: {
      ready: Promise.resolve(registration),
      getRegistration: vi.fn(async () => registration),
      getRegistrations: vi.fn(async () => [registration, ...registrations.filter((item) => item.unregister.mock.calls.length === 0)]),
      register,
    },
  });
  vi.stubGlobal('matchMedia', () => ({ matches: false }));
  vi.stubGlobal('PushManager', function PushManager() {});
  vi.stubGlobal('Notification', { permission, requestPermission: vi.fn(async () => permission) });
  vi.stubGlobal('localStorage', store);
  return { pushManager, store, register, registrations };
}

// A second P-256 point: a peer's own VAPID key.
const PEER_KEY_BYTES = new Uint8Array(65).map((_, index) => (index === 0 ? 4 : 200 - index));
const PEER_KEY = toBase64Url(PEER_KEY_BYTES.buffer);

/** Home and peers that answer the push routes; `down` hosts fail, `revoked` hosts answer 401. */
function servers({
  down = [] as string[], revoked = [] as string[], sent = 1,
  /** Peers answer their key only after this settles (a slow or hostile peer). */
  hold = Promise.resolve() as Promise<unknown>,
  /** Home's computer list. */
  listed = [] as Array<typeof VPS>,
} = {}) {
  const fetch = vi.fn(async (url: string, _init?: RequestInit) => {
    const { host, pathname } = new URL(url, HOME);
    if (down.includes(host)) throw new TypeError('offline');
    if (revoked.includes(host)) return json(401, { error: { code: 'revoked', message: 'revoked' } });
    if (pathname === '/api/v1/computers') return json(200, { computers: listed.map(({ origin, label }) => ({ origin, label })) });
    if (pathname === '/api/v1/push' && host !== 'mac.tailx.ts.net') await hold;
    if (pathname === '/api/v1/push') return json(200, { publicKey: host === 'mac.tailx.ts.net' ? PUBLIC_KEY : PEER_KEY });
    if (pathname === '/api/v1/push/test') return json(200, { sent });
    return json(204);
  });
  vi.stubGlobal('fetch', fetch);
  return { fetch, calls: () => fetch.mock.calls.map(([url, init]) => `${init?.method ?? 'GET'} ${url}`) };
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

/** enablePush with the peers' late answer awaited. */
async function enabled() {
  const result = await enablePush();
  return result.ok ? { ok: true, missing: await result.missing } : result;
}

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

    await expect(enabled()).resolves.toEqual({ ok: true, missing: [] });

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

describe('push from peers', () => {
  const on = () => fakeSubscription('https://push.example/home');

  it('subscribes each peer on its own scope with that peer\'s key, and sends it to that peer with its cookie', async () => {
    const { register, registrations } = browser({ peers: [VPS, PI] });
    const { fetch, calls } = servers();
    await expect(enabled()).resolves.toEqual({ ok: true, missing: [] });
    expect(register.mock.calls.map(([script, options]) => [script, options.scope]).sort()).toEqual([
      ['/sw.js', '/push/pi.tailx.ts.net/'], ['/sw.js', '/push/vps.tailx.ts.net/'],
    ]);
    for (const registration of registrations) {
      expect(registration.pushManager.subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: PEER_KEY_BYTES });
    }
    const [, init] = fetch.mock.calls.find(([url]) => url === 'https://vps.tailx.ts.net/api/v1/push/subscribe') ?? [];
    expect(init).toMatchObject({ method: 'POST', credentials: 'include', mode: 'cors' });
    expect(JSON.parse(init?.body as string).endpoint).toBe('https://push.example/push/vps.tailx.ts.net/new');
    expect(calls()).toContain('POST https://pi.tailx.ts.net/api/v1/push/subscribe');
    // Home's registration is asked for by scope /, never by the page's address.
    for (const [scope] of vi.mocked(navigator.serviceWorker.getRegistration).mock.calls) expect(scope).toBe('/');
  });

  it('keeps home and the other peers when one peer fails, and names it', async () => {
    browser({ peers: [VPS, PI] });
    const { calls } = servers({ down: ['vps.tailx.ts.net'] });
    await expect(enabled()).resolves.toEqual({ ok: true, missing: ['vps'] });
    expect(calls()).toContain('POST /api/v1/push/subscribe');
    expect(calls()).toContain('POST https://pi.tailx.ts.net/api/v1/push/subscribe');
  });

  it('on a later load subscribes a new peer without a prompt, and drops the registration of a removed one', async () => {
    const gone = fakeRegistration(peerScope('old.tailx.ts.net'), fakeSubscription('https://push.example/old'));
    const { register } = browser({ subscription: on(), peers: [VPS], scoped: [gone] });
    servers();
    await expect(syncPeerPush()).resolves.toEqual([]);
    expect(Notification.requestPermission).not.toHaveBeenCalled();
    expect(register).toHaveBeenCalledWith('/sw.js', { scope: '/push/vps.tailx.ts.net/' });
    expect((await gone.pushManager.getSubscription())?.unsubscribe).toHaveBeenCalledOnce();
    expect(gone.unregister).toHaveBeenCalledOnce();
  });

  it('off, a removal or a log out during a slow peer wins: the held sync makes no registration', async () => {
    const actions: Array<(store: ReturnType<typeof memoryStorage>) => Promise<void>> = [
      () => disablePush(),
      async (store) => { store.setItem(COMPUTERS_KEY, '[]'); },
    ];
    for (const action of actions) {
      const { register, store } = browser({ subscription: on(), peers: [VPS] });
      let release: () => void = () => undefined;
      const { calls } = servers({ hold: new Promise<void>((resolve) => { release = resolve; }) });
      const sync = syncPeerPush();
      await vi.waitFor(() => expect(calls()).toContain('GET https://vps.tailx.ts.net/api/v1/push'));
      await action(store);
      release();
      await expect(sync).resolves.toEqual(['vps']);
      expect(register).not.toHaveBeenCalled();
      expect(calls()).not.toContain('POST https://vps.tailx.ts.net/api/v1/push/subscribe');
    }
  });

  it('off while the peer registration is made: the registration is dropped again', async () => {
    const { register, registrations, store } = browser({ subscription: on(), peers: [VPS] });
    servers();
    // The list changes after the first check, while register() runs.
    register.mockImplementationOnce(async (_script, { scope }) => {
      store.setItem(COMPUTERS_KEY, '[]');
      const made = fakeRegistration(scope);
      registrations.push(made);
      return made;
    });
    await syncPeerPush();
    expect(registrations[0]?.unregister).toHaveBeenCalledOnce();
  });

  it('with push on, drops a stored peer that home\'s list no longer has (a host a compromised home wrote)', async () => {
    const EVIL = { origin: 'https://evil.tailx.ts.net', host: 'evil.tailx.ts.net', label: 'evil' };
    const planted = fakeRegistration(peerScope(EVIL.host), fakeSubscription('https://push.example/evil', new Uint8Array(65).fill(7)));
    browser({ subscription: on(), peers: [VPS, EVIL], scoped: [planted] });
    const { calls } = servers({ listed: [VPS] });
    // Until home answers, the load-start cleanup keeps it: its host is in the stored list.
    const container = navigator.serviceWorker;
    await expect(removeStrayWorkers(container)).resolves.toBe(0);
    // Home's list replaces the stored one (refreshComputers), then the sync runs.
    const list = await refreshComputers();
    await syncPeerPush(list.filter((computer) => !computer.home));
    expect(planted.unregister).toHaveBeenCalledOnce();
    expect((await planted.pushManager.getSubscription())?.unsubscribe).toHaveBeenCalledOnce();
    expect(calls().some((call) => call.includes('evil.tailx.ts.net'))).toBe(false);
    await expect(removeStrayWorkers(container)).resolves.toBe(0);
  });

  it('drops every peer registration and asks no peer while push is off', async () => {
    const left = fakeRegistration(peerScope(VPS.host), fakeSubscription('https://push.example/vps'));
    browser({ peers: [VPS], scoped: [left] });
    const { fetch } = servers();
    await expect(syncPeerPush()).resolves.toEqual([]);
    expect(left.unregister).toHaveBeenCalledOnce();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('drops the registration of a peer that says pair again, and makes none for it', async () => {
    const stale = fakeRegistration(peerScope(VPS.host), fakeSubscription('https://push.example/vps'));
    const { register } = browser({ subscription: on(), peers: [VPS], scoped: [stale] });
    servers({ revoked: ['vps.tailx.ts.net'] });
    await expect(syncPeerPush()).resolves.toEqual(['vps']);
    expect(stale.unregister).toHaveBeenCalledOnce();
    expect(register).not.toHaveBeenCalled();
  });

  it('turning off unsubscribes on each peer and unregisters its registration, also when the peer is down', async () => {
    const vps = fakeRegistration(peerScope(VPS.host), fakeSubscription('https://push.example/vps'));
    const pi = fakeRegistration(peerScope(PI.host), fakeSubscription('https://push.example/pi'));
    const home = on();
    browser({ subscription: home, peers: [VPS, PI], scoped: [vps, pi] });
    const { fetch, calls } = servers({ down: ['pi.tailx.ts.net'] });
    await disablePush();
    expect(calls().sort()).toEqual([
      'POST /api/v1/push/unsubscribe', 'POST https://pi.tailx.ts.net/api/v1/push/unsubscribe', 'POST https://vps.tailx.ts.net/api/v1/push/unsubscribe',
    ]);
    const [, init] = fetch.mock.calls.find(([url]) => url.startsWith(VPS.origin)) ?? [];
    expect(JSON.parse(init?.body as string)).toEqual({ endpoint: 'https://push.example/vps' });
    for (const registration of [vps, pi]) expect(registration.unregister).toHaveBeenCalledOnce();
    expect(home.unsubscribe).toHaveBeenCalledOnce();
  });

  it('tests every computer and names the peers that sent nothing', async () => {
    browser({ peers: [VPS, PI] });
    servers({ down: ['pi.tailx.ts.net'] });
    const first = await testPush();
    expect(first.sent).toBe(true);
    await expect(first.missing).resolves.toEqual(['pi']);
    servers({ sent: 0 });
    const second = await testPush();
    expect(second.sent).toBe(false);
    await expect(second.missing).resolves.toEqual(['vps', 'pi']);
  });

  it('does not wait for a slow peer: on, test and off answer when home has answered', async () => {
    const vps = fakeRegistration(peerScope(VPS.host), fakeSubscription('https://push.example/vps'));
    browser({ peers: [VPS], scoped: [vps] });
    // The peer never answers anything.
    const fetch = vi.fn(async (url: string, _init?: RequestInit) => {
      if (url.startsWith(VPS.origin)) return new Promise<Response>(() => undefined);
      return url === '/api/v1/push' ? json(200, { publicKey: PUBLIC_KEY }) : url === '/api/v1/push/test' ? json(200, { sent: 1 }) : json(204);
    });
    vi.stubGlobal('fetch', fetch);
    const result = await enablePush();
    expect(result.ok).toBe(true);
    expect((await testPush()).sent).toBe(true);
    await disablePush();
    expect(vps.unregister).toHaveBeenCalledOnce();
    expect(fetch.mock.calls.map(([url]) => url)).toContain('https://vps.tailx.ts.net/api/v1/push/unsubscribe');
  });

  it('unregisters a peer registration also when its unsubscribe fails', async () => {
    const broken = fakeSubscription('https://push.example/vps');
    broken.unsubscribe.mockRejectedValue(new Error('push service down'));
    const vps = fakeRegistration(peerScope(VPS.host), broken);
    browser({ subscription: fakeSubscription('https://push.example/home'), peers: [VPS], scoped: [vps] });
    servers();
    await disablePush();
    expect(vps.unregister).toHaveBeenCalledOnce();
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

describe('removeStrayWorkers', () => {
  const ORIGIN = 'https://host.example';
  const registration = (scope: string, script: string | null, state: 'active' | 'waiting' | 'installing' = 'active') => ({
    scope,
    active: null,
    waiting: null,
    installing: null,
    ...(script === null ? {} : { [state]: { scriptURL: script } }),
    unregister: vi.fn(async () => true),
  });
  const container = (registrations: unknown[]) => ({ getRegistrations: vi.fn(async () => registrations) }) as unknown as ServiceWorkerContainer;

  it('keeps only the app\'s own /sw.js at scope /, and removes every other worker', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    const own = registration(`${ORIGIN}/`, `${ORIGIN}/sw.js`);
    const ownWaiting = registration(`${ORIGIN}/`, `${ORIGIN}/sw.js`, 'waiting');
    const strays = [
      registration(`${ORIGIN}/`, `${ORIGIN}/evil.js`),
      registration(`${ORIGIN}/peer/`, `${ORIGIN}/sw.js`),
      registration(`${ORIGIN}/`, 'https://evil.example/sw.js', 'installing'),
      registration(`${ORIGIN}/`, `${ORIGIN}/sw.js?v=2`),
      registration(`${ORIGIN}/x/`, null),
    ];
    await expect(removeStrayWorkers(container([own, ownWaiting, ...strays]))).resolves.toBe(strays.length);
    expect(own.unregister).not.toHaveBeenCalled();
    expect(ownWaiting.unregister).not.toHaveBeenCalled();
    for (const stray of strays) expect(stray.unregister).toHaveBeenCalledOnce();
  });

  it('keeps /sw.js at the exact push scope of a listed peer, and removes every other push scope', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    const listed = registration(`${ORIGIN}/push/vps.tailx.ts.net/`, `${ORIGIN}/sw.js`);
    const fresh = registration(`${ORIGIN}/push/pi.tailx.ts.net/`, null);
    const strays = [
      registration(`${ORIGIN}/push/evil.tailx.ts.net/`, `${ORIGIN}/sw.js`),
      registration(`${ORIGIN}/push/vps.tailx.ts.net/`, `${ORIGIN}/evil.js`),
      registration(`${ORIGIN}/push/vps.tailx.ts.net/x/`, `${ORIGIN}/sw.js`),
      registration(`${ORIGIN}/push/vps.tailx.ts.net`, `${ORIGIN}/sw.js`),
      registration(`${ORIGIN}/push/VPS.tailx.ts.net/`, `${ORIGIN}/sw.js`),
      registration(`${ORIGIN}/push/`, `${ORIGIN}/sw.js`),
      registration(`${ORIGIN}/c/vps.tailx.ts.net/`, `${ORIGIN}/sw.js`),
      registration('https://vps.tailx.ts.net/push/vps.tailx.ts.net/', `${ORIGIN}/sw.js`),
    ];
    await expect(removeStrayWorkers(container([listed, fresh, ...strays]), ['vps.tailx.ts.net', 'pi.tailx.ts.net'])).resolves.toBe(strays.length);
    expect(listed.unregister).not.toHaveBeenCalled();
    expect(fresh.unregister).not.toHaveBeenCalled();
    // With no peers in the list, a push scope is a stray like any other.
    await expect(removeStrayWorkers(container([listed]), [])).resolves.toBe(1);
  });

  it('reads the allowed peers from this phone\'s stored list', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    const store = memoryStorage();
    store.setItem(COMPUTERS_KEY, JSON.stringify([{ origin: 'https://vps.tailx.ts.net', label: 'vps' }]));
    vi.stubGlobal('localStorage', store);
    const listed = registration(`${ORIGIN}/push/vps.tailx.ts.net/`, `${ORIGIN}/sw.js`);
    const other = registration(`${ORIGIN}/push/pi.tailx.ts.net/`, `${ORIGIN}/sw.js`);
    await expect(removeStrayWorkers(container([listed, other]))).resolves.toBe(1);
    expect(other.unregister).toHaveBeenCalledOnce();
    await expect(workersSafe(container([listed]))).resolves.toBe(true);
  });

  it('removes a registration when ANY of its workers is not /sw.js', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    const mixed = { ...registration(`${ORIGIN}/`, `${ORIGIN}/sw.js`), waiting: { scriptURL: `${ORIGIN}/evil.js` } };
    const installing = { ...registration(`${ORIGIN}/`, `${ORIGIN}/sw.js`, 'waiting'), installing: { scriptURL: 'https://evil.example/sw.js' } };
    await expect(removeStrayWorkers(container([mixed, installing]))).resolves.toBe(2);
  });

  it('keeps a fresh registration of its own scope that has no worker yet (first install)', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    const fresh = registration(`${ORIGIN}/`, null);
    await expect(removeStrayWorkers(container([fresh]))).resolves.toBe(0);
    expect(fresh.unregister).not.toHaveBeenCalled();
  });

  it('never rejects: no support, a failed list, or a failed unregister', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    await expect(removeStrayWorkers(undefined)).resolves.toBe(0);
    const broken = { getRegistrations: vi.fn(async () => { throw new Error('denied'); }) } as unknown as ServiceWorkerContainer;
    await expect(removeStrayWorkers(broken)).resolves.toBe(0);
    const stuck = { ...registration(`${ORIGIN}/x/`, `${ORIGIN}/sw.js`), unregister: vi.fn(async () => { throw new Error('busy'); }) };
    const stray = registration(`${ORIGIN}/`, `${ORIGIN}/other.js`);
    await expect(removeStrayWorkers(container([stuck, stray]))).resolves.toBe(1);
    vi.stubGlobal('navigator', {});
    await expect(removeStrayWorkers()).resolves.toBe(0);
  });
});

describe('workersSafe', () => {
  const ORIGIN = 'https://host.example';
  const own = { scope: `${ORIGIN}/`, active: { scriptURL: `${ORIGIN}/sw.js` }, waiting: null, installing: null, unregister: vi.fn(async () => true) };
  const stray = { scope: `${ORIGIN}/`, active: { scriptURL: `${ORIGIN}/evil.js` }, waiting: null, installing: null, unregister: vi.fn(async () => true) };
  const container = (registrations: unknown[], controller: string | null) => ({
    getRegistrations: vi.fn(async () => registrations),
    controller: controller === null ? null : { scriptURL: controller },
  }) as unknown as ServiceWorkerContainer;

  it('refuses a load still controlled by a stray worker, so no grant is redeemed on it', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    // unregister() ran, but the stray still controls this page until the next load.
    await expect(workersSafe(container([stray], `${ORIGIN}/evil.js`))).resolves.toBe(false);
    // Already unregistered elsewhere, yet still in control.
    await expect(workersSafe(container([own], `${ORIGIN}/evil.js`))).resolves.toBe(false);
    // Removed a stray that did not control the page: still not this load.
    await expect(workersSafe(container([own, stray], `${ORIGIN}/sw.js`))).resolves.toBe(false);
  });

  it('allows the app\'s own controller, or none (the first visit, or no support)', async () => {
    vi.stubGlobal('location', { origin: ORIGIN });
    await expect(workersSafe(container([own], `${ORIGIN}/sw.js`))).resolves.toBe(true);
    await expect(workersSafe(container([], null))).resolves.toBe(true);
    await expect(workersSafe(undefined)).resolves.toBe(true);
  });
});
