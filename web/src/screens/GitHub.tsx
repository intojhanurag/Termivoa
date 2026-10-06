import { useEffect, useRef, useState, type ReactNode } from 'react';
import { client } from '../api/http';
import type { Computer } from '../computers';
import type {
  HTTPGitHubInfo, HTTPGitHubProblem, HTTPGitHubRepo, HTTPGitHubReposResponse, HTTPGitHubResponse,
  HTTPGitHubSetup, HTTPGitHubSetupProblem, HTTPGitHubState, HTTPSettingsResponse,
} from '../generated/protocol';
import { checkAgain, hosted, type ComputerEntry, type HostedSession } from './allSessions';
import type { ProviderTap } from './Herdr';
import { NewSessionHelp } from './NewSession';
import { CHANGED, githubShown, manageStep, NoteLine, type ManageNote } from './ProviderManage';
import { providerState, type ProviderEntry } from './providers';
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

/** Every computer with a GitHub row, in the computers' order. */
export const githubSources = (entries: ReadonlyArray<ComputerEntry>): GitHubSource[] => entries.flatMap((entry) => {
  if (entry.status.kind !== 'ok') return [];
  const info = githubInfo(entry.status.settings);
  return info === undefined ? [] : [{ computer: entry.computer, settings: entry.status.settings, info }];
});

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
export function filterRepos(repos: ReadonlyArray<HTTPGitHubRepo>, query: string): ReadonlyArray<HTTPGitHubRepo> {
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

type RowNote = { readonly kind: 'disconnected'; readonly revokeFailed?: boolean } | { readonly kind: 'error'; readonly text: string; readonly revoke?: boolean };

/** The row of one computer in Settings → Integrations. State comes from props, so each state renders on its own. */
export function GitHubRowView({ label, state, login, mine, problem, confirming = false, busy, note, children, onConnect, onContinue, onSetUp, onDisconnect, onConfirm, onCancel, onCheck, onRetry }: {
  readonly label: string;
  readonly state: HTTPGitHubState;
  readonly login?: string | undefined;
  /** This phone started the Connect that runs (the answer has its code). */
  readonly mine?: boolean;
  readonly problem?: HTTPGitHubProblem | undefined;
  readonly confirming?: boolean;
  readonly busy?: 'disconnect' | 'check' | undefined;
  readonly note?: RowNote | undefined;
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
}) {
  const meta = state === 'ready' || state === 'needsInstall' ? login
    : state === 'notConnected' ? 'not connected'
      : state === 'noGit' ? `git is not on ${label}`
        : mine === true ? 'Connecting…' : 'Connecting from another phone…';
  const word = state === 'ready' ? { text: 'Connected', tone: 'green' }
    : state === 'noGit' ? { text: 'Git missing', tone: 'amber' }
      : state === 'needsInstall' ? { text: 'Signed in', tone: 'amber' } : undefined;
  const off = busy !== undefined;
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
          <Button className="link" busy={busy === 'check'} busyLabel="Checking…" onClick={onCheck}>Check again</Button>
        </div>
      )}
      {problem === 'otherProgram' && (
        <p className="hint-text">Another program used this login. <OutLink className="gh-inline" href={AUTHORIZED_APPS_URL}>Revoke Termivoa on GitHub</OutLink>, then connect again.</p>
      )}
      {problem === 'ended' && state === 'notConnected' && <p className="hint-text">{endedText(label)}</p>}
      {problem === 'gitHubDown' && (
        <Notice tone="warning" title={reachText(label)} action={<button type="button" className="link" onClick={onRetry}>Retry</button>} />
      )}
      {confirming && (
        <div role="alertdialog" aria-labelledby={`gh-disconnect-${label}`} className="confirm stack">
          <p id={`gh-disconnect-${label}`}>Disconnect GitHub on {label}?</p>
          <ConfirmPair confirm="Disconnect" busy={busy === 'disconnect'} busyLabel="Disconnecting…" onCancel={onCancel} onConfirm={onConfirm} />
        </div>
      )}
      <div role="status" className="field-note">
        {note?.kind === 'disconnected' && <span className="muted">GitHub is disconnected on {label}.</span>}
        {note?.kind === 'disconnected' && note.revokeFailed === true && <p className="hint-text"><OutLink className="gh-inline" href={AUTHORIZED_APPS_URL}>Also remove Termivoa on GitHub.</OutLink></p>}
        {note?.kind === 'error' && <Notice tone="danger" title={note.text} />}
        {note?.kind === 'error' && note.revoke === true && <p className="hint-text"><OutLink className="gh-inline" href={AUTHORIZED_APPS_URL}>Also remove Termivoa on GitHub.</OutLink></p>}
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
export function GitHubRow({ source, provider, handlers }: {
  readonly source: GitHubSource;
  readonly provider?: ProviderEntry | undefined;
  readonly handlers: GitHubHandlers;
}) {
  const { computer, info, settings } = source;
  // The row's own read: the problem line, and whether this phone started the Connect. Settings has only state and login.
  const [live, setLive] = useState<HTTPGitHubResponse>();
  const [sheet, setSheet] = useState<'connect' | 'resume' | 'list'>();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState<'disconnect' | 'check'>();
  const [note, setNote] = useState<RowNote>();
  const read = () => client(computer.origin).github().then(setLive, () => undefined);
  useEffect(() => { void read(); }, [computer.origin, info.state]);

  const state = live?.state ?? info.state;
  const disconnect = () => {
    setBusy('disconnect');
    setNote(undefined);
    client(computer.origin).githubAction({ action: 'disconnect' }).then(
      (answer) => { setLive(answer); setNote({ kind: 'disconnected', revokeFailed: answer.revokeFailed === true }); void handlers.onChanged().catch(() => undefined); },
      (error: unknown) => setNote({ kind: 'error', text: handlers.onFailure(error, computer), revoke: true }),
    ).finally(() => { setBusy(undefined); setConfirming(false); });
  };
  const check = () => {
    setBusy('check');
    setNote(undefined);
    void checkAgain(handlers.onChanged(), (error) => handlers.onFailure(error, computer)).then((problem) => {
      if (problem !== undefined) setNote({ kind: 'error', text: problem });
      return read();
    }).finally(() => setBusy(undefined));
  };

  return (
    <GitHubRowView
      label={computer.label}
      state={state}
      login={live?.login ?? info.login}
      mine={live?.connect !== undefined}
      problem={live?.problem}
      confirming={confirming}
      busy={busy}
      note={note}
      onConnect={() => { setNote(undefined); setSheet('connect'); }}
      onContinue={() => setSheet('resume')}
      onSetUp={() => { setNote(undefined); setSheet('list'); }}
      onDisconnect={() => { setNote(undefined); setConfirming(true); }}
      onConfirm={disconnect}
      onCancel={() => setConfirming(false)}
      onCheck={check}
      onRetry={() => { void read(); }}
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

/** The Connect sheet's body for one answer: the code, step 2, or a problem with its action. */
export function ConnectBody({ label, state, now, busy, onConnect }: {
  readonly label: string;
  readonly state: HTTPGitHubResponse | undefined;
  readonly now: number;
  readonly busy: boolean;
  /** Get a new code, Try again, Retry, Connect: a new Connect. */
  readonly onConnect: () => void;
}) {
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
        <Status tone="amber" text="Waiting for GitHub…" time={`${clock(connect.expiresAt - now)} left`} />
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
      <Button className="primary wide" busy={busy} busyLabel="Connecting…" onClick={onConnect}>{problem?.action ?? 'Connect'}</Button>
    </>
  );
}

// ---------------------------------------------------------------- The list

export type ReposStatus =
  | { readonly kind: 'loading' }
  | { readonly kind: 'ok'; readonly body: HTTPGitHubReposResponse }
  | { readonly kind: 'error'; readonly text: string };

/** The repository list: search, rows, and the way to choose more on GitHub. */
export function RepoList({ label, repos, query, now, app, busy, onQuery, onSetUp, onRetry }: {
  readonly label: string;
  readonly repos: ReposStatus;
  readonly query: string;
  readonly now: number;
  readonly app: string | undefined;
  /** The repository whose set-up is being asked for. */
  readonly busy: string | undefined;
  readonly onQuery: (query: string) => void;
  readonly onSetUp: (repo: HTTPGitHubRepo) => void;
  readonly onRetry: () => void;
}) {
  const url = installUrl(app);
  if (repos.kind === 'loading') return <p role="status" className="muted">Reading your repositories…</p>;
  if (repos.kind === 'error') return <Notice tone="danger" role="alert" title={repos.text} action={<button type="button" className="link" onClick={onRetry}>Retry</button>} />;
  const all = repos.body.repos;
  if (all.length === 0) {
    return (
      <>
        <p className="gh-line">Termivoa sees no repository yet.</p>
        {url !== undefined && <OutLink href={url}>Choose on GitHub</OutLink>}
      </>
    );
  }
  const shown = filterRepos(all, query);
  const off = busy !== undefined;
  return (
    <>
      <input
        type="search"
        className="files-search"
        aria-label={`Search ${all.length} repositories`}
        placeholder={`Search ${all.length} repositories`}
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
          const name = repoName(repo.repo);
          const taken = repo.taken && !repo.here;
          return (
            <li key={repo.repo} className={taken ? 'gh-repo taken' : 'gh-repo'}>
              <span className="row-text">
                <span className="gh-repo-name">{name}</span>
                <span className="gh-repo-meta">
                  {taken ? `Another folder named ${name} is on ${label}.` : repoMeta(repo, now)}
                  {!taken && isBig(repo) && <> · <span className="gh-big">{sizeText(repo.sizeKb)}</span></>}
                </span>
              </span>
              <span className="gh-slot">
                {repo.here ? (
                  <Button className="gh-tag" aria-label={`${name}: on ${label}`} busy={busy === repo.repo} disabled={off} onClick={() => onSetUp(repo)}>
                    <span className="dot green" aria-hidden="true" />on {label}
                  </Button>
                ) : taken ? <span className="gh-taken">name taken</span> : (
                  <Button className="link accent" aria-label={`Set up ${name}`} busy={busy === repo.repo} disabled={off} onClick={() => onSetUp(repo)}>Set up</Button>
                )}
              </span>
            </li>
          );
        })}
      </ul>
      {repos.body.truncated === true && <p className="hint-text">Showing {all.length} repositories.</p>}
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
          {setup.bytes !== undefined && <p className="gh-small">{Math.round(setup.bytes / (1024 * 1024))} MB so far</p>}
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

