import { useCallback, useEffect, useState } from 'react';
import { api } from '../api/http';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';

type Pending =
  | { readonly kind: 'rename'; readonly id: string; readonly title: string }
  | { readonly kind: 'terminate'; readonly id: string };

const SAFE_TITLE = /^[^\p{Cc}\s](?:[^\p{Cc}]{0,126}[^\p{Cc}\s])?$/u;

function lifecycleLabel(session: BrokerRPCSessionRecord): string {
  switch (session.state) {
    case 'starting': return 'Starting';
    case 'running': return 'Shell running';
    case 'exiting': return 'Ending';
    case 'exited': return session.exitCode === undefined ? 'Shell exited' : `Shell exited (code ${session.exitCode})`;
    case 'lost': return 'Lost';
  }
}

function controlLabel(session: BrokerRPCSessionRecord): string {
  if (session.controllerIsYou === true) return 'Controlled by this device';
  return session.controlled === true ? 'Controlled by another device' : 'No controller';
}

function viewerLabel(count: number): string {
  if (count === 0) return 'No viewers';
  return count === 1 ? '1 viewer' : `${count} viewers`;
}

/** Live presence facts; absent when the broker reports no live presence. */
export function PresenceFacts({ session }: { readonly session: BrokerRPCSessionRecord }) {
  if (session.controlled === undefined || session.viewerCount === undefined) return null;
  return (
    <>
      <dt>Control</dt><dd>{controlLabel(session)}</dd>
      <dt>Viewers</dt><dd>{viewerLabel(session.viewerCount)}</dd>
    </>
  );
}

/** What to do when there is nothing to start a session with, or undefined when ready. */
export function NewSessionHelp({ settings }: { readonly settings: HTTPSettingsResponse }) {
  if (settings.projects.length === 0) {
    return (
      <div className="stack">
        <p>Add a project on your computer first:</p>
        <pre className="command"><code>pterm project add &lt;folder&gt;</code></pre>
        <p className="muted">Then tap Check again. A project is a folder where new sessions start.</p>
      </div>
    );
  }
  if (settings.shellProfiles.length === 0) {
    return (
      <div className="stack">
        <p>Your computer has no shell available for new sessions.</p>
        <p className="muted">Run <code>pterm doctor</code> on your computer to see why, then tap Check again.</p>
      </div>
    );
  }
  return null;
}

