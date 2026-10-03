import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { Computer } from '../computers';
import { LINK_PROBLEMS, parseAddComputerLink } from '../pairing/addComputer';
import { SCAN_ASKING, SCAN_POINT } from '../pairing/scan';
import { findAddComputerLinks } from '../terminal/pathLinks';
import {
  AddComputerConfirm, AddComputerContinue, AddComputerPage, AddComputerSheet, addFailure, LeaveAdd, ownerText, PeerAddPage, viaLabel,
} from './AddComputer';
import { ApiError } from '../api/http';
import { ReadFailure, type ComputerEntry } from './allSessions';
import { checkFailureText, computerDetail, ComputersSection, healthText, onlineText, readLink, ScanView } from './Computers';

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
    if (node.type === 'button' && props.onClick !== undefined) found.set(String(props.children), props.onClick);
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
    { computer: vps, status: { kind: 'unreachable', lastSeen: Date.now() - 3 * 3_600_000 } },
  ];

  it('lists each computer as online or offline, home as this computer', () => {
    expect(onlineText(entries[0] as ComputerEntry)).toEqual({ text: 'online', tone: 'green' });
    expect(onlineText(entries[1] as ComputerEntry)).toEqual({ text: 'offline', tone: 'muted' });
    expect(computerDetail(entries[0] as ComputerEntry, Date.now())).toBe('0 sessions · this computer');
    expect(computerDetail(entries[1] as ComputerEntry, Date.now())).toBe("vps · can't reach · last seen 3 h ago");
  });

  it('offers Check again on every computer, rename and remove for peers only, and + Add a computer', () => {
    const html = renderToStaticMarkup(<ComputersSection entries={entries} onCheck={() => Promise.resolve()} onChanged={noop} onFailure={() => ''} />);
    expect(html.match(/>Check again<\/button>/gu)).toHaveLength(2);
    // One status line per row for its own result, and one for rename and remove.
    expect(html.match(/role="status"/gu)).toHaveLength(3);
    expect(html.match(/>Rename<\/button>/gu)).toHaveLength(1);
    expect(html.match(/>Remove<\/button>/gu)).toHaveLength(1);
    expect(html).toContain('>+ Add a computer</button>');
    expect(html).toContain('<code>pterm pair --via mac</code>');
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

  it('says what Check again found when the read fails', () => {
    const onFailure = () => 'Request failed';
    expect(checkFailureText(vps, new ReadFailure(vps, new ApiError(0, 'network', 'Connection unavailable')), onFailure)).toBe("vps · can't reach");
    expect(checkFailureText(vps, new ReadFailure(vps, new ApiError(500, 'internal_error', 'boom')), onFailure)).toBe('Request failed');
    // The refusal text names the origin's first label; the row shows the name on this phone.
    const outdated = new ReadFailure(vps, new ApiError(0, 'computer_outdated', 'Update Termivoa on vps'));
    expect(checkFailureText({ label: 'server' }, outdated, onFailure)).toBe('Update Termivoa on server');
  });
});
