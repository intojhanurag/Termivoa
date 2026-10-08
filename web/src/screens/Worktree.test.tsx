import { useState } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import type { BranchesListedPayload, BrokerRPCSessionRecord } from '../generated/protocol';
import type { BranchesView, WorktreeRemoval } from '../terminal/Connection';
import { SessionMenu } from './SessionMenu';
import {
  branchChoices,
  createProblem,
  MAX_WORKTREE_NAME,
  NewWorktreeSheet,
  randomName,
  RemoveWorktree,
  worktreeNameProblem,
} from './Worktree';

// useState as React's own, so a test can open the menu on one step (static markup cannot tap).
vi.mock('react', async (importOriginal) => {
  const react = await importOriginal<typeof import('react')>();
  return { ...react, useState: vi.fn(react.useState) };
});

const NAME = /^[a-z0-9]+(-[a-z0-9]+)*$/u;

const ready: BranchesListedPayload = {
  state: 'ready', repo: 'app', defaultBranch: 'main', current: 'feat/x', truncated: false,
  branches: [
    { name: 'feat/x', remote: false },
    { name: 'main', remote: false },
    { name: 'origin/main', remote: true },
  ],
};

const { defaultBranch: _default, current: _current, ...bare } = ready;

const sheet = (view: BranchesView | undefined, extra: { name?: string; error?: string; busy?: boolean } = {}) => renderToStaticMarkup(
  <NewWorktreeSheet
    view={view}
    name={extra.name ?? 'brave-otter'}
    chosenBase={undefined}
    start=""
    busy={extra.busy ?? false}
    error={extra.error}
    onName={() => undefined}
    onShuffle={() => undefined}
    onBase={() => undefined}
    onStart={() => undefined}
    onRetry={() => undefined}
    onCreate={() => undefined}
    onClose={() => undefined}
  />,
);

describe('worktree names', () => {
  it('makes valid adjective-noun names from the whole word lists', () => {
    expect(randomName(() => 0)).toBe('brave-badger');
    expect(randomName(() => 0.9999)).toBe('zesty-wren');
    const seen = new Set<string>();
    for (let step = 0; step < 1600; step++) {
      const picks = [Math.floor(step / 40) / 40, (step % 40) / 40];
      const name = randomName(() => picks.shift() ?? 0);
      expect(name).toMatch(NAME);
      expect(worktreeNameProblem(name)).toBeUndefined();
      seen.add(name);
    }
    expect(seen.size).toBe(1600);
  });

  it('checks a name with the schema rule', () => {
    for (const good of ['a', 'brave-otter', 'v2-fix-3', 'x'.repeat(MAX_WORKTREE_NAME)]) {
      expect(worktreeNameProblem(good)).toBeUndefined();
    }
    expect(worktreeNameProblem('')).toBe('Enter a name.');
    expect(worktreeNameProblem('x'.repeat(MAX_WORKTREE_NAME + 1))).toBe('Use 40 characters at most.');
    for (const bad of ['-a', 'a-', 'a--b', 'Brave', 'a_b', 'a b', 'a/b', 'ü']) {
      expect(worktreeNameProblem(bad)).toBe('Use lowercase letters and digits, with single hyphens between words.');
    }
  });
});

describe('branchChoices', () => {
  it('preselects the default branch, then keeps a listed choice', () => {
    expect(branchChoices(ready, undefined)).toEqual({ options: ready.branches, base: 'main' });
    expect(branchChoices(ready, 'origin/main').base).toBe('origin/main');
    expect(branchChoices(ready, 'gone').base).toBe('main');
  });

  it('falls back to the current branch, then the first, and adds a default cut off the list', () => {
    expect(branchChoices({ ...bare, current: 'main' }, undefined).base).toBe('main');
    expect(branchChoices(bare, undefined).base).toBe('feat/x');
    const cut = branchChoices({ ...ready, defaultBranch: 'trunk', truncated: true }, undefined);
    expect(cut.base).toBe('trunk');
    expect(cut.options[0]).toEqual({ name: 'trunk', remote: false });
    expect(branchChoices({ ...bare, branches: [] }, undefined))
      .toEqual({ options: [], base: undefined });
  });
});

