import '@xterm/xterm/css/xterm.css';
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent, type PointerEvent } from 'react';
import { client } from '../api/http';
import { homeComputer } from '../computers';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { Connection, INITIAL_STATUS, type LinkState } from '../terminal/Connection';
import { scheduler, THUMBNAIL_UPDATES_PER_SECOND, type TileHandle, type TileState } from '../terminal/scheduler';
import { TerminalView } from '../terminal/TerminalView';
import { agentBadge, hasNewOutput, readViewed, since, storage, type StorageLike, type ViewedTimes } from './activity';
import { keyOf, type HostedSession } from './allSessions';
import { sessionName } from './folder';
import { websocketUrl } from './hooks';
import { ComputerTag, StatusPill } from './Sessions';

/** Tiles draw at this font size, then shrink to fit the tile. */
export const TILE_FONT = 12;
/** This device's tile order, as `host/id` session keys (a bare id is an older home entry). Not secret. */
export const WALL_ORDER_KEY = 'pterm.wallOrder';
const MAX_ORDER = 100;
/** A tile moves after a finger holds it this long; a move before that scrolls. */
const HOLD_MS = 300;
const SLOP_PX = 8;
/** Dragging this close to the top or bottom edge scrolls the page. */
const EDGE_PX = 56;
const SCROLL_STEP_PX = 10;

const noSubscribe = () => () => undefined;
const initialStatus = () => INITIAL_STATUS;
const ended = (session: BrokerRPCSessionRecord) => session.state === 'exited' || session.state === 'lost';
const rank = (session: BrokerRPCSessionRecord) => {
  switch (session.agent?.state) {
    case 'waiting': return 0;
    case 'working': return 1;
    default: return 2;
  }
};

/** Running sessions, waiting first, then working, then the rest in the broker's order; and how many ended. */
export function wallSessions<T extends BrokerRPCSessionRecord>(sessions: ReadonlyArray<T>): { shown: T[]; ended: number } {
  const shown = sessions.filter((session) => !ended(session)).sort((a, b) => rank(a) - rank(b));
  return { shown, ended: sessions.length - shown.length };
}

/** The saved order as session keys; an older bare id means a home session. */
export function readWallOrder(store: StorageLike | undefined = storage(), home?: string): string[] {
  try {
    const parsed: unknown = JSON.parse(store?.getItem(WALL_ORDER_KEY) ?? '[]');
    return Array.isArray(parsed)
      ? parsed.filter((id): id is string => typeof id === 'string').slice(0, MAX_ORDER).map((id) => (id.includes('/') ? id : `${home ?? homeComputer().host}/${id}`))
      : [];
  } catch {
    return [];
  }
}

export function saveWallOrder(ids: ReadonlyArray<string>, store: StorageLike | undefined = storage()): void {
  try {
    store?.setItem(WALL_ORDER_KEY, JSON.stringify(ids.slice(0, MAX_ORDER)));
  } catch {
    // Private browsing may refuse storage; the order then lasts for this visit.
  }
}

/** Tiles in the saved order; tiles it does not name follow in their own order. */
export function orderTiles<T>(shown: ReadonlyArray<T>, order: ReadonlyArray<string>, key: (item: T) => string): T[] {
  const place = new Map(order.map((id, index) => [id, index]));
  const at = (session: T) => place.get(key(session)) ?? order.length;
  return shown.map((session, index) => ({ session, index }))
    .sort((a, b) => at(a.session) - at(b.session) || a.index - b.index)
    .map(({ session }) => session);
}

/** The ids with the one at `from` moved to `to`. */
export function moveTile(ids: ReadonlyArray<string>, from: number, to: number): string[] {
  const next = [...ids];
  const [moved] = next.splice(from, 1);
  if (moved !== undefined) next.splice(to, 0, moved);
  return next;
}

/** The new order first, then saved ids of sessions not on the Wall now. */
export function mergeOrder(ids: ReadonlyArray<string>, saved: ReadonlyArray<string>): string[] {
  return [...ids, ...saved.filter((id) => !ids.includes(id))].slice(0, MAX_ORDER);
}

/**
 * The factor that fits the grid's width in the tile's screen box, so a tall
 * grid does not shrink to a narrow column (tileOffset then picks the rows to
 * show). Never enlarges; 0 until both are measured.
 */
