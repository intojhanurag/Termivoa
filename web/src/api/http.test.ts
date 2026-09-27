import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, deviceLabelProblem } from './http';

const device = { id: '00000000-0000-4000-8000-000000000020', label: 'Work phone' };

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());

describe('deviceLabelProblem mirrors the deviceLabel schema', () => {
  it('accepts 1–64 characters within 64 UTF-8 bytes', () => {
    expect(deviceLabelProblem('a')).toBeUndefined();
    expect(deviceLabelProblem('x'.repeat(64))).toBeUndefined();
    expect(deviceLabelProblem('Anurag’s iPhone')).toBeUndefined();
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
