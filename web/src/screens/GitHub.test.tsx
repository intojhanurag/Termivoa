import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { HTTPGitHubRepo, HTTPGitHubResponse, HTTPGitHubSetup } from '../generated/protocol';
import {
  BIG_KB, clock, ConnectBody, connectProblem, copyCode, DEVICE_URL, filterRepos, GitHubRowView, githubInfo, githubSources, INSTALL_GIT, installUrl, isBig,
  nextMode, pushedText, RepoList, repoMeta, setupProblem, SetupBody, sizeText, timeLeft, unreadableText, type GitHubSource, type ReposStatus,
} from './GitHub';
import { IntegrationsSection } from './Settings';
import { WorkspaceList } from './ProviderManage';
import { info, mac, okEntry, ready, settingsWith, vps } from './providerFixtures';

const noop = () => undefined;
const NOW = Date.UTC(2026, 9, 7, 12, 0, 0);
const HOUR = 3_600_000;

const repo = (name: string, extra: Partial<HTTPGitHubRepo> = {}): HTTPGitHubRepo => ({
  repo: `octo-demo/${name}`, private: true, fork: false, archived: false, sizeKb: 2048, pushedAt: NOW - 2 * HOUR, here: false, taken: false, ...extra,
});
const answer = (extra: Partial<HTTPGitHubResponse> = {}): HTTPGitHubResponse => ({ state: 'notConnected', app: 'termivoa', ...extra });

describe('helpers', () => {
  it('builds the install address only from a schema-valid slug', () => {
    expect(installUrl('termivoa')).toBe('https://github.com/apps/termivoa/installations/new');
    expect(installUrl('evil.com/x')).toBeUndefined();
    expect(installUrl('../x')).toBeUndefined();
    expect(installUrl('Termivoa')).toBeUndefined();
    expect(installUrl(undefined)).toBeUndefined();
  });

  it('shows GitHub only at revision 7 with github in the settings', () => {
    expect(githubInfo(settingsWith({ protocolRevision: 7, github: { state: 'ready', login: 'octo-demo' } }))).toEqual({ state: 'ready', login: 'octo-demo' });
    expect(githubInfo(settingsWith({ protocolRevision: 6, github: { state: 'ready' } }))).toBeUndefined();
    expect(githubInfo(settingsWith({ protocolRevision: 7 }))).toBeUndefined();
    expect(githubSources([okEntry(mac, { protocolRevision: 7, github: { state: 'notConnected' } }), okEntry(vps, { protocolRevision: 7 })]).map((one) => one.computer.label)).toEqual(['mac']);
  });

  it('says size, time, meta and clock as drawn', () => {
    expect(isBig({ sizeKb: BIG_KB })).toBe(true);
    expect(isBig({ sizeKb: BIG_KB - 1 })).toBe(false);
    expect(sizeText(5 * BIG_KB)).toBe('5.0 GB');
    expect(pushedText(NOW - 2 * HOUR, NOW)).toBe('2 h ago');
    expect(pushedText(NOW, NOW)).toBe('just now');
    expect(pushedText(undefined, NOW)).toBeUndefined();
    expect(repoMeta(repo('a', { private: false, fork: true, archived: true }), NOW)).toBe('octo-demo · public · fork · archived · 2 h ago');
    expect(clock(14 * 60_000 + 32_000)).toBe('14:32');
    expect(clock(-5)).toBe('0:00');
  });

  it('filters by name and owner without regard to case', () => {
    const repos = [repo('shop-api'), repo('notes', { repo: 'acme-labs/notes' })];
    expect(filterRepos(repos, 'SHOP').map((one) => one.repo)).toEqual(['octo-demo/shop-api']);
    expect(filterRepos(repos, 'acme').map((one) => one.repo)).toEqual(['acme-labs/notes']);
    expect(filterRepos(repos, '  ')).toHaveLength(2);
  });

  it('has the exact line for each problem', () => {
    expect(connectProblem('codeExpired', 'vps')).toEqual({ text: 'The code ran out.', action: 'Get a new code' });
    expect(connectProblem('denied', 'vps')).toEqual({ text: 'GitHub did not connect.', action: 'Try again' });
    expect(connectProblem('gitHubDown', 'vps')).toEqual({ text: 'vps cannot reach GitHub.', action: 'Retry' });
    const lines = {
      nameTaken: 'Another folder named notes is on mac.',
      badName: 'Termivoa cannot set up a repository with this name.',
      noSpace: 'mac has too little free space for this repository.',
      tooManyProjects: 'mac has the most projects Termivoa keeps (200).',
      notFound: 'That repository is not on GitHub under this name now.',
      cloneFailed: 'The clone stopped. Nothing was left on mac.',
      stopped: 'The clone stopped. Nothing was left on mac.',
      timeout: 'The clone took more than 30 minutes and stopped. Nothing was left on mac.',
      gitHubDown: 'mac cannot reach GitHub.',
      notConnected: 'GitHub is not connected on mac now.',
    } as const;
    for (const [problem, text] of Object.entries(lines)) expect(setupProblem(problem as keyof typeof lines, 'notes', 'mac').text).toBe(text);
    expect(setupProblem('stopped', 'notes', 'mac').action).toBe('Try again');
    expect(setupProblem('nameTaken', 'notes', 'mac').action).toBeUndefined();
    expect(setupProblem('notConnected', 'notes', 'mac').action).toBe('Connect');
  });
});

