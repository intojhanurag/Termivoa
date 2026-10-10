import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { ApiError, client } from '../api/http';
import type { Computer } from '../computers';
import type {
  HTTPGitHubInfo, HTTPGitHubProblem, HTTPGitHubRepo, HTTPGitHubReposResponse, HTTPGitHubResponse,
  HTTPGitHubSetup, HTTPGitHubSetupProblem, HTTPGitHubState, HTTPSettingsResponse,
} from '../generated/protocol';
import { checkAgain, hosted, type ComputerEntry, type HostedSession } from './allSessions';
import type { ProviderTap } from './Herdr';
import { NewSessionHelp } from './NewSession';
import { CHANGED, githubShown, manageStep, NoteLine, type ManageNote } from './ProviderManage';
import { providerState, startInstall, type ProviderEntry } from './providers';
import { age } from './Sessions';
import { Sheet, SheetHeader } from './Sheet';
import { Button, ConfirmPair, Notice } from './ui';

/*
 * GitHub on each computer (docs/github-integration.md, sections 2, 6 and 9): the Settings row, the Connect sheet, the
 * repository list and the set-up. The phone sends only actions and `owner/name`; every address it opens is fixed here
 * (the app's slug is checked again before it goes into one). Each problem is one line and one action.
 */

/** GitHub's device page: fixed in the app, never taken from an answer. */
export const DEVICE_URL = 'https://github.com/login/device';
/** Settings → Applications → Authorized GitHub Apps. */
export const AUTHORIZED_APPS_URL = 'https://github.com/settings/applications';
/** The answers are read this often while a sheet waits for GitHub or a clone. */
export const POLL_MS = 2_000;
/** From this size a repository shows its size in amber, and Set up asks first. */
export const BIG_KB = 1024 * 1024;

const APP_SLUG = /^[a-z0-9][a-z0-9-]{0,33}$/u;

/** Where the user chooses repositories for the app; undefined when the slug is not one the schema allows. */
export const installUrl = (app: string | undefined): string | undefined =>
  (app !== undefined && APP_SLUG.test(app) ? `https://github.com/apps/${app}/installations/new` : undefined);

/** The GitHub row of a computer that offers it (revision 7, a build with the app, not Windows). */
export const githubInfo = (settings: Pick<HTTPSettingsResponse, 'protocolRevision' | 'github'> | undefined): HTTPGitHubInfo | undefined =>
  (settings !== undefined && githubShown(settings) ? settings.github : undefined);

export interface GitHubSource {
  readonly computer: Computer;
  readonly settings: HTTPSettingsResponse;
  readonly info: HTTPGitHubInfo;
}

/**
 * Every computer with a GitHub row, in the computers' order. `kept` holds each row's last good read; a computer whose
 * sheet is `open` keeps it through a read that fails, so one failed settings read never closes a code or a clone on
 * screen (the pattern of keptSettings in Sessions.tsx). `kept` is updated in place.
 */
export function githubSources(entries: ReadonlyArray<ComputerEntry>, kept = new Map<string, GitHubSource>(), open: ReadonlySet<string> = new Set()): GitHubSource[] {
  return entries.flatMap((entry) => {
    const { origin } = entry.computer;
    const info = entry.status.kind === 'ok' ? githubInfo(entry.status.settings) : undefined;
    if (entry.status.kind === 'ok' && info !== undefined) {
      const source = { computer: entry.computer, settings: entry.status.settings, info };
      kept.set(origin, source);
      return [source];
    }
    const last = entry.status.kind !== 'ok' && open.has(origin) ? kept.get(origin) : undefined;
    if (last === undefined) kept.delete(origin);
    return last === undefined ? [] : [last];
  });
}

/** How often a row reads again while a clone runs: 12 reads a minute, under the computer's limit of 60. */
export const ROW_CLONE_READ_MS = 5_000;
/** Install git types this, not run: macOS's installer, else Debian's or Ubuntu's (the settings do not say the platform). */
export const INSTALL_GIT = 'if [ "$(uname)" = Darwin ]; then xcode-select --install; else sudo apt-get install -y git; fi';
/** A device code lives 15 minutes: the time left never shows more, whatever the clocks say. */
export const CODE_MS = 15 * 60_000;
/** Polls that the app cannot read, in a row, before the sheet says so. */
export const UNREADABLE_AFTER = 3;
export const unreadableText = (label: string): string => `${label} answered something this app cannot read. Update Termivoa on ${label}.`;

/**
 * A new session in that project, with the computer's first shell, as New session starts one. Undefined when the
 * computer has no shell: New session's own help says what to do.
 */
export async function newSessionIn(computer: Computer, settings: Pick<HTTPSettingsResponse, 'shellProfiles'> | undefined, projectId: string): Promise<HostedSession | undefined> {
  const shell = settings?.shellProfiles[0];
  if (shell === undefined) return undefined;
  const made = await client(computer.origin).createSession({ operationId: crypto.randomUUID(), projectId, shellProfileId: shell.id });
  return hosted(made.session, computer);
}

export const repoName = (repo: string): string => repo.slice(repo.indexOf('/') + 1);
const repoOwner = (repo: string): string => repo.slice(0, repo.indexOf('/'));
export const isBig = (repo: Pick<HTTPGitHubRepo, 'sizeKb'>): boolean => repo.sizeKb >= BIG_KB;
export const sizeText = (kb: number): string => `${(kb / BIG_KB).toFixed(1)} GB`;

/** "2 h ago", "yesterday", "just now"; undefined without a date. */
export function pushedText(at: number | undefined, now: number): string | undefined {
  if (at === undefined) return undefined;
  const text = age(new Date(at).toISOString(), now);
  if (text === '') return undefined;
  return text === 'now' ? 'just now' : text === 'yesterday' ? text : `${text} ago`;
}

