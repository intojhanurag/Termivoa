import { useVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useRef, useSyncExternalStore } from 'react';
import type { Connection } from '../terminal/Connection';

/**
 * A frozen, selectable snapshot of normal history plus the current screen.
 * Rows never change after loading, so a selection is never disturbed.
 */
export function History({ connection, fontSize, onReturn }: {
  readonly connection: Connection;
  readonly fontSize: number;
  readonly onReturn: () => void;
}) {
  const view = useSyncExternalStore(connection.subscribe, connection.getHistory);
  const scroller = useRef<HTMLDivElement>(null);
  const lines = view?.state === 'ready' ? view.lines : [];
  const lineHeight = Math.ceil(fontSize * 1.35);
  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => lineHeight,
    overscan: 30,
  });

  const ready = view?.state === 'ready';
  useEffect(() => {
    if (ready && lines.length > 0) {
      virtualizer.scrollToIndex(lines.length - 1, { align: 'end' });
    }
    // Scroll to the newest line once, when the frozen snapshot arrives.
  }, [ready]);

  return (
    <section className="history" aria-labelledby="history-title">
      <div className="history-bar">
        <h2 id="history-title" className="visually-hidden">History and selection</h2>
        <span role="status" className={view?.newOutput === true ? 'accent' : 'muted'}>
          {view === undefined || view.state === 'loading'
            ? 'Loading history…'
            : view.state === 'unavailable'
              ? 'History unavailable right now. Return to live and try again.'
              : view.newOutput ? 'New output available' : 'Frozen snapshot — select text to copy'}
        </span>
        <button type="button" className="primary" onClick={onReturn}>Return to live</button>
      </div>
      <div
        ref={scroller}
        className="history-scroll"
        tabIndex={0}
        aria-label="Terminal history, selectable text"
        style={{ fontSize, lineHeight: `${lineHeight}px` }}
      >
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((item) => (
            <div
              key={item.key}
              className={item.index === view?.historyRows && item.index > 0 ? 'history-line screen-start' : 'history-line'}
              style={{ transform: `translateY(${item.start}px)`, height: lineHeight }}
            >
              {lines[item.index] || ' '}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
