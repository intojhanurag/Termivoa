import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { HostedSession } from './allSessions';
import { firstBlocked, foldKey, groupWordText, isOpen, itemMeta, numberedNames, ProviderSection, ProviderTree, sectionParts, wordText } from './Herdr';
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
    expect(shop?.items.map(itemMeta)).toEqual(['claude · codex · 3 tabs', 'claude · tab 1', 'claude · tab 1']);
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
    expect(page).toContain('<span class="group-name">shop</span>');
    expect(page).toContain('<span class="state-word amber">1 blocked</span>');
    expect(page).toContain('<span class="row-title">silver-meadow</span><span class="state-word amber">blocked · 4m</span>');
    expect(page).toContain('<span class="state-word accent">2 working</span>');
    expect(page).toContain('<span class="state-word green">done</span>');
    // A workspace outside git is one row; the repeat is numbered.
    expect(page).toContain('<span class="group-name">notes · 2</span>');
    expect(page).toContain('aria-label="New worktree in shop"');
    expect(isOpen(mac, shopGroups()[1] ?? group('x', 'x', []))).toBe(false);
    expect(foldKey(mac, { id: 'g1' })).toBe('mac.tailx.ts.net/g1');
  });

  it('folds a quiet multi-worktree project and says how many worktrees', () => {
    const quiet = group('g2', 'blog', [item('w2', 'main', [target('w2:p1.t', 'idle', { agent: 'claude' })]), item('w3', 'draft', [target('w3:p1.t', 'none')])]);
    const page = html([ready(mac, [quiet])], true);
    expect(page).toContain('aria-expanded="false"');
    expect(page).toContain('<span class="group-where">on mac</span><span class="group-where">2 worktrees</span>');
    expect(page).not.toContain('row-title');
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
    expect(page).toContain('<span class="home-badge">herdr</span><span class="caps provider-head-name">shop · on mac</span><span class="caps faint">4 agents</span>');
    expect(page).toContain('claude · tab 1');
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
    expect(page).toMatch(/>All<\/button><button type="button" class="chip" aria-pressed="true">herdr<span class="chip-count" aria-hidden="true">2<\/span>/u);
    expect(page).toContain('1 blocked · 1 done · 2 working');
    expect(page).toContain('Open the blocked agent');
    expect(page).not.toContain('new session');
  });

  it('keeps the sessions line when the provider cannot be read', () => {
    const page = renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac)]} providers={[providerEntry(mac, { kind: 'unreachable' })]} />);
    expect(page).toContain('Your computer · connected');
    expect(page).not.toContain('can&#x27;t reach');
  });
});
