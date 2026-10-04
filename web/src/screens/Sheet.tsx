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

export function SheetHeader({ id, glyph, title, children }: {
  readonly id: string;
  readonly glyph?: string;
  readonly title: string;
  readonly children?: ReactNode;
}) {
  return (
    <div className="sheet-header">
      <h2 id={id}>
        {glyph !== undefined && <span className="sheet-glyph" aria-hidden="true">{glyph} </span>}
        {title}
      </h2>
      <div className="row">{children}</div>
    </div>
  );
}
