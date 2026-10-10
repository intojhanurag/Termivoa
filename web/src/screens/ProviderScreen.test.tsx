import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/http';
import { INITIAL_STATUS, type ConnectionStatus } from '../terminal/Connection';
import { CockpitDock, ReadOnlyDock } from './Dock';
import { ProviderEnded } from './Live';
import { item, mac, providerEntry, ready, shopGroups, snapshot, target } from './providerFixtures';
import {
  liveMenu, openedPhase, openProblem, phoneGrid, ProviderChips, providerHosted, ProviderScreen, sizeLine, tapOpens, targetLeft, unavailableText, visibilityStep,
} from './ProviderScreen';

const noop = () => undefined;
const screen = (tapped: number, entry = ready(mac)) => renderToStaticMarkup(
  <ProviderScreen
    computer={mac} provider="herdr" targetId="w8:p1.term_b1" tapped={tapped} entry={entry} fontSize={13} gestures onFontSize={noop} multi={false}
    onChip={noop} onGone={noop} onClosed={noop} onOpened={noop} onReload={async () => undefined} onSessions={noop} onSettings={noop} onFailure={() => 'x'} onRevoked={noop}
  />,
);

describe('a provider terminal before it opens', () => {
  it('after a link or a reload: shows its names and Open, not busy', () => {
    const page = screen(0);
    expect(page).toContain('<h1><bdi>silver-meadow</bdi></h1>');
    expect(page).toContain('<dt>Project</dt><dd><bdi>shop</bdi></dd>');
    expect(page).toContain('<dt>Agent</dt><dd><bdi>claude</bdi></dd>');
    expect(page).toContain('mac shows this terminal at phone size while it is open here.');
    expect(page).toMatch(/<button type="button" class="primary wide"><span class="btn-swap"><span>Open<\/span>/u);
    expect(page).not.toContain('aria-busy');
  });

  it('after a tap: busy at once', () => {
    expect(screen(1)).toContain('aria-busy="true"');
  });

  it('chips are the workspace\'s tabs; a tab this herdr cannot open is off and says why', () => {
    const page = renderToStaticMarkup(
      <ProviderChips name="herdr"
        item={item('w1', 'main', [target('w1:p1.a', 'blocked', { agent: 'claude', label: '1' }), target('w1:p2.b', 'working', { agent: 'codex', label: '1' }), target('w1:p3.c', 'none', { label: 'logs', canOpen: false })])}
        current="w1:p2.b" onChip={noop} onSessions={noop}
      />,
    );
    expect(page).toContain('‹ Sessions');
    expect(page).toContain('<span class="tab-label"><bdi>1</bdi> · <bdi>claude</bdi></span><span class="dot amber" aria-hidden="true"></span><span class="visually-hidden">, blocked</span>');
    expect(page).toContain('aria-current="page"');
    expect(page).toContain('title="Update herdr to open this tab"');
    expect(page.match(/disabled=""/gu)).toHaveLength(1);
  });
});

describe('the ⋯ menu in a provider terminal (docs/herdr-manage.md, section 14, B3)', () => {
  it('shows only where the computer can manage, for an item in the latest list', () => {
    expect(liveMenu(ready(mac), true)).toBe(true);
    expect(liveMenu(ready(mac), false)).toBe(false);
    expect(liveMenu({ ...ready(mac), manage: false }, true)).toBe(false);
    expect(liveMenu(undefined, true)).toBe(false);
    // Not a revision 6 control: it reuses the revision 5 actions.
    expect(liveMenu({ ...ready(mac), agentTab: false }, true)).toBe(true);
  });

  it('is the item menu\'s own button, after the tab chips, named for the workspace', () => {
    const page = screen(0);
    expect(page).toContain('</div><button type="button" class="round item-menu" aria-label="Menu for silver-meadow" aria-haspopup="dialog"><span aria-hidden="true">⋯</span></button></nav>');
    expect(page).not.toContain('role="dialog"');
  });

  it('is away below revision 5, and for a terminal the list does not have', () => {
    expect(screen(0, { ...ready(mac), manage: false })).not.toContain('Menu for');
    expect(screen(0, ready(mac, shopGroups().filter((one) => one.name !== 'shop')))).not.toContain('Menu for');
    expect(screen(0, providerEntry(mac, { kind: 'loading' }))).not.toContain('Menu for');
  });
});

