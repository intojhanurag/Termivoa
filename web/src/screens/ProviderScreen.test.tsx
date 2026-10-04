import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/http';
import { INITIAL_STATUS, type ConnectionStatus } from '../terminal/Connection';
import { CockpitDock, ReadOnlyDock } from './Dock';
import { ProviderEnded } from './Live';
import { item, mac, ready, target } from './providerFixtures';
import { openProblem, phoneGrid, ProviderChips, providerHosted, ProviderScreen, sizeLine } from './ProviderScreen';

const noop = () => undefined;
const screen = (tapped: boolean, entry = ready(mac)) => renderToStaticMarkup(
  <ProviderScreen
    computer={mac} provider="herdr" targetId="w8:p1.term_b1" tapped={tapped} entry={entry} fontSize={13} multi={false}
    onChip={noop} onGone={noop} onSessions={noop} onSettings={noop} onFailure={() => 'x'} onRevoked={noop}
  />,
);

describe('a provider terminal before it opens', () => {
  it('after a link or a reload: shows its names and Open, and opens nothing', () => {
    const page = screen(false);
    expect(page).toContain('<h1>silver-meadow</h1>');
    expect(page).toContain('<dt>Project</dt><dd>shop</dd>');
    expect(page).toContain('<dt>Agent</dt><dd>claude</dd>');
    expect(page).toContain('mac shows this terminal at phone size while it is open here.');
    expect(page).toMatch(/<button type="button" class="primary wide"><span class="btn-swap"><span>Open<\/span>/u);
    expect(page).not.toContain('aria-busy');
  });

  it('after a tap: busy at once (it opens), so a second tap does nothing', () => {
    expect(screen(true)).toContain('aria-busy="true"');
  });

  it('chips are the workspace\'s tabs; a tab this herdr cannot open is off and says why', () => {
    const page = renderToStaticMarkup(
      <ProviderChips
        item={item('w1', 'main', [target('w1:p1.a', 'blocked', { agent: 'claude', label: '1' }), target('w1:p2.b', 'working', { agent: 'codex', label: '1' }), target('w1:p3.c', 'none', { label: 'logs', canOpen: false })])}
        current="w1:p2.b" onChip={noop} onSessions={noop}
      />,
    );
    expect(page).toContain('‹ Sessions');
    expect(page).toContain('<span class="tab-label">1 · claude</span><span class="dot amber" aria-hidden="true"></span><span class="visually-hidden">, blocked</span>');
    expect(page).toContain('aria-current="page"');
    expect(page).toContain('title="Update herdr to open this tab"');
    expect(page.match(/disabled=""/gu)).toHaveLength(1);
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
    expect(openProblem(new ApiError(429, 'limit_exceeded', 'x'), 'herdr', 'mac')?.text).toBe('Too many herdr terminals are open on mac. Close one, or wait a few seconds.');
    expect(openProblem(new ApiError(503, 'host_unavailable', 'x'), 'herdr', 'mac')?.text).toBe('herdr is not running on mac.');
    expect(openProblem(new ApiError(0, 'network', 'x'), 'herdr', 'mac')).toBeUndefined();
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
