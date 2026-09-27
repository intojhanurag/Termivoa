import { useState } from 'react';
import { lineCount } from '../terminal/composer';
import {
  loadSnippets,
  MAX_SNIPPET_TITLE,
  MAX_SNIPPETS,
  saveSnippets,
  sendSnippet,
  snippetProblem,
  type Snippet,
  type SnippetTarget,
} from '../terminal/snippets';
import { Sheet, SheetHeader } from './Sheet';

interface Draft {
  readonly id: string | undefined;
  readonly title: string;
  readonly text: string;
  readonly sendEnter: boolean;
  readonly shell: boolean;
}

const EMPTY: Draft = { id: undefined, title: '', text: '', sendEnter: true, shell: false };

function SnippetForm({ draft, onChange, onSave, onDelete, onCancel, problem }: {
  readonly draft: Draft;
  readonly onChange: (draft: Draft) => void;
  readonly onSave: () => void;
  readonly onDelete: (() => void) | undefined;
  readonly onCancel: () => void;
  readonly problem: string | undefined;
}) {
  return (
    <form className="stack" onSubmit={(event) => { event.preventDefault(); onSave(); }}>
      <label htmlFor="snippet-title">Title</label>
      <input
        id="snippet-title"
        value={draft.title}
        maxLength={MAX_SNIPPET_TITLE}
        autoComplete="off"
        onChange={(event) => onChange({ ...draft, title: event.target.value })}
      />
      <label htmlFor="snippet-text">Text to send</label>
      <textarea
        id="snippet-text"
        className="draft"
        rows={4}
        value={draft.text}
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => onChange({ ...draft, text: event.target.value })}
      />
      <label className="check">
        <input type="checkbox" checked={draft.sendEnter} onChange={(event) => onChange({ ...draft, sendEnter: event.target.checked })} />
        Press Enter after pasting
      </label>
      <label className="check">
        <input type="checkbox" checked={draft.shell} onChange={(event) => onChange({ ...draft, shell: event.target.checked })} />
        Shell command
      </label>
      {problem !== undefined && <p role="alert" className="error">{problem}</p>}
      <div className="row">
        <button type="submit" className="primary">save</button>
        <button type="button" onClick={onCancel}>cancel</button>
        {onDelete !== undefined && <button type="button" className="danger" onClick={onDelete}>delete</button>}
      </div>
    </form>
  );
}

/** Saved snippets. Tap sends as a paste; multi-line text asks first, like the composer. */
export function Snippets({ target, canSend, onClose }: {
  readonly target: SnippetTarget;
  readonly canSend: boolean;
  readonly onClose: () => void;
}) {
  const [list, setList] = useState<Snippet[]>(() => loadSnippets());
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<Draft>();
  const [confirming, setConfirming] = useState<Snippet>();
  const [problem, setProblem] = useState<string>();

  function send(snippet: Snippet, confirmed: boolean) {
    setProblem(undefined);
    switch (sendSnippet(target, snippet, canSend, confirmed)) {
      case 'needs-confirmation':
        setConfirming(snippet);
        return;
      case 'sent':
        onClose();
        return;
      case 'disabled':
        setProblem('Take control to send snippets.');
        return;
      case 'too-large':
        setProblem('This snippet is larger than 64 KiB.');
        return;
      case 'failed':
        setConfirming(undefined);
        setProblem('Not sent. Check the terminal before sending again.');
        return;
      default:
    }
  }

  function store(next: Snippet[]): boolean {
    const failure = saveSnippets(next);
    if (failure !== undefined) {
      setProblem(failure);
      return false;
    }
    setList(next);
    setDraft(undefined);
    setProblem(undefined);
    return true;
  }

  function save(current: Draft) {
    const title = current.title.trim();
    const invalid = snippetProblem(title, current.text);
    if (invalid !== undefined) {
      setProblem(invalid);
      return;
    }
    const snippet: Snippet = {
      id: current.id ?? crypto.randomUUID(), title, text: current.text, sendEnter: current.sendEnter, shell: current.shell,
    };
    store(current.id === undefined ? [...list, snippet] : list.map((item) => (item.id === current.id ? snippet : item)));
  }

  return (
    <Sheet labelledBy="snippets-title" onClose={onClose}>
      <SheetHeader id="snippets-title" glyph="⌘" title="Snippets">
        {draft === undefined && (
          <>
            <button type="button" className="link" aria-pressed={editing} onClick={() => setEditing(!editing)}>
              {editing ? 'done' : 'edit'}
            </button>
            <button
              type="button"
              className="link"
              disabled={list.length >= MAX_SNIPPETS}
              onClick={() => { setProblem(undefined); setDraft(EMPTY); }}
            >+ new</button>
          </>
        )}
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>

      {draft !== undefined ? (
        <SnippetForm
          draft={draft}
          problem={problem}
          onChange={setDraft}
          onSave={() => save(draft)}
          onCancel={() => { setDraft(undefined); setProblem(undefined); }}
          onDelete={draft.id === undefined ? undefined : () => { store(list.filter((item) => item.id !== draft.id)); }}
        />
      ) : confirming !== undefined ? (
        <div role="alertdialog" aria-labelledby="snippet-warning" className="confirm stack">
          <p id="snippet-warning">
            “{confirming.title}” has {lineCount(confirming.text)} lines. A shell without safe bracketed paste can run
            each line as a command.
          </p>
          <div className="row">
            <button type="button" className="danger" disabled={!canSend} onClick={() => send(confirming, true)}>
              Paste {lineCount(confirming.text)} lines
            </button>
            <button type="button" onClick={() => setConfirming(undefined)}>Cancel</button>
          </div>
        </div>
      ) : (
        <>
          {!canSend && <p role="status" className="notice">Take control to send snippets. You can still edit them.</p>}
          {problem !== undefined && <p role="alert" className="error">{problem}</p>}
          {list.length === 0 && <p className="muted">No snippets. Tap + new to save one.</p>}
          <ul className="rows snippet-list">
            {list.map((snippet) => (
              <li key={snippet.id}>
                <button
                  type="button"
                  className="snippet-row"
                  disabled={!editing && !canSend}
                  {...(editing ? { 'aria-label': `Edit ${snippet.title}` } : {})}
                  onClick={() => {
                    if (editing) {
                      setProblem(undefined);
                      setDraft({ ...snippet });
                    } else {
                      send(snippet, false);
                    }
                  }}
                >
                  <span className={snippet.shell ? 'snippet-glyph muted' : 'snippet-glyph accent'} aria-hidden="true">
                    {snippet.shell ? '$' : '❯'}
                  </span>
                  <span className="row-text">
                    <span className="row-title">{snippet.title}</span>
                    <span className="row-meta">{snippet.shell ? 'shell command' : snippet.text}</span>
                  </span>
                  <span className={snippet.sendEnter ? 'badge accent' : 'badge'}>
                    {editing ? 'edit' : snippet.sendEnter ? '⏎ send' : 'paste'}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <p className="hint-text">Tap to send · edit to change · saved on this phone</p>
        </>
      )}
    </Sheet>
  );
}
