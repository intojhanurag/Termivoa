import { Component, useState, type ReactNode } from 'react';
import { StateScreen, WindowArt } from './StateScreen';

/** A lazily loaded screen's code could not be fetched (usually after a host upgrade). */
export class ChunkLoadError extends Error {
  constructor(cause: unknown) {
    super('Screen code could not be loaded', { cause });
    this.name = 'ChunkLoadError';
  }
}

/** Wraps a dynamic import so only its load failures become a ChunkLoadError. */
export function loadChunk<T>(load: () => Promise<T>): Promise<T> {
  return load().catch((cause: unknown) => {
    throw new ChunkLoadError(cause);
  });
}

/**
 * Shown instead of a blank screen when an old page asks for code that the
 * host no longer serves. Reload fetches the current page from the network;
 * it does not force a waiting service worker to activate.
 */
export function UpdateRequired({ onReload }: { readonly onReload: () => void }) {
  return (
    <StateScreen
      tone="accent"
      label="Termivoa · update"
      art={<WindowArt mark="update" />}
      title={<>A new version<br />is ready</>}
      lead="Reload to load it. Your sessions keep running."
      actions={<button type="button" className="primary" onClick={onReload}>Reload</button>}
    />
  );
}

interface BoundaryState {
  readonly error: unknown;
}

/**
 * Shows UpdateRequired for chunk load failures. Any other render error goes
 * to `fallback`, or propagates without one. The app's outer boundary passes
 * an ErrorScreen fallback: the last line of defense, never a blank page.
 */
export class ErrorBoundary extends Component<{
  readonly children: ReactNode;
  readonly fallback?: (error: unknown) => ReactNode;
}, BoundaryState> {
  override state: BoundaryState = { error: undefined };

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return { error };
  }

  override render() {
    const { error } = this.state;
    if (error === undefined) {
      return this.props.children;
    }
    if (error instanceof ChunkLoadError) {
      return <UpdateRequired onReload={() => window.location.reload()} />;
    }
    if (this.props.fallback === undefined) throw error;
    return this.props.fallback(error);
  }
}

/** The text shown for an error nothing else handled. */
export function errorText(error: unknown): string {
  return error instanceof Error ? `${error.name}: ${error.message}` : String(error);
}

/** A render error: what broke, a way to copy it, and a reload. */
export function ErrorScreen({ text }: { readonly text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <StateScreen
      tone="red"
      label="Termivoa · error"
      art={<WindowArt mark="crash" />}
      title={<>This screen<br />broke</>}
      lead="Your sessions are safe. They keep running."
      actions={(
        <>
          <button
            type="button"
            className="state-pill"
            onClick={() => {
              void navigator.clipboard?.writeText(text).then(() => setCopied(true), () => undefined);
            }}
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <rect x="5" y="5" width="9" height="9" rx="2" stroke="currentColor" strokeWidth="1.6" />
              <path d="M11 3.5V3a1.5 1.5 0 0 0-1.5-1.5h-6A1.5 1.5 0 0 0 2 3v6a1.5 1.5 0 0 0 1.5 1.5H4" stroke="currentColor" strokeWidth="1.6" />
            </svg>
            {copied ? 'copied' : 'copy error'}
          </button>
          <button type="button" className="primary" onClick={() => window.location.reload()}>Reload</button>
        </>
      )}
    >
      <pre className="error-detail">{text}</pre>
    </StateScreen>
  );
}
