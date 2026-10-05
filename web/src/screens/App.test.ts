import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, client } from '../api/http';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { hosted } from './allSessions';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  AddBanner, askWorkerTap, guardLink, guardStartup, homeAdoption, homePreview, leaveRemoved, notificationPath, providerPath, offlineRetry, parseRoute, pathFor, peerPreviewOnLoad, PREVIEW_TAP, previewFor, sessionPath, startApp, startLike, installView,
  STRAY_RELOAD_KEY, takeStartupLinks, titleFor, newTap, providerArrival, providerTabView, sameScreen, queueNext, queueOnArrival, queueScreen,
} from './App';
import { inboxItems } from './Inbox';
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

describe('Install opens Live with the command typed, not run (installView)', () => {
  it('always asks for typeOnly: the user presses Enter', () => {
    const session = hosted({ id: 'new', projectId: 'project-1', shellProfileId: 'zsh' } as BrokerRPCSessionRecord, vps);
    expect(installView(session, 'curl -fsSL https://herdr.dev/install.sh | sh')).toEqual({
      name: 'live', session, runFirst: { command: 'curl -fsSL https://herdr.dev/install.sh | sh', settled: false, typeOnly: true },
    });
  });
});

describe('routes', () => {
  const id = '84444444-4444-4444-8444-444444444444';

  it('keeps /sessions/<id> for home, so push links keep working, and uses /c/<host>/sessions/<id> for a peer', () => {
    expect(sessionPath({ id, computer: home })).toBe(`/sessions/${id}`);
    expect(sessionPath({ id, computer: vps })).toBe(`/c/vps.tailx.ts.net/sessions/${id}`);
    expect(pathFor({ name: 'live', session: hosted({ id } as BrokerRPCSessionRecord, vps) })).toBe(`/c/vps.tailx.ts.net/sessions/${id}`);
  });

  it('gives a preview its address beside the session: home as before, a peer under /c/<host>', () => {
    expect(pathFor({ name: 'preview', session: hosted({ id } as BrokerRPCSessionRecord, home), port: 5173 })).toBe(`/sessions/${id}/preview?port=5173`);
    const path = pathFor({ name: 'preview', session: hosted({ id } as BrokerRPCSessionRecord, vps), port: 5173 });
    expect(path).toBe(`/c/vps.tailx.ts.net/sessions/${id}/preview?port=5173`);
    expect(parseRoute(path.slice(0, path.indexOf('?')))).toEqual({ name: 'session', host: 'vps.tailx.ts.net', id, preview: true });
  });

  it('accepts a notification message only for a session of home or a listed peer, by lowercase UUID', () => {
    const list = [home, vps];
    const open = (path: unknown) => notificationPath({ type: 'open', path }, list);
    expect(open(`/sessions/${id}`)).toBe(`/sessions/${id}`);
    expect(open(`/c/vps.tailx.ts.net/sessions/${id}`)).toBe(`/c/vps.tailx.ts.net/sessions/${id}`);
    // Built here, never taken as sent: an escaped spelling comes back in the plain form.
    expect(open(`/c/vps%2Etailx.ts.net/sessions/${id}`)).toBe(`/c/vps.tailx.ts.net/sessions/${id}`);
    for (const path of [
      '/', '/settings', '/wall', `/sessions/${id}/preview`, '/sessions/AB444444-4444-4444-8444-44444444444C', '/sessions/abc', '/sessions/..%2Fsettings',
      `/c/vps.tailx.ts.net/sessions/${id}/preview`, `/c/evil.tailx.ts.net/sessions/${id}`, `/c/${home.host}/sessions/${id}`, `//evil.example/sessions/${id}`,
      `https://evil.example/sessions/${id}`, `/sessions/${id}?x=1`, `/sessions/${id}#x`, 42, undefined,
    ]) expect(open(path)).toBeUndefined();
    for (const data of [null, 'open', 42, { type: 'skip-waiting', path: `/sessions/${id}` }, { path: `/sessions/${id}` }]) {
      expect(notificationPath(data, list)).toBeUndefined();
    }
    // Before the list has the peer (or after it was removed), its links do nothing.
    expect(notificationPath({ type: 'open', path: `/c/vps.tailx.ts.net/sessions/${id}` }, [home])).toBeUndefined();
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
    for (const path of ['/c/Evil Host/sessions/x', '/c/vps/sessions/', '/c/a%ZZ/sessions/x', '/c/vps/sessions/x/preview/y', '/c/vps/sessions//preview', '/nope', '/sessions/%E0%A4%A']) {
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
    expect(links.add).toEqual({ origin: 'https://vps.tailx.ts.net', grant, back: '/' });
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

describe('no sessionStorage (guardStartup, guardLink)', () => {
  const broken = {
    getItem: () => { throw new DOMException('denied', 'SecurityError'); },
    setItem: () => { throw new DOMException('denied', 'SecurityError'); },
    removeItem: () => { throw new DOMException('denied', 'SecurityError'); },
  };
  const slots = (grant?: string) => ({ grant: { value: grant }, add: { value: undefined } });

  it('storage that throws still names the link that was not used, and never reloads', () => {
    const unsafe = slots('g'.repeat(43));
    expect(guardStartup(unsafe, false, broken)).toEqual({ safe: false, reload: false, refused: 'pair' });
    expect(unsafe.grant.value).toBeUndefined();
  });

  it('storage that throws keeps the grant on a safe load', () => {
    const safe = slots('g'.repeat(43));
    expect(guardStartup(safe, true, broken)).toEqual({ safe: true, reload: false, refused: undefined });
    expect(safe.grant.value).toBe('g'.repeat(43));
  });

  it('a new link in the same tab after an unsafe start is used once the workers are safe, without a reload', async () => {
    const later = slots('h'.repeat(43));
    expect(await guardLink(later, true, async () => true, undefined)).toEqual({ safe: true, reload: false, refused: undefined });
    expect(later.grant.value).toBe('h'.repeat(43));
    const still = slots('h'.repeat(43));
    expect(await guardLink(still, true, async () => false, undefined)).toEqual({ safe: false, reload: false, refused: 'pair' });
    expect(still.grant.value).toBeUndefined();
    const check = vi.fn(async () => false);
    expect(await guardLink(slots('h'.repeat(43)), false, check, undefined)).toMatchObject({ safe: true });
    expect(check).not.toHaveBeenCalled();
  });
});

describe('preview link before home answered (homePreview)', () => {
  it('reads the preview config when a preview link asks first, once, and a failed read means no previews', async () => {
    const seen: Array<string | undefined> = [];
    const load = vi.fn(async () => ({ origin: 'https://mac.tailx.ts.net:8443/' }));
    const preview = homePreview((origin) => seen.push(origin), load);
    expect(await preview.origin()).toBe('https://mac.tailx.ts.net:8443');
    expect(await preview.origin()).toBe('https://mac.tailx.ts.net:8443');
    expect(load).toHaveBeenCalledOnce();
    expect(seen).toEqual(['https://mac.tailx.ts.net:8443']);
    const down = homePreview(() => undefined, async () => { throw new ApiError(0, 'network', 'x'); });
    expect(await down.origin()).toBeUndefined();
  });
});

describe('where a preview is framed from (previewFor)', () => {
  const ok = (protocolRevision: number) => ({ kind: 'ok' as const, settings: { protocolRevision } as HTTPSettingsResponse, sessions: [], at: 1 });

  it('home uses its configured origin, whatever its poll says; none means no previews', () => {
    expect(previewFor(home, undefined, 'https://mac.tailx.ts.net:8443')).toEqual({ origin: 'https://mac.tailx.ts.net:8443' });
    expect(previewFor(home, { kind: 'unreachable' }, 'http://127.0.0.1:8790')).toEqual({ origin: 'http://127.0.0.1:8790' });
    expect(previewFor(home, ok(2), undefined).origin).toBeUndefined();
  });

  it('a peer at revision 2 or higher is framed from its own host on 8443, with home\'s previews off or on', () => {
    expect(previewFor(vps, ok(2), undefined)).toEqual({ origin: 'https://vps.tailx.ts.net:8443' });
    expect(previewFor(vps, ok(3), 'https://mac.tailx.ts.net:8443')).toEqual({ origin: 'https://vps.tailx.ts.net:8443' });
  });

  it('a peer below revision 2 has no preview: no chip, and an opened link says to update', () => {
    expect(previewFor(vps, ok(1), 'https://mac.tailx.ts.net:8443')).toEqual({ notice: 'Update Termivoa on vps' });
  });

  it('a peer has a frame only while its own poll answers: otherwise the existing text, or nothing yet', () => {
    expect(previewFor(vps, { kind: 'pair' }, undefined)).toEqual({ notice: 'vps · pair again' });
    expect(previewFor(vps, { kind: 'unreachable', lastSeen: 1 }, undefined)).toEqual({ notice: 'vps · can\'t reach' });
    expect(previewFor(vps, { kind: 'outdated', message: 'Update Termivoa on vps' }, undefined)).toEqual({ notice: 'Update Termivoa on vps' });
    expect(previewFor(vps, { kind: 'loading' }, undefined)).toEqual({});
    expect(previewFor(vps, undefined, 'https://mac.tailx.ts.net:8443')).toEqual({});
  });
});

describe('home adoption (homeAdoption)', () => {
  const answer = { device: { id: 'd', label: 'Phone' } } as unknown as HTTPSettingsResponse;
  const flush = () => new Promise((resolve) => { setTimeout(resolve, 0); });

  it('uses the settings once, and reads the list again on the next good answer after a failed read', async () => {
    const settings = vi.fn();
    const list = vi.fn();
    const readList = vi.fn().mockRejectedValueOnce(new ApiError(0, 'network', 'x')).mockResolvedValue([home, vps]);
    const adoption = homeAdoption({ settings, list, readList });
    adoption.answer(answer);
    await flush();
    expect(list).not.toHaveBeenCalled();
    adoption.answer(answer);
    await flush();
    expect(list).toHaveBeenCalledWith([home, vps]);
    adoption.answer(answer);
    await flush();
    expect(readList).toHaveBeenCalledTimes(2);
    expect(settings).toHaveBeenCalledOnce();
  });

  it('after a re-pair or log out (reset), adopts home again; a read from before the reset is dropped', async () => {
    const settings = vi.fn();
    const list = vi.fn();
    let finish: ((value: Computer[]) => void) | undefined;
    const readList = vi.fn(() => new Promise<Computer[]>((resolve) => { finish = resolve; }));
    const adoption = homeAdoption({ settings, list, readList });
    adoption.answer(answer);
    const stale = finish;
    adoption.reset();
    adoption.answer(answer);
    stale?.([home, vps]);
    await flush();
    expect(list).not.toHaveBeenCalled();
    finish?.([home]);
    await flush();
    expect(list).toHaveBeenCalledWith([home]);
    expect(settings).toHaveBeenCalledTimes(2);
  });
});

describe('leaveRemoved', () => {
  const session = hosted({ id: 's1' } as BrokerRPCSessionRecord, vps);

  it('leaves a Live view whose computer home\'s new list dropped, with a short notice', () => {
    expect(leaveRemoved({ name: 'live', session }, [home])).toEqual({ name: 'sessions', message: 'vps is no longer in your list.' });
    expect(leaveRemoved({ name: 'preview', session, port: 5173 }, [home])).toEqual({ name: 'sessions', message: 'vps is no longer in your list.' });
    expect(leaveRemoved({ name: 'live', session }, [home, vps])).toBeUndefined();
    expect(leaveRemoved({ name: 'settings' }, [home])).toBeUndefined();
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

describe('add banner (AddBanner)', () => {
  const banner = (task: Parameters<typeof AddBanner>[0]['task']) => renderToStaticMarkup(createElement(AddBanner, { task, onOpen: () => undefined }));
  const pending = { pendingId: 'p', verificationCode: '123-456', expiresAt: 0 };

  it('while the add waits on another screen: says where to approve, with Open and Cancel', () => {
    const html = banner({ origin: vps.origin, label: 'vps', phase: { kind: 'waiting', pending } });
    expect(html).toContain('Adding vps… approve on vps');
    expect(html).toContain('>Open</button>');
    expect(html).toContain('>Cancel</button>');
  });

  it('says when it finished in the background, and hides once dismissed or cancelled', () => {
    const done = { origin: vps.origin, label: 'vps', phase: { kind: 'done' as const, computer: vps } };
    expect(banner(done)).toContain('vps is added');
    expect(banner(done)).not.toContain('>Open</button>');
    expect(banner({ ...done, dismissed: true })).toBe('');
    expect(banner({ origin: vps.origin, label: 'vps', phase: { kind: 'cancelled' } })).toBe('');
    expect(banner({ origin: vps.origin, label: 'vps', phase: { kind: 'expired' } })).toContain('vps was not added');
  });
});

describe('a peer preview address on load', () => {
  const previews = [{ port: 5173 }];

  it('opens only from an entry a tap in this app made, for a port the session lists', () => {
    expect(peerPreviewOnLoad(PREVIEW_TAP, previews, 5173)).toBe(true);
    // The marker as the browser gives it back after a reload (a structured clone).
    expect(peerPreviewOnLoad(structuredClone(PREVIEW_TAP), previews, 5173)).toBe(true);
  });

  it('a link from outside (no state), other state, or a port the session does not list opens the terminal', () => {
    for (const state of [null, undefined, 'previewTap', {}, { previewTap: 'true' }, { previewTap: 1 }]) {
      expect(peerPreviewOnLoad(state, previews, 5173)).toBe(false);
    }
    expect(peerPreviewOnLoad(PREVIEW_TAP, previews, 3000)).toBe(false);
    expect(peerPreviewOnLoad(PREVIEW_TAP, [], 5173)).toBe(false);
    expect(peerPreviewOnLoad(PREVIEW_TAP, undefined, 5173)).toBe(false);
  });
});

describe('provider addresses', () => {
  const target = 'w8:p1.term_65d0031602b777';
  const encoded = encodeURIComponent(target);

  it('are /p/<provider>/<target> on home and /c/<host>/p/… on a peer, and read back', () => {
    expect(providerPath(home, 'herdr', target)).toBe(`/p/herdr/${encoded}`);
    expect(providerPath(vps, 'herdr', target)).toBe(`/c/vps.tailx.ts.net/p/herdr/${encoded}`);
    expect(pathFor({ name: 'provider', computer: vps, provider: 'herdr', target, tapped: 1 })).toBe(`/c/vps.tailx.ts.net/p/herdr/${encoded}`);
    expect(parseRoute(`/p/herdr/${encoded}`)).toEqual({ name: 'provider', host: undefined, provider: 'herdr', target });
    expect(parseRoute(`/c/vps.tailx.ts.net/p/herdr/${encoded}`)).toEqual({ name: 'provider', host: 'vps.tailx.ts.net', provider: 'herdr', target });
    expect(titleFor({ name: 'provider', computer: home, provider: 'herdr', target, tapped: 0 })).toBe('herdr · Termivoa');
    for (const path of ['/p/Herdr/w8', '/p/herdr/..%2Fx', '/p/herdr/a%2Fb', '/p/herdr/', '/p/herdr/w8/x', '/c/Evil Host/p/herdr/w8', '/p/herdr/%ZZ', `/p/${'a'.repeat(33)}/w8`]) {
      expect(parseRoute(path), path).toEqual({ name: 'unknown' });
    }
  });

  it('a notification message may open a provider terminal of home or a listed peer, rebuilt here', () => {
    const list = [home, vps];
    expect(notificationPath({ type: 'open', path: `/p/herdr/${encoded}` }, list)).toBe(`/p/herdr/${encoded}`);
    expect(notificationPath({ type: 'open', path: `/c/vps%2Etailx.ts.net/p/herdr/${target}` }, list)).toBe(`/c/vps.tailx.ts.net/p/herdr/${encoded}`);
    expect(notificationPath({ type: 'open', path: `/c/evil.tailx.ts.net/p/herdr/${encoded}` }, list)).toBeUndefined();
    expect(notificationPath({ type: 'open', path: '/p/herdr/..' }, list)).toBeUndefined();
  });

  it('a notification tap opens at once only when the worker confirms it, and each tap is new, also for the same address (route → askWorkerTap → tapped)', async () => {
    const path = `/p/herdr/${encoded}`;
    const asked: string[] = [];
    const yes = async (at: string) => { asked.push(at); return true; };
    const first = await providerArrival(home, 'herdr', target, path, yes);
    const second = await providerArrival(home, 'herdr', target, path, yes);
    expect(asked).toEqual([path, path]);
    expect(first).toMatchObject({ name: 'provider', computer: home, provider: 'herdr', target });
    const taps = [first, second].map((view) => (view.name === 'provider' ? view.tapped : -1));
    expect(taps[0]).toBeGreaterThan(0);
    // Same address, same screen key: only a new number tells the screen there was a second tap.
    expect(taps[1]).not.toBe(taps[0]);
    // A link or a reload (no fresh tap): Open first.
    expect(await providerArrival(home, 'herdr', target, path, async () => false)).toMatchObject({ tapped: 0 });
    expect(newTap()).toBeGreaterThan(taps[1] ?? 0);
  });

  it('a chip replaces the history entry: tabs of one workspace are one screen', () => {
    const at = (computer: Computer, id: string) => ({ name: 'provider', computer, provider: 'herdr', target: id, tapped: 1 }) as const;
    expect(sameScreen(at(home, 'w8:p1.a'), at(home, 'w8:p2.b'))).toBe(true);
    expect(sameScreen(at(home, 'w8:p1.a'), at(vps, 'w8:p2.b'))).toBe(false);
    expect(sameScreen({ name: 'sessions' }, at(home, 'w8:p2.b'))).toBe(false);
    expect(sameScreen(at(home, 'w8:p1.a'), { name: 'sessions' })).toBe(false);
  });

  it('goes back to the provider\'s tab: "‹ Sessions", a close the user asked for (no message), and a terminal that left (with one)', () => {
    expect(providerTabView('herdr')).toEqual({ name: 'sessions', tab: 'herdr' });
    expect(providerTabView('herdr', 'That terminal is gone.')).toEqual({ name: 'sessions', tab: 'herdr', message: 'That terminal is gone.' });
    // The tab is state, not an address: Back from it never lands on a dead terminal's path.
    expect(pathFor(providerTabView('herdr'))).toBe('/');
    expect(sameScreen({ name: 'provider', computer: home, provider: 'herdr', target, tapped: 1 }, providerTabView('herdr'))).toBe(false);
  });

  it('leaves a provider terminal whose computer left the list', () => {
    expect(leaveRemoved({ name: 'provider', computer: vps, provider: 'herdr', target, tapped: 0 }, [home])).toEqual({ name: 'sessions', message: 'vps is no longer in your list.' });
    expect(leaveRemoved({ name: 'provider', computer: vps, provider: 'herdr', target, tapped: 0 }, [home, vps])).toBeUndefined();
  });
});

describe('askWorkerTap', () => {
  afterEach(() => { vi.useRealTimers(); });
  /** A worker that answers on the port it was sent, like sw.js. */
  const worker = (answer: (path: string) => unknown) => ({
    postMessage: vi.fn((message: unknown, ports: Transferable[]) => { (ports[0] as MessagePort | undefined)?.postMessage(answer((message as { path: string }).path)); }),
  });

  it('asks the worker with the page\'s path and believes only a fresh tap for it', async () => {
    const yes = worker(() => ({ type: 'tap', fresh: true }));
    await expect(askWorkerTap('/p/herdr/w8', yes)).resolves.toBe(true);
    expect(yes.postMessage.mock.calls[0]?.[0]).toEqual({ type: 'tap?', path: '/p/herdr/w8' });
    await expect(askWorkerTap('/p/herdr/w8', worker(() => ({ type: 'tap', fresh: false })))).resolves.toBe(false);
    await expect(askWorkerTap('/p/herdr/w8', worker(() => ({ type: 'tap', fresh: 'yes' })))).resolves.toBe(false);
    await expect(askWorkerTap('/p/herdr/w8', worker(() => ({ type: 'open', fresh: true })))).resolves.toBe(false);
  });

  it('shows Open when no worker controls the page, or it does not answer in time', async () => {
    await expect(askWorkerTap('/p/herdr/w8', null)).resolves.toBe(false);
    await expect(askWorkerTap('/p/herdr/w8', undefined)).resolves.toBe(false);
    vi.useFakeTimers();
    const silent = askWorkerTap('/p/herdr/w8', { postMessage: vi.fn() }, 1_500);
    await vi.advanceTimersByTimeAsync(1_500);
    await expect(silent).resolves.toBe(false);
  });
});

describe('the Needs you queue', () => {
  const waiting = (computer: Computer, id: string, since: string) => hosted({
    id, generation: 'g', title: id, projectId: 'p', shellProfileId: 's', state: 'running', columns: 80, rows: 24, createdAt: since, updatedAt: since,
    agent: { state: 'waiting', stateSince: since },
  }, computer);
  const a = waiting(home, 'a', '2026-10-04T09:00:00Z');
  const b = waiting(vps, 'b', '2026-10-04T09:10:00Z');
  const c = waiting(home, 'c', '2026-10-04T09:20:00Z');

  it('a notification tap the worker confirms starts it with nothing shown; a link, a reload or Back does not', async () => {
    const asked: string[] = [];
    await expect(queueOnArrival('/sessions/x', async (path) => { asked.push(path); return true; })).resolves.toEqual(new Set());
    await expect(queueOnArrival('/sessions/x', async () => false)).resolves.toBeUndefined();
    expect(asked).toEqual(['/sessions/x']);
  });

  it('runs only while a terminal shows', () => {
    expect((['live', 'provider', 'preview'] as const).every(queueScreen)).toBe(true);
    expect((['sessions', 'wall', 'new', 'settings', 'setup', 'boot', 'offline', 'add-computer'] as const).some(queueScreen)).toBe(false);
  });

  it('from a notification tap on b: Next goes to a, then c, then the inbox; each shown item is remembered', () => {
    const items = inboxItems([a, b, c], [], {});
    const first = queueNext(items, new Set(), { session: b });
    expect(first).toMatchObject({ left: 2, next: { names: ['a'] } });
    expect(first.visited).toEqual(new Set(['s:vps.tailx.ts.net/b/g', 's:mac.tailx.ts.net/a/g']));
    const second = queueNext(items, first.visited, { session: a });
    expect(second).toMatchObject({ left: 1, next: { names: ['c'] } });
    // On the last one nothing is left, also while a and b still wait (they were shown): back to the inbox.
    expect(queueNext(items, second.visited, { session: c })).toMatchObject({ left: 0, next: undefined });
  });

  it('counts an item the user answered as gone, and does not count the one on screen', () => {
    const items = inboxItems([b, c], [], {});
    // a was answered (it is not in the lists any more) and is still on screen.
    expect(queueNext(items, new Set(['s:mac.tailx.ts.net/a/g']), { session: a })).toMatchObject({ left: 2, next: { names: ['b'] } });
    expect(queueNext(items, new Set(), undefined)).toMatchObject({ left: 2 });
  });
});
