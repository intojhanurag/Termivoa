import { useEffect, useState } from 'react';
import type { Connection } from '../terminal/Connection';
import { checkPaste, lineCount, readDraft, saveDraft } from '../terminal/composer';
import { NO_MODIFIERS } from '../terminal/InputController';

export function Composer({ connection, canSend, draftKey, onClose }: {
  readonly connection: Connection;
  readonly canSend: boolean;
  readonly draftKey: string;
  readonly onClose: () => void;
}) {
  const [text, setText] = useState(() => readDraft(draftKey));
  const [confirming, setConfirming] = useState(false);
  const [problem, setProblem] = useState<string>();

  useEffect(() => { saveDraft(draftKey, text); }, [draftKey, text]);
  useEffect(() => { if (!canSend) setConfirming(false); }, [canSend]);

  function paste(confirmed: boolean) {
    setProblem(undefined);
    switch (checkPaste(text, canSend, confirmed)) {
      case 'needs-confirmation':
        setConfirming(true);
        return;
      case 'too-large':
        setProblem('This draft is larger than 64 KiB. Shorten it before pasting.');
        return;
      case 'ready':
        if (connection.sendPaste(text)) {
          setText('');
        }
        setConfirming(false);
        return;
      default:
    }
  }

  const lines = lineCount(text);

  return (
    <section className="panel stack" aria-labelledby="composer-title">
      <div className="page-header">
        <h2 id="composer-title">Composer</h2>
        <button type="button" onClick={onClose}>Close</button>
      </div>
      <label htmlFor="composer-draft" className="muted">
        Draft text here. Nothing is sent until you choose an action.
      </label>
      <textarea
        id="composer-draft"
        className="composer-input"
        value={text}
        rows={5}
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => { setText(event.target.value); setConfirming(false); }}
      />
      {!canSend && (
        <p role="status" className="notice">
          Sending is off because you are not connected or do not have control. Your draft is kept.
        </p>
      )}
      {problem !== undefined && <p role="alert" className="error">{problem}</p>}
      {confirming ? (
        <div role="alertdialog" aria-labelledby="paste-warning" className="confirm stack">
          <p id="paste-warning">
            This draft has {lines} lines. A shell without safe bracketed paste can run each line as a command.
          </p>
          <div className="row">
            <button type="button" className="danger" disabled={!canSend} onClick={() => paste(true)}>
              Paste {lines} lines
            </button>
            <button type="button" onClick={() => setConfirming(false)}>Cancel</button>
          </div>
        </div>
      ) : (
        <div className="row">
          <button type="button" className="primary" disabled={!canSend || text === ''} onClick={() => paste(false)}>
            Paste into terminal
          </button>
          <button type="button" disabled={!canSend} onClick={() => connection.sendKey('enter', NO_MODIFIERS)}>
            Enter
          </button>
          <button type="button" disabled={text === ''} onClick={() => setText('')}>Clear draft</button>
        </div>
      )}
    </section>
  );
}
