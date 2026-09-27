import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse, KeyName } from '../generated/protocol';
import { sendAgentKey } from '../terminal/agentKeys';
import { canSendWith, Connection, INITIAL_STATUS } from '../terminal/Connection';
import { draftKey, readDraft, saveDraft } from '../terminal/composer';
import { InputController } from '../terminal/InputController';
import { runFirstCommand } from '../terminal/runFirst';
import { followCursor, TerminalView } from '../terminal/TerminalView';
import { hasNewOutput, markViewed, readViewed, type ViewedTimes } from './activity';
import { Composer } from './Composer';
import { CockpitDock, controlBadge, linkLabel, ReadOnlyDock } from './Dock';
import { History } from './History';
import { useKeyboardOpen, websocketUrl } from './hooks';
import type { RunFirstRequest } from './presets';
import { SessionMenu } from './SessionMenu';
import { Snippets } from './Snippets';
import { StateScreen, WindowArt } from './StateScreen';

interface Runtime {
  readonly connection: Connection;
  readonly view: TerminalView;
}

type Panel = 'none' | 'composer' | 'history' | 'snippets' | 'menu';

const noSubscribe = () => () => undefined;
const initialStatus = () => INITIAL_STATUS;
const sameSession = (a: BrokerRPCSessionRecord, b: BrokerRPCSessionRecord) => a.id === b.id && a.generation === b.generation;

/** Tabs: running sessions, plus the open one even after it exits. */
export function liveTabs(
  sessions: ReadonlyArray<BrokerRPCSessionRecord> | undefined,
  current: BrokerRPCSessionRecord,
): BrokerRPCSessionRecord[] {
  const running = (sessions ?? []).filter((item) => sameSession(item, current)
    || (item.state !== 'exited' && item.state !== 'lost'));
  return running.some((item) => sameSession(item, current)) ? running : [current, ...running];
}

/** Session tabs. Switching opens a new read-only connection; nothing carries over. */
export function TabBar({ tabs, current, viewed, onOpen, onNew, onMenu }: {
  readonly tabs: ReadonlyArray<BrokerRPCSessionRecord>;
  readonly current: BrokerRPCSessionRecord;
  readonly viewed: ViewedTimes;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onNew: () => void;
  readonly onMenu: () => void;
}) {
  return (
    <nav className="tabs" aria-label="Open sessions">
      <div className="tab-scroll">
        {tabs.map((tab) => {
          const active = sameSession(tab, current);
          const fresh = !active && hasNewOutput(tab, viewed);
          return (
            <button
              key={`${tab.id}/${tab.generation}`}
              type="button"
              className={active ? 'tab active' : 'tab'}
              {...(active ? { 'aria-current': 'page' as const } : {})}
              onClick={() => { if (!active) onOpen(tab); }}
            >
              <span className="tab-face">
                {active && <span className="tab-prompt" aria-hidden="true">❯</span>}
                <span className="tab-label">{tab.title}</span>
                {fresh && <><span className="dot accent" aria-hidden="true" /><span className="visually-hidden">, new output</span></>}
              </span>
            </button>
          );
        })}
      </div>
      <button type="button" className="tab tab-new" aria-label="New session" onClick={onNew}>
        <span className="tab-face">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
            <path d="M6 1v10M1 6h10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
          </svg>
        </span>
      </button>
      <span className="tab-spacer" />
      <button type="button" className="tab-more" aria-label="Session menu" aria-haspopup="dialog" onClick={onMenu}>
        <svg width="18" height="4" viewBox="0 0 18 4" aria-hidden="true">
          <circle cx="2" cy="2" r="1.6" fill="currentColor" />
          <circle cx="9" cy="2" r="1.6" fill="currentColor" />
          <circle cx="16" cy="2" r="1.6" fill="currentColor" />
        </svg>
      </button>
    </nav>
  );
}

