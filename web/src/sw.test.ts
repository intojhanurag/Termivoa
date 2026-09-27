import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';

type Handler = (event: Record<string, unknown>) => void;

/** Runs src/sw.js against a fake worker global and returns its handlers. */
function loadWorker(network: (request: Request) => Promise<Response>) {
  const handlers = new Map<string, Handler>();
  const put = vi.fn(async () => undefined);
  const cache = { match: vi.fn(async () => undefined), put };
  const self = {
    location: { origin: 'https://host.example' },
    addEventListener: (type: string, handler: Handler) => handlers.set(type, handler),
    skipWaiting: vi.fn(),
    clients: { claim: vi.fn(async () => undefined) },
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
    expect(html).toContain('<meta http-equiv="refresh" content="10">');
    expect(html).toContain('<a href="/">Retry now</a>');
  });
});
