import { useState } from 'react';
import type { ProjectTask, TaskKind } from '../generated/protocol';
import type { TasksView } from '../terminal/Connection';
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
import { displayPath } from './folder';
import { runFirstProblem } from './presets';
import { Sheet, SheetHeader } from './Sheet';
import { Button, Notice } from './ui';

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

const KINDS: ReadonlyArray<TaskKind> = ['test', 'dev', 'build', 'lint', 'other'];
/** More "other" tasks than this fold behind More. */
export const OTHER_SHOWN = 3;

/** The tasks by kind, in the order test, dev, build, lint, other; empty kinds are left out. */
export function taskGroups(tasks: ReadonlyArray<ProjectTask>): Array<{ readonly kind: TaskKind; readonly tasks: ProjectTask[] }> {
  return KINDS.map((kind) => ({ kind, tasks: tasks.filter((task) => task.kind === kind) })).filter((group) => group.tasks.length > 0);
}

/**
 * What already holds the terminal: a full-screen app (Claude Code and Codex
 * run in the alternate screen), or a program the broker sees working or
 * waiting. Typing a command there would go into that program.
 */
export type TerminalBusy = 'fullscreen' | 'running';

/** Why "Type here" is off, or undefined when this device can type into the session. */
export function typeHereProblem(canSend: boolean, busy: TerminalBusy | undefined): string | undefined {
  if (busy === 'fullscreen') return 'A full-screen app, such as Claude Code, is open here. Use Run in new tab.';
  if (busy === 'running') return 'Something is running here. Use Run in new tab.';
  if (!canSend) return 'Take control to type here.';
  return undefined;
}

/** A task typed like a snippet with "Press Enter after pasting". */
export function taskSnippet(task: ProjectTask): Snippet {
  return { id: `task:${task.name}`, title: task.name, text: task.command, sendEnter: true, shell: true };
}

/**
 * The project's commands, read from its files on the computer. Tapping one
 * opens its actions; nothing runs until an action is tapped.
 */
export function ProjectTasks({ view, open, more, starting = false, typeProblem, onOpen, onMore, onRetry, onRun, onType }: {
  readonly view: TasksView | undefined;
  readonly open: ProjectTask | undefined;
  /** Run in new tab is starting the session. */
  readonly starting?: boolean;
  /** Every "other" task is shown. */
  readonly more: boolean;
  readonly typeProblem: string | undefined;
  readonly onOpen: (task: ProjectTask | undefined) => void;
  readonly onMore: () => void;
  readonly onRetry: () => void;
  readonly onRun: (task: ProjectTask) => void;
  readonly onType: (task: ProjectTask) => void;
}) {
  const listed = view?.state === 'ready' ? view.listed : undefined;
  let body;
  if (view === undefined || view.state === 'loading') {
    body = <p role="status" className="muted">Looking for project commands…</p>;
  } else if (listed === undefined || listed.state === 'unavailable') {
    body = (
      <div className="row">
        <p role="status" className="muted">Could not read the project.</p>
        <button type="button" onClick={onRetry}>Retry</button>
      </div>
    );
  } else if (listed.tasks.length === 0) {
    body = (
      <p role="status" className="muted">
        No project commands found in {listed.folder === undefined ? 'this folder' : <code>{displayPath(listed.folder)}</code>}.
      </p>
    );
  } else if (open !== undefined) {
    const runProblem = runFirstProblem(open.command);
    body = (
      <div className="task-actions stack" role="group" aria-label={`Actions for ${open.name}`}>
        <p className="task-title">{open.name}</p>
        <code className="task-command">{open.command}</code>
        <Button className="primary" busy={starting} busyLabel="Starting…" disabled={runProblem !== undefined} onClick={() => onRun(open)}>
          Run in new tab
        </Button>
        {runProblem !== undefined && <p className="hint-text">{runProblem}</p>}
        <button
          type="button"
          disabled={typeProblem !== undefined}
          {...(typeProblem === undefined ? {} : { 'aria-describedby': 'type-here-reason' })}
          onClick={() => onType(open)}
        >Type here</button>
        {typeProblem !== undefined && <p id="type-here-reason" className="hint-text">{typeProblem}</p>}
        <button type="button" className="link" onClick={() => onOpen(undefined)}>Back to commands</button>
      </div>
    );
  } else {
    body = taskGroups(listed.tasks).map(({ kind, tasks }) => {
      const hidden = kind === 'other' && !more && tasks.length > OTHER_SHOWN;
      return (
        <div key={kind} className="task-group">
          <h4 className="caps">{kind}</h4>
          <ul className="task-chips">
            {(hidden ? tasks.slice(0, OTHER_SHOWN) : tasks).map((task) => (
              <li key={`${task.source}:${task.name}`}>
                <button type="button" className="task-chip" onClick={() => onOpen(task)}>
                  <span className="task-name">{task.name}</span>
                  <code className="task-command">{task.command}</code>
                </button>
              </li>
            ))}
            {hidden && (
              <li>
                <button type="button" className="task-chip more" onClick={onMore}>
                  More · {tasks.length - OTHER_SHOWN}
                </button>
              </li>
            )}
          </ul>
        </div>
      );
    });
  }
  return (
    <section className="project-tasks stack" aria-labelledby="project-tasks-title">
      <h3 id="project-tasks-title" className="caps">This project</h3>
      {body}
    </section>
  );
}

