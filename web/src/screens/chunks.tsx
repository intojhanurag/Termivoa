import { Component, type ReactNode } from 'react';

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
    <main className="page">
      <section role="alert" className="stack">
        <h1>A new version is available</h1>
        <p>
          This screen could not load, usually because your computer was updated. Reload to continue.
          Your sessions keep running on your computer.
        </p>
        <p className="muted">If this phone is offline, reload when the connection returns.</p>
        <button type="button" className="primary" onClick={onReload}>Reload</button>
      </section>
    </main>
  );
}

interface BoundaryState {
  readonly error: unknown;
}

/** Shows UpdateRequired for chunk load failures; any other render error propagates. */
export class ChunkErrorBoundary extends Component<{ readonly children: ReactNode }, BoundaryState> {
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
    throw error;
  }
}