describe('the Settings row', () => {
  const row = (extra: Partial<Parameters<typeof GitHubRowView>[0]>) => renderToStaticMarkup(
    <GitHubRowView label="mac" state="ready" onConnect={noop} onContinue={noop} onSetUp={noop} onDisconnect={noop} onConfirm={noop} onCancel={noop} onCheck={noop} onRetry={noop} {...extra} />,
  );

  it('ready: the login, CONNECTED, Set up a repository and Disconnect', () => {
    const page = row({ login: 'octo-demo' });
    expect(page).toContain('GitHub on mac');
    expect(page).toContain('<span class="row-meta">octo-demo</span>');
    expect(page).toContain('<span class="computer-state green">Connected</span>');
    expect(page).toContain('>Set up a repository</button>');
    expect(page).toContain('>Disconnect</button>');
  });

  it('not connected: the meta and a bone Connect', () => {
    const page = row({ state: 'notConnected' });
    expect(page).toContain('not connected');
    expect(page).toContain('<button type="button" class="primary compact">Connect</button>');
    expect(page).not.toContain('computer-state');
  });

  it('connecting: Continue for this phone, a line for another', () => {
    expect(row({ state: 'connecting', mine: true })).toContain('Connecting…');
    expect(row({ state: 'connecting', mine: true })).toContain('>Continue</button>');
    expect(row({ state: 'connecting' })).toContain('Connecting from another phone…');
    expect(row({ state: 'connecting' })).not.toContain('Continue');
  });

  it('needs install: Choose on GitHub', () => {
    expect(row({ state: 'needsInstall', login: 'octo-demo' })).toContain('>Choose on GitHub</button>');
  });

  it('no git: GIT MISSING in amber and Check again only (the settings do not say the platform)', () => {
    const page = row({ state: 'noGit' });
    expect(page).toContain('git is not on mac');
    expect(page).toContain('<span class="computer-state amber">Git missing</span>');
    expect(page).toContain('Check again');
    expect(page).not.toContain('Install git');
  });

  it('a login problem is one line under the row', () => {
    const other = row({ state: 'notConnected', problem: 'otherProgram' });
    expect(other).toContain('Another program used this login. <a class="gh-inline" href="https://github.com/settings/applications" target="_blank" rel="noopener noreferrer">Revoke Termivoa on GitHub</a>, then connect again.');
    expect(row({ state: 'notConnected', problem: 'ended' })).toContain('GitHub is not connected on mac now.');
    const down = row({ problem: 'gitHubDown' });
    expect(down).toContain('mac cannot reach GitHub.');
    expect(down).toContain('>Retry</button>');
  });

  it('Disconnect asks first, then says one line; a failure gives the revoke link', () => {
    const asking = row({ confirming: true });
    expect(asking).toContain('Disconnect GitHub on mac?');
    expect(asking).toContain('>Cancel</button>');
    expect(asking).not.toContain('Set up a repository');
    expect(row({ state: 'notConnected', note: { kind: 'disconnected' } })).toContain('GitHub is disconnected on mac.');
    expect(row({ state: 'notConnected', note: { kind: 'disconnected' } })).not.toContain('Also remove Termivoa on GitHub.');
    expect(row({ state: 'notConnected', note: { kind: 'disconnected', revokeFailed: true } })).toContain('>Also remove Termivoa on GitHub.</a>');
    const failed = row({ note: { kind: 'error', text: 'Connection unavailable' } });
    expect(failed).toContain('Connection unavailable');
    expect(failed).not.toContain('Also remove');
    expect(row({ state: 'notConnected', note: { kind: 'disconnected', revokeFailed: true } })).toContain('>Also remove Termivoa on GitHub.</a>');
  });

  it('Integrations shows GitHub rows also on a computer with no herdr', () => {
    const page = renderToStaticMarkup(
      <IntegrationsSection entries={[okEntry(mac, { protocolRevision: 7, providers: [], github: { state: 'ready', login: 'octo-demo' } })]} onFailure={() => ''} />,
    );
    expect(page).toContain('Integrations');
    expect(page).toContain('GitHub on mac');
    expect(renderToStaticMarkup(<IntegrationsSection entries={[okEntry(mac, { protocolRevision: 7, providers: [] })]} onFailure={() => ''} />)).toBe('');
  });
});

