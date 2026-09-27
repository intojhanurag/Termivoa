import { useVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { Connection } from '../terminal/Connection';
import { lineCount } from '../terminal/composer';

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
  const [pausedAt] = useState(() => new Date().toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }));
  const [selected, setSelected] = useState('');
  const [copied, setCopied] = useState<'copied' | 'failed'>();
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

  // Track the user's own selection inside the history to label Copy.
  useEffect(() => {
    const onSelection = () => {
      const selection = document.getSelection();
      const inside = selection !== null && !selection.isCollapsed
        && scroller.current?.contains(selection.anchorNode) === true;
      setSelected(inside ? selection.toString() : '');
      setCopied(undefined);
    };
    document.addEventListener('selectionchange', onSelection);
    return () => document.removeEventListener('selectionchange', onSelection);
  }, []);

  const selectedLines = selected === '' ? 0 : lineCount(selected.replace(/\r?\n$/u, ''));

  async function copy() {
    try {
      await navigator.clipboard.writeText(selected);
      setCopied('copied');
    } catch {
      setCopied('failed');
    }
  }

  return (
    <section className="history" aria-labelledby="history-title">
      <header className="history-bar">
        <div>
          <h2 id="history-title">History</h2>
          <p className="subtitle" role="status">
            {view === undefined || view.state === 'loading'
              ? 'Loading history…'
              : view.state === 'unavailable'
                ? 'History unavailable right now. Return to live and try again.'
                : `Paused at ${pausedAt} · ${lines.length.toLocaleString()} ${lines.length === 1 ? 'line' : 'lines'}`}
          </p>
        </div>
        <button type="button" className="link strong" onClick={onReturn}>Done</button>
      </header>
      <div
        ref={scroller}
        className="history-scroll"
        tabIndex={0}
        aria-label="Terminal history, selectable text"
        style={{ fontSize, lineHeight: `${lineHeight}px` }}
      >
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((item) => {
            const old = view !== undefined && item.index < view.historyRows;
            const start = item.index === view?.historyRows && item.index > 0;
            return (
              <div
                key={item.key}
                className={`history-line${old ? ' old' : ''}${start ? ' screen-start' : ''}`}
                style={{ transform: `translateY(${item.start}px)`, height: lineHeight }}
              >
                {lines[item.index] || ' '}
              </div>
            );
          })}
        </div>
      </div>
      <div className="history-actions">
        {view?.newOutput === true && (
          <button type="button" className="pill" onClick={onReturn}>
            <svg width="12" height="14" viewBox="0 0 12 14" aria-hidden="true">
              <path d="M6 1v11m0 0L1.5 7.5M6 12l4.5-4.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            New output · Return to live
          </button>
        )}
        {copied !== undefined && (
          <p role="status" className={copied === 'copied' ? 'muted' : 'error'}>
            {copied === 'copied' ? 'Copied.' : 'Copy did not work. Use your phone’s Copy action on the selection.'}
          </p>
        )}
        <button type="button" className="primary wide" disabled={selected === ''} onClick={() => { void copy(); }}>
          <svg width="16" height="18" viewBox="0 0 16 18" aria-hidden="true">
            <rect x="4.5" y="4.5" width="10" height="12" rx="2" fill="none" stroke="currentColor" strokeWidth="1.8" />
            <path d="M11 2.5V2a1 1 0 0 0-1-1H2.5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1H3" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
          </svg>
          {selected === '' ? 'Select text to copy' : `Copy ${selectedLines} ${selectedLines === 1 ? 'line' : 'lines'}`}
        </button>
      </div>
    </section>
  );
}
