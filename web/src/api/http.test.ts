import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, blobToBase64, deviceLabelProblem, REQUEST_TIMEOUT_MS, UPLOAD_TIMEOUT_MS } from './http';

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

describe('photo upload', () => {
  afterEach(() => { vi.useRealTimers(); });

  it('posts the image to the session uploads path with the fixed header', async () => {
    const saved = { path: '.termivoa/uploads/20260927-143012-a1b2c3d4.png', size: 421000, contentType: 'image/png' };
    const fetch = vi.fn(async () => json(200, saved));
    vi.stubGlobal('fetch', fetch);

    await expect(api.uploadImage('a/b', { generation: 'g1', data: 'iVBORw0KGgo=' })).resolves.toEqual(saved);

    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/v1/sessions/a%2Fb/uploads');
    expect(init.method).toBe('POST');
    expect(init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(init.body as string)).toEqual({ generation: 'g1', data: 'iVBORw0KGgo=' });
  });

  it('waits longer than a normal request before giving up', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_path: string, init: RequestInit) => new Promise((_resolve, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    })));
    let settled = false;
    const pending = api.uploadImage('s', { generation: 'g', data: 'AA==' }).catch((error: unknown) => { settled = true; return error; });
    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(UPLOAD_TIMEOUT_MS - REQUEST_TIMEOUT_MS);
    expect(await pending).toMatchObject({ code: 'network' });
  });

  it('shows the server message for a refused upload', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(409, { error: { code: 'not_controller', message: 'Take control to attach a photo.' } })));
    const failure = await api.uploadImage('s', { generation: 'g', data: 'AA==' }).catch((error: unknown) => error);
    expect(failure).toMatchObject({ status: 409, code: 'not_controller', message: 'Take control to attach a photo.' });
  });

  it('encodes a blob as standard padded base64 across chunk edges', async () => {
    const bytes = new Uint8Array(0x8000 * 2 + 5).map((_value, index) => (index * 7) % 256);
    expect(await blobToBase64(new Blob([bytes]))).toBe(Buffer.from(bytes).toString('base64'));
    expect(await blobToBase64(new Blob([new Uint8Array([1])]))).toBe('AQ==');
    expect(await blobToBase64(new Blob([]))).toBe('');
  });
});