describe('opening', () => {
  it('starts the attach at the phone\'s size, within the schema\'s limits', () => {
    expect(phoneGrid(390, 844, 13, () => 7.8)).toEqual({ columns: 50, rows: 32 });
    expect(phoneGrid(100, 200, 13, () => 7.8)).toEqual({ columns: 20, rows: 5 });
    expect(phoneGrid(9000, 9000, 13, () => 7.8)).toEqual({ columns: 400, rows: 200 });
  });

  it('words the refusals it knows; a terminal that left is gone', () => {
    expect(openProblem(new ApiError(404, 'session_missing', 'x'), 'herdr', 'mac')).toEqual({ gone: true, text: 'That terminal is gone.' });
    expect(openProblem(new ApiError(0, 'network', 'x'), 'herdr', 'mac')).toBeUndefined();
    expect(openProblem(new ApiError(409, 'operation_conflict', 'x'), 'herdr', 'mac')).toEqual({ gone: false, text: 'herdr on mac did not open this terminal. Another program may hold it, or it just closed.' });
  });

  it('limit_exceeded (the cap of open terminals, or this phone\'s open rate) says what is true for both', () => {
    const text = openProblem(new ApiError(429, 'limit_exceeded', 'too many requests; wait a minute and try again'), 'herdr', 'mac')?.text;
    expect(text).toBe('mac cannot open more herdr terminals just now. Close one you do not use, or wait a minute.');
    expect(text).not.toMatch(/Too many herdr terminals are open/u);
  });

  it('host_unavailable for a provider that is Off says Off, not "is not running"', () => {
    const unavailable = new ApiError(503, 'host_unavailable', 'x');
    expect(openProblem(unavailable, 'herdr', 'mac', 'off')?.text).toBe('herdr is off on mac. Turn it on in Settings → mac.');
    expect(openProblem(unavailable, 'herdr', 'mac', 'notRunning')?.text).toBe('herdr is not running on mac.');
    expect(unavailableText('herdr', 'mac', undefined)).toBe('herdr is off or not running on mac.');
  });

  it('shows the provider session in Live without a project or shell', () => {
    const session = providerHosted({
      id: '84444444-4444-4444-8444-444444444444', generation: '85555555-5555-4555-8555-555555555555', title: 'herdr · shop · silver-meadow',
      state: 'running', columns: 50, rows: 32, provider: 'herdr', targetId: 'w8:p1.term_b1',
    }, mac);
    expect(session).toMatchObject({ projectId: '', shellProfileId: '', title: 'herdr · shop · silver-meadow', computer: mac });
    expect(sizeLine('vps')).toBe('vps shows this terminal at phone size while it is open here.');
  });
});

describe('Live for a provider terminal', () => {
  const live: ConnectionStatus = { ...INITIAL_STATUS, link: 'live', control: 'observer', sessionState: 'running' };

  it('has no History in either dock, and keeps Take control and Changes', () => {
    const readOnly = renderToStaticMarkup(<ReadOnlyDock status={live} historyEnabled history={false} onTakeControl={noop} onHistory={noop} onChanges={noop} onSessions={noop} />);
    expect(readOnly).not.toContain('History');
    expect(readOnly).toContain('Take control');
    expect(readOnly).toContain('Changes');
    const cockpit = renderToStaticMarkup(
      <CockpitDock compact={false} disabled={false} latched={{ ctrl: false, alt: false }} alternate historyEnabled history={false}
        onAgentKey={noop} onKey={noop} onLatch={noop} onWheel={noop} onType={noop} onCompose={noop} onSnippets={noop} onHistory={noop} onChanges={noop} onHideKeyboard={noop} />,
    );
    expect(cockpit).not.toContain('history');
    expect(cockpit).toContain('changes');
  });

  it('says when the attach ended and offers Open again', () => {
    const page = renderToStaticMarkup(<ProviderEnded onOpenAgain={async () => undefined} onSessions={noop} />);
    expect(page).toContain('Closed here');
    expect(page).toContain('Open again');
  });
});

