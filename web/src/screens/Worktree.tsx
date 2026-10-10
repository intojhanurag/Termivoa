import { useState, type ReactElement } from 'react';
import { ApiError, isAuthFailure } from '../api/http';
import type { BranchesListedPayload, BranchRef, SessionWorktree } from '../generated/protocol';
import type { BranchesView, WorktreeRemoval } from '../terminal/Connection';
import { Sheet, SheetHeader } from './Sheet';
import { ConfirmPair } from './ui';

/** Mirrors the `worktreeName` schema. */
const WORKTREE_NAME = /^[a-z0-9]+(-[a-z0-9]+)*$/u;
export const MAX_WORKTREE_NAME = 40;

const ADJECTIVES = [
  'brave', 'calm', 'clever', 'cosy', 'crisp', 'curious', 'daring', 'eager', 'fancy', 'fierce',
  'gentle', 'glad', 'golden', 'happy', 'humble', 'jolly', 'keen', 'kind', 'lively', 'lucky',
  'merry', 'mighty', 'misty', 'nimble', 'noble', 'plucky', 'proud', 'quick', 'quiet', 'rapid',
  'rosy', 'shiny', 'silent', 'sleepy', 'snowy', 'sunny', 'swift', 'tidy', 'witty', 'zesty',
] as const;

const NOUNS = [
  'badger', 'bear', 'beaver', 'bison', 'crane', 'crow', 'deer', 'dolphin', 'eagle', 'falcon',
  'ferret', 'finch', 'fox', 'gecko', 'heron', 'koala', 'lark', 'lemur', 'lion', 'lynx',
  'marten', 'mole', 'moose', 'newt', 'otter', 'owl', 'panda', 'puffin', 'quail', 'raven',
  'robin', 'seal', 'sparrow', 'swan', 'tiger', 'toad', 'trout', 'walrus', 'wolf', 'wren',
] as const;

/** A name like "brave-otter". `random` returns a number in [0, 1), like Math.random. */
export function randomName(random: () => number = Math.random): string {
  const pick = <T,>(list: ReadonlyArray<T>) => list[Math.floor(random() * list.length)] ?? list[0];
  return `${pick(ADJECTIVES)}-${pick(NOUNS)}`;
}

/** A short message for the first problem with a worktree name, or undefined when it is valid. */
export function worktreeNameProblem(name: string): string | undefined {
  if (name === '') return 'Enter a name.';
  if (name.length > MAX_WORKTREE_NAME) return `Use ${MAX_WORKTREE_NAME} characters at most.`;
  if (!WORKTREE_NAME.test(name)) return 'Use lowercase letters and digits, with single hyphens between words.';
  return undefined;
}

/** The folder the host creates, as the user sees it. */
export function worktreeFolder(repo: string, name: string): string {
  return `~/termivoa/worktrees/${repo}/${name}`;
}

/** What to type once in the new session. */
export const START_WITH = [
  { label: 'Shell', command: '' },
  { label: 'Claude', command: 'claude' },
  { label: 'Codex', command: 'codex' },
] as const;

/**
 * The branches to offer, local first then remote as the host lists them, and
 * the base to preselect: the user's choice while it is listed, else the
 * repository's default, else the current branch. A default the host cut off
 * the list is added first, so the select always shows what is sent.
 */
export function branchChoices(listed: BranchesListedPayload, chosen: string | undefined): {
  readonly options: ReadonlyArray<BranchRef>;
  readonly base: string | undefined;
} {
  const fallback = listed.defaultBranch ?? listed.current ?? listed.branches[0]?.name;
  const options = fallback === undefined || listed.branches.some((branch) => branch.name === fallback)
    ? listed.branches
    : [{ name: fallback, remote: false }, ...listed.branches];
  const base = chosen !== undefined && options.some((branch) => branch.name === chosen) ? chosen : fallback;
  return { options, base };
}

const NOT_GIT = 'This folder is not in a git repository. cd into a repo first.';

/** Plain words for a failed create; others use the host's text. */
export function createProblem(failure: unknown, name: string): string {
  if (!(failure instanceof ApiError)) return failure instanceof Error ? failure.message : 'Request failed';
  switch (failure.code) {
    case 'worktree_exists': return `“${name}” already exists in this repository. Pick another name, or tap 🎲 for a new one.`;
    case 'not_a_repository': return NOT_GIT;
    case 'limit_exceeded': return 'Termivoa reached a limit on this computer. Wait for other actions to finish, or remove a worktree or session you no longer need, then try again.';
    // The host cleaned up, so the same name works again.
    case 'worktree_failed': return `${failure.message}. Nothing was left behind, so you can try again.`;
    default: return failure.message;
  }
}

