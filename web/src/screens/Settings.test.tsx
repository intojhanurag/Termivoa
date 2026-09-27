import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import type { HTTPSettingsResponse } from '../generated/protocol';
import { saveDeviceName, Settings } from './Settings';

const settings: HTTPSettingsResponse = {
  device: { id: '00000000-0000-4000-8000-000000000020', label: 'iPhone' },
  brokerGeneration: '00000000-0000-4000-8000-000000000030',
  hostBuild: 'pterm/test',
  projects: [],
  shellProfiles: [],
};

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());

describe('saveDeviceName', () => {
  it('trims, sends PATCH /api/v1/device and returns the saved device', async () => {
    const fetch = vi.fn(async () => json(200, { ...settings.device, label: 'Work phone' }));
    vi.stubGlobal('fetch', fetch);

    await expect(saveDeviceName('  Work phone ', 'iPhone'))
      .resolves.toEqual({ kind: 'saved', device: { ...settings.device, label: 'Work phone' } });
    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/v1/device');
    expect(init.method).toBe('PATCH');
    expect(init.body).toBe('{"label":"Work phone"}');
  });

  it('does not call the server for an invalid or unchanged name', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(saveDeviceName('   ', 'iPhone')).resolves.toEqual({ kind: 'invalid', message: 'Enter a name.' });
    await expect(saveDeviceName('x'.repeat(65), 'iPhone')).resolves.toMatchObject({ kind: 'invalid' });
    await expect(saveDeviceName('iPhone ', 'iPhone')).resolves.toEqual({ kind: 'unchanged' });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('throws the API error so the screen can show it or return to Setup', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(401, { error: { code: 'revoked', message: 'device revoked' } })));
    await expect(saveDeviceName('Work phone', 'iPhone')).rejects.toBeInstanceOf(ApiError);
  });
});

describe('Settings device name field', () => {
  it('shows the current label in a labelled field with a Save button', () => {
    const html = renderToStaticMarkup(
      <Settings
        settings={settings}
        fontSize={14}
        onFontSize={() => undefined}
        onBack={() => undefined}
        onRenamed={() => undefined}
        onFailure={() => ''}
        onLogout={async () => undefined}
      />,
    );
    expect(html).toContain('<label for="device-name">Device name</label>');
    expect(html).toMatch(/<input id="device-name" [^>]*value="iPhone"/u);
    expect(html).toContain('aria-describedby="device-name-help device-name-status"');
    // Unchanged name: nothing to save yet.
    expect(html).toMatch(/<button type="submit" class="primary" disabled="">Save name<\/button>/u);
  });
});
