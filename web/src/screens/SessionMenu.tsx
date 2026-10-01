import { useState } from 'react';
import { api, isAuthFailure } from '../api/http';
import type { BrokerRPCSessionRecord, SessionWorktree } from '../generated/protocol';
import type { WorktreeRemoval } from '../terminal/Connection';
import { Sheet, SheetHeader } from './Sheet';
import { RemoveWorktree } from './Worktree';

const SAFE_TITLE = /^[^\p{Cc}\s](?:[^\p{Cc}]{0,126}[^\p{Cc}\s])?$/u;

/** The "…" menu: control, worktrees, rename, terminate, and the way out to other screens. */
export function SessionMenu({
  session, controlling, running, removal,
  onRelease, onNewWorktree, onRemoveWorktree, onSessions, onSettings, onChanged, onAuthFailure, onClose,
}: {
  readonly session: BrokerRPCSessionRecord;
  readonly controlling: boolean;
  /** The shell is running now, so its folder can be read. */
  readonly running: boolean;
  /** The latest worktree remove on this connection. */
  readonly removal: WorktreeRemoval | undefined;
  readonly onRelease: () => void;
  readonly onNewWorktree: () => void;
  readonly onRemoveWorktree: (force: boolean) => void;
  readonly onSessions: () => void;
  readonly onSettings: () => void;
  readonly onChanged: () => Promise<void>;
  readonly onAuthFailure: () => void;
  readonly onClose: () => void;
}) {
  const [mode, setMode] = useState<'menu' | 'rename' | 'terminate' | 'remove'>('menu');
  // Kept while removing: the record loses it once the session ends.
  const [removing, setRemoving] = useState<SessionWorktree>();
  // Only a remove sent from this menu shows its reply; an older one is not news.
  const [removeSent, setRemoveSent] = useState(false);
  const [title, setTitle] = useState(session.title);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const ended = session.state === 'exited' || session.state === 'lost';
  const titleOk = SAFE_TITLE.test(title.trim());

  async function mutate(work: () => Promise<unknown>) {
    setBusy(true);
    setError(undefined);
    try {
      await work();
      await onChanged();
      onClose();
    } catch (failure) {
      if (isAuthFailure(failure)) {
        onAuthFailure();
        return;
      }
      setError(failure instanceof Error ? failure.message : 'Request failed');
      setBusy(false);
    }
  }

  return (
    <Sheet labelledBy="menu-title" onClose={onClose}>
      <SheetHeader id="menu-title" title={session.title}>
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>
      {error !== undefined && <p role="alert" className="error">{error}</p>}
      {mode === 'rename' && (
        <form
          className="stack"
          onSubmit={(event) => {
            event.preventDefault();
            if (!titleOk) return;
            void mutate(() => api.renameSession(session.id, {
              operationId: crypto.randomUUID(),
              generation: session.generation,
              title: title.trim(),
            }));
          }}
        >
          <label htmlFor="session-title">New title</label>
          <input id="session-title" value={title} maxLength={128} autoComplete="off" onChange={(event) => setTitle(event.target.value)} />
          <div className="row">
            <button type="submit" className="primary" disabled={busy || !titleOk}>Save title</button>
            <button type="button" onClick={() => setMode('menu')}>Cancel</button>
          </div>
        </form>
      )}
      {mode === 'terminate' && (
        <div role="alertdialog" aria-labelledby="terminate-warning" className="confirm stack">
          <p id="terminate-warning">
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
            <button type="button" onClick={() => setMode('menu')}>Keep running</button>
          </div>
        </div>
      )}
      {mode === 'remove' && removing !== undefined && (
        <RemoveWorktree
          worktree={removing}
          removal={removeSent ? removal : undefined}
          onRemove={(force) => { onRemoveWorktree(force); setRemoveSent(true); }}
          onCancel={() => { setRemoveSent(false); setMode('menu'); }}
        />
      )}
      {mode === 'menu' && (
        <ul className="menu-list">
          {controlling && (
            <li><button type="button" onClick={() => { onRelease(); onClose(); }}>Release control</button></li>
          )}
          {running && (
            <li><button type="button" onClick={onNewWorktree}>New worktree…</button></li>
          )}
          <li><button type="button" disabled={ended} onClick={() => setMode('rename')}>Rename…</button></li>
          {session.worktree !== undefined && controlling && (
            <li>
              <button
                type="button"
                className="danger"
                disabled={ended}
                onClick={() => { setRemoving(session.worktree); setMode('remove'); }}
              >Remove worktree…</button>
            </li>
          )}
          <li><button type="button" className="danger" disabled={ended} onClick={() => setMode('terminate')}>Terminate…</button></li>
          <li><button type="button" onClick={onSessions}>All sessions</button></li>
          <li><button type="button" onClick={onSettings}>Settings</button></li>
        </ul>
      )}
    </Sheet>
  );
}
