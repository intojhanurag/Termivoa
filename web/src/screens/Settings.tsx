import { lazy, Suspense, useEffect, useState } from 'react';
import { api, client, CLIENT_BUILD, deviceLabelProblem } from '../api/http';
import type {
  HTTPDeviceSummary, HTTPProviderInfo, HTTPProviderNotifyState, HTTPProviderUpdateRequest, HTTPSettingsResponse,
} from '../generated/protocol';
import { disablePush, enablePush, pushEnabled, pushSupport, syncPeerPush, testPush } from '../push';
import type { Computer } from '../computers';
import type { ComputerEntry } from './allSessions';
import { ErrorBoundary, loadChunk } from './chunks';
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from './hooks';
import { providerKey, providerSources, type ProviderEntry, type ProviderSource } from './providers';
import { Button, Notice } from './ui';

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

/** "No notifications from vps, pi." */
export const noPushFrom = (labels: ReadonlyArray<string>): string => `No notifications from ${labels.join(', ')}.`;

/** Push on/off for this phone on every computer, plus a test send. */
export function NotificationSettings({ onFailure }: { readonly onFailure: (error: unknown) => string }) {
  const [support] = useState(pushSupport);
  // undefined while the browser is asked whether this phone is subscribed.
  const [enabled, setEnabled] = useState<boolean>();
  // What runs now: the switch, or a test send.
  const [busy, setBusy] = useState<'switch' | 'test'>();
  const [note, setNote] = useState<Note>();

  // The computers that send no notifications to this phone (home is on).
  const [missing, setMissing] = useState<ReadonlyArray<string>>([]);

  useEffect(() => {
    if (support !== 'available') return undefined;
    let live = true;
    pushEnabled().catch(() => false).then((value) => {
      if (live) setEnabled(value);
      // Tries each peer again, so this also says which ones are still without.
      return value ? syncPeerPush() : [];
    }).then((labels) => { if (live) setMissing(labels); });
    return () => { live = false; };
  }, [support]);

  function run(what: 'switch' | 'test', work: Promise<void>) {
    setBusy(what);
    setNote(undefined);
    work.catch((failure: unknown) => setNote({ text: onFailure(failure), error: true })).finally(() => setBusy(undefined));
  }

  // Called straight from the tap: enablePush asks for permission before any await (iOS needs that).
  const toggle = () => run('switch', enabled === true
    ? disablePush().then(() => { setEnabled(false); setMissing([]); })
    : enablePush().then((result) => {
      if (!result.ok) {
        setNote({ text: result.message, error: true });
        return;
      }
      // Home is on: the switch is free again. The peers answer later.
      setEnabled(true);
      void result.missing.then(setMissing);
    }));

  // One test from each computer. Home's answer shows at once; a peer that sent none is named when known.
  const sendTest = () => run('test', testPush().then(({ sent, missing: late }) => {
    setNote(sent ? { text: 'Sent', error: false } : { text: 'No subscription on this phone', error: true });
    if (sent) void late.then((labels) => { if (labels.length > 0) setNote({ text: `Sent. ${noPushFrom(labels)}`, error: true }); });
  }));

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
              disabled={enabled === undefined || busy !== undefined}
              onClick={toggle}
            ><span className="switch-track" aria-hidden="true" /></button>
          </div>
          <p className="hint-text">Only the session name and status. Never terminal text.</p>
          {enabled === true && missing.length > 0 && <Notice tone="warning" title={noPushFrom(missing)} />}
          <Button className="secondary" busy={busy === 'test'} busyLabel="Sending…" disabled={enabled !== true || busy === 'switch'} onClick={sendTest}>
            Send a test notification
          </Button>
          <div role="status" className="field-note">
            {note?.error === true ? <Notice tone="danger" title={note.text} /> : <span className="muted">{note?.text ?? ''}</span>}
          </div>
        </>
      ) : (
        <p className="muted">{support === 'needs-home-screen' ? 'Add to Home Screen to get notifications' : 'Not supported in this browser'}</p>
      )}
    </section>
  );
}

/** The state word and its one line for Settings → Integrations (docs/herdr-provider.md, section 8). */
export function integrationText(info: Pick<HTTPProviderInfo, 'name' | 'state'>, label: string): {
  readonly word: string;
  readonly tone: 'green' | 'amber' | 'muted';
  readonly line?: string;
} {
  switch (info.state) {
    case 'ready': return { word: 'Running', tone: 'green' };
    case 'notRunning': return { word: 'Not running', tone: 'amber', line: `${info.name} is not running on ${label}. Start it there to see its projects.` };
    case 'unsupported': return { word: 'Update', tone: 'amber', line: `Update ${info.name} on ${label}. This version is too old for Termivoa.` };
    case 'off': return { word: 'Off', tone: 'muted', line: `Off for every phone: ${label} does not show ${info.name} or buzz for it.` };
  }
}

