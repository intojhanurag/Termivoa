import { useState } from 'react';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { agentBadge, hasNewOutput, readViewed } from './activity';
import type { HostedSession } from './allSessions';
import { previewSrc } from './PreviewChip';

/** Top bar: back to the terminal, the previewed address, reload. */
export function PreviewBar({ port, onBack, onReload }: {
  readonly port: number;
  readonly onBack: () => void;
  readonly onReload: () => void;
}) {
  return (
    <header className="preview-bar">
      <button type="button" className="link preview-back" onClick={onBack}>‹ terminal</button>
      <span className="preview-address"><span className="dot green" aria-hidden="true" />localhost:{port}</span>
      <button type="button" className="link preview-reload" aria-label="Reload preview" onClick={onReload}>↻</button>
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
 * A running dev server of this session, framed from the separate preview
 * origin. The frame gets scripts and forms but no top navigation, popups, or
 * device features, and sends no referrer.
 */
export function Preview({ session, sessions, origin, port, onBack, onTell }: {
  readonly session: HostedSession;
  readonly sessions: ReadonlyArray<HostedSession> | undefined;
  readonly origin: string;
  readonly port: number;
  readonly onBack: () => void;
  readonly onTell: () => void;
}) {
  const [loads, setLoads] = useState(0);
  const listed = sessions?.find((item) => item.id === session.id && item.generation === session.generation);
  const current = listed ?? session;
  const present = current.previews?.some((item) => item.port === port) === true;
  // Only a port seen listening and then gone counts as stopped, so a list
  // polled just before the server started does not flash the notice.
  const [seen, setSeen] = useState(present);
  if (present && !seen) setSeen(true);
  return (
    <main className="preview-screen">
      <PreviewBar port={port} onBack={onBack} onReload={() => setLoads((value) => value + 1)} />
      {seen && !present && <div className="banner" role="alert"><span>Dev server stopped</span></div>}
      <iframe
        key={loads}
        className="preview-frame"
        title={`Preview of localhost:${port}`}
        src={previewSrc(origin, session.id, port)}
        sandbox="allow-scripts allow-forms allow-same-origin"
        referrerPolicy="no-referrer"
      />
      <PreviewStatus session={current} fresh={hasNewOutput(current, readViewed())} onTell={onTell} />
    </main>
  );
}