/**
 * Saved snippets. Tap sends as a paste; multi-line text asks first, like the
 * composer. The project's own commands come first.
 */
export function Snippets({ target, canSend, busy, tasks, onRetryTasks, onRunTask, onClose }: {
  readonly target: SnippetTarget;
  readonly canSend: boolean;
  /** What holds the terminal, so "Type here" is off; undefined at a shell prompt. */
  readonly busy: TerminalBusy | undefined;
  readonly tasks: TasksView | undefined;
  readonly onRetryTasks: () => void;
  /** Starts a new session in the same project and shell that runs this command first; settles when it opened or failed. Absent: no project commands (a provider terminal). */
  readonly onRunTask?: ((command: string) => Promise<void>) | undefined;
  readonly onClose: () => void;
}) {
  const [openTask, setOpenTask] = useState<ProjectTask>();
  const [more, setMore] = useState(false);
  const [list, setList] = useState<Snippet[]>(() => loadSnippets());
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<Draft>();
  const [confirming, setConfirming] = useState<Snippet>();
  const [problem, setProblem] = useState<string>();
  const [starting, setStarting] = useState(false);

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
          {onRunTask !== undefined && <ProjectTasks
            view={tasks}
            open={openTask}
            more={more}
            starting={starting}
            typeProblem={typeHereProblem(canSend, busy)}
            onOpen={setOpenTask}
            onMore={() => setMore(true)}
            onRetry={onRetryTasks}
            onRun={(task) => { setStarting(true); void onRunTask(task.command).finally(() => setStarting(false)); }}
            onType={(task) => send(taskSnippet(task), false)}
          />}
          <h3 className="caps">Saved snippets</h3>
          {!canSend && <Notice tone="neutral" role="status">Take control to send snippets. You can still edit them.</Notice>}
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
                  <span className={snippet.shell ? 'snippet-glyph muted' : 'snippet-glyph'} aria-hidden="true">
                    {snippet.shell ? '$' : '❯'}
                  </span>
                  <span className="row-text">
                    <span className="row-title">{snippet.title}</span>
                    <span className="row-meta">{snippet.shell ? 'shell command' : snippet.text}</span>
                  </span>
                  <span className={snippet.sendEnter ? 'badge strong' : 'badge'}>
                    {editing ? 'edit' : snippet.sendEnter ? '⏎ send' : 'paste'}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {list.length >= MAX_SNIPPETS && <p className="hint-text">You can keep {MAX_SNIPPETS} snippets at most. Delete one to add another.</p>}
          <p className="hint-text">Tap to send · edit to change · saved on this phone</p>
        </>
      )}
    </Sheet>
  );
}
