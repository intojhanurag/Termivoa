import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { COMPUTERS_KEY } from '../computers';
import { AddComputerFlow, addComputerUrl, parseAddComputerLink, takeAddComputerFragment } from './addComputer';

const app = { origin: 'https://mac.tailx.ts.net', hostname: 'mac.tailx.ts.net' };
const grant = 'G'.repeat(43);
const via = encodeURIComponent(app.origin);
const link = (host: string, fragment = `grant=${grant}&via=${via}`) => `https://${host}/add-computer#${fragment}`;

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('parseAddComputerLink', () => {
  it('accepts a peer in the same tailnet with a valid grant and this app as via', () => {
    expect(parseAddComputerLink(link('vps.tailx.ts.net'), app)).toEqual({ origin: 'https://vps.tailx.ts.net', grant });
    expect(parseAddComputerLink(`  ${link('vps.tailx.ts.net', `via=${app.origin}&grant=${grant}`)}\n`, app))
      .toEqual({ origin: 'https://vps.tailx.ts.net', grant });
  });

  it('rejects near-miss hosts and other tailnets', () => {
    for (const host of ['vps.xtailx.ts.net', 'vps.tailx.ts.net.evil.com', 'a.vps.tailx.ts.net', 'tailx.ts.net', 'vps.taily.ts.net', 'vps.ts.net']) {
      expect(parseAddComputerLink(link(host), app)).toEqual({ problem: 'tailnet' });
    }
  });

  it('rejects non-canonical spellings', () => {
    for (const text of [
      `http://vps.tailx.ts.net/add-computer#grant=${grant}&via=${via}`,
      link('vps.tailx.ts.net:443'),
      link('vps.tailx.ts.net:8443'),
      link('VPS.tailx.ts.net'),
      link('user@vps.tailx.ts.net'),
      link('vps.tailx.ts.net.'),
      `https://vps.tailx.ts.net/pair#grant=${grant}&via=${via}`,
      `https://vps.tailx.ts.net/add-computer?x=1#grant=${grant}&via=${via}`,
      'not a link',
    ]) {
      expect(parseAddComputerLink(text, app)).toEqual({ problem: 'format' });
    }
  });

  it('rejects this app\'s own origin, a wrong via and a bad grant', () => {
    expect(parseAddComputerLink(link('mac.tailx.ts.net'), app)).toEqual({ problem: 'self' });
    for (const other of ['https://evil.tailx.ts.net', 'https://mac.tailx.ts.net/', 'http://mac.tailx.ts.net', '']) {
      expect(parseAddComputerLink(link('vps.tailx.ts.net', `grant=${grant}&via=${encodeURIComponent(other)}`), app)).toEqual({ problem: 'via' });
    }
    expect(parseAddComputerLink(link('vps.tailx.ts.net', `grant=${grant}&via=${via}&via=${via}`), app)).toEqual({ problem: 'via' });
    for (const bad of ['short', `${grant}A`, `${'G'.repeat(42)}!`]) {
      expect(parseAddComputerLink(link('vps.tailx.ts.net', `grant=${bad}&via=${via}`), app)).toEqual({ problem: 'grant' });
    }
  });
});

describe('add-computer reload', () => {
  it('scrubs the fragment at once and returns a still-valid link', () => {
    const replaceState = vi.fn();
    const url = addComputerUrl({ origin: 'https://vps.tailx.ts.net', grant });
    expect(url.startsWith('/computers/add#')).toBe(true);
    const hash = url.slice(url.indexOf('#'));
    expect(takeAddComputerFragment({ pathname: '/computers/add', search: '', hash }, { replaceState }, app))
      .toEqual({ origin: 'https://vps.tailx.ts.net', grant });
    expect(replaceState).toHaveBeenCalledWith(null, '', '/computers/add');
  });

  it('scrubs but refuses a tampered fragment, and ignores other paths', () => {
    const replaceState = vi.fn();
    const hash = `#${new URLSearchParams({ grant, origin: 'https://vps.evil.ts.net' }).toString()}`;
    expect(takeAddComputerFragment({ pathname: '/computers/add', search: '', hash }, { replaceState }, app)).toBeUndefined();
    expect(replaceState).toHaveBeenCalledOnce();
    expect(takeAddComputerFragment({ pathname: '/pair', search: '', hash }, { replaceState }, app)).toBeUndefined();
    expect(replaceState).toHaveBeenCalledOnce();
  });
});