// ---------------------------------------------------------------- The sheet

type Mode = 'connect' | 'list' | 'setup';

/**
 * Connect, the list and a set-up, in one sheet for one computer. Everything is drawn from GET /api/v1/github, so the
 * sheet comes back in the same step after iOS reloads the app. `connect` starts a Connect; `resume` shows the one that
 * runs (or step 2); `list` opens the list, or a set-up that runs or just ended.
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
  const [mode, setMode] = useState<Mode>(start === 'list' ? 'list' : 'connect');
  const [state, setState] = useState<HTTPGitHubResponse>();
  const [repos, setRepos] = useState<ReposStatus>();
  const [query, setQuery] = useState('');
  const [confirm, setConfirm] = useState<HTTPGitHubRepo>();
  const [note, setNote] = useState<string>();
  const [manageNote, setManageNote] = useState<ManageNote>();
  const [help, setHelp] = useState<HTTPSettingsResponse>();
  const [busy, setBusy] = useState<string>();
  // Set-ups started in this sheet: Done closes it. One shown from earlier goes back to the list.
  const startedHere = useRef(false);
  const reading = useRef(false);
  const running = useRef(false);
  const words = (error: unknown) => handlers.onFailure(error, computer);

  const read = () => {
    if (reading.current) return;
    reading.current = true;
    api.github().then(setState, () => undefined).finally(() => { reading.current = false; });
  };

  /** One action at a time; its answer is the new state. */
  function act(what: string, work: () => Promise<HTTPGitHubResponse>, then?: (answer: HTTPGitHubResponse) => void) {
    if (running.current) return;
    running.current = true;
    setBusy(what);
    setNote(undefined);
    work().then(
      (answer) => { setState(answer); then?.(answer); },
      (error: unknown) => setNote(words(error)),
    ).finally(() => { running.current = false; setBusy(undefined); });
  }
  const connect = () => act('connect', () => api.githubAction({ action: 'connect' }));
  const setUp = (repo: string) => act(repo, () => api.githubAction({ action: 'setup', repo }), (answer) => {
    startedHere.current = true;
    setConfirm(undefined);
    setManageNote(undefined);
    setHelp(undefined);
    if (answer.setup !== undefined) setMode('setup');
  });

  // The first read: a set-up that runs or just ended shows before the list.
  const open = () => api.github().then((answer) => {
    setState(answer);
    if (start === 'list' && answer.setup !== undefined) setMode('setup');
  }, (error: unknown) => (start === 'list' ? setRepos({ kind: 'error', text: words(error) }) : setNote(words(error))));
  useEffect(() => {
    if (start === 'connect') connect();
    else void open();
  }, []);

  // The steps follow the computer's state.
  const notified = useRef<string>(undefined);
  useEffect(() => {
    if (state === undefined) return;
    if (mode === 'connect' && state.state === 'ready') {
      setMode('list');
      void handlers.onChanged().catch(() => undefined);
    } else if (mode === 'list' && state.state !== 'ready') {
      setMode('connect');
    }
    const { setup } = state;
    if (mode === 'setup' && setup?.phase === 'finished' && notified.current !== `${setup.repo} ${setup.startedAt}`) {
      // The new project: New workspace and New session list it.
      notified.current = `${setup.repo} ${setup.startedAt}`;
      void handlers.onChanged().catch(() => undefined);
    }
    if (mode === 'setup' && setup?.phase === 'failed' && setup.problem === 'notFound') {
      setNote(setupProblem('notFound', repoName(setup.repo), label).text);
      setRepos(undefined);
      setMode('list');
    }
  }, [state, mode]);

  // The list is read when it shows; again after Retry or a set-up that found the repository gone.
  useEffect(() => {
    if (mode === 'connect' || (mode === 'setup' && state?.setup !== undefined) || state?.state !== 'ready' || repos !== undefined) return;
    setRepos({ kind: 'loading' });
    api.githubRepos().then((body) => setRepos({ kind: 'ok', body }), (error: unknown) => setRepos({ kind: 'error', text: words(error) }));
  }, [mode, state, repos]);

  const waiting = mode === 'connect' ? state === undefined || state.state === 'connecting' || state.state === 'needsInstall'
    : mode === 'setup' && state?.setup?.phase === 'cloning';
  usePoll(waiting, read);
  const now = useNow(mode !== 'list' || repos?.kind === 'ok');

  const cancel = () => {
    if (state?.state === 'connecting' && state.connect !== undefined) void api.githubAction({ action: 'cancel' }).catch(() => undefined);
    onClose();
  };
  const back = () => {
    if (startedHere.current) {
      onClose();
      return;
    }
    setRepos(undefined);
    setMode('list');
  };

  function newSession(projectId: string) {
    const shell = settings?.shellProfiles[0];
    if (shell === undefined) {
      setHelp(settings);
      return;
    }
    if (running.current) return;
    running.current = true;
    setBusy('session');
    setNote(undefined);
    api.createSession({ operationId: crypto.randomUUID(), projectId, shellProfileId: shell.id }).then(
      (made) => handlers.onOpenSession?.(hosted(made.session, computer)),
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
  let header: ReactNode;
  let body: ReactNode;
  if (mode === 'setup' && setup !== undefined) {
    const cloning = setup.phase === 'cloning';
    header = (
      <SheetHeader id="github-title" caps={caps} title={<span className="gh-mono">{repoName(setup.repo)}</span>}>
        <button type="button" className="link" onClick={cloning ? onClose : back}>{setup.phase === 'finished' ? 'Done' : 'Close'}</button>
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
        onConnect={() => { setMode('connect'); connect(); }}
        onNewSession={() => { if (setup.projectId !== undefined) newSession(setup.projectId); }}
        onWorkspace={() => { if (setup.projectId !== undefined) newWorkspace(setup.projectId); }}
      />
    );
  } else if (mode !== 'connect') {
    header = (
      <SheetHeader id="github-title" caps={caps} title="Set up a repository">
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>
    );
    body = confirm !== undefined ? (
      <div role="alertdialog" aria-labelledby="gh-confirm" className="confirm gh-ask stack">
        <p id="gh-confirm"><bdi>{repoName(confirm.repo)}</bdi> is {sizeText(confirm.sizeKb)}. Set it up on {label}?</p>
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
        now={now}
        app={state?.app}
        busy={busy}
        onQuery={setQuery}
        onSetUp={(repo) => { if (!repo.here && isBig(repo)) setConfirm(repo); else setUp(repo.repo); }}
        onRetry={() => {
          setRepos(undefined);
          if (state === undefined) void open();
        }}
      />
    );
  } else {
    const step2 = state?.state === 'needsInstall';
    header = (
      <SheetHeader id="github-title" caps={step2 ? `${caps} · step 2 of 2` : caps} title={step2 ? 'Choose repositories' : 'Connect'}>
        <button type="button" className="link" onClick={step2 ? onClose : cancel}>{step2 ? 'Later' : 'Cancel'}</button>
      </SheetHeader>
    );
    body = <ConnectBody label={label} state={state} now={now} busy={busy === 'connect'} onConnect={connect} />;
  }

  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="github-title" onClose={mode === 'setup' && setup?.phase !== 'cloning' ? back : onClose}>
        {header}
        {note !== undefined && <Notice tone="danger" role="alert" title={note} />}
        {body}
      </Sheet>
    </div>
  );
}
