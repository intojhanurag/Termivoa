import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import type { HostedSession } from './allSessions';
import { pathFor, previewPortParam, titleFor } from './App';
import { cantPreview, Preview, PREVIEW_RELOAD_KEY, PreviewBar, PreviewBody, PreviewStatus, probePreview, reloadForPolicy } from './Preview';
import { isPreviewPort, latestPreview, PreviewChip, previewOrigin, previewSrc } from './PreviewChip';

const session: HostedSession = {
  computer: { origin: 'https://mac.tailnet.ts.net', host: 'mac.tailnet.ts.net', label: 'mac', color: '#64D8E8', home: true },
  id: '84444444-4444-4444-8444-444444444444',
  generation: '85555555-5555-4555-8555-555555555555',
  title: 'claude',
  projectId: '81111111-1111-4111-8111-111111111111',
  shellProfileId: '82222222-2222-4222-8222-222222222222',
  state: 'running',
  columns: 80,
  rows: 24,
  createdAt: '2026-09-27T10:00:00Z',
  updatedAt: '2026-09-27T10:00:00Z',
  previews: [{ port: 3000, since: '2026-09-27T10:05:00Z' }, { port: 5173, since: '2026-09-27T10:06:00Z' }],
};
const origin = 'https://mac.tailnet.ts.net:8443';
const vps: HostedSession = {
  ...session,
  computer: { origin: 'https://vps.tailnet.ts.net', host: 'vps.tailnet.ts.net', label: 'vps', color: '#F2B544', home: false },
};
const vpsOrigin = 'https://vps.tailnet.ts.net:8443';
const noop = () => undefined;

/** Host buttons of a hookless component's element tree, by visible text or label. */
function buttons(node: ReactNode, found = new Map<string, () => void>()): Map<string, () => void> {
  if (Array.isArray(node)) {
    node.forEach((child: ReactNode) => buttons(child, found));
  } else if (isValidElement(node)) {
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void; 'aria-label'?: string }>;
    if (node.type === 'button' && props.onClick !== undefined) {
      found.set(props['aria-label'] ?? String(props.children), props.onClick);
    }
    buttons(props.children, found);
  }
  return found;
}

describe('preview chip', () => {
  it('shows the most recent port and how many others', () => {
    expect(latestPreview(session.previews)).toEqual({ port: 5173, more: 1 });
    const html = renderToStaticMarkup(<PreviewChip previews={session.previews} onOpen={noop} />);
    expect(html).toContain('Preview ready');
    expect(html).toContain('<span class="preview-port">:5173</span>');
    expect(html).toContain('<span class="preview-more">+1</span>');
    expect(html).toContain('aria-label="Open preview of port 5173"');
    expect(html).toContain('Open ↗');
  });

  it('opens the shown port', () => {
    const onOpen = vi.fn();
    const chip = PreviewChip({ previews: session.previews, onOpen });
    buttons(chip).get('Open preview of port 5173')?.();
    expect(onOpen).toHaveBeenCalledWith(5173);
  });

  it('is hidden without a preview origin or without ports', () => {
    expect(renderToStaticMarkup(<PreviewChip previews={session.previews} onOpen={undefined} />)).toBe('');
    expect(renderToStaticMarkup(<PreviewChip previews={undefined} onOpen={noop} />)).toBe('');
    expect(renderToStaticMarkup(<PreviewChip previews={[{ port: 0, since: '2026-09-27T10:00:00Z' }]} onOpen={noop} />)).toBe('');
    expect(latestPreview([{ port: 8080, since: 'x' }])).toEqual({ port: 8080, more: 0 });
  });
});

