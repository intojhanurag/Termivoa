import { readFileSync } from 'node:fs';
import { afterEach, describe, expect, it, vi } from 'vitest';

type Handler = (event: Record<string, unknown>) => void;
type Network = (input: unknown, init?: RequestInit) => Promise<Response>;

const BUILD = '0123456789abcdef';
const CACHE = `pterm-bundle-${BUILD}`;
const FILES = ['/assets/Wall-1.js', '/assets/index-1.css', '/assets/index-1.js', '/icon-192.png', '/index.html', '/manifest.webmanifest'];
const SOURCE = readFileSync(new URL('./sw.js', import.meta.url), 'utf8')
  .replaceAll('__BUILD__', BUILD)
  .replaceAll('__BUNDLE__', JSON.stringify({ build: BUILD, files: FILES }));

const shell = (build: string, body = 'app') =>
  new Response(`<!doctype html><html><head>\n    <meta name="pterm-build" content="${build}" />\n</head><body>${body}</body></html>`, {
    headers: { 'Content-Type': 'text/html; charset=utf-8' },
  });

/** A Cache Storage kept in maps: cache name -> key -> response. */
function cacheStorage(initial: Record<string, Record<string, Response>> = {}) {
  const store = new Map<string, Map<string, Response>>(Object.entries(initial).map(([name, entries]) => [name, new Map(Object.entries(entries))]));
  const put = vi.fn(async (_key: string, _response: Response) => undefined);
  const caches = {
    open: vi.fn(async (name: string) => {
      const entries = store.get(name) ?? new Map<string, Response>();
      store.set(name, entries);
      return {
        match: async (key: string) => entries.get(key)?.clone(),
        put: async (key: string, response: Response) => { await put(key, response); entries.set(key, response); },
      };
    }),
    keys: vi.fn(async () => [...store.keys()]),
    delete: vi.fn(async (name: string) => store.delete(name)),
  };
  return { store, caches, put };
}

/** Runs src/sw.js against a fake worker global and returns its handlers. */
function loadWorker(network: Network, windows: Array<Record<string, unknown>> = [], storage = cacheStorage(), scope = 'https://host.example/') {
  const handlers = new Map<string, Handler>();
  const self = {
    location: { origin: 'https://host.example' },
    addEventListener: (type: string, handler: Handler) => handlers.set(type, handler),
    skipWaiting: vi.fn(),
    clients: {
      claim: vi.fn(async () => undefined),
      matchAll: vi.fn(async () => windows),
      openWindow: vi.fn(async () => null),
    },
    registration: { scope, showNotification: vi.fn(async () => undefined) },
  };
  const fetch = vi.fn(network);
  new Function('self', 'caches', 'fetch', SOURCE)(self, storage.caches, fetch);
  return { handlers, self, fetch, ...storage };
}

/** The worker's pure decision functions. */
const pure = new Function('self', 'caches', 'fetch', `${SOURCE}\nreturn { buildOf, shouldCacheShell, BUNDLE_FILES, withTimeout };`)(
  { location: { origin: 'https://host.example' }, registration: { scope: 'https://host.example/' }, addEventListener: () => undefined }, {}, () => undefined,
) as {
  buildOf: (html: string) => string | undefined;
  shouldCacheShell: (status: number, contentType: string | null, html: string, build: string) => boolean;
  BUNDLE_FILES: ReadonlySet<string>;
  withTimeout: <T>(promise: Promise<T>, ms: number) => Promise<T>;
};

/** A server that answers every bundle file, with overrides per path. */
function server(overrides: Record<string, () => Promise<Response>> = {}): Network {
  return async (input) => {
    const path = String(input);
    const override = overrides[path];
    if (override) return override();
    return path === '/index.html' ? shell(BUILD) : new Response(`file ${path}`);
  };
}

/** Starts a navigation and returns the page reply and the background work. */
function navigate(handlers: Map<string, Handler>, path = '/sessions') {
  let reply: Promise<Response> | undefined;
  let work: Promise<unknown> | undefined;
  handlers.get('fetch')?.({
    // Request cannot be built with mode "navigate", so pass the fields the worker reads.
    request: { method: 'GET', url: `https://host.example${path}`, mode: 'navigate' },
    respondWith: (value: Promise<Response>) => { reply = value; },
    waitUntil: (value: Promise<unknown>) => { work = value; },
  });
  return { reply: reply as Promise<Response>, work: () => work };
}

