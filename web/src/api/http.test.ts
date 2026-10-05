import { afterEach, describe, expect, it, vi } from 'vitest';
import { MIN_PROTOCOL_REVISION, PROTOCOL_REVISION } from '../generated/protocol';
import {
  api, ApiError, blobToBase64, client, deviceLabelProblem, firstLabel, forgetClient, MAX_RESPONSE_BYTES, PEER_REQUEST_FAILED,
  PROVIDER_CREATE_TIMEOUT_MS, PROVIDER_MANAGE_TIMEOUT_MS, providerWord, REQUEST_TIMEOUT_MS, revisionProblem, UPLOAD_TIMEOUT_MS,
} from './http';

// Pretend this app is at revision 3 and serves computers from revision 2, so a
// real (schema-valid) revision 1 computer is outdated.
vi.mock('../generated/protocol', async (original) => ({
  ...await original<typeof import('../generated/protocol')>(),
  PROTOCOL_REVISION: 3,
  MIN_PROTOCOL_REVISION: 2,
}));

const device = { id: '00000000-0000-4000-8000-000000000020', label: 'Work phone' };

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());


/** Fake timers do not drive AbortSignal.timeout; this one runs on them. */
function fakeTimeoutSignals() {
  vi.useFakeTimers();
  vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms) => {
    const controller = new AbortController();
    setTimeout(() => controller.abort(new DOMException('timeout', 'TimeoutError')), ms);
    return controller.signal;
  });
}

describe('deviceLabelProblem mirrors the deviceLabel schema', () => {
  it('accepts 1–64 characters within 64 UTF-8 bytes', () => {
    expect(deviceLabelProblem('a')).toBeUndefined();
    expect(deviceLabelProblem('x'.repeat(64))).toBeUndefined();
    expect(deviceLabelProblem('Ada’s iPhone')).toBeUndefined();
    expect(deviceLabelProblem('😀'.repeat(16))).toBeUndefined(); // 16 characters, 64 bytes
  });

  it('rejects empty, too long, too many bytes, control characters and edge spaces', () => {
    expect(deviceLabelProblem('')).toBe('Enter a name.');
    expect(deviceLabelProblem('x'.repeat(65))).toMatch(/shorter/u);
    expect(deviceLabelProblem('😀'.repeat(17))).toMatch(/shorter/u); // 68 bytes
    expect(deviceLabelProblem('電'.repeat(22))).toMatch(/shorter/u); // 66 bytes
    expect(deviceLabelProblem('a\u0007b')).toMatch(/control/u);
    expect(deviceLabelProblem('a\nb')).toMatch(/control/u);
    expect(deviceLabelProblem(' phone')).toMatch(/start or end/u);
    expect(deviceLabelProblem('phone ')).toMatch(/start or end/u);
  });
});

describe('api.renameDevice', () => {
  it('sends a same-origin JSON PATCH with the fixed request header', async () => {
    const fetch = vi.fn(async () => json(200, device));
    vi.stubGlobal('fetch', fetch);

    await expect(api.renameDevice({ label: 'Work phone' })).resolves.toEqual(device);

    expect(fetch).toHaveBeenCalledOnce();
    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/v1/device');
    expect(init.method).toBe('PATCH');
    expect(init.credentials).toBe('same-origin');
    expect(init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(init.body as string)).toEqual({ label: 'Work phone' });
  });

  it('surfaces the server error code and safe message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(400, { error: { code: 'invalid_request', message: 'label is invalid' } })));
    const failure = await api.renameDevice({ label: 'x' }).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ApiError);
    expect(failure).toMatchObject({ status: 400, code: 'invalid_request', message: 'label is invalid' });
  });
});

describe('request timeout', () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  it('turns a request that never answers into a network error', async () => {
    fakeTimeoutSignals();
    vi.stubGlobal('fetch', vi.fn((_path: string, init: RequestInit) => new Promise((_resolve, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    })));
    const pending = api.settings().catch((error: unknown) => error);
    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS);
    const error = await pending;
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('network');
  });
});