/** The notify list after one buzz switch: in the fixed order blocked, done. */
export function notifyWith(current: ReadonlyArray<HTTPProviderNotifyState>, word: HTTPProviderNotifyState, on: boolean): HTTPProviderNotifyState[] {
  return (['blocked', 'done'] as const).filter((item) => (item === word ? on : current.includes(item)));
}

type Field = 'enabled' | HTTPProviderNotifyState;

/** One switch saved on its computer: the new info, or the failure's words for that computer (a peer's 401 says pair again, never Setup). */
export async function saveSwitch(
  info: HTTPProviderInfo, field: Field, on: boolean, computer: Computer,
  save: (body: HTTPProviderUpdateRequest) => Promise<HTTPProviderInfo>,
  onFailure: (error: unknown, from: Computer) => string,
): Promise<{ readonly info: HTTPProviderInfo } | { readonly error: string }> {
  try {
    return { info: await save(field === 'enabled' ? { enabled: on } : { notify: notifyWith(info.notify, field, on) }) };
  } catch (failure) {
    return { error: onFailure(failure, computer) };
  }
}

/** One provider on one computer: on or off, and which states buzz. Saved on that computer for every device. */
export function IntegrationRow({ source, entry, onFailure, onSaved, save = (body) => client(source.computer.origin).providerUpdate(source.info.id, body) }: {
  readonly source: ProviderSource;
  /** The provider's latest read on that computer: its `state` is newer than the settings' (docs/herdr-provider.md, 4.5). */
  readonly entry?: Pick<ProviderEntry, 'status'> | undefined;
  readonly onFailure: (error: unknown, from: Computer) => string;
  /** Reads the computer's settings and the provider again, so the rest of the app follows. */
  readonly onSaved?: () => void;
  readonly save?: (body: HTTPProviderUpdateRequest) => Promise<HTTPProviderInfo>;
}) {
  const [saved, setSaved] = useState<{ readonly info: HTTPProviderInfo; readonly at: number }>();
  // The switch moves at once; it is off for taps until the computer answered.
  const [pending, setPending] = useState<{ readonly field: Field; readonly on: boolean }>();
  const [note, setNote] = useState<Note>();
  const info = saved?.info ?? source.info;
  const read = entry?.status.kind === 'ok' ? entry.status : undefined;
  // The state word: the newest of the provider read and this screen's own save.
  const state = read !== undefined && (saved === undefined || read.at >= saved.at) ? read.snapshot.state : info.state;
  const { computer } = source;
  const text = integrationText({ name: info.name, state }, computer.label);
  const id = `integration-${computer.host}-${info.id}`.replace(/[^A-Za-z0-9_-]/gu, '-');
  const value = (field: Field) => (pending?.field === field ? pending.on : field === 'enabled' ? info.enabled : info.notify.includes(field));

  function change(field: Field) {
    const on = !value(field);
    setPending({ field, on });
    setNote(undefined);
    void saveSwitch(info, field, on, computer, save, onFailure).then((result) => {
      if ('info' in result) {
        setSaved({ info: result.info, at: Date.now() });
        setNote({ text: 'Saved.', error: false });
        onSaved?.();
      } else {
        setNote({ text: result.error, error: true });
      }
      setPending(undefined);
    });
  }

  const toggle = (field: Field, label: string, off = false) => (
    <div className="spread">
      <span id={`${id}-${field}`}>{label}</span>
      <button
        type="button"
        role="switch"
        className="switch"
        aria-checked={value(field)}
        aria-labelledby={`${id}-${field}`}
        {...(pending?.field === field ? { 'aria-busy': true } : {})}
        disabled={pending !== undefined || off}
        onClick={() => change(field)}
      ><span className="switch-track" aria-hidden="true" /></button>
    </div>
  );

  return (
    <li className="computer-card">
      <div className="computer-head">
        <span className="row-text">
          <span className="row-title"><bdi>{info.name}</bdi> on {computer.label}</span>
          {info.version !== undefined && <span className="row-meta">{info.version}</span>}
        </span>
        <span className={`computer-state ${text.tone}`}>{text.word}</span>
      </div>
      {text.line !== undefined && <p className="hint-text">{text.line}</p>}
      {toggle('enabled', 'On')}
      {toggle('blocked', 'Buzz when blocked', !value('enabled'))}
      {toggle('done', 'Buzz when finished', !value('enabled'))}
      <div role="status" className="field-note">
        {note?.error === true ? <Notice tone="danger" title={note.text} /> : <span className="muted">{pending === undefined ? note?.text ?? '' : 'Saving…'}</span>}
      </div>
    </li>
  );
}