describe('the Connect sheet', () => {
  const body = (state: HTTPGitHubResponse | undefined) => renderToStaticMarkup(<ConnectBody label="vps" state={state} now={NOW} busy={false} onConnect={noop} />);

  it('shows the code, a real link to the fixed device page, and the time left', () => {
    const page = body(answer({ state: 'connecting', connect: { userCode: 'WDJB-MJHT', expiresAt: NOW + 14 * 60_000 + 32_000 } }));
    expect(page).toContain('Your code');
    expect(page).toContain('<p class="gh-code">WDJB-MJHT</p>');
    expect(page).toContain(`<a class="gh-button" href="${DEVICE_URL}" target="_blank" rel="noopener noreferrer">Copy code and open GitHub</a>`);
    expect(DEVICE_URL).toBe('https://github.com/login/device');
    expect(page).toContain('Waiting for GitHub…');
    expect(page).toContain('14:32 left');
    expect(page).toContain('Enter a code only when you started Connect yourself.');
  });

  it('copies in the tap itself, before anything waits', () => {
    const order: string[] = [];
    const clipboard = { writeText: vi.fn((text: string) => { order.push(`copy ${text}`); return Promise.resolve(); }) };
    copyCode('WDJB-MJHT', clipboard);
    order.push('returned');
    expect(order).toEqual(['copy WDJB-MJHT', 'returned']);
    // A refused copy is quiet: the code stays on screen.
    expect(() => copyCode('WDJB-MJHT', { writeText: () => Promise.reject(new Error('denied')) })).not.toThrow();
    expect(() => copyCode('WDJB-MJHT', undefined)).not.toThrow();
  });

  it('another phone\'s Connect shows no code', () => {
    const page = body(answer({ state: 'connecting' }));
    expect(page).toContain('Connecting from another phone…');
    expect(page).not.toContain('gh-code');
  });

  it('step 2: signed in, Choose on GitHub at the app\'s install address', () => {
    const page = body(answer({ state: 'needsInstall', login: 'octo-demo' }));
    expect(page).toContain('Signed in as octo-demo');
    expect(page).toContain('Choose what Termivoa can read');
    expect(page).toContain('<a class="gh-button" href="https://github.com/apps/termivoa/installations/new" target="_blank" rel="noopener noreferrer">Choose on GitHub</a>');
    expect(page).toContain('One time for your GitHub account.');
    expect(body(answer({ state: 'needsInstall', app: 'x/../y' }))).not.toContain('<a ');
  });

  it('each problem: one line and one action', () => {
    const expired = body(answer({ problem: 'codeExpired' }));
    expect(expired).toContain('The code ran out.');
    expect(expired).toContain('Get a new code');
    expect(body(answer({ problem: 'denied' }))).toContain('GitHub did not connect.');
    expect(body(answer({ problem: 'denied' }))).toContain('Try again');
    expect(body(answer({ problem: 'gitHubDown' }))).toContain('vps cannot reach GitHub.');
    expect(body(answer({ problem: 'gitHubDown' }))).toContain('Retry');
    expect(body(answer())).toContain('>Connect</button>');
    expect(body(answer({ state: 'noGit' }))).toContain('git is not on vps. GitHub needs it.');
  });
});

