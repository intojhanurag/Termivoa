import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse, KeyName } from '../generated/protocol';
import { canSendWith, Connection, INITIAL_STATUS, type ConnectionStatus } from '../terminal/Connection';
import { draftKey, readDraft, saveDraft } from '../terminal/composer';
import { InputController } from '../terminal/InputController';
import { TerminalView } from '../terminal/TerminalView';
import { Composer } from './Composer';
import { History } from './History';
import { websocketUrl } from './hooks';

interface Runtime {
  readonly connection: Connection;
  readonly view: TerminalView;
}

const noSubscribe = () => () => undefined;
const initialStatus = () => INITIAL_STATUS;

function linkLabel(status: ConnectionStatus): string {
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

function controlLabel(status: ConnectionStatus): string {
  if (status.sessionState === 'exited') {
    return status.exitCode === undefined ? 'Shell exited' : `Shell exited (code ${status.exitCode})`;
  }
  switch (status.control) {
    case 'controller': return 'You have control';
    case 'acquiring': return 'Requesting control…';
    case 'observer':
      return status.otherController
        ? `Viewing — ${status.controllerLabel ?? 'another connection'} has control`
        : 'Viewing (read-only)';
  }
}

/** One calm line that says why typing is off and what to do about it. */
export function readOnlyHint(status: ConnectionStatus): string | undefined {
  if (status.link !== 'live' || status.control !== 'observer'
    || status.sessionState === 'exited' || status.sessionState === 'exiting') {
    return undefined;
  }
  return status.otherController
    ? 'Tap Take control to move control to this phone.'
    : 'Read-only. Tap Take control to type.';
}

const ARROWS: ReadonlyArray<[KeyName, string, string]> = [
  ['arrow-left', '←', 'Left arrow'],
  ['arrow-up', '↑', 'Up arrow'],
  ['arrow-down', '↓', 'Down arrow'],
  ['arrow-right', '→', 'Right arrow'],
];

/** Buttons that must not take focus from the input textarea (keeps the keyboard up). */
function KeyButton({ label, onPress, disabled, pressed, children }: {
  readonly label: string;
  readonly onPress: () => void;
  readonly disabled: boolean;
  readonly pressed?: boolean;
  readonly children: ReactNode;
}) {
  return (
    <button
      type="button"
      className="key"
      aria-label={label}
      {...(pressed === undefined ? {} : { 'aria-pressed': pressed })}
      disabled={disabled}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onPress}
    >{children}</button>
  );
}