/** Settings → Integrations: only where a computer lists a provider. */
export function IntegrationsSection({ entries, providers = [], onFailure, onCheck, onProviderReload }: {
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** The providers' latest reads, for their state. */
  readonly providers?: ReadonlyArray<ProviderEntry> | undefined;
  readonly onFailure: (error: unknown, from?: Computer) => string;
  readonly onCheck?: (origin: string) => Promise<void>;
  readonly onProviderReload?: ((key: string) => Promise<void>) | undefined;
}) {
  const sources = providerSources(entries);
  if (sources.length === 0) return null;
  return (
    <section className="group stack" aria-labelledby="integrations-heading">
      <h2 id="integrations-heading" className="caps">Integrations</h2>
      <ul className="computer-list">
        {sources.map((source) => (
          <IntegrationRow
            key={`${source.computer.origin} ${source.info.id}`}
            source={source}
            entry={providers.find((entry) => entry.key === providerKey(source.computer.origin, source.info.id))}
            onFailure={onFailure}
            onSaved={() => {
              void onCheck?.(source.computer.origin).catch(() => undefined);
              void onProviderReload?.(providerKey(source.computer.origin, source.info.id)).catch(() => undefined);
            }}
          />
        ))}
      </ul>
    </section>
  );
}

export function Settings({
  settings, entries, providers, fontSize, onFontSize, onBack, onRenamed, onFailure, onLogout, onCheck, onProviderReload, onComputersChanged,
}: {
  /** Home's settings; undefined while home cannot be reached (only Computers and text size then). */
  readonly settings: HTTPSettingsResponse | undefined;
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** Providers (herdr) of every computer, read while Settings shows, for Integrations. */
  readonly providers?: ReadonlyArray<ProviderEntry> | undefined;
  readonly fontSize: number;
  readonly onFontSize: (size: number) => void;
  readonly onBack: () => void;
  readonly onRenamed: (device: HTTPDeviceSummary) => void;
  readonly onFailure: (error: unknown, from?: Computer) => string;
  readonly onLogout: () => Promise<void>;
  readonly onCheck: (origin: string) => Promise<void>;
  readonly onProviderReload?: ((key: string) => Promise<void>) | undefined;
  readonly onComputersChanged: () => void;
}) {
  const [logoutError, setLogoutError] = useState<string>();
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
    <main className="page screen settings-screen">
      <header className="screen-header">
        <h1 className="display">Settings</h1>
        <button type="button" className="link strong" onClick={onBack}>Done</button>
      </header>
      <ErrorBoundary>
        <Suspense fallback={<p role="status" className="muted">Loading computers…</p>}>
          <ComputersSection entries={entries} onCheck={onCheck} onChanged={onComputersChanged} onFailure={onFailure} />
        </Suspense>
      </ErrorBoundary>

      {settings === undefined && (
        <Notice tone="warning">Your home computer cannot be reached. Device name, notifications and log out need it.</Notice>
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
          <Button
            type="submit"
            className="secondary"
            busy={nameState.kind === 'saving'}
            disabled={nameProblem !== undefined || unchanged}
          >Save name</Button>
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

      <IntegrationsSection entries={entries} providers={providers} onFailure={onFailure} onCheck={onCheck} onProviderReload={onProviderReload} />

      <section className="group stack" aria-labelledby="build-heading">
        <h2 id="build-heading" className="caps">Build</h2>
        <dl className="facts">
          {settings !== undefined && <><dt>Host build</dt><dd>{settings.hostBuild}</dd></>}
          <dt>Web build</dt><dd>{CLIENT_BUILD}</dd>
        </dl>
      </section>

      {settings !== undefined && (
        <LogoutSection
          multi={multi}
          busy={busy}
          error={logoutError}
          onLogout={() => {
            setBusy(true);
            setLogoutError(undefined);
            onLogout().catch((failure: unknown) => {
              setLogoutError(failure instanceof Error ? failure.message : 'Logout failed');
              setBusy(false);
            });
          }}
        />
      )}
    </main>
  );
}

/** Log out; while it runs (each peer may take a few seconds), the button is off and says why. A failure shows here by the button, not at the top of the page, out of sight. */
export function LogoutSection({ multi, busy, error, onLogout }: {
  readonly multi: boolean;
  readonly busy: boolean;
  readonly error?: string | undefined;
  readonly onLogout: () => void;
}) {
  return (
    <section className="group stack" aria-labelledby="logout-heading">
      <h2 id="logout-heading" className="caps">Log out</h2>
      <p className="muted">
        {multi
          ? 'Signs this phone out of every computer. Running sessions keep running.'
          : 'Removes this browser\'s access. Running sessions keep running on your computer.'}
      </p>
      {error !== undefined && <Notice tone="danger" role="alert" title={error} />}
      <Button className="danger" busy={busy} onClick={onLogout}>Log out</Button>
      <p role="status" className="muted field-note">{busy ? 'Signing out…' : ''}</p>
    </section>
  );
}
