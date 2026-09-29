import type { ReactNode } from 'react';
import type { KeyName } from '../generated/protocol';
import { AGENT_KEYS, type AgentKey } from '../terminal/agentKeys';
import type { ConnectionStatus } from '../terminal/Connection';

/** A keycap that never takes focus from the input textarea, so the keyboard stays up. */
export function KeyButton({ label, onPress, disabled, pressed, className, weight, children }: {
  readonly label: string;
  readonly onPress: () => void;
  readonly disabled: boolean;
  readonly pressed?: boolean;
  readonly className?: string;
  readonly weight?: number;
  readonly children: ReactNode;
}) {
  return (
    <button
      type="button"
      className={className === undefined ? 'key' : `key ${className}`}
      aria-label={label}
      {...(pressed === undefined ? {} : { 'aria-pressed': pressed })}
      {...(weight === undefined ? {} : { style: { flexGrow: weight } })}
      disabled={disabled}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onPress}
    >{children}</button>
  );
}

const MODIFIER_ARROWS: ReadonlyArray<[KeyName, string, string]> = [
  ['arrow-left', '←', 'Left arrow'],
  ['arrow-down', '↓', 'Down arrow'],
  ['arrow-right', '→', 'Right arrow'],
];

/** The in-control dock: agent keys, modifiers and arrows, and the five actions. */
export function CockpitDock({
  compact, disabled, latched, alternate, historyEnabled,
  onAgentKey, onKey, onLatch, onWheel, onType, onCompose, onSnippets, onHistory, onChanges, onHideKeyboard,
}: {
  /** The keyboard is up: one scrolling row of keys, so the terminal keeps its space. */
  readonly compact: boolean;
  readonly disabled: boolean;
  readonly latched: { readonly ctrl: boolean; readonly alt: boolean };
  readonly alternate: boolean;
  /** History and changes read over the live socket. */
  readonly historyEnabled: boolean;
  readonly onAgentKey: (key: AgentKey) => void;
  readonly onKey: (key: KeyName) => void;
  readonly onLatch: (modifier: 'ctrl' | 'alt') => void;
  readonly onWheel: (button: 'wheel-up' | 'wheel-down') => void;
  readonly onType: () => void;
  readonly onCompose: () => void;
  readonly onSnippets: () => void;
  readonly onHistory: () => void;
  readonly onChanges: () => void;
  readonly onHideKeyboard: () => void;
}) {
  const agentKeys = AGENT_KEYS.map((key) => (
    <KeyButton
      key={key.id}
      label={key.name}
      disabled={disabled}
      weight={key.weight}
      {...(key.tone === undefined ? {} : { className: key.tone })}
      onPress={() => onAgentKey(key)}
    >
      <span className="glyph">{key.glyph}</span>
      {key.sub !== undefined && <span className="sub">{key.sub}</span>}
    </KeyButton>
  ));
  const modifierKeys = (
    <>
      <KeyButton label="Tab" disabled={disabled} onPress={() => onKey('tab')}>tab</KeyButton>
      <KeyButton label="Control modifier" disabled={disabled} pressed={latched.ctrl} onPress={() => onLatch('ctrl')}>ctrl</KeyButton>
      <KeyButton label="Alt modifier" disabled={disabled} pressed={latched.alt} onPress={() => onLatch('alt')}>alt</KeyButton>
      {MODIFIER_ARROWS.map(([name, glyph, label]) => (
        <KeyButton key={name} label={label} disabled={disabled} weight={0.8} onPress={() => onKey(name)}>{glyph}</KeyButton>
      ))}
      {alternate && (
        <>
          <KeyButton label="Scroll application up" disabled={disabled} weight={0.8} onPress={() => onWheel('wheel-up')}>▲</KeyButton>
          <KeyButton label="Scroll application down" disabled={disabled} weight={0.8} onPress={() => onWheel('wheel-down')}>▼</KeyButton>
        </>
      )}
    </>
  );
  if (compact) {
    return (
      <section className="dock compact" aria-label="Terminal keys">
        <div className="key-row compact-row" role="group" aria-label="Keys">
          {agentKeys}
          {modifierKeys}
          <KeyButton label="Snippets" disabled={false} className="snips-key" onPress={onSnippets}>⌘ snips</KeyButton>
        </div>
        <KeyButton label="Hide keyboard" disabled={false} className="hide-keyboard" onPress={onHideKeyboard}>
          <svg width="20" height="16" viewBox="0 0 20 16" aria-hidden="true">
            <rect x="1" y="1" width="18" height="9" rx="2" fill="none" stroke="currentColor" strokeWidth="1.4" />
            <path d="M7 12.5l3 2.5 3-2.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
          </svg>
        </KeyButton>
      </section>
    );
  }
  return (
    <section className="dock" aria-label="Terminal keys">
      <p className="caps dock-label" id="agent-keys-label">Agent keys</p>
      <div className="key-row" role="group" aria-labelledby="agent-keys-label">{agentKeys}</div>
      <div className="key-row" role="group" aria-label="Modifier and arrow keys">{modifierKeys}</div>
      <div className="action-row">
        <button type="button" className="action primary" disabled={disabled} onClick={onType}>
          <span aria-hidden="true" className="glyph">❯</span>type
        </button>
        <button type="button" className="action" onClick={onCompose}>
          <span aria-hidden="true" className="glyph">¶</span>compose
        </button>
        <button type="button" className="action" onClick={onSnippets}>
          <span aria-hidden="true" className="glyph">⌘</span>snips
        </button>
        <button type="button" className="action" disabled={!historyEnabled} onClick={onHistory}>
          <span aria-hidden="true" className="glyph">↺</span>history
        </button>
        <button type="button" className="action" disabled={!historyEnabled} onClick={onChanges}>
          <span aria-hidden="true" className="glyph">±</span>changes
        </button>
      </div>
    </section>
  );
}

