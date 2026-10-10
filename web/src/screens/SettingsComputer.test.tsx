import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { HTTPGitHubState } from '../generated/protocol';
import type { ComputerEntry } from './allSessions';
import { ComputersSection } from './Computers';
import { info, mac, okEntry, providerEntry, settingsWith, snapshot, vps } from './providerFixtures';
import { ChatViewRow, chatInfo, ComputerPage, pushWord, Settings } from './Settings';
import {
  cardChips, ComputerCard, ComputerHead, initialPage, MAIN_PAGE, PAGE_KEY, pageFromHistory, shownPage, statusDot,
} from './SettingsComputer';

const noop = () => undefined;
const github = (state: HTTPGitHubState) => ({ protocolRevision: 7, github: { state, login: 'octo-demo' } });
const project = (id: string) => ({ id: `00000000-0000-4000-8000-0000000000${id}`, name: id });

afterEach(() => vi.unstubAllGlobals());

describe('the main page: a card per computer', () => {
  it('has herdr, GitHub and projects chips, each only where that computer reports it', () => {
    const chips = (entry: ComputerEntry, providers = []) => cardChips(entry, providers).map((chip) => `${chip.text}:${chip.tone}`);
    expect(chips(okEntry(mac, { ...github('ready'), projects: [project('a1'), project('a2'), project('a3'), project('a4')] })))
      .toEqual(['herdr:green', 'GitHub:green', '4 projects:muted']);
    expect(chips(okEntry(vps, github('notConnected')))).toEqual(['herdr:green', 'Connect GitHub:dashed', '0 projects:muted']);
    expect(chips(okEntry(vps, { ...github('noGit'), providers: [], projects: [project('a1')] }))).toEqual(['git missing:amber', '1 project:muted']);
    // No herdr listed, GitHub not offered (revision 4): projects only.
    expect(chips(okEntry(vps, { providers: [] }))).toEqual(['0 projects:muted']);
    expect(chips(okEntry(vps, { providers: [info({ state: 'notRunning' })] }))[0]).toBe('herdr:amber');
    expect(chips(okEntry(vps, { providers: [info({ state: 'off' })] }))[0]).toBe('herdr:muted');
    expect(chips(okEntry(vps, { providers: [info({ state: 'notInstalled' })] }))[0]).toBe('herdr:muted');
    // The provider read is newer than the settings.
    expect(cardChips(okEntry(mac, { providers: [info({ state: 'notRunning' })] }), [providerEntry(mac, { kind: 'ok', snapshot: snapshot([]), at: 1 })])[0]?.tone).toBe('green');
    expect(cardChips({ computer: vps, status: { kind: 'unreachable' } })).toEqual([]);
  });

  it('draws each state: online plain, checking plain, cannot reach and pair again dashed with their lines', () => {
    const card = (entry: ComputerEntry) => renderToStaticMarkup(<ul><ComputerCard entry={entry} now={0} onOpen={noop} /></ul>);
    const online = card(okEntry(mac, github('notConnected')));
    expect(online).toContain('<span class="dot green" aria-hidden="true"></span><span class="computer-name">mac</span><span class="home-badge">Home</span>');
    expect(online).toContain('<span class="sc-chip dashed">Connect GitHub</span>');
    expect(online).toContain('<li class="computer-card sc-card is-ok">');
    expect(online).not.toContain('computer-state');
    // The dot is hidden from screen readers: the state is said in words.
    expect(online).toContain('<span class="home-badge">Home</span><span class="visually-hidden">, Online</span>');
    const loading = card({ computer: vps, status: { kind: 'loading' } });
    expect(loading).toContain('>Checking</span>');
    expect(loading).not.toContain('dashed');
    const away = card({ computer: vps, status: { kind: 'unreachable', lastSeen: 0, network: true } });
    expect(away).toContain('class="computer-card sc-card is-unreachable dashed"');
    expect(away).toContain('Can&#x27;t reach');
    expect(away).toContain('Last seen just now. Is it on, and in your tailnet?');
    const pair = card({ computer: vps, status: { kind: 'pair' } });
    expect(pair).toContain('dashed');
    expect(pair).toContain('>Needs you</span>');
    expect(pair).toContain('vps · pair again · Run pterm pair --via');
    expect(statusDot({ computer: vps, status: { kind: 'pair' } })).toBe('red');
  });

  it('lists home first as given, Check again only on a dashed card, then + Add a computer', () => {
    const html = renderToStaticMarkup(
      <ComputersSection entries={[okEntry(mac), { computer: vps, status: { kind: 'unreachable' } }]} onCheck={() => Promise.resolve()} onFailure={() => ''} onOpen={noop} />,
    );
    expect(html.indexOf('>mac<')).toBeLessThan(html.indexOf('>vps<'));
    expect(html.match(/<span>Check again<\/span>/gu)).toHaveLength(1);
    expect(html).toContain('id="add-computer"');
    expect(html).toContain('>+ Add a computer</button>');
  });
});