afterEach(() => { vi.useRealTimers(); });

describe('service worker', () => {
  it('never activates a new version on install; only an explicit reload message does', async () => {
    const { handlers, self } = loadWorker(server());
    await fire(handlers, 'install', {});
    expect(self.skipWaiting).not.toHaveBeenCalled();
    handlers.get('message')?.({ data: { type: 'skip-waiting' } });
    expect(self.skipWaiting).toHaveBeenCalledOnce();
  });

  it('precaches exactly the bundle files, past the HTTP cache', async () => {
    const { handlers, fetch, store } = loadWorker(server());
    await fire(handlers, 'install', {});
    expect([...(store.get(CACHE)?.keys() ?? [])].sort()).toEqual(FILES);
    expect(fetch).toHaveBeenCalledTimes(FILES.length);
    for (const [, init] of fetch.mock.calls) expect(init).toEqual({ cache: 'reload' });
  });

  it('fails install and keeps no new cache when a file fails, is missing, or the HTML is of another build', async () => {
    const failures: Array<Record<string, () => Promise<Response>>> = [
      { '/assets/Wall-1.js': async () => { throw new TypeError('offline'); } },
      { '/assets/index-1.css': async () => new Response('not found', { status: 404 }) },
      { '/index.html': async () => shell('fedcba9876543210') },
    ];
    for (const overrides of failures) {
      const old = new Response('old');
      const { handlers, self, store } = loadWorker(server(overrides), [], cacheStorage({ 'pterm-bundle-old': { '/index.html': old } }));
      await expect(fire(handlers, 'install', {})).rejects.toBeDefined();
      expect([...store.keys()]).toEqual(['pterm-bundle-old']);
      expect(self.skipWaiting).not.toHaveBeenCalled();
    }
  });

  it('on activate deletes the app\'s older caches only, then takes control', async () => {
    const storage = cacheStorage({ 'pterm-bundle-bbb': {}, [CACHE]: {}, 'someone-else': {} });
    const { handlers, self, store } = loadWorker(server(), [], storage);
    await fire(handlers, 'activate', {});
    expect([...store.keys()].sort()).toEqual([CACHE, 'someone-else']);
    expect(self.clients.claim).toHaveBeenCalledOnce();
  });

  it('does not cache a missing chunk, so a reload can fetch the new build', async () => {
    const { handlers, put } = loadWorker(async () => new Response('not found', { status: 404 }));
    let reply: Promise<Response> | undefined;
    handlers.get('fetch')?.({
      request: new Request('https://host.example/assets/Wall-1.js'),
      respondWith: (value: Promise<Response>) => { reply = value; },
    });
    expect((await reply)?.status).toBe(404);
    expect(put).not.toHaveBeenCalled();
  });

  it('serves bundle files from the cache, and leaves other files and the API to the network', async () => {
    const storage = cacheStorage({ [CACHE]: { '/assets/Wall-1.js': new Response('cached wall') } });
    const { handlers, fetch } = loadWorker(server(), [], storage);
    const replies: Array<Promise<Response>> = [];
    for (const path of ['/assets/Wall-1.js', '/assets/Other-9.js', '/api/v1/sessions']) {
      handlers.get('fetch')?.({
        request: new Request(`https://host.example${path}`),
        respondWith: (value: Promise<Response>) => { replies.push(value); },
      });
    }
    expect(replies).toHaveLength(1);
    expect(await (await replies[0])?.text()).toBe('cached wall');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('answers a failed page load with an offline page that retries by itself', async () => {
    const { handlers } = loadWorker(async () => { throw new TypeError('offline'); });
    const response = await navigate(handlers).reply;
    expect(response?.status).toBe(503);
    const html = await response?.text();
    expect(html).toContain('<meta http-equiv="refresh" content="5">');
    expect(html).toContain('retrying every 5s');
    expect(html).toContain('<a href="/">Retry now</a>');
  });
});

describe('service worker navigation', () => {
  const withShell = () => cacheStorage({ [CACHE]: { '/index.html': shell(BUILD, 'cached') } });

  it('serves the network page and re-caches it as the shell when its build matches', async () => {
    const { handlers, store } = loadWorker(async () => shell(BUILD, 'fresh'), [], withShell());
    const page = navigate(handlers);
    expect(await (await page.reply).text()).toContain('fresh');
    await page.work();
    expect(await store.get(CACHE)?.get('/index.html')?.clone().text()).toContain('fresh');
  });

  it('serves but never caches a page of another build, an error page, or a non-HTML answer', async () => {
    for (const answer of [() => shell('fedcba9876543210', 'newer'), () => new Response('gone', { status: 404, headers: { 'Content-Type': 'text/html' } }), () => new Response('{}')]) {
      const { handlers, put } = loadWorker(async () => answer(), [], withShell());
      const page = navigate(handlers);
      expect((await page.reply).status).toBe(answer().status);
      await page.work();
      expect(put).not.toHaveBeenCalled();
    }
  });

  it('serves the cached shell on a network error or a 5xx', async () => {
    for (const network of [async () => { throw new TypeError('offline'); }, async () => new Response('bad gateway', { status: 502 })]) {
      const { handlers } = loadWorker(network, [], withShell());
      const response = await navigate(handlers).reply;
      expect(response.status).toBe(200);
      expect(await response.text()).toContain('cached');
    }
  });

  it('waits for the network past 2.5 s on a fresh reload (the app\'s CSP was out of date), and falls back to the shell after 12 s or on an error', async () => {
    vi.useFakeTimers();
    let answer: (response: Response) => void = () => undefined;
    const { handlers } = loadWorker(() => new Promise((resolve) => { answer = resolve; }), [], withShell());
    const page = navigate(handlers, '/sessions?pterm-fresh=1');
    let served: Response | undefined;
    void page.reply.then((response) => { served = response; });
    await vi.advanceTimersByTimeAsync(10_000);
    expect(served).toBeUndefined();
    answer(shell(BUILD, 'fresh'));
    await vi.advanceTimersByTimeAsync(0);
    expect(await served?.text()).toContain('fresh');
    const hung = navigate(loadWorker(() => new Promise(() => undefined), [], withShell()).handlers, '/?pterm-fresh=1');
    let late: Response | undefined;
    void hung.reply.then((response) => { late = response; });
    await vi.advanceTimersByTimeAsync(11_900);
    expect(late).toBeUndefined();
    await vi.advanceTimersByTimeAsync(100);
    expect(await late?.text()).toContain('cached');
    vi.useRealTimers();
    const offline = loadWorker(async () => { throw new TypeError('offline'); }, [], withShell());
    expect(await (await navigate(offline.handlers, '/?pterm-fresh=1').reply).text()).toContain('cached');
  });

  it('serves the cached shell after 2.5 s, and a late answer of this build still refreshes it', async () => {
    vi.useFakeTimers();
    let answer: (response: Response) => void = () => undefined;
    const { handlers, store } = loadWorker(() => new Promise((resolve) => { answer = resolve; }), [], withShell());
    const page = navigate(handlers);
    let served: Response | undefined;
    void page.reply.then((response) => { served = response; });
    await vi.advanceTimersByTimeAsync(2400);
    expect(served).toBeUndefined();
    await vi.advanceTimersByTimeAsync(100);
    expect(await served?.text()).toContain('cached');
    answer(shell(BUILD, 'late'));
    await page.work();
    expect(await store.get(CACHE)?.get('/index.html')?.clone().text()).toContain('late');
  });
});

describe('service worker decisions', () => {
  it('reads the build ID from the HTML meta', () => {
    expect(pure.buildOf(`<head>\n    <meta name="pterm-build" content="${BUILD}" />`)).toBe(BUILD);
    expect(pure.buildOf('<head></head>')).toBeUndefined();
  });

  it('caches the shell only for an OK HTML page of the same build', async () => {
    const html = await shell(BUILD).text();
    expect(pure.shouldCacheShell(200, 'text/html; charset=utf-8', html, BUILD)).toBe(true);
    expect(pure.shouldCacheShell(200, 'text/html', html, 'fedcba9876543210')).toBe(false);
    expect(pure.shouldCacheShell(404, 'text/html', html, BUILD)).toBe(false);
    expect(pure.shouldCacheShell(200, 'application/json', html, BUILD)).toBe(false);
    expect(pure.shouldCacheShell(200, null, html, BUILD)).toBe(false);
  });

  it('knows the bundle files', () => {
    expect(pure.BUNDLE_FILES.has('/assets/Wall-1.js')).toBe(true);
    expect(pure.BUNDLE_FILES.has('/index.html')).toBe(true);
    expect(pure.BUNDLE_FILES.has('/assets/Wall-0.js')).toBe(false);
    expect(pure.BUNDLE_FILES.has('/')).toBe(false);
  });

  it('times out a slow promise and passes a fast one through', async () => {
    vi.useFakeTimers();
    const slow = pure.withTimeout(new Promise(() => undefined), 1000);
    const caught = slow.catch((error: Error) => error.message);
    await vi.advanceTimersByTimeAsync(1000);
    expect(await caught).toBe('timeout');
    await expect(pure.withTimeout(Promise.resolve(7), 1000)).resolves.toBe(7);
    await expect(pure.withTimeout(Promise.reject(new Error('down')), 1000)).rejects.toThrow('down');
  });
});

const SESSION = '84444444-4444-4444-8444-444444444444';

/** Fires an event whose waitUntil work must finish; resolves when it has. */
async function fire(handlers: Map<string, Handler>, type: string, event: Record<string, unknown>) {
  let work: Promise<unknown> | undefined;
  handlers.get(type)?.({ ...event, waitUntil: (value: Promise<unknown>) => { work = value; } });
  await work;
}

const pushEvent = (json: () => unknown) => ({ data: { json } });

describe('service worker push', () => {
  it('shows the session name and status, tagged per session, opening that session', async () => {
    const { handlers, self } = loadWorker(async () => new Response(''));
    await fire(handlers, 'push', pushEvent(() => ({ title: 'api', body: 'Waiting for you', sessionId: SESSION, kind: 'waiting' })));
    expect(self.registration.showNotification).toHaveBeenCalledWith('api', {
      body: 'Waiting for you',
      tag: `${SESSION}:waiting`,
      icon: '/icon-192.png',
      data: { url: `/sessions/${SESSION}` },
    });
  });

  it('still shows a notification for a payload that is not JSON or not an object', async () => {
    for (const json of [() => { throw new SyntaxError('bad'); }, () => null, () => ['x'], () => 'text']) {
      const { handlers, self } = loadWorker(async () => new Response(''));
      await fire(handlers, 'push', pushEvent(json));
      expect(self.registration.showNotification).toHaveBeenCalledWith('Termivoa', expect.objectContaining({ body: '', tag: 'pterm:test', data: { url: '/' } }));
    }
    const { handlers, self } = loadWorker(async () => new Response(''));
    await fire(handlers, 'push', {});
    expect(self.registration.showNotification).toHaveBeenCalledOnce();
  });

  it('uses only strings, clamps them, and opens / for a session id that is not a UUID', async () => {
    const { handlers, self } = loadWorker(async () => new Response(''));
    await fire(handlers, 'push', pushEvent(() => ({ title: 'x'.repeat(500), body: { html: '<b>' }, sessionId: '../settings', kind: 'evil' })));
    const [title, options] = self.registration.showNotification.mock.calls[0] as unknown as [string, { body: string; tag: string; data: { url: string } }];
    expect(title).toHaveLength(120);
    expect(options.body).toBe('');
    expect(options.tag).toBe('pterm:test');
    expect(options.data.url).toBe('/');
    await fire(handlers, 'push', pushEvent(() => ({ body: 'y'.repeat(500), sessionId: 'AB444444-4444-4444-8444-44444444444C', kind: 'done' })));
    const [, second] = self.registration.showNotification.mock.calls[1] as unknown as [string, { body: string; data: { url: string } }];
    expect(second.body).toHaveLength(160);
    expect(second.data.url).toBe('/');
  });

  const PEER_SCOPE = 'https://host.example/push/vps.tailx.ts.net/';

  it('from a peer scope, names the computer and opens /c/<host>/sessions/<id>; the host is the scope\'s, never the payload\'s', async () => {
    const { handlers, self } = loadWorker(async () => new Response(''), [], cacheStorage(), PEER_SCOPE);
    await fire(handlers, 'push', pushEvent(() => ({
      title: 'api', body: 'Waiting for you', sessionId: SESSION, kind: 'waiting', host: 'evil.example', url: 'https://evil.example/', computer: 'mac',
    })));
    expect(self.registration.showNotification).toHaveBeenCalledWith('vps · api', {
      body: 'vps · Waiting for you',
      tag: `/c/vps.tailx.ts.net${SESSION}:waiting`,
      icon: '/icon-192.png',
      data: { url: `/c/vps.tailx.ts.net/sessions/${SESSION}` },
    });
    await fire(handlers, 'push', pushEvent(() => ({ sessionId: '../../settings' })));
    expect(self.registration.showNotification).toHaveBeenLastCalledWith('vps · Termivoa', expect.objectContaining({ body: 'vps', data: { url: '/' } }));
    // A title that names another computer still starts with this one.
    await fire(handlers, 'push', pushEvent(() => ({ title: 'mac · api is waiting for you', body: 'mac · tap to answer' })));
    expect(self.registration.showNotification).toHaveBeenLastCalledWith('vps · mac · api is waiting for you', expect.objectContaining({ body: 'vps · mac · tap to answer' }));

    const tapped = loadWorker(async () => new Response(''), [], cacheStorage(), PEER_SCOPE);
    await fire(tapped.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: `/c/vps.tailx.ts.net/sessions/${SESSION}` } } });
    expect(tapped.self.clients.openWindow).toHaveBeenCalledWith(`https://host.example/c/vps.tailx.ts.net/sessions/${SESSION}`);
  });

  it('treats a scope that is not exactly /push/<host>/ as the app\'s own', async () => {
    for (const path of ['/push/', '/push/a/b/', '/push/Vps.example/', '/push/a%2Fb/', '/push/a:1/', '/c/vps.tailx.ts.net/']) {
      const { handlers, self } = loadWorker(async () => new Response(''), [], cacheStorage(), `https://host.example${path}`);
      await fire(handlers, 'push', pushEvent(() => ({ body: 'b', sessionId: SESSION, kind: 'done' })));
      expect(self.registration.showNotification).toHaveBeenCalledWith('Termivoa', expect.objectContaining({ body: 'b', data: { url: `/sessions/${SESSION}` } }));
    }
  });

  it('a peer registration stores no bundle and deletes no cache', async () => {
    const storage = cacheStorage({ 'pterm-bundle-old': {} });
    const { handlers, self, fetch, store } = loadWorker(server(), [], storage, PEER_SCOPE);
    await fire(handlers, 'install', {});
    await fire(handlers, 'activate', {});
    expect(fetch).not.toHaveBeenCalled();
    expect([...store.keys()]).toEqual(['pterm-bundle-old']);
    expect(self.clients.claim).not.toHaveBeenCalled();
    // A page inside the peer scope gets nothing from this worker: no shell, no bundle file.
    const respondWith = vi.fn();
    handlers.get('fetch')?.({ request: { method: 'GET', url: 'https://host.example/push/vps.tailx.ts.net/x', mode: 'navigate' }, respondWith });
    handlers.get('fetch')?.({ request: new Request('https://host.example/assets/Wall-1.js'), respondWith });
    expect(respondWith).not.toHaveBeenCalled();
  });

  it('on tap, focuses an open app window and moves it to the session', async () => {
    const client = { url: 'https://host.example/', focus: vi.fn(async () => undefined), navigate: vi.fn(async () => ({})) };
    const { handlers, self } = loadWorker(async () => new Response(''), [{ url: 'https://other.example/', focus: vi.fn() }, client]);
    const close = vi.fn();
    await fire(handlers, 'notificationclick', { notification: { close, data: { url: `/sessions/${SESSION}` } } });
    expect(close).toHaveBeenCalledOnce();
    expect(self.clients.matchAll).toHaveBeenCalledWith({ type: 'window', includeUncontrolled: true });
    expect(client.focus).toHaveBeenCalledOnce();
    expect(client.navigate).toHaveBeenCalledWith(`https://host.example/sessions/${SESSION}`);
    expect(self.clients.openWindow).not.toHaveBeenCalled();
  });

  it('opens a window when none is open; when navigate fails, sends the path to the page and opens no window', async () => {
    const empty = loadWorker(async () => new Response(''));
    await fire(empty.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: `/sessions/${SESSION}` } } });
    expect(empty.self.clients.openWindow).toHaveBeenCalledWith(`https://host.example/sessions/${SESSION}`);

    // focus() used the one window action of the tap: a browser refuses openWindow() after it.
    const client = {
      url: 'https://host.example/', focus: vi.fn(async () => undefined), postMessage: vi.fn(),
      navigate: vi.fn(async () => { throw new TypeError('not controlled'); }),
    };
    const stuck = loadWorker(async () => new Response(''), [client]);
    await fire(stuck.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: `/sessions/${SESSION}` } } });
    expect(client.focus).toHaveBeenCalledOnce();
    expect(client.postMessage).toHaveBeenCalledWith({ type: 'open', path: `/sessions/${SESSION}` });
    expect(stuck.self.clients.openWindow).not.toHaveBeenCalled();
  });

  it('a peer tap with the app open sends the path to the page and focuses it: no navigate (the window is home\'s), no new window', async () => {
    // As in a browser: only the worker that controls a window may navigate it.
    const client = {
      url: 'https://host.example/wall', focus: vi.fn(async () => undefined), postMessage: vi.fn(),
      navigate: vi.fn(async () => { throw new TypeError('This service worker is not the client\'s active service worker.'); }),
    };
    const { handlers, self } = loadWorker(async () => new Response(''), [client], cacheStorage(), PEER_SCOPE);
    await fire(handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: `/c/vps.tailx.ts.net/sessions/${SESSION}` } } });
    expect(client.postMessage).toHaveBeenCalledWith({ type: 'open', path: `/c/vps.tailx.ts.net/sessions/${SESSION}` });
    expect(client.focus).toHaveBeenCalledOnce();
    expect(client.postMessage.mock.invocationCallOrder[0]).toBeLessThan(client.focus.mock.invocationCallOrder[0] ?? 0);
    expect(client.navigate).not.toHaveBeenCalled();
    expect(self.clients.openWindow).not.toHaveBeenCalled();
    // Data that points to another origin becomes "/", also in the message.
    await fire(handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: '//evil.example/x' } } });
    expect(client.postMessage).toHaveBeenLastCalledWith({ type: 'open', path: '/' });
  });

  it('with several windows, a tap goes to the focused visible one, then a visible one, then the first', async () => {
    const win = (url: string, state: string, focused = false) => ({
      url, visibilityState: state, focused, focus: vi.fn(async () => undefined), postMessage: vi.fn(), navigate: vi.fn(async () => ({})),
    });
    const home = `/sessions/${SESSION}`;
    const peer = `/c/vps.tailx.ts.net/sessions/${SESSION}`;

    // Home scope: the hidden first window is skipped; a focused window beats a visible one.
    const hidden = win('https://host.example/', 'hidden');
    const visible = win('https://host.example/', 'visible');
    const front = win('https://host.example/', 'visible', true);
    const a = loadWorker(async () => new Response(''), [hidden, visible, front]);
    await fire(a.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: home } } });
    expect(front.focus).toHaveBeenCalledOnce();
    expect(front.navigate).toHaveBeenCalledWith(`https://host.example${home}`);
    for (const other of [hidden, visible]) {
      expect(other.focus).not.toHaveBeenCalled();
      expect(other.navigate).not.toHaveBeenCalled();
    }

    // Peer scope: the visible window gets the message and the focus.
    const hidden2 = win('https://host.example/', 'hidden');
    const visible2 = win('https://host.example/', 'visible');
    const b = loadWorker(async () => new Response(''), [hidden2, visible2], cacheStorage(), PEER_SCOPE);
    await fire(b.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: peer } } });
    expect(visible2.postMessage).toHaveBeenCalledWith({ type: 'open', path: peer });
    expect(visible2.focus).toHaveBeenCalledOnce();
    expect(visible2.navigate).not.toHaveBeenCalled();
    expect(hidden2.postMessage).not.toHaveBeenCalled();
    expect(hidden2.focus).not.toHaveBeenCalled();
    expect(b.self.clients.openWindow).not.toHaveBeenCalled();

    // All hidden: the first one, in both scopes.
    for (const scope of ['https://host.example/', PEER_SCOPE]) {
      const first = win('https://host.example/', 'hidden');
      const second = win('https://host.example/', 'hidden');
      const c = loadWorker(async () => new Response(''), [first, second], cacheStorage(), scope);
      await fire(c.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: scope === PEER_SCOPE ? peer : home } } });
      expect(first.focus).toHaveBeenCalledOnce();
      expect(second.focus).not.toHaveBeenCalled();
      expect(c.self.clients.openWindow).not.toHaveBeenCalled();
    }
  });

  it('never opens another origin from notification data', async () => {
    for (const url of ['//evil.example/x', 'https://evil.example/', '/\\evil.example/x', 42]) {
      const { handlers, self } = loadWorker(async () => new Response(''));
      await fire(handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url } } });
      expect(self.clients.openWindow).toHaveBeenCalledWith('https://host.example/');
    }
  });
});
