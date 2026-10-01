import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AgentStatus, BrokerRPCSessionRecord } from '../generated/protocol';
import { AGENT_KEYS } from '../terminal/agentKeys';
import { INITIAL_STATUS, type ConnectionStatus } from '../terminal/Connection';
import { viewedKey } from './activity';
import { canTakeControl, CockpitDock, controlBadge, controllerLine, ReadOnlyDock } from './Dock';
import { liveTabs, revealTab, TabBar } from './Live';

const live: ConnectionStatus = { ...INITIAL_STATUS, link: 'live', control: 'observer', sessionState: 'running' };
const noop = () => undefined;

function dock(disabled: boolean, latched = { ctrl: false, alt: false }, alternate = false, compact = false) {
  return renderToStaticMarkup(
    <CockpitDock
      compact={compact}
      disabled={disabled}
      latched={latched}
      alternate={alternate}
      historyEnabled
      onAgentKey={noop}
      onKey={noop}
      onLatch={noop}
      onWheel={noop}
      onType={noop}
      onCompose={noop}
      onSnippets={noop}
      onHistory={noop}
      onChanges={noop}
      onHideKeyboard={noop}
    />,
  );
}

const keyButtons = (html: string) => [...html.matchAll(/<button type="button" class="key[^"]*" aria-label="([^"]+)"[^>]*>/gu)];

describe('read-only state', () => {
  it('names who has control and explains watching', () => {
    expect(controllerLine(live)).toEqual({ text: 'No one has control', tone: 'muted' });
    expect(controllerLine({ ...live, otherController: true, controllerLabel: 'MacBook' }))
      .toEqual({ text: 'MacBook has control', tone: 'amber' });
    expect(controllerLine({ ...live, otherController: true })).toEqual({ text: 'Another connection has control', tone: 'amber' });
    expect(controllerLine({ ...live, link: 'reconnecting' }).text).toBe('Reconnecting… typing is paused');
    expect(controllerLine({ ...live, sessionState: 'exited', exitCode: 2 }).text).toBe('Shell exited (code 2)');

    const html = renderToStaticMarkup(
      <ReadOnlyDock status={{ ...live, otherController: true, controllerLabel: 'MacBook' }} historyEnabled
        onTakeControl={noop} onHistory={noop} onChanges={noop} onSessions={noop} />,
    );
    expect(html).toContain('MacBook has control');
    expect(html).toContain('You are watching. The agent keeps working.');
    expect(html).toMatch(/<button type="button" class="primary take">.*Take control<\/button>/u);
  });

  it('shows CONTROL only for the controller and READ-ONLY otherwise', () => {
    expect(controlBadge({ ...live, control: 'controller' })).toEqual({ text: '● CONTROL', tone: 'accent' });
    expect(controlBadge(live)).toEqual({ text: 'READ-ONLY', tone: 'amber' });
    expect(controlBadge({ ...live, sessionState: 'exited' }).text).toBe('EXITED');
  });

  it('offers Take control only to a live observer of a running shell', () => {
    expect(canTakeControl(live)).toBe(true);
    expect(canTakeControl({ ...live, control: 'acquiring' })).toBe(false);
    expect(canTakeControl({ ...live, link: 'reconnecting' })).toBe(false);
    expect(canTakeControl({ ...live, sessionState: 'exited' })).toBe(false);
  });
});

describe('cockpit dock', () => {
  it('gives every glyph key a spoken name', () => {
    const names = keyButtons(dock(false)).map((match) => match[1]);
    expect(names).toEqual([
      'Escape', 'Control C', 'Shift Tab', 'Up arrow', '1', '2', 'y', 'Enter',
      'Tab', 'Control modifier', 'Alt modifier', 'Left arrow', 'Down arrow', 'Right arrow',
    ]);
    expect(AGENT_KEYS.map((key) => key.glyph)).toEqual(['esc', '⌃C', '⇧⇥', '↑', '1', '2', 'y', '⏎']);
    expect(dock(false)).not.toContain('>edit<');
  });

  it('disables every key without control', () => {
    const buttons = keyButtons(dock(true));
    expect(buttons).toHaveLength(14);
    for (const [tag] of buttons) {
      expect(tag).toContain('disabled=""');
    }
    expect(keyButtons(dock(false)).filter(([tag]) => tag.includes('disabled'))).toEqual([]);
  });

  it('shows the armed modifier and the application scroll keys on the alternate screen', () => {
    expect(dock(false, { ctrl: true, alt: false })).toMatch(/aria-label="Control modifier" aria-pressed="true"/u);
    expect(dock(false, undefined, true)).toContain('aria-label="Scroll application up"');
    expect(dock(false)).not.toContain('Scroll application');
  });
});

const record = (n: number, extra: Partial<BrokerRPCSessionRecord> = {}): BrokerRPCSessionRecord => ({
  id: `00000000-0000-4000-8000-00000000000${n}`,
  generation: `00000000-0000-4000-8000-00000000010${n}`,
  title: `s${n}`,
  projectId: 'p',
  shellProfileId: 'z',
  state: 'running',
  columns: 80,
  rows: 24,
  createdAt: '2026-09-27T10:00:00Z',
  updatedAt: '2026-09-27T10:00:00Z',
  ...extra,
});

describe('session tabs', () => {
  it('lists running sessions and keeps the open one even after it exits', () => {
    const open = record(1, { state: 'exited' });
    const list = [open, record(2), record(3, { state: 'lost' }), record(4)];
    expect(liveTabs(list, open).map((item) => item.title)).toEqual(['s1', 's2', 's4']);
    expect(liveTabs(undefined, open)).toEqual([open]);
    expect(liveTabs([record(2)], record(1)).map((item) => item.title)).toEqual(['s1', 's2']);
  });

  it('marks the open tab and shows a new-output dot on other tabs only', () => {
    const open = record(1, { lastOutputAt: '2026-09-27T10:05:00Z' });
    const other = record(2, { lastOutputAt: '2026-09-27T10:05:00Z' });
    const seen = record(3, { lastOutputAt: '2026-09-27T10:05:00Z' });
    const viewed = { [viewedKey(seen)]: Date.parse('2026-09-27T10:06:00Z') };
    const html = renderToStaticMarkup(
      <TabBar tabs={[open, other, seen]} current={open} viewed={viewed} onOpen={noop} onNew={noop} onMenu={noop} />,
    );
    expect(html).toContain('class="tab active" aria-current="page"');
    expect(html.match(/, new output/gu)).toHaveLength(1);
    expect(html).toMatch(/s2<\/span><span class="dot accent"/u);
    expect(html).toContain('aria-label="New session"');
    expect(html).toContain('aria-label="Session menu"');
  });

  it('shows the cwd basename as the tab label, the live cwd for the active tab, and the title when custom', () => {
    const open = record(1, { cwd: '/Users/me/src/app' });
    const custom = record(2, { title: 'My shell', titleIsCustom: true, cwd: '/Users/me/other' });
    const stale = record(3, { cwd: '/Users/me/stale' });
    const html = renderToStaticMarkup(
      <TabBar
        tabs={[open, custom, stale]}
        current={open}
        viewed={{}}
        liveCwd="/Users/me/fresh"
        onOpen={noop}
        onNew={noop}
        onMenu={noop}
      />,
    );
    expect(html).toContain('<span class="tab-label">fresh</span>');
    expect(html).toContain('<span class="tab-label">My shell</span>');
    expect(html).toContain('<span class="tab-label">stale</span>');
  });

  it('shows an amber waiting dot instead of the new-output dot on another waiting tab', () => {
    const agent: AgentStatus = { state: 'waiting', stateSince: '2026-09-27T10:05:00Z', question: { id: 'q', text: 'Proceed?', options: [{ label: 'Yes' }] } };
    const open = record(1, { agent });
    const other = record(2, { agent, lastOutputAt: '2026-09-27T10:05:00Z' });
    const html = renderToStaticMarkup(
      <TabBar tabs={[open, other]} current={open} viewed={{}} onOpen={noop} onNew={noop} onMenu={noop} />,
    );
    expect(html.match(/, waiting for you/gu)).toHaveLength(1);
    expect(html).toMatch(/s2<\/span><span class="dot amber"/u);
    expect(html).not.toContain(', new output');
  });

  describe('revealTab', () => {
    afterEach(() => { vi.unstubAllGlobals(); });
    const reveal = (reduce: boolean) => {
      vi.stubGlobal('matchMedia', (query: string) => ({ matches: reduce && query === '(prefers-reduced-motion: reduce)' }));
      const scrollIntoView = vi.fn();
      revealTab({ scrollIntoView });
      return scrollIntoView.mock.calls;
    };

    it('scrolls only as far as needed, smoothly', () => {
      expect(reveal(false)).toEqual([[{ inline: 'nearest', block: 'nearest', behavior: 'smooth' }]]);
    });

    it('jumps without animation when the user prefers reduced motion', () => {
      expect(reveal(true)).toEqual([[{ inline: 'nearest', block: 'nearest', behavior: 'auto' }]]);
    });
  });
});

describe('keyboard-up dock', () => {
  it('folds every key into one row, drops the action row, and can hide the keyboard', () => {
    const html = dock(false, { ctrl: false, alt: false }, true, true);
    expect(html).toContain('class="dock compact"');
    expect(html).not.toContain('action-row');
    expect(html).not.toContain('Agent keys');
    const labels = keyButtons(html).map((match) => match[1]);
    for (const key of AGENT_KEYS) expect(labels).toContain(key.name);
    expect(labels).toEqual(expect.arrayContaining(['Tab', 'Control modifier', 'Left arrow', 'Scroll application up', 'Snippets', 'Hide keyboard']));
  });

  it('puts Down next to Up, once, so both are in reach while typing', () => {
    const labels = keyButtons(dock(false, { ctrl: false, alt: false }, false, true)).map((match) => match[1]);
    expect(labels.slice(0, 6)).toEqual(['Escape', 'Control C', 'Shift Tab', 'Up arrow', 'Down arrow', '1']);
    expect(labels.filter((label) => label === 'Down arrow')).toHaveLength(1);
    expect(labels).toEqual(expect.arrayContaining(['Left arrow', 'Right arrow']));
  });
});
