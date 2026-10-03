import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { Computer } from '../computers';
import { LINK_PROBLEMS, parseAddComputerLink, resetAdd, startAdd } from '../pairing/addComputer';
import { SCAN_ASKING, SCAN_POINT } from '../pairing/scan';
import { findAddComputerLinks } from '../terminal/pathLinks';
import {
  AddComputerConfirm, AddComputerContinue, AddComputerPage, AddComputerSheet, addFailure, LeaveAdd, ownerText, PeerAddPage, viaLabel,
} from './AddComputer';
import { ApiError } from '../api/http';
import { ReadFailure, type ComputerEntry } from './allSessions';
import {
  answeredText, atLeast, checkFailureText, CHECKING_MIN_MS, computerDetail, ComputersSection, FoundBlock, healthText, onlineText, readLink, ScanView,
} from './Computers';

const app = { origin: 'https://mac.tailx.ts.net', hostname: 'mac.tailx.ts.net' };
const grant = 'G'.repeat(43);
const link = { origin: 'https://vps.tailx.ts.net', grant };
const printed = `https://vps.tailx.ts.net/add-computer#grant=${grant}&via=${encodeURIComponent(app.origin)}`;
const noop = () => undefined;

/** Host buttons of a hookless component's element tree, by text. */
function buttons(node: ReactNode, found = new Map<string, () => void>()): Map<string, () => void> {
  if (Array.isArray(node)) {
    node.forEach((child: ReactNode) => buttons(child, found));
  } else if (isValidElement(node)) {
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void }>;
    // Keyed by the button's text; an icon next to it is left out.
    const text = Array.isArray(props.children) ? props.children.filter((child) => !isValidElement(child)) : props.children;
    if (node.type === 'button' && props.onClick !== undefined) found.set(String(text), props.onClick);
    buttons(props.children, found);
  }
  return found;
}

describe('add-computer confirm sheet', () => {
  it('shows the full origin, machine name and owner, and adds only on an explicit tap', () => {
    const onConfirm = vi.fn();
    const html = renderToStaticMarkup(<AddComputerConfirm link={link} node={{ id: 'n1', owner: 'ada@example.com', tags: [] }} onConfirm={onConfirm} onCancel={noop} />);
    expect(html).toContain('<dt>Address</dt><dd>https://vps.tailx.ts.net</dd>');
    expect(html).toContain('<dt>Machine</dt><dd>vps</dd>');
    expect(html).toContain('<dt>Owner</dt><dd>ada@example.com</dd>');
    expect(html).toContain('<button type="button" class="primary">Add vps</button>');
    expect(onConfirm).not.toHaveBeenCalled();
    buttons(AddComputerConfirm({ link, node: { id: 'n1', tags: [] }, onConfirm, onCancel: noop })).get('Add ,vps')?.();
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it('shows tags for a tagged computer', () => {
    expect(ownerText({ id: 'n', tags: ['tag:server', 'tag:prod'] })).toEqual({ term: 'Tags', value: 'tag:server, tag:prod' });
    expect(ownerText({ id: 'n', owner: 'ada@example.com', tags: [] })).toEqual({ term: 'Owner', value: 'ada@example.com' });
  });

  it('never adds when it opens: it checks with home first and offers no Add until then', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(noop)));
    const go = vi.fn();
    const html = renderToStaticMarkup(<AddComputerSheet link={link} onClose={noop} go={go} />);
    expect(html).toContain('Add this computer?');
    expect(html).toContain('Checking vps…');
    expect(html).not.toContain('Add vps');
    expect(go).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});

describe('Cancel before adding', () => {
  it('deletes the pending entry on home, from the confirm sheet and from the add page', async () => {
    const calls: string[] = [];
    vi.stubGlobal('location', { origin: app.origin });
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: noop });
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      return new Response(null, { status: 204 });
    }));
    const onCancel = vi.fn();
    buttons(AddComputerConfirm({ link, node: { id: 'n1', tags: [] }, onConfirm: noop, onCancel })).get('Cancel')?.();
    buttons(AddComputerContinue({ link, onContinue: noop, onCancel })).get('Cancel')?.();
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    expect(calls).toEqual(['DELETE /api/v1/computers/vps.tailx.ts.net', 'DELETE /api/v1/computers/vps.tailx.ts.net']);
    expect(onCancel).toHaveBeenCalledTimes(2);
    vi.unstubAllGlobals();
  });
});

