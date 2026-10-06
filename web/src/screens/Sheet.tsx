import { useEffect, useRef, type ReactNode } from 'react';

/** Bottom sheet over the dimmed terminal. Escape closes it; focus moves into it on open. */
export function Sheet({ labelledBy, onClose, children }: {
  readonly labelledBy: string;
  readonly onClose: () => void;
  readonly children: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  useEffect(() => { ref.current?.focus(); }, []);
  return (
    <section
      ref={ref}
      className="sheet"
      role="dialog"
      aria-labelledby={labelledBy}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <div className="grabber" aria-hidden="true" />
      {children}
    </section>
  );
}

export function SheetHeader({ id, glyph, caps, title, children }: {
  readonly id: string;
  readonly glyph?: string;
  /** One small line over the title, for example "GitHub · on mac" (shown in capitals). */
  readonly caps?: ReactNode;
  readonly title: ReactNode;
  readonly children?: ReactNode;
}) {
  const heading = (
    <h2 id={id}>
      {glyph !== undefined && <span className="sheet-glyph" aria-hidden="true">{glyph} </span>}
      {title}
    </h2>
  );
  return (
    <div className={caps === undefined ? 'sheet-header' : 'sheet-header create-head'}>
      {caps === undefined ? heading : <div className="create-title"><p className="caps">{caps}</p>{heading}</div>}
      <div className="row">{children}</div>
    </div>
  );
}
