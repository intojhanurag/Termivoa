import { useEffect, useState } from 'react';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { agentBadge, hasNewOutput, readViewed } from './activity';
import type { HostedSession } from './allSessions';
import { previewSrc } from './PreviewChip';
import { ComputerTag } from './Sessions';

/** What the screen says when nothing answers on the preview origin. Names no cause: a network error, a timeout and a blocked request look the same to the app. */
export const cantPreview = (label: string): string =>
  `Can't open the preview on ${label}. If ${label} does not share dev-server previews yet, run pterm setup on ${label} and answer yes to the preview question.`;

/**
 * Whether anything answers on the preview origin, asked before the frame
 * shows (the app cannot see what a frame is answered). Any HTTP answer counts,
 * also a refusal; the answer itself is opaque and never read.
 */
export async function probePreview(origin: string, fetcher: typeof fetch = fetch): Promise<boolean> {
  try {
    await fetcher(`${origin}/__termivoa/select`, { mode: 'no-cors', credentials: 'omit', signal: AbortSignal.timeout(5000) });
    return true;
  } catch {
    return false;
  }
}

/**
 * Top bar: back to the terminal, the previewed address, reload. Over a peer's
 * page it always names the computer and marks the page as untrusted: the peer
 * chooses what the frame shows, and the page cannot cover or change this bar.
 */
export function PreviewBar({ port, peer, onBack, onReload }: {
  readonly port: number;
  readonly peer: Pick<Computer, 'label' | 'color'> | undefined;
  readonly onBack: () => void;
  readonly onReload: () => void;
}) {
  return (
    <header className="preview-bar">
      <button type="button" className="link preview-back" onClick={onBack}>‹ terminal</button>
      <span className="preview-address"><span className="dot green" aria-hidden="true" />localhost:{port}</span>
      <button type="button" className="link preview-reload" aria-label="Reload preview" onClick={onReload}>↻</button>
      {peer !== undefined && <p className="preview-peer"><ComputerTag computer={peer} /> · <strong>untrusted page</strong></p>}
    </header>
  );
}

/** Floating pill with the agent's status from the polled record, and a way back to tell it something. */
export function PreviewStatus({ session, fresh, onTell }: {
  readonly session: BrokerRPCSessionRecord;
  readonly fresh: boolean;
  readonly onTell: () => void;
}) {
  const badge = agentBadge(session, fresh);
  const word = badge === undefined ? undefined : badge.tone === 'amber' ? 'waiting for you' : badge.label.toLowerCase();
  return (
    <div className="preview-status">
      <span className="preview-agent" role="status">
        {badge?.tone === 'accent' ? <span className="spinner" aria-hidden="true" />
          : <span className={`dot ${badge?.tone ?? 'muted'}`} aria-hidden="true" />}
        <span className="preview-agent-text">{session.title}{word !== undefined && ` · ${word}`}</span>
      </span>
      <button type="button" className="preview-tell" onClick={onTell}>Tell agent</button>
    </div>
  );
}

/**
 * The frame, or the text in its place. The frame exists only with an origin
 * that answered the probe and without a notice. It gets scripts and forms but
 * no top navigation, popups, full screen or device features. Its referrer is
 * the app's origin alone, which the preview listener needs to admit the
 * select request.
 */
export function PreviewBody({ session, origin, port, notice, reach }: {
  readonly session: HostedSession;
  readonly origin: string | undefined;
  readonly port: number;
  readonly notice: string | undefined;
  /** The probe's answer; undefined while it runs. */
  readonly reach: boolean | undefined;
}) {
  const { computer } = session;
  if (notice !== undefined) return <div className="banner" role="alert"><span>{notice}</span></div>;
  if (origin === undefined || reach === undefined) return <p role="status" className="muted preview-note">Opening preview…</p>;
  if (!reach) return <div className="banner" role="alert"><span>{cantPreview(computer.label)}</span></div>;
  return (
    <iframe
      className="preview-frame"
      title={`Preview of localhost:${port}${computer.home ? '' : ` on ${computer.label}`}`}
      src={previewSrc(origin, session.id, port)}
      sandbox="allow-scripts allow-forms allow-same-origin"
      referrerPolicy="origin"
    />
  );
}

/**
 * A running dev server of this session, framed from the separate preview
 * origin of the session's computer. `notice` (that computer refuses the phone,
 * cannot be reached or must update) replaces the frame.
 */
export function Preview({ session, sessions, origin, notice, port, onBack, onTell }: {
  readonly session: HostedSession;
  readonly sessions: ReadonlyArray<HostedSession> | undefined;
  readonly origin?: string | undefined;
  readonly notice?: string;
  readonly port: number;
  readonly onBack: () => void;
  readonly onTell: () => void;
}) {
  // Each new value mounts a new frame, so the select request runs again.
  const [loads, setLoads] = useState(0);
  const [reach, setReach] = useState<boolean>();
  useEffect(() => {
    if (origin === undefined || notice !== undefined) return undefined;
    let current = true;
    setReach(undefined);
    void probePreview(origin).then((answered) => { if (current) setReach(answered); });
    return () => { current = false; };
  }, [origin, notice, loads]);
  // A page shown again from history (WebKit) loads the frame's last address
  // without a select request, and the preview listener refuses that.
  useEffect(() => {
    const onShow = (event: PageTransitionEvent) => { if (event.persisted) setLoads((value) => value + 1); };
    window.addEventListener('pageshow', onShow);
    return () => window.removeEventListener('pageshow', onShow);
  }, []);
  const listed = sessions?.find((item) => item.id === session.id && item.generation === session.generation);
  const current = listed ?? session;
  const present = current.previews?.some((item) => item.port === port) === true;
  // Only a port seen listening and then gone counts as stopped, so a list
  // polled just before the server started does not flash the notice.
  const [seen, setSeen] = useState(present);
  if (present && !seen) setSeen(true);
  return (
    <main className="preview-screen">
      <PreviewBar port={port} peer={session.computer.home ? undefined : session.computer} onBack={onBack} onReload={() => setLoads((value) => value + 1)} />
      {seen && !present && <div className="banner" role="alert"><span>Dev server stopped</span></div>}
      <PreviewBody key={loads} session={session} origin={origin} port={port} notice={notice} reach={reach} />
      <PreviewStatus session={current} fresh={hasNewOutput(current, readViewed())} onTell={onTell} />
    </main>
  );
}