describe('add-computer link in the terminal', () => {
  it('turns only a link for this app into a tap target', () => {
    const [found] = findAddComputerLinks(`run this: ${printed}.`);
    expect(found?.url).toBe(printed);
    expect(parseAddComputerLink(found?.url ?? '', app)).toEqual(link);
    const other = printed.replace(encodeURIComponent(app.origin), encodeURIComponent('https://evil.tailx.ts.net'));
    expect(parseAddComputerLink(other, app)).toEqual({ problem: 'via' });
    expect(parseAddComputerLink(printed.replace('vps.tailx', 'vps.other'), app)).toEqual({ problem: 'tailnet' });
  });
});

describe('add-computer page', () => {
  it('asks to paste the link again when there is none', () => {
    const html = renderToStaticMarkup(<AddComputerPage link={undefined} deviceLabel="iPhone" onDone={noop} onSettings={noop} onLeave={noop} />);
    expect(html).toContain('This add-computer link is used or incomplete.');
  });

  it('shows the full origin and waits for Continue before it sends anything', () => {
    const fetch = vi.fn(() => new Promise(noop));
    vi.stubGlobal('fetch', fetch);
    const html = renderToStaticMarkup(<AddComputerPage link={link} deviceLabel="iPhone" onDone={noop} onSettings={noop} onLeave={noop} />);
    expect(html).toContain('<dt>Address</dt><dd>https://vps.tailx.ts.net</dd>');
    expect(html).toContain('<button type="button" class="primary">Continue</button>');
    expect(html).not.toContain('Connecting to vps…');
    expect(fetch).not.toHaveBeenCalled();
    const onContinue = vi.fn();
    buttons(AddComputerContinue({ link, onContinue, onCancel: noop })).get('Continue')?.();
    expect(onContinue).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  it('asks for the command the user ran on every path: setup (Found), or pterm pair --via (Found, paste, scan)', () => {
    const sheet = renderToStaticMarkup(<AddComputerConfirm link={link} node={{ id: 'n1', owner: 'ada@example.com', tags: [] }} onConfirm={noop} onCancel={noop} />);
    const page = renderToStaticMarkup(<AddComputerContinue link={link} onContinue={noop} onCancel={noop} />);
    expect(sheet.replace(/<\/?(?:code|span)[^>]*>/gu, '')).toContain('Add it only if you just ran pterm setup or pterm pair --via on vps.');
    expect(page.replace(/<\/?(?:code|span)[^>]*>/gu, '')).toContain('Continue only if you just ran pterm setup or pterm pair --via on vps.');
    // The command stays on one line (global.css: .add-computer-hint code).
    for (const html of [sheet, page]) expect(html).toContain('<p class="hint-text add-computer-hint">');
  });

  it('says how to try again after the code expired: Add again (Found), or a new link', async () => {
    vi.stubGlobal('location', { origin: app.origin });
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: noop });
    vi.stubGlobal('fetch', vi.fn(async (url: string) => (url.endsWith('/pair/redeem')
      ? new Response(JSON.stringify({ pendingId: '00000000-0000-4000-8000-000000000010', pollSecret: 'p'.repeat(43), verificationCode: '123-456', expiresAt: new Date(Date.now() + 60_000).toISOString() }), { status: 200 })
      : new Response(JSON.stringify({ error: { code: 'pairing_expired', message: 'x' } }), { status: 410 }))));
    await startAdd({ ...link, found: true }, 'iPhone');
    const html = renderToStaticMarkup(<AddComputerPage link={{ ...link, found: true }} deviceLabel="iPhone" onDone={noop} onSettings={noop} onLeave={noop} />);
    expect(html.replace(/<\/?(?:code|span)[^>]*>/gu, '')).toContain('The code expired or vps refused it. Run pterm pair --via on vps again, then tap Add again, or scan or paste the new link.');
    resetAdd();
    vi.unstubAllGlobals();
  });

  it('offers a way out of the waiting page in this load: back to the terminal the add came from, else Sessions', () => {
    const onLeave = vi.fn();
    const terminal = LeaveAdd({ back: '/c/vps.tailx.ts.net/sessions/s1', onLeave });
    expect(renderToStaticMarkup(terminal)).toContain('Back to terminal');
    terminal.props.onClick();
    expect(onLeave).toHaveBeenLastCalledWith('/c/vps.tailx.ts.net/sessions/s1');
    const other = LeaveAdd({ back: '/settings', onLeave });
    expect(renderToStaticMarkup(other)).toContain('Sessions');
    other.props.onClick();
    expect(onLeave).toHaveBeenLastCalledWith('/');
  });

  it('shows why the app refused a link on this load', () => {
    const html = renderToStaticMarkup(<AddComputerPage link={undefined} notice="Not used." deviceLabel="iPhone" onDone={noop} onSettings={noop} onLeave={noop} />);
    expect(html).toContain('Not used.');
    expect(html).not.toContain('used or incomplete');
  });


  it('says the found computer stopped waiting when its grant is gone and home lists no new one', async () => {
    vi.stubGlobal('location', { origin: app.origin });
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: noop });
    vi.stubGlobal('fetch', vi.fn(async (url: string) => (url.endsWith('/computers/found')
      ? new Response(JSON.stringify({ computers: [] }), { status: 200 })
      : new Response(JSON.stringify({ error: { code: 'pairing_expired', message: 'x' } }), { status: 410 }))));
    await startAdd({ ...link, found: true }, 'iPhone');
    const html = renderToStaticMarkup(<AddComputerPage link={{ ...link, found: true }} deviceLabel="iPhone" onDone={noop} onSettings={noop} onLeave={noop} />);
    expect(html.replace(/<\/?(?:code|span)[^>]*>/gu, '')).toContain('vps stopped waiting. On vps run pterm pair --via mac, then tap Add again.');
    expect(html).not.toContain('expired or was already used');
    expect(html).toContain('>Settings</button>');
    resetAdd();
    vi.unstubAllGlobals();
  });

  it('says what went wrong in plain words, never a peer\'s own text', () => {
    expect(addFailure(new ApiError(0, 'network', 'x'), 'vps')).toBe('This phone cannot reach vps. Check that it is on and in your tailnet.');
    expect(addFailure(new ApiError(0, 'computer_outdated', 'Update Termivoa on vps'), 'vps')).toBe('Update Termivoa on vps');
    expect(addFailure(new ApiError(410, 'unknown', 'x'), 'vps')).toBe('This add-computer link expired or was already used.');
    expect(addFailure(new Error('boom'), 'vps')).toBe('Could not add vps.');
  });
});

