import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { api, client, CLIENT_BUILD, deviceLabelProblem } from '../api/http';
import type {
  HTTPDeviceSummary, HTTPProviderInfo, HTTPProviderNotifyState, HTTPProviderUpdateRequest, HTTPSessionCreateRequest, HTTPSessionMutationResponse,
  HTTPSettingsResponse,
} from '../generated/protocol';
import { disablePush, enablePush, pushEnabled, pushSupport, syncPeerPush, testPush, type PushSupport } from '../push';
import { storedComputers, type Computer } from '../computers';
import { checkAgain, hosted, type ComputerEntry, type HostedSession } from './allSessions';
import { ErrorBoundary, loadChunk } from './chunks';
import { GitHubRow, githubSources, readyProvider, type GitHubSource } from './GitHub';
import type { ProviderTap } from './Herdr';
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from './hooks';
import { NewSessionHelp } from './NewSession';
import { StartButton } from './ProviderManage';
import { INSTALL_COMMANDS, providerKey, providerSources, startInstall, type ProviderEntry, type ProviderSource } from './providers';
import { ComputerHead, initialPage, MAIN_PAGE, PAGE_KEY, pageFromHistory, shownPage, type SettingsPage } from './SettingsComputer';
import { Button, Notice } from './ui';

// Computers (and the add-computer sheet it opens) load only when Settings opens.
const computersChunk = () => loadChunk(() => import('./Computers'));
const ComputersSection = lazy(() => computersChunk().then((module) => ({ default: module.ComputersSection })));
const ComputerActions = lazy(() => computersChunk().then((module) => ({ default: module.ComputerActions })));

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

/** "No notifications from vps, pi.": the labels of the computers with these hosts. */
export function noPushFrom(hosts: ReadonlyArray<string>, computers: ReadonlyArray<Pick<Computer, 'host' | 'label'>> = storedComputers()): string {
  // Two computers can have one label: say it once.
  const labels = hosts.map((host) => computers.find((computer) => computer.host === host)?.label ?? host);
  return `No notifications from ${[...new Set(labels)].join(', ')}.`;
}

/** What the Notifications section knows about this phone's push: `enabled` is undefined until the browser answered, `missing` has the hosts of the peers that send none. */
export type PushState = { readonly support: PushSupport; readonly enabled: boolean | undefined; readonly missing: ReadonlyArray<string> };

/** The link of a card's "no buzz" line, by why this phone gets no buzz from that computer. All go to the Notifications section. */
export const BUZZ_LINKS = {
  off: 'Turn on notifications',
  homeScreen: 'Add to Home Screen',
  why: 'See why',
} as const;

/**
 * Why this phone gets no buzz from a computer, or undefined when it does (or
 * that is not known yet). `why`: a browser without push, or push is on at
 * home and this peer still sends none (Notifications names it).
 */
export function buzzGap(push: PushState | undefined, computer: Pick<Computer, 'home' | 'host'>): keyof typeof BUZZ_LINKS | undefined {
  if (push === undefined) return undefined;
  if (push.support !== 'available') return push.support === 'needs-home-screen' ? 'homeScreen' : 'why';
  if (push.enabled !== true) return push.enabled === false ? 'off' : undefined;
  return !computer.home && push.missing.includes(computer.host) ? 'why' : undefined;
}