/** The New worktree sheet's form. All state comes from props, so each state renders on its own. */
export function NewWorktreeSheet({
  view, name, chosenBase, start, busy, error,
  onName, onShuffle, onBase, onStart, onRetry, onCreate, onClose,
}: {
  readonly view: BranchesView | undefined;
  readonly name: string;
  readonly chosenBase: string | undefined;
  readonly start: string;
  readonly busy: boolean;
  readonly error: string | undefined;
  readonly onName: (name: string) => void;
  readonly onShuffle: () => void;
  readonly onBase: (base: string) => void;
  readonly onStart: (command: string) => void;
  readonly onRetry: () => void;
  readonly onCreate: (base: string) => void;
  readonly onClose: () => void;
}) {
  const listed = view?.state === 'ready' ? view.listed : undefined;
  let body: ReactElement;
  if (view === undefined || view.state === 'loading') {
    body = <p role="status" className="muted">Reading branches…</p>;
  } else if (listed === undefined || listed.state === 'unavailable') {
    body = (
      <div className="row">
        <p role="alert" className="error">Could not read the branches.</p>
        <button type="button" onClick={onRetry}>Retry</button>
      </div>
    );
  } else if (listed.state === 'not_git') {
    body = <p role="alert">{NOT_GIT}</p>;
  } else {
    const repo = listed.repo ?? 'repo';
    const { options, base } = branchChoices(listed, chosenBase);
    const problem = worktreeNameProblem(name);
    const local = options.filter((branch) => !branch.remote);
    const remote = options.filter((branch) => branch.remote);
    const option = (branch: BranchRef) => (
      <option key={branch.name} value={branch.name}>
        {branch.name === listed.current ? `${branch.name} · current` : branch.name}
      </option>
    );
    body = (
      <form
        className="stack"
        onSubmit={(event) => {
          event.preventDefault();
          if (!busy && problem === undefined && base !== undefined) onCreate(base);
        }}
      >
        <p className="muted">Repository <strong>{repo}</strong></p>
        <div className="field-group">
          <div className="field-row">
            <label htmlFor="worktree-name">name</label>
            <input
              id="worktree-name"
              value={name}
              maxLength={MAX_WORKTREE_NAME}
              autoCapitalize="off"
              autoCorrect="off"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={problem !== undefined}
              aria-describedby="worktree-name-help"
              onChange={(event) => {
                // Lowercasing keeps the length, so the cursor can stay where it was.
                const input = event.target;
                const cursor = input.selectionStart;
                onName(input.value.toLowerCase());
                requestAnimationFrame(() => input.setSelectionRange(cursor, cursor));
              }}
            />
            <button type="button" className="link" aria-label="New random name" onClick={onShuffle}>
              <span aria-hidden="true">🎲</span>
            </button>
          </div>
          <div className="field-row">
            <label htmlFor="worktree-base">from</label>
            {base === undefined ? (
              <select id="worktree-base" disabled><option>no branches</option></select>
            ) : (
              <select id="worktree-base" value={base} onChange={(event) => onBase(event.target.value)}>
                {local.length > 0 && <optgroup label="Local">{local.map(option)}</optgroup>}
                {remote.length > 0 && <optgroup label="Remote">{remote.map(option)}</optgroup>}
              </select>
            )}
          </div>
          <div className="field-row">
            <label htmlFor="worktree-start">start with</label>
            <select id="worktree-start" value={start} onChange={(event) => onStart(event.target.value)}>
              {START_WITH.map((item) => <option key={item.label} value={item.command}>{item.label}</option>)}
            </select>
          </div>
        </div>
        <p id="worktree-name-help" className={problem === undefined ? 'hint-text' : 'error'}>
          {problem ?? <>Creates <code>{worktreeFolder(repo, name)}</code> on branch <code>termivoa/{name}</code></>}
        </p>
        {base === undefined && <p className="hint-text">This repository has no branches yet. Make a first commit, then try again.</p>}
        {listed.truncated && <p className="hint-text">Only the first {listed.branches.length} branches are listed.</p>}
        {error !== undefined && <p role="alert" className="error">{error}</p>}
        <button type="submit" className="primary" disabled={busy || problem !== undefined || base === undefined}>
          {busy ? 'Creating…' : 'Create'}
        </button>
      </form>
    );
  }
  return (
    <Sheet labelledBy="worktree-title" onClose={onClose}>
      <SheetHeader id="worktree-title" glyph="⎇" title="New worktree">
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>
      {body}
    </Sheet>
  );
}

