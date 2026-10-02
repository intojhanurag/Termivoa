import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, client } from '../api/http';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { hosted } from './allSessions';
import {
  guardStartup, offlineRetry, parseRoute, pathFor, sessionPath, startApp, startLike, STRAY_RELOAD_KEY, takeStartupLinks, titleFor,
} from './App';
import { ApiError } from '../api/http';
import type { HTTPSettingsResponse } from '../generated/protocol';

// Outside a browser the home origin is '', so the home client is client('').
const home: Computer = { origin: '', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };

describe('offlineRetry', () => {
  it('keeps "connecting" for the first failure, then shows offline', () => {
    expect(offlineRetry(1).offline).toBe(false);
    expect(offlineRetry(2).offline).toBe(true);
    expect(offlineRetry(9).offline).toBe(true);
  });

  it('backs off 1, 2, 4, 8 seconds, then retries every 10', () => {
    expect([1, 2, 3, 4, 5, 6, 50].map((failures) => offlineRetry(failures).retryIn)).toEqual([1, 2, 4, 8, 10, 10, 10]);
  });
});

describe('wall route', () => {
  it('has its own address and tab title, so browser back works', () => {
    expect(pathFor({ name: 'wall' })).toBe('/wall');
    expect(titleFor({ name: 'wall' })).toBe('Wall · Termivoa');
  });
});

describe('run in new tab', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  const from = hosted({ id: 'old', projectId: 'project-1', shellProfileId: 'zsh' } as BrokerRPCSessionRecord, home);
  const raw = { id: 'new', projectId: 'project-1', shellProfileId: 'zsh' } as BrokerRPCSessionRecord;
  const created = hosted(raw, home);

  it('creates a session in the same project and shell that runs the command first, once', async () => {
    const create = vi.spyOn(api, 'createSession').mockResolvedValue({ session: raw } as Awaited<ReturnType<typeof api.createSession>>);
    expect(await startLike(from, 'pnpm test')).toEqual({
      name: 'live', session: created, runFirst: { command: 'pnpm test', settled: false },
    });
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ projectId: 'project-1', shellProfileId: 'zsh' }));
    expect(await startLike(from)).toEqual({ name: 'live', session: created });
  });

  it('asks for a worktree of the source session only when one is given', async () => {
    const create = vi.spyOn(api, 'createSession').mockResolvedValue({ session: raw } as Awaited<ReturnType<typeof api.createSession>>);
    expect(await startLike(from, 'claude', { name: 'brave-otter', base: 'main' })).toEqual({
      name: 'live', session: created, runFirst: { command: 'claude', settled: false },
    });
    expect(create).toHaveBeenLastCalledWith(expect.objectContaining({
      projectId: 'project-1', shellProfileId: 'zsh', worktree: { sourceSessionId: 'old', name: 'brave-otter', base: 'main' },
    }));
    await startLike(from);
    expect(create.mock.lastCall?.[0]).not.toHaveProperty('worktree');
  });
});

describe('routes', () => {
  const id = '84444444-4444-4444-8444-444444444444';

  it('keeps /sessions/<id> for home, so push links keep working, and uses /c/<host>/sessions/<id> for a peer', () => {
    expect(sessionPath({ id, computer: home })).toBe(`/sessions/${id}`);
    expect(sessionPath({ id, computer: vps })).toBe(`/c/vps.tailx.ts.net/sessions/${id}`);
    expect(pathFor({ name: 'live', session: hosted({ id } as BrokerRPCSessionRecord, vps) })).toBe(`/c/vps.tailx.ts.net/sessions/${id}`);
  });

  it('reads every app address back', () => {
    expect(parseRoute(`/sessions/${id}`)).toEqual({ name: 'session', host: undefined, id, preview: false });
    expect(parseRoute(`/sessions/${id}/preview`)).toEqual({ name: 'session', host: undefined, id, preview: true });
    expect(parseRoute(`/c/vps.tailx.ts.net/sessions/${id}`)).toEqual({ name: 'session', host: 'vps.tailx.ts.net', id, preview: false });
    expect(parseRoute('/c/vps.tailx.ts.net%3A8443/sessions/x')).toEqual({ name: 'session', host: 'vps.tailx.ts.net:8443', id: 'x', preview: false });
    expect(parseRoute('/')).toEqual({ name: 'sessions' });
    expect(parseRoute('/wall')).toEqual({ name: 'wall' });
    expect(parseRoute('/computers/add')).toEqual({ name: 'add-computer' });
    expect(parseRoute('/add-computer')).toEqual({ name: 'peer-add' });
    for (const path of ['/c/Evil Host/sessions/x', '/c/vps/sessions/', '/c/a%ZZ/sessions/x', '/c/vps/sessions/x/preview', '/nope', '/sessions/%E0%A4%A']) {
      expect(parseRoute(path), path).toEqual({ name: 'unknown' });
    }
  });
});

