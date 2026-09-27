import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { adoptFragmentGrant, claimGrant, PairingFlow, takeGrantFromFragment } from './pairing';

const grant = 'g'.repeat(43);
const pollSecret = 'p'.repeat(43);
const pendingId = '00000000-0000-4000-8000-000000000010';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('takeGrantFromFragment', () => {
  it('removes the fragment immediately and returns the grant', () => {
    const replaceState = vi.fn();
    const result = takeGrantFromFragment(
      { hash: `#${grant}`, pathname: '/pair', search: '' },
      { replaceState },
    );
    expect(result).toBe(grant);
    expect(replaceState).toHaveBeenCalledWith(null, '', '/pair');
  });

  it('accepts the grant= form and still strips malformed fragments', () => {
    const replaceState = vi.fn();
    expect(takeGrantFromFragment({ hash: `#grant=${grant}`, pathname: '/pair', search: '' }, { replaceState }))
      .toBe(grant);
    expect(takeGrantFromFragment({ hash: '#short', pathname: '/pair', search: '?x=1' }, { replaceState }))
      .toBeUndefined();
    expect(replaceState).toHaveBeenLastCalledWith(null, '', '/pair?x=1');
  });

  it('strips a fragment on any path but accepts a grant only on /pair', () => {
    const replaceState = vi.fn();
    expect(takeGrantFromFragment({ hash: `#${grant}`, pathname: '/pair/', search: '' }, { replaceState }))
      .toBeUndefined();
    expect(replaceState).toHaveBeenLastCalledWith(null, '', '/pair/');
    expect(takeGrantFromFragment({ hash: `#${grant}`, pathname: '/', search: '' }, { replaceState }))
      .toBeUndefined();
    expect(replaceState).toHaveBeenLastCalledWith(null, '', '/');
  });

  it('does nothing without a fragment', () => {
    const replaceState = vi.fn();
    expect(takeGrantFromFragment({ hash: '', pathname: '/pair', search: '' }, { replaceState })).toBeUndefined();
    expect(replaceState).not.toHaveBeenCalled();
  });
});

describe('PairingFlow', () => {
  const storage = { setItem: vi.fn(), getItem: vi.fn(), removeItem: vi.fn(), clear: vi.fn() };

  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('localStorage', storage);
    vi.stubGlobal('sessionStorage', storage);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it('redeems, polls every 2 s until approved, and never persists the secret', async () => {
    const calls: Array<{ url: string; init: RequestInit }> = [];
    let polls = 0;
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      if (url === '/api/v1/pair/redeem') {
        return json(200, {
          pendingId,
          pollSecret,
          verificationCode: '123-456',
          expiresAt: new Date(Date.now() + 120_000).toISOString(),
        });
      }
      polls++;
      return json(200, polls < 3 ? { status: 'pending' } : { status: 'approved' });
    }));

    const flow = new PairingFlow();
    const pending = await flow.redeem(grant, 'My phone');
    expect(pending.verificationCode).toBe('123-456');
    expect(flow.holdsSecret()).toBe(true);

    const outcome = flow.poll(pending);
    await vi.advanceTimersByTimeAsync(1999);
    expect(polls).toBe(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(polls).toBe(1);
    await vi.advanceTimersByTimeAsync(4000);
    await expect(outcome).resolves.toBe('approved');
    expect(polls).toBe(3);
    expect(flow.holdsSecret()).toBe(false);

    const redeem = calls[0];
    expect(redeem?.init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(redeem?.init.credentials).toBe('same-origin');
    expect(JSON.parse(String(redeem?.init.body))).toEqual({ grant, deviceLabel: 'My phone' });
    expect(JSON.parse(String(calls[1]?.init.body))).toEqual({ pendingId, pollSecret });
    for (const call of calls) {
      expect(call.url).not.toContain(pollSecret);
      expect(call.url).not.toContain(grant);
    }
    expect(storage.setItem).not.toHaveBeenCalled();
  });

  it('reports expiry from the server and clears the secret', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => (url.endsWith('/redeem')
      ? json(200, { pendingId, pollSecret, verificationCode: '000-111', expiresAt: new Date(Date.now() + 120_000).toISOString() })
      : json(410, { error: { code: 'pairing_expired', message: 'Pairing expired' } }))));
    const flow = new PairingFlow();
    const outcome = flow.poll(await flow.redeem(grant, 'Phone'));
    await vi.advanceTimersByTimeAsync(2000);
    await expect(outcome).resolves.toBe('expired');
    expect(flow.holdsSecret()).toBe(false);
  });

  it('stops locally at the expiry time', async () => {
    const fetchMock = vi.fn(async (url: string) => (url.endsWith('/redeem')
      ? json(200, { pendingId, pollSecret, verificationCode: '000-111', expiresAt: new Date(Date.now() + 3000).toISOString() })
      : json(200, { status: 'pending' })));
    vi.stubGlobal('fetch', fetchMock);
    const flow = new PairingFlow();
    const outcome = flow.poll(await flow.redeem(grant, 'Phone'));
    await vi.advanceTimersByTimeAsync(6000);
    await expect(outcome).resolves.toBe('expired');
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});

describe('adoptFragmentGrant', () => {
  const grant = 'A'.repeat(43);

  it('takes a new grant from a same-tab link and strips it from the URL', () => {
    const holder: { value: string | undefined } = { value: undefined };
    const replaced: string[] = [];
    const adopted = adoptFragmentGrant(
      holder,
      { hash: `#${grant}`, pathname: '/pair', search: '' },
      { replaceState: (_data, _unused, url) => { replaced.push(url); } },
    );
    expect(adopted).toBe(true);
    expect(holder.value).toBe(grant);
    expect(replaced).toEqual(['/pair']);
  });

  it('ignores fragments that are not a pairing grant', () => {
    const holder: { value: string | undefined } = { value: 'kept' };
    const adopted = adoptFragmentGrant(
      holder,
      { hash: '#section', pathname: '/pair', search: '' },
      { replaceState: () => undefined },
    );
    expect(adopted).toBe(false);
    expect(holder.value).toBe('kept');
  });
});

describe('claimGrant', () => {
  it('keeps the grant when the device name is not valid yet', () => {
    const holder: { value: string | undefined } = { value: 'secret' };
    expect(claimGrant(holder, false)).toEqual({ problem: 'label' });
    expect(holder.value).toBe('secret');
  });

  it('reports a missing grant instead of doing nothing', () => {
    expect(claimGrant({ value: undefined }, true)).toEqual({ problem: 'missing' });
  });

  it('hands out the grant exactly once', () => {
    const holder: { value: string | undefined } = { value: 'secret' };
    expect(claimGrant(holder, true)).toEqual({ secret: 'secret' });
    expect(holder.value).toBeUndefined();
    expect(claimGrant(holder, true)).toEqual({ problem: 'missing' });
  });
});
