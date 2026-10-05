import { useEffect, useRef, useState } from 'react';
import { ApiError, client, PEER_REQUEST_FAILED, PROVIDER_CREATE_TIMEOUT_MS } from '../api/http';
import type { Computer } from '../computers';
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

/**
 * Fixed words for each refusal; never herdr's or git's text, nor a peer's. `limit_exceeded` is this phone's create rate
 * or "the provider is busy with another change": the home computer's own fixed words say which (undefined here, so
 * they show); a peer's words are never shown, so its text is true for both.
 */
export function createFailure(error: unknown, name: string, group: string, provider: string, label: string): string | undefined {
  switch (error instanceof ApiError ? error.code : undefined) {
    case 'worktree_exists': return `“${name}” already exists in ${group}. Pick another name.`;
    case 'not_a_repository': return `${group} is not in git, so it cannot have worktrees.`;
    case 'worktree_failed': return `${provider} could not make the worktree. Nothing was opened. Try again or pick another name.`;
    case 'limit_exceeded': return error instanceof ApiError && error.message === PEER_REQUEST_FAILED ? `${provider} on ${label} is busy, or this phone asked for too many worktrees just now. Wait a minute, then try again.` : undefined;
    case 'invalid_request': return `${provider} on ${label} refused this name or agent. Check them and try again.`;
    case 'host_unavailable': return `${provider} is off or not running on ${label}.`;
    case 'session_missing': return `${group} is gone from ${provider}. Close this and look again.`;
    default: return undefined;
  }
}

/** A failed create's words: fixed text, else onFailure for that computer, so a peer's 401 says pair again and never sends the user to Setup. */
export function createError(
  failure: unknown, name: string, group: HTTPProviderGroup, entry: ProviderEntry, onFailure: (error: unknown, from: Computer) => string,
): string {
  return createFailure(failure, name, group.name, entry.info.name, entry.computer.label) ?? onFailure(failure, entry.computer);
}

/**
 * The agent to start: the picked kind while the computer still offers it (a fresh settings read can take it away),
 * else none. A picked agent never changes to another agent.
 */
export const offeredAgent = (picked: string | undefined, kinds: ReadonlyArray<string>): string | undefined =>
  picked !== undefined && kinds.includes(picked) ? picked : undefined;

/** Said when a fresh settings read took the picked agent away, so the change to no agent is not silent. */
export const droppedAgent = (picked: string | undefined, kinds: ReadonlyArray<string>, provider: string): string | undefined =>
  picked !== undefined && !kinds.includes(picked) ? `${picked} is not running in ${provider} now.` : undefined;

/** The worktree exists; the agent did not start, or the prompt did not go: a notice, not an error. `prompted`: a first prompt was sent with the request. */
export function startNotice(answer: { readonly agentStarted?: boolean; readonly problem?: HTTPProviderCreateResponse['problem'] | undefined }, prompted: boolean, made: 'worktree' | 'tab' = 'worktree'): string | undefined {
  switch (answer.problem) {
    case 'agentNotReady': return `The agent started and waits at a question.${prompted ? ' Your prompt was not sent.' : ''}`;
    case 'agentFailed': return `The ${made} is ready, but the agent did not start.`;
    case 'promptFailed': return 'The agent started, but your first prompt was not sent.';
    default: return undefined;
  }
}

/** Said in the sheet when a create took longer than the phone waits: the computer may still finish it. */
export const stillWorking = (label: string): string => `Still working on ${label}… It shows in the list when it is ready.`;

/**
 * One create, start to end. A late answer after the sheet is gone (the user left) does nothing, so it never pulls them into Live.
 * A network failure only after the whole wait is the phone's timeout: the computer may still make it, so it is not an error
 * of the computer and goes nowhere; the list is read again so the new worktree shows.
 * ponytail: the timeout is told from the time it took; a timeout code from the HTTP client would be exact.
 */
export async function submitCreate({ create, alive, now = Date.now, onCreated, onSlow, onFailed }: {
  readonly create: () => Promise<HTTPProviderCreateResponse>;
  readonly alive: () => boolean;
  readonly now?: () => number;
  readonly onCreated: (answer: HTTPProviderCreateResponse) => void;
  readonly onSlow: () => void;
  readonly onFailed: (failure: unknown) => void;
}): Promise<void> {
  const started = now();
  try {
    const answer = await create();
    if (alive()) onCreated(answer);
  } catch (failure) {
    if (!alive()) return;
    if (failure instanceof ApiError && failure.code === 'network' && now() - started >= PROVIDER_CREATE_TIMEOUT_MS) onSlow();
    else onFailed(failure);
  }
}

/** The first prompt of a new agent, with its check: in New worktree, and in New tab of the item menu (docs/herdr-manage.md, B1). */
export function PromptField({ prompt, busy, onPrompt }: { readonly prompt: string; readonly busy: boolean; readonly onPrompt: (prompt: string) => void }) {
  const problem = promptProblem(prompt);
  return (
    <div className="stack create-field">
      <label className="caps" htmlFor="create-prompt">First prompt (optional)</label>
      <textarea
        id="create-prompt"
        className="create-prompt"
        value={prompt}
        rows={3}
        placeholder="Tell the agent what to do…"
        aria-invalid={problem !== undefined}
        aria-describedby="create-prompt-help"
        disabled={busy}
        onChange={(event) => onPrompt(event.target.value)}
      />
      {problem !== undefined && <p id="create-prompt-help" role="alert" className="error field-note">{problem}</p>}
    </div>
  );
}