describe('push API', () => {
  it('reads the key, then posts subscribe, unsubscribe and test with the fixed header', async () => {
    const fetch = vi.fn(async (path: string) => (path === '/api/v1/push'
      ? json(200, { publicKey: 'B'.repeat(87) })
      : path === '/api/v1/push/test' ? json(200, { sent: 1 }) : new Response(null, { status: 204 })));
    vi.stubGlobal('fetch', fetch);
    const endpoint = 'https://push.example/abc';

    await expect(api.pushConfig()).resolves.toEqual({ publicKey: 'B'.repeat(87) });
    await expect(api.pushSubscribe({ endpoint, keys: { p256dh: 'B'.repeat(87), auth: 'a'.repeat(22) } })).resolves.toBeUndefined();
    await expect(api.pushUnsubscribe({ endpoint })).resolves.toBeUndefined();
    await expect(api.pushTest()).resolves.toEqual({ sent: 1 });

    const calls = fetch.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls.map(([path, init]) => `${init.method} ${path}`)).toEqual([
      'GET /api/v1/push',
      'POST /api/v1/push/subscribe',
      'POST /api/v1/push/unsubscribe',
      'POST /api/v1/push/test',
    ]);
    expect(calls[1]?.[1].headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(calls[1]?.[1].body as string)).toEqual({ endpoint, keys: { p256dh: 'B'.repeat(87), auth: 'a'.repeat(22) } });
    expect(JSON.parse(calls[2]?.[1].body as string)).toEqual({ endpoint });
    expect(calls[3]?.[1].body).toBe('{}');
  });
});

describe('photo upload', () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  it('posts the image to the session uploads path with the fixed header', async () => {
    const saved = { path: '.termivoa/uploads/20260927-143012-a1b2c3d4.png', size: 421000, contentType: 'image/png' };
    const fetch = vi.fn(async () => json(200, saved));
    vi.stubGlobal('fetch', fetch);

    await expect(api.uploadImage('a/b', { generation: 'g1', data: 'iVBORw0KGgo=' })).resolves.toEqual(saved);

    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/v1/sessions/a%2Fb/uploads');
    expect(init.method).toBe('POST');
    expect(init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(init.body as string)).toEqual({ generation: 'g1', data: 'iVBORw0KGgo=' });
  });

  it('waits longer than a normal request before giving up', async () => {
    fakeTimeoutSignals();
    vi.stubGlobal('fetch', vi.fn((_path: string, init: RequestInit) => new Promise((_resolve, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    })));
    let settled = false;
    const pending = api.uploadImage('s', { generation: 'g', data: 'AA==' }).catch((error: unknown) => { settled = true; return error; });
    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(UPLOAD_TIMEOUT_MS - REQUEST_TIMEOUT_MS);
    expect(await pending).toMatchObject({ code: 'network' });
  });

  it('shows the server message for a refused upload', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(409, { error: { code: 'not_controller', message: 'Take control to attach a photo.' } })));
    const failure = await api.uploadImage('s', { generation: 'g', data: 'AA==' }).catch((error: unknown) => error);
    expect(failure).toMatchObject({ status: 409, code: 'not_controller', message: 'Take control to attach a photo.' });
  });

  it('encodes a blob as standard padded base64 across chunk edges', async () => {
    const bytes = new Uint8Array(0x8000 * 2 + 5).map((_value, index) => (index * 7) % 256);
    expect(await blobToBase64(new Blob([bytes]))).toBe(Buffer.from(bytes).toString('base64'));
    expect(await blobToBase64(new Blob([new Uint8Array([1])]))).toBe('AQ==');
    expect(await blobToBase64(new Blob([]))).toBe('');
  });
});

const settings = (revision = PROTOCOL_REVISION, minRevision = MIN_PROTOCOL_REVISION) => ({
  device,
  projects: [],
  shellProfiles: [],
  hostBuild: 'test',
  brokerGeneration: '00000000-0000-4000-8000-000000000021',
  protocolRevision: revision,
  minProtocolRevision: minRevision,
});

