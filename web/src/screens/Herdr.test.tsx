import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { HostedSession } from './allSessions';
import { ClosedRow, closedOf, firstBlocked, foldKey, groupWordText, isOpen, itemMeta, numberedNames, ProviderSection, ProviderTree, sectionParts, wordText } from './Herdr';
import { group, item, mac, okEntry, providerEntry, ready, shopGroups, snapshot, target, vps } from './providerFixtures';
import { Sessions } from './Sessions';

const now = Date.parse('2026-10-04T09:55:00Z');
const noop = () => undefined;
const retry = async () => undefined;

describe('state words', () => {
  it('say blocked, working, done and idle; unknown and none say nothing', () => {
    expect(wordText('blocked', 1, '4m')).toBe('blocked · 4m');
    expect(wordText('working', 2)).toBe('2 working');
    expect(wordText('done', 1)).toBe('done');
    expect(wordText('idle', 1)).toBe('idle');
    expect(wordText('unknown', 1)).toBeUndefined();
    expect(wordText('none', 3)).toBeUndefined();
    const [shop, notes] = shopGroups();
    expect(shop && groupWordText(shop)).toBe('1 blocked');
    expect(notes && groupWordText(notes)).toBe('idle');
  });

  it('name the agents and the tab, never a terminal title', () => {
    const [shop] = shopGroups();
    expect(shop?.items.map((one) => itemMeta(one).join(' · '))).toEqual(['claude · codex · 3 tabs', 'claude · tab 1', 'claude · tab 1']);
  });

  it('number a repeated name', () => {
    expect(numberedNames([{ name: 'notes' }, { name: 'shop' }, { name: 'notes' }, { name: 'notes' }])).toEqual(['notes', 'shop', 'notes · 2', 'notes · 3']);
  });
});