export function tileScale(boxWidth: number, boxHeight: number, gridWidth: number, gridHeight: number): number {
  if (!(boxWidth > 0 && boxHeight > 0 && gridWidth > 0 && gridHeight > 0)) {
    return 0;
  }
  return Math.min(1, boxWidth / gridWidth);
}

/**
 * How far to move a scaled grid up, in grid pixels, so the cursor row stays in
 * the tile: 0 while everything above the cursor fits, then the newest rows.
 */
export function tileOffset(visibleHeight: number, gridHeight: number, cursorBottom: number | undefined): number {
  if (!(visibleHeight > 0) || cursorBottom === undefined || gridHeight <= visibleHeight) {
    return 0;
  }
  return Math.min(gridHeight - visibleHeight, Math.max(0, cursorBottom - visibleHeight));
}

/** The tile's spoken name, such as "claude, Waiting, open session". */
export function tileLabel(session: BrokerRPCSessionRecord, fresh: boolean): string {
  return [sessionName(session), agentBadge(session, fresh)?.label, 'open session'].filter((part) => part !== undefined).join(', ');
}

/** The mark over a tile: Paused beyond the live budget, a dot while a live tile reconnects. */
export function tileMark(tile: TileState, link: LinkState): 'paused' | 'reconnecting' | undefined {
  if (tile === 'paused') return 'paused';
  return link === 'live' ? undefined : 'reconnecting';
}

/**
 * One session's screen, scaled down. It attaches read-only and stays that
 * way: no control, no input, no PTY resize. The phone-wide scheduler decides
 * whether it is live: offscreen tiles, tiles beyond the live budget, and all
 * tiles while the app is in the background keep their last frame, show
 * Paused and close their connection. A computer with thumbnail mode sends
 * frames at most THUMBNAIL_UPDATES_PER_SECOND; an older one streams at full
 * rate and costs more of the budget.
 */
function MiniTerminal({ session }: { readonly session: HostedSession }) {
  const box = useRef<HTMLSpanElement>(null);
  const host = useRef<HTMLSpanElement>(null);
  const handle = useRef<TileHandle>(undefined);
  const [view, setView] = useState<TerminalView>();
  const [connection, setConnection] = useState<Connection>();
  const [tile, setTile] = useState<TileState>('paused');
  const [fit, setFit] = useState({ scale: 0, offset: 0 });
  const status = useSyncExternalStore(
    connection?.subscribe ?? noSubscribe,
    connection?.getStatus ?? initialStatus,
    initialStatus,
  );
  const { origin } = session.computer;
  const key = keyOf(session);

  // The view lives as long as the tile, so a paused tile keeps its last frame.
  useEffect(() => {
    const frame = box.current;
    const element = host.current;
    if (frame === null || element === null) {
      return undefined;
    }
    const next = new TerminalView(TILE_FONT);
    next.open(element);
    setView(next);
    // The grid changes size when the controller resizes the PTY, the tile when
    // the layout changes, and the cursor row with output.
    const screen = element.querySelector<HTMLElement>('.xterm-screen');
    const measure = () => {
      if (screen === null) return;
      const scale = tileScale(frame.clientWidth, frame.clientHeight, screen.offsetWidth, screen.offsetHeight);
      const offset = scale > 0 ? tileOffset(frame.clientHeight / scale, screen.offsetHeight, next.cursorBand()?.bottom) : 0;
      setFit((last) => (last.scale === scale && last.offset === offset ? last : { scale, offset }));
    };
    const observer = new ResizeObserver(measure);
    observer.observe(frame);
    if (screen !== null) observer.observe(screen);
    const written = next.terminal.onWriteParsed(measure);
    return () => {
      written.dispose();
      observer.disconnect();
      next.dispose();
      setView(undefined);
    };
  }, [key, session.generation]);

  // A live slot from the scheduler; only a visible tile keeps one.
  useLayoutEffect(() => {
    const frame = box.current;
    const slot = scheduler.requestLiveTile(key, { fullRate: !client(origin).supports('thumbnail'), onChange: setTile });
    handle.current = slot;
    setTile(slot.state());
    const observer = frame === null ? undefined : new IntersectionObserver((entries) => {
      const last = entries.at(-1);
      if (last !== undefined) slot.setVisible(last.isIntersecting);
    });
    if (frame !== null) observer?.observe(frame);
    return () => {
      observer?.disconnect();
      slot.release();
      handle.current = undefined;
    };
  }, [key, origin]);

  // Connected only while live.
  useEffect(() => {
    if (view === undefined || tile !== 'live') {
      return undefined;
    }
    const thumbnail = client(origin).supports('thumbnail');
    const next = new Connection({
      url: websocketUrl(origin),
      sessionId: session.id,
      generation: session.generation,
      display: view.renderer,
      ...(thumbnail ? { thumbnail: { maxUpdatesPerSecond: THUMBNAIL_UPDATES_PER_SECOND } } : {}),
      onMessageBytes: (bytes) => handle.current?.bytes(bytes),
      gate: scheduler.connectGate(origin),
      peer: !session.computer.home,
    });
    next.start();
    setConnection(next);
    return () => {
      next.detach();
      setConnection(undefined);
    };
  }, [view, tile, origin, session.computer.home, session.id, session.generation]);

  const mark = tileMark(tile, status.link);
  return (
    <span ref={box} className="wall-screen" aria-hidden="true">
      <span ref={host} className="wall-host" style={{ transform: `scale(${fit.scale}) translateY(${-fit.offset}px)` }} />
      {mark === 'reconnecting' && <span className="dot amber wall-link" />}
      {mark === 'paused' && <span className="wall-paused">Paused</span>}
    </span>
  );
}