/**
 * Creates a git worktree from the session's repository and starts a session
 * in it. `onCreate` opens the new session, or rejects with the API error.
 */
export function NewWorktree({ view, onRetry, onCreate, onAuthFailure, onClose }: {
  readonly view: BranchesView | undefined;
  readonly onRetry: () => void;
  readonly onCreate: (worktree: { readonly name: string; readonly base: string }, runFirst: string | undefined) => Promise<void>;
  readonly onAuthFailure: () => void;
  readonly onClose: () => void;
}) {
  const [name, setName] = useState(() => randomName());
  const [chosenBase, setBase] = useState<string>();
  const [start, setStart] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function create(base: string) {
    setBusy(true);
    setError(undefined);
    try {
      await onCreate({ name, base }, start === '' ? undefined : start);
    } catch (failure) {
      if (isAuthFailure(failure)) {
        onAuthFailure();
        return;
      }
      setError(createProblem(failure, name));
      setBusy(false);
    }
  }

  return (
    <NewWorktreeSheet
      view={view}
      name={name}
      chosenBase={chosenBase}
      start={start}
      busy={busy}
      error={error}
      onName={(value) => { setName(value); setError(undefined); }}
      onShuffle={() => { setName(randomName()); setError(undefined); }}
      onBase={setBase}
      onStart={setStart}
      onRetry={onRetry}
      onCreate={(base) => { void create(base); }}
      onClose={onClose}
    />
  );
}

/**
 * The Remove worktree step of the session menu. `removal` is the reply to a
 * remove sent from this menu, if any. The branch is always kept.
 */
export function RemoveWorktree({ worktree, removal, onRemove, onCancel }: {
  readonly worktree: SessionWorktree;
  readonly removal: WorktreeRemoval | undefined;
  readonly onRemove: (force: boolean) => void;
  readonly onCancel: () => void;
}) {
  const branch = <code>termivoa/{worktree.name}</code>;
  if (removal?.state === 'pending') {
    return <p role="status">Removing the worktree…</p>;
  }
  if (removal?.state === 'error') {
    return (
      <div className="stack">
        <p role="alert" className="error">{removal.message}</p>
        <div className="row"><button type="button" onClick={onCancel}>Back</button></div>
      </div>
    );
  }
  switch (removal?.removed?.state) {
    case 'removed':
      return <p role="status">Worktree removed. The branch {branch} is kept.</p>;
    case 'dirty':
      return (
        <div role="alertdialog" aria-labelledby="worktree-dirty" className="confirm stack">
          <p id="worktree-dirty">It has uncommitted changes or ignored files, such as <code>node_modules</code> or <code>.env</code>. Remove anyway deletes them for good. The branch {branch} is kept.</p>
          {/* Its own key: a new pair, so the tap that asked for the first remove never lands on this one. */}
          <ConfirmPair key="anyway" cancel="Keep it" confirm="Remove anyway" onCancel={onCancel} onConfirm={() => onRemove(true)} />
        </div>
      );
    case 'failed':
      return (
        <div className="stack">
          <p role="alert" className="error">Could not remove the worktree.</p>
          {removal?.removed?.output !== undefined && <pre className="ship-output">{removal.removed.output}</pre>}
          <div className="row"><button type="button" onClick={onCancel}>Back</button></div>
        </div>
      );
    default:
      return (
        <div role="alertdialog" aria-labelledby="worktree-remove-warning" className="confirm stack">
          <p id="worktree-remove-warning">
            Remove worktree “{worktree.name}”? This deletes the folder <code>{worktreeFolder(worktree.repo, worktree.name)}</code> and
            ends this session. The branch {branch} is kept.
          </p>
          <ConfirmPair cancel="Keep it" confirm="Remove worktree" onCancel={onCancel} onConfirm={() => onRemove(false)} />
        </div>
      );
  }
}

/** "⎇ brave-otter": the session started in a Termivoa worktree. */
export function WorktreeBadge({ worktree }: { readonly worktree: SessionWorktree }) {
  return (
    <span className="worktree-badge" title={`Worktree ${worktree.name} of ${worktree.repo}`}>
      <span aria-hidden="true">⎇ </span><span className="visually-hidden">worktree </span>{worktree.name}
    </span>
  );
}
