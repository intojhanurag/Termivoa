import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { ApiError, PEER_REQUEST_FAILED, PROVIDER_CREATE_TIMEOUT_MS } from '../api/http';
import type { Computer } from '../computers';
import { branchProblem, createError, createFailure, CreateForm, createRequest, droppedAgent, offeredAgent, PromptField, promptProblem, startNotice, stillWorking, submitCreate } from './ProviderCreate';
import { group, item, mac, providerEntry, ready, shopGroups, target, vps } from './providerFixtures';

const noop = () => undefined;
const shop = shopGroups()[0]!;
const form = (extra: Partial<Parameters<typeof CreateForm>[0]> = {}) => renderToStaticMarkup(
  <CreateForm entry={ready(mac)} group={shop} name="fix/login-redirect" agent="claude" prompt="" busy={false} error={undefined}
    onName={noop} onAgent={noop} onPrompt={noop} onCreate={noop} onClose={noop} {...extra} />,
);

describe('branch name', () => {
  it('follows the schema: lowercase parts, at most 64 characters and 4 parts, not .lock', () => {
    for (const name of ['fix', 'fix/login-redirect', 'a.b_c-d/e', 'a/b/c/d']) expect(branchProblem(name), name).toBeUndefined();
    expect(branchProblem('')).toBe('Enter a branch name.');
    expect(branchProblem('x'.repeat(65))).toBe('Use 64 characters at most.');
    for (const name of ['Fix', 'fix//x', '-fix', 'fix-', 'fix login', 'fix\u001b[31m', 'a..b', '/fix', 'fix/']) {
      expect(branchProblem(name), name).toMatch(/lowercase letters/u);
    }
    expect(branchProblem('main.lock')).toBe('A branch name cannot end in .lock.');
    expect(branchProblem('a/b/c/d/e')).toBe('Use at most 4 parts between slashes.');
  });
});

describe('first prompt', () => {
  it('refuses ESC and other control characters, keeps tab and line feed', () => {
    expect(promptProblem('run the tests\n\tthen fix them')).toBeUndefined();
    for (const bad of ['\u001b[2J', 'a\u0000', 'a\r', 'a\u007f', 'a\u009b', 'a\u0008']) expect(promptProblem(bad), JSON.stringify(bad)).toMatch(/control characters/u);
    expect(promptProblem('x'.repeat(4001))).toBe('Use 4000 characters at most.');
  });

  it('is left out when blank or when no agent starts', () => {
    expect(createRequest('g1', 'fix', 'claude', 'go')).toEqual({ groupId: 'g1', name: 'fix', agent: 'claude', prompt: 'go' });
    expect(createRequest('g1', 'fix', 'claude', '  ')).toEqual({ groupId: 'g1', name: 'fix', agent: 'claude' });
    expect(createRequest('g1', 'fix', undefined, '\u001b')).toEqual({ groupId: 'g1', name: 'fix' });
    expect(createRequest('g1', 'Fix', 'claude', '')).toBeUndefined();
    expect(createRequest('g1', 'fix', 'claude', '\u001b')).toBeUndefined();
  });
});

describe('the prompt field (New worktree, and New tab of the item menu)', () => {
  const field = (prompt: string, busy = false) => renderToStaticMarkup(<PromptField prompt={prompt} busy={busy} onPrompt={() => undefined} />);

  it('is one labelled box; a prompt that breaks the rules says so by the box', () => {
    expect(field('Fix the test.\n\tThen stop.')).toContain('<label class="caps" for="create-prompt">First prompt (optional)</label>');
    expect(field('Fix the test.')).toContain('aria-invalid="false"');
    expect(field('Fix the test.')).not.toContain('role="alert"');
    expect(field('a\u001bb')).toContain('aria-invalid="true"');
    expect(field('a\u001bb')).toContain('<p id="create-prompt-help" role="alert" class="error field-note">Remove control characters (such as Esc) from the prompt.</p>');
    expect(field('', true)).toContain('disabled=""');
  });
});