/** Push on/off for this phone on every computer, plus a test send. */
export function NotificationSettings({ onFailure, onPush }: {
  readonly onFailure: (error: unknown) => string;
  /** Tells Settings what this section knows, for the Integrations cards. */
  readonly onPush?: (state: PushState) => void;
}) {
  const [support] = useState(pushSupport);
  // undefined while the browser is asked whether this phone is subscribed.
  const [enabled, setEnabled] = useState<boolean>();
  // What runs now: the switch, or a test send.
  const [busy, setBusy] = useState<'switch' | 'test'>();
  const [note, setNote] = useState<Note>();

  // The hosts of the computers that send no notifications to this phone (home is on).
  const [missing, setMissing] = useState<ReadonlyArray<string>>([]);

  useEffect(() => {
    if (support !== 'available') return undefined;
    let live = true;
    pushEnabled().catch(() => false).then((value) => {
      if (live) setEnabled(value);
      // Tries each peer again, so this also says which ones are still without.
      return value ? syncPeerPush() : [];
    }).then((hosts) => { if (live) setMissing(hosts); });
    return () => { live = false; };
  }, [support]);

  useEffect(() => { onPush?.({ support, enabled, missing }); }, [onPush, support, enabled, missing]);

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
    if (sent) void late.then((hosts) => { if (hosts.length > 0) setNote({ text: `Sent. ${noPushFrom(hosts)}`, error: true }); });
  }));

  return (
    <section id="notifications" className="group stack" aria-labelledby="push-heading" tabIndex={-1}>
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
        support === 'needs-home-screen' ? (
          // iOS gives a web app no way to add itself: these are the taps the user makes.
          <Notice tone="neutral" title="To get notifications on iPhone, add Termivoa to your Home Screen.">
            <p>iPhone sends notifications only to an app on the Home Screen, not to a Safari tab.</p>
            <ol className="steps-list">
              <li>In Safari, tap the Share button (the square with an arrow up).</li>
              <li>Tap <b>Add to Home Screen</b>, then <b>Add</b>.</li>
              <li>Open Termivoa from the new icon. If it asks, pair this phone again.</li>
              <li>Come back to Settings there and turn notifications on.</li>
            </ol>
          </Notice>
        ) : <p className="muted">This browser cannot show notifications. Try Chrome, Edge, Firefox or Safari.</p>
      )}
    </section>
  );
}