describe('the computer page', () => {
  const page = (entry: ComputerEntry, providers = [] as Parameters<typeof ComputerPage>[0]['providers']) => renderToStaticMarkup(
    <ComputerPage entry={entry} providers={providers} onBack={noop} onFailure={() => ''} onCheck={() => Promise.resolve()} onComputersChanged={noop} onRemoved={noop} />,
  );

  it('has ‹ Settings, the label, its state and address', () => {
    const head = renderToStaticMarkup(<ComputerHead entry={okEntry(mac)} now={0} onBack={noop} />);
    expect(head).toContain('>‹ Settings</button>');
    expect(head).toContain('<h1 class="display" tabindex="-1">mac</h1>');
    expect(head).toContain('<span class="computer-state green">Online · Home</span>');
    expect(head).toContain('<span class="row-meta">mac.tailx.ts.net</span>');
    expect(head).toContain('Termivoa 0.1.0 · last seen just now');
  });

  it('shows herdr in every state with its switches or its install card', () => {
    expect(page(okEntry(mac))).toContain('<span class="computer-state green">Running</span>');
    expect(page(okEntry(mac)).match(/role="switch"/gu)).toHaveLength(3);
    expect(page(okEntry(mac, { providers: [info({ state: 'notRunning' })] }))).toContain('herdr is not running on mac.');
    expect(page(okEntry(mac, { providers: [info({ state: 'off', enabled: false })] }))).toContain('Off for every phone');
    expect(page(okEntry(mac, { providers: [info({ state: 'unsupported' })] }))).toContain('Update herdr on mac.');
    const absent = page(okEntry(mac, { providers: [info({ state: 'notInstalled' })] }));
    expect(absent).toContain('Not installed');
    expect(absent).not.toContain('role="switch"');
  });

  it('shows GitHub in every state', () => {
    expect(page(okEntry(mac, github('ready')))).toContain('>Set up a repository</button>');
    expect(page(okEntry(mac, github('ready')))).toContain('>Disconnect</button>');
    expect(page(okEntry(mac, github('notConnected')))).toContain('>Connect</button>');
    expect(page(okEntry(mac, github('noGit')))).toContain('git missing');
    expect(page(okEntry(mac, github('needsInstall')))).toContain('>Choose on GitHub</button>');
    expect(page(okEntry(mac, github('connecting')))).toContain('Connecting from another phone…');
  });

  it('has This computer while its chunk loads, and nothing of herdr or GitHub on a computer that cannot be reached', () => {
    const away = page({ computer: vps, status: { kind: 'unreachable' } });
    expect(away).toContain('Can&#x27;t reach');
    expect(away).not.toContain('herdr');
    expect(away).not.toContain('GitHub');
  });
});

describe('where Settings is', () => {
  it('reads its own history entries, and the main page from any other', () => {
    expect(pageFromHistory(null)).toEqual(MAIN_PAGE);
    expect(pageFromHistory({ preview: true })).toEqual(MAIN_PAGE);
    expect(pageFromHistory({ [PAGE_KEY]: { kind: 'computer', origin: vps.origin } })).toEqual({ kind: 'computer', origin: vps.origin });
    expect(pageFromHistory({ [PAGE_KEY]: { kind: 'computer' } })).toEqual(MAIN_PAGE);
    expect(pageFromHistory({ [PAGE_KEY]: { kind: 'notifications' } })).toEqual({ kind: 'notifications' });
  });

  it('opens at Notifications, at Add a computer on the main page, or at the page of the history entry', () => {
    const computer = { [PAGE_KEY]: { kind: 'computer', origin: vps.origin } };
    expect(initialPage('notifications', computer)).toEqual({ kind: 'notifications' });
    expect(initialPage('add-computer', computer)).toEqual(MAIN_PAGE);
    expect(initialPage(undefined, computer)).toEqual({ kind: 'computer', origin: vps.origin });
  });

  it('shows the main page for Notifications while home cannot be reached', () => {
    expect(shownPage({ kind: 'notifications' }, [okEntry(mac)], false)).toEqual(MAIN_PAGE);
    expect(shownPage({ kind: 'notifications' }, [okEntry(mac)], true)).toEqual({ kind: 'notifications' });
  });

  it('shows the main page for a computer that left the list', () => {
    expect(shownPage({ kind: 'computer', origin: vps.origin }, [okEntry(mac)])).toEqual(MAIN_PAGE);
    expect(shownPage({ kind: 'computer', origin: vps.origin }, [okEntry(vps)])).toEqual({ kind: 'computer', origin: vps.origin });
  });
});