/** The fields, the button and what went wrong. State comes from props, so each state renders on its own. */
export function CreateForm({ entry, group, name, agent, dropped, prompt, busy, error, slow = false, onName, onAgent, onPrompt, onCreate, onClose }: {
  readonly entry: ProviderEntry;
  readonly group: HTTPProviderGroup;
  readonly name: string;
  /** undefined: No agent. */
  readonly agent: string | undefined;
  /** droppedAgent's words. */
  readonly dropped?: string | undefined;
  readonly prompt: string;
  readonly busy: boolean;
  readonly error: string | undefined;
  /** The create took longer than the phone waits (stillWorking). */
  readonly slow?: boolean;
  readonly onName: (name: string) => void;
  readonly onAgent: (agent: string | undefined) => void;
  readonly onPrompt: (prompt: string) => void;
  readonly onCreate: () => void;
  readonly onClose: () => void;
}) {
  const { info, computer } = entry;
  const nameProblem = name === '' ? undefined : branchProblem(name);
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
          <p className="caps"><bdi>{info.name}</bdi> · <bdi>{group.name}</bdi> · on {computer.label}</p>
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
        <p id="create-name-help" className={nameProblem === undefined ? 'hint-text' : 'error field-note'}>
          {nameProblem ?? <><bdi>{info.name}</bdi> makes the folder and a new branch from where <bdi>{group.name}</bdi> is now.</>}
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
            ><bdi>{kind.label}</bdi></button>
          ))}
        </div>
        {dropped !== undefined && <p role="status" className="hint-text">{dropped}</p>}
      </div>
      {agent !== undefined && <PromptField prompt={prompt} busy={busy} onPrompt={onPrompt} />}
      {slow && <p role="status" className="hint-text">{stillWorking(computer.label)}</p>}
      {error !== undefined && <Notice tone="danger" role="alert" title={error} />}
      <Button type="submit" className="primary wide" busy={busy} busyLabel="Creating…" disabled={!ready}>
        {agent === undefined ? 'Create' : 'Create and start'}
      </Button>
    </form>
  );
}

/** The sheet with its own state. `onCreated` opens the new terminal; a failure stays here so the name can change. */
export function ProviderCreateSheet({ entry, group, onCreated, onFailure, onReload, onKinds, onClose, create = (body) => client(entry.computer.origin).providerCreate(entry.info.id, body) }: {
  readonly entry: ProviderEntry;
  readonly group: HTTPProviderGroup;
  readonly onCreated: (tap: ProviderTap, notice: string | undefined) => void;
  /** Words a failure of this computer (a peer's 401 says pair again; it never sends the user to Setup). */
  readonly onFailure: (error: unknown, from: Computer) => string;
  /** Reads this provider again, so a slow create shows in the list. */
  readonly onReload?: () => void;
  /** Reads that computer's settings again, quietly: `entry` then has the agent kinds it offers now. */
  readonly onKinds?: (() => void) | undefined;
  readonly onClose: () => void;
  readonly create?: (body: HTTPProviderCreateRequest) => Promise<HTTPProviderCreateResponse>;
}) {
  const [name, setName] = useState('');
  const [picked, setAgent] = useState<string | undefined>(entry.info.agentKinds[0]);
  const agent = offeredAgent(picked, entry.info.agentKinds);
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [slow, setSlow] = useState(false);
  // One create per tap, even if two taps land before the re-render.
  const running = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  // The kinds are from the app's last settings read, and the computer offers a kind only while an agent of it runs:
  // read them again when the sheet opens.
  useEffect(() => { onKinds?.(); }, []);

  function submit() {
    const body = createRequest(group.id, name, agent, prompt);
    if (body === undefined || running.current) return;
    running.current = true;
    setBusy(true);
    setError(undefined);
    setSlow(false);
    const settle = () => { setBusy(false); running.current = false; };
    void submitCreate({
      create: () => create(body),
      alive: () => mounted.current,
      onCreated: (answer) => onCreated({ computer: entry.computer, provider: entry.info.id, targetId: answer.targetId }, startNotice(answer, body.prompt !== undefined)),
      onSlow: () => { setSlow(true); settle(); onReload?.(); },
      onFailed: (failure) => {
        setError(createError(failure, name, group, entry, onFailure));
        settle();
      },
    });
  }

  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="create-title" onClose={() => { if (!running.current) onClose(); }}>
        <CreateForm
          entry={entry}
          group={group}
          name={name}
          agent={agent}
          dropped={droppedAgent(picked, entry.info.agentKinds, entry.info.name)}
          prompt={prompt}
          busy={busy}
          error={error}
          slow={slow}
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
