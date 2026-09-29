import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { Connection, INITIAL_STATUS } from '../terminal/Connection';
import { TerminalView } from '../terminal/TerminalView';
import { agentBadge, hasNewOutput, readViewed, since } from './activity';
import { sessionName } from './folder';
import { websocketUrl } from './hooks';
import { StatusPill } from './Sessions';

/** Tiles draw at this font size, then shrink to fit the tile. */
export const TILE_FONT = 12;

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
export function wallSessions(sessions: ReadonlyArray<BrokerRPCSessionRecord>): { shown: BrokerRPCSessionRecord[]; ended: number } {
  const shown = sessions.filter((session) => !ended(session)).sort((a, b) => rank(a) - rank(b));
  return { shown, ended: sessions.length - shown.length };
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

/**
 * One session's screen, scaled down. It attaches read-only and stays that way:
 * no control, no input, no PTY resize. It detaches when the tile goes away.
 */
function MiniTerminal({ session }: { readonly session: BrokerRPCSessionRecord }) {
  const box = useRef<HTMLSpanElement>(null);
  const host = useRef<HTMLSpanElement>(null);
  const [connection, setConnection] = useState<Connection>();
  const [fit, setFit] = useState({ scale: 0, offset: 0 });
  const status = useSyncExternalStore(
    connection?.subscribe ?? noSubscribe,
    connection?.getStatus ?? initialStatus,
    initialStatus,
  );

  useEffect(() => {
    const frame = box.current;
    const element = host.current;
    if (frame === null || element === null) {
      return undefined;
    }
    const view = new TerminalView(TILE_FONT);
    view.open(element);
    const next = new Connection({
      url: websocketUrl(),
      sessionId: session.id,
      generation: session.generation,
      display: view.renderer,
    });
    next.start();
    setConnection(next);
    // The grid changes size when the controller resizes the PTY, the tile when
    // the layout changes, and the cursor row with output.
    const screen = element.querySelector<HTMLElement>('.xterm-screen');
    const measure = () => {
      if (screen === null) return;
      const scale = tileScale(frame.clientWidth, frame.clientHeight, screen.offsetWidth, screen.offsetHeight);
      const offset = scale > 0 ? tileOffset(frame.clientHeight / scale, screen.offsetHeight, view.cursorBand()?.bottom) : 0;
      setFit((last) => (last.scale === scale && last.offset === offset ? last : { scale, offset }));
    };
    const observer = new ResizeObserver(measure);
    observer.observe(frame);
    if (screen !== null) observer.observe(screen);
    const written = view.terminal.onWriteParsed(measure);
    return () => {
      written.dispose();
      observer.disconnect();
      next.detach();
      view.dispose();
      setConnection(undefined);
    };
  }, [session.id, session.generation]);

  return (
    <span ref={box} className="wall-screen" aria-hidden="true">
      <span ref={host} className="wall-host" style={{ transform: `scale(${fit.scale}) translateY(${-fit.offset}px)` }} />
      {status.link !== 'live' && <span className="dot amber wall-link" />}
    </span>
  );
}

export function Wall({ sessions, onOpen, onNew, onList }: {
  readonly sessions: ReadonlyArray<BrokerRPCSessionRecord> | undefined;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
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
          <p className="subtitle">Watch only. Tap a session to open it.</p>
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
      <ul className="wall-grid">
        {wall.shown.map((session) => {
          const fresh = hasNewOutput(session, viewed);
          const badge = agentBadge(session, fresh);
          const question = session.agent?.state === 'waiting' ? session.agent.question?.text : undefined;
          return (
            <li key={`${session.id}/${session.generation}`}>
              <button
                type="button"
                className={badge?.tone === 'amber' ? 'wall-tile asking' : 'wall-tile'}
                aria-label={tileLabel(session, fresh)}
                onClick={() => onOpen(session)}
              >
                <span className="wall-name">{sessionName(session)}</span>
                <MiniTerminal session={session} />
                {badge !== undefined && <StatusPill badge={badge} duration={since(session.agent?.stateSince, now)} />}
                {question !== undefined && <span className="wall-question">{question}</span>}
              </button>
            </li>
          );
        })}
      </ul>
      {wall.ended > 0 && (
        <p className="hint-text">{wall.ended} ended {wall.ended === 1 ? 'session is' : 'sessions are'} on the list.</p>
      )}
    </main>
  );
}