describe('NewWorktreeSheet', () => {
  it('shows loading, unreadable and not-a-repository states', () => {
    expect(sheet(undefined)).toContain('<p role="status" class="muted">Reading branches…</p>');
    expect(sheet({ state: 'loading', listed: undefined })).toContain('Reading branches…');
    const views: BranchesView[] = [
      { state: 'unavailable', listed: undefined },
      { state: 'ready', listed: { state: 'unavailable', branches: [], truncated: false } },
    ];
    for (const view of views) {
      const html = sheet(view);
      expect(html).toContain('Could not read the branches.');
      expect(html).toContain('>Retry</button>');
      expect(html).not.toContain('<form');
    }
    const html = sheet({ state: 'ready', listed: { state: 'not_git', branches: [], truncated: false } });
    expect(html).toContain('<p role="alert">This folder is not in a git repository. cd into a repo first.</p>');
    expect(html).not.toContain('Create');
  });

  it('shows the form: name, base with local then remote and the current one marked, start with, and the folder', () => {
    const html = sheet({ state: 'ready', listed: ready });
    expect(html).toContain('role="dialog" aria-labelledby="worktree-title"');
    expect(html).toContain('Repository <strong>app</strong>');
    expect(html).toContain('<label for="worktree-name">name</label><input id="worktree-name" maxLength="40"');
    expect(html).toContain('value="brave-otter"');
    expect(html).toContain('aria-label="New random name"');
    expect(html).toMatch(/<select id="worktree-base">.*<optgroup label="Local"><option value="feat\/x">feat\/x · current<\/option>.*<option value="main" selected="">main<\/option><\/optgroup><optgroup label="Remote"><option value="origin\/main">origin\/main<\/option><\/optgroup>/su);
    expect(html).toContain('<option value="" selected="">Shell</option><option value="claude">Claude</option><option value="codex">Codex</option>');
    expect(html).toContain('Creates <code>~/termivoa/worktrees/app/brave-otter</code> on branch <code>termivoa/brave-otter</code>');
    expect(html).toContain('<button type="submit" class="primary">Create</button>');
    expect(html).not.toContain('role="alert"');
  });

  it('explains a bad name, an empty repository, a cut list, a failed create and a busy create', () => {
    const bad = sheet({ state: 'ready', listed: ready }, { name: 'a--b' });
    expect(bad).toContain('aria-invalid="true"');
    expect(bad).toContain('<p id="worktree-name-help" class="error">Use lowercase letters');
    expect(bad).toContain('<button type="submit" class="primary" disabled="">Create</button>');

    const empty = sheet({ state: 'ready', listed: { state: 'ready', repo: 'app', branches: [], truncated: false } });
    expect(empty).toContain('<select id="worktree-base" disabled=""><option>no branches</option></select>');
    expect(empty).toContain('This repository has no branches yet.');
    expect(empty).toContain('disabled="">Create</button>');

    expect(sheet({ state: 'ready', listed: { ...ready, truncated: true } })).toContain('Only the first 3 branches are listed.');
    const error = createProblem(new ApiError(409, 'worktree_exists', 'exists'), 'brave-otter');
    expect(sheet({ state: 'ready', listed: ready }, { error })).toContain(`<p role="alert" class="error">${error}</p>`);
    expect(sheet({ state: 'ready', listed: ready }, { busy: true })).toContain('disabled="">Creating…</button>');
  });
});

describe('createProblem', () => {
  it('does not confuse transport saturation with the repository worktree limit', () => {
    const text = createProblem(new ApiError(429, 'limit_exceeded', 'too many operations in progress; try again after one finishes'), 'a');
    expect(text).toContain('Wait for pending actions to finish');
    expect(text).not.toContain('This repository has too many');
    expect(createProblem(new ApiError(429, 'limit_exceeded', 'That computer could not do this.'), 'a')).toBe(text);
  });

  it('suggests a new name when it exists and explains the other refusals', () => {
    expect(createProblem(new ApiError(409, 'worktree_exists', 'exists'), 'brave-otter'))
      .toBe('“brave-otter” already exists in this repository. Pick another name, or tap 🎲 for a new one.');
    expect(createProblem(new ApiError(409, 'not_a_repository', 'no repo'), 'a'))
      .toBe('This folder is not in a git repository. cd into a repo first.');
    expect(createProblem(new ApiError(429, 'limit_exceeded', 'too many'), 'a'))
      .toBe('Termivoa reached a limit on this computer. Wait for pending actions to finish and check the session and worktree limits before trying again.');
    expect(createProblem(new ApiError(422, 'worktree_failed', 'git worktree add failed: husky - post-checkout failed'), 'a'))
      .toBe('git worktree add failed: husky - post-checkout failed. Nothing was left behind, so you can try again.');
    expect(createProblem(new ApiError(0, 'network', 'Connection unavailable'), 'a')).toBe('Connection unavailable');
  });
});