describe('answers', () => {
  it('words each refusal with fixed text, never the host\'s', () => {
    const say = (code: ConstructorParameters<typeof ApiError>[1]) => createFailure(new ApiError(409, code, 'git: /Users/x/secret exists'), 'fix', 'shop', 'herdr', 'mac');
    expect(say('worktree_exists')).toBe('“fix” already exists in shop. Pick another name.');
    expect(say('not_a_repository')).toBe('shop is not in git, so it cannot have worktrees.');
    expect(say('worktree_failed')).toBe('herdr could not make the worktree. Nothing was opened. Try again or pick another name.');
    // The create rate or "herdr is busy": the home computer's own fixed words say which, so they show (createError).
    expect(createFailure(new ApiError(429, 'limit_exceeded', 'herdr is busy with another change; try again'), 'fix', 'shop', 'herdr', 'mac')).toBeUndefined();
    // A peer's words are never shown: one text that is true for both causes.
    expect(createFailure(new ApiError(429, 'limit_exceeded', PEER_REQUEST_FAILED), 'fix', 'shop', 'herdr', 'vps'))
      .toBe('herdr on vps is busy, or this phone asked for too many worktrees just now. Wait a minute, then try again.');
    for (const code of ['worktree_exists', 'not_a_repository', 'worktree_failed', 'invalid_request', 'host_unavailable', 'session_missing'] as const) {
      expect(say(code)).not.toContain('secret');
    }
    expect(say('network')).toBeUndefined();
  });

  it('a worktree without a started agent is a notice, not an error', () => {
    expect(startNotice({ agentStarted: false, problem: 'agentFailed' }, true)).toBe('The worktree is ready, but the agent did not start.');
    expect(startNotice({ agentStarted: true, problem: 'promptFailed' }, true)).toBe('The agent started, but your first prompt was not sent.');
    expect(startNotice({ agentStarted: false, problem: 'agentNotReady' }, true)).toBe('The agent started and waits at a question. Your prompt was not sent.');
    // No prompt was sent: it does not say that one was not sent.
    expect(startNotice({ agentStarted: false, problem: 'agentNotReady' }, false)).toBe('The agent started and waits at a question.');
    expect(startNotice({ agentStarted: false }, true)).toBeUndefined();
  });
});

describe('the + sheet (H6)', () => {
  it('names the place, offers herdr\'s agent kinds and No agent, and says where the branch starts', () => {
    const page = form();
    expect(page).toContain('<bdi>herdr</bdi> · <bdi>shop</bdi> · on mac');
    expect(page).toContain('<bdi>herdr</bdi> makes the folder and a new branch from where <bdi>shop</bdi> is now.');
    expect(page).toContain('role="radio" aria-checked="true"><bdi>claude</bdi></button>');
    expect(page).toContain('role="radio" aria-checked="false"><bdi>codex</bdi></button>');
    expect(page).toContain('><bdi>No agent</bdi></button>');
    expect(page).toContain('Create and start');
    expect(page).not.toContain('disabled=""');
  });

  it('a picked agent that left with a fresh settings read never changes to another agent: no agent, and a line says so (issue 195)', () => {
    expect(offeredAgent('codex', ['claude', 'codex'])).toBe('codex');
    expect(offeredAgent('codex', ['claude'])).toBeUndefined();
    expect(offeredAgent('codex', [])).toBeUndefined();
    expect(offeredAgent(undefined, ['claude'])).toBeUndefined();
    expect(droppedAgent('codex', ['claude'], 'herdr')).toBe('codex is not running in herdr now.');
    expect(droppedAgent('codex', ['claude', 'codex'], 'herdr')).toBeUndefined();
    // No agent is the user's choice: nothing to say.
    expect(droppedAgent(undefined, ['claude'], 'herdr')).toBeUndefined();
    const page = form({ agent: undefined, dropped: 'codex is not running in herdr now.' });
    expect(page).toContain('<p role="status" class="hint-text">codex is not running in herdr now.</p>');
    expect(page).toContain('role="radio" aria-checked="true"><bdi>No agent</bdi></button>');
    expect(page).toContain('<span>Create</span>');
    expect(form()).not.toContain('is not running in');
  });

  it('shows a field problem in the error colour, at the size of a hint (issue 200)', () => {
    expect(form({ name: 'Bad Name' })).toContain('<p id="create-name-help" class="error field-note">');
    expect(form({ name: '' })).toContain('<p id="create-name-help" class="hint-text">');
  });

  it('with no kinds from herdr offers only No agent and no prompt', () => {
    const page = form({ entry: providerEntry(mac, { kind: 'loading' }, { agentKinds: [] }), agent: undefined });
    expect(page.match(/role="radio"/gu)).toHaveLength(1);
    expect(page).not.toContain('create-prompt');
    expect(page).toContain('>Create<');
  });

  it('checks before sending: a bad name or an ESC in the prompt keeps the button off', () => {
    expect(form({ name: 'Fix Login' })).toMatch(/aria-invalid="true"/u);
    expect(form({ name: 'Fix Login' })).toMatch(/<button type="submit" class="primary wide" disabled=""/u);
    const escaped = form({ prompt: 'go\u001b[2J' });
    expect(escaped).toContain('Remove control characters (such as Esc) from the prompt.');
    expect(escaped).toMatch(/<button type="submit" class="primary wide" disabled=""/u);
  });

  it('while creating: busy, fields and Close off, so nothing is sent twice', () => {
    const page = form({ busy: true, error: undefined });
    expect(page).toContain('aria-busy="true"');
    expect(page).toContain('Creating…');
    expect(page).toMatch(/class="link" disabled="">Close/u);
  });

  it('shows a failure inside the sheet', () => {
    expect(form({ error: '“fix” already exists in shop. Pick another name.' })).toContain('role="alert"');
  });
});

