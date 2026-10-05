import { useState } from 'react';
import { client } from '../api/http';
import type { Computer } from '../computers';
import type { HTTPSettingsResponse } from '../generated/protocol';
import { checkAgain, hosted, type ComputerEntry, type HostedSession } from './allSessions';
import { ComputerChips, problemText } from './Sessions';
import { Button, Notice } from './ui';
import {
  loadPresets,
  presetFrom,
  runFirstProblem,
  samePreset,
  savePresets,
  type LaunchPreset,
} from './presets';

/** What to do when there is nothing to start a session with, or null when ready. */
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

const EMPTY: HTTPSettingsResponse['projects'] = [];

/**
 * The computer a new session starts on: the chosen one while it answers,
 * else `initial` (the session it was opened from), else home, else the first
 * that answers. Computers that do not answer cannot be picked.
 */
export function pickComputer(entries: ReadonlyArray<ComputerEntry>, chosen: string | undefined, initial?: string): ComputerEntry | undefined {
  const ok = entries.filter((entry) => entry.status.kind === 'ok');
  return ok.find((entry) => entry.computer.host === chosen)
    ?? ok.find((entry) => entry.computer.host === initial)
    ?? ok.find((entry) => entry.computer.home)
    ?? ok[0];
}

export function NewSession({ entries, initial, onCancel, onStarted, onReload, onFailure }: {
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** Preselects this computer (the session the screen was opened from). */
  readonly initial?: Computer;
  readonly onCancel: () => void;
  /** `runFirst` is the trimmed command to type once, if any. */
  readonly onStarted: (session: HostedSession, runFirst: string | undefined) => void;
  /** Reads one computer again, settings included. */
  readonly onReload: (origin: string) => Promise<void>;
  readonly onFailure: (error: unknown, computer?: Computer) => string;
}) {
  const [chosenComputer, setComputer] = useState<string>();
  const picked = pickComputer(entries, chosenComputer, initial?.host);
  const computer = picked?.computer;
  const settings: HTTPSettingsResponse | undefined = picked?.status.kind === 'ok' ? picked.status.settings : undefined;
  const projects = settings?.projects ?? EMPTY;
  const shells = settings?.shellProfiles ?? EMPTY;
  const multi = entries.length > 1;
  const [presets, setPresets] = useState<LaunchPreset[]>(() => loadPresets());
  const [editing, setEditing] = useState(false);
  const [chosenProject, setProjectId] = useState<string>();
  const [chosenShell, setShellId] = useState<string>();
  const [runFirst, setRunFirst] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [note, setNote] = useState<string>();

  // The server lists the user's login shell first, so it is the default.
  const projectId = projects.find((item) => item.id === chosenProject)?.id ?? projects[0]?.id ?? '';
  const shellId = shells.find((item) => item.id === chosenShell)?.id ?? shells[0]?.id ?? '';
  const canCreate = projects.length > 0 && shells.length > 0;
  const projectName = new Map(projects.map((item) => [item.id, item.name]));
  const shellName = new Map(shells.map((item) => [item.id, item.name]));
  const command = runFirst.trim();
  const commandProblem = runFirstProblem(command);

  async function start(project: string, shell: string, first: string | undefined) {
    if (computer === undefined) return;
    setBusy(true);
    setError(undefined);
    try {
      const created = await client(computer.origin).createSession({ operationId: crypto.randomUUID(), projectId: project, shellProfileId: shell });
      onStarted(hosted(created.session, computer), first);
    } catch (failure) {
      setError(onFailure(failure, computer));
      setBusy(false);
    }
  }

  function store(next: LaunchPreset[], done: string) {
    const problem = savePresets(next);
    if (problem !== undefined) {
      setError(problem);
      return;
    }
    setPresets(next);
    setError(undefined);
    setNote(done);
  }

  function saveCustom() {
    const preset = presetFrom(projectId, shellId, command, projectName.get(projectId) ?? '');
    if (presets.some((item) => samePreset(item, preset))) {
      setNote('This preset is already saved.');
      return;
    }
    store([...presets, preset], `Saved “${preset.label}” as a preset.`);
  }

  async function refresh() {
    if (computer === undefined) return;
    setBusy(true);
    setError(undefined);
    setError(await checkAgain(onReload(computer.origin), (failure) => onFailure(failure, computer)));
    setBusy(false);
  }

  const now = Date.now();
  const unavailable = entries.flatMap((entry) => {
    const text = problemText(entry, now);
    return text === undefined ? [] : [text];
  });

  return (
    <main className="page screen">
      <header className="screen-header">
        <div>
          <h1>New session</h1>
          <p className="subtitle">{multi && computer !== undefined ? `Runs on ${computer.label}` : 'Runs on your computer'}</p>
        </div>
        <button type="button" className="link" onClick={onCancel}>Cancel</button>
      </header>
      {error !== undefined && <Notice tone="danger" role="alert" title={error} />}

      {multi && (
        <section className="group" aria-labelledby="computer-heading">
          <h2 id="computer-heading" className="caps">Computer</h2>
          <ComputerChips
            computers={entries.map((entry) => entry.computer)}
            chosen={computer?.host}
            all={false}
            disabled={(host) => entries.find((entry) => entry.computer.host === host)?.status.kind !== 'ok'}
            onChoose={(host) => { if (host !== undefined) { setComputer(host); setNote(undefined); } }}
          />
          {unavailable.map((text) => <p key={text} className="hint-text">{text}</p>)}
        </section>
      )}

      {settings === undefined ? (
        <p role="status" className="muted">{entries.some((entry) => entry.status.kind === 'loading' || entry.status.kind === 'connecting') ? 'Loading…' : 'No computer can be reached.'}</p>
      ) : !canCreate ? (
        <section className="stack">
          <NewSessionHelp settings={settings} />
          <Button className="secondary" busy={busy} busyLabel="Checking…" onClick={() => { void refresh(); }}>Check again</Button>
        </section>
      ) : (
        <>
          {presets.length > 0 && (
            <section className="group" aria-labelledby="launch-heading">
              <div className="section-head">
                <h2 id="launch-heading" className="caps">Launch</h2>
                <button type="button" className="link" aria-pressed={editing} onClick={() => setEditing(!editing)}>
                  {editing ? 'Done' : 'Edit'}
                </button>
              </div>
              <ul className="rows">
                {presets.map((preset, index) => {
                  const available = projectName.has(preset.projectId) && shellName.has(preset.shellProfileId);
                  return (
                    <li key={`${preset.label}/${preset.projectId}/${preset.shellProfileId}/${preset.runFirst ?? ''}`}>
                      <button
                        type="button"
                        className={index === 0 && !editing ? 'launch-row first' : 'launch-row'}
                        disabled={busy || (!editing && !available)}
                        {...(editing ? { 'aria-label': `Remove preset ${preset.label}` } : {})}
                        onClick={() => {
                          if (editing) {
                            store(presets.filter((_, at) => at !== index), `Removed “${preset.label}”.`);
                          } else {
                            void start(preset.projectId, preset.shellProfileId, preset.runFirst);
                          }
                        }}
                      >
                        <span className="launch-glyph" aria-hidden="true">
                          {preset.runFirst === undefined ? '$' : '❯'}
                        </span>
                        <span className="row-text">
                          <span className="row-title">{preset.label}</span>
                          <span className="row-meta">
                            {available
                              ? `${projectName.get(preset.projectId)} · ${shellName.get(preset.shellProfileId)}`
                              : 'Project or shell is no longer available'}
                          </span>
                        </span>
                        <span className="row-trail">{editing ? 'remove' : index === 0 ? 'start ⏎' : 'start'}</span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </section>
          )}

          <section className="group" aria-labelledby="custom-heading">
            <h2 id="custom-heading" className="caps">Custom</h2>
            <div className="field-group">
              <div className="field-row">
                <label htmlFor="project">project</label>
                <select id="project" value={projectId} onChange={(event) => setProjectId(event.target.value)}>
                  {settings.projects.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                </select>
              </div>
              <div className="field-row">
                <label htmlFor="shell">shell</label>
                <select id="shell" value={shellId} onChange={(event) => setShellId(event.target.value)}>
                  {settings.shellProfiles.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                </select>
              </div>
              <div className="field-row">
                <label htmlFor="run-first">run first</label>
                <input
                  id="run-first"
                  value={runFirst}
                  placeholder="optional, e.g. claude"
                  autoCapitalize="off"
                  autoCorrect="off"
                  autoComplete="off"
                  spellCheck={false}
                  aria-describedby="run-first-help"
                  aria-invalid={commandProblem !== undefined}
                  onChange={(event) => { setRunFirst(event.target.value); setNote(undefined); }}
                />
              </div>
            </div>
            <p id="run-first-help" className={commandProblem === undefined ? 'hint-text' : 'error field-note'}>
              {commandProblem ?? 'Typed once with Enter when the session opens, only if no one else has control.'}
            </p>
            <div className="row">
              <button type="button" disabled={commandProblem !== undefined} onClick={saveCustom}>Save as preset</button>
              {note !== undefined && <span role="status" className="hint-text">{note}</span>}
            </div>
          </section>

          <div className="bottom-bar">
            <Button
              className="primary wide"
              icon={<span aria-hidden="true" className="prompt">❯</span>}
              busy={busy}
              busyLabel="starting…"
              disabled={commandProblem !== undefined}
              onClick={() => { void start(projectId, shellId, command === '' ? undefined : command); }}
            >start session</Button>
          </div>
        </>
      )}
    </main>
  );
}