/** "octo-demo · private · fork · 2 h ago" (the size is drawn apart, in amber). */
export function repoMeta(repo: HTTPGitHubRepo, now: number): string {
  const when = pushedText(repo.pushedAt, now);
  return [repoOwner(repo.repo), repo.private ? 'private' : 'public', ...(repo.fork ? ['fork'] : []), ...(repo.archived ? ['archived'] : []), ...(when === undefined ? [] : [when])].join(' · ');
}

/** Search by name and owner, in memory. */
export function filterRepos<Row extends Pick<HTTPGitHubRepo, 'repo'>>(repos: ReadonlyArray<Row>, query: string): ReadonlyArray<Row> {
  const wanted = query.trim().toLowerCase();
  return wanted === '' ? repos : repos.filter((repo) => repo.repo.toLowerCase().includes(wanted));
}

/** "4:05" for a span in milliseconds. */
export function clock(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

export const reachText = (label: string): string => `${label} cannot reach GitHub.`;
export const endedText = (label: string): string => `GitHub is not connected on ${label} now.`;
export const OTHER_PROGRAM = 'Another program used this login. Revoke Termivoa on GitHub, then connect again.';

/** The line of a Connect that ended, and its one action. */
export function connectProblem(problem: HTTPGitHubProblem, label: string): { readonly text: string; readonly action?: string } {
  switch (problem) {
    case 'codeExpired': return { text: 'The code ran out.', action: 'Get a new code' };
    case 'denied': return { text: 'GitHub did not connect.', action: 'Try again' };
    case 'gitHubDown': return { text: reachText(label), action: 'Retry' };
    case 'ended': return { text: endedText(label), action: 'Connect' };
    case 'otherProgram': return { text: OTHER_PROGRAM };
  }
}

/** The line of a set-up that failed, and its one action. */
export function setupProblem(problem: HTTPGitHubSetupProblem | undefined, name: string, label: string): { readonly text: string; readonly action?: 'Try again' | 'Retry' | 'Connect' } {
  switch (problem) {
    case 'nameTaken': return { text: `Another folder named ${name} is on ${label}.` };
    case 'badName': return { text: 'Termivoa cannot set up a repository with this name.' };
    case 'noSpace': return { text: `${label} has too little free space for this repository.` };
    case 'tooManyProjects': return { text: `${label} has the most projects Termivoa keeps (200).` };
    case 'notFound': return { text: 'That repository is not on GitHub under this name now.' };
    case 'timeout': return { text: `The clone took more than 30 minutes and stopped. Nothing was left on ${label}.`, action: 'Try again' };
    case 'gitHubDown': return { text: reachText(label), action: 'Retry' };
    case 'notConnected': return { text: endedText(label), action: 'Connect' };
    // cloneFailed, stopped, and a word this app does not know yet.
    default: return { text: `The clone stopped. Nothing was left on ${label}.`, action: 'Try again' };
  }
}

/**
 * Copies the code in the tap that also opens GitHub: no await comes before the link's own navigation (iOS allows
 * the copy only inside the tap). A copy that fails is fine: the code stays on screen.
 */
export function copyCode(code: string, clipboard: Pick<Clipboard, 'writeText'> | undefined = globalThis.navigator?.clipboard): void {
  void clipboard?.writeText(code).catch(() => undefined);
}

/** A link styled as the bone button, opened in a new tab. */
function OutLink({ href, className = 'gh-button', onClick, children }: {
  readonly href: string;
  readonly className?: string;
  readonly onClick?: () => void;
  readonly children: ReactNode;
}) {
  return <a className={className} href={href} target="_blank" rel="noopener noreferrer" {...(onClick === undefined ? {} : { onClick })}>{children}</a>;
}

/** Ticks once a second while `on`. */
function useNow(on: boolean): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!on) return undefined;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [on]);
  return now;
}

/** Calls `then` each time the page shows again (the user comes back from GitHub). */
function useVisible(then: () => void): void {
  const latest = useRef(then);
  latest.current = then;
  useEffect(() => {
    const seen = () => { if (document.visibilityState === 'visible') latest.current(); };
    document.addEventListener('visibilitychange', seen);
    return () => document.removeEventListener('visibilitychange', seen);
  }, []);
}