describe('a create, start to end (submitCreate)', () => {
  const answer = { itemId: 'w10', targetId: 'w10:p1.term_f1', agentStarted: true };
  const run = (create: () => Promise<typeof answer>, alive = true, took = 0) => {
    const calls = { created: vi.fn(), slow: vi.fn(), failed: vi.fn() };
    let clock = 0;
    const done = submitCreate({
      create: () => { clock += took; return create(); },
      alive: () => alive,
      now: () => clock,
      onCreated: calls.created, onSlow: calls.slow, onFailed: calls.failed,
    });
    return { done, calls };
  };

  it('opens the new terminal when the sheet is still there', async () => {
    const { done, calls } = run(async () => answer);
    await done;
    expect(calls.created).toHaveBeenCalledWith(answer);
  });

  it('a create that ends after the user left the screen does not pull them into Live', async () => {
    const { done, calls } = run(async () => answer, false);
    await done;
    expect(calls.created).not.toHaveBeenCalled();
    const failed = run(async () => { throw new ApiError(409, 'worktree_exists', 'x'); }, false);
    await failed.done;
    expect(failed.calls.failed).not.toHaveBeenCalled();
  });

  it('past the 45 s wait: "Still working…" in the sheet, never the computer\'s failure (no "Can\'t reach", no Setup)', async () => {
    const { done, calls } = run(async () => { throw new ApiError(0, 'network', 'x'); }, true, PROVIDER_CREATE_TIMEOUT_MS);
    await done;
    expect(calls.slow).toHaveBeenCalledOnce();
    expect(calls.failed).not.toHaveBeenCalled();
    expect(stillWorking('mac')).toBe('Still working on mac… It shows in the list when it is ready.');
    expect(form({ slow: true })).toContain('<p role="status" class="hint-text">Still working on mac… It shows in the list when it is ready.</p>');
  });

  it('a quick network failure or a refusal is a failure', async () => {
    const quick = run(async () => { throw new ApiError(0, 'network', 'x'); }, true, 1_000);
    await quick.done;
    expect(quick.calls.failed).toHaveBeenCalledOnce();
    expect(quick.calls.slow).not.toHaveBeenCalled();
  });

  it('words a failure for the computer it came from, so a peer\'s 401 never counts as home\'s', () => {
    const seen: Array<Computer | undefined> = [];
    const onFailure = (_error: unknown, from: Computer) => { seen.push(from); return 'Pair vps again'; };
    const peer = providerEntry(vps, { kind: 'loading' });
    const shop = group('g1', 'shop', [item('w1', 'main', [target('w1:p1.a', 'idle')])]);
    expect(createError(new ApiError(401, 'unauthorized', 'x'), 'fix', shop, peer, onFailure)).toBe('Pair vps again');
    expect(seen).toEqual([vps]);
    expect(createError(new ApiError(409, 'worktree_exists', 'x'), 'fix', shop, peer, onFailure)).toBe('“fix” already exists in shop. Pick another name.');
    expect(seen).toHaveLength(1);
  });
});