export function Live({
  session, sessions, settings, fontSize, runFirst,
  onOpen, onNew, onSessions, onSettings, onChanged, onStartNew, onRevoked,
}: {
  readonly session: BrokerRPCSessionRecord;
  readonly sessions: ReadonlyArray<BrokerRPCSessionRecord> | undefined;
  readonly settings: HTTPSettingsResponse | undefined;
  readonly fontSize: number;
  readonly runFirst?: RunFirstRequest;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onNew: () => void;
  readonly onSessions: () => void;
  readonly onSettings: () => void;
  readonly onChanged: () => Promise<void>;
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
  const [panel, setPanel] = useState<Panel>('none');
  const [composerVersion, setComposerVersion] = useState(0);
  const [latched, setLatched] = useState({ ctrl: false, alt: false });
  const [runFirstNotice, setRunFirstNotice] = useState<string>();
  const keyboard = useKeyboardOpen();
  const keyboardRef = useRef(keyboard);
  keyboardRef.current = keyboard;
  // PTY rows measured without the keyboard. The keyboard never shrinks the
  // PTY, so full-screen programs do not redraw each time it opens or closes.
  const restRows = useRef<number | undefined>(undefined);

  const status = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getStatus ?? initialStatus,
  );
  const canSend = canSendWith(status);
  const draft = draftKey(session.id, session.generation);
  const current = sessions?.find((item) => sameSession(item, session)) ?? session;
  const projectName = settings?.projects.find((item) => item.id === session.projectId)?.name;
  const shellName = settings?.shellProfiles.find((item) => item.id === session.shellProfileId)?.name;

  // This device is looking at the session: its output is not "new" here.
  const latest = useRef(current);
  latest.current = current;
  useEffect(() => { markViewed(latest.current); }, [current]);
  useEffect(() => () => markViewed(latest.current), []);

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

  useEffect(() => {
    if (runtime === undefined || runFirst === undefined || runFirst.settled) {
      return undefined;
    }
    return runFirstCommand(runtime.connection, runFirst, setRunFirstNotice);
  }, [runtime, runFirst]);

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
        // offset* include a classic scrollbar, so a grid that briefly overflows
        // does not shrink the next measurement and start a resize loop.
        const size = runtime.view.fit(element.offsetWidth, element.offsetHeight);
        if (size === undefined) {
          return;
        }
        if (!keyboardRef.current || restRows.current === undefined) {
          restRows.current = size.rows;
        }
        runtime.connection.resize(size.columns, keyboardRef.current ? restRows.current : size.rows);
      }, 150);
    };
    const observer = new ResizeObserver(fit);
    observer.observe(element);
    fit();
    return () => {
      observer.disconnect();
      clearTimeout(timer);
    };
  }, [runtime, canSend, fontSize, panel === 'history', keyboard]);

  // With the keyboard up the grid is taller than the view: keep the cursor row in sight.
  useEffect(() => {
    const element = area.current;
    if (runtime === undefined || element === null || !keyboard) {
      return undefined;
    }
    const follow = () => {
      const band = runtime.view.cursorBand();
      if (band !== undefined) {
        element.scrollTop = followCursor(element.scrollTop, element.clientHeight, band);
      }
    };
    follow();
    const parsed = runtime.view.terminal.onWriteParsed(follow);
    const resized = runtime.view.terminal.onResize(follow);
    const observer = new ResizeObserver(follow);
    observer.observe(element);
    return () => {
      parsed.dispose();
      resized.dispose();
      observer.disconnect();
    };
  }, [runtime, keyboard]);

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

  const normalBuffer = status.buffer === 'normal';
  const historyEnabled = status.link === 'live';
  const sheet = panel === 'composer' || panel === 'snippets' || panel === 'menu';
  const badge = controlBadge(status);
  const place = [projectName, shellName, status.columns === undefined ? undefined : `${status.columns}×${status.rows}`]
    .filter((part) => part !== undefined).join(' · ');

  if (status.link === 'lost') {
    return (
      <StateScreen
        tone="red"
        label="Termivoa · session lost"
        art={<WindowArt mark="gone" />}
        title={<>This session<br />is gone</>}
        lead={`“${session.title}” ended on your computer. Nothing was re-run.`}
        actions={(
          <>
            <button type="button" className="state-pill" onClick={onSessions}>sessions</button>
            <button type="button" className="primary" onClick={onStartNew}>New session</button>
          </>
        )}
      />
    );
  }

  return (
    <main className={keyboard ? 'live keyboard' : 'live'}>
      {panel !== 'history' && (
        <>
          <TabBar
            tabs={liveTabs(sessions, session)}
            current={session}
            viewed={readViewed()}
            onOpen={onOpen}
            onNew={onNew}
            onMenu={() => setPanel('menu')}
          />
          <div className="status-line" role="status">
            <span className="place">
              <span className={status.link === 'live' ? 'dot green' : 'dot amber'} aria-hidden="true" />
              {status.link === 'live' ? place || 'Connected' : linkLabel(status)}
            </span>
            <span className={`badge-text ${badge.tone}`}>{badge.text}</span>
          </div>
        </>
      )}

      {status.notice !== undefined && (
        <div className="banner" role="alert">
          <span>{status.notice}</span>
          <button type="button" onClick={() => runtime?.connection.dismissNotice()}>Dismiss</button>
        </div>
      )}
      {runFirstNotice !== undefined && (
        <div className="banner" role="alert">
          <span>{runFirstNotice}</span>
          <button type="button" onClick={() => setRunFirstNotice(undefined)}>Dismiss</button>
        </div>
      )}

      <div
        ref={area}
        className={sheet ? 'terminal-area dim' : 'terminal-area'}
        hidden={panel === 'history'}
        onClick={() => {
          if (sheet) setPanel('none');
          else if (canSend) input.current?.focus();
        }}
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

      {panel !== 'history' && (
        <div className={sheet ? 'dock-slot covered' : 'dock-slot'}>
          {status.control === 'controller' ? (
            <CockpitDock
              compact={keyboard}
              disabled={!canSend}
              latched={latched}
              alternate={!normalBuffer}
              historyEnabled={historyEnabled}
              onAgentKey={(key) => { if (runtime !== undefined) sendAgentKey(runtime.connection, key); }}
              onKey={(key: KeyName) => controller.current?.pressKey(key)}
              onLatch={(modifier) => controller.current?.toggleLatch(modifier)}
              onWheel={(button) => runtime?.connection.sendWheel(button)}
              onType={() => { setPanel('none'); input.current?.focus(); }}
              onCompose={() => setPanel('composer')}
              onSnippets={() => setPanel('snippets')}
              onHistory={openHistory}
              onHideKeyboard={() => input.current?.blur()}
            />
          ) : (
            <ReadOnlyDock
              status={status}
              historyEnabled={historyEnabled}
              onTakeControl={() => runtime?.connection.takeControl()}
              onHistory={openHistory}
              onSessions={onSessions}
            />
          )}
        </div>
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
      {panel === 'snippets' && runtime !== undefined && (
        <Snippets target={runtime.connection} canSend={canSend} onClose={() => setPanel('none')} />
      )}
      {panel === 'menu' && (
        <SessionMenu
          session={current}
          controlling={status.control === 'controller'}
          onRelease={() => runtime?.connection.releaseControl()}
          onSessions={onSessions}
          onSettings={onSettings}
          onChanged={onChanged}
          onAuthFailure={() => onRevoked('This phone is no longer paired with your computer.')}
          onClose={() => setPanel('none')}
        />
      )}
    </main>
  );
}
