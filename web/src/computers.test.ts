import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { READ_TIMEOUT_MS } from './api/http';
import {
  COMPUTERS_KEY, confirmComputer, forgetPending, logOut, logoutNote, logoutPeers, PALETTE, previewOriginOf, refreshComputers, removeComputer, renameComputer,
  sessionKey, storedComputers, toComputer,
} from './computers';

const home = 'https://mac.tailx.ts.net';
const vps = { origin: 'https://vps.tailx.ts.net', label: 'vps' };
const pi = { origin: 'https://pi.tailx.ts.net', label: 'Kitchen Pi' };

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const store = new Map<string, string>();
const storage = { getItem: (key: string) => store.get(key) ?? null, setItem: (key: string, value: string) => { store.set(key, value); } };

beforeEach(() => vi.stubGlobal('location', { origin: home }));
afterEach(() => {
  vi.unstubAllGlobals();
  store.clear();
});


/** Fake timers do not drive AbortSignal.timeout; this one runs on them. */
function fakeTimeoutSignals() {
  vi.useFakeTimers();
  vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms) => {
    const controller = new AbortController();
    setTimeout(() => controller.abort(new DOMException('timeout', 'TimeoutError')), ms);
    return controller.signal;
  });
}

describe('computer model', () => {
  it('keys sessions as host/sessionId', () => {
    const computer = toComputer(vps);
    expect(computer).toMatchObject({ host: 'vps.tailx.ts.net', label: 'vps', home: false });
    const key = sessionKey(computer, '00000000-0000-4000-8000-000000000001');
    expect(key).toBe('vps.tailx.ts.net/00000000-0000-4000-8000-000000000001');
  });

  it('frames home from its configured preview origin, and a peer always from its own host on 8443', () => {
    const homeComputer = toComputer({ origin: home, label: 'mac' }, true);
    expect(previewOriginOf(homeComputer, 'http://127.0.0.1:8790')).toBe('http://127.0.0.1:8790');
    expect(previewOriginOf(homeComputer, undefined)).toBeUndefined();
    // A peer needs nothing from home, and nothing a peer sent is used.
    expect(previewOriginOf(toComputer(vps), undefined)).toBe('https://vps.tailx.ts.net:8443');
    expect(previewOriginOf(toComputer(vps), 'https://mac.tailx.ts.net:8443')).toBe('https://vps.tailx.ts.net:8443');
  });

  it('colours computers by place: home keeps the first colour, and no two of 8 computers share one', () => {
    const peers = ['a', 'b', 'c', 'd', 'e', 'f', 'g'].map((name) => ({ origin: `https://${name}.tailx.ts.net`, label: name }));
    store.set(COMPUTERS_KEY, JSON.stringify(peers));
    const computers = storedComputers(storage);
    expect(computers).toHaveLength(8);
    expect(computers[0]).toMatchObject({ home: true, color: PALETTE[0] });
    expect(new Set(computers.map((computer) => computer.color)).size).toBe(8);
    store.set(COMPUTERS_KEY, JSON.stringify([]));
    expect(storedComputers(storage)[0]?.color).toBe(PALETTE[0]);
  });

  it('lists home first, then stored peers; drops bad, duplicate and home entries', () => {
    store.set(COMPUTERS_KEY, JSON.stringify([vps, vps, { origin: home, label: 'mac' }, { origin: 'http://x.tailx.ts.net', label: 'x' }, pi]));
    expect(storedComputers(storage).map((computer) => [computer.label, computer.home])).toEqual([
      ['mac', true], ['vps', false], ['Kitchen Pi', false],
    ]);
    store.set(COMPUTERS_KEY, '{broken');
    expect(storedComputers(storage)).toHaveLength(1);
  });
});

describe('computer list on home', () => {
  it('replaces the stored list on every load from home', async () => {
    store.set(COMPUTERS_KEY, JSON.stringify([pi]));
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { computers: [vps] })));
    const list = await refreshComputers(storage);
    expect(list.map((computer) => computer.origin)).toEqual([home, vps.origin]);
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([vps]);
  });

  it('keeps the stored list when home cannot answer', async () => {
    store.set(COMPUTERS_KEY, JSON.stringify([pi]));
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline'); }));
    await expect(refreshComputers(storage)).rejects.toMatchObject({ code: 'network' });
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([pi]);
  });

  it('renames a confirmed computer on home and here', async () => {
    store.set(COMPUTERS_KEY, JSON.stringify([vps, pi]));
    const fetch = vi.fn(async () => json(200, { computer: { ...vps, label: 'Cloud' }, state: 'confirmed', node: { id: 'n', tags: ['tag:server'] } }));
    vi.stubGlobal('fetch', fetch);
    await expect(renameComputer(vps.origin, 'Cloud', storage)).resolves.toMatchObject({ label: 'Cloud' });
    expect(JSON.parse((fetch.mock.calls[0] as unknown as [string, RequestInit])[1].body as string))
      .toEqual({ origin: vps.origin, label: 'Cloud', state: 'confirmed' });
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([{ ...vps, label: 'Cloud' }, pi]);
  });
});

