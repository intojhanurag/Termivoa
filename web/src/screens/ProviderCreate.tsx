import { useRef, useState } from 'react';
import { ApiError, client } from '../api/http';
import type { HTTPProviderCreateRequest, HTTPProviderCreateResponse, HTTPProviderGroup } from '../generated/protocol';
import type { ProviderTap } from './Herdr';
import type { ProviderEntry } from './providers';
import { Sheet } from './Sheet';
import { Button, Notice } from './ui';

/*
 * The + of a project row (Paper H6, milestone M2): a new worktree and an agent
 * in it. Name and prompt are checked here with the schema's own rules before
 * anything is sent; the host checks them again.
 */

/** The schema's create `name`: lowercase parts joined by . _ - and up to 4 parts by /. */
const BRANCH = /^[a-z0-9]+([._-][a-z0-9]+)*(\/[a-z0-9]+([._-][a-z0-9]+)*)*$/u;
export const MAX_BRANCH = 64;
/** The schema's prompt: no control characters but tab and line feed (so no ESC). */
const PROMPT_CONTROL = /[\u0000-\u0008\u000B-\u001F\u007F-\u009F]/u;
export const MAX_PROMPT = 4000;

export function branchProblem(name: string): string | undefined {
  if (name === '') return 'Enter a branch name.';
  if ([...name].length > MAX_BRANCH) return `Use ${MAX_BRANCH} characters at most.`;
  if (!BRANCH.test(name)) return 'Use lowercase letters and digits, joined by - . _ or /, like fix/login-redirect.';
  if (name.endsWith('.lock')) return 'A branch name cannot end in .lock.';
  if (name.split('/').length > 4) return 'Use at most 4 parts between slashes.';
  return undefined;
}

export function promptProblem(prompt: string): string | undefined {
  if (PROMPT_CONTROL.test(prompt)) return 'Remove control characters (such as Esc) from the prompt.';
  // 4000 characters are at most 16000 UTF-8 bytes, the schema's other limit.
  if ([...prompt].length > MAX_PROMPT) return `Use ${MAX_PROMPT} characters at most.`;
  return undefined;
}

/** The request, or undefined while a field has a problem. A blank prompt, or one with no agent, is left out. */
export function createRequest(groupId: string, name: string, agent: string | undefined, prompt: string): HTTPProviderCreateRequest | undefined {
  if (branchProblem(name) !== undefined || (agent !== undefined && promptProblem(prompt) !== undefined)) return undefined;
  return {
    groupId,
    name,
    ...(agent === undefined ? {} : { agent }),
    ...(agent === undefined || prompt.trim() === '' ? {} : { prompt }),
  };
}

/** Fixed words for each refusal; never the host's, herdr's or git's text. */
export function createFailure(error: unknown, name: string, group: string, provider: string, label: string): string | undefined {
  switch (error instanceof ApiError ? error.code : undefined) {
    case 'worktree_exists': return `“${name}” already exists in ${group}. Pick another name.`;
    case 'not_a_repository': return `${group} is not in git, so it cannot have worktrees.`;
    case 'worktree_failed': return `${provider} could not make the worktree. Nothing was opened. Try again or pick another name.`;
    case 'limit_exceeded': return 'Too many new worktrees just now. Wait a minute, then try again.';
    case 'invalid_request': return `${provider} on ${label} refused this name or agent. Check them and try again.`;
    case 'host_unavailable': return `${provider} is not running on ${label}.`;
    case 'session_missing': return `${group} is gone from ${provider}. Close this and look again.`;
    default: return undefined;
  }
}

/** The worktree exists; the agent did not start, or the prompt did not go: a notice, not an error. */
export function startNotice(answer: Pick<HTTPProviderCreateResponse, 'agentStarted' | 'problem'>): string | undefined {
  switch (answer.problem) {
    case 'agentNotReady': return 'The agent started and waits at a question. Your prompt was not sent.';
    case 'agentFailed': return 'The worktree is ready, but the agent did not start.';
    case 'promptFailed': return 'The agent started, but your first prompt was not sent.';
    default: return undefined;
  }
}