describe('the repository list', () => {
  const list = (repos: ReposStatus, query = '') => renderToStaticMarkup(
    <RepoList label="mac" repos={repos} query={query} app="termivoa" busy={undefined} onQuery={noop} onSetUp={noop} onRetry={noop} />,
  );
  const ok = (repos: HTTPGitHubRepo[], truncated?: boolean): ReposStatus => ({ kind: 'ok', at: NOW, body: { repos, ...(truncated === undefined ? {} : { truncated }) } });

  it('loading, error and empty', () => {
    expect(list({ kind: 'loading' })).toContain('Reading your repositories…');
    const error = list({ kind: 'error', text: 'mac cannot reach GitHub.' });
    expect(error).toContain('mac cannot reach GitHub.');
    expect(error).toContain('Retry');
    const empty = list(ok([]));
    expect(empty).toContain('Termivoa sees no repository yet.');
    expect(empty).toContain('>Choose on GitHub</a>');
    expect(empty).toContain('>Check again</button>');
  });

  it('rows: on mac, name taken, size in amber, Set up', () => {
    const page = list(ok([
      repo('shop-api', { here: true }),
      repo('notes', { taken: true }),
      repo('design-archive', { private: false, fork: true, sizeKb: 5 * BIG_KB }),
      repo('pdf-editor'),
    ]));
    expect(page).toContain('placeholder="Search 4 repositories"');
    expect(page).toContain('<span class="gh-repo-name">shop-api</span>');
    expect(page).toContain('on mac');
    expect(page).toContain('Another folder named notes is on mac.');
    expect(page).toContain('name taken');
    expect(page).toContain('octo-demo · public · fork · 2 h ago · <span class="gh-big">5.0 GB</span>');
    expect(page).toContain('aria-label="Set up pdf-editor"');
    expect(page).toContain('Not in the list?');
    expect(page).toContain('>Choose repositories on GitHub</a>');
    expect(page).not.toContain('Showing');
  });

  it('filters by the query and says when the list was cut', () => {
    const page = list(ok([repo('shop-api'), repo('notes')], true), 'shop');
    expect(page).toContain('shop-api');
    expect(page).not.toContain('gh-repo-name">notes');
    expect(page).toContain('Showing 2 repositories.');
  });
});

describe('the set-up', () => {
  const setup = (extra: Partial<HTTPGitHubSetup>): HTTPGitHubSetup => ({ repo: 'octo-demo/pdf-editor', phase: 'cloning', ...extra });
  const view = (one: HTTPGitHubSetup, extra: Partial<Parameters<typeof SetupBody>[0]> = {}) => renderToStaticMarkup(
    <SetupBody label="mac" setup={one} now={NOW} busy={undefined} onStop={noop} onRetry={noop} onConnect={noop} onNewSession={noop} onWorkspace={noop} {...extra} />,
  );

  it('cloning: percent, a named progress, size so far, Stop, elapsed time', () => {
    const page = view(setup({ percent: 64, bytes: 38 * 1024 * 1024, startedAt: NOW - 42_000 }));
    expect(page).toContain('octo-demo/pdf-editor');
    expect(page).toContain('64%');
    expect(page).toContain('<progress aria-label="Cloning octo-demo/pdf-editor" max="100" value="64"></progress>');
    expect(page).toContain('38 MB so far');
    expect(page).toContain('>Stop</span>');
    expect(page).toContain('Cloning on mac…');
    expect(page).toContain('0:42');
    expect(page).toContain('You can lock the phone. mac continues.');
  });

  it('ready: New session, the green line, herdr only when it runs there', () => {
    const done = setup({ phase: 'finished', projectId: '00000000-0000-4000-8000-000000000001' });
    const page = view(done);
    expect(page).toContain('New session');
    expect(page).toContain('Ready on mac');
    expect(page).not.toContain('New workspace');
    expect(view(done, { provider: 'herdr' })).toContain('New workspace in <bdi>herdr</bdi>');
    expect(view({ ...done, already: true })).toContain('Already on mac.');
    expect(view({ ...done, lfs: true })).toContain('Large files were not downloaded. Run <code>git lfs pull</code> in a session.');
  });

  it('failed: the line and its action', () => {
    const stopped = view(setup({ phase: 'failed', problem: 'stopped' }));
    expect(stopped).toContain('The clone stopped. Nothing was left on mac.');
    expect(stopped).toContain('Try again');
    const taken = view(setup({ phase: 'failed', problem: 'nameTaken' }));
    expect(taken).toContain('Another folder named pdf-editor is on mac.');
    expect(taken).not.toContain('<button');
    expect(view(setup({ phase: 'failed', problem: 'notConnected' }))).toContain('>Connect</button>');
  });
});