export function Live({ session, settings, fontSize, onSessions, onStartNew, onRevoked }: {
  readonly session: BrokerRPCSessionRecord;
  readonly settings: HTTPSettingsResponse | undefined;
  readonly fontSize: number;
  readonly onSessions: () => void;
  readonly onStartNew: () => void;
  readonly onRevoked: (reason: string | undefined) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const area = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const controller = useRef<InputController | undefined>(undefined);
  const initialFont = useRef(fontSize);
  const touchStart = useRef<number | undefined>(undefined);
  const [runtime, setRuntime] = useState<Runtime>();
  const [panel, setPanel] = useState<'none' | 'composer' | 'history'>('none');
  const [composerVersion, setComposerVersion] = useState(0);
  const [latched, setLatched] = useState({ ctrl: false, alt: false });

  const status = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getStatus ?? initialStatus,
  );
  const canSend = canSendWith(status);
  const draft = draftKey(session.id, session.generation);
  const projectName = settings?.projects.find((item) => item.id === session.projectId)?.name;

  useEffect(() => {
    const element = host.current;
    if (element === null) {
      return undefined;
    }
    const view = new TerminalView(initialFont.current);
    view.open(element);
    const connection = new Connection({
      url: websocketUrl(),
      sessionId: session.id,
      generation: session.generation,
      display: view.renderer,
    });
    connection.start();
    setRuntime({ connection, view });
    return () => {
      connection.detach();
      view.dispose();
      setRuntime(undefined);
    };
  }, [session.id, session.generation]);

  useEffect(() => { runtime?.view.setFontSize(fontSize); }, [runtime, fontSize]);

  useEffect(() => {
    const element = input.current;
    if (runtime === undefined || element === null) {
      return undefined;
    }
    const { connection } = runtime;
    const inputController = new InputController({
      key: (key, modifiers, character) => { connection.sendKey(key, modifiers, character); },
      text: (text) => { connection.sendText(text); },
      paste: (text) => {
        const existing = readDraft(draft);
        saveDraft(draft, existing === '' ? text : `${existing}\n${text}`);
        setComposerVersion((value) => value + 1);
        setPanel('composer');
      },
    }, setLatched);
    controller.current = inputController;
    const detach = inputController.attach(element);
    return () => {
      detach();
      controller.current = undefined;
    };
  }, [runtime, draft]);

  // Only the controller resizes the PTY, from the measured cell grid.
  useEffect(() => {
    const element = area.current;
    if (runtime === undefined || element === null || !canSend) {
      return undefined;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    const fit = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        const size = runtime.view.fit(element.clientWidth, element.clientHeight);
        if (size !== undefined) {
          runtime.connection.resize(size.columns, size.rows);
        }
      }, 150);
    };
    const observer = new ResizeObserver(fit);
    observer.observe(element);
    fit();
    return () => {
      observer.disconnect();
      clearTimeout(timer);
    };
  }, [runtime, canSend, fontSize, panel]);

  // Release control when the page is hidden; expiry covers missed events.
  useEffect(() => {
    if (runtime === undefined) {
      return undefined;
    }
    const release = () => runtime.connection.releaseControl();
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        release();
      }
    };
    window.addEventListener('pagehide', release);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      window.removeEventListener('pagehide', release);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [runtime]);

  useEffect(() => {
    if (status.link === 'revoked') {
      onRevoked(status.notice);
    }
  }, [status.link, status.notice, onRevoked]);

  function openHistory() {
    if (runtime?.connection.openHistory() === true) {
      input.current?.blur();
      setPanel('history');
    }
  }

  function closeHistory() {
    runtime?.connection.closeHistory();
    setPanel('none');
  }

  const press = (key: KeyName) => controller.current?.pressKey(key);
  const normalBuffer = status.buffer === 'normal';
  const hint = readOnlyHint(status);

  if (status.link === 'lost') {
    return (
      <main className="page">
        <section role="alert" className="stack">
          <h1>Session lost</h1>
          <p>
            “{session.title}” is no longer available. Its host restarted or the session generation changed.
            Programs from that session are not running here, and nothing was re-run.
          </p>
          <button type="button" className="primary" onClick={onStartNew}>Start new session</button>
          <button type="button" onClick={onSessions}>Back to sessions</button>
        </section>
      </main>
    );
  }

  return (
    <main className="live">
      <header className="live-bar">
        <div className="live-title">
          <strong>{session.title}</strong>
          {projectName !== undefined && <span className="muted"> · {projectName}</span>}
        </div>
        <div className="live-status" role="status">
          <span className={status.link === 'live' ? 'accent' : 'warn'}>{linkLabel(status)}</span>
          <span> · {controlLabel(status)}</span>
        </div>
      </header>

      {status.notice !== undefined && (
        <div className="banner" role="alert">
          <span>{status.notice}</span>
          <button type="button" onClick={() => runtime?.connection.dismissNotice()}>Dismiss</button>
        </div>
      )}

      <div
        ref={area}
        className="terminal-area"
        hidden={panel === 'history'}
        onClick={() => { if (canSend) input.current?.focus(); }}
        onWheel={(event) => { if (event.deltaY < 0 && normalBuffer && area.current?.scrollTop === 0) openHistory(); }}
        onTouchStart={(event) => { touchStart.current = event.touches[0]?.clientY; }}
        onTouchMove={(event) => {
          const start = touchStart.current;
          const now = event.touches[0]?.clientY;
          if (start !== undefined && now !== undefined && now - start > 48 && normalBuffer && area.current?.scrollTop === 0) {
            touchStart.current = undefined;
            openHistory();
          }
        }}
      >
        <div ref={host} className="terminal-host" aria-label="Terminal display" />
        <textarea
          ref={input}
          className="terminal-input"
          aria-label="Terminal keyboard input"
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
          spellCheck={false}
          data-gramm="false"
          enterKeyHint="send"
          readOnly={!canSend}
          rows={1}
        />
      </div>

      {panel === 'history' && runtime !== undefined && (
        <History connection={runtime.connection} fontSize={fontSize} onReturn={closeHistory} />
      )}
      {panel === 'composer' && runtime !== undefined && (
        <Composer
          key={composerVersion}
          connection={runtime.connection}
          canSend={canSend}
          draftKey={draft}
          onClose={() => setPanel('none')}
        />
      )}

      <nav className="action-bar" aria-label="Terminal controls">
        {hint !== undefined && panel !== 'history' && <p className="hint" role="status">{hint}</p>}
        {panel !== 'history' && (
          <div className="key-row" role="toolbar" aria-label="Terminal keys">
            <KeyButton label="Escape" disabled={!canSend} onPress={() => press('escape')}>Esc</KeyButton>
            <KeyButton label="Tab" disabled={!canSend} onPress={() => press('tab')}>Tab</KeyButton>
            <KeyButton label="Control modifier" disabled={!canSend} pressed={latched.ctrl}
              onPress={() => controller.current?.toggleLatch('ctrl')}>Ctrl</KeyButton>
            <KeyButton label="Alt modifier" disabled={!canSend} pressed={latched.alt}
              onPress={() => controller.current?.toggleLatch('alt')}>Alt</KeyButton>
            {ARROWS.map(([key, glyph, label]) => (
              <KeyButton key={key} label={label} disabled={!canSend} onPress={() => press(key)}>{glyph}</KeyButton>
            ))}
            {!normalBuffer && (
              <>
                <KeyButton label="Scroll application up" disabled={!canSend}
                  onPress={() => runtime?.connection.sendWheel('wheel-up')}>Scroll ▲</KeyButton>
                <KeyButton label="Scroll application down" disabled={!canSend}
                  onPress={() => runtime?.connection.sendWheel('wheel-down')}>Scroll ▼</KeyButton>
              </>
            )}
          </div>
        )}
        <div className="key-row">
          <button type="button" aria-label="Open keyboard" disabled={!canSend}
            onClick={() => { setPanel('none'); input.current?.focus(); }}>Type</button>
          <button type="button" aria-pressed={panel === 'composer'}
            onClick={() => setPanel(panel === 'composer' ? 'none' : 'composer')}>Compose</button>
          <button type="button" aria-pressed={panel === 'history'} disabled={status.link !== 'live' && panel !== 'history'}
            onClick={() => (panel === 'history' ? closeHistory() : openHistory())}>History</button>
          <button type="button" onClick={onSessions}>Sessions</button>
          {status.control === 'controller' ? (
            <button type="button" onClick={() => runtime?.connection.releaseControl()}>Release</button>
          ) : (
            <button
              type="button"
              className="primary"
              disabled={status.link !== 'live' || status.control === 'acquiring'
                || status.sessionState === 'exited' || status.sessionState === 'exiting'}
              onClick={() => runtime?.connection.takeControl()}
            >Take control</button>
          )}
        </div>
      </nav>
    </main>
  );
}