describe('client(origin) for a peer', () => {
  it('sends credentialed CORS requests to the peer origin and never follows redirects', async () => {
    const fetch = vi.fn(async () => json(200, settings()));
    vi.stubGlobal('fetch', fetch);
    const peer = client('https://vps.tailx.ts.net');
    expect(peer.home).toBe(false);
    expect(client('https://vps.tailx.ts.net')).toBe(peer);
    await peer.settings();
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('https://vps.tailx.ts.net/api/v1/settings');
    expect(init).toMatchObject({ mode: 'cors', credentials: 'include', redirect: 'error', cache: 'no-store' });
  });

  it('keeps home same-origin with relative paths', async () => {
    const fetch = vi.fn(async () => json(200, { sessions: [] }));
    vi.stubGlobal('fetch', fetch);
    expect(api.home).toBe(true);
    expect(client()).toBe(api);
    await api.sessions();
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/sessions');
    expect(init.credentials).toBe('same-origin');
    expect(init.redirect).toBeUndefined();
  });

  it('never shows the peer\'s own error text', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(409, { error: { code: 'not_controller', message: 'Call +1 555 0100 to unlock' } })));
    const failure = await client('https://text.tailx.ts.net').uploadImage('s', { generation: 'g', data: 'AA==' }).catch((error: unknown) => error);
    expect(failure).toMatchObject({ status: 409, code: 'not_controller', message: PEER_REQUEST_FAILED });
  });

  it('refuses bodies over the size limit and bodies that break the schema', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(`{"sessions":[],"pad":"${'x'.repeat(MAX_RESPONSE_BYTES)}"}`, { status: 200 })));
    await expect(client('https://big.tailx.ts.net').sessions()).rejects.toMatchObject({ code: 'invalid_response' });
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { sessions: 'all' })));
    await expect(client('https://bad.tailx.ts.net').sessions()).rejects.toMatchObject({ code: 'invalid_response' });
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('redirect'); }));
    await expect(client('https://moved.tailx.ts.net').sessions()).rejects.toMatchObject({ code: 'network' });
  });

  it('accepts a normal build and refuses a peer build that is not plain text', async () => {
    const build = (hostBuild: string) => vi.stubGlobal('fetch', vi.fn(async () => json(200, { ...settings(), hostBuild })));
    build('0.1.0-beta.2+1a2b3c4d5e6f.dirty');
    await expect(client('https://ok.tailx.ts.net').settings()).resolves.toMatchObject({ hostBuild: '0.1.0-beta.2+1a2b3c4d5e6f.dirty' });
    for (const bad of ['<b>1</b>', 'v1\u202e', 'a b', 'x'.repeat(129)]) {
      build(bad);
      await expect(client('https://bad.tailx.ts.net').settings()).rejects.toMatchObject({ code: 'invalid_response' });
    }
  });

  it('refuses an outdated peer until a settings read passes; logout still works', async () => {
    let body = settings(MIN_PROTOCOL_REVISION - 1);
    const fetch = vi.fn(async (url: string) => (url.endsWith('/settings') ? json(200, body) : url.endsWith('/logout')
      ? new Response(null, { status: 204 }) : json(200, { sessions: [] })));
    vi.stubGlobal('fetch', fetch);
    const peer = client('https://old.tailx.ts.net');
    await expect(peer.settings()).rejects.toMatchObject({ code: 'computer_outdated', message: 'Update Termivoa on old' });
    await expect(peer.sessions()).rejects.toMatchObject({ code: 'computer_outdated' });
    await expect(peer.logout()).resolves.toBeUndefined();
    body = settings();
    await peer.settings();
    await expect(peer.sessions()).resolves.toEqual({ sessions: [] });
  });

  it('forgets a removed peer\'s client, but never home\'s', () => {
    const peer = client('https://removed.tailx.ts.net');
    forgetClient('https://removed.tailx.ts.net');
    expect(client('https://removed.tailx.ts.net')).not.toBe(peer);
    forgetClient('');
    expect(client()).toBe(api);
  });

  it('asks to update home when a peer needs a newer app', async () => {
    vi.stubGlobal('location', { origin: 'https://mac.tailx.ts.net' });
    vi.stubGlobal('fetch', vi.fn(async () => json(200, settings(PROTOCOL_REVISION + 1, PROTOCOL_REVISION + 1))));
    await expect(client('https://new.tailx.ts.net').settings()).rejects.toMatchObject({ code: 'app_outdated', message: 'Update Termivoa on mac' });
  });
});