describe('removeComputer', () => {
  it('signs out on the peer first, then removes it on home and here', async () => {
    store.set(COMPUTERS_KEY, JSON.stringify([vps, pi]));
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      return new Response(null, { status: 204 });
    }));
    await expect(removeComputer(toComputer(vps), storage)).resolves.toEqual({ loggedOut: true });
    expect(calls).toEqual(['POST https://vps.tailx.ts.net/api/v1/logout', 'DELETE /api/v1/computers/vps.tailx.ts.net']);
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([pi]);
  });

  it('counts a 401 from the peer as signed out', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => (url.startsWith('https://')
      ? json(401, { error: { code: 'unauthorized', message: 'x' } }) : new Response(null, { status: 204 }))));
    await expect(removeComputer(toComputer(vps), storage)).resolves.toEqual({ loggedOut: true });
  });

  it('still removes an unreachable peer and says to run pterm revoke there', async () => {
    store.set(COMPUTERS_KEY, JSON.stringify([vps]));
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.startsWith('https://')) throw new TypeError('offline');
      return new Response(null, { status: 204 });
    }));
    const result = await removeComputer(toComputer(vps), storage);
    expect(result).toEqual({ loggedOut: false, hint: expect.stringContaining('pterm revoke on vps') });
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([]);
  });

  it('removes nothing when home cannot remove it', async () => {
    store.set(COMPUTERS_KEY, JSON.stringify([vps]));
    vi.stubGlobal('fetch', vi.fn(async (url: string) => (url.startsWith('https://')
      ? new Response(null, { status: 204 }) : json(500, { error: { code: 'internal_error', message: 'x' } }))));
    await expect(removeComputer(toComputer(vps), storage)).rejects.toMatchObject({ status: 500 });
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([vps]);
  });
});

describe('confirmComputer', () => {
  it('gives a computer it could not store a peer colour, never home\'s', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { computer: vps, state: 'confirmed', node: { id: 'n1', tags: [] } })));
    const refused = { getItem: () => null, setItem: () => { throw new Error('quota'); } };
    const computer = await confirmComputer(vps.origin, vps.label, refused);
    expect(computer.color).not.toBe(PALETTE[0]);
  });
});

describe('forgetPending', () => {
  it('deletes the pending entry on home, but never a computer already in the list', async () => {
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      return new Response(null, { status: 204 });
    }));
    store.set(COMPUTERS_KEY, JSON.stringify([pi]));
    await forgetPending(vps.origin, storage);
    await forgetPending(pi.origin, storage);
    expect(calls).toEqual(['DELETE /api/v1/computers/vps.tailx.ts.net']);
  });

  it('never rejects: the entry expires on home', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline'); }));
    await expect(forgetPending(vps.origin, storage)).resolves.toBeUndefined();
  });
});

describe('logoutPeers', () => {
  it('signs out on each reachable peer, skips unreachable ones, and names every peer still signed in', async () => {
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      if (url.startsWith('https://down.')) throw new TypeError('offline');
      if (url.startsWith('https://gone.')) return json(401, { error: { code: 'revoked', message: 'x' } });
      return new Response(null, { status: 204 });
    }));
    const peer = (name: string, label = name) => toComputer({ origin: `https://${name}.tailx.ts.net`, label });
    const left = await logoutPeers([
      { computer: toComputer({ origin: home, label: 'mac' }, true), reachable: true },
      { computer: peer('vps'), reachable: true },
      { computer: peer('gone'), reachable: true },
      { computer: peer('down', 'Build box'), reachable: true },
      { computer: peer('pi', 'Kitchen Pi'), reachable: false },
    ]);
    expect(left).toEqual(['Build box', 'Kitchen Pi']);
    expect(calls).toEqual([
      'POST https://vps.tailx.ts.net/api/v1/logout',
      'POST https://gone.tailx.ts.net/api/v1/logout',
      'POST https://down.tailx.ts.net/api/v1/logout',
    ]);
    expect(logoutNote([])).toBe('You logged out. This phone no longer has access.');
    expect(logoutNote(left)).toBe(
      'You logged out. This phone no longer has access. Could not sign out of Build box, Kitchen Pi. Run pterm revoke there to remove this phone.',
    );
  });
});

describe('log out', () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  it('waits at most READ_TIMEOUT_MS for a peer that never answers, and signs out of the peers at once', async () => {
    fakeTimeoutSignals();
    const started: string[] = [];
    vi.stubGlobal('fetch', vi.fn((url: string, init: RequestInit) => {
      started.push(url);
      return new Promise<Response>((_, reject) => {
        init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
    }));
    const peer = (name: string) => toComputer({ origin: `https://${name}.tailx.ts.net`, label: name });
    let left: string[] | undefined;
    void logoutPeers([{ computer: peer('vps'), reachable: true }, { computer: peer('pi'), reachable: true }]).then((labels) => { left = labels; });
    await vi.advanceTimersByTimeAsync(0);
    expect(started).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(READ_TIMEOUT_MS);
    expect(left).toEqual(['vps', 'pi']);
  });

  it('forgets the stored computer list once home logged out, and keeps it when home could not', async () => {
    const local = new Map([[COMPUTERS_KEY, JSON.stringify([vps])]]);
    const memory = { removeItem: (key: string) => { local.delete(key); } };
    vi.stubGlobal('fetch', vi.fn(async () => json(500, { error: { code: 'internal', message: 'Home failed' } })));
    await expect(logOut([], memory)).rejects.toMatchObject({ status: 500 });
    expect(local.has(COMPUTERS_KEY)).toBe(true);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })));
    await expect(logOut([], memory)).resolves.toBe('You logged out. This phone no longer has access.');
    expect(local.has(COMPUTERS_KEY)).toBe(false);
  });
});
