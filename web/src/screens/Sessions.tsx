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

export function Sessions({ settings, message, onOpen, onSettings, onFailure }: {
  readonly settings: HTTPSettingsResponse;
  readonly message?: string;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onSettings: () => void;
  readonly onFailure: (error: unknown) => string;
}) {
  const [sessions, setSessions] = useState<ReadonlyArray<BrokerRPCSessionRecord>>();
  const [error, setError] = useState<string | undefined>(message);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Pending>();
  const [projectId, setProjectId] = useState(settings.projects[0]?.id ?? '');
  const [shellId, setShellId] = useState(settings.shellProfiles[0]?.id ?? '');

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

  async function mutate(work: () => Promise<unknown>, after?: (result: unknown) => void) {
    setBusy(true);
    setError(undefined);
    try {
      const result = await work();
      setPending(undefined);
      after?.(result);
      await load();
    } catch (failure) {
      setError(onFailure(failure));
    } finally {
      setBusy(false);
    }
  }

  const create = () => mutate(
    () => api.createSession({ operationId: crypto.randomUUID(), projectId, shellProfileId: shellId }),
    (result) => onOpen((result as Awaited<ReturnType<typeof api.createSession>>).session),
  );

  return (
    <main className="page">
      <header className="page-header">
        <h1>Sessions</h1>
        <button type="button" onClick={onSettings}>Settings</button>
      </header>
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      <section className="card stack" aria-labelledby="new-session">
        <h2 id="new-session">New session</h2>
        {settings.projects.length === 0 || settings.shellProfiles.length === 0 ? (
          <p className="muted">
            No saved project or shell profile yet. On your computer, run <code>pterm project add &lt;path&gt;</code>.
          </p>
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
              Start session
            </button>
          </>
        )}
      </section>

      <section aria-labelledby="session-list" className="stack">
        <div className="page-header">
          <h2 id="session-list">Your sessions</h2>
          <button type="button" onClick={() => { void load(); }} disabled={busy}>Refresh</button>
        </div>
        {sessions === undefined && <p role="status">Loading…</p>}
        {sessions?.length === 0 && <p className="muted">No sessions yet.</p>}
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