export function linkLabel(status: ConnectionStatus): string {
  switch (status.link) {
    case 'connecting': return 'Connecting…';
    case 'attaching': return 'Loading screen…';
    case 'live': return 'Connected';
    case 'reconnecting': return 'Reconnecting… typing is paused';
    case 'lost': return 'Session lost';
    case 'revoked': return 'Access revoked';
    case 'closed': return 'Disconnected';
  }
}

const shellEnded = (status: ConnectionStatus) => status.sessionState === 'exited' || status.sessionState === 'exiting';

/** Who holds control, as the read-only dock says it. */
export function controllerLine(status: ConnectionStatus): { readonly text: string; readonly tone: 'amber' | 'muted' } {
  if (shellEnded(status)) {
    return { text: status.exitCode === undefined ? 'Shell exited' : `Shell exited (code ${status.exitCode})`, tone: 'muted' };
  }
  if (status.link !== 'live') return { text: linkLabel(status), tone: 'amber' };
  if (status.otherController) return { text: `${status.controllerLabel ?? 'Another connection'} has control`, tone: 'amber' };
  return { text: 'No one has control', tone: 'muted' };
}

/** The short label at the right of the status line. */
export function controlBadge(status: ConnectionStatus): { readonly text: string; readonly tone: 'accent' | 'amber' | 'muted' } {
  if (shellEnded(status)) return { text: 'EXITED', tone: 'muted' };
  if (status.control === 'controller') return { text: '● CONTROL', tone: 'accent' };
  if (status.control === 'acquiring') return { text: 'TAKING CONTROL…', tone: 'muted' };
  return { text: 'READ-ONLY', tone: 'amber' };
}

export function canTakeControl(status: ConnectionStatus): boolean {
  return status.link === 'live' && status.control === 'observer' && !shellEnded(status);
}

/** The observer dock: who has control, what watching means, and Take control. */
export function ReadOnlyDock({ status, historyEnabled, onTakeControl, onHistory, onChanges, onSessions }: {
  readonly status: ConnectionStatus;
  readonly historyEnabled: boolean;
  readonly onTakeControl: () => void;
  readonly onHistory: () => void;
  readonly onChanges: () => void;
  readonly onSessions: () => void;
}) {
  const controller = controllerLine(status);
  const ended = shellEnded(status);
  return (
    <section className="dock readonly" aria-label="Control">
      <p className={`controller-line ${controller.tone}`} role="status">
        <span className={`dot ${controller.tone}`} aria-hidden="true" />{controller.text}
      </p>
      <p className="watching">
        <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
          <path d="M1.5 9S4.2 3.5 9 3.5 16.5 9 16.5 9 13.8 14.5 9 14.5 1.5 9 1.5 9Z" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <circle cx="9" cy="9" r="2.4" fill="none" stroke="currentColor" strokeWidth="1.5" />
        </svg>
        {ended ? 'The shell ended. Nothing more runs in this session.' : 'You are watching. The agent keeps working.'}
      </p>
      <button type="button" className="primary take" disabled={!canTakeControl(status)} onClick={onTakeControl}>
        <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
          <path d="M5 8V5.5a4 4 0 0 1 7.6-1.7" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
          <rect x="3" y="8" width="12" height="8" rx="2" fill="currentColor" />
        </svg>
        {status.control === 'acquiring' ? 'Taking control…' : 'Take control'}
      </button>
      <div className="spread">
        <button type="button" className="link" disabled={!historyEnabled} onClick={onHistory}>History</button>
        <button type="button" className="link" disabled={!historyEnabled} onClick={onChanges}>Changes</button>
        <button type="button" className="link" onClick={onSessions}>Sessions</button>
      </div>
    </section>
  );
}
