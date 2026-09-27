import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';

type Handler = (event: Record<string, unknown>) => void;

/** Runs src/sw.js against a fake worker global and returns its handlers. */
function loadWorker(network: (request: Request) => Promise<Response>, windows: Array<Record<string, unknown>> = []) {
  const handlers = new Map<string, Handler>();
  const put = vi.fn(async () => undefined);
  const cache = { match: vi.fn(async () => undefined), put };
  const self = {
    location: { origin: 'https://host.example' },
    addEventListener: (type: string, handler: Handler) => handlers.set(type, handler),
    skipWaiting: vi.fn(),
    clients: {
      claim: vi.fn(async () => undefined),
      matchAll: vi.fn(async () => windows),
      openWindow: vi.fn(async () => null),
    },
    registration: { showNotification: vi.fn(async () => undefined) },
  };
  const caches = { open: vi.fn(async () => cache), keys: vi.fn(async () => []), delete: vi.fn() };
  const source = readFileSync(new URL('./sw.js', import.meta.url), 'utf8');
  new Function('self', 'caches', 'fetch', source)(self, caches, network);
  return { handlers, self, put };
}

describe('service worker', () => {
  it('never activates a new version on install; only an explicit reload message does', async () => {
    const { handlers, self } = loadWorker(async () => new Response(''));
    handlers.get('install')?.({ waitUntil: vi.fn() });
    expect(self.skipWaiting).not.toHaveBeenCalled();
    handlers.get('message')?.({ data: { type: 'skip-waiting' } });
    expect(self.skipWaiting).toHaveBeenCalledOnce();
  });

  it('does not cache a missing chunk, so a reload can fetch the new build', async () => {
    const { handlers, put } = loadWorker(async () => new Response('not found', { status: 404 }));
    let reply: Promise<Response> | undefined;
    handlers.get('fetch')?.({
      request: new Request('https://host.example/assets/Live-old.js'),
      respondWith: (value: Promise<Response>) => { reply = value; },
    });
    expect((await reply)?.status).toBe(404);
    expect(put).not.toHaveBeenCalled();
  });

  it('answers a failed page load with an offline page that retries by itself', async () => {
    const { handlers } = loadWorker(async () => { throw new TypeError('offline'); });
    let reply: Promise<Response> | undefined;
    handlers.get('fetch')?.({
      // Request cannot be built with mode "navigate", so pass the fields the worker reads.
      request: { method: 'GET', url: 'https://host.example/', mode: 'navigate' },
      respondWith: (value: Promise<Response>) => { reply = value; },
    });
    const response = await reply;
    expect(response?.status).toBe(503);
    const html = await response?.text();
    expect(html).toContain('<meta http-equiv="refresh" content="5">');
    expect(html).toContain('retrying every 5s');
    expect(html).toContain('<a href="/">Retry now</a>');
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
      expect(self.registration.showNotification).toHaveBeenCalledWith('Phone Terminal', expect.objectContaining({ body: '', tag: 'pterm:test', data: { url: '/' } }));
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

  it('opens a window when none is open, or when navigate fails', async () => {
    const empty = loadWorker(async () => new Response(''));
    await fire(empty.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: `/sessions/${SESSION}` } } });
    expect(empty.self.clients.openWindow).toHaveBeenCalledWith(`https://host.example/sessions/${SESSION}`);

    const client = { url: 'https://host.example/', focus: vi.fn(async () => undefined), navigate: vi.fn(async () => { throw new TypeError('not controlled'); }) };
    const stuck = loadWorker(async () => new Response(''), [client]);
    await fire(stuck.handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url: '/' } } });
    expect(stuck.self.clients.openWindow).toHaveBeenCalledWith('https://host.example/');
  });

  it('never opens another origin from notification data', async () => {
    for (const url of ['//evil.example/x', 'https://evil.example/', '/\\evil.example/x', 42]) {
      const { handlers, self } = loadWorker(async () => new Response(''));
      await fire(handlers, 'notificationclick', { notification: { close: vi.fn(), data: { url } } });
      expect(self.clients.openWindow).toHaveBeenCalledWith('https://host.example/');
    }
  });
});
