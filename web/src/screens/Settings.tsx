import { useState } from 'react';
import type { HTTPSettingsResponse } from '../generated/protocol';
import { CLIENT_BUILD } from '../terminal/Connection';
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from './hooks';

export function Settings({ settings, fontSize, onFontSize, onBack, onLogout }: {
  readonly settings: HTTPSettingsResponse;
  readonly fontSize: number;
  readonly onFontSize: (size: number) => void;
  readonly onBack: () => void;
  readonly onLogout: () => Promise<void>;
}) {
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  return (
    <main className="page">
      <header className="page-header">
        <h1>Settings</h1>
        <button type="button" onClick={onBack}>Sessions</button>
      </header>
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      <section className="card stack" aria-labelledby="device-heading">
        <h2 id="device-heading">This device</h2>
        <dl className="facts">
          <dt>Device name</dt><dd>{settings.device.label}</dd>
          <dt>Host build</dt><dd>{settings.hostBuild}</dd>
          <dt>Web build</dt><dd>{CLIENT_BUILD}</dd>
        </dl>
        <p className="muted">Device names are set when pairing. Manage devices on your computer with <code>pterm devices</code>.</p>
      </section>

      <section className="card stack" aria-labelledby="font-heading">
        <h2 id="font-heading">Terminal text size</h2>
        <div className="row" role="group" aria-labelledby="font-heading">
          <button type="button" aria-label="Smaller text" disabled={fontSize <= MIN_FONT_SIZE}
            onClick={() => onFontSize(fontSize - 1)}>A−</button>
          <output aria-live="polite" className="font-size">{fontSize} px</output>
          <button type="button" aria-label="Larger text" disabled={fontSize >= MAX_FONT_SIZE}
            onClick={() => onFontSize(fontSize + 1)}>A+</button>
        </div>
      </section>

      <section className="card stack" aria-labelledby="logout-heading">
        <h2 id="logout-heading">Log out</h2>
        <p className="muted">Removes this browser's access. Running sessions keep running on your computer.</p>
        <button
          type="button"
          className="danger"
          disabled={busy}
          onClick={() => {
            setBusy(true);
            onLogout().catch((failure: unknown) => {
              setError(failure instanceof Error ? failure.message : 'Logout failed');
              setBusy(false);
            });
          }}
        >Log out</button>
      </section>
    </main>
  );
}
