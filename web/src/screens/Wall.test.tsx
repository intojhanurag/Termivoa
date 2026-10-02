import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { keyOf, type HostedSession } from './allSessions';
import {
  mergeOrder, moveTile, orderTiles, readWallOrder, saveWallOrder, tileLabel, tileMark, tileOffset, tileScale, Wall, WALL_ORDER_KEY, WallGrid, wallSessions,
} from './Wall';

const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };

const session = (id: string, extra: Partial<BrokerRPCSessionRecord> = {}, computer = mac): HostedSession => ({
  computer,
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

/** The first element of this component type in an element tree. */
function find(node: ReactNode, type: unknown): ReactElement<Record<string, unknown>> | undefined {
  if (Array.isArray(node)) {
    for (const child of node as ReactNode[]) {
      const found = find(child, type);
      if (found !== undefined) return found;
    }
  } else if (isValidElement(node)) {
    if (node.type === type) return node as ReactElement<Record<string, unknown>>;
    return find((node.props as { children?: ReactNode }).children, type);
  }
  return undefined;
}

const memoryStore = (value?: string) => {
  const items = new Map<string, string>(value === undefined ? [] : [[WALL_ORDER_KEY, value]]);
  return { getItem: (key: string) => items.get(key) ?? null, setItem: (key: string, next: string) => { items.set(key, next); } };
};

describe('wall order', () => {
  it('puts tiles in the saved order and keeps the others after them in their own order', () => {
    const shown = [waiting, working, idle];
    expect(orderTiles(shown, [], keyOf).map((item) => item.id)).toEqual([waiting.id, working.id, idle.id]);
    expect(orderTiles(shown, [keyOf(idle), 'gone', keyOf(waiting)], keyOf).map((item) => item.id)).toEqual([idle.id, waiting.id, working.id]);
  });

  it('keys tiles by computer and session, so equal ids on two computers keep their own places', () => {
    const there = session('1', {}, vps);
    expect(keyOf(idle)).toBe(`mac.tailx.ts.net/${idle.id}`);
    expect(orderTiles([idle, there], [keyOf(there), keyOf(idle)], keyOf)).toEqual([there, idle]);
  });

  it('moves one tile to a new place', () => {
    expect(moveTile(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd']);
    expect(moveTile(['a', 'b', 'c', 'd'], 3, 0)).toEqual(['d', 'a', 'b', 'c']);
    expect(moveTile(['a', 'b'], 1, 1)).toEqual(['a', 'b']);
  });

  it('keeps saved places of sessions that are not on the Wall now, up to 100', () => {
    expect(mergeOrder(['b', 'a'], ['a', 'x', 'b', 'y'])).toEqual(['b', 'a', 'x', 'y']);
    expect(mergeOrder([], Array.from({ length: 150 }, (_, index) => `${index}`))).toHaveLength(100);
  });

  it('saves the order on this device and ignores a damaged value', () => {
    const store = memoryStore();
    saveWallOrder(['h/b', 'h/a'], store);
    expect(readWallOrder(store)).toEqual(['h/b', 'h/a']);
    expect(readWallOrder(memoryStore('{"a":1}'))).toEqual([]);
    expect(readWallOrder(memoryStore('not json'))).toEqual([]);
    expect(readWallOrder(memoryStore('["h/a",2,"h/b"]'))).toEqual(['h/a', 'h/b']);
    expect(readWallOrder(undefined)).toEqual([]);
  });
});

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
    // A tall grid fills the width; tileOffset picks its rows.
    expect(tileScale(160, 120, 320, 480)).toBe(0.5);
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

  it('gives the running sessions and onOpen to the tiles, and goes back to the list', () => {
    const onOpen = vi.fn();
    const onList = vi.fn();
    const tree = Wall({ sessions: [idle, waiting, exited], onOpen, onNew: noop, onList });
    const grid = find(tree, WallGrid);
    expect(grid?.props.shown).toEqual([waiting, idle]);
    expect(grid?.props.onOpen).toBe(onOpen);
    buttons(tree).get('Session list')?.();
    expect(onList).toHaveBeenCalledOnce();
  });

  it('names each tile\'s computer when there is more than one', () => {
    const there = session('6', {}, vps);
    const html = renderToStaticMarkup(<Wall sessions={[idle, there]} multi onOpen={noop} onNew={noop} onList={noop} />);
    expect(html).toContain('<span class="wall-computer"><span class="computer-tag"><span class="dot" style="background:#9B8CFF" aria-hidden="true"></span>vps</span></span>');
    expect(renderToStaticMarkup(<Wall sessions={[idle]} onOpen={noop} onNew={noop} onList={noop} />)).not.toContain('wall-computer');
  });

  it('labels a tile for screen readers', () => {
    expect(tileLabel(waiting, false)).toBe('claude, Waiting, open session');
  });
});

describe('tileOffset', () => {
  it('shows the top while the cursor fits, then follows the cursor to the end', () => {
    expect(tileOffset(300, 800, 120)).toBe(0);
    expect(tileOffset(300, 800, 500)).toBe(200);
    expect(tileOffset(300, 800, 800)).toBe(500);
    expect(tileOffset(300, 200, 150)).toBe(0);
    expect(tileOffset(300, 800, undefined)).toBe(0);
  });
});

describe('tile budget marks', () => {
  it('shows Paused for a tile beyond the live budget, and the amber dot only for a live tile that is not connected', () => {
    expect(tileMark('paused', 'connecting')).toBe('paused');
    expect(tileMark('paused', 'live')).toBe('paused');
    // Never live: no last frame to keep, so the name and Paused fill the tile.
    expect(tileMark('paused', 'connecting', false)).toBe('blank');
    expect(tileMark('live', 'live', false)).toBeUndefined();
    expect(tileMark('live', 'reconnecting')).toBe('reconnecting');
    expect(tileMark('live', 'live')).toBeUndefined();
  });

  it('draws every tile paused until the scheduler gives it a live slot; a never-live tile shows its name and Paused', () => {
    const html = renderToStaticMarkup(<Wall sessions={[idle]} onOpen={noop} onNew={noop} onList={noop} />);
    expect(html).toMatch(/<span class="wall-blank"><span>[^<]+<\/span><span class="wall-paused-text">Paused<\/span><\/span>/u);
  });
});
