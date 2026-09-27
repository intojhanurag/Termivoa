import { useState } from 'react';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { hasNewOutput, readViewed } from './activity';
import type { SessionList } from './hooks';
import { NewSessionHelp } from './NewSession';

type Tone = 'accent' | 'amber' | 'muted';

/** Who controls the session, as the list shows it; undefined when unknown. */
export function controllerText(session: BrokerRPCSessionRecord): { readonly text: string; readonly tone: Tone } | undefined {
  switch (session.state) {
    case 'exited':
      return { text: session.exitCode === undefined ? 'Exited' : `Exited · ${session.exitCode}`, tone: 'muted' };
    case 'lost': return { text: 'Lost', tone: 'muted' };
    case 'exiting': return { text: 'Ending', tone: 'muted' };
    case 'starting': return { text: 'Starting', tone: 'muted' };
    default:
  }
  if (session.controllerIsYou === true) return { text: 'You', tone: 'accent' };
  if (session.controlled === true) return { text: 'Other device', tone: 'amber' };
  if (session.controlled === false) return { text: 'No one', tone: 'muted' };
  return undefined;
}

/** Short age such as "35 min", "2 h", "yesterday" or "3 d". */
export function age(iso: string | undefined, now: number): string {
  const then = Date.parse(iso ?? '');
  if (Number.isNaN(then)) return '';
  const minutes = Math.max(0, Math.floor((now - then) / 60_000));
  if (minutes < 1) return 'now';
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h`;
  return hours < 48 ? 'yesterday' : `${Math.floor(hours / 24)} d`;
}

const ended = (session: BrokerRPCSessionRecord) => session.state === 'exited' || session.state === 'lost';

function PromptGlyph() {
  return (
    <svg width="18" height="16" viewBox="0 0 18 16" aria-hidden="true">
      <path d="M2 3l5 5-5 5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M9.5 13H16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

/** One session. Title and preview are terminal-controlled text: rendered as text only. */
export function SessionRow({ session, project, shell, fresh, now, onOpen }: {
  readonly session: BrokerRPCSessionRecord;
  readonly project: string;
  readonly shell: string;
  readonly fresh: boolean;
  readonly now: number;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
}) {
  const done = ended(session);
  const controller = controllerText(session);
  const you = session.controllerIsYou === true && !done;
  const when = age(done ? session.endedAt ?? session.updatedAt : session.startedAt ?? session.createdAt, now);
  return (
    <li>
      <button
        type="button"
        className={`session-row${you ? ' you' : ''}${done ? ' ended' : ''}`}
        disabled={done}
        onClick={() => onOpen(session)}
      >
        <span className={done ? 'tile dashed' : you ? 'tile accent' : 'tile'} aria-hidden="true">
          {done ? <span className="stop-glyph" /> : <PromptGlyph />}
        </span>
        <span className="row-text">
          <span className="row-head">
            <span className="row-title">
              {session.title}
              {fresh && <><span className="dot accent" aria-hidden="true" /><span className="visually-hidden">, new output</span></>}
            </span>
            {controller !== undefined && <span className={`controller ${controller.tone}`}>{controller.text}</span>}
          </span>
          <span className="row-meta">{[project, shell, when].filter((part) => part !== '').join(' · ')}</span>
          {session.preview !== undefined && session.preview !== '' && !done && (
            <span className="preview">{session.preview}</span>
          )}
        </span>
      </button>
    </li>
  );
}

export function Sessions({ settings, list, message, onOpen, onNew, onSettings, onRefreshSettings, onFailure, onUpdate }: {
  readonly settings: HTTPSettingsResponse;
  readonly list: SessionList;
  readonly message?: string;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onNew: () => void;
  readonly onSettings: () => void;
  readonly onRefreshSettings: () => Promise<void>;
  readonly onFailure: (error: unknown) => string;
  /** Present when a new version waits; tapping it reloads into that version. */
  readonly onUpdate?: () => void;
}) {
  const [error, setError] = useState<string | undefined>(message);
  const [busy, setBusy] = useState(false);
  const canCreate = settings.projects.length > 0 && settings.shellProfiles.length > 0;
  const projectName = new Map(settings.projects.map((item) => [item.id, item.name]));
  const shellName = new Map(settings.shellProfiles.map((item) => [item.id, item.name]));
  const viewed = readViewed();
  const now = Date.now();
  const { sessions, reachable } = list;

  async function refresh() {
    setBusy(true);
    setError(undefined);
    try {
      await onRefreshSettings();
      await list.reload();
    } catch (failure) {
      setError(onFailure(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="page screen">
      <header className="screen-header sessions-header">
        <div>
          <h1 className="display">Sessions</h1>
          <p className="host-line" role="status">
            <span className={reachable ? 'dot green' : 'dot amber'} aria-hidden="true" />
            {reachable ? 'Your computer · connected' : 'Your computer · not reachable, retrying'}
          </p>
        </div>
        <div className="header-actions">
          {onUpdate !== undefined && (
            <button type="button" className="update-pill" aria-label="Update to the new version" onClick={onUpdate}>
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
                <path d="M12 7a5 5 0 1 1-1.5-3.6M12 1.5v2.9H9.1" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
              update
            </button>
          )}
          <button type="button" className="round" aria-label="Settings" onClick={onSettings}>
            <svg width="20" height="18" viewBox="0 0 20 18" aria-hidden="true">
              <path d="M2 4h9M15 4h3M2 14h3M9 14h9" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
              <circle cx="13" cy="4" r="2.2" fill="none" stroke="currentColor" strokeWidth="1.7" />
              <circle cx="7" cy="14" r="2.2" fill="none" stroke="currentColor" strokeWidth="1.7" />
            </svg>
          </button>
        </div>
      </header>
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      {!canCreate && (
        <section className="card stack" aria-label="Before your first session">
          <NewSessionHelp settings={settings} />
          <button type="button" disabled={busy} onClick={() => { void refresh(); }}>Check again</button>
        </section>
      )}

      <section aria-label="Your sessions" className="list-section">
        {sessions === undefined && <p role="status" className="muted">Loading…</p>}
        {sessions?.length === 0 && <p className="muted empty">No sessions yet.</p>}
        <ul className="rows">
          {sessions?.map((session) => (
            <SessionRow
              key={`${session.id}/${session.generation}`}
              session={session}
              project={projectName.get(session.projectId) ?? 'Unknown project'}
              shell={shellName.get(session.shellProfileId) ?? 'Unknown shell'}
              fresh={!ended(session) && hasNewOutput(session, viewed)}
              now={now}
              onOpen={onOpen}
            />
          ))}
        </ul>
      </section>

      {canCreate && (
        <div className="bottom-bar">
          <button type="button" className="primary wide" onClick={onNew}>
            <span aria-hidden="true" className="prompt">❯</span> new session
          </button>
        </div>
      )}
    </main>
  );
}