interface Drag {
  readonly id: string;
  readonly pointerId: number;
  readonly startX: number;
  readonly startY: number;
  x: number;
  y: number;
  timer?: number;
  scroll?: number | undefined;
  /** Removes the window listeners that follow this pointer. */
  release?: () => void;
  /** Set when the hold ends and the tile starts to move. */
  li?: HTMLLIElement;
  /** The order when the tile started to move; an unchanged order is not saved. */
  before: ReadonlyArray<string>;
  grabX: number;
  grabY: number;
  /** The page's scroll end before the tile moved; the moving tile would stretch it. */
  maxScroll: number;
}

/**
 * The tiles. Hold one and drag it to a new place; Alt+arrow keys move the
 * focused tile. The order is saved on this device only.
 */
export function WallGrid({ shown, viewed, now, multi = false, onOpen }: {
  readonly shown: ReadonlyArray<HostedSession>;
  readonly viewed: ViewedTimes;
  readonly now: number;
  /** Name each tile's computer. */
  readonly multi?: boolean;
  readonly onOpen: (session: HostedSession) => void;
}) {
  const [order, setOrder] = useState(readWallOrder);
  // The live order while a tile moves; saved when it is dropped.
  const [dragIds, setDragIds] = useState<string[]>();
  const [dragging, setDragging] = useState<string>();
  const [announcement, setAnnouncement] = useState('');
  const grid = useRef<HTMLUListElement>(null);
  const drag = useRef<Drag>(undefined);
  const idsRef = useRef<string[]>([]);
  const droppedAt = useRef(Number.NEGATIVE_INFINITY);
  const ordered = orderTiles(shown, order, keyOf);
  const tiles = dragIds === undefined ? ordered : orderTiles(shown, dragIds, keyOf);
  const ids = tiles.map(keyOf);

  const save = (next: string[]) => {
    const merged = mergeOrder(next, order);
    setOrder(merged);
    saveWallOrder(merged);
  };

  // Keep the moving tile under the finger; its grid slot changes as it moves.
  const place = () => {
    const current = drag.current;
    const list = grid.current;
    if (current?.li === undefined || list === null) return;
    const box = list.getBoundingClientRect();
    const x = current.x - current.grabX - box.left - current.li.offsetLeft;
    const y = current.y - current.grabY - box.top - current.li.offsetTop;
    current.li.style.transform = `translate(${x}px, ${y}px)`;
  };

  // Move the tile into the slot of the tile under the finger.
  const follow = () => {
    const current = drag.current;
    if (current?.li === undefined) return;
    place();
    const target = document.elementsFromPoint(current.x, current.y)
      .map((element) => element.closest<HTMLElement>('[data-tile]'))
      .find((element) => element !== null && element.dataset.tile !== current.id);
    const live = idsRef.current;
    const from = live.indexOf(current.id);
    const to = target?.dataset.tile === undefined ? -1 : live.indexOf(target.dataset.tile);
    if (from >= 0 && to >= 0) setDragIds(moveTile(live, from, to));
  };

  const edgeScroll = () => {
    const current = drag.current;
    const page = grid.current?.closest('.page');
    if (current?.li === undefined || page === null || page === undefined) return;
    const box = page.getBoundingClientRect();
    const step = current.y < box.top + EDGE_PX ? -SCROLL_STEP_PX : current.y > box.bottom - EDGE_PX ? SCROLL_STEP_PX : 0;
    const next = Math.min(current.maxScroll, Math.max(0, page.scrollTop + step));
    if (step === 0 || next === page.scrollTop) {
      current.scroll = undefined;
      return;
    }
    page.scrollTop = next;
    follow();
    current.scroll = requestAnimationFrame(edgeScroll);
  };

  const end = () => {
    const current = drag.current;
    drag.current = undefined;
    if (current === undefined) return;
    current.release?.();
    window.clearTimeout(current.timer);
    if (current.scroll !== undefined) cancelAnimationFrame(current.scroll);
    if (current.li === undefined) return;
    current.li.style.transform = '';
    droppedAt.current = performance.now();
    setDragging(undefined);
    setDragIds(undefined);
    // A drag whose session ended saves nothing: only the others moved.
    const moved = idsRef.current.includes(current.id) && idsRef.current.join() !== current.before.join();
    if (moved) save(idsRef.current);
  };

  const move = (x: number, y: number) => {
    const current = drag.current;
    if (current === undefined) return;
    current.x = x;
    current.y = y;
    if (current.li === undefined) {
      // Moved before the hold ended: a scroll, not a drag.
      if (Math.hypot(x - current.startX, y - current.startY) > SLOP_PX) end();
      return;
    }
    follow();
    if (current.scroll === undefined) current.scroll = requestAnimationFrame(edgeScroll);
  };

  // Window listeners call the handlers of the latest render.
  const handlers = useRef({ move, end });

  const start = (event: PointerEvent<HTMLButtonElement>, id: string) => {
    if (drag.current !== undefined || (event.pointerType === 'mouse' && event.button !== 0)) return;
    const li = event.currentTarget.closest('li');
    const current: Drag = {
      id, pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, x: event.clientX, y: event.clientY,
      before: [], grabX: 0, grabY: 0, maxScroll: 0,
    };
    // The window, not the tile, follows the pointer: moving the tile in the
    // list can drop the tile's pointer capture, and a release off the tiles
    // must still end the drag.
    const onMove = (next: globalThis.PointerEvent) => {
      if (next.pointerId === current.pointerId) handlers.current.move(next.clientX, next.clientY);
    };
    const onEnd = (next: globalThis.PointerEvent) => {
      if (next.pointerId === current.pointerId) handlers.current.end();
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onEnd);
    window.addEventListener('pointercancel', onEnd);
    current.release = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onEnd);
      window.removeEventListener('pointercancel', onEnd);
    };
    current.timer = window.setTimeout(() => {
      if (drag.current !== current || li === null) return;
      const box = li.getBoundingClientRect();
      current.grabX = current.x - box.left;
      current.grabY = current.y - box.top;
      const page = li.closest('.page');
      current.maxScroll = page === null ? 0 : page.scrollHeight - page.clientHeight;
      current.before = idsRef.current;
      current.li = li;
      setDragIds(idsRef.current);
      setDragging(id);
      navigator.vibrate?.(10);
    }, HOLD_MS);
    drag.current = current;
  };

  const keyMove = (event: KeyboardEvent<HTMLButtonElement>, session: HostedSession) => {
    const step = !event.altKey ? 0
      : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1
        : event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : 0;
    if (step === 0) return;
    // Alt+arrow also means Back and Forward in the browser; never leave the Wall.
    event.preventDefault();
    const from = ids.indexOf(keyOf(session));
    const to = from + step;
    if (from < 0 || to < 0 || to >= ids.length) return;
    save(moveTile(ids, from, to));
    setAnnouncement(`${sessionName(session)} moved to place ${to + 1} of ${ids.length}`);
  };

  useLayoutEffect(() => {
    idsRef.current = ids;
    handlers.current = { move, end };
    // The moving tile's session ended: drop the drag.
    if (drag.current?.li !== undefined && !ids.includes(drag.current.id)) end();
    place();
  });

  useEffect(() => {
    const list = grid.current;
    if (list === null) return undefined;
    // A moving tile must not scroll the page under it.
    const hold = (event: TouchEvent) => {
      if (drag.current?.li !== undefined) event.preventDefault();
    };
    list.addEventListener('touchmove', hold, { passive: false });
    return () => {
      list.removeEventListener('touchmove', hold);
      const current = drag.current;
      drag.current = undefined;
      current?.release?.();
      window.clearTimeout(current?.timer);
      if (current?.scroll !== undefined) cancelAnimationFrame(current.scroll);
    };
  }, []);

  return (
    <>
      <ul ref={grid} className="wall-grid">
        {tiles.map((session) => {
          const fresh = hasNewOutput(session, viewed);
          const badge = agentBadge(session, fresh);
          const question = session.agent?.state === 'waiting' ? session.agent.question?.text : undefined;
          const key = keyOf(session);
          return (
            <li key={`${key}/${session.generation}`} data-tile={key} className={dragging === key ? 'dragging' : undefined}>
              <button
                type="button"
                className={badge?.tone === 'amber' ? 'wall-tile asking' : 'wall-tile'}
                aria-label={tileLabel(session, fresh)}
                aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight Alt+ArrowUp Alt+ArrowDown"
                onClick={() => {
                  // A drop ends with a click on the tile; it must not open it.
                  if (performance.now() - droppedAt.current > 400) onOpen(session);
                }}
                onPointerDown={(event) => start(event, key)}
                onContextMenu={(event) => {
                  if (drag.current !== undefined) event.preventDefault();
                }}
                onKeyDown={(event) => keyMove(event, session)}
              >
                <span className="wall-name">{sessionName(session)}</span>
                {multi && <span className="wall-computer"><ComputerTag computer={session.computer} /></span>}
                <MiniTerminal session={session} />
                {badge !== undefined && <StatusPill badge={badge} duration={since(session.agent?.stateSince, now)} />}
                {question !== undefined && <span className="wall-question">{question}</span>}
              </button>
            </li>
          );
        })}
      </ul>
      <p className="visually-hidden" role="status">{announcement}</p>
      {shown.length > 1 && shown.some((session) => order.includes(keyOf(session))) && (
        <button type="button" className="link wall-reset" onClick={() => {
          setOrder([]);
          saveWallOrder([]);
          setAnnouncement('Default order');
          // The button goes away; keep keyboard focus on the Wall.
          grid.current?.querySelector<HTMLButtonElement>('.wall-tile')?.focus();
        }}>
          Reset order
        </button>
      )}
    </>
  );
}

