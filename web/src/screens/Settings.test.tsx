import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import type { Computer } from '../computers';
import type { HTTPSettingsResponse } from '../generated/protocol';
import type { ComputerEntry } from './allSessions';
import { LogoutSection, NotificationSettings, saveDeviceName, Settings } from './Settings';

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
        onCheck={() => Promise.resolve()}
        onComputersChanged={() => undefined}
      />,
    );
    expect(html).toContain('<label for="device-name">Device name</label>');
    expect(html).toMatch(/<input id="device-name" [^>]*value="iPhone"/u);
    expect(html).toContain('aria-describedby="device-name-help device-name-status"');
    // Unchanged name: nothing to save yet.
    expect(html).toMatch(/<button type="submit" class="secondary" disabled="">Save name<\/button>/u);
  });
});

describe('Settings log out', () => {
  const page = (entries: ComputerEntry[]) => renderToStaticMarkup(
    <Settings
      settings={settings}
      fontSize={14}
      onFontSize={() => undefined}
      onBack={() => undefined}
      onRenamed={() => undefined}
      onFailure={() => ''}
      onLogout={async () => undefined}
      entries={entries}
      onCheck={() => Promise.resolve()}
      onComputersChanged={() => undefined}
    />,
  );
  const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
  const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };

  it('says it signs out of every computer only when there are several', () => {
    expect(page([{ computer: mac, status: { kind: 'loading' } }])).toContain('Removes this browser&#x27;s access.');
    const multi = page([{ computer: mac, status: { kind: 'loading' } }, { computer: vps, status: { kind: 'loading' } }]);
    expect(multi).toContain('Signs this phone out of every computer. Running sessions keep running.');
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
    // Busy, it says "Sending…" in place of its label, so a second test that says "Sent" again is not a dead tap.
    expect(html).toMatch(/<button type="button" class="secondary" disabled=""><span class="btn-swap"><span>Send a test notification<\/span><span class="off" aria-hidden="true">.*?Sending…<\/span><\/span><\/button>/u);
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

describe('LogoutSection', () => {
  it('says "Signing out…" and turns the button off while log out runs', () => {
    const idle = renderToStaticMarkup(<LogoutSection multi busy={false} onLogout={() => undefined} />);
    expect(idle).not.toContain('Signing out');
    expect(idle).not.toContain('disabled');
    const busy = renderToStaticMarkup(<LogoutSection multi busy onLogout={() => undefined} />);
    expect(busy).toContain('<p role="status" class="muted field-note">Signing out…</p>');
    // Busy: off, marked busy, and the label stays (under the spinner) for screen readers.
    expect(busy).toMatch(/<button[^>]*disabled=""[^>]*aria-busy="true"[^>]*>Log out<svg[^]*<\/svg><\/button>/u);
  });

  it('shows a failed log out next to its button, not at the top of the page', () => {
    const failed = renderToStaticMarkup(<LogoutSection multi busy={false} error="Connection unavailable" onLogout={() => undefined} />);
    expect(failed).toMatch(/aria-labelledby="logout-heading">.*<div class="notice danger" role="alert"><p class="notice-title">Connection unavailable<\/p><\/div><button/u);
    expect(renderToStaticMarkup(<LogoutSection multi busy={false} onLogout={() => undefined} />)).not.toContain('role="alert"');
  });
});
