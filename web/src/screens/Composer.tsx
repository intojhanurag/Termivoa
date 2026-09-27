import { useEffect, useState } from 'react';
import type { Connection } from '../terminal/Connection';
import { checkPaste, lineCount, readDraft, saveDraft } from '../terminal/composer';
import { NO_MODIFIERS } from '../terminal/InputController';
import { KeyButton } from './Dock';
import { Sheet, SheetHeader } from './Sheet';

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
    <Sheet labelledBy="composer-title" onClose={onClose}>
      <SheetHeader id="composer-title" title="Compose">
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>
      <label htmlFor="composer-draft" className="visually-hidden">
        Draft text here. Nothing is sent until you choose an action.
      </label>
      <textarea
        id="composer-draft"
        className="draft composer-input"
        value={text}
        rows={5}
        placeholder="Draft text here. Nothing is sent until you paste it."
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => { setText(event.target.value); setConfirming(false); }}
      />
      <div className="spread composer-meta">
        {lines > 1 ? (
          <span className="amber note">
            <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
              <path d="M7 1.5 13 12.5H1z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
              <path d="M7 5.5v3M7 10.3v.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
            {lines} lines · you confirm before it pastes
          </span>
        ) : <span />}
        <button type="button" className="link" disabled={text === ''} onClick={() => setText('')}>Clear</button>
      </div>
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
        <div className="composer-actions">
          <button type="button" className="primary paste" disabled={!canSend || text === ''} onClick={() => paste(false)}>
            <span aria-hidden="true" className="prompt">❯</span> Paste into terminal
          </button>
          <KeyButton
            label="Enter"
            className="enter tall"
            disabled={!canSend}
            onPress={() => connection.sendKey('enter', NO_MODIFIERS)}
          >
            <span className="glyph">⏎</span><span className="sub">enter</span>
          </KeyButton>
        </div>
      )}
    </Sheet>
  );
}
