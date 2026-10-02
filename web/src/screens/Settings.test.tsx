import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import type { HTTPSettingsResponse } from '../generated/protocol';
import { NotificationSettings, saveDeviceName, Settings } from './Settings';

const settings: HTTPSettingsResponse = {
  device: { id: '00000000-0000-4000-8000-000000000020', label: 'iPhone' },
  brokerGeneration: '00000000-0000-4000-8000-000000000030',
  hostBuild: 'pterm/test',
  projects: [],
  shellProfiles: [],
  protocolRevision: 1,
  minProtocolRevision: 1,
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
        entries={[]}
        onRetry={() => undefined}
        onComputersChanged={() => undefined}
      />,
    );
    expect(html).toContain('<label for="device-name">Device name</label>');
    expect(html).toMatch(/<input id="device-name" [^>]*value="iPhone"/u);
    expect(html).toContain('aria-describedby="device-name-help device-name-status"');
    // Unchanged name: nothing to save yet.
    expect(html).toMatch(/<button type="submit" class="primary" disabled="">Save name<\/button>/u);
  });
});

describe('Settings notifications', () => {
  const render = () => renderToStaticMarkup(<NotificationSettings onFailure={() => ''} />);
  const stubBrowser = (userAgent: string, standalone = false) => {
    vi.stubGlobal('navigator', { userAgent, maxTouchPoints: 5, standalone, serviceWorker: {} });
    vi.stubGlobal('PushManager', function PushManager() {});
    vi.stubGlobal('Notification', { permission: 'default' });
  };

  it('shows an accessible switch and a test button that waits until push is on', () => {
    stubBrowser('Mozilla/5.0 (Linux; Android 14) Chrome/130.0 Mobile');
    const html = render();
    expect(html).toContain('<h2 id="push-heading" class="caps">Notifications</h2>');
    expect(html).toMatch(/<button type="button" role="switch" class="switch" aria-checked="false" aria-labelledby="push-label" disabled="">/u);
    expect(html).toContain('<button type="button" disabled="">Send a test notification</button>');
  });

  it('tells an iPhone Safari tab to add the app to the Home Screen', () => {
    stubBrowser('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1');
    const html = render();
    expect(html).toContain('Add to Home Screen to get notifications');
    expect(html).not.toContain('role="switch"');
  });

  it('says when the browser has no push', () => {
    vi.stubGlobal('navigator', { userAgent: 'Mozilla/5.0 (X11; Linux) Firefox/130.0', maxTouchPoints: 0 });
    expect(render()).toContain('Not supported in this browser');
  });
});
