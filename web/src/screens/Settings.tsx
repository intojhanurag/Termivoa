import { useState } from 'react';
import { api, CLIENT_BUILD, deviceLabelProblem } from '../api/http';
import type { HTTPDeviceSummary, HTTPSettingsResponse } from '../generated/protocol';
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from './hooks';

export type DeviceNameResult =
  | { readonly kind: 'saved'; readonly device: HTTPDeviceSummary }
  | { readonly kind: 'invalid'; readonly message: string }
  | { readonly kind: 'unchanged' };

/** Validates like the schema, then renames this device. API failures throw ApiError. */
export async function saveDeviceName(draft: string, current: string): Promise<DeviceNameResult> {
  const label = draft.trim();
  const problem = deviceLabelProblem(label);
  if (problem !== undefined) return { kind: 'invalid', message: problem };
  if (label === current) return { kind: 'unchanged' };
  return { kind: 'saved', device: await api.renameDevice({ label }) };
}

type NameState =
  | { readonly kind: 'idle' | 'saving' | 'saved' }
  | { readonly kind: 'error'; readonly message: string };

export function Settings({ settings, fontSize, onFontSize, onBack, onRenamed, onFailure, onLogout }: {
  readonly settings: HTTPSettingsResponse;
  readonly fontSize: number;
  readonly onFontSize: (size: number) => void;
  readonly onBack: () => void;
  readonly onRenamed: (device: HTTPDeviceSummary) => void;
  readonly onFailure: (error: unknown) => string;
  readonly onLogout: () => Promise<void>;
}) {
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState(settings.device.label);
  const [nameState, setNameState] = useState<NameState>({ kind: 'idle' });
  const nameProblem = deviceLabelProblem(name.trim());
  const unchanged = name.trim() === settings.device.label;

  async function rename() {
    setNameState({ kind: 'saving' });
    try {
      const result = await saveDeviceName(name, settings.device.label);
      if (result.kind === 'invalid') {
        setNameState({ kind: 'error', message: result.message });
        return;
      }
      if (result.kind === 'saved') {
        setName(result.device.label);
        onRenamed(result.device);
      }
      setNameState({ kind: 'saved' });
    } catch (failure) {
      setNameState({ kind: 'error', message: onFailure(failure) });
    }
  }

  return (
    <main className="page">
      <header className="page-header">
        <h1>Settings</h1>
        <button type="button" onClick={onBack}>Sessions</button>
      </header>
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      <section className="card stack" aria-labelledby="device-heading">
        <h2 id="device-heading">This device</h2>
        <form
          className="stack"
          onSubmit={(event) => {
            event.preventDefault();
            void rename();
          }}
        >
          <label htmlFor="device-name">Device name</label>
          <input
            id="device-name"
            value={name}
            maxLength={64}
            autoComplete="off"
            aria-describedby="device-name-help device-name-status"
            aria-invalid={nameProblem !== undefined}
            onChange={(event) => {
              setName(event.target.value);
              setNameState({ kind: 'idle' });
            }}
          />
          <p id="device-name-help" className="muted field-note">
            Your computer shows this name in <code>pterm devices</code>.
          </p>
          <p id="device-name-status" role="status" className={nameState.kind === 'error' || nameProblem !== undefined ? 'error field-note' : 'muted field-note'}>
            {nameProblem ?? (nameState.kind === 'error' ? nameState.message
              : nameState.kind === 'saving' ? 'Saving…'
                : nameState.kind === 'saved' ? 'Name saved.' : '')}
          </p>
          <button
            type="submit"
            className="primary"
            disabled={nameProblem !== undefined || unchanged || nameState.kind === 'saving'}
          >Save name</button>
        </form>
        <dl className="facts">
          <dt>Host build</dt><dd>{settings.hostBuild}</dd>
          <dt>Web build</dt><dd>{CLIENT_BUILD}</dd>
        </dl>
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