describe('RemoveWorktree', () => {
  const remove = (removal: WorktreeRemoval | undefined) => renderToStaticMarkup(
    <RemoveWorktree worktree={{ name: 'brave-otter', repo: 'app' }} removal={removal} onRemove={() => undefined} onCancel={() => undefined} />,
  );

  it('asks first, then shows each reply', () => {
    const ask = remove(undefined);
    expect(ask).toContain('role="alertdialog"');
    expect(ask).toContain('Remove worktree “brave-otter”? This deletes the folder <code>~/termivoa/worktrees/app/brave-otter</code>');
    expect(ask).toContain('<button type="button" class="danger">Remove worktree</button>');
    expect(remove({ state: 'pending' })).toBe('<p role="status">Removing the worktree…</p>');
    expect(remove({ state: 'done', removed: { state: 'removed' } }))
      .toBe('<p role="status">Worktree removed. The branch <code>termivoa/brave-otter</code> is kept.</p>');
    const dirty = remove({ state: 'done', removed: { state: 'dirty' } });
    expect(dirty).toContain('It has uncommitted changes or ignored files');
    expect(dirty).toContain('<button type="button" class="danger">Remove anyway</button>');
    const failed = remove({ state: 'done', removed: { state: 'failed', output: 'fatal: <locked>' } });
    expect(failed).toContain('<p role="alert" class="error">Could not remove the worktree.</p>');
    expect(failed).toContain('<pre class="ship-output">fatal: &lt;locked&gt;</pre>');
    expect(remove({ state: 'error', message: 'busy' })).toContain('<p role="alert" class="error">busy</p>');
  });
});

describe('session menu', () => {
  const session: BrokerRPCSessionRecord = {
    id: '84444444-4444-4444-8444-444444444444',
    generation: '85555555-5555-4555-8555-555555555555',
    title: 'brave-otter',
    projectId: '81111111-1111-4111-8111-111111111111',
    shellProfileId: '82222222-2222-4222-8222-222222222222',
    state: 'running',
    columns: 80,
    rows: 24,
    createdAt: '2026-09-27T10:00:00Z',
    updatedAt: '2026-09-27T10:00:00Z',
  };
  const menu = (record: BrokerRPCSessionRecord, controlling: boolean, running: boolean) => renderToStaticMarkup(
    <SessionMenu
      session={record}
      controlling={controlling}
      running={running}
      removal={undefined}
      onRelease={() => undefined}
      onNewWorktree={() => undefined}
      onRemoveWorktree={() => false}
      onSessions={() => undefined}
      onSettings={() => undefined}
      onChanged={async () => undefined}
      onAuthFailure={() => undefined}
      onClose={() => undefined}
    />,
  );
  const worktree = { ...session, worktree: { name: 'brave-otter', repo: 'app' } };

  it('offers New worktree only while the session runs', () => {
    expect(menu(session, false, true)).toContain('>New worktree…</button>');
    expect(menu(session, true, false)).not.toContain('New worktree');
  });

  it('says "Saving…" and "Terminating…" while rename and terminate run, so a slow computer is not a dead tap', () => {
    // The menu's first state is its step.
    vi.mocked(useState).mockImplementationOnce(() => ['rename', () => undefined]);
    expect(menu(session, true, true)).toMatch(/<button type="submit" class="primary"><span class="btn-swap"><span>Save title<\/span><span class="off" aria-hidden="true">.*?Saving…<\/span><\/span><\/button>/u);
    vi.mocked(useState).mockImplementationOnce(() => ['terminate', () => undefined]);
    expect(menu(session, true, true)).toMatch(/<button type="button" class="danger"><span class="btn-swap"><span>Terminate session<\/span><span class="off" aria-hidden="true">.*?Terminating…<\/span><\/span><\/button>/u);
  });

  it('offers Remove worktree only for a worktree session this phone controls', () => {
    expect(menu(worktree, true, true)).toContain('<button type="button" class="danger">Remove worktree…</button>');
    expect(menu(worktree, false, true)).not.toContain('Remove worktree');
    expect(menu(session, true, true)).not.toContain('Remove worktree');
  });
});