/** Calls `read` every POLL_MS while `on` and the page shows, and at once when it shows again (back from GitHub). */
function usePoll(on: boolean, read: () => void): void {
  const latest = useRef(read);
  latest.current = read;
  useEffect(() => {
    if (!on) return undefined;
    const tick = () => { if (globalThis.document?.visibilityState !== 'hidden') latest.current(); };
    const timer = setInterval(tick, POLL_MS);
    document.addEventListener('visibilitychange', tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, [on]);
}

// ---------------------------------------------------------------- Settings row

type RowNote = { readonly kind: 'disconnected'; readonly revokeFailed?: boolean } | { readonly kind: 'error'; readonly text: string };
type RowBusy = 'disconnect' | 'check' | 'install' | 'session';

/** The row of one computer in Settings → (computer). State comes from props, so each state renders on its own. */
export function GitHubRowView({
  label, state, login, mine, problem, setup, confirming = false, busy, note, help, children,
  onConnect, onContinue, onSetUp, onDisconnect, onConfirm, onCancel, onCheck, onRetry, onInstall, onShow, onNewSession,
}: {
  readonly label: string;
  readonly state: HTTPGitHubState;
  readonly login?: string | undefined;
  /** This phone started the Connect that runs (the answer has its code). */
  readonly mine?: boolean;
  readonly problem?: HTTPGitHubProblem | undefined;
  /** A set-up that runs, or one that ended in the last 10 minutes (the host keeps it that long). */
  readonly setup?: HTTPGitHubSetup | undefined;
  readonly confirming?: boolean;
  readonly busy?: RowBusy | undefined;
  readonly note?: RowNote | undefined;
  /** Install git or New session found no project or shell there: New session's own help. */
  readonly help?: HTTPSettingsResponse | undefined;
  /** The row's sheet, kept inside its list item. */
  readonly children?: ReactNode;
  readonly onConnect: () => void;
  /** Opens the Connect sheet where it is: the code, or step 2. */
  readonly onContinue: () => void;
  readonly onSetUp: () => void;
  readonly onDisconnect: () => void;
  readonly onConfirm: () => void;
  readonly onCancel: () => void;
  readonly onCheck: () => void;
  readonly onRetry: () => void;
  /** Absent: this screen cannot open a terminal, so no Install git. */
  readonly onInstall?: (() => void) | undefined;
  /** Opens the sheet at the set-up that runs. */
  readonly onShow?: () => void;
  readonly onNewSession?: () => void;
}) {
  const confirmId = useId();
  const meta = state === 'ready' || state === 'needsInstall' ? login
    : state === 'notConnected' ? 'not connected'
      : state === 'noGit' ? `git is not on ${label}`
        : mine === true ? 'Connecting…' : 'Connecting from another phone…';
  const word = state === 'ready' ? { text: 'Connected', tone: 'green' }
    : state === 'noGit' ? { text: 'git missing', tone: 'amber' }
      : state === 'needsInstall' ? { text: 'Signed in', tone: 'amber' } : undefined;
  const off = busy !== undefined;
  const name = setup === undefined ? '' : repoName(setup.repo);
  return (
    <li className="computer-card">
      <div className="computer-head gh-head">
        <span className="row-text">
          <span className="row-title"><span>GitHub on {label}</span></span>
          {meta !== undefined && <span className="row-meta">{meta}</span>}
        </span>
        {word !== undefined && <span className={`computer-state ${word.tone}`}>{word.text}</span>}
        {state === 'notConnected' && <button type="button" className="primary compact" onClick={onConnect}>Connect</button>}
        {state === 'connecting' && mine === true && <button type="button" className="primary compact" onClick={onContinue}>Continue</button>}
      </div>
      {state === 'ready' && setup?.phase === 'cloning' && onShow !== undefined && (
        <p className="gh-row-line"><span>Setting up <bdi>{name}</bdi>…</span><button type="button" className="link accent" onClick={onShow}>Show</button></p>
      )}
      {state === 'ready' && setup?.phase === 'finished' && setup.projectId !== undefined && onNewSession !== undefined && (
        <p className="gh-row-line">
          <span><bdi>{name}</bdi> is ready.</span>
          <Button className="link accent" busy={busy === 'session'} busyLabel="Opening…" disabled={off} onClick={onNewSession}>New session</Button>
        </p>
      )}
      {!confirming && state === 'ready' && (
        <div className="gh-links">
          <button type="button" className="link accent" disabled={off} onClick={onSetUp}>Set up a repository</button>
          <button type="button" className="link" disabled={off} onClick={onDisconnect}>Disconnect</button>
        </div>
      )}
      {state === 'needsInstall' && (
        <div className="gh-links"><button type="button" className="link accent" onClick={onContinue}>Choose on GitHub</button></div>
      )}
      {state === 'noGit' && (
        <div className="gh-links">
          {onInstall !== undefined && <Button className="link accent" busy={busy === 'install'} busyLabel="Opening…" disabled={off} onClick={onInstall}>Install git</Button>}
          <Button className="link" busy={busy === 'check'} busyLabel="Checking…" disabled={off} onClick={onCheck}>Check again</Button>
        </div>
      )}
      {help !== undefined && <NewSessionHelp settings={help} />}
      {problem === 'otherProgram' && (
        <p className="hint-text">Another program used this login. <OutLink className="gh-inline" href={AUTHORIZED_APPS_URL}>Revoke Termivoa on GitHub</OutLink>, then connect again.</p>
      )}
      {problem === 'ended' && state === 'notConnected' && <p className="hint-text">{endedText(label)}</p>}
      {problem === 'gitHubDown' && (
        <Notice tone="warning" title={reachText(label)} action={<button type="button" className="link" onClick={onRetry}>Retry</button>} />
      )}
      {confirming && (
        <div role="alertdialog" aria-labelledby={confirmId} className="confirm stack">
          <p id={confirmId}>Disconnect GitHub on {label}?</p>
          <ConfirmPair confirm="Disconnect" busy={busy === 'disconnect'} busyLabel="Disconnecting…" onCancel={onCancel} onConfirm={onConfirm} />
        </div>
      )}
      <div role="status" className="field-note">
        {note?.kind === 'disconnected' && <span className="muted">GitHub is disconnected on {label}.</span>}
        {note?.kind === 'disconnected' && note.revokeFailed === true && <p className="hint-text"><OutLink className="gh-inline" href={AUTHORIZED_APPS_URL}>Also remove Termivoa on GitHub.</OutLink></p>}
        {note?.kind === 'error' && <Notice tone="danger" title={note.text} />}
      </div>
      {children}
    </li>
  );
}

/** What a GitHub sheet needs from the screen that opens it. */
export interface GitHubHandlers {
  /** Words a failure of this computer. */
  readonly onFailure: (error: unknown, from: Computer) => string;
  /** Reads that computer's settings again (a new project, a new login); rejects when that read failed. */
  readonly onChanged: () => Promise<void>;
  /** New session made a session: open it in Live. */
  readonly onOpenSession?: ((session: HostedSession) => void) | undefined;
  /** Install git made a session: open it in Live with the command typed, not run. Absent: no Install git. */
  readonly onTypeIn?: ((session: HostedSession, command: string) => void) | undefined;
  /** New workspace in herdr opened a terminal. `key` is the provider read's. */
  readonly onProviderOpened?: ((tap: ProviderTap, key: string) => void) | undefined;
  /** Reads a provider's list again; never rejects. */
  readonly onProviderReload?: ((key: string) => Promise<void>) | undefined;
}

/** The provider (herdr) of that computer when New workspace can run there. */
export function readyProvider(providers: ReadonlyArray<ProviderEntry>, origin: string): ProviderEntry | undefined {
  return providers.find((entry) => entry.computer.origin === origin && entry.manage && providerState(entry) === 'ready');
}

/** One computer's row and its sheet. */
export function GitHubRow({ source, provider, handlers, onSheet }: {
  readonly source: GitHubSource;
  readonly provider?: ProviderEntry | undefined;
  readonly handlers: GitHubHandlers;
  /** Says when the row's sheet opens and closes, so the row stays through a failed settings read. */
  readonly onSheet?: (open: boolean) => void;
}) {
  const { computer, info, settings } = source;
  // The row's own read (once on open, and when the page shows again): the problem line, a set-up, and whether this
  // phone started the Connect. Settings has only state and login.
  const [live, setLive] = useState<HTTPGitHubResponse>();
  const [sheet, setSheetState] = useState<'connect' | 'resume' | 'list'>();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState<RowBusy>();
  const [note, setNote] = useState<RowNote>();
  const [help, setHelp] = useState<HTTPSettingsResponse>();
  const read = () => client(computer.origin).github().then(setLive, () => undefined);
  useEffect(() => { void read(); }, [computer.origin, info.state]);
  useVisible(() => { void read(); });
  // While a clone runs (and no sheet shows it), the row reads again every 5 s, so "Setting up" turns into "is ready".
  const cloning = live?.setup?.phase === 'cloning' && sheet === undefined;
  useEffect(() => {
    if (!cloning) return undefined;
    const timer = window.setInterval(() => { if (document.visibilityState === 'visible') void read(); }, ROW_CLONE_READ_MS);
    return () => window.clearInterval(timer);
  }, [cloning, computer.origin]);
  const setSheet = (next: typeof sheet) => {
    setSheetState(next);
    onSheet?.(next !== undefined);
  };
  const words = (error: unknown) => handlers.onFailure(error, computer);

  function run(what: RowBusy, work: () => Promise<void>) {
    if (busy !== undefined) return;
    setBusy(what);
    setNote(undefined);
    setHelp(undefined);
    work().catch((error: unknown) => setNote({ kind: 'error', text: words(error) })).finally(() => setBusy(undefined));
  }

  const state = live?.state ?? info.state;
  const disconnect = () => run('disconnect', async () => {
    try {
      const answer = await client(computer.origin).githubAction({ action: 'disconnect' });
      setLive(answer);
      setNote({ kind: 'disconnected', revokeFailed: answer.revokeFailed === true });
      void handlers.onChanged().catch(() => undefined);
    } finally {
      setConfirming(false);
    }
  });
  const check = () => run('check', async () => {
    const problem = await checkAgain(handlers.onChanged(), words);
    if (problem !== undefined) setNote({ kind: 'error', text: problem });
    await read();
  });
  const { onTypeIn } = handlers;
  const install = onTypeIn === undefined ? undefined : () => run('install', async () => {
    const session = await startInstall(computer, settings);
    if (session === undefined) setHelp(settings);
    else onTypeIn(session, INSTALL_GIT);
  });
  const { onOpenSession } = handlers;
  const projectId = live?.setup?.projectId;
  const newSession = onOpenSession === undefined || projectId === undefined ? undefined : () => run('session', async () => {
    const session = await newSessionIn(computer, settings, projectId);
    if (session === undefined) setHelp(settings);
    else onOpenSession(session);
  });

  return (
    <GitHubRowView
      label={computer.label}
      state={state}
      login={live?.login ?? info.login}
      mine={live?.connect !== undefined}
      problem={live?.problem}
      {...(sheet === undefined ? { setup: live?.setup } : {})}
      confirming={confirming}
      busy={busy}
      note={note}
      help={help}
      onConnect={() => { setNote(undefined); setSheet('connect'); }}
      onContinue={() => setSheet('resume')}
      onSetUp={() => { setNote(undefined); setSheet('list'); }}
      onDisconnect={() => { setNote(undefined); setConfirming(true); }}
      onConfirm={disconnect}
      onCancel={() => setConfirming(false)}
      onCheck={check}
      onRetry={() => { void read(); }}
      onInstall={install}
      onShow={() => setSheet('list')}
      {...(newSession === undefined ? {} : { onNewSession: newSession })}
    >
      {sheet !== undefined && (
        <GitHubSheet
          computer={computer}
          settings={settings}
          provider={provider}
          start={sheet}
          handlers={handlers}
          onClose={() => {
            setSheet(undefined);
            void read();
            void handlers.onChanged().catch(() => undefined);
          }}
        />
      )}
    </GitHubRowView>
  );
}

// ---------------------------------------------------------------- Connect

const Status = ({ tone, text, time }: { readonly tone: 'amber' | 'accent' | 'green'; readonly text: string; readonly time?: string | undefined }) => (
  <p className={`gh-status ${tone}`}>
    <span role="status">{text}</span>
    {time !== undefined && <span className="gh-time">{time}</span>}
  </p>
);

/** The time left of a code, never below 0 or above 15 minutes. ponytail: uses the phone's clock; a computer clock that is off by minutes shows a wrong time left (the host still ends the code itself). */
export const timeLeft = (expiresAt: number, now: number): string => `${clock(Math.min(CODE_MS, Math.max(0, expiresAt - now)))} left`;

/** The Connect sheet's body for one answer: the code, step 2, or a problem with its action. */
export function ConnectBody({ label, state, now, busy, onConnect }: {
  readonly label: string;
  readonly state: HTTPGitHubResponse | undefined;
  readonly now: number;
  /** A Connect is being asked for. */
  readonly busy: boolean;
  /** Get a new code, Try again, Retry, Connect: a new Connect. */
  readonly onConnect: () => void;
}) {
  if (busy && (state === undefined || state.state === 'notConnected')) return <Status tone="amber" text="Getting a code…" />;
  if (state === undefined) return <p role="status" className="muted field-note" />;
  const down = state.problem === 'gitHubDown' ? <p className="hint-text">{reachText(label)}</p> : undefined;
  if (state.state === 'needsInstall') {
    const url = installUrl(state.app);
    return (
      <>
        <ol className="gh-box gh-steps">
          <li className="done"><span className="gh-mark" aria-hidden="true">✓</span><span>Signed in as {state.login}</span></li>
          <li><span className="gh-mark" aria-hidden="true">2</span><span>Choose what Termivoa can read</span></li>
        </ol>
        {url !== undefined && <OutLink href={url}>Choose on GitHub</OutLink>}
        <Status tone="amber" text="Waiting for GitHub…" />
        {down ?? <p className="hint-text">One time for your GitHub account.</p>}
      </>
    );
  }
  if (state.state === 'connecting') {
    const { connect } = state;
    if (connect === undefined) return <p className="hint-text">Connecting from another phone…</p>;
    return (
      <>
        <div className="gh-box gh-code-box">
          <p className="caps">Your code</p>
          <p className="gh-code">{connect.userCode}</p>
        </div>
        <OutLink href={DEVICE_URL} onClick={() => copyCode(connect.userCode)}>Copy code and open GitHub</OutLink>
        <Status tone="amber" text="Waiting for GitHub…" time={timeLeft(connect.expiresAt, now)} />
        {down ?? <p className="hint-text">Enter a code only when you started Connect yourself.</p>}
      </>
    );
  }
  if (state.state === 'noGit') return <p className="hint-text">git is not on {label}. GitHub needs it.</p>;
  if (state.state === 'ready') return <Status tone="green" text={`Connected as ${state.login ?? ''}`} />;
  // notConnected: what ended the last try, and one action.
  if (state.problem === 'otherProgram') {
    return <p className="hint-text">Another program used this login. <OutLink className="gh-inline" href={AUTHORIZED_APPS_URL}>Revoke Termivoa on GitHub</OutLink>, then connect again.</p>;
  }
  const problem = state.problem === undefined ? undefined : connectProblem(state.problem, label);
  return (
    <>
      {problem !== undefined && <p role="alert" className="gh-line">{problem.text}</p>}
      <Button className="primary wide" onClick={onConnect}>{problem?.action ?? 'Connect'}</Button>
    </>
  );
}

// ---------------------------------------------------------------- The list

export type ReposStatus =
  | { readonly kind: 'loading' }
  /** `at`: when the list arrived; each row's "2 h ago" is worked out once, from it. */
  | { readonly kind: 'ok'; readonly body: HTTPGitHubReposResponse; readonly at: number }
  | { readonly kind: 'error'; readonly text: string };

/** The repository list: search, rows, and the way to choose more on GitHub. */
export function RepoList({ label, repos, query, app, busy, onQuery, onSetUp, onRetry }: {
  readonly label: string;
  readonly repos: ReposStatus;
  readonly query: string;
  readonly app: string | undefined;
  /** The repository whose set-up is being asked for. */
  readonly busy: string | undefined;
  readonly onQuery: (query: string) => void;
  readonly onSetUp: (repo: HTTPGitHubRepo) => void;
  /** Retry, and Check again on an empty list: reads the list again. */
  readonly onRetry: () => void;
}) {
  // Each row's words once per list; a key stroke only filters (1000 rows stay smooth on a phone).
  const rows = useMemo(() => (repos.kind === 'ok' ? repos.body.repos.map((repo) => ({ ...repo, name: repoName(repo.repo), meta: repoMeta(repo, repos.at) })) : []), [repos]);
  const shown = useMemo(() => filterRepos(rows, query), [rows, query]);
  const url = installUrl(app);
  if (repos.kind === 'loading') return <p role="status" className="muted">Reading your repositories…</p>;
  if (repos.kind === 'error') return <Notice tone="danger" role="alert" title={repos.text} action={<button type="button" className="link" onClick={onRetry}>Retry</button>} />;
  if (rows.length === 0) {
    return (
      <>
        <p className="gh-line">Termivoa sees no repository yet.</p>
        {url !== undefined && <OutLink href={url}>Choose on GitHub</OutLink>}
        <button type="button" className="link gh-center" onClick={onRetry}>Check again</button>
      </>
    );
  }
  const off = busy !== undefined;
  return (
    <>
      <input
        type="search"
        className="files-search"
        aria-label={`Search ${rows.length} repositories`}
        placeholder={`Search ${rows.length} repositories`}
        maxLength={140}
        enterKeyHint="search"
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
        spellCheck={false}
        value={query}
        onChange={(event) => onQuery(event.target.value)}
      />
      <ul className="gh-repos">
        {shown.map((repo) => {
          const { name } = repo;
          const taken = repo.taken && !repo.here;
          return (
            <li key={repo.repo} className={taken ? 'gh-repo taken' : 'gh-repo'}>
              <span className="row-text">
                <span className="gh-repo-name">{name}</span>
                <span className="gh-repo-meta">
                  {taken ? `Another folder named ${name} is on ${label}.` : repo.meta}
                  {!taken && isBig(repo) && <> · <span className="gh-big">{sizeText(repo.sizeKb)}</span></>}
                </span>
              </span>
              <span className="gh-slot">
                {repo.here ? (
                  <Button className="gh-tag" aria-label={`${name}: on ${label}`} busy={busy === repo.repo} disabled={off} onClick={() => onSetUp(repo)}>
                    <span className="dot green" aria-hidden="true" /><span className="gh-tag-text">on {label}</span>
                  </Button>
                ) : taken ? <span className="gh-taken">name taken</span> : (
                  <Button className="link accent" aria-label={`Set up ${name}`} busy={busy === repo.repo} disabled={off} onClick={() => onSetUp(repo)}>Set up</Button>
                )}
              </span>
            </li>
          );
        })}
      </ul>
      {repos.body.truncated === true && <p className="hint-text">Showing {rows.length} repositories.</p>}
      {url !== undefined && (
        <div className="gh-foot">
          <p className="hint-text">Not in the list?</p>
          <OutLink className="gh-inline strong" href={url}>Choose repositories on GitHub</OutLink>
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------- Set up

export type SetupBusy = 'stop' | 'retry' | 'session' | 'workspace';

/** One set-up: cloning, ready (or already there), or failed. State comes from props. */
export function SetupBody({ label, setup, now, busy, provider, note, help, onStop, onRetry, onConnect, onNewSession, onWorkspace }: {
  readonly label: string;
  readonly setup: HTTPGitHubSetup;
  readonly now: number;
  readonly busy: SetupBusy | undefined;
  /** herdr's name when New workspace can run on that computer. */
  readonly provider?: string | undefined;
  readonly note?: ManageNote | undefined;
  /** The computer has no shell for New session: New session's own help. */
  readonly help?: HTTPSettingsResponse | undefined;
  readonly onStop: () => void;
  readonly onRetry: () => void;
  readonly onConnect: () => void;
  readonly onNewSession: () => void;
  readonly onWorkspace: () => void;
}) {
  const off = busy !== undefined;
  const elapsed = setup.startedAt === undefined ? undefined : clock(now - setup.startedAt);
  if (setup.phase === 'cloning') {
    return (
      <>
        <div className="gh-box gh-progress">
          <div className="spread"><span className="gh-mono">{setup.repo}</span>{setup.percent !== undefined && <span className="gh-mono">{setup.percent}%</span>}</div>
          <progress aria-label={`Cloning ${setup.repo}`} max={100} {...(setup.percent === undefined ? {} : { value: setup.percent })} />
          {setup.bytes !== undefined && setup.bytes > 0 && <p className="gh-small">{Math.round(setup.bytes / (1024 * 1024))} MB so far</p>}
        </div>
        <Button className="secondary wide" busy={busy === 'stop'} busyLabel="Stopping…" onClick={onStop}>Stop</Button>
        <Status tone="accent" text={`Cloning on ${label}…`} time={elapsed} />
        <p className="hint-text">You can lock the phone. {label} continues.</p>
      </>
    );
  }
  if (setup.phase === 'failed') {
    const problem = setupProblem(setup.problem, repoName(setup.repo), label);
    const act = problem.action === 'Connect' ? onConnect : onRetry;
    return (
      <Notice
        tone="danger"
        role="alert"
        title={problem.text}
        {...(problem.action === undefined ? {} : { action: <Button className="link" busy={busy === 'retry'} onClick={act}>{problem.action}</Button> })}
      />
    );
  }
  return (
    <>
      {note !== undefined && <NoteLine note={note} name="" provider={provider ?? ''} />}
      {setup.projectId !== undefined && help === undefined && (
        <Button className="primary wide" busy={busy === 'session'} busyLabel="Opening…" disabled={off} onClick={onNewSession}>New session</Button>
      )}
      {help !== undefined && <NewSessionHelp settings={help} />}
      <Status tone="green" text={setup.already === true ? `Already on ${label}.` : `Ready on ${label}`} />
      {setup.lfs === true && <p className="hint-text">Large files were not downloaded. Run <code>git lfs pull</code> in a session.</p>}
      {provider !== undefined && setup.projectId !== undefined && (
        <Button className="link accent gh-center" busy={busy === 'workspace'} busyLabel="Opening…" disabled={off} onClick={onWorkspace}>
          <span>New workspace in <bdi>{provider}</bdi></span>
        </Button>
      )}
    </>
  );
}

// ---------------------------------------------------------------- The sheet's steps

export type Mode = 'connect' | 'list' | 'setup';

export type ModeEvent =
  /** The first read of a sheet opened with `resume` or `list`. */
  | { readonly kind: 'opened'; readonly state: HTTPGitHubResponse }
  /** Any later answer: a poll, or an action's. */
  | { readonly kind: 'answer'; readonly state: HTTPGitHubResponse }
  /** Set up answered. */
  | { readonly kind: 'started'; readonly state: HTTPGitHubResponse }
  /** Done or Close in the set-up view, Escape, or the backdrop. `startedHere`: the set-up began in this sheet. */
  | { readonly kind: 'back'; readonly state: HTTPGitHubResponse | undefined; readonly startedHere: boolean };

export interface ModeStep {
  readonly mode: Mode;
  /** The sheet closes. */
  readonly close?: true;
  /** The list is read again. */
  readonly reload?: true;
  /** One line over the list. */
  readonly note?: string;
  /** The computer's settings are read again (a new login). */
  readonly changed?: true;
}

/** Where the sheet goes next. Only a clone that runs covers the list when the sheet opens; only a finished set-up of this sheet closes on Done. */
export function nextMode(mode: Mode, event: ModeEvent, label: string): ModeStep {
  const setup = event.state?.setup;
  const failedLine = (one: HTTPGitHubSetup) => setupProblem(one.problem, repoName(one.repo), label).text;
  switch (event.kind) {
    case 'opened':
      if (mode === 'list' && event.state.state === 'ready' && setup?.phase === 'cloning') return { mode: 'setup' };
      return nextMode(mode, { kind: 'answer', state: event.state }, label);
    case 'started':
      return { mode: setup === undefined ? mode : 'setup' };
    case 'back':
      if (setup === undefined) return { mode: 'list', reload: true };
      if (setup.phase === 'cloning' || (setup.phase === 'finished' && event.startedHere)) return { mode, close: true };
      if (setup.phase === 'failed') return { mode: 'list', reload: true, note: failedLine(setup) };
      return { mode: 'list', reload: true };
    case 'answer': {
      const { state } = event;
      if (mode === 'connect' && state.state === 'ready') return { mode: 'list', changed: true };
      if (mode === 'list' && state.state !== 'ready') return { mode: 'connect' };
      if (mode === 'setup' && setup?.phase === 'failed' && setup.problem === 'notFound') return { mode: 'list', reload: true, note: failedLine(setup) };
      return { mode };
    }
  }
}

// ---------------------------------------------------------------- The sheet

/**
 * Connect, the list and a set-up, in one sheet for one computer. Everything is drawn from GET /api/v1/github, so the
 * sheet comes back in the same step after iOS reloads the app. `connect` starts a Connect; `resume` shows the one that
 * runs (or step 2); `list` opens the list, or the clone that runs.
 */
export function GitHubSheet({ computer, settings, provider, start, handlers, onClose }: {
  readonly computer: Computer;
  /** That computer's settings: the shell New session uses. */
  readonly settings: HTTPSettingsResponse | undefined;
  /** herdr on that computer when New workspace can run there. */
  readonly provider?: ProviderEntry | undefined;
  readonly start: 'connect' | 'resume' | 'list';
  readonly handlers: GitHubHandlers;
  readonly onClose: () => void;
}) {
  const api = client(computer.origin);
  const { label } = computer;
  const titleId = useId();
  const confirmId = useId();
  const [mode, setModeState] = useState<Mode>(start === 'list' ? 'list' : 'connect');
  const modeRef = useRef(mode);
  const [state, setState] = useState<HTTPGitHubResponse>();
  const [repos, setRepos] = useState<ReposStatus>();
  const [query, setQuery] = useState('');
  const [confirm, setConfirm] = useState<HTTPGitHubRepo>();
  const [note, setNote] = useState<string>();
  const [manageNote, setManageNote] = useState<ManageNote>();
  const [help, setHelp] = useState<HTTPSettingsResponse>();
  const [busy, setBusy] = useState<string>();
  const [unreadable, setUnreadable] = useState(false);
  const startedHere = useRef(false);
  const reading = useRef(false);
  const running = useRef(false);
  const misses = useRef(0);
  const words = (error: unknown) => handlers.onFailure(error, computer);

  function go(step: ModeStep) {
    if (step.close === true) {
      onClose();
      return;
    }
    modeRef.current = step.mode;
    setModeState(step.mode);
    if (step.reload === true) setRepos(undefined);
    if (step.note !== undefined) setNote(step.note);
    if (step.changed === true) void handlers.onChanged().catch(() => undefined);
  }
  function receive(answer: HTTPGitHubResponse, kind: 'opened' | 'answer' | 'started' = 'answer') {
    setState(answer);
    go(nextMode(modeRef.current, { kind, state: answer }, label));
  }

  const read = () => {
    if (reading.current) return;
    reading.current = true;
    api.github().then((answer) => {
      misses.current = 0;
      setUnreadable(false);
      receive(answer);
    }, (error: unknown) => {
      // A poll that keeps failing to validate says so, instead of freezing on the last good answer.
      if (error instanceof ApiError && error.code === 'invalid_response' && ++misses.current >= UNREADABLE_AFTER) setUnreadable(true);
    }).finally(() => { reading.current = false; });
  };

  /** One action at a time; its answer is the new state. */
  function act(what: string, work: () => Promise<HTTPGitHubResponse>, kind: 'answer' | 'started' = 'answer', then?: () => void) {
    if (running.current) return;
    running.current = true;
    setBusy(what);
    setNote(undefined);
    work().then(
      (answer) => { then?.(); receive(answer, kind); },
      (error: unknown) => setNote(words(error)),
    ).finally(() => { running.current = false; setBusy(undefined); });
  }
  const connect = () => act('connect', () => api.githubAction({ action: 'connect' }));
  const setUp = (repo: string) => act(repo, () => api.githubAction({ action: 'setup', repo }), 'started', () => {
    startedHere.current = true;
    setConfirm(undefined);
    setManageNote(undefined);
    setHelp(undefined);
  });

  const open = () => api.github().then((answer) => receive(answer, 'opened'), (error: unknown) => (start === 'list' ? setRepos({ kind: 'error', text: words(error) }) : setNote(words(error))));
  useEffect(() => {
    if (start === 'connect') connect();
    else void open();
  }, []);

  // A finished set-up made a project: New workspace and New session list it.
  const notified = useRef<string>(undefined);
  useEffect(() => {
    const setup = state?.setup;
    if (mode !== 'setup' || setup?.phase !== 'finished' || notified.current === `${setup.repo} ${setup.startedAt}`) return;
    notified.current = `${setup.repo} ${setup.startedAt}`;
    void handlers.onChanged().catch(() => undefined);
  }, [state, mode]);

  const loadRepos = (quiet: boolean) => {
    if (!quiet) setRepos({ kind: 'loading' });
    api.githubRepos().then(
      (body) => setRepos({ kind: 'ok', body, at: Date.now() }),
      (error: unknown) => { if (!quiet) setRepos({ kind: 'error', text: words(error) }); },
    );
  };
  // The list is read when it shows; again after Retry, Check again, or a set-up that found the repository gone.
  useEffect(() => {
    if (mode === 'connect' || (mode === 'setup' && state?.setup !== undefined) || state?.state !== 'ready' || repos !== undefined) return;
    loadRepos(false);
  }, [mode, state, repos]);
  // Back from GitHub (Choose on GitHub): the list again, without a blank in between.
  useVisible(() => { if (modeRef.current === 'list' && repos?.kind === 'ok') loadRepos(true); });

  const waiting = mode === 'connect' ? state === undefined || state.state === 'connecting' || state.state === 'needsInstall'
    : mode === 'setup' && state?.setup?.phase === 'cloning';
  usePoll(waiting, read);
  const now = useNow(mode !== 'list');

  const cancel = () => {
    if (state?.state === 'connecting' && state.connect !== undefined) void api.githubAction({ action: 'cancel' }).catch(() => undefined);
    onClose();
  };
  const back = () => go(nextMode(modeRef.current, { kind: 'back', state, startedHere: startedHere.current }, label));

  function newSession(projectId: string) {
    if (running.current) return;
    running.current = true;
    setBusy('session');
    setNote(undefined);
    newSessionIn(computer, settings, projectId).then(
      (session) => { if (session === undefined) setHelp(settings); else handlers.onOpenSession?.(session); },
      (error: unknown) => setNote(words(error)),
    ).finally(() => { running.current = false; setBusy(undefined); });
  }

  function newWorkspace(projectId: string) {
    if (provider === undefined || running.current) return;
    running.current = true;
    setBusy('workspace');
    setManageNote(undefined);
    const reload = () => handlers.onProviderReload?.(provider.key) ?? Promise.resolve();
    void manageStep({ action: 'newWorkspace', projectId }, (body) => api.providerManage(provider.info.id, body), reload, words).then((step) => {
      running.current = false;
      setBusy(undefined);
      switch (step.kind) {
        case 'open': handlers.onProviderOpened?.({ computer, provider: provider.info.id, targetId: step.targetId }, provider.key); break;
        case 'done': onClose(); break;
        case 'changed': setManageNote({ kind: 'error', text: CHANGED }); break;
        case 'note': setManageNote(step.note); break;
      }
    });
  }

  const caps = `GitHub · on ${label}`;
  const setup = state?.setup;
  const step2 = state?.state === 'needsInstall';
  let header: ReactNode;
  let body: ReactNode;
  // Escape and the backdrop do what the header's button does.
  let dismiss = onClose;
  if (mode === 'setup' && setup !== undefined) {
    dismiss = back;
    header = (
      <SheetHeader id={titleId} caps={caps} title={<span className="gh-mono">{repoName(setup.repo)}</span>}>
        <button type="button" className="link" onClick={back}>{setup.phase === 'finished' ? 'Done' : 'Close'}</button>
      </SheetHeader>
    );
    body = (
      <SetupBody
        label={label}
        setup={setup}
        now={now}
        busy={busy === 'stop' || busy === 'session' || busy === 'workspace' ? busy : busy === setup.repo ? 'retry' : undefined}
        provider={provider?.info.name}
        note={manageNote}
        help={help}
        onStop={() => act('stop', () => api.githubAction({ action: 'stop' }))}
        onRetry={() => setUp(setup.repo)}
        onConnect={() => { go({ mode: 'connect' }); connect(); }}
        onNewSession={() => { if (setup.projectId !== undefined) newSession(setup.projectId); }}
        onWorkspace={() => { if (setup.projectId !== undefined) newWorkspace(setup.projectId); }}
      />
    );
  } else if (mode !== 'connect') {
    header = (
      <SheetHeader id={titleId} caps={caps} title="Set up a repository">
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>
    );
    body = confirm !== undefined ? (
      <div role="alertdialog" aria-labelledby={confirmId} className="confirm gh-ask stack">
        <p id={confirmId}><bdi>{repoName(confirm.repo)}</bdi> is {sizeText(confirm.sizeKb)}. Set it up on {label}?</p>
        <div className="row confirm-pair">
          <button type="button" disabled={busy !== undefined} onClick={() => setConfirm(undefined)}>Cancel</button>
          <Button className="primary" busy={busy === confirm.repo} onClick={() => setUp(confirm.repo)}>Set up</Button>
        </div>
      </div>
    ) : (
      <RepoList
        label={label}
        repos={repos ?? { kind: 'loading' }}
        query={query}
        app={state?.app}
        busy={busy}
        onQuery={setQuery}
        onSetUp={(repo) => { setNote(undefined); if (!repo.here && isBig(repo)) setConfirm(repo); else setUp(repo.repo); }}
        onRetry={() => {
          setRepos(undefined);
          if (state === undefined) void open();
        }}
      />
    );
  } else {
    dismiss = step2 ? onClose : cancel;
    header = (
      <SheetHeader id={titleId} caps={step2 ? `${caps} · step 2 of 2` : caps} title={step2 ? 'Choose repositories' : 'Connect'}>
        <button type="button" className="link" onClick={dismiss}>{step2 ? 'Later' : 'Cancel'}</button>
      </SheetHeader>
    );
    body = <ConnectBody label={label} state={state} now={now} busy={busy === 'connect'} onConnect={connect} />;
  }

  return (
    <div className="sheet-backdrop" onClick={(event) => { if (event.target === event.currentTarget) dismiss(); }}>
      <Sheet labelledBy={titleId} onClose={dismiss}>
        {header}
        {unreadable && <Notice tone="warning" role="alert" title={unreadableText(label)} />}
        {note !== undefined && <Notice tone="danger" role="alert" title={note} />}
        {body}
      </Sheet>
    </div>
  );
}
