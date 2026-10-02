import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  colorFor, COMPUTERS_KEY, PALETTE, parseSessionKey, refreshComputers, removeComputer, renameComputer, sessionKey,
  storedComputers, toComputer,
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

describe('computer model', () => {
  it('keys sessions as host/sessionId', () => {
    const computer = toComputer(vps);
    expect(computer).toMatchObject({ host: 'vps.tailx.ts.net', label: 'vps', home: false });
    const key = sessionKey(computer, '00000000-0000-4000-8000-000000000001');
    expect(key).toBe('vps.tailx.ts.net/00000000-0000-4000-8000-000000000001');
    expect(parseSessionKey(key)).toEqual({ host: 'vps.tailx.ts.net', sessionId: '00000000-0000-4000-8000-000000000001' });
    expect(parseSessionKey('no-slash')).toBeUndefined();
    expect(parseSessionKey('/id')).toBeUndefined();
    expect(parseSessionKey('host/')).toBeUndefined();
  });

  it('gives each origin a fixed palette colour', () => {
    expect(colorFor(vps.origin)).toBe(colorFor(vps.origin));
    expect(PALETTE).toContain(colorFor(vps.origin));
    const colours = new Set(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'].map((name) => colorFor(`https://${name}.tailx.ts.net`)));
    expect(colours.size).toBeGreaterThan(1);
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