export function Sessions({ settings, message, onOpen, onSettings, onRefreshSettings, onFailure }: {
  readonly settings: HTTPSettingsResponse;
  readonly message?: string;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onSettings: () => void;
  readonly onRefreshSettings: () => Promise<void>;
  readonly onFailure: (error: unknown) => string;
}) {
  const [sessions, setSessions] = useState<ReadonlyArray<BrokerRPCSessionRecord>>();
  const [error, setError] = useState<string | undefined>(message);
  const [busy, setBusy] = useState(false);
  const [starting, setStarting] = useState(false);
  const [pending, setPending] = useState<Pending>();
  const [chosenProject, setProjectId] = useState<string>();
  const [chosenShell, setShellId] = useState<string>();
  // Fall back to the first option when the choice is gone or settings were refreshed.
  const projectId = settings.projects.find((item) => item.id === chosenProject)?.id ?? settings.projects[0]?.id ?? '';
  const shellId = settings.shellProfiles.find((item) => item.id === chosenShell)?.id ?? settings.shellProfiles[0]?.id ?? '';
  const canCreate = settings.projects.length > 0 && settings.shellProfiles.length > 0;

  const projectName = new Map(settings.projects.map((item) => [item.id, item.name]));
  const shellName = new Map(settings.shellProfiles.map((item) => [item.id, item.name]));

  const load = useCallback(async () => {
    try {
      setSessions((await api.sessions()).sessions);
    } catch (failure) {
      setError(onFailure(failure));
    }
  }, [onFailure]);

  useEffect(() => { void load(); }, [load]);

  async function mutate(work: () => Promise<unknown>) {
    setBusy(true);
    setError(undefined);
    try {
      await work();
      setPending(undefined);
      await load();
    } catch (failure) {
      setError(onFailure(failure));
    } finally {
      setBusy(false);
    }
  }

  async function refresh() {
    setBusy(true);
    setError(undefined);
    try {
      await onRefreshSettings();
      await load();
    } catch (failure) {
      setError(onFailure(failure));
    } finally {
      setBusy(false);
    }
  }

  // Start session opens the live terminal right away; the list is not reloaded behind it.
  async function create() {
    setBusy(true);
    setStarting(true);
    setError(undefined);
    try {
      const created = await api.createSession({ operationId: crypto.randomUUID(), projectId, shellProfileId: shellId });
      onOpen(created.session);
    } catch (failure) {
      setError(onFailure(failure));
      setBusy(false);
      setStarting(false);
    }
  }

  return (
    <main className="page">
      <header className="page-header">
        <h1>Sessions</h1>
        <button type="button" onClick={onSettings}>Settings</button>
      </header>
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      <section className="card stack" aria-labelledby="new-session">
        <h2 id="new-session">New session</h2>
        {!canCreate ? (
          <>
            <NewSessionHelp settings={settings} />
            <button type="button" disabled={busy} onClick={() => { void refresh(); }}>Check again</button>
          </>
        ) : (
          <>
            <label htmlFor="project">Project</label>
            <select id="project" value={projectId} onChange={(event) => setProjectId(event.target.value)}>
              {settings.projects.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
            <label htmlFor="shell">Shell</label>
            <select id="shell" value={shellId} onChange={(event) => setShellId(event.target.value)}>
              {settings.shellProfiles.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
            <button type="button" className="primary" disabled={busy} onClick={() => { void create(); }}>
              {starting ? 'Starting…' : 'Start session'}
            </button>
          </>
        )}
      </section>

      <section aria-labelledby="session-list" className="stack">
        <div className="page-header">
          <h2 id="session-list">Your sessions</h2>
          <button type="button" onClick={() => { void refresh(); }} disabled={busy}>Refresh</button>
        </div>
        {sessions === undefined && <p role="status">Loading…</p>}
        {sessions?.length === 0 && (
          <p className="muted">{canCreate ? 'No sessions yet. Start one above.' : 'No sessions yet.'}</p>
        )}
        <ul className="session-list">
          {sessions?.map((session) => {
            const ended = session.state === 'exited' || session.state === 'lost';
            const renaming = pending?.kind === 'rename' && pending.id === session.id ? pending : undefined;
            const terminating = pending?.kind === 'terminate' && pending.id === session.id;
            const titleOk = renaming !== undefined && SAFE_TITLE.test(renaming.title.trim());
            return (
              <li key={`${session.id}/${session.generation}`} className="card stack">
                <h3 className="session-title">{session.title}</h3>
                <dl className="facts">
                  <dt>Project</dt><dd>{projectName.get(session.projectId) ?? 'Unknown project'}</dd>
                  <dt>Shell</dt><dd>{shellName.get(session.shellProfileId) ?? 'Unknown shell'}</dd>
                  <dt>State</dt><dd>{lifecycleLabel(session)}</dd>
                  <PresenceFacts session={session} />
                  <dt>Size</dt><dd>{session.columns}×{session.rows}</dd>
                </dl>
                {renaming !== undefined && (
                  <div className="stack">
                    <label htmlFor={`title-${session.id}`}>New title</label>
                    <input
                      id={`title-${session.id}`}
                      value={renaming.title}
                      maxLength={128}
                      autoComplete="off"
                      onChange={(event) => setPending({ ...renaming, title: event.target.value })}
                    />
                    <div className="row">
                      <button
                        type="button"
                        className="primary"
                        disabled={busy || !titleOk}
                        onClick={() => {
                          void mutate(() => api.renameSession(session.id, {
                            operationId: crypto.randomUUID(),
                            generation: session.generation,
                            title: renaming.title.trim(),
                          }));
                        }}
                      >Save title</button>
                      <button type="button" onClick={() => setPending(undefined)}>Cancel</button>
                    </div>
                  </div>
                )}
                {terminating && (
                  <div role="alertdialog" aria-labelledby={`terminate-${session.id}`} className="confirm stack">
                    <p id={`terminate-${session.id}`}>
                      Terminate “{session.title}”? This ends the shell and every program running in it.
                      It is not Ctrl+C and not Detach, and it cannot be undone.
                    </p>
                    <div className="row">
                      <button
                        type="button"
                        className="danger"
                        disabled={busy}
                        onClick={() => {
                          void mutate(() => api.terminateSession(session.id, {
                            operationId: crypto.randomUUID(),
                            generation: session.generation,
                          }));
                        }}
                      >Terminate session</button>
                      <button type="button" onClick={() => setPending(undefined)}>Keep running</button>
                    </div>
                  </div>
                )}
                {renaming === undefined && !terminating && (
                  <div className="row">
                    <button type="button" className="primary" disabled={ended} onClick={() => onOpen(session)}>
                      Open
                    </button>
                    <button
                      type="button"
                      disabled={ended}
                      onClick={() => setPending({ kind: 'rename', id: session.id, title: session.title })}
                    >Rename</button>
                    <button
                      type="button"
                      className="danger"
                      disabled={ended}
                      onClick={() => setPending({ kind: 'terminate', id: session.id })}
                    >Terminate…</button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      </section>
    </main>
  );
}
