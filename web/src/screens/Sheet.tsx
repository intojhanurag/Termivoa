import { useEffect, useRef, useState, type ReactNode } from 'react';

/** Bottom sheet: focus enters on open and returns to its opener on unmount. */
export function Sheet({ labelledBy, onClose, children }: {
  readonly labelledBy: string;
  readonly onClose: () => void;
  readonly children: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  // Capture before child focus/autoFocus runs during commit. Each nested
  // sheet remembers its own opener, and re-renders do not replace it.
  const [opener] = useState(() => globalThis.document?.activeElement);
  useEffect(() => {
    const sheet = ref.current;
    sheet?.focus();
    return () => {
      if (!sheet || !(opener instanceof HTMLElement) || !opener.isConnected) return;
      const active = sheet.ownerDocument.activeElement;
      // Removal puts focus on body. Do not steal focus when navigation or
      // another control already deliberately moved it outside this sheet.
      if (active === sheet.ownerDocument.body || sheet.contains(active)) {
        opener.focus({ preventScroll: true });
      }
    };
  }, [opener]);
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
