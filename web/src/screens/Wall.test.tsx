import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { tileLabel, tileScale, Wall, wallSessions } from './Wall';

const session = (id: string, extra: Partial<BrokerRPCSessionRecord> = {}): BrokerRPCSessionRecord => ({
  id: `84444444-4444-4444-8444-44444444444${id}`,
  generation: '85555555-5555-4555-8555-555555555555',
  title: `shell ${id}`,
  projectId: '81111111-1111-4111-8111-111111111111',
  shellProfileId: '82222222-2222-4222-8222-222222222222',
  state: 'running',
  columns: 80,
  rows: 24,
  createdAt: '2026-09-27T10:00:00Z',
  updatedAt: '2026-09-27T10:00:00Z',
  ...extra,
});

const idle = session('1');
const working = session('2', { agent: { state: 'working', stateSince: '2026-09-27T11:56:00Z' } });
const exited = session('3', { state: 'exited', exitCode: 0 });
const waiting = session('4', {
  title: 'claude',
  titleIsCustom: true,
  agent: { state: 'waiting', stateSince: '2026-09-27T11:58:00Z', question: { id: 'q1', text: 'Run the tests?', options: [{ label: 'Yes' }] } },
});
const lost = session('5', { state: 'lost' });
const noop = () => undefined;

/** Host buttons by label. Wall has no hooks, so its element tree is read directly; tiles are not mounted. */
function buttons(node: ReactNode, found = new Map<string, () => void>()): Map<string, () => void> {
  if (Array.isArray(node)) {
    node.forEach((child: ReactNode) => buttons(child, found));
  } else if (isValidElement(node)) {
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void; 'aria-label'?: string }>;
    if (node.type === 'button' && props.onClick !== undefined) {
      found.set(props['aria-label'] ?? 'new session', props.onClick);
    }
    buttons(props.children, found);
  }
  return found;
}

describe('wallSessions', () => {
  it('shows running sessions: waiting, then working, then the rest in list order', () => {
    const wall = wallSessions([idle, working, exited, waiting, lost]);
    expect(wall.shown.map((item) => item.id)).toEqual([waiting.id, working.id, idle.id]);
    expect(wall.ended).toBe(2);
    expect(wallSessions([]).shown).toEqual([]);
  });
});

describe('tileScale', () => {
  it('fits the whole grid in the tile and never enlarges it', () => {
    expect(tileScale(160, 120, 640, 240)).toBe(0.25);
    expect(tileScale(160, 120, 320, 480)).toBe(0.25);
    expect(tileScale(400, 300, 200, 100)).toBe(1);
  });

  it('is 0 until the tile and the grid are measured', () => {
    expect(tileScale(0, 120, 640, 240)).toBe(0);
    expect(tileScale(160, 120, 0, 0)).toBe(0);
    expect(tileScale(160, 120, Number.NaN, 240)).toBe(0);
  });
});

describe('Wall', () => {
  it('renders one read-only tile per running session, with name, status and question', () => {
    const html = renderToStaticMarkup(<Wall sessions={[idle, working, exited, waiting]} onOpen={noop} onNew={noop} onList={noop} />);
    expect(html.match(/<button type="button" class="wall-tile[^"]*"/gu)).toEqual([
      '<button type="button" class="wall-tile asking"',
      '<button type="button" class="wall-tile"',
      '<button type="button" class="wall-tile"',
    ]);
    expect(html).toContain('aria-label="claude, Waiting, open session"');
    expect(html).toContain('aria-label="shell 2, Working, open session"');
    expect(html).toContain('aria-label="shell 1, open session"');
    expect(html).toContain('<span class="wall-question">Run the tests?</span>');
    expect(html).toContain('<span class="wall-screen" aria-hidden="true">');
    expect(html).toContain('1 ended session is on the list.');
    expect(html).not.toContain('No running sessions');
  });

  it('shows the empty state with New session when nothing runs', () => {
    const onNew = vi.fn();
    const html = renderToStaticMarkup(<Wall sessions={[exited]} onOpen={noop} onNew={onNew} onList={noop} />);
    expect(html).toContain('No running sessions');
    expect(html).not.toContain('wall-tile');
    buttons(Wall({ sessions: [exited], onOpen: noop, onNew, onList: noop })).get('new session')?.();
    expect(onNew).toHaveBeenCalledOnce();
  });

  it('opens the tapped session, and goes back to the list', () => {
    const onOpen = vi.fn();
    const onList = vi.fn();
    const found = buttons(Wall({ sessions: [idle, waiting], onOpen, onNew: noop, onList }));
    found.get('shell 1, open session')?.();
    expect(onOpen).toHaveBeenCalledWith(idle);
    found.get('Session list')?.();
    expect(onList).toHaveBeenCalledOnce();
  });

  it('labels a tile for screen readers', () => {
    expect(tileLabel(waiting, false)).toBe('claude, Waiting, open session');
  });
});
