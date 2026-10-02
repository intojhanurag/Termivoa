import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import type { Computer } from '../computers';
import { parseAddComputerLink } from '../pairing/addComputer';
import { findAddComputerLinks } from '../terminal/pathLinks';
import {
  AddComputerConfirm, AddComputerContinue, AddComputerPage, AddComputerSheet, addFailure, cancelOnUnmount, ownerText, PeerAddPage, viaLabel,
} from './AddComputer';
import type { ComputerEntry } from './allSessions';
import { computerDetail, ComputersSection, onlineText } from './Computers';

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
    const html = renderToStaticMarkup(<AddComputerPage link={undefined} deviceLabel="iPhone" onDone={noop} onSettings={noop} />);
    expect(html).toContain('This add-computer link is used or incomplete.');
  });

  it('shows the full origin and waits for Continue before it sends anything', () => {
    const fetch = vi.fn(() => new Promise(noop));
    vi.stubGlobal('fetch', fetch);
    const html = renderToStaticMarkup(<AddComputerPage link={link} deviceLabel="iPhone" onDone={noop} onSettings={noop} />);
    expect(html).toContain('<dt>Address</dt><dd>https://vps.tailx.ts.net</dd>');
    expect(html).toContain('<button type="button" class="primary">Continue</button>');
    expect(html).not.toContain('Connecting to vps…');
    expect(fetch).not.toHaveBeenCalled();
    const onContinue = vi.fn();
    buttons(AddComputerContinue({ link, onContinue, onCancel: noop })).get('Continue')?.();
    expect(onContinue).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  it('shows why the app refused a link on this load', () => {
    const html = renderToStaticMarkup(<AddComputerPage link={undefined} notice="Not used." deviceLabel="iPhone" onDone={noop} onSettings={noop} />);
    expect(html).toContain('Not used.');
    expect(html).not.toContain('used or incomplete');
  });

  it('stops the flow when the page goes away', () => {
    const cancel = vi.fn();
    cancelOnUnmount({ current: { cancel } })();
    expect(cancel).toHaveBeenCalledOnce();
    expect(() => cancelOnUnmount({ current: undefined })()).not.toThrow();
  });

  it('says what went wrong in plain words, never a peer\'s own text', () => {
    expect(addFailure(new ApiError(0, 'network', 'x'), 'vps')).toBe('This phone cannot reach vps. Check that it is on and in your tailnet.');
    expect(addFailure(new ApiError(0, 'computer_outdated', 'Update Termivoa on vps'), 'vps')).toBe('Update Termivoa on vps');
    expect(addFailure(new ApiError(410, 'unknown', 'x'), 'vps')).toBe('This add-computer link expired or was already used.');
    expect(addFailure(new Error('boom'), 'vps')).toBe('Could not add vps.');
  });
});

describe('peer add-computer page', () => {
  it('names the app to paste into and offers Copy link, never redeeming the grant', () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const html = renderToStaticMarkup(<PeerAddPage link={printed} />);
    expect(html).toContain('Open your Termivoa app on mac and paste this link there.');
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

describe('Settings → Computers', () => {
  const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
  const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };
  const settings = { projects: [], shellProfiles: [] } as never;
  const entries: ComputerEntry[] = [
    { computer: mac, status: { kind: 'ok', settings, sessions: [], at: 0 } },
    { computer: vps, status: { kind: 'unreachable', lastSeen: Date.now() - 3 * 3_600_000 } },
  ];

  it('lists each computer as online or offline, home as this computer', () => {
    expect(onlineText(entries[0] as ComputerEntry)).toEqual({ text: 'online', tone: 'green' });
    expect(onlineText(entries[1] as ComputerEntry)).toEqual({ text: 'offline', tone: 'muted' });
    expect(computerDetail(entries[0] as ComputerEntry, Date.now())).toBe('0 sessions · this computer');
    expect(computerDetail(entries[1] as ComputerEntry, Date.now())).toBe("vps · can't reach · last seen 3 h ago");
  });

  it('offers Retry for an offline computer, rename and remove for peers only, and + Add a computer', () => {
    const html = renderToStaticMarkup(<ComputersSection entries={entries} onRetry={noop} onChanged={noop} onFailure={() => ''} />);
    expect(html.match(/>Rename<\/button>/gu)).toHaveLength(1);
    expect(html.match(/>Remove<\/button>/gu)).toHaveLength(1);
    expect(html).toContain('>Retry</button>');
    expect(html).toContain('>+ Add a computer</button>');
    expect(html).toContain('<code>pterm pair --via mac</code>');
    expect(html).toContain('<span class="computer-state green">online</span>');
    expect(html).toContain('<span class="computer-state muted">offline</span>');
  });
});