describe('startup links', () => {
  const app = { origin: 'https://mac.tailx.ts.net', hostname: 'mac.tailx.ts.net' };
  const grant = 'G'.repeat(43);
  const run = (pathname: string, hash: string) => {
    const history = { replaceState: vi.fn() };
    return { links: takeStartupLinks({ pathname, hash, search: '' }, history, app), history };
  };

  it('takes the add-computer fragment before the pairing grant strips fragments', () => {
    const { links, history } = run('/computers/add', `#grant=${grant}&origin=${encodeURIComponent('https://vps.tailx.ts.net')}`);
    expect(links.add).toEqual({ origin: 'https://vps.tailx.ts.net', grant });
    expect(links.grant.value).toBeUndefined();
    expect(history.replaceState).toHaveBeenCalledWith(null, '', '/computers/add');
  });

  it('keeps the peer page link in memory only and strips it from the address', () => {
    const hash = `#grant=${grant}&via=${encodeURIComponent(app.origin)}`;
    const { links, history } = run('/add-computer', hash);
    expect(links.peerLink).toBe(`${app.origin}/add-computer${hash}`);
    expect(links.grant.value).toBeUndefined();
    expect(links.add).toBeUndefined();
    expect(history.replaceState).toHaveBeenCalledWith(null, '', '/add-computer');
  });

  it('still takes a pairing grant on /pair', () => {
    expect(run('/pair', `#grant=${grant}`).links).toEqual({ grant: { value: grant }, add: undefined, peerLink: undefined });
  });
});

describe('start on the same computer', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('creates the new session on the computer of the session it came from', async () => {
    const peer = client(vps.origin);
    const create = vi.spyOn(peer, 'createSession').mockResolvedValue({ session: { id: 'n' } } as Awaited<ReturnType<typeof api.createSession>>);
    const onHome = vi.spyOn(api, 'createSession');
    const view = await startLike(hosted({ id: 'o', projectId: 'p', shellProfileId: 'z' } as BrokerRPCSessionRecord, vps));
    expect(create).toHaveBeenCalledOnce();
    expect(onHome).not.toHaveBeenCalled();
    expect(view).toMatchObject({ name: 'live', session: { id: 'n', computer: vps } });
  });
});

describe('stray service worker at start (guardStartup)', () => {
  const memory = () => {
    const map = new Map<string, string>();
    return { map, getItem: (key: string) => map.get(key) ?? null, setItem: (key: string, value: string) => { map.set(key, value); }, removeItem: (key: string) => { map.delete(key); } };
  };
  const addLink = { origin: 'https://vps.tailx.ts.net', grant: 'G'.repeat(43) };

  it('a stray controller means no redeem: drops the grant and the add link, then reloads once', () => {
    const store = memory();
    const slots = { grant: { value: 'g'.repeat(43) as string | undefined }, add: { value: undefined as typeof addLink | undefined } };
    expect(guardStartup(slots, false, store)).toEqual({ safe: false, reload: true, refused: 'pair' });
    expect(slots.grant.value).toBeUndefined();
    expect(store.map.get(STRAY_RELOAD_KEY)).toBe('pair');

    const add = { grant: { value: undefined as string | undefined }, add: { value: addLink as typeof addLink | undefined } };
    expect(guardStartup(add, false, memory())).toMatchObject({ reload: true, refused: 'add' });
    expect(add.add.value).toBeUndefined();
  });

  it('after the reload: says which link was not used, and never reloads twice in a row', () => {
    const store = memory();
    store.setItem(STRAY_RELOAD_KEY, 'add');
    const empty = () => ({ grant: { value: undefined }, add: { value: undefined } });
    expect(guardStartup(empty(), false, store)).toEqual({ safe: false, reload: false, refused: 'add' });
    expect(guardStartup(empty(), true, store)).toEqual({ safe: true, reload: false, refused: 'add' });
    expect(store.map.has(STRAY_RELOAD_KEY)).toBe(false);
    expect(guardStartup(empty(), true, store)).toEqual({ safe: true, reload: false, refused: undefined });
  });

  it('keeps the grant on a safe load, and never reloads without storage for the guard', () => {
    const slots = { grant: { value: 'g'.repeat(43) as string | undefined }, add: { value: undefined } };
    expect(guardStartup(slots, true, memory())).toEqual({ safe: true, reload: false, refused: undefined });
    expect(slots.grant.value).toBe('g'.repeat(43));
    expect(guardStartup(slots, false, undefined)).toEqual({ safe: false, reload: false, refused: 'pair' });
    expect(slots.grant.value).toBeUndefined();
  });
});

describe('startApp', () => {
  const answer = { device: { id: 'd', label: 'Phone' } } as unknown as HTTPSettingsResponse;
  const deps = (computers: number, homeSettings: () => Promise<HTTPSettingsResponse>) => {
    const calls: string[] = [];
    return {
      calls,
      run: startApp({
        computers,
        homeSettings,
        onHome: () => calls.push('home'),
        onSetup: () => calls.push('setup'),
        onOffline: () => calls.push('offline'),
        route: async () => { calls.push('route'); },
        sleep: async () => { calls.push('sleep'); },
      }),
    };
  };

  it('with peers stored, opens at once even while home never answers', async () => {
    const homeSettings = vi.fn(() => new Promise<HTTPSettingsResponse>(() => undefined));
    const { calls, run } = deps(2, homeSettings);
    await run;
    expect(calls).toEqual(['route']);
    expect(homeSettings).not.toHaveBeenCalled();
  });

  it('with one computer, waits for home as before: Setup on 401, offline after the grace', async () => {
    const ok = deps(1, async () => answer);
    await ok.run;
    expect(ok.calls).toEqual(['home', 'route']);
    const auth = deps(1, async () => { throw new ApiError(401, 'unauthorized', 'x'); });
    await auth.run;
    expect(auth.calls).toEqual(['setup']);
    const down = deps(1, async () => { throw new ApiError(0, 'network', 'x'); });
    await down.run;
    expect(down.calls).toEqual(['sleep', 'offline']);
  });
});