describe('This phone', () => {
  const settings = settingsWith({ hostBuild: 'host-1' });
  const render = (home: typeof settings | undefined, section?: 'notifications' | 'add-computer') => renderToStaticMarkup(
    <Settings
      settings={home}
      {...(section === undefined ? {} : { section })}
      entries={[okEntry(mac)]}
      fontSize={14}
      onFontSize={noop}
      gestures
      onGestures={noop}
      onBack={noop}
      onRenamed={noop}
      onFailure={() => ''}
      onLogout={async () => undefined}
      onCheck={() => Promise.resolve()}
      onComputersChanged={noop}
    />,
  );

  it('has Name, Notifications, Terminal text size, Terminal gestures, Build and Log out', () => {
    const html = render(settings);
    expect(html).toContain('<h2 id="phone-heading" class="caps">This phone</h2>');
    expect(html).toContain('<span>Name</span><span class="sc-value">phone ›</span>');
    expect(html).toContain('aria-expanded="false" aria-controls="device-name-form"');
    expect(html).toContain('<span>Notifications</span>');
    expect(html).toContain('<span id="font-heading">Terminal text size</span>');
    expect(html).toContain('aria-label="Smaller text"');
    expect(html).toContain('<output aria-live="polite" class="font-size">14 px</output>');
    expect(html).toContain('aria-label="Larger text"');
    // Terminal gestures: a switch, on by default.
    expect(html).toMatch(/<span id="gestures-label">Terminal gestures<\/span><button type="button" role="switch" class="switch" aria-checked="true" aria-labelledby="gestures-label">/u);
    expect(html).toContain('<dt>Web build</dt>');
    expect(html).toContain('<dt>Host build</dt><dd>host-1</dd>');
    expect(html).toContain('<h2 id="logout-heading" class="caps">Log out</h2>');
    // Notifications is on its own page: there, but hidden.
    expect(html).toMatch(/<div class="stack" hidden="">.*Notifications/u);
  });

  it('without home: only text size and build, and the line that says why', () => {
    const html = render(undefined);
    expect(html).toContain('Your home computer cannot be reached. Device name, notifications and log out need it.');
    expect(html).not.toContain('<span>Name</span>');
    expect(html).not.toContain('<span>Notifications</span>');
    expect(html).not.toContain('logout-heading');
    expect(html).not.toContain('<dt>Host build</dt>');
    expect(html).toContain('Terminal text size');
  });

  it('opens at Notifications with ‹ Settings, and shows the section', () => {
    const html = render(settings, 'notifications');
    expect(html).toContain('>‹ Settings</button>');
    expect(html).toContain('<div class="stack">');
    expect(html).not.toContain('phone-heading');
  });

  it('opened at Notifications without home: the main page, not a page with only ‹ Settings', () => {
    const html = render(undefined, 'notifications');
    expect(html).toContain('<h2 id="phone-heading" class="caps">This phone</h2>');
    expect(html).not.toContain('‹ Settings');
  });

  it('says On or Off for push once the browser answered', () => {
    expect(pushWord(undefined)).toBe('');
    expect(pushWord({ support: 'available', enabled: undefined, missing: [] })).toBe('');
    expect(pushWord({ support: 'available', enabled: true, missing: [] })).toBe('On');
    expect(pushWord({ support: 'available', enabled: false, missing: [] })).toBe('Off');
    expect(pushWord({ support: 'needs-home-screen', enabled: undefined, missing: [] })).toBe('Off');
  });
});

describe('the Chat view row on a computer\'s page', () => {
  const failure = () => 'failed';

  it('shows On with a switch to turn it off, and Off with the command and no switch (only the computer turns it on)', () => {
    const onRow = renderToStaticMarkup(<ul><ChatViewRow computer={mac} info={{ enabled: true }} onFailure={failure} /></ul>);
    expect(onRow).toContain('Chat view');
    expect(onRow).toMatch(/<button type="button" role="switch" class="switch" aria-checked="true" aria-labelledby="chat-[^"]+-label"/u);
    expect(onRow).toContain('Turning it off closes every open chat.');
    const offRow = renderToStaticMarkup(<ul><ChatViewRow computer={mac} info={{ enabled: false }} onFailure={failure} /></ul>);
    expect(offRow).not.toContain('role="switch"');
    expect(offRow).toContain('Off');
    expect(offRow).toContain('Turn it on at the computer: <code>pterm chat on</code>');
  });

  it('is there only for a computer at revision 8 that reports Chat', () => {
    expect(chatInfo({ protocolRevision: 8, chat: { enabled: false } })).toEqual({ enabled: false });
    expect(chatInfo({ protocolRevision: 7, chat: { enabled: true } })).toBeUndefined();
    expect(chatInfo({ protocolRevision: 8 })).toBeUndefined();
    expect(chatInfo(undefined)).toBeUndefined();
    const page = (extra: object) => renderToStaticMarkup(
      <ComputerPage entry={okEntry(mac, extra)} onBack={noop} onFailure={() => ''} onCheck={() => Promise.resolve()} onComputersChanged={noop} onRemoved={noop} />,
    );
    expect(page({ protocolRevision: 8, chat: { enabled: true } })).toContain('aria-label="Chat view"');
    expect(page({ protocolRevision: 8 })).not.toContain('aria-label="Chat view"');
  });
});
