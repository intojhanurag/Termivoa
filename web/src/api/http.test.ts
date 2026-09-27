import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, deviceLabelProblem, REQUEST_TIMEOUT_MS } from './http';

const device = { id: '00000000-0000-4000-8000-000000000020', label: 'Work phone' };

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());

describe('deviceLabelProblem mirrors the deviceLabel schema', () => {
  it('accepts 1–64 characters within 64 UTF-8 bytes', () => {
    expect(deviceLabelProblem('a')).toBeUndefined();
    expect(deviceLabelProblem('x'.repeat(64))).toBeUndefined();
    expect(deviceLabelProblem('Ada’s iPhone')).toBeUndefined();
    expect(deviceLabelProblem('😀'.repeat(16))).toBeUndefined(); // 16 characters, 64 bytes
  });

  it('rejects empty, too long, too many bytes, control characters and edge spaces', () => {
    expect(deviceLabelProblem('')).toBe('Enter a name.');
    expect(deviceLabelProblem('x'.repeat(65))).toMatch(/shorter/u);
    expect(deviceLabelProblem('😀'.repeat(17))).toMatch(/shorter/u); // 68 bytes
    expect(deviceLabelProblem('電'.repeat(22))).toMatch(/shorter/u); // 66 bytes
    expect(deviceLabelProblem('a\u0007b')).toMatch(/control/u);
    expect(deviceLabelProblem('a\nb')).toMatch(/control/u);
    expect(deviceLabelProblem(' phone')).toMatch(/start or end/u);
    expect(deviceLabelProblem('phone ')).toMatch(/start or end/u);
  });
});

describe('api.renameDevice', () => {
  it('sends a same-origin JSON PATCH with the fixed request header', async () => {
    const fetch = vi.fn(async () => json(200, device));
    vi.stubGlobal('fetch', fetch);

    await expect(api.renameDevice({ label: 'Work phone' })).resolves.toEqual(device);

    expect(fetch).toHaveBeenCalledOnce();
    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/v1/device');
    expect(init.method).toBe('PATCH');
    expect(init.credentials).toBe('same-origin');
    expect(init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(init.body as string)).toEqual({ label: 'Work phone' });
  });

  it('surfaces the server error code and safe message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(400, { error: { code: 'invalid_request', message: 'label is invalid' } })));
    const failure = await api.renameDevice({ label: 'x' }).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ApiError);
    expect(failure).toMatchObject({ status: 400, code: 'invalid_request', message: 'label is invalid' });
  });
});

describe('request timeout', () => {
  afterEach(() => { vi.useRealTimers(); });

  it('turns a request that never answers into a network error', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_path: string, init: RequestInit) => new Promise((_resolve, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    })));
    const pending = api.settings().catch((error: unknown) => error);
    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS);
    const error = await pending;
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('network');
  });
});

describe('push API', () => {
  it('reads the key, then posts subscribe, unsubscribe and test with the fixed header', async () => {
    const fetch = vi.fn(async (path: string) => (path === '/api/v1/push'
      ? json(200, { publicKey: 'B'.repeat(87) })
      : path === '/api/v1/push/test' ? json(200, { sent: 1 }) : new Response(null, { status: 204 })));
    vi.stubGlobal('fetch', fetch);
    const endpoint = 'https://push.example/abc';

    await expect(api.pushConfig()).resolves.toEqual({ publicKey: 'B'.repeat(87) });
    await expect(api.pushSubscribe({ endpoint, keys: { p256dh: 'B'.repeat(87), auth: 'a'.repeat(22) } })).resolves.toBeUndefined();
    await expect(api.pushUnsubscribe({ endpoint })).resolves.toBeUndefined();
    await expect(api.pushTest()).resolves.toEqual({ sent: 1 });

    const calls = fetch.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls.map(([path, init]) => `${init.method} ${path}`)).toEqual([
      'GET /api/v1/push',
      'POST /api/v1/push/subscribe',
      'POST /api/v1/push/unsubscribe',
      'POST /api/v1/push/test',
    ]);
    expect(calls[1]?.[1].headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(calls[1]?.[1].body as string)).toEqual({ endpoint, keys: { p256dh: 'B'.repeat(87), auth: 'a'.repeat(22) } });
    expect(JSON.parse(calls[2]?.[1].body as string)).toEqual({ endpoint });
    expect(calls[3]?.[1].body).toBe('{}');
  });
});
