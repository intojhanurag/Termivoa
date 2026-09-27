import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { pathFor, previewPortParam, titleFor } from './App';
import { Preview, PreviewBar, PreviewStatus } from './Preview';
import { isPreviewPort, latestPreview, PreviewChip, previewOrigin, previewSrc } from './PreviewChip';

const session: BrokerRPCSessionRecord = {
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
  it('frames the select address in a sandbox with the address pill', () => {
    const html = renderToStaticMarkup(
      <Preview session={session} sessions={[session]} origin={origin} port={5173} onBack={noop} onTell={noop} />,
    );
    const src = previewSrc(origin, session.id, 5173);
    expect(src).toBe(`${origin}/__termivoa/select?session=${session.id}&port=5173`);
    expect(html).toContain(`src="${src.replace('&', '&amp;')}"`);
    expect(html).toContain('sandbox="allow-scripts allow-forms allow-same-origin"');
    expect(html).toContain('referrerPolicy="no-referrer"');
    expect(html).toContain('title="Preview of localhost:5173"');
    expect(html).not.toContain(' allow="');
    expect(html).toContain('<span class="preview-address"><span class="dot green" aria-hidden="true"></span>localhost:5173</span>');
    expect(html).not.toContain('Dev server stopped');
  });

  it('wires back, reload, and Tell agent', () => {
    const onBack = vi.fn();
    const onReload = vi.fn();
    const onTell = vi.fn();
    const bar = buttons(PreviewBar({ port: 5173, onBack, onReload }));
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
