import type { PreviewPort } from '../generated/protocol';

/** A TCP port the phone may preview: an integer 1–65535, nothing else. */
export function isPreviewPort(port: unknown): port is number {
  return Number.isInteger(port) && (port as number) >= 1 && (port as number) <= 65535;
}

/** The configured preview origin, or undefined unless it is a plain http(s) origin. */
export function previewOrigin(raw: string | undefined): string | undefined {
  if (raw === undefined || !/^https?:\/\//u.test(raw)) return undefined;
  try {
    return new URL(raw).origin;
  } catch {
    return undefined;
  }
}

/**
 * The iframe address for one port of one session. Built only from the
 * configured origin, the session id, and the protocol's port number, never
 * from terminal text.
 */
export function previewSrc(origin: string, sessionId: string, port: number): string {
  return `${origin}/__termivoa/select?session=${encodeURIComponent(sessionId)}&port=${port}`;
}

/** The most recently seen valid port and how many others there are. */
export function latestPreview(previews: ReadonlyArray<PreviewPort> | undefined): { readonly port: number; readonly more: number } | undefined {
  const valid = (previews ?? []).filter((item) => isPreviewPort(item.port));
  const latest = valid.reduce<PreviewPort | undefined>(
    (best, item) => (best === undefined || Date.parse(item.since) > Date.parse(best.since) ? item : best),
    undefined,
  );
  return latest === undefined ? undefined : { port: latest.port, more: valid.length - 1 };
}

/**
 * "Preview ready" chip just above the dock. Renders nothing without a preview
 * origin (onOpen undefined) or without a listening port.
 */
export function PreviewChip({ previews, onOpen }: {
  readonly previews: ReadonlyArray<PreviewPort> | undefined;
  readonly onOpen: ((port: number) => void) | undefined;
}) {
  const latest = latestPreview(previews);
  if (latest === undefined || onOpen === undefined) return null;
  return (
    <div className="preview-anchor">
      <div className="preview-chip" role="status">
        <span className="dot green" aria-hidden="true" />
        <span className="preview-ready">Preview ready</span>
        <span className="preview-port">:{latest.port}</span>
        {latest.more > 0 && <span className="preview-more">+{latest.more}</span>}
        <button type="button" className="preview-open" aria-label={`Open preview of port ${latest.port}`} onClick={() => onOpen(latest.port)}>
          Open ↗
        </button>
      </div>
    </div>
  );
}