describe('peer add-computer page', () => {
  it('sends the user to Scan QR in the app and offers Copy link, never redeeming the grant', () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const html = renderToStaticMarkup(<PeerAddPage link={printed} />);
    expect(html).toContain('This page cannot add the computer. Open your Termivoa app on mac, then:');
    expect(html).toContain('Settings → + Add a computer → Scan QR');
    expect(html).toContain('Or copy this link and tap <strong>Paste link</strong> there.');
    expect(html).not.toContain('>Continue<');
    expect(html).toContain('>Copy link</button>');
    expect(html).not.toContain(grant);
    expect(fetch).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it('reads the via label only from an https origin', () => {
    expect(viaLabel(printed)).toBe('mac');
    expect(viaLabel('https://vps/add-computer#via=javascript%3Aalert(1)')).toBeUndefined();
    expect(viaLabel(undefined)).toBeUndefined();
    expect(renderToStaticMarkup(<PeerAddPage link={undefined} />)).toContain('This link is incomplete.');
  });
});

describe('Scan QR', () => {
  it('gives a scanned text to the same check as a pasted one: the same link for the confirm sheet', () => {
    expect(readLink(printed, app)).toEqual(parseAddComputerLink(printed, app));
    expect(readLink(printed, app)).toEqual(link);
    const html = renderToStaticMarkup(<AddComputerConfirm link={readLink(printed, app) as typeof link} node={{ id: 'n1', owner: 'ada@example.com', tags: [] }} onConfirm={noop} onCancel={noop} />);
    expect(html).toContain('<dt>Address</dt><dd>https://vps.tailx.ts.net</dd>');
  });

  it('says what is wrong with a QR code that is not a Termivoa add-computer link, in the paste words', () => {
    const via = encodeURIComponent(app.origin);
    expect(readLink('WIFI:S:cafe;T:WPA;P:secret;;', app)).toEqual({ problem: LINK_PROBLEMS.format });
    expect(readLink('https://example.com/', app)).toEqual({ problem: LINK_PROBLEMS.format });
    expect(readLink(`https://mac.tailx.ts.net/pair#${grant}`, app)).toEqual({ problem: LINK_PROBLEMS.format });
    expect(readLink(`https://vps.other.ts.net/add-computer#grant=${grant}&via=${via}`, app)).toEqual({ problem: LINK_PROBLEMS.tailnet });
    expect(readLink(`https://mac.tailx.ts.net/add-computer#grant=${grant}&via=${via}`, app)).toEqual({ problem: LINK_PROBLEMS.self });
    expect(readLink(`https://vps.tailx.ts.net/add-computer#grant=${grant}&via=${encodeURIComponent('https://pi.tailx.ts.net')}`, app)).toEqual({ problem: LINK_PROBLEMS.via });
    expect(readLink(`https://vps.tailx.ts.net/add-computer#grant=short&via=${via}`, app)).toEqual({ problem: LINK_PROBLEMS.grant });
  });

  it('shows the camera inline with a text alternative, and Cancel and Paste link', () => {
    const asking = renderToStaticMarkup(<ScanView scanning={false} onPaste={noop} onCancel={noop} />);
    expect(asking).toContain('playsInline=""');
    expect(asking).toContain('aria-label="Camera view" aria-describedby="scan-help"');
    expect(asking).toContain(`<p id="scan-help" role="status" class="hint-text">${SCAN_ASKING}</p>`);
    expect(SCAN_ASKING).toBe('Allow the camera to scan the QR code.');
    expect(asking).toContain('>Cancel</button>');
    expect(asking).toContain('>Paste link</button>');
    expect(renderToStaticMarkup(<ScanView scanning onPaste={noop} onCancel={noop} />)).toContain(SCAN_POINT);
    expect(SCAN_POINT).toBe('Point the camera at the QR code that pterm pair --via printed.');
  });

  it('says an expired link in plain words, after Continue', () => {
    expect(addFailure(new ApiError(410, 'pairing_expired', 'x'), 'vps')).toBe('This add-computer link expired or was already used.');
  });
});

describe('Settings → Computers', () => {
  const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
  const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };
  const settings = { projects: [], shellProfiles: [], hostBuild: '0.1.0-beta.2+1a2b3c4d5e6f' } as never;
  const entries: ComputerEntry[] = [
    { computer: mac, status: { kind: 'ok', settings, sessions: [], at: Date.now() } },
    { computer: vps, status: { kind: 'unreachable', lastSeen: Date.now() - 3 * 3_600_000, network: true } },
  ];

  it('lists each computer as online or offline, home with the Home badge and its session count', () => {
    expect(onlineText(entries[0] as ComputerEntry)).toEqual({ text: 'online', tone: 'green' });
    expect(onlineText(entries[1] as ComputerEntry)).toEqual({ text: 'offline', tone: 'muted' });
    expect(computerDetail(entries[0] as ComputerEntry, Date.now())).toBe('0 sessions');
    const html = renderToStaticMarkup(<ComputersSection entries={entries} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />);
    expect(html).toContain('<span class="computer-name">mac</span><span class="badge accent">Home</span>');
    expect(html).toContain('<span class="row-meta">0 sessions</span>');
    expect(html.match(/class="badge accent">Home</gu)).toHaveLength(1);
    expect(html).not.toContain('this computer');
    expect(html).toContain('<p class="hint-text">Home serves this app to your phone.</p>');
    expect(computerDetail(entries[1] as ComputerEntry, Date.now())).toBe("vps · can't reach");
    expect(html).toContain('<span class="hint-text">Last seen 3 h ago. Is it on, and in your tailnet?</span>');
  });

  it('shows no empty line under home while it is checked; a peer shows its address', () => {
    const loading: ComputerEntry[] = [{ computer: mac, status: { kind: 'loading' } }, { computer: vps, status: { kind: 'loading' } }];
    expect(computerDetail(loading[0] as ComputerEntry, Date.now())).toBe('');
    const html = renderToStaticMarkup(<ComputersSection entries={loading} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />);
    expect(html).not.toContain('<span class="row-meta"></span>');
    expect(html).toContain('<span class="row-meta">vps.tailx.ts.net</span>');
    expect(html.match(/<span class="computer-state muted">checking<\/span>/gu)).toHaveLength(2);
  });

  it('offers Check again on every computer, rename and remove for peers only, and + Add a computer', () => {
    const html = renderToStaticMarkup(<ComputersSection entries={entries} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />);
    expect(html.match(/<span>Check again<\/span><span class="off" aria-hidden="true">Checking…<\/span><\/span><\/button>/gu)).toHaveLength(2);
    // One status line per row for its own result, and one for rename and remove.
    expect(html.match(/role="status"/gu)).toHaveLength(3);
    expect(html.match(/>Rename<\/button>/gu)).toHaveLength(1);
    expect(html.match(/>Remove<\/button>/gu)).toHaveLength(1);
    expect(html).toContain('>+ Add a computer</button>');
    expect(html).toContain('<span class="computer-state green">online</span>');
    expect(html).toContain('<span class="computer-state muted">offline</span>');
    expect(html).toContain('Termivoa 0.1.0-beta.2+1a2b3c4d5e6f · last seen just now');
  });

  it('shows the build and when the phone last read the computer, only for a computer that answered', () => {
    const now = 10 * 3_600_000;
    const answered = (at: number): ComputerEntry => ({ computer: vps, status: { kind: 'ok', settings, sessions: [], at } });
    expect(healthText(answered(now), now)).toBe('Termivoa 0.1.0-beta.2+1a2b3c4d5e6f · last seen just now');
    expect(healthText(answered(now - 5 * 60_000), now)).toBe('Termivoa 0.1.0-beta.2+1a2b3c4d5e6f · last seen 5 min ago');
    expect(healthText(entries[1] as ComputerEntry, now)).toBeUndefined();
    expect(healthText({ computer: vps, status: { kind: 'pair' } }, now)).toBeUndefined();
  });

  it('shows a peer build as plain text, never as markup', () => {
    const hostile = { projects: [], shellProfiles: [], hostBuild: '<img src=x onerror=alert(1)>' } as never;
    const html = renderToStaticMarkup(
      <ComputersSection entries={[{ computer: vps, status: { kind: 'ok', settings: hostile, sessions: [], at: 0 } }]} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />,
    );
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;img');
  });

  it('shows no Found block before home answers, or when home is too old to list it', () => {
    const html = renderToStaticMarkup(<ComputersSection entries={entries} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />);
    expect(html).not.toContain('Found on your network');
    expect(FoundBlock({ found: undefined, homeName: 'mac', working: new Set(), app, onAdd: noop })).toBeNull();
  });

  it('lists each found computer by its label from home and its address, with Add', () => {
    const rows = [
      { origin: 'https://nas.tailx.ts.net', label: 'nas', grant, expiresAt: '2026-10-03T10:00:00Z' },
      { origin: 'https://vps.tailx.ts.net', label: '<b>vps</b>', grant, expiresAt: '2026-10-03T10:00:00Z' },
      // A row this app may not add (another tailnet) is not shown.
      { origin: 'https://evil.other.ts.net', label: 'evil', grant, expiresAt: '2026-10-03T10:00:00Z' },
    ];
    const html = renderToStaticMarkup(<FoundBlock found={{ kind: 'rows', computers: rows }} homeName="mac" working={new Set()} app={app} onAdd={noop} />);
    expect(html).toContain('<h3 id="found-heading" class="caps">Found on your network</h3>');
    expect(html).toContain('<span class="computer-name">nas</span></span><span class="row-meta">nas.tailx.ts.net</span>');
    expect(html).toContain('&lt;b&gt;vps&lt;/b&gt;');
    expect(html).toMatch(/<button type="button" class="pill-lit found-add" aria-label="Add nas"><svg[^>]*>.*?<\/svg>Add<\/button>/u);
    expect(html.match(/<\/svg>Add<\/button>/gu)).toHaveLength(2);
    expect(html).not.toContain('evil');
    expect(html).not.toContain(grant);
  });

  it('says how to make a computer show when none waits, and when home cannot read Tailscale', () => {
    const empty = renderToStaticMarkup(<FoundBlock found={{ kind: 'rows', computers: [] }} homeName="mac" working={new Set()} app={app} onAdd={noop} />);
    expect(empty.replace(/<\/?(?:code|span)[^>]*>/gu, '')).toContain(
      'To add another computer, run pterm setup on it, or pterm pair --via mac if it is already set up. It shows here while it waits.',
    );
    const down = renderToStaticMarkup(<FoundBlock found={{ kind: 'no_status' }} homeName="mac" working={new Set()} app={app} onAdd={noop} />);
    expect(down).toContain('mac cannot read its Tailscale status. Check Tailscale on mac.');
  });

  it('hides a computer this phone uses and reads now; one that lost its access shows again', () => {
    const row = { origin: 'https://vps.tailx.ts.net', label: 'vps', grant, expiresAt: '2026-10-03T10:00:00Z' };
    const found = { kind: 'rows' as const, computers: [row] };
    const working = renderToStaticMarkup(<FoundBlock found={found} homeName="mac" working={new Set([row.origin])} app={app} onAdd={noop} />);
    expect(working).not.toContain('aria-label="Add vps"');
    expect(working.replace(/<\/?(?:code|span)[^>]*>/gu, '')).toContain('To add another computer, run pterm setup on it');
    const lost = renderToStaticMarkup(<FoundBlock found={found} homeName="mac" working={new Set()} app={app} onAdd={noop} />);
    expect(lost).toContain('aria-label="Add vps"');
  });

  it('Add opens the same confirm sheet as a pasted link, with the same link marked found', () => {
    const onAdd = vi.fn();
    const row = { origin: 'https://vps.tailx.ts.net', label: 'vps', grant, expiresAt: '2026-10-03T10:00:00Z' };
    const tree = FoundBlock({ found: { kind: 'rows', computers: [row] }, homeName: 'mac', working: new Set(), app, onAdd });
    expect(onAdd).not.toHaveBeenCalled();
    buttons(tree).get('Add')?.();
    expect(onAdd).toHaveBeenCalledWith({ ...readLink(printed, app), found: true });
    const sheet = renderToStaticMarkup(<AddComputerSheet link={onAdd.mock.calls[0]?.[0] as typeof link} onClose={noop} go={noop} />);
    expect(sheet).toContain('Add this computer?');
  });

  it('+ Add a computer names home by its machine name, not the name given on this phone (#164)', () => {
    vi.stubGlobal('location', { origin: mac.origin });
    const renamed: ComputerEntry[] = [{ ...(entries[0] as ComputerEntry), computer: { ...mac, label: 'Work laptop' } }];
    const html = renderToStaticMarkup(<ComputersSection entries={renamed} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />);
    vi.unstubAllGlobals();
    expect(html).toContain('<code>pterm pair <span class="nowrap">--via</span> mac</code>');
    expect(html).not.toContain('--via Work laptop');
  });

  it('says how fast Check again answered, in seconds with one decimal', () => {
    expect(answeredText('vps', 230)).toBe('vps answered in 0.2 s.');
    expect(answeredText('vps', 40)).toBe('vps answered in 0.1 s.');
    expect(answeredText('vps', 0)).toBe('vps answered in 0.1 s.');
    expect(answeredText('vps', 1_960)).toBe('vps answered in 2.0 s.');
  });

  it('keeps Checking… on screen at least 600 ms, and no longer than the read when it is slower', async () => {
    vi.useFakeTimers();
    const fast = vi.fn();
    void atLeast(Promise.resolve('answer'), CHECKING_MIN_MS).then(fast);
    await vi.advanceTimersByTimeAsync(CHECKING_MIN_MS - 1);
    expect(fast).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fast).toHaveBeenCalledWith('answer');
    const failed = vi.fn();
    void atLeast(Promise.reject(new Error('down')), CHECKING_MIN_MS).catch(failed);
    await vi.advanceTimersByTimeAsync(CHECKING_MIN_MS - 1);
    expect(failed).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(failed).toHaveBeenCalledOnce();
    const slow = vi.fn();
    void atLeast(new Promise((resolve) => { setTimeout(() => resolve('late'), 2_000); }), CHECKING_MIN_MS).then(slow);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(slow).toHaveBeenCalledWith('late');
    vi.useRealTimers();
  });

  it('says what Check again found when the read fails', () => {
    const onFailure = () => 'Request failed';
    expect(checkFailureText(vps, new ReadFailure(vps, new ApiError(0, 'network', 'Connection unavailable')), onFailure)).toBe("vps · can't reach");
    expect(checkFailureText(vps, new ReadFailure(vps, new ApiError(500, 'internal_error', 'boom')), onFailure)).toBe('Request failed');
    // The refusal text names the origin's first label; the row shows the name on this phone.
    const outdated = new ReadFailure(vps, new ApiError(0, 'computer_outdated', 'Update Termivoa on vps'));
    expect(checkFailureText({ label: 'server' }, outdated, onFailure)).toBe('Update Termivoa on server');
  });
});
