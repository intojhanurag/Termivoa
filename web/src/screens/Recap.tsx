/** The recap goes away on its own after this long. */
export const RECAP_MS = 12_000;

/**
 * Calls onDismiss once: after RECAP_MS, or on the first pointer down that
 * `inside` does not claim. Returns the cleanup.
 */
export function autoDismiss(target: EventTarget, inside: (node: EventTarget | null) => boolean, onDismiss: () => void): () => void {
  const timer = setTimeout(onDismiss, RECAP_MS);
  const onDown = (event: Event) => {
    if (!inside(event.target)) onDismiss();
  };
  // Capture, so a tap that opens something else still ends the recap first.
  target.addEventListener('pointerdown', onDown, { capture: true });
  return () => {
    clearTimeout(timer);
    target.removeEventListener('pointerdown', onDown, { capture: true });
  };
}

/** "While you were away": one line of facts, See changes and ✕. */
export function RecapCard({ line, onSeeChanges, onDismiss }: {
  readonly line: string;
  readonly onSeeChanges: () => void;
  readonly onDismiss: () => void;
}) {
  return (
    <section className="recap-card" aria-label="While you were away">
      <p className="recap-line" role="status">{line}</p>
      <div className="recap-actions">
        <button type="button" className="link strong" onClick={onSeeChanges}>See changes</button>
        <button type="button" className="link recap-close" aria-label="Dismiss" onClick={onDismiss}>✕</button>
      </div>
    </section>
  );
}