describe('the herdr tab (H3)', () => {
  const html = (entries = [ready(mac)], multi = false) =>
    renderToStaticMarkup(<ProviderTree entries={entries} multi={multi} now={now} onOpen={noop} onCreate={noop} onRetry={retry} />);

  it('opens a project with work in it and folds a quiet one, with aria-expanded', () => {
    const page = html();
    expect(page).toContain('aria-expanded="true"');
    expect(page).toContain('<span class="group-name"><bdi>shop</bdi></span>');
    expect(page).toContain('<span class="state-word amber">1 blocked</span>');
    expect(page).toContain('<span class="row-title"><bdi>silver-meadow</bdi></span><span class="state-word amber">blocked · 4m</span>');
    expect(page).toContain('<span class="state-word accent">2 working</span>');
    expect(page).toContain('<span class="state-word green">done</span>');
    // A workspace outside git is one row; the repeat is numbered.
    expect(page).toContain('<span class="group-name"><bdi>notes · 2</bdi></span>');
    expect(page).toContain('aria-label="New worktree in shop"');
    expect(isOpen(mac, shopGroups()[1] ?? group('x', 'x', []))).toBe(false);
    expect(foldKey(mac, { id: 'g1' })).toBe('mac.tailx.ts.net/g1');
  });

  it('gives a git project with only its main checkout one row with its +, and none outside git', () => {
    const acme = group('g4', 'acme-web', [item('w4', 'acme-web', [target('w4:p1.t', 'idle', { agent: 'claude' })])]);
    const page = html([ready(mac, [acme, ...shopGroups().slice(1)])]);
    expect(page).toContain('aria-label="New worktree in acme-web"');
    expect(page).not.toContain('New worktree in notes');
    expect(page).not.toContain('aria-expanded');
  });

  it('keeps a project name and its small label in one wrapping title, with no "on mac" (the computer header or the drawer pill names it)', () => {
    const quiet = group('g2', 'storefront-redesign', [item('w2', 'main', [target('w2:p1.t', 'idle', { agent: 'claude' })]), item('w3', 'draft', [target('w3:p1.t', 'none')])]);
    expect(html([ready(mac, [quiet])], true)).toContain(
      '<span class="group-title"><span class="group-name"><bdi>storefront-redesign</bdi></span><span class="group-where">2 worktrees</span></span>',
    );
  });

  it('puts the head of each computer over its projects', () => {
    const page = renderToStaticMarkup(<ProviderTree entries={[ready(mac), ready(vps, [])]} multi head={(entry) => <h2>head {entry.computer.label}</h2>} now={now} onOpen={noop} onRetry={retry} />);
    expect(page.indexOf('head mac')).toBeLessThan(page.indexOf('shop'));
    expect(page.indexOf('shop')).toBeLessThan(page.indexOf('head vps'));
  });

  it('folds a quiet multi-worktree project and says how many worktrees', () => {
    const quiet = group('g2', 'blog', [item('w2', 'main', [target('w2:p1.t', 'idle', { agent: 'claude' })]), item('w3', 'draft', [target('w3:p1.t', 'none')])]);
    const page = html([ready(mac, [quiet])], true);
    expect(page).toContain('aria-expanded="false"');
    expect(page).toContain('<span class="group-where">2 worktrees</span>');
    expect(page).not.toContain('on mac');
    expect(page).not.toContain('row-title');
  });

  it('points aria-controls only at a list that is in the page: an open group has it, a folded one does not', () => {
    const quiet = group('g2', 'blog', [item('w2', 'main', [target('w2:p1.t', 'idle', { agent: 'claude' })]), item('w3', 'draft', [target('w3:p1.t', 'none')])]);
    expect(html([ready(mac, [quiet])])).not.toContain('aria-controls');
    const page = html();
    const ids = [...page.matchAll(/aria-controls="([^"]+)"/gu)].map((match) => match[1]);
    expect(ids.length).toBeGreaterThan(0);
    for (const id of ids) expect(page).toContain(`id="${id}"`);
  });

  it('isolates every name from a peer (bdi): project, worktree, agent kinds and tab', () => {
    const rtl = group('g7', 'ab\u202ecd', [item('w7', 'ef\u202egh', [target('w7:p1.t', 'blocked', { agent: 'cl\u202eaude', label: '\u202e1' })]), item('w8', 'x', [target('w8:p1.t', 'none')])]);
    const page = html([ready(mac, [rtl])]);
    expect(page).toContain('<span class="group-name"><bdi>ab\u202ecd</bdi></span>');
    expect(page).toContain('<span class="row-title"><bdi>ef\u202egh</bdi></span>');
    expect(page).toContain('<span class="row-meta"><bdi>cl\u202eaude</bdi> · <bdi>tab \u202e1</bdi></span>');
  });

  it('shows each computer\'s trouble in its own part only', () => {
    const page = html([
      ready(mac),
      providerEntry(vps, { kind: 'unreachable' }),
      providerEntry({ ...vps, host: 'pi', label: 'pi' }, { kind: 'ok', snapshot: snapshot([], { state: 'notRunning' }), at: 0 }),
      providerEntry({ ...vps, host: 'nas', label: 'nas' }, { kind: 'ok', snapshot: snapshot([], { state: 'unsupported' }), at: 0 }),
      providerEntry({ ...vps, host: 'old', label: 'old' }, { kind: 'ok', snapshot: snapshot([], { state: 'off' }), at: 0 }),
    ], true);
    expect(page).toContain('herdr on vps · can&#x27;t reach');
    expect(page).toContain('>Retry<');
    expect(page).toContain('herdr is not running on pi');
    expect(page).toContain('Update herdr on nas');
    expect(page).not.toContain('on old');
    expect(page).toContain('silver-meadow');
  });

  it('says when a list was cut, and has no + where the provider cannot create', () => {
    const page = renderToStaticMarkup(
      <ProviderTree entries={[providerEntry(mac, { kind: 'ok', snapshot: snapshot(shopGroups(), { truncated: true }), at: 0 }, { canCreate: false })]} multi={false} now={now} onOpen={noop} onCreate={noop} onRetry={retry} />,
    );
    expect(page).toContain('Some projects on mac are not shown.');
    expect(page).not.toContain('New worktree');
  });

  describe('manage (docs/herdr-manage.md)', () => {
    const handlers = { onMenu: noop, onWorkspace: noop, onOpened: noop, onFailure: () => '' };
    const tree = (entries = [ready(mac)], multi = false) =>
      renderToStaticMarkup(<ProviderTree entries={entries} multi={multi} now={now} onOpen={noop} onCreate={noop} onRetry={retry} manage={handlers} />);
    const stopped = (extra = {}) => providerEntry(mac, { kind: 'ok', snapshot: snapshot([], { state: 'notRunning' }), at: 0 }, { state: 'notRunning', ...extra });

    it('puts a ⋯ menu button by every item row, beside the row and never inside it', () => {
      const page = tree();
      for (const name of ['main', 'silver-meadow', 'lucky-valley', 'notes', 'notes · 2']) expect(page).toContain(`aria-label="Menu for ${name}"`);
      // The project row of a folding group has no menu: its items have.
      expect(page).not.toContain('aria-label="Menu for shop"');
      expect(page).toContain('<li class="item-line"><button type="button" class="session-row provider-item nested"');
      expect(page).toContain('</button><button type="button" class="round item-menu" aria-label="Menu for silver-meadow" aria-haspopup="dialog"><span aria-hidden="true">⋯</span></button></li>');
    });

    it('has no menu, no New workspace and no Start without the handlers or below revision 5', () => {
      for (const page of [html([ready(mac)]), tree([{ ...ready(mac), manage: false }]), html([stopped({ canStart: true })]), tree([{ ...stopped({ canStart: true }), manage: false }])]) {
        expect(page).not.toContain('Menu for');
        expect(page).not.toContain('New workspace');
        expect(page).not.toContain('<span>Start');
      }
      // The All tab's rows never have a menu.
      expect(renderToStaticMarkup(<ProviderSection entries={[ready(mac)]} multi={false} now={now} onOpen={noop} />)).not.toContain('Menu for');
    });

    it('offers New workspace for each computer whose provider is ready, also with an empty list', () => {
      expect(tree()).toContain('aria-haspopup="dialog">New workspace</button>');
      const empty = tree([ready(mac, [])]);
      expect(empty).toContain('No herdr projects on mac.');
      expect(empty).toContain('>New workspace</button>');
      const two = tree([ready(mac), ready(vps, []), stopped()], true);
      expect(two).toContain('>New workspace on mac</button>');
      expect(two).toContain('>New workspace on vps</button>');
      expect(two.match(/New workspace/gu)).toHaveLength(2);
      expect(tree([providerEntry(mac, { kind: 'loading' })])).not.toContain('New workspace');
    });

    describe('closed worktrees (section 14, B2)', () => {
      const main = item('w1', 'main', [target('w1:p1.t', 'working', { agent: 'claude' })]);
      const busyShop = (closed: string[]) => group('g1', 'shop', [main, item('w8', 'silver-meadow', [target('w8:p1.t', 'idle')], { linked: true })], { closed });
      const row = (name: string) => `<li class="item-line closed-line"><span class="closed-name"><bdi>${name}</bdi></span><span class="state-word muted">closed</span><button type="button" class="link" aria-label="Open ${name}">`;

      it('shows each under its project\'s items: the name as text, CLOSED and Open', () => {
        const page = tree([ready(mac, [busyShop(['fix/login', 'lucky-valley'])])]);
        expect(page).toContain(row('fix/login'));
        expect(page).toContain(row('lucky-valley'));
        expect(page.match(/>Open<\/span>/gu)).toHaveLength(2);
        // After the items, in the project's own list.
        expect(page.indexOf('Menu for silver-meadow')).toBeLessThan(page.indexOf('closed-line'));
        expect(page.indexOf('closed-line')).toBeLessThan(page.indexOf('New workspace'));
      });

      it('shows them only where the computer can open one: revision 6, the manage gate and the handlers', () => {
        const shop = busyShop(['fix/login']);
        expect(closedOf(ready(mac), shop)).toEqual(['fix/login']);
        expect(closedOf({ manage: true, agentTab: false }, shop)).toEqual([]);
        expect(closedOf({ manage: false, agentTab: true }, shop)).toEqual([]);
        expect(closedOf(ready(mac), group('g2', 'none', []))).toEqual([]);
        expect(tree([{ ...ready(mac, [shop]), agentTab: false }])).not.toContain('closed-line');
        expect(html([ready(mac, [shop])])).not.toContain('closed-line');
      });

      it('counts none of them: the project word, the folded count and the status line stay', () => {
        const withClosed = busyShop(['fix/login', 'lucky-valley']);
        expect(groupWordText(withClosed)).toBe(groupWordText(busyShop([])));
        expect(tree([ready(mac, [withClosed])])).toContain('<span class="state-word accent">1 working</span>');
        const quiet = group('g7', 'quiet', [item('w1', 'main', [target('w1:p1.t', 'idle')]), item('w2', 'two', [target('w2:p1.t', 'idle')])], { closed: ['fix/login'] });
        expect(tree([ready(mac, [quiet])])).toContain('<span class="group-where">2 worktrees</span>');
      });

      it('a folded project shows none', () => {
        const quiet = group('g7', 'quiet', [item('w1', 'main', [target('w1:p1.t', 'idle')]), item('w2', 'two', [target('w2:p1.t', 'idle')])], { closed: ['fix/login'] });
        const page = tree([ready(mac, [quiet])]);
        expect(page).toContain('aria-expanded="false"');
        expect(page).not.toContain('closed-line');
      });

      it('a project with one item is a group (not one row) while it has a closed worktree, so the rows have a place', () => {
        const lone = (closed: string[]) => group('g4', 'acme-web', [item('w4', 'acme-web', [target('w4:p1.t', 'working', { agent: 'claude' })])], { closed });
        expect(tree([ready(mac, [lone([])])])).not.toContain('aria-expanded');
        const page = tree([ready(mac, [lone(['fix/login'])])]);
        expect(page).toContain('aria-expanded="true"');
        expect(page).toContain(row('fix/login'));
        expect(page).toContain('aria-label="Menu for acme-web"');
        // One item is not "1 worktrees" when it folds.
        const folded = tree([ready(mac, [group('g5', 'still', [item('w5', 'still', [target('w5:p1.t', 'idle')])], { closed: ['fix/login'] })])]);
        expect(folded).toContain('aria-expanded="false"');
        expect(folded).not.toContain('worktrees</span>');
        // Below revision 6 it stays one row.
        expect(tree([{ ...ready(mac, [lone(['fix/login'])]), agentTab: false }])).not.toContain('aria-expanded');
      });

      it('shows the tapped row busy and every other Open off', () => {
        const rows = (busy: boolean, off: boolean) => renderToStaticMarkup(<ClosedRow name="fix/login" busy={busy} off={off} onOpen={noop} />);
        expect(rows(true, true)).toContain('aria-busy="true"');
        expect(rows(true, true)).toContain('Opening…');
        expect(rows(false, true)).toContain('disabled=""');
        expect(rows(false, true)).not.toContain('aria-busy');
        expect(rows(false, false)).not.toContain('disabled');
      });

      it('holds 16 long names, each isolated and never as markup', () => {
        const names = Array.from({ length: 16 }, (_, index) => `feature/a-very-long-branch-name-for-the-phone-screen-${index}`);
        const page = tree([ready(mac, [busyShop(names)])]);
        expect(page.match(/closed-line/gu)).toHaveLength(16);
        expect(renderToStaticMarkup(<ClosedRow name={'<b>\u202ex</b>'} busy={false} off={false} onOpen={noop} />)).toContain('<bdi>&lt;b&gt;\u202ex&lt;/b&gt;</bdi>');
      });
    });

    it('has Start herdr on the not-running notice only where the computer can start it', () => {
      const can = tree([stopped({ canStart: true })]);
      expect(can).toContain('herdr is not running on mac');
      expect(can).toContain('<span>Start <bdi>herdr</bdi></span>');
      expect(can).not.toContain('Start it there');
      const cannot = tree([stopped()]);
      expect(cannot).toContain('Start it there; its projects show here.');
      expect(cannot).not.toContain('<span>Start');
    });
  });

  it('finds the first blocked agent for the bottom button', () => {
    expect(firstBlocked([ready(mac)])).toEqual({ computer: mac, provider: 'herdr', targetId: 'w8:p1.term_b1' });
    expect(firstBlocked([ready(mac, [])])).toBeUndefined();
  });
});