/** The state word and its one line for Settings → (computer) (docs/herdr-provider.md, section 8). */
export function integrationText(info: Pick<HTTPProviderInfo, 'name' | 'state'>, label: string, canStart = false): {
  readonly word: string;
  readonly tone: 'green' | 'amber' | 'muted';
  /** Absent for Running, and for Not installed (InstallCard has its own). */
  readonly line?: string;
} {
  switch (info.state) {
    case 'ready': return { word: 'Running', tone: 'green' };
    case 'notRunning': return { word: 'Not running', tone: 'amber', line: `${info.name} is not running on ${label}. Start it ${canStart ? '' : 'there '}to see its projects.` };
    case 'notInstalled': return { word: 'Not installed', tone: 'muted' };
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

/**
 * Install, start to end. The new session opens in Live only while Settings still shows (`shown`): a user who left during
 * the request stays where they are, and finds the session in the list. False: the computer has no project or shell.
 */
export async function installAndOpen(start: () => Promise<HostedSession | undefined>, shown: () => boolean, open: (session: HostedSession) => void): Promise<boolean> {
  const session = await start();
  if (session !== undefined && shown()) open(session);
  return session !== undefined;
}

/** The body of a card whose provider is not installed: no switches; Install where the official installer can work, else `pterm doctor`. */
export function InstallCard({ source, command, help, busy, error, onInstall, onCheck }: {
  readonly source: ProviderSource;
  /** What Install types (INSTALL_COMMANDS); undefined: this computer cannot run the installer. */
  readonly command: string | undefined;
  /** The computer has no project or shell for the session: New session's help. */
  readonly help?: HTTPSettingsResponse | undefined;
  readonly busy: 'install' | 'check' | undefined;
  readonly error: string | undefined;
  readonly onInstall: () => void;
  readonly onCheck: () => void;
}) {
  const { computer, info } = source;
  return (
    <>
      <p className="hint-text">
        {command === undefined
          ? <>Run <code>pterm doctor</code> on {computer.label}.</>
          : <>Opens a terminal on {computer.label} with <bdi>{info.name}</bdi>{'\'s install command. Press Enter there to run it.'}</>}
      </p>
      {help !== undefined && <NewSessionHelp settings={help} />}
      <div className="row">
        {command !== undefined && (
          <Button className="secondary" busy={busy === 'install'} busyLabel="Opening…" disabled={busy !== undefined} onClick={onInstall}>
            <span>Install <bdi>{info.name}</bdi></span>
          </Button>
        )}
        <Button className="link" busy={busy === 'check'} busyLabel="Checking…" disabled={busy !== undefined} onClick={onCheck}>Check again</Button>
      </div>
      <div role="status" className="field-note">{error !== undefined && <Notice tone="danger" title={error} />}</div>
    </>
  );
}

/** One provider on one computer: on or off, and which states buzz. Saved on that computer for every device. */
export function IntegrationRow({
  source, entry, settings, push, onFailure, onSaved, onInstall, onCheck, onNotifications, save = (body) => client(source.computer.origin).providerUpdate(source.info.id, body),
}: {
  readonly source: ProviderSource;
  /** The "no buzz" line's link: opens Notifications. */
  readonly onNotifications?: (() => void) | undefined;
  /** This phone's push state: a card whose buzz is on says when no buzz can reach this phone. */
  readonly push?: PushState | undefined;
  /** That computer's settings: the project and shell Install starts its session with. */
  readonly settings?: HTTPSettingsResponse | undefined;
  /** Install made a session: open it in Live and type `command` there, without Enter. */
  readonly onInstall?: ((session: HostedSession, command: string) => void) | undefined;
  /** Reads that computer again, settings included (Check again). */
  readonly onCheck?: (() => Promise<void>) | undefined;
  /** The provider's latest read on that computer: its `state` is newer than the settings' (docs/herdr-provider.md, 4.5). */
  readonly entry?: Pick<ProviderEntry, 'status'> | undefined;
  readonly onFailure: (error: unknown, from: Computer) => string;
  /** Reads the computer's settings and the provider again, so the rest of the app follows; never rejects. */
  readonly onSaved?: () => Promise<void>;
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
  const canStart = source.manage && state === 'notRunning' && info.canStart === true;
  const text = integrationText({ name: info.name, state }, computer.label, canStart);
  const id = `integration-${computer.host}-${info.id}`.replace(/[^A-Za-z0-9_-]/gu, '-');
  const command = source.manage && info.canInstall === true && settings !== undefined && onInstall !== undefined ? INSTALL_COMMANDS[info.id] : undefined;
  const [working, setWorking] = useState<'install' | 'check'>();
  const [needsProject, setNeedsProject] = useState(false);
  // One session per tap, even if two taps land before the re-render.
  const running = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  function act(what: 'install' | 'check', work: () => Promise<string | undefined>) {
    if (running.current) return;
    running.current = true;
    setWorking(what);
    setNote(undefined);
    void work().then((problem) => {
      if (problem !== undefined) setNote({ text: problem, error: true });
      setWorking(undefined);
      running.current = false;
    });
  }

  const install = () => act('install', async () => {
    if (settings === undefined || command === undefined) return undefined;
    try {
      setNeedsProject(!await installAndOpen(() => startInstall(computer, settings), () => mounted.current, (session) => onInstall?.(session, command)));
      return undefined;
    } catch (failure) {
      return onFailure(failure, computer);
    }
  });
  const check = () => act('check', () => checkAgain(onCheck?.() ?? Promise.resolve(), (failure) => onFailure(failure, computer)));
  const gap = buzzGap(push, computer);
  const value = (field: Field) => (pending?.field === field ? pending.on : field === 'enabled' ? info.enabled : info.notify.includes(field));

  function change(field: Field) {
    const on = !value(field);
    setPending({ field, on });
    setNote(undefined);
    void saveSwitch(info, field, on, computer, save, onFailure).then((result) => {
      if ('info' in result) {
        setSaved({ info: result.info, at: Date.now() });
        setNote({ text: 'Saved.', error: false });
        void onSaved?.();
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
          {/* One inner span: .row-title is a flex row, and its children would be laid out as separate items. */}
          <span className="row-title"><span><bdi>{info.name}</bdi> on {computer.label}</span></span>
          {info.version !== undefined && <span className="row-meta">{info.version}</span>}
        </span>
        <span className={`computer-state ${text.tone}`}>{text.word}</span>
      </div>
      {text.line !== undefined && <p className="hint-text">{text.line}</p>}
      {state === 'notInstalled' ? (
        <InstallCard
          source={source}
          command={command}
          help={needsProject ? settings : undefined}
          busy={working}
          error={note?.error === true ? note.text : undefined}
          onInstall={install}
          onCheck={check}
        />
      ) : (
        <>
          {canStart && <StartButton source={source} onStarted={async () => { await onSaved?.(); }} onFailure={onFailure} />}
          {toggle('enabled', 'On')}
          {toggle('blocked', 'Buzz when blocked', !value('enabled'))}
          {toggle('done', 'Buzz when finished', !value('enabled'))}
          {/* The switches are that computer's, for every phone: they can be on while this phone gets nothing. */}
          {gap !== undefined && value('enabled') && (value('blocked') || value('done')) && (
            <Notice
              tone="warning"
              role="status"
              title="This phone gets no buzz yet."
              action={<button type="button" className="link" onClick={onNotifications}>{BUZZ_LINKS[gap]}</button>}
            />
          )}
          <div role="status" className="field-note">
            {note?.error === true ? <Notice tone="danger" title={note.text} /> : <span className="muted">{pending === undefined ? note?.text ?? '' : 'Saving…'}</span>}
          </div>
        </>
      )}
    </li>
  );
}

/**
 * One computer's page (docs/ui-revamp.md, section 5): each provider it lists (herdr) with its switches, GitHub where
 * it offers it, and This computer. Back goes to the main page.
 */
export function ComputerPage({
  entry, providers = [], push, onBack, onFailure, onCheck, onProviderReload, onInstall, onOpenSession, onProviderOpened, onNotifications, onComputersChanged, onRemoved,
}: {
  readonly entry: ComputerEntry;
  readonly providers?: ReadonlyArray<ProviderEntry> | undefined;
  readonly push?: PushState | undefined;
  readonly onBack: () => void;
  readonly onFailure: (error: unknown, from?: Computer) => string;
  readonly onCheck: (origin: string) => Promise<void>;
  readonly onProviderReload?: ((key: string) => Promise<void>) | undefined;
  readonly onInstall?: ((session: HostedSession, command: string) => void) | undefined;
  readonly onOpenSession?: ((session: HostedSession) => void) | undefined;
  readonly onProviderOpened?: ((tap: ProviderTap, key: string) => void) | undefined;
  readonly onNotifications?: (() => void) | undefined;
  readonly onComputersChanged: () => void;
  readonly onRemoved: (text: string) => void;
}) {
  const { origin } = entry.computer;
  const settings = entry.status.kind === 'ok' ? entry.status.settings : undefined;
  const sources = providerSources([entry]);
  // An open GitHub sheet keeps its row (and the sheet) through a failed settings read.
  const keptGitHub = useRef(new Map<string, GitHubSource>());
  const [sheetOpen, setSheetOpen] = useState(false);
  const github = githubSources([entry], keptGitHub.current, sheetOpen ? new Set([origin]) : new Set());
  return (
    <>
      <ComputerHead entry={entry} now={Date.now()} onBack={onBack} />
      {sources.map((source) => (
        <section key={source.info.id} className="group stack" aria-label={source.info.name}>
          <h2 className="caps">{source.info.name}</h2>
          <ul className="computer-list sc-block">
            <IntegrationRow
              source={source}
              entry={providers.find((one) => one.key === providerKey(origin, source.info.id))}
              settings={settings}
              push={push}
              onInstall={onInstall}
              onCheck={() => onCheck(origin)}
              onNotifications={onNotifications}
              onFailure={onFailure}
              onSaved={async () => {
                await Promise.all([
                  onCheck(origin).catch(() => undefined),
                  onProviderReload?.(providerKey(origin, source.info.id)).catch(() => undefined),
                ]);
              }}
            />
          </ul>
        </section>
      ))}
      {github.map((source) => (
        <section key="github" className="group stack" aria-label="GitHub">
          <h2 className="caps">GitHub</h2>
          <ul className="computer-list sc-block">
            <GitHubRow
              source={source}
              provider={readyProvider(providers, origin)}
              handlers={{
                onFailure,
                onChanged: () => onCheck(origin),
                onOpenSession,
                onTypeIn: onInstall,
                onProviderOpened,
                onProviderReload,
              }}
              onSheet={setSheetOpen}
            />
          </ul>
        </section>
      ))}
      <ErrorBoundary>
        <Suspense fallback={<p role="status" className="muted">Loading…</p>}>
          <ComputerActions entry={entry} projects={settings?.projects.length} onCheck={onCheck} onChanged={onComputersChanged} onFailure={onFailure} onRemoved={onRemoved} />
        </Suspense>
      </ErrorBoundary>
    </>
  );
}

/** This phone's name on home (`pterm devices`): today's form, opened from the Name row. */
export function DeviceNameForm({ current, onRenamed, onFailure }: {
  readonly current: string;
  readonly onRenamed: (device: HTTPDeviceSummary) => void;
  readonly onFailure: (error: unknown) => string;
}) {
  // Follows home's label (home may answer after this screen opened) until the user types.
  const [draft, setDraft] = useState<string>();
  const name = draft ?? current;
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
    <form
      id="device-name-form"
      className="stack sc-inline"
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
  );
}

/** "On", "Off", or nothing while the browser has not answered: the Notifications row's value. */
export function pushWord(push: PushState | undefined): 'On' | 'Off' | '' {
  if (push === undefined) return '';
  if (push.support !== 'available') return 'Off';
  return push.enabled === undefined ? '' : push.enabled ? 'On' : 'Off';
}

export function Settings({
  settings, section, entries, providers, fontSize, onFontSize, gestures, onGestures, onBack, onRenamed, onFailure, onLogout, onCheck, onProviderReload, onInstall, onOpenSession, onProviderOpened, onComputersChanged,
}: {
  /** Home's settings; undefined while home cannot be reached (only Computers, text size and build then). */
  readonly settings: HTTPSettingsResponse | undefined;
  /** Opens at Notifications, or with Add a computer open. */
  readonly section?: 'notifications' | 'add-computer';
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** Providers (herdr) of every computer, read while Settings shows. */
  readonly providers?: ReadonlyArray<ProviderEntry> | undefined;
  readonly fontSize: number;
  readonly onFontSize: (size: number) => void;
  /** The "Terminal gestures" switch, kept on this phone. */
  readonly gestures: boolean;
  readonly onGestures: (on: boolean) => void;
  readonly onBack: () => void;
  readonly onRenamed: (device: HTTPDeviceSummary) => void;
  readonly onFailure: (error: unknown, from?: Computer) => string;
  readonly onLogout: () => Promise<void>;
  readonly onCheck: (origin: string) => Promise<void>;
  readonly onProviderReload?: ((key: string) => Promise<void>) | undefined;
  /** Install herdr made a session on a computer: open it in Live with the install command typed, not run. */
  readonly onInstall?: ((session: HostedSession, command: string) => void) | undefined;
  /** GitHub's New session made a session: open it in Live. */
  readonly onOpenSession?: ((session: HostedSession) => void) | undefined;
  /** GitHub's New workspace in herdr opened a terminal; `key` is the provider read's. */
  readonly onProviderOpened?: ((tap: ProviderTap, key: string) => void) | undefined;
  readonly onComputersChanged: () => void;
}) {
  const [logoutError, setLogoutError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [push, setPush] = useState<PushState>();
  const [naming, setNaming] = useState(false);
  const [note, setNote] = useState<{ readonly text: string; readonly error: boolean }>();
  const [wanted, setWanted] = useState<SettingsPage>(() => initialPage(section, globalThis.history?.state));
  const page = shownPage(wanted, entries, settings !== undefined);
  const main = useRef<HTMLElement>(null);
  const multi = entries.length > 1;

  // The phone's Back goes from a computer page (or Notifications) to the main page. App's own popstate reads the
  // address, which stays /settings: it shows Settings again, and this keeps the page of the entry.
  useEffect(() => {
    const pop = () => setWanted(pageFromHistory(window.history.state));
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
  }, []);
  // A new level starts at its top, with focus on its title (or the Notifications section).
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    main.current?.scrollTo?.(0, 0);
    main.current?.querySelector<HTMLElement>(page.kind === 'notifications' ? '#notifications' : 'h1')?.focus({ preventScroll: true });
  }, [page.kind, page.kind === 'computer' ? page.origin : '']);

  const go = (next: SettingsPage) => {
    window.history.pushState({ [PAGE_KEY]: next }, '', window.location.pathname + window.location.search);
    setWanted(next);
  };
  const back = () => {
    if (pageFromHistory(window.history.state).kind === 'main') setWanted(MAIN_PAGE);
    else window.history.back();
  };
  const openNotifications = settings === undefined ? undefined : () => go({ kind: 'notifications' });
  const word = pushWord(push);
  const computer = page.kind === 'computer' ? entries.find((entry) => entry.computer.origin === page.origin) : undefined;

  return (
    <main ref={main} className="page screen settings-screen">
      {computer !== undefined && (
        <ComputerPage
          entry={computer}
          providers={providers}
          // Without home there is no Notifications page to point at.
          push={settings === undefined ? undefined : push}
          onBack={back}
          onFailure={onFailure}
          onCheck={onCheck}
          onProviderReload={onProviderReload}
          onInstall={onInstall}
          onOpenSession={onOpenSession}
          onProviderOpened={onProviderOpened}
          onNotifications={openNotifications}
          onComputersChanged={onComputersChanged}
          onRemoved={(text) => { setNote({ text, error: false }); back(); }}
        />
      )}
      {page.kind === 'notifications' && (
        <header className="sc-page-head">
          <button type="button" className="link accent sc-back" onClick={back}>‹ Settings</button>
        </header>
      )}
      {page.kind === 'main' && (
        <>
          <header className="screen-header">
            <h1 className="display" tabIndex={-1}>Settings</h1>
            <button type="button" className="link strong" onClick={onBack}>Done</button>
          </header>
          <ErrorBoundary>
            <Suspense fallback={<p role="status" className="muted">Loading computers…</p>}>
              <ComputersSection
                entries={entries}
                providers={providers}
                adding={section === 'add-computer'}
                note={note}
                onCheck={onCheck}
                onFailure={onFailure}
                onOpen={(origin) => { setNote(undefined); go({ kind: 'computer', origin }); }}
              />
            </Suspense>
          </ErrorBoundary>

          <section className="group stack" aria-labelledby="phone-heading">
            <h2 id="phone-heading" className="caps">This phone</h2>
            {settings === undefined && (
              <Notice tone="warning">Your home computer cannot be reached. Device name, notifications and log out need it.</Notice>
            )}
            <ul className="sc-list">
              {settings !== undefined && (
                <li className="sc-item">
                  <button type="button" className="sc-row" aria-expanded={naming} aria-controls="device-name-form" onClick={() => setNaming(!naming)}>
                    <span>Name</span><span className="sc-value">{settings.device.label} ›</span>
                  </button>
                  {naming && <DeviceNameForm current={settings.device.label} onRenamed={onRenamed} onFailure={onFailure} />}
                </li>
              )}
              {openNotifications !== undefined && (
                <li className="sc-item">
                  <button type="button" className="sc-row" onClick={openNotifications}>
                    <span>Notifications</span><span className={word === 'On' ? 'sc-value green' : 'sc-value'}>{word} ›</span>
                  </button>
                </li>
              )}
              <li className="sc-row" role="group" aria-labelledby="font-heading">
                <span id="font-heading">Terminal text size</span>
                <span className="font-row">
                  <button type="button" className="key" aria-label="Smaller text" disabled={fontSize <= MIN_FONT_SIZE}
                    onClick={() => onFontSize(fontSize - 1)}>A−</button>
                  <output aria-live="polite" className="font-size">{fontSize} px</output>
                  <button type="button" className="key" aria-label="Larger text" disabled={fontSize >= MAX_FONT_SIZE}
                    onClick={() => onFontSize(fontSize + 1)}>A+</button>
                </span>
              </li>
              <li className="sc-row">
                <span id="gestures-label">Terminal gestures</span>
                <button
                  type="button"
                  role="switch"
                  className="switch"
                  aria-checked={gestures}
                  aria-labelledby="gestures-label"
                  onClick={() => onGestures(!gestures)}
                ><span className="switch-track" aria-hidden="true" /></button>
              </li>
              <li className="sc-row">
                <span id="build-heading">Build</span>
                <dl className="facts sc-facts" aria-labelledby="build-heading">
                  <dt>Web build</dt><dd>{CLIENT_BUILD}</dd>
                  {settings !== undefined && <><dt>Host build</dt><dd>{settings.hostBuild}</dd></>}
                </dl>
              </li>
            </ul>
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
        </>
      )}
      {/* Always there, so the rows know whether push is on; it shows on its own page. */}
      {settings !== undefined && (
        <div className="stack" hidden={page.kind !== 'notifications'}>
          <NotificationSettings onFailure={onFailure} onPush={setPush} />
        </div>
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
      <p className="hint-text">
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
