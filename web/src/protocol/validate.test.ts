import { describe, expect, it } from 'vitest';
import { InvalidProtocolMessageError, parseHostMessage } from './validate';

const requestId = '00000000-0000-4000-8000-000000000001';
const brokerGeneration = '00000000-0000-4000-8000-000000000002';

function welcome() {
  return {
    version: 1,
    type: 'welcome',
    requestId,
    payload: {
      brokerGeneration,
      negotiatedVersion: 1,
      capabilityProfile: 'phone-terminal-v1',
      hostBuild: 'test',
      limits: {
        inboundMessageBytes: 262144,
        outboundMessageBytes: 1048576,
        outgoingQueueBytes: 2097152,
        maxTextBytes: 65536,
        maxPasteBytes: 65536,
        minColumns: 20,
        maxColumns: 400,
        minRows: 5,
        maxRows: 200,
        maxHistoryPageRows: 200,
      },
    },
  };
}

describe('parseHostMessage', () => {
  it('returns a schema-validated host message', () => {
    const message = parseHostMessage(JSON.stringify(welcome()));
    expect(message.type).toBe('welcome');
  });

  it('accepts session.state with a waiting agent question', () => {
    const message = parseHostMessage(JSON.stringify({
      version: 1,
      type: 'session.state',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: {
        state: 'running',
        agent: {
          state: 'waiting',
          stateSince: '2026-09-27T10:00:00.000Z',
          question: {
            id: '0123456789abcdef0123456789abcdef',
            text: 'Do you want to proceed?',
            detail: 'Bash command\nnpm test -- --watch=false',
            options: [{ label: 'Yes' }, { label: 'No' }],
          },
        },
      },
    }));
    expect(message.type === 'session.state' && message.payload.agent?.question?.options).toHaveLength(2);
  });

  it('accepts afterWork as a boolean only', () => {
    const idle = (afterWork: unknown) => JSON.stringify({
      version: 1,
      type: 'session.state',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: { state: 'running', agent: { state: 'idle', stateSince: '2026-09-27T10:00:00.000Z', afterWork } },
    });
    const message = parseHostMessage(idle(true));
    expect(message.type === 'session.state' && message.payload.agent?.afterWork).toBe(true);
    expect(() => parseHostMessage(idle('yes'))).toThrow(InvalidProtocolMessageError);
  });

  it('rejects an agent detail with control characters or blank lines', () => {
    for (const detail of ['rm \u001b[31m', 'a\n\nb', 'a \nb', '\nb']) {
      expect(() => parseHostMessage(JSON.stringify({
        version: 1,
        type: 'session.state',
        requestId,
        sessionId: brokerGeneration,
        generation: brokerGeneration,
        payload: {
          state: 'running',
          agent: {
            state: 'waiting',
            stateSince: '2026-09-27T10:00:00.000Z',
            question: { id: '0123456789abcdef0123456789abcdef', text: 'Proceed?', detail, options: [{ label: 'Yes' }] },
          },
        },
      }))).toThrow(InvalidProtocolMessageError);
    }
  });

  it('accepts changes.summary and changes.patch', () => {
    const summary = parseHostMessage(JSON.stringify({
      version: 1,
      type: 'changes.summary',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: {
        state: 'ready',
        root: '/Users/me/app',
        branch: 'main',
        files: [{ path: 'src/b.ts', oldPath: 'src/a.ts', status: 'renamed', additions: 2, deletions: 1, binary: false }],
        truncated: false,
      },
    }));
    expect(summary.type === 'changes.summary' && summary.payload.files[0]?.status).toBe('renamed');

    const patch = parseHostMessage(JSON.stringify({
      version: 1,
      type: 'changes.patch',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: {
        path: 'src/b.ts',
        state: 'ready',
        lines: [
          { kind: 'hunk', text: '@@ -1,2 +1,2 @@' },
          { kind: 'remove', text: 'old', oldLine: 1 },
          { kind: 'add', text: 'new', newLine: 1 },
          { kind: 'context', text: 'same', oldLine: 2, newLine: 2 },
        ],
        truncated: true,
      },
    }));
    expect(patch.type === 'changes.patch' && patch.payload.lines.map((line) => line.kind))
      .toEqual(['hunk', 'remove', 'add', 'context']);
  });

  it('rejects a changes reply outside the contract', () => {
    const patch = (lines: unknown[]) => JSON.stringify({
      version: 1,
      type: 'changes.patch',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: { path: 'a', state: 'ready', lines, truncated: false },
    });
    expect(() => parseHostMessage(patch([{ kind: 'delete', text: 'x' }]))).toThrow(InvalidProtocolMessageError);
    expect(() => parseHostMessage(patch([{ kind: 'add', text: 'x', newLine: 0 }]))).toThrow(InvalidProtocolMessageError);
    expect(() => parseHostMessage(patch(Array.from({ length: 2001 }, () => ({ kind: 'add', text: '' })))))
      .toThrow(InvalidProtocolMessageError);
    expect(() => parseHostMessage(JSON.stringify({
      version: 1,
      type: 'changes.list',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: {},
    }))).toThrow('not host-to-client');
  });

  it('accepts changes.committed and changes.pushed within the contract', () => {
    const reply = (type: string, payload: object, extra: object = {}) => JSON.stringify({
      version: 1, type, requestId, sessionId: brokerGeneration, generation: brokerGeneration, payload, ...extra,
    });
    const committed = parseHostMessage(reply('changes.committed', { state: 'committed', commit: 'abc1234' }));
    expect(committed.type === 'changes.committed' && committed.payload.commit).toBe('abc1234');
    expect(parseHostMessage(reply('changes.committed', { state: 'failed', output: 'hook failed' })).type).toBe('changes.committed');
    const pushed = parseHostMessage(reply('changes.pushed', { state: 'pushed', branch: 'main', url: 'https://github.com/o/r/pull/new/main' }));
    expect(pushed.type === 'changes.pushed' && pushed.payload.url).toBe('https://github.com/o/r/pull/new/main');
    expect(parseHostMessage(reply('changes.pushed', { state: 'no_remote' })).type).toBe('changes.pushed');

    for (const bad of [
      reply('changes.committed', { state: 'done' }),
      reply('changes.committed', { state: 'committed', commit: 'ABC1234' }),
      reply('changes.committed', { state: 'failed', output: 'x'.repeat(2001) }),
      reply('changes.pushed', { state: 'pushed', url: 'http://example.com/pull' }),
      reply('changes.pushed', { state: 'pushed', url: 'javascript:alert(1)' }),
      reply('changes.pushed', { state: 'pushed', force: true }),
    ]) {
      expect(() => parseHostMessage(bad)).toThrow(InvalidProtocolMessageError);
    }
    for (const type of ['changes.commit', 'changes.push']) {
      expect(() => parseHostMessage(reply(type, { lease: 'L'.repeat(43), message: 'm', paths: ['a'] }))).toThrow(InvalidProtocolMessageError);
    }
    expect(() => parseHostMessage(reply('changes.push', { lease: 'L'.repeat(43) }))).toThrow('not host-to-client');
  });

  it('accepts tasks.listed within the contract and refuses tasks.list from the host', () => {
    const reply = (type: string, payload: object) => JSON.stringify({
      version: 1, type, requestId, sessionId: brokerGeneration, generation: brokerGeneration, payload,
    });
    const task = { name: 'test', command: 'pnpm test', source: 'package.json', kind: 'test' };
    const listed = parseHostMessage(reply('tasks.listed', { state: 'ready', folder: '/Users/me/app', tasks: [task] }));
    expect(listed.type === 'tasks.listed' && listed.payload.tasks[0]?.command).toBe('pnpm test');
    expect(parseHostMessage(reply('tasks.listed', { state: 'none', tasks: [] })).type).toBe('tasks.listed');
    expect(parseHostMessage(reply('tasks.listed', { state: 'unavailable', tasks: [] })).type).toBe('tasks.listed');

    for (const bad of [
      reply('tasks.listed', { state: 'done', tasks: [] }),
      reply('tasks.listed', { state: 'ready' }),
      reply('tasks.listed', { state: 'ready', tasks: [{ ...task, kind: 'deploy' }] }),
      reply('tasks.listed', { state: 'ready', tasks: [{ ...task, source: 'pyproject.toml' }] }),
      reply('tasks.listed', { state: 'ready', tasks: [{ ...task, command: '' }] }),
      reply('tasks.listed', { state: 'ready', tasks: [{ ...task, command: 'x'.repeat(513) }] }),
      reply('tasks.listed', { state: 'ready', tasks: [{ ...task, run: true }] }),
      reply('tasks.listed', { state: 'ready', tasks: Array.from({ length: 51 }, () => task) }),
    ]) {
      expect(() => parseHostMessage(bad)).toThrow(InvalidProtocolMessageError);
    }
    expect(() => parseHostMessage(reply('tasks.list', {}))).toThrow('not host-to-client');
  });

  it('accepts branches.listed and worktree.removed within the contract and refuses their requests from the host', () => {
    const reply = (type: string, payload: object) => JSON.stringify({
      version: 1, type, requestId, sessionId: brokerGeneration, generation: brokerGeneration, payload,
    });
    const ready = {
      state: 'ready', repo: 'termivoa', defaultBranch: 'main', current: 'feat/x', truncated: false,
      branches: [{ name: 'main', remote: false }, { name: 'origin/main', remote: true }],
    };
    const listed = parseHostMessage(reply('branches.listed', ready));
    expect(listed.type === 'branches.listed' && listed.payload.branches[1]?.remote).toBe(true);
    expect(parseHostMessage(reply('branches.listed', { state: 'not_git', branches: [], truncated: false })).type).toBe('branches.listed');
    expect(parseHostMessage(reply('worktree.removed', { state: 'dirty' })).type).toBe('worktree.removed');
    expect(parseHostMessage(reply('worktree.removed', { state: 'failed', output: 'fatal: locked' })).type).toBe('worktree.removed');

    for (const bad of [
      reply('branches.listed', { state: 'ready', branches: [] }),
      reply('branches.listed', { ...ready, branches: [{ name: '', remote: false }] }),
      reply('branches.listed', { ...ready, branches: Array.from({ length: 201 }, () => ({ name: 'a', remote: false })) }),
      reply('worktree.removed', { state: 'gone' }),
      reply('worktree.removed', { state: 'failed', output: 'x'.repeat(2001) }),
    ]) {
      expect(() => parseHostMessage(bad)).toThrow(InvalidProtocolMessageError);
    }
    expect(() => parseHostMessage(reply('branches.list', {}))).toThrow('not host-to-client');
    expect(() => parseHostMessage(reply('worktree.remove', { lease: 'L'.repeat(43), force: false }))).toThrow('not host-to-client');
  });

  it('accepts files.listed, files.content and files.found within the contract and refuses their requests from the host', () => {
    const reply = (type: string, payload: object) => JSON.stringify({
      version: 1, type, requestId, sessionId: brokerGeneration, generation: brokerGeneration, payload,
    });
    const listed = { state: 'ready', root: '/Users/me/app', path: 'src', entries: [{ name: 'a', kind: 'dir' }, { name: 'b.ts', kind: 'file', size: 12 }, { name: 'l', kind: 'link' }], truncated: false };
    const parsed = parseHostMessage(reply('files.listed', listed));
    expect(parsed.type === 'files.listed' && parsed.payload.entries[1]?.size).toBe(12);
    expect(parseHostMessage(reply('files.listed', { state: 'not_found', path: 'gone', entries: [], truncated: false })).type).toBe('files.listed');
    expect(parseHostMessage(reply('files.content', { state: 'ready', path: 'a.ts', size: 3, text: 'abc', truncated: false })).type).toBe('files.content');
    expect(parseHostMessage(reply('files.content', { state: 'binary', path: 'a.png', size: 3, truncated: false })).type).toBe('files.content');
    expect(parseHostMessage(reply('files.found', { state: 'ready', query: 'q', paths: Array.from({ length: 200 }, () => 'a.ts'), truncated: true })).type).toBe('files.found');
    expect(parseHostMessage(reply('files.found', { state: 'unavailable', query: 'q', paths: [], truncated: false })).type).toBe('files.found');

    for (const bad of [
      reply('files.listed', { ...listed, entries: [{ name: 'a', kind: 'socket' }] }),
      reply('files.listed', { ...listed, state: 'gone' }),
      reply('files.content', { state: 'ready', path: '', text: 'abc', truncated: false }),
      reply('files.content', { state: 'ready', path: 'a.ts', truncated: false, extra: 1 }),
      reply('files.found', { state: 'ready', query: 'q', paths: Array.from({ length: 201 }, () => 'a.ts'), truncated: true }),
      reply('files.found', { state: 'ready', query: '', paths: [], truncated: false }),
    ]) {
      expect(() => parseHostMessage(bad)).toThrow(InvalidProtocolMessageError);
    }
    expect(() => parseHostMessage(reply('files.list', { path: '' }))).toThrow('not host-to-client');
    expect(() => parseHostMessage(reply('files.read', { path: 'a.ts' }))).toThrow('not host-to-client');
    expect(() => parseHostMessage(reply('files.search', { query: 'q' }))).toThrow('not host-to-client');
  });

  it('rejects client-to-host messages even when their schema is valid', () => {
    expect(() => parseHostMessage(JSON.stringify({
      version: 1,
      type: 'hello',
      requestId,
      payload: {
        clientBuild: 'test',
        clientKind: 'browser',
        supportedVersions: [1],
        capabilityProfile: 'phone-terminal-v1',
      },
    }))).toThrow('not host-to-client');
  });

  it('rejects malformed JSON and additional properties', () => {
    expect(() => parseHostMessage('{')).toThrow('not valid JSON');
    expect(() => parseHostMessage(JSON.stringify({ ...welcome(), unexpected: true })))
      .toThrow(InvalidProtocolMessageError);
  });

  it('rejects non-lowercase canonical UUID spelling', () => {
    expect(() => parseHostMessage(JSON.stringify({
      ...welcome(),
      requestId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA',
    }))).toThrow(InvalidProtocolMessageError);
  });

  it('rejects messages beyond the host byte limit before parsing', () => {
    expect(() => parseHostMessage(`{"padding":"${'x'.repeat(1024 * 1024)}"}`))
      .toThrow('outside');
  });
});
