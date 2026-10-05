import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import type { HTTPProviderManageResponse } from '../generated/protocol';
import { ADD_FOLDER_COMMAND } from './providers';
import { addFolderStep, NO_SESSION, CHANGED, closeWorktreesFirst, CONFIRM_AFTER_MS, confirmReady, ItemMenu, itemRequest, LAST_TAB, manageStep, StartButton, WorkspaceList, type ManageNote } from './ProviderManage';
import { group, item, mac, ready, settingsWith, shopGroups, target, vps } from './providerFixtures';

const noop = () => undefined;
const main = item('w1', 'main', [target('w1:p1.t', 'idle')]);
const linked = item('w8', 'silver-meadow', [target('w8:p1.t', 'idle')], { linked: true });
const shop = group('g1', 'shop', [main, linked]);

describe('one manage action', () => {
  const answer = (body: HTTPProviderManageResponse) => vi.fn(async () => body);
  const refuse = (code: ApiError['code'], status = 400) => vi.fn(async () => { throw new ApiError(status, code, 'The computer\'s own words.'); });
  const words = (error: unknown) => (error instanceof Error ? error.message : 'never');

  it('sends the item, and for close and remove also the name the user confirmed', () => {
    expect(itemRequest('newTab', linked)).toEqual({ action: 'newTab', itemId: 'w8' });
    expect(itemRequest('closeItem', linked)).toEqual({ action: 'closeItem', itemId: 'w8', name: 'silver-meadow' });
    expect(itemRequest('removeItem', linked)).toEqual({ action: 'removeItem', itemId: 'w8', name: 'silver-meadow' });
  });

  it('sends the item and the terminal for Close tab, and no name', () => {
    expect(itemRequest({ closeTab: 'w8:p1.t' }, linked)).toEqual({ action: 'closeTab', itemId: 'w8', targetId: 'w8:p1.t' });
  });

  it('reads the list again after a tab closed, and is done', async () => {
    const reload = vi.fn(async () => undefined);
    await expect(manageStep(itemRequest({ closeTab: 'w8:p1.t' }, linked), answer({ itemId: 'w8' }), reload, words)).resolves.toEqual({ kind: 'done' });
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('says the last tab in its own words (nothing changed, nothing read); a terminal that left is "the list changed"', async () => {
    const reload = vi.fn(async () => undefined);
    await expect(manageStep(itemRequest({ closeTab: 'w8:p1.t' }, linked), refuse('operation_conflict', 409), reload, words))
      .resolves.toEqual({ kind: 'note', note: { kind: 'error', text: 'This is the workspace\'s last tab. Close the workspace.' } });
    expect(LAST_TAB).toBe('This is the workspace\'s last tab. Close the workspace.');
    expect(reload).not.toHaveBeenCalled();
    await expect(manageStep(itemRequest({ closeTab: 'w8:p1.t' }, linked), refuse('session_missing', 404), reload, words)).resolves.toEqual({ kind: 'changed' });
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('opens a new terminal as a tap does; the caller reads the list with the move', async () => {
    const reload = vi.fn(async () => undefined);
    await expect(manageStep({ action: 'newTab', itemId: 'w8' }, answer({ itemId: 'w8', targetId: 'w8:p2.term_9' }), reload, words)).resolves.toEqual({ kind: 'open', targetId: 'w8:p2.term_9' });
    expect(reload).not.toHaveBeenCalled();
  });

  it('reads the list again at once after a success, before it says anything', async () => {
    const order: string[] = [];
    const reload = async () => { order.push('reload'); };
    const made = (action: 'newTab' | 'newWorkspace' | 'closeItem' | 'removeItem' | 'startServer') =>
      manageStep({ action }, async () => { order.push(action); return { itemId: 'w8' }; }, reload, words);
    await expect(made('newTab')).resolves.toEqual({ kind: 'note', note: { kind: 'made', what: 'tab' } });
    await expect(made('newWorkspace')).resolves.toEqual({ kind: 'note', note: { kind: 'made', what: 'workspace' } });
    for (const action of ['closeItem', 'removeItem', 'startServer'] as const) await expect(made(action)).resolves.toEqual({ kind: 'done' });
    expect(order).toEqual(['newTab', 'reload', 'newWorkspace', 'reload', 'closeItem', 'reload', 'removeItem', 'reload', 'startServer', 'reload']);
  });

  it('changes nothing and reads nothing when the worktree has changes that are not committed', async () => {
    const reload = vi.fn(async () => undefined);
    await expect(manageStep(itemRequest('removeItem', linked), answer({ problem: 'uncommitted' }), reload, words)).resolves.toEqual({ kind: 'note', note: { kind: 'uncommitted' } });
    expect(reload).not.toHaveBeenCalled();
  });

  it('reads the list again and says it changed when the item left or was renamed', async () => {
    for (const code of ['operation_conflict', 'session_missing'] as const) {
      const reload = vi.fn(async () => undefined);
      await expect(manageStep(itemRequest('closeItem', linked), refuse(code, 409), reload, words)).resolves.toEqual({ kind: 'changed' });
      expect(reload).toHaveBeenCalledTimes(1);
    }
    expect(CHANGED).toBe('The list changed. Look again, then try again.');
  });

  it('shows the computer\'s own words for any other refusal, never a guessed text', async () => {
    const reload = vi.fn(async () => undefined);
    for (const code of ['invalid_request', 'host_unavailable', 'limit_exceeded', 'worktree_failed', 'project_missing', 'network'] as const) {
      await expect(manageStep({ action: 'startServer' }, refuse(code), reload, words)).resolves.toEqual({ kind: 'note', note: { kind: 'error', text: 'The computer\'s own words.' } });
    }
    expect(reload).not.toHaveBeenCalled();
  });

  it('asks to close a main checkout\'s worktrees first; a linked worktree and a lone item close at once', () => {
    expect(closeWorktreesFirst(shop, main)).toBe(true);
    expect(closeWorktreesFirst(shop, linked)).toBe(false);
    expect(closeWorktreesFirst(group('ws-w5', 'notes', [main]), main)).toBe(false);
  });

  it('follows the host\'s rule: two main checkouts close; a full group (64 items, the list may be cut) blocks a main checkout only', () => {
    const mains = (count: number) => Array.from({ length: count }, (_, index) => item(`m${index}`, 'main', []));
    expect(closeWorktreesFirst(group('g1', 'shop', mains(2)), main)).toBe(false);
    expect(closeWorktreesFirst(group('g1', 'shop', mains(63)), main)).toBe(false);
    expect(closeWorktreesFirst(group('g1', 'shop', mains(64)), main)).toBe(true);
    expect(closeWorktreesFirst(group('g1', 'shop', mains(64)), linked)).toBe(false);
  });

  it('takes no confirm tap for 400 ms after the confirmation shows (a double tap on the menu)', () => {
    expect(CONFIRM_AFTER_MS).toBe(400);
    expect(confirmReady(1_000, 1_000)).toBe(false);
    expect(confirmReady(1_000, 1_399)).toBe(false);
    expect(confirmReady(1_000, 1_400)).toBe(true);
  });
});

describe('the item menu', () => {
  const menu = (extra: Partial<Parameters<typeof ItemMenu>[0]> = {}) => renderToStaticMarkup(
    <ItemMenu entry={ready(vps)} group={shop} item={linked} mode="menu" busy={undefined} note={undefined} onMode={noop} onAction={noop} onClose={noop} {...extra} />,
  );

  it('names the item, its project and its computer, each provider name isolated (bdi)', () => {
    const page = menu();
    expect(page).toContain('<p class="caps"><bdi>herdr</bdi> · <bdi>shop</bdi> · on vps</p>');
    expect(page).toContain('<h2 id="item-menu-title"><bdi>silver-meadow</bdi></h2>');
    // A workspace outside git is named once.
    const notes = item('w5', 'notes', []);
    expect(menu({ group: group('ws-w5', 'notes', [notes]), item: notes })).toContain('<p class="caps"><bdi>herdr</bdi> · on vps</p>');
  });

  it('has New tab and Close workspace, and Remove worktree only for a linked worktree', () => {
    const page = menu();
    expect(page).toContain('New tab');
    expect(page).toContain('>Close workspace</button>');
    expect(page).toContain('>Remove worktree</button>');
    expect(page).not.toContain('Close its worktrees first.');
    expect(page).not.toContain('role="alertdialog"');
    const lone = menu({ group: group('ws-w5', 'main', [main]), item: main });
    expect(lone).not.toContain('Remove worktree');
    expect(lone).not.toContain('Close its worktrees first.');
  });

  it('turns Close workspace off for a main checkout with worktrees, and says why', () => {
    const page = menu({ item: main });
    expect(page).toContain('<button type="button" class="danger" disabled="" aria-describedby="close-first">Close workspace</button>');
    expect(page).toContain('<p id="close-first" class="hint-text">Close its worktrees first.</p>');
    expect(page).not.toContain('Remove worktree');
  });

  it('asks before a close, with what ends and what stays', () => {
    const page = menu({ mode: 'close' });
    expect(page).toContain('role="alertdialog"');
    expect(page).toContain('<p id="manage-confirm">Close <bdi>silver-meadow</bdi>? Its terminals end. Files stay.</p>');
    expect(page).toContain('Close workspace');
    expect(page).toContain('>Cancel</button>');
    expect(page).not.toContain('New tab');
  });

  it('puts Cancel first in every confirmation, and gives the confirmation (not a button) the focus target', () => {
    for (const mode of ['close', 'remove', { closeTab: 'w8:p1.t' }] as const) {
      const page = menu({ mode });
      expect(page).toContain('<div role="alertdialog" aria-labelledby="manage-confirm" tabindex="-1" class="confirm stack">');
      expect(page).toMatch(/<div class="row confirm-pair"><button type="button">Cancel<\/button><button type="button" class="danger">/u);
      expect(page.match(/<button/gu)).toHaveLength(3);
    }
  });

  it('asks before a remove, and says that ignored files go and the branch stays', () => {
    const page = menu({ mode: 'remove' });
    expect(page).toContain('<p id="manage-confirm">Remove the worktree <bdi>silver-meadow</bdi>? Its folder is deleted, also files that git ignores (such as .env). The branch stays.</p>');
    expect(page).toContain('Remove worktree');
    expect(page).toContain('>Cancel</button>');
  });

  describe('Tabs (Close tab)', () => {
    const three = shopGroups()[0]!.items[0]!;

    it('lists each terminal by its label, with its agent when it has one, names isolated', () => {
      const page = menu({ item: three });
      expect(page).toContain('<h3 id="item-tabs" class="caps">Tabs</h3>');
      expect(page).toContain('<span class="tab-row-name"><bdi>1</bdi> · <bdi>claude</bdi></span>');
      expect(page).toContain('<span class="tab-row-name"><bdi>2</bdi> · <bdi>codex</bdi></span>');
      expect(page).toContain('<span class="tab-row-name"><bdi>logs</bdi></span>');
      expect(page.match(/>Close tab<\/button>/gu)).toHaveLength(3);
      expect(page).not.toContain('Close the workspace to end its last tab.');
      // Under New tab, above Close workspace.
      expect(page.indexOf('New tab')).toBeLessThan(page.indexOf('item-tabs'));
      expect(page.indexOf('item-tabs')).toBeLessThan(page.indexOf('Close workspace'));
    });

    it('has no Close tab for an item with one terminal, and says what to do', () => {
      const page = menu();
      expect(page).toContain('<span class="tab-row-name"><bdi>1</bdi></span>');
      expect(page).not.toContain('Close tab');
      expect(page).toContain('<p class="hint-text">Close the workspace to end its last tab.</p>');
      expect(menu({ item: item('w5', 'notes', []) })).toContain('Close the workspace to end its last tab.');
    });

    it('asks before a tab closes, by its label; busy says Closing…', () => {
      const page = menu({ item: three, mode: { closeTab: 'w1:p3.term_a3' } });
      expect(page).toContain('<p id="manage-confirm">Close tab <bdi>logs</bdi>? Its terminals end.</p>');
      expect(page).toContain('>Close tab</span>');
      expect(page).not.toContain('New tab');
      const closing = menu({ item: three, mode: { closeTab: 'w1:p3.term_a3' }, busy: 'closeTab' });
      expect(closing).toContain('Closing…');
      expect(closing.match(/disabled=""/gu)).toHaveLength(3);
    });

    it('turns every Close tab off while another action runs', () => {
      // Close (the header), New tab, three Close tab, Close workspace.
      expect(menu({ item: three, busy: 'newTab' }).match(/disabled=""/gu)).toHaveLength(6);
    });
  });

  it('shows one action busy and every other button off, so a second tap sends nothing', () => {
    const tab = menu({ busy: 'newTab' });
    expect(tab.match(/aria-busy="true"/gu)).toHaveLength(1);
    expect(tab).toContain('Opening…');
    // Close (the header), New tab, Close workspace, Remove worktree.
    expect(tab.match(/disabled=""/gu)).toHaveLength(4);
    const closing = menu({ mode: 'close', busy: 'closeItem' });
    expect(closing).toContain('Closing…');
    expect(closing.match(/disabled=""/gu)).toHaveLength(3);
    const removing = menu({ mode: 'remove', busy: 'removeItem' });
    expect(removing).toContain('Removing…');
    expect(removing.match(/disabled=""/gu)).toHaveLength(3);
  });

  it('says each result in its own words', () => {
    const note = (one: ManageNote) => menu({ note: one });
    expect(note({ kind: 'uncommitted' })).toContain('<p class="notice-title"><bdi>silver-meadow</bdi> has changes that are not committed. Commit or discard them first.</p>');
    expect(note({ kind: 'made', what: 'tab' })).toContain('<p class="notice-title">The tab is made. Update <bdi>herdr</bdi> to open it from the phone.</p>');
    expect(note({ kind: 'error', text: 'Too many requests. Wait a minute.' })).toContain('<div class="notice danger" role="alert"><p class="notice-title">Too many requests. Wait a minute.</p>');
  });

  it('after the list changed, says so and offers only Close', () => {
    const page = menu({ mode: 'changed', note: { kind: 'uncommitted' } });
    expect(page).toContain('The list changed. Look again, then try again.');
    expect(page).not.toContain('not committed');
    expect(page.match(/<button/gu)).toHaveLength(1);
    expect(page).toContain('>Close</button>');
  });
});

describe('New workspace', () => {
  const projects = [{ id: '00000000-0000-4000-8000-0000000000a1', name: 'shop' }, { id: '00000000-0000-4000-8000-0000000000a2', name: 'notes' }];
  const list = (extra: Partial<Parameters<typeof WorkspaceList>[0]> = {}) => renderToStaticMarkup(
    <WorkspaceList entry={ready(vps)} settings={settingsWith({ projects })} busy={undefined} checking={false} note={undefined} onPick={noop} onCheck={noop} onClose={noop} {...extra} />,
  );

  it('lists that computer\'s Termivoa projects, names isolated', () => {
    const page = list();
    expect(page).toContain('<p class="caps"><bdi>herdr</bdi> · on vps</p>');
    expect(page).toContain('<h2 id="workspace-title">New workspace</h2>');
    expect(page).toContain('<bdi>shop</bdi>');
    expect(page).toContain('<bdi>notes</bdi>');
    expect(page).not.toContain('pterm project add');
  });

  it('offers Add a folder under the projects, and says what it does', () => {
    const page = list({ onAddFolder: noop });
    expect(page).toContain('This opens a terminal on vps with <code>pterm project add</code> typed. Type the folder, press Enter, then tap Check again here.');
    expect(page).toMatch(/<button type="button" class="secondary"><span class="btn-swap"><span>Add a folder<\/span>/u);
    expect(page).toMatch(/<span>Check again<\/span>/u);
  });

  it('types the command and never runs it: no newline in it, and a space for the folder', () => {
    expect(ADD_FOLDER_COMMAND).toBe('pterm project add ');
  });

  it('Add a folder: done when the session opened; words when it could not or failed', async () => {
    expect(await addFolderStep(async () => true, () => 'x')).toEqual({ kind: 'done' });
    expect(await addFolderStep(async () => false, () => 'x')).toEqual({ kind: 'note', note: { kind: 'error', text: NO_SESSION } });
    expect(await addFolderStep(async () => { throw new Error('no'); }, () => 'vps · can\'t reach')).toEqual({ kind: 'note', note: { kind: 'error', text: 'vps · can\'t reach' } });
  });

  it('with no project says what New session says, with Check again', () => {
    const page = list({ settings: settingsWith() });
    expect(page).toContain('Add a project on your computer first:');
    expect(page).toContain('pterm project add &lt;folder&gt;');
    expect(page).toContain('Check again');
    expect(list({ settings: settingsWith(), checking: true })).toContain('Checking…');
  });

  it('shows the tapped project busy and the rest off', () => {
    const page = list({ busy: projects[1]!.id });
    expect(page.match(/aria-busy="true"/gu)).toHaveLength(1);
    // Close and both projects.
    expect(page.match(/disabled=""/gu)).toHaveLength(3);
  });

  it('says when the workspace is made but cannot open from the phone, and shows a refusal', () => {
    expect(list({ note: { kind: 'made', what: 'workspace' } })).toContain('The workspace is made. Update <bdi>herdr</bdi> to open it from the phone.');
    expect(list({ note: { kind: 'error', text: 'That computer could not do this.' } })).toContain('role="alert"><p class="notice-title">That computer could not do this.</p>');
  });
});

describe('Start', () => {
  it('is one button named for the provider', () => {
    const page = renderToStaticMarkup(<StartButton source={ready(mac, shopGroups())} onStarted={async () => undefined} onFailure={() => ''} />);
    expect(page).toBe('<button type="button" class="secondary"><span class="btn-swap"><span><span>Start <bdi>herdr</bdi></span></span><span class="off" aria-hidden="true"><svg class="icon icon-spinner" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><circle class="icon-track" cx="12" cy="12" r="8"></circle><path d="M12 4a8 8 0 0 1 8 8"></path></svg>Starting…</span></span></button>');
  });
});