/** The fields, the button and what went wrong. State comes from props, so each state renders on its own. */
export function CreateForm({ entry, group, name, agent, prompt, busy, error, onName, onAgent, onPrompt, onCreate, onClose }: {
  readonly entry: ProviderEntry;
  readonly group: HTTPProviderGroup;
  readonly name: string;
  /** undefined: No agent. */
  readonly agent: string | undefined;
  readonly prompt: string;
  readonly busy: boolean;
  readonly error: string | undefined;
  readonly onName: (name: string) => void;
  readonly onAgent: (agent: string | undefined) => void;
  readonly onPrompt: (prompt: string) => void;
  readonly onCreate: () => void;
  readonly onClose: () => void;
}) {
  const { info, computer } = entry;
  const nameProblem = name === '' ? undefined : branchProblem(name);
  const prompted = agent === undefined ? undefined : promptProblem(prompt);
  const ready = createRequest(group.id, name, agent, prompt) !== undefined;
  const kinds = [...info.agentKinds.map((kind) => ({ id: kind as string | undefined, label: kind })), { id: undefined, label: 'No agent' }];
  return (
    <form
      className="stack create-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready && !busy) onCreate();
      }}
    >
      <div className="sheet-header create-head">
        <div className="create-title">
          <p className="caps">{info.name} · {group.name} · on {computer.label}</p>
          <h2 id="create-title">New worktree</h2>
        </div>
        <button type="button" className="link" disabled={busy} onClick={onClose}>Close</button>
      </div>
      <div className="stack create-field">
        <label className="caps" htmlFor="create-name">Branch name</label>
        <input
          id="create-name"
          value={name}
          maxLength={MAX_BRANCH}
          placeholder="fix/login-redirect"
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
          spellCheck={false}
          aria-invalid={nameProblem !== undefined}
          aria-describedby="create-name-help"
          disabled={busy}
          onChange={(event) => onName(event.target.value.toLowerCase())}
        />
        <p id="create-name-help" className={nameProblem === undefined ? 'hint-text' : 'error hint-text'}>
          {nameProblem ?? `${info.name} makes the folder and a new branch from where ${group.name} is now.`}
        </p>
      </div>
      <div className="stack create-field">
        <p className="caps" id="create-agent">Start an agent in it</p>
        <div className="kind-chips" role="radiogroup" aria-labelledby="create-agent">
          {kinds.map((kind) => (
            <button
              key={kind.label}
              type="button"
              role="radio"
              aria-checked={agent === kind.id}
              disabled={busy}
              onClick={() => onAgent(kind.id)}
            >{kind.label}</button>
          ))}
        </div>
      </div>
      {agent !== undefined && (
        <div className="stack create-field">
          <label className="caps" htmlFor="create-prompt">First prompt (optional)</label>
          <textarea
            id="create-prompt"
            className="create-prompt"
            value={prompt}
            rows={3}
            placeholder="Tell the agent what to do…"
            aria-invalid={prompted !== undefined}
            aria-describedby="create-prompt-help"
            disabled={busy}
            onChange={(event) => onPrompt(event.target.value)}
          />
          {prompted !== undefined && <p id="create-prompt-help" role="alert" className="error hint-text">{prompted}</p>}
        </div>
      )}
      {error !== undefined && <Notice tone="danger" role="alert" title={error} />}
      <Button type="submit" className="primary wide" busy={busy} busyLabel="Creating…" disabled={!ready}>
        {agent === undefined ? 'Create' : 'Create and start'}
      </Button>
    </form>
  );
}

/** The sheet with its own state. `onCreated` opens the new terminal; a failure stays here so the name can change. */
export function ProviderCreateSheet({ entry, group, onCreated, onFailure, onClose, create = (body) => client(entry.computer.origin).providerCreate(entry.info.id, body) }: {
  readonly entry: ProviderEntry;
  readonly group: HTTPProviderGroup;
  readonly onCreated: (tap: ProviderTap, notice: string | undefined) => void;
  readonly onFailure: (error: unknown) => string;
  readonly onClose: () => void;
  readonly create?: (body: HTTPProviderCreateRequest) => Promise<HTTPProviderCreateResponse>;
}) {
  const [name, setName] = useState('');
  const [agent, setAgent] = useState<string | undefined>(entry.info.agentKinds[0]);
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // One create per tap, even if two taps land before the re-render.
  const running = useRef(false);

  function submit() {
    const body = createRequest(group.id, name, agent, prompt);
    if (body === undefined || running.current) return;
    running.current = true;
    setBusy(true);
    setError(undefined);
    create(body).then(
      (answer) => onCreated({ computer: entry.computer, provider: entry.info.id, targetId: answer.targetId }, startNotice(answer)),
      (failure: unknown) => {
        setError(createFailure(failure, name, group.name, entry.info.name, entry.computer.label) ?? onFailure(failure));
        setBusy(false);
        running.current = false;
      },
    );
  }

  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="create-title" onClose={() => { if (!running.current) onClose(); }}>
        <CreateForm
          entry={entry}
          group={group}
          name={name}
          agent={agent}
          prompt={prompt}
          busy={busy}
          error={error}
          onName={(value) => { setName(value); setError(undefined); }}
          onAgent={setAgent}
          onPrompt={setPrompt}
          onCreate={submit}
          onClose={() => { if (!running.current) onClose(); }}
        />
      </Sheet>
    </div>
  );
}
