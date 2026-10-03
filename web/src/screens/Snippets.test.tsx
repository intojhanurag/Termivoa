import { isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { ProjectTask } from '../generated/protocol';
import type { TasksView } from '../terminal/Connection';
import { sendSnippet } from '../terminal/snippets';
import { ProjectTasks, Snippets, taskGroups, taskSnippet, typeHereProblem } from './Snippets';

const noop = () => undefined;
const task = (name: string, kind: ProjectTask['kind'], command = `pnpm ${name}`): ProjectTask =>
  ({ name, command, kind, source: 'package.json' });
const tasks = [
  task('lint', 'lint'),
  task('dev', 'dev'),
  task('fmt', 'other'),
  task('test', 'test', 'pnpm test'),
  task('build', 'build'),
  task('e2e', 'test'),
];
const ready = (list: ProjectTask[] = tasks): TasksView =>
  ({ state: 'ready', listed: { state: list.length === 0 ? 'none' : 'ready', tasks: list, folder: '/Users/me/app' } });

function section(props: Partial<Parameters<typeof ProjectTasks>[0]> = {}) {
  return (
    <ProjectTasks
      view={ready()}
      open={undefined}
      more={false}
      typeProblem={undefined}
      onOpen={noop}
      onMore={noop}
      onRetry={noop}
      onRun={noop}
      onType={noop}
      {...props}
    />
  );
}
const html = (props: Partial<Parameters<typeof ProjectTasks>[0]> = {}) => renderToStaticMarkup(section(props));

/** Host buttons by label or visible text; ProjectTasks has no hooks, so it is called directly. */
function buttons(node: ReactNode, found = new Map<string, () => void>()): Map<string, () => void> {
  if (Array.isArray(node)) {
    node.forEach((child: ReactNode) => buttons(child, found));
  } else if (isValidElement(node)) {
    const { props } = node as ReactElement<{ children?: ReactNode; onClick?: () => void; 'aria-label'?: string }>;
    if (typeof node.type === 'function') {
      buttons((node.type as (props: unknown) => ReactNode)(props), found);
    } else if (node.type === 'button' && props.onClick !== undefined) {
      found.set(props['aria-label'] ?? renderToStaticMarkup(<>{props.children}</>).replace(/<[^>]+>/gu, ''), props.onClick);
    }
    buttons(props.children, found);
  }
  return found;
}

describe('project commands', () => {
  it('groups tasks as test, dev, build, lint, then other, keeping the host order in a kind', () => {
    expect(taskGroups(tasks).map((group) => [group.kind, group.tasks.map((item) => item.name)])).toEqual([
      ['test', ['test', 'e2e']], ['dev', ['dev']], ['build', ['build']], ['lint', ['lint']], ['other', ['fmt']],
    ]);
    const markup = html();
    expect(markup.match(/<h4 class="caps">(\w+)<\/h4>/gu)?.join()).toBe(
      '<h4 class="caps">test</h4>,<h4 class="caps">dev</h4>,<h4 class="caps">build</h4>,<h4 class="caps">lint</h4>,<h4 class="caps">other</h4>',
    );
    expect(markup).toContain('<button type="button" class="task-chip"><span class="task-name">test</span><code class="task-command">pnpm test</code></button>');
  });

  it('folds many other tasks behind More', () => {
    const others = ['a', 'b', 'c', 'd', 'e'].map((name) => task(name, 'other'));
    const folded = html({ view: ready([task('test', 'test'), ...others]) });
    expect(folded).toContain('pnpm c');
    expect(folded).not.toContain('pnpm d');
    expect(folded).toContain('<button type="button" class="task-chip more">More · 2</button>');
    const onMore = vi.fn();
    buttons(section({ view: ready(others), onMore })).get('More · 2')?.();
    expect(onMore).toHaveBeenCalledOnce();
    const all = html({ view: ready(others), more: true });
    expect(all).toContain('pnpm e');
    expect(all).not.toContain('More');
  });

  it('says loading, none and unreadable in plain words, with Retry', () => {
    expect(html({ view: undefined })).toContain('<p role="status" class="muted">Looking for project commands…</p>');
    expect(html({ view: { state: 'loading', listed: undefined } })).toContain('Looking for project commands…');
    expect(html({ view: ready([]) })).toContain('<p role="status" class="muted">No project commands found in <code>~/app</code>.</p>');
    expect(html({ view: { state: 'ready', listed: { state: 'none', tasks: [] } } })).toContain('No project commands found in this folder.');

    const onRetry = vi.fn();
    for (const view of [{ state: 'unavailable', listed: undefined }, { state: 'ready', listed: { state: 'unavailable', tasks: [] } }] as TasksView[]) {
      expect(html({ view })).toContain('<p role="status" class="muted">Could not read the project.</p><button type="button">Retry</button>');
      buttons(section({ view, onRetry })).get('Retry')?.();
    }
    expect(onRetry).toHaveBeenCalledTimes(2);
  });

  it('opens a task, then runs it in a new tab or types it here', () => {
    const onOpen = vi.fn();
    buttons(section({ onOpen })).get('testpnpm test')?.();
    expect(onOpen).toHaveBeenCalledWith(tasks[3]);

    const open = tasks[3];
    const markup = html({ open });
    expect(markup).toContain('role="group" aria-label="Actions for test"');
    expect(markup).toContain('<code class="task-command">pnpm test</code><button type="button" class="primary">Run in new tab</button>');
    expect(markup).toContain('<button type="button">Type here</button>');
    expect(markup).not.toContain('task-chip');

    const onRun = vi.fn();
    const onType = vi.fn();
    const found = buttons(section({ open, onRun, onType, onOpen }));
    found.get('Run in new tab')?.();
    found.get('Type here')?.();
    found.get('Back to commands')?.();
    expect(onRun).toHaveBeenCalledWith(open);
    expect(onType).toHaveBeenCalledWith(open);
    expect(onOpen).toHaveBeenLastCalledWith(undefined);
  });

  it('turns Type here off with a one-line reason; Run in new tab stays', () => {
    // A plain shell at its prompt, with control: typing is fine.
    expect(typeHereProblem(true, undefined)).toBeUndefined();
    const fullscreen = 'A full-screen app, such as Claude Code, is open here. Use Run in new tab.';
    expect(typeHereProblem(true, 'fullscreen')).toBe(fullscreen);
    expect(typeHereProblem(false, 'fullscreen')).toBe(fullscreen);
    expect(typeHereProblem(true, 'running')).toBe('Something is running here. Use Run in new tab.');
    expect(typeHereProblem(false, undefined)).toBe('Take control to type here.');

    const markup = html({ open: tasks[3], typeProblem: typeHereProblem(true, 'fullscreen') });
    expect(markup).toContain('<button type="button" disabled="" aria-describedby="type-here-reason">Type here</button>');
    expect(markup).toContain(`<p id="type-here-reason" class="hint-text">${fullscreen}</p>`);
    expect(markup).toContain('<button type="button" class="primary">Run in new tab</button>');
  });

  it('turns Run in new tab off for a command that cannot be typed as one line', () => {
    const markup = html({ open: task('odd', 'other', 'make\tall') });
    expect(markup).toContain('<button type="button" class="primary" disabled="">Run in new tab</button>');
    expect(markup).toContain('Use one line without control characters.');
  });

  it('types a task as a paste, then Enter', () => {
    const target = { sendPaste: vi.fn(() => true), sendKey: vi.fn(() => true) };
    expect(sendSnippet(target, taskSnippet(tasks[3] as ProjectTask), true, false)).toBe('sent');
    expect(target.sendPaste).toHaveBeenCalledWith('pnpm test');
    expect(target.sendKey).toHaveBeenCalledWith('enter', expect.objectContaining({ ctrl: false }));
    expect(sendSnippet(target, taskSnippet(tasks[3] as ProjectTask), false, false)).toBe('disabled');
  });

  it('shows the project section above the saved snippets', () => {
    const markup = renderToStaticMarkup(
      <Snippets
        target={{ sendPaste: () => true, sendKey: () => true }}
        canSend
        busy={undefined}
        tasks={ready()}
        onRetryTasks={noop}
        onRunTask={noop}
        onClose={noop}
      />,
    );
    const project = markup.indexOf('This project');
    const saved = markup.indexOf('Saved snippets');
    expect(project).toBeGreaterThan(0);
    expect(saved).toBeGreaterThan(project);
    expect(markup.indexOf('Run tests and fix failures')).toBeGreaterThan(saved);
  });

  it('says to take control in a neutral notice with its icon', () => {
    const markup = renderToStaticMarkup(
      <Snippets target={{ sendPaste: () => true, sendKey: () => true }} canSend={false} busy={undefined} tasks={ready()} onRetryTasks={noop} onRunTask={noop} onClose={noop} />,
    );
    expect(markup).toMatch(/<div class="notice neutral" role="status"><span class="notice-icon">.*Take control to send snippets/u);
  });
});