describe('revision helpers', () => {
  it('gates revisions', () => {
    expect(revisionProblem({ protocolRevision: MIN_PROTOCOL_REVISION, minProtocolRevision: PROTOCOL_REVISION })).toBeUndefined();
    expect(revisionProblem({ protocolRevision: MIN_PROTOCOL_REVISION - 1, minProtocolRevision: 1 })).toBe('computer_outdated');
    expect(revisionProblem({ protocolRevision: PROTOCOL_REVISION + 5, minProtocolRevision: PROTOCOL_REVISION + 1 })).toBe('app_outdated');
    expect(firstLabel('https://vps.tailx.ts.net')).toBe('vps');
  });
});

describe('api.foundComputers', () => {
  const grant = 'G'.repeat(43);
  const row = (host: string) => ({ origin: `https://${host}.tailx.ts.net`, label: host, grant, expiresAt: '2026-10-03T10:00:00Z' });

  it('reads home\'s Found list same-origin and drops a row that breaks the schema', async () => {
    const fetch = vi.fn(async () => json(200, { computers: [row('nas'), { ...row('pi'), grant: 'short' }, row('vps')] }));
    vi.stubGlobal('fetch', fetch);
    await expect(api.foundComputers()).resolves.toEqual({ computers: [row('nas'), row('vps')] });
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/computers/found');
    expect(init).toMatchObject({ method: 'GET', credentials: 'same-origin' });
  });

  it('refuses a body that breaks the schema', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { computers: Array.from({ length: 17 }, (_, at) => row(`c${at}`)) })));
    await expect(api.foundComputers()).rejects.toMatchObject({ code: 'invalid_response' });
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { computers: 'all' })));
    await expect(api.foundComputers()).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('keeps every failure an error, 404 and 405 too (app and home are one build)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('404 page not found', { status: 404 })));
    await expect(api.foundComputers()).rejects.toMatchObject({ status: 404 });
    vi.stubGlobal('fetch', vi.fn(async () => json(503, { error: { code: 'host_unavailable', message: 'x' } })));
    await expect(api.foundComputers()).rejects.toMatchObject({ status: 503, code: 'host_unavailable' });
  });
});