describe('AddComputerFlow', () => {
  const store = new Map<string, string>();
  const storage = { getItem: (key: string) => store.get(key) ?? null, setItem: (key: string, value: string) => { store.set(key, value); } };
  const settings = {
    device: { id: '00000000-0000-4000-8000-000000000020', label: 'Phone' }, projects: [], shellProfiles: [], hostBuild: 'b',
    brokerGeneration: '00000000-0000-4000-8000-000000000021', protocolRevision: 1, minProtocolRevision: 1,
  };

  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('location', { origin: app.origin });
    vi.stubGlobal('localStorage', storage);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    store.clear();
  });

  it('redeems on the peer with credentials, polls, reads peer settings, then confirms on home', async () => {
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url} ${init.credentials}`);
      if (url.endsWith('/pair/redeem')) {
        return json(200, { pendingId: '00000000-0000-4000-8000-000000000010', pollSecret: 'p'.repeat(43), verificationCode: '123-456', expiresAt: new Date(Date.now() + 60_000).toISOString() });
      }
      if (url.endsWith('/pair/poll')) return json(200, { status: 'approved' });
      if (url.endsWith('/settings')) return json(200, settings);
      return json(200, { computer: { origin: 'https://vps.tailx.ts.net', label: 'vps' }, state: 'confirmed', node: { id: 'n1', tags: [] } });
    }));
    const flow = new AddComputerFlow('https://vps.tailx.ts.net', grant);
    const pending = await flow.redeem('Phone');
    expect(pending.verificationCode).toBe('123-456');
    await expect(flow.redeem('Phone')).rejects.toThrow('already used');
    const done = flow.finish(pending);
    await vi.advanceTimersByTimeAsync(2000);
    await expect(done).resolves.toMatchObject({ kind: 'confirmed', computer: { origin: 'https://vps.tailx.ts.net', label: 'vps' } });
    expect(calls).toEqual([
      'POST https://vps.tailx.ts.net/api/v1/pair/redeem include',
      'POST https://vps.tailx.ts.net/api/v1/pair/poll include',
      'GET https://vps.tailx.ts.net/api/v1/settings include',
      'POST /api/v1/computers same-origin',
    ]);
    expect(JSON.parse(store.get(COMPUTERS_KEY) ?? '')).toEqual([{ origin: 'https://vps.tailx.ts.net', label: 'vps' }]);
  });

  it('retries the first peer settings read on a network error, for when the peer has not yet allowed this app', async () => {
    let refused = 2;
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      if (url.endsWith('/pair/redeem')) {
        return json(200, { pendingId: '00000000-0000-4000-8000-000000000010', pollSecret: 'p'.repeat(43), verificationCode: '123-456', expiresAt: new Date(Date.now() + 60_000).toISOString() });
      }
      if (url.endsWith('/pair/poll')) return json(200, { status: 'approved' });
      if (url.endsWith('/settings')) {
        if (refused-- > 0) throw new TypeError('CORS');
        return json(200, settings);
      }
      return json(200, { computer: { origin: 'https://lag.tailx.ts.net', label: 'lag' }, state: 'confirmed', node: { id: 'n1', tags: [] } });
    }));
    const flow = new AddComputerFlow('https://lag.tailx.ts.net', grant);
    const done = flow.finish(await flow.redeem('Phone'));
    await vi.advanceTimersByTimeAsync(4000);
    await expect(done).resolves.toMatchObject({ kind: 'confirmed' });
    expect(calls.filter((call) => call.endsWith('/settings'))).toHaveLength(3);
  });

  it('gives up after about 3 s of network errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/pair/redeem')) {
        return json(200, { pendingId: '00000000-0000-4000-8000-000000000010', pollSecret: 'p'.repeat(43), verificationCode: '123-456', expiresAt: new Date(Date.now() + 60_000).toISOString() });
      }
      if (url.endsWith('/pair/poll')) return json(200, { status: 'approved' });
      throw new TypeError('CORS');
    }));
    const flow = new AddComputerFlow('https://never.tailx.ts.net', grant);
    const done = flow.finish(await flow.redeem('Phone'));
    const caught = done.catch((error: unknown) => error);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(await caught).toMatchObject({ code: 'network' });
  });
});