describe('preview addresses', () => {
  it('accepts only integer ports 1–65535', () => {
    expect([1, 5173, 65535].every(isPreviewPort)).toBe(true);
    expect([0, 65536, 3.5, Number.NaN, '5173', undefined].some(isPreviewPort)).toBe(false);
  });

  it('accepts only http(s) origins and drops any path', () => {
    expect(previewOrigin(origin)).toBe(origin);
    expect(previewOrigin('http://127.0.0.1:8790/')).toBe('http://127.0.0.1:8790');
    expect(previewOrigin('javascript:alert(1)')).toBeUndefined();
    expect(previewOrigin('//evil.example')).toBeUndefined();
    expect(previewOrigin('https://')).toBeUndefined();
    expect(previewOrigin(undefined)).toBeUndefined();
  });

  it('round-trips the preview path and rejects bad ports', () => {
    const path = pathFor({ name: 'preview', session, port: 5173 });
    expect(path).toBe(`/sessions/${session.id}/preview?port=5173`);
    expect(previewPortParam(path.slice(path.indexOf('?')))).toBe(5173);
    for (const bad of ['', '?port=0', '?port=70000', '?port=5173.0', '?port=-1', '?port=1e3', '?port=abc']) {
      expect(previewPortParam(bad)).toBeUndefined();
    }
    expect(titleFor({ name: 'preview', session, port: 5173 })).toBe('Preview :5173 · Termivoa');
  });
});