describe('the All tab section (H1)', () => {
  it('lists worktrees with an agent under a HERDR label, by project, on that computer', () => {
    const parts = sectionParts([ready(mac)]);
    expect(parts.map((part) => [part.name, part.items.map((one) => one.name), part.agents])).toEqual([
      ['shop', ['main', 'silver-meadow', 'lucky-valley'], 4],
      ['notes', ['notes'], 1],
    ]);
    const page = renderToStaticMarkup(<ProviderSection entries={[ready(mac)]} multi now={now} onOpen={noop} />);
    expect(page).toContain('<span class="home-badge"><bdi>herdr</bdi></span><span class="caps provider-head-name"><bdi>shop</bdi> · on mac</span><span class="caps faint">4 agents</span>');
    expect(page).toContain('<bdi>claude</bdi> · <bdi>tab 1</bdi>');
  });
});

describe('Sessions with a provider', () => {
  const base = { sessions: [] as HostedSession[], offerPush: false, onOpen: noop, onNew: noop, onWall: noop, onSettings: noop, onReload: retry, onRetry: retry, onFailure: () => '' };

  it('has no provider tab when no computer has one', () => {
    const page = renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac)]} providers={[]} />);
    expect(page).not.toContain('herdr');
    expect(page).not.toContain('class="chips"');
  });

  it('adds a herdr tab with its count, and on it the blocked button', () => {
    const page = renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac), okEntry(vps)]} providers={[ready(mac)]} tab="herdr" />);
    expect(page).toMatch(/>Sessions<\/button><button type="button" class="chip" aria-pressed="true">herdr<span class="chip-count" aria-hidden="true">2<\/span>/u);
    expect(page).toContain('1 blocked · 1 done · 2 working');
    expect(page).toContain('Open the blocked agent');
    expect(page).not.toContain('new session');
  });

  it('opens on the herdr tab when asked (the way back from a herdr terminal), and on Sessions when not', () => {
    const on = (tab?: string) => renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac)]} providers={[ready(mac)]} {...(tab === undefined ? {} : { tab })} />);
    expect(on('herdr')).toContain('class="provider-tree"');
    expect(on('herdr')).toMatch(/aria-pressed="true">herdr</u);
    expect(on()).not.toContain('class="provider-tree"');
    expect(on()).toMatch(/aria-pressed="true">Sessions</u);
  });

  it('keeps the sessions line when the provider cannot be read', () => {
    const page = renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac)]} providers={[providerEntry(mac, { kind: 'unreachable' })]} />);
    expect(page).toContain('Your computer · connected');
    expect(page).not.toContain('can&#x27;t reach');
  });
});