export function Wall({ sessions, multi = false, onOpen, onNew, onList }: {
  /** Every computer's sessions; undefined until one computer answers. */
  readonly sessions: ReadonlyArray<HostedSession> | undefined;
  readonly multi?: boolean;
  readonly onOpen: (session: HostedSession) => void;
  readonly onNew: () => void;
  readonly onList: () => void;
}) {
  const viewed = readViewed();
  const now = Date.now();
  const wall = wallSessions(sessions ?? []);

  return (
    <main className="page screen wall">
      <header className="screen-header">
        <div>
          <h1 className="display">Wall</h1>
          <p className="subtitle">Watch only. Tap a session to open it. Hold and drag to move it.</p>
        </div>
        <button type="button" className="round" aria-label="Session list" onClick={onList}>
          <svg width="18" height="16" viewBox="0 0 18 16" aria-hidden="true">
            <path d="M2 2.5h14M2 8h14M2 13.5h14" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
          </svg>
        </button>
      </header>

      {sessions === undefined && <p role="status" className="muted">Loading…</p>}
      {sessions !== undefined && wall.shown.length === 0 && (
        <section className="stack wall-empty">
          <p className="muted">No running sessions</p>
          <button type="button" className="primary" onClick={onNew}>
            <span aria-hidden="true" className="prompt">❯</span> new session
          </button>
        </section>
      )}
      <WallGrid shown={wall.shown} viewed={viewed} now={now} multi={multi} onOpen={onOpen} />
      {wall.ended > 0 && (
        <p className="hint-text">{wall.ended} ended {wall.ended === 1 ? 'session is' : 'sessions are'} on the list.</p>
      )}
    </main>
  );
}
