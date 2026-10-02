import { lazy, Suspense, useEffect, useState } from 'react';
import { api, CLIENT_BUILD, deviceLabelProblem } from '../api/http';
import type { HTTPDeviceSummary, HTTPSettingsResponse } from '../generated/protocol';
import { disablePush, enablePush, pushEnabled, pushSupport } from '../push';
import type { ComputerEntry } from './allSessions';
import { ChunkErrorBoundary, loadChunk } from './chunks';
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from './hooks';

// Computers (and the add-computer sheet it opens) load only when Settings opens.
const ComputersSection = lazy(() => loadChunk(() => import('./Computers')).then((module) => ({ default: module.ComputersSection })));

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

type Note = { readonly text: string; readonly error: boolean };

/** Push on/off for this phone, plus a test send. */
export function NotificationSettings({ onFailure }: { readonly onFailure: (error: unknown) => string }) {
  const [support] = useState(pushSupport);
  // undefined while the browser is asked whether this phone is subscribed.
  const [enabled, setEnabled] = useState<boolean>();
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>();

  useEffect(() => {
    if (support !== 'available') return undefined;
    let live = true;
    pushEnabled().catch(() => false).then((value) => { if (live) setEnabled(value); });
    return () => { live = false; };
  }, [support]);

  function run(work: Promise<void>) {
    setBusy(true);
    setNote(undefined);
    work.catch((failure: unknown) => setNote({ text: onFailure(failure), error: true })).finally(() => setBusy(false));
  }

  // Called straight from the tap: enablePush asks for permission before any await (iOS needs that).
  const toggle = () => run(enabled === true
    ? disablePush().then(() => setEnabled(false))
    : enablePush().then((result) => {
      if (result.ok) setEnabled(true);
      else setNote({ text: result.message, error: true });
    }));

  const sendTest = () => run(api.pushTest().then(({ sent }) => setNote(sent > 0
    ? { text: 'Sent', error: false }
    : { text: 'No subscription on this phone', error: true })));

  return (
    <section className="group stack" aria-labelledby="push-heading">
      <h2 id="push-heading" className="caps">Notifications</h2>
      {support === 'available' ? (
        <>
          <div className="spread">
            <span id="push-label">Buzz when an agent needs you</span>
            <button
              type="button"
              role="switch"
              className="switch"
              aria-checked={enabled === true}
              aria-labelledby="push-label"
              disabled={enabled === undefined || busy}
              onClick={toggle}
            ><span className="switch-track" aria-hidden="true" /></button>
          </div>
          <p className="hint-text">Only the session name and status. Never terminal text.</p>
          <button type="button" disabled={enabled !== true || busy} onClick={sendTest}>Send a test notification</button>
          <p role="status" className={note?.error === true ? 'error field-note' : 'muted field-note'}>{note?.text ?? ''}</p>
        </>
      ) : (
        <p className="muted">{support === 'needs-home-screen' ? 'Add to Home Screen to get notifications' : 'Not supported in this browser'}</p>
      )}
    </section>
  );
}

export function Settings({
  settings, entries, fontSize, onFontSize, onBack, onRenamed, onFailure, onLogout, onRetry, onComputersChanged,
}: {
  /** Home's settings; undefined while home cannot be reached (only Computers and text size then). */
  readonly settings: HTTPSettingsResponse | undefined;
  readonly entries: ReadonlyArray<ComputerEntry>;
  readonly fontSize: number;
  readonly onFontSize: (size: number) => void;
  readonly onBack: () => void;
  readonly onRenamed: (device: HTTPDeviceSummary) => void;
  readonly onFailure: (error: unknown) => string;
  readonly onLogout: () => Promise<void>;
  readonly onRetry: (origin: string) => void;
  readonly onComputersChanged: () => void;
}) {
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const current = settings?.device.label ?? '';
  // Follows home's label (home may answer after this screen opened) until the user types.
  const [draft, setDraft] = useState<string>();
  const name = draft ?? current;
  const multi = entries.length > 1;
  const [nameState, setNameState] = useState<NameState>({ kind: 'idle' });
  const nameProblem = deviceLabelProblem(name.trim());
  const unchanged = name.trim() === current;

  async function rename() {
    setNameState({ kind: 'saving' });
    try {
      const result = await saveDeviceName(name, current);
      if (result.kind === 'invalid') {
        setNameState({ kind: 'error', message: result.message });
        return;
      }
      if (result.kind === 'saved') {
        setDraft(undefined);
        onRenamed(result.device);
      }
      setNameState({ kind: 'saved' });
    } catch (failure) {
      setNameState({ kind: 'error', message: onFailure(failure) });
    }
  }

  return (
    <main className="page screen">
      <header className="screen-header">
        <h1 className="display">Settings</h1>
        <button type="button" className="link strong" onClick={onBack}>Done</button>
      </header>
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      <ChunkErrorBoundary>
        <Suspense fallback={<p role="status" className="muted">Loading computers…</p>}>
          <ComputersSection entries={entries} onRetry={onRetry} onChanged={onComputersChanged} onFailure={onFailure} />
        </Suspense>
      </ChunkErrorBoundary>

      {settings === undefined && (
        <p className="muted">Your home computer cannot be reached. Device name, notifications and log out need it.</p>
      )}
      {settings !== undefined && (
      <section className="group stack" aria-labelledby="device-heading">
        <h2 id="device-heading" className="caps">This device</h2>
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
              setDraft(event.target.value);
              setNameState({ kind: 'idle' });
            }}
          />
          <p id="device-name-help" className="hint-text">
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
      </section>
      )}

      <section className="group stack" aria-labelledby="font-heading">
        <h2 id="font-heading" className="caps">Terminal text size</h2>
        <div className="font-row" role="group" aria-labelledby="font-heading">
          <button type="button" className="key" aria-label="Smaller text" disabled={fontSize <= MIN_FONT_SIZE}
            onClick={() => onFontSize(fontSize - 1)}>A−</button>
          <output aria-live="polite" className="font-size">{fontSize} px</output>
          <button type="button" className="key" aria-label="Larger text" disabled={fontSize >= MAX_FONT_SIZE}
            onClick={() => onFontSize(fontSize + 1)}>A+</button>
        </div>
      </section>

      {settings !== undefined && <NotificationSettings onFailure={onFailure} />}

      <section className="group stack" aria-labelledby="build-heading">
        <h2 id="build-heading" className="caps">Build</h2>
        <dl className="facts">
          {settings !== undefined && <><dt>Host build</dt><dd>{settings.hostBuild}</dd></>}
          <dt>Web build</dt><dd>{CLIENT_BUILD}</dd>
        </dl>
      </section>

      {settings !== undefined && (
      <section className="group stack" aria-labelledby="logout-heading">
        <h2 id="logout-heading" className="caps">Log out</h2>
        <p className="muted">
          {multi
            ? 'Signs this phone out of every computer. Running sessions keep running.'
            : 'Removes this browser\'s access. Running sessions keep running on your computer.'}
        </p>
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
      )}
    </main>
  );
}
