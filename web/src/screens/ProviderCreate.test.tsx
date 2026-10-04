import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/http';
import { branchProblem, createFailure, CreateForm, createRequest, promptProblem, startNotice } from './ProviderCreate';
import { mac, providerEntry, ready, shopGroups } from './providerFixtures';

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

describe('answers', () => {
  it('words each refusal with fixed text, never the host\'s', () => {
    const say = (code: ConstructorParameters<typeof ApiError>[1]) => createFailure(new ApiError(409, code, 'git: /Users/x/secret exists'), 'fix', 'shop', 'herdr', 'mac');
    expect(say('worktree_exists')).toBe('“fix” already exists in shop. Pick another name.');
    expect(say('not_a_repository')).toBe('shop is not in git, so it cannot have worktrees.');
    expect(say('worktree_failed')).toBe('herdr could not make the worktree. Nothing was opened. Try again or pick another name.');
    expect(say('limit_exceeded')).toBe('Too many new worktrees just now. Wait a minute, then try again.');
    for (const code of ['worktree_exists', 'not_a_repository', 'worktree_failed', 'limit_exceeded', 'invalid_request', 'host_unavailable', 'session_missing'] as const) {
      expect(say(code)).not.toContain('secret');
    }
    expect(say('network')).toBeUndefined();
  });

  it('a worktree without a started agent is a notice, not an error', () => {
    expect(startNotice({ agentStarted: false, problem: 'agentFailed' })).toBe('The worktree is ready, but the agent did not start.');
    expect(startNotice({ agentStarted: true, problem: 'promptFailed' })).toBe('The agent started, but your first prompt was not sent.');
    expect(startNotice({ agentStarted: false, problem: 'agentNotReady' })).toMatch(/waits at a question/u);
    expect(startNotice({ agentStarted: false })).toBeUndefined();
  });
});

describe('the + sheet (H6)', () => {
  it('names the place, offers herdr\'s agent kinds and No agent, and says where the branch starts', () => {
    const page = form();
    expect(page).toContain('herdr · shop · on mac');
    expect(page).toContain('herdr makes the folder and a new branch from where shop is now.');
    expect(page).toContain('role="radio" aria-checked="true">claude</button>');
    expect(page).toContain('role="radio" aria-checked="false">codex</button>');
    expect(page).toContain('>No agent</button>');
    expect(page).toContain('Create and start');
    expect(page).not.toContain('disabled=""');
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