describe('From GitHub in New workspace', () => {
  const projects = [{ id: '00000000-0000-4000-8000-000000000001', name: 'shop' }];
  const list = (extra: Parameters<typeof settingsWith>[0]) => renderToStaticMarkup(
    <WorkspaceList entry={ready(vps)} settings={settingsWith({ projects, ...extra })} busy={undefined} checking={false} note={undefined} onPick={noop} onCheck={noop} onAddFolder={noop} onFromGitHub={noop} onClose={noop} />,
  );

  it('shows only at revision 7 with github in that computer\'s settings', () => {
    expect(list({ protocolRevision: 7, github: { state: 'ready' } })).toContain('>From GitHub</button>');
    expect(list({ protocolRevision: 7 })).not.toContain('From GitHub');
    expect(list({ protocolRevision: 6, github: { state: 'ready' } })).not.toContain('From GitHub');
  });
});

/** Every element in a tree that a component returned, without rendering it. */
function elements(node: ReactNode): ReactElement<Record<string, unknown>>[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!isValidElement<Record<string, unknown>>(node)) return [];
  return [node, ...elements(node.props.children as ReactNode)];
}

describe('review fixes', () => {
  const setupOf = (extra: Partial<HTTPGitHubSetup>): HTTPGitHubSetup => ({ repo: 'octo-demo/pdf-editor', phase: 'cloning', ...extra });
  const ready7 = (setup?: HTTPGitHubSetup) => answer({ state: 'ready', login: 'octo-demo', ...(setup === undefined ? {} : { setup }) });

  it('the sheet steps: code, step 2, list', () => {
    const code = answer({ state: 'connecting', connect: { userCode: 'WDJB-MJHT', expiresAt: NOW } });
    expect(nextMode('connect', { kind: 'answer', state: code }, 'vps')).toEqual({ mode: 'connect' });
    expect(nextMode('connect', { kind: 'answer', state: answer({ state: 'needsInstall' }) }, 'vps')).toEqual({ mode: 'connect' });
    expect(nextMode('connect', { kind: 'answer', state: ready7() }, 'vps')).toEqual({ mode: 'list', changed: true });
    expect(nextMode('list', { kind: 'answer', state: answer() }, 'vps')).toEqual({ mode: 'connect' });
  });

  it('on open only a running clone covers the list; an old result or notFound does not', () => {
    expect(nextMode('list', { kind: 'opened', state: ready7(setupOf({})) }, 'mac')).toEqual({ mode: 'setup' });
    expect(nextMode('list', { kind: 'opened', state: ready7(setupOf({ phase: 'finished' })) }, 'mac')).toEqual({ mode: 'list' });
    expect(nextMode('list', { kind: 'opened', state: ready7(setupOf({ phase: 'failed', problem: 'notFound' })) }, 'mac')).toEqual({ mode: 'list' });
    expect(nextMode('connect', { kind: 'opened', state: ready7() }, 'mac')).toEqual({ mode: 'list', changed: true });
  });

  it('cloning to finished stays; notFound goes to the list, with its line, and reads it again', () => {
    expect(nextMode('list', { kind: 'started', state: ready7(setupOf({})) }, 'mac')).toEqual({ mode: 'setup' });
    expect(nextMode('setup', { kind: 'answer', state: ready7(setupOf({ phase: 'finished' })) }, 'mac')).toEqual({ mode: 'setup' });
    expect(nextMode('setup', { kind: 'answer', state: ready7(setupOf({ phase: 'failed', problem: 'notFound' })) }, 'mac'))
      .toEqual({ mode: 'list', reload: true, note: 'That repository is not on GitHub under this name now.' });
  });

  it('back: a clone closes, a finished one of this sheet closes, a failed one goes to the list with its line', () => {
    const back = (setup: HTTPGitHubSetup, startedHere: boolean) => nextMode('setup', { kind: 'back', state: ready7(setup), startedHere }, 'mac');
    expect(back(setupOf({}), false)).toEqual({ mode: 'setup', close: true });
    expect(back(setupOf({ phase: 'finished' }), true)).toEqual({ mode: 'setup', close: true });
    expect(back(setupOf({ phase: 'finished' }), false)).toEqual({ mode: 'list', reload: true });
    expect(back(setupOf({ phase: 'failed', problem: 'stopped' }), true)).toEqual({ mode: 'list', reload: true, note: 'The clone stopped. Nothing was left on mac.' });
  });

  it('the Connect link itself copies the code when tapped', () => {
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const tree = ConnectBody({ label: 'vps', state: answer({ state: 'connecting', connect: { userCode: 'WDJB-MJHT', expiresAt: NOW } }), now: NOW, busy: false, onConnect: noop });
    const link = elements(tree).find((element) => element.props.href === DEVICE_URL);
    expect(link).toBeDefined();
    (link?.props.onClick as () => void)();
    expect(writeText).toHaveBeenCalledWith('WDJB-MJHT');
    vi.unstubAllGlobals();
  });

  it('says Getting a code while the Connect is asked for, and clamps the time left', () => {
    expect(renderToStaticMarkup(<ConnectBody label="vps" state={undefined} now={NOW} busy onConnect={noop} />)).toContain('Getting a code…');
    expect(timeLeft(NOW + 60 * 60_000, NOW)).toBe('15:00 left');
    expect(timeLeft(NOW - 1000, NOW)).toBe('0:00 left');
    expect(unreadableText('mac')).toBe('mac answered something this app cannot read. Update Termivoa on mac.');
  });

  it('no "0 MB so far"', () => {
    const page = renderToStaticMarkup(<SetupBody label="mac" setup={setupOf({ bytes: 0 })} now={NOW} busy={undefined} onStop={noop} onRetry={noop} onConnect={noop} onNewSession={noop} onWorkspace={noop} />);
    expect(page).not.toContain('MB so far');
  });

  it('the row: Install git, a running set-up, a ready one', () => {
    const row = (extra: Partial<Parameters<typeof GitHubRowView>[0]>) => renderToStaticMarkup(
      <GitHubRowView label="mac" state="ready" onConnect={noop} onContinue={noop} onSetUp={noop} onDisconnect={noop} onConfirm={noop} onCancel={noop} onCheck={noop} onRetry={noop} onShow={noop} onNewSession={noop} {...extra} />,
    );
    const git = row({ state: 'noGit', onInstall: noop });
    expect(git).toContain('Install git');
    expect(git).toContain('Check again');
    expect(INSTALL_GIT).toBe('xcode-select --install 2>/dev/null || sudo apt-get install -y git');
    const cloning = row({ setup: setupOf({}) });
    expect(cloning).toContain('Setting up <bdi>pdf-editor</bdi>…');
    expect(cloning).toContain('>Show</button>');
    const done = row({ setup: setupOf({ phase: 'finished', projectId: '00000000-0000-4000-8000-000000000001' }) });
    expect(done).toContain('<bdi>pdf-editor</bdi> is ready.');
    expect(done).toContain('New session');
    expect(row({ setup: setupOf({ phase: 'failed', problem: 'stopped' }) })).not.toContain('pdf-editor');
  });

  it('a computer whose sheet is open keeps its row through a failed read', () => {
    const kept = new Map<string, GitHubSource>();
    const good = okEntry(mac, { protocolRevision: 7, github: { state: 'ready' } });
    expect(githubSources([good], kept)).toHaveLength(1);
    const failed = { computer: mac, status: { kind: 'unreachable' } } as unknown as Parameters<typeof githubSources>[0][number];
    expect(githubSources([failed], kept, new Set([mac.origin])).map((one) => one.computer.label)).toEqual(['mac']);
    expect(githubSources([failed], kept)).toEqual([]);
  });

  it('Integrations with herdr and no GitHub shows only the herdr row', () => {
    const page = renderToStaticMarkup(<IntegrationsSection entries={[okEntry(mac, { protocolRevision: 7, providers: [info()] })]} onFailure={() => ''} />);
    expect(page).toContain('<bdi>herdr</bdi> on mac');
    expect(page).not.toContain('GitHub on');
  });
});