describe('hide and show (the 10 s end rule)', () => {
  const session = {
    id: '84444444-4444-4444-8444-444444444444', generation: '85555555-5555-4555-8555-555555555555', title: 'herdr · shop',
    state: 'running', columns: 50, rows: 32, provider: 'herdr', targetId: 'w8:p1.term_b1',
  } as const;

  it('an open that answers while the page is hidden pauses, so Live never attaches while hidden', () => {
    expect(openedPhase(session, true)).toEqual({ kind: 'paused' });
    expect(openedPhase(session, false)).toEqual({ kind: 'open', session });
    // Back in front, the paused screen opens again.
    expect(visibilityStep('paused', false, false)).toBe('open');
  });

  it('hidden closes an open stream; back opens again; nothing else moves', () => {
    expect(visibilityStep('open', false, true)).toBe('pause');
    expect(visibilityStep('opening', false, true)).toBeUndefined();
    expect(visibilityStep('ask', false, false)).toBeUndefined();
    expect(visibilityStep('error', false, false)).toBeUndefined();
  });

  it('after the attach ended (Closed here, Open again), hide and show neither pause nor open again', () => {
    expect(visibilityStep('open', true, true)).toBeUndefined();
    expect(visibilityStep('open', true, false)).toBeUndefined();
  });

  it('a new tap for the address already shown opens, unless the terminal shows here', () => {
    expect(tapOpens('ask', false)).toBe(true);
    expect(tapOpens('error', false)).toBe(true);
    expect(tapOpens('open', true)).toBe(true);
    expect(tapOpens('open', false)).toBe(false);
  });
});

describe('That terminal is gone', () => {
  const without = shopGroups().filter((one) => one.name !== 'shop');

  it('shows only for a complete read, newer than the screen, that lacks the target', () => {
    expect(targetLeft(providerEntry(mac, { kind: 'ok', snapshot: snapshot(without), at: 10 }), false, 5)).toBe(true);
    expect(targetLeft(providerEntry(mac, { kind: 'ok', snapshot: snapshot(without), at: 10 }), true, 5)).toBe(false);
  });

  it('not for a cut (truncated) list: the target may just not be in it', () => {
    expect(targetLeft(providerEntry(mac, { kind: 'ok', snapshot: snapshot(without, { truncated: true }), at: 10 }), false, 5)).toBe(false);
  });

  it('not for the list read before the screen (a create\'s reload still runs): the real error shows then', () => {
    expect(targetLeft(providerEntry(mac, { kind: 'ok', snapshot: snapshot(without), at: 4 }), false, 5)).toBe(false);
    expect(targetLeft(providerEntry(mac, { kind: 'loading' }), false, 5)).toBe(false);
  });
});

describe('names from a peer', () => {
  it('are isolated (bdi), so a right-to-left mark in one cannot reorder the line', () => {
    const page = renderToStaticMarkup(
      <ProviderChips name="herdr" item={item('w1', 'main', [target('w1:p1.a', 'idle', { agent: 'cl\u202eaude', label: '\u202e1' }), target('w1:p2.b', 'idle', { label: '\u202e1' })])}
        current="w1:p2.b" onChip={noop} onSessions={noop} />,
    );
    expect(page).toContain('<bdi>\u202e1</bdi> · <bdi>cl\u202eaude</bdi>');
  });
});