describe('preview screen', () => {
  const body = (props: Partial<Parameters<typeof PreviewBody>[0]>) => renderToStaticMarkup(
    <PreviewBody session={session} origin={origin} port={5173} notice={undefined} reach {...props} />,
  );

  it('frames the select address in a sandbox, with the app origin as the only referrer', () => {
    const src = previewSrc(origin, session.id, 5173);
    expect(src).toBe(`${origin}/__termivoa/select?session=${session.id}&port=5173`);
    // The whole element: no allow, no allowfullscreen, nothing else.
    expect(body({})).toBe(
      `<iframe class="preview-frame" title="Preview of localhost:5173" src="${src.replace('&', '&amp;')}" sandbox="allow-scripts allow-forms allow-same-origin" referrerPolicy="origin"></iframe>`,
    );
    expect(body({ session: vps, origin: vpsOrigin })).toBe(
      `<iframe class="preview-frame" title="Preview of localhost:5173 on vps" src="${previewSrc(vpsOrigin, vps.id, 5173).replace('&', '&amp;')}" sandbox="allow-scripts allow-forms allow-same-origin" referrerPolicy="origin"></iframe>`,
    );
  });

  it('shows the address pill, and no frame before the probe answered', () => {
    const html = renderToStaticMarkup(
      <Preview session={session} sessions={[session]} origin={origin} port={5173} onBack={noop} onTell={noop} />,
    );
    expect(html).toContain('<span class="preview-address"><span class="dot green" aria-hidden="true"></span>localhost:5173</span>');
    expect(html).toContain('Opening preview…');
    expect(html).not.toContain('<iframe');
    expect(html).not.toContain('Dev server stopped');
  });

  it('over a peer, the bar always names the computer and marks the page as untrusted; home has neither', () => {
    const mark = '<p class="preview-peer"><span class="computer-tag"><span class="dot" style="background:#F2B544" aria-hidden="true"></span>vps</span> · <strong>untrusted page</strong></p>';
    const screen = (props: Partial<Parameters<typeof Preview>[0]>) => renderToStaticMarkup(
      <Preview session={vps} sessions={[vps]} origin={vpsOrigin} port={5173} onBack={noop} onTell={noop} {...props} />,
    );
    // While the probe runs, with a notice in place of the frame, without an origin, and without the session in the list.
    for (const html of [screen({}), screen({ notice: 'vps · pair again' }), screen({ origin: undefined }), screen({ sessions: undefined })]) {
      expect(html).toContain(mark);
    }
    const home = screen({ session, sessions: [session], origin });
    expect(home).not.toContain('preview-peer');
    expect(home).not.toContain('untrusted page');
  });

  it('a computer that refuses the phone, cannot be reached or must update has no frame, only its text', () => {
    for (const notice of ['vps · pair again', 'vps · can\'t reach', 'Update Termivoa on vps']) {
      const html = body({ session: vps, origin: vpsOrigin, notice });
      expect(html).toBe(`<div class="banner" role="alert"><span>${notice.replace('\'', '&#x27;')}</span></div>`);
    }
    // No origin (a peer that has not answered yet): nothing to frame.
    expect(body({ session: vps, origin: undefined })).not.toContain('<iframe');
  });

  it('asks the preview origin before framing it; no answer shows neutral words, never a cause', async () => {
    const text = 'Can&#x27;t open the preview on vps. If vps does not share dev-server previews yet, run pterm setup on vps and answer yes to the preview question.';
    expect(body({ session: vps, origin: vpsOrigin, reach: false })).toBe(`<div class="banner" role="alert"><span>${text}</span></div>`);
    expect(body({ reach: false })).toContain('Can&#x27;t open the preview on mac.');
    expect(body({ reach: undefined })).toBe('<p role="status" class="muted preview-note">Opening preview…</p>');
    expect(cantPreview('vps')).not.toMatch(/network|offline|time|blocked|reach/iu);

    const timeout = vi.spyOn(AbortSignal, 'timeout');
    const answered = vi.fn(async () => new Response(null, { status: 403 }));
    // Any HTTP answer means the port is served, a refusal too.
    expect(await probePreview(vpsOrigin, answered as unknown as typeof fetch)).toBe(true);
    const [url, init] = answered.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${vpsOrigin}/__termivoa/select`);
    expect(init).toMatchObject({ mode: 'no-cors', credentials: 'omit' });
    expect(timeout).toHaveBeenCalledWith(5000);
    // A network error, a CSP block (both a TypeError) and a timeout end the same way.
    for (const error of [new TypeError('Failed to fetch'), new DOMException('timeout', 'TimeoutError')]) {
      expect(await probePreview(vpsOrigin, (async () => { throw error; }) as unknown as typeof fetch)).toBe(false);
    }
    timeout.mockRestore();
  });

  it('wires back, reload, and Tell agent', () => {
    const onBack = vi.fn();
    const onReload = vi.fn();
    const onTell = vi.fn();
    const bar = buttons(PreviewBar({ port: 5173, peer: undefined, onBack, onReload }));
    bar.get('‹ terminal')?.();
    bar.get('Reload preview')?.();
    buttons(PreviewStatus({ session, fresh: false, onTell })).get('Tell agent')?.();
    expect([onBack, onReload, onTell].map((fn) => fn.mock.calls.length)).toEqual([1, 1, 1]);
  });

  it('shows the agent status from the record', () => {
    const status = (agent: BrokerRPCSessionRecord['agent'], fresh = false) =>
      renderToStaticMarkup(<PreviewStatus session={{ ...session, ...(agent === undefined ? {} : { agent }) }} fresh={fresh} onTell={noop} />);
    const since = '2026-09-27T10:00:00Z';
    expect(status({ state: 'working', stateSince: since })).toMatch(/class="spinner".*claude · working/u);
    expect(status({ state: 'waiting', stateSince: since })).toContain('claude · waiting for you');
    expect(status({ state: 'idle', stateSince: since, afterWork: true }, true)).toContain('claude · your turn');
    expect(status(undefined)).toContain('<span class="preview-agent-text">claude</span>');
  });
});

describe('a preview origin the loaded policy blocks (a computer added in this visit)', () => {
  const memory = () => {
    const values = new Map<string, string>();
    return { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value); }, values };
  };

  it('reloads once for a blocked address on that origin, and remembers it', () => {
    const store = memory();
    expect(reloadForPolicy(`${vpsOrigin}/__termivoa/select`, vpsOrigin, store)).toBe(true);
    expect(store.values.get(PREVIEW_RELOAD_KEY)).toBe(vpsOrigin);
    expect(reloadForPolicy(vpsOrigin, vpsOrigin, store)).toBe(false);
  });

  it('does not reload for another origin, a near miss, or without storage', () => {
    const store = memory();
    expect(reloadForPolicy('https://evil.tailnet.ts.net:8443', vpsOrigin, store)).toBe(false);
    expect(reloadForPolicy(`${vpsOrigin}0/x`, vpsOrigin, store)).toBe(false);
    expect(reloadForPolicy(vpsOrigin, vpsOrigin, undefined)).toBe(false);
    expect(reloadForPolicy(vpsOrigin, vpsOrigin, { getItem: () => null, setItem: () => { throw new Error('full'); } })).toBe(false);
    expect(store.values.size).toBe(0);
  });
});