describe('provider routes', () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
  const target = (id: string, state: string, extra: object = {}) => ({ id, label: '1', state, canOpen: true, ...extra });
  const snapshot = {
    provider: 'herdr', state: 'ready', groups: [
      { id: 'g3f9a0c2b17e4', name: 'shop', state: 'blocked', canCreate: true, items: [
        { id: 'w8', name: 'silver-meadow', state: 'blocked', openTarget: 'w8:p1.term_1', targets: [target('w8:p1.term_1', 'blocked', { agent: 'claude' }), target('w8:p3.term_2', 'none', { canOpen: false })] },
        { id: 'w9', name: 'lucky-valley', state: 'snoozing', targets: [target('w9:p1.term_3', 'snoozing'), target('../x', 'done')] },
      ] },
      { id: '../etc', name: 'bad', state: 'idle', canCreate: false, items: [] },
    ],
  };

  it('reads a snapshot, drops only the bad group and the bad target, and shows unknown words as unknown', async () => {
    const fetch = vi.fn(async () => json(200, structuredClone(snapshot)));
    vi.stubGlobal('fetch', fetch);
    const body = await client('https://vps.tailx.ts.net').provider('herdr');
    expect((fetch.mock.calls as unknown as Array<[string]>)[0]?.[0]).toBe('https://vps.tailx.ts.net/api/v1/providers/herdr');
    expect(body.groups.map((group) => group.id)).toEqual(['g3f9a0c2b17e4']);
    const [first, second] = body.groups[0]?.items ?? [];
    expect(first?.targets.map((item) => item.state)).toEqual(['blocked', 'none']);
    expect(second?.state).toBe('unknown');
    expect(second?.targets).toEqual([target('w9:p1.term_3', 'unknown')]);
  });

  it('knows six words; any other word is unknown', () => {
    for (const word of ['blocked', 'done', 'working', 'idle', 'unknown', 'none']) expect(providerWord(word)).toBe(word);
    for (const word of ['waiting', 'Blocked', '', 'toString']) expect(providerWord(word)).toBe('unknown');
  });

  it('opens, updates and creates with the mutation header, and create waits 45 s', async () => {
    const session = {
      id: '84444444-4444-4444-8444-444444444444', generation: '85555555-5555-4555-8555-555555555555', title: 'herdr · shop · silver-meadow',
      state: 'running', columns: 45, rows: 20, provider: 'herdr', targetId: 'w8:p1.term_1',
    };
    const info = { id: 'herdr', name: 'herdr', state: 'ready', enabled: false, notify: ['blocked'], canCreate: true, agentKinds: ['claude'] };
    const fetch = vi.fn(async (path: string) => json(200, path.endsWith('/open') ? { session, reused: false }
      : path.endsWith('/create') ? { itemId: 'w10', targetId: 'w10:p1.term_9', agentStarted: false, problem: 'agentNotReady' } : info));
    vi.stubGlobal('fetch', fetch);
    await expect(api.providerOpen('herdr', { targetId: 'w8:p1.term_1', columns: 45, rows: 20 })).resolves.toEqual({ session, reused: false });
    await expect(api.providerUpdate('herdr', { enabled: false })).resolves.toEqual(info);
    const timeout = vi.spyOn(AbortSignal, 'timeout');
    await expect(api.providerCreate('herdr', { groupId: 'g1', name: 'fix/login' })).resolves.toMatchObject({ agentStarted: false, problem: 'agentNotReady' });
    expect(timeout).toHaveBeenCalledWith(PROVIDER_CREATE_TIMEOUT_MS);
    expect(PROVIDER_CREATE_TIMEOUT_MS).toBe(45_000);
    const calls = fetch.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls.map(([path, init]) => `${init.method} ${path}`)).toEqual([
      'POST /api/v1/providers/herdr/open', 'PATCH /api/v1/providers/herdr', 'POST /api/v1/providers/herdr/create',
    ]);
    for (const [, init] of calls) expect(init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
  });

  it('manages with the mutation header and waits 30 s; an answer keeps only the fields the schema knows', async () => {
    const fetch = vi.fn(async () => json(200, { itemId: 'w10', targetId: 'w10:p1.term_9', path: '/Users/demo' }));
    vi.stubGlobal('fetch', fetch);
    const timeout = vi.spyOn(AbortSignal, 'timeout');
    await expect(api.providerManage('herdr', { action: 'newTab', itemId: 'w10' })).resolves.toEqual({ itemId: 'w10', targetId: 'w10:p1.term_9' });
    expect(timeout).toHaveBeenCalledWith(PROVIDER_MANAGE_TIMEOUT_MS);
    expect(PROVIDER_MANAGE_TIMEOUT_MS).toBe(30_000);
    const [path, init] = (fetch.mock.calls as unknown as Array<[string, RequestInit]>)[0] ?? ['', {}];
    expect(`${init.method} ${path}`).toBe('POST /api/v1/providers/herdr/manage');
    expect(init.headers).toEqual({ 'Content-Type': 'application/json', 'X-Pterm-Request': '1' });
    expect(JSON.parse(String(init.body))).toEqual({ action: 'newTab', itemId: 'w10' });

    vi.stubGlobal('fetch', vi.fn(async () => json(200, { problem: 'uncommitted' })));
    await expect(api.providerManage('herdr', { action: 'removeItem', itemId: 'w8', name: 'silver-meadow' })).resolves.toEqual({ problem: 'uncommitted' });
    // A problem or a target the app does not know is refused, never shown or opened.
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { problem: 'rm -rf' })));
    await expect(api.providerManage('herdr', { action: 'removeItem', itemId: 'w8', name: 'silver-meadow' })).rejects.toMatchObject({ code: 'invalid_response' });
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { targetId: '../x' })));
    await expect(api.providerManage('herdr', { action: 'newTab', itemId: 'w8' })).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('waits as long as a create for a new tab with an agent (the agent starts, the prompt goes)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { itemId: 'w10', targetId: 'w10:p2.term_9', agentStarted: false, agentProblem: 'agentFailed' })));
    const timeout = vi.spyOn(AbortSignal, 'timeout');
    await expect(api.providerManage('herdr', { action: 'newTab', itemId: 'w10', agent: 'claude' }))
      .resolves.toEqual({ itemId: 'w10', targetId: 'w10:p2.term_9', agentStarted: false, agentProblem: 'agentFailed' });
    expect(timeout).toHaveBeenCalledWith(PROVIDER_CREATE_TIMEOUT_MS);
    // A problem the app has no words for is refused.
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { itemId: 'w10', agentStarted: false, agentProblem: 'rm -rf' })));
    await expect(api.providerManage('herdr', { action: 'newTab', itemId: 'w10', agent: 'claude' })).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('drops a closed worktree name that is not a branch name; the group and its other names stay', async () => {
    const closed = ['fix/login', '../etc', 'Has Space', '<b>x</b>', 'lucky-valley', ''];
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { provider: 'herdr', state: 'ready', groups: [{ id: 'g1', name: 'shop', state: 'idle', canCreate: true, items: [], closed }] })));
    const body = await client('https://vps.tailx.ts.net').provider('herdr');
    expect(body.groups.map((group) => group.closed)).toEqual([['fix/login', 'lucky-valley']]);
  });

  it('keeps a manage refusal\'s code, with home\'s own words and never a peer\'s', async () => {
    const refusal = () => json(409, { error: { code: 'operation_conflict', message: 'The item changed.' } });
    vi.stubGlobal('fetch', vi.fn(async () => refusal()));
    await expect(api.providerManage('herdr', { action: 'closeItem', itemId: 'w8', name: 'x' })).rejects.toMatchObject({ code: 'operation_conflict', message: 'The item changed.' });
    await expect(client('https://vps.tailx.ts.net').providerManage('herdr', { action: 'closeItem', itemId: 'w8', name: 'x' }))
      .rejects.toMatchObject({ code: 'operation_conflict', message: PEER_REQUEST_FAILED });
  });

  it('reads revision 5: not installed with canInstall, canStart, and a linked worktree', async () => {
    const base = { id: 'herdr', name: 'herdr', enabled: true, notify: [], canCreate: false, agentKinds: [] };
    const settings = {
      device, projects: [], shellProfiles: [], hostBuild: '0.1.0', brokerGeneration: '00000000-0000-4000-8000-000000000002',
      protocolRevision: 3, minProtocolRevision: 1,
      providers: [{ ...base, state: 'notInstalled', canInstall: true }],
    };
    vi.stubGlobal('fetch', vi.fn(async () => json(200, settings)));
    await expect(api.settings()).resolves.toMatchObject({ providers: [{ state: 'notInstalled', canInstall: true }] });
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { ...settings, providers: [{ ...base, state: 'notRunning', canStart: true }] })));
    await expect(api.settings()).resolves.toMatchObject({ providers: [{ state: 'notRunning', canStart: true }] });
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { provider: 'herdr', state: 'ready', groups: [
      { id: 'g1', name: 'shop', state: 'idle', canCreate: true, items: [{ id: 'w8', name: 'silver-meadow', state: 'idle', linked: true, targets: [] }, { id: 'w1', name: 'main', state: 'idle', targets: [] }] },
    ] })));
    const list = await api.provider('herdr');
    expect(list.groups[0]?.items.map((one) => one.linked)).toEqual([true, undefined]);
  });

  it('keeps settings readable when one provider entry is bad', async () => {
    const settings = {
      device, projects: [], shellProfiles: [], hostBuild: '0.1.0', brokerGeneration: '00000000-0000-4000-8000-000000000002',
      protocolRevision: 4, minProtocolRevision: 1,
      providers: [{ id: 'herdr', name: 'herdr', state: 'ready', enabled: true, notify: ['blocked', 'done'], canCreate: false, agentKinds: [] }, { id: 'Bad Id' }],
    };
    vi.stubGlobal('fetch', vi.fn(async () => json(200, settings)));
    await expect(api.settings()).resolves.toMatchObject({ providers: [{ id: 'herdr' }] });
  });
});
