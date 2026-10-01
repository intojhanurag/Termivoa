import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { BrokerRPCSessionRecord, DiffLine, HTTPSettingsResponse, KeyName } from '../generated/protocol';
import { sendAgentKey } from '../terminal/agentKeys';
import {
  canSendWith, Connection, INITIAL_STATUS, type BranchesView, type ChangesView, type TasksView, type WorktreeRemoval,
} from '../terminal/Connection';
import { draftKey, readDraft, saveDraft } from '../terminal/composer';
import { InputController } from '../terminal/InputController';
import { tapTarget, type PathLink } from '../terminal/pathLinks';
import { runFirstCommand } from '../terminal/runFirst';
import { followCursor, TerminalView, wheelSteps } from '../terminal/TerminalView';
import { hasNewOutput, markViewed, readViewed, readWorked, recapLine, viewedKey, type RecapFacts, type ViewedTimes } from './activity';
import { AnswerCard, AnswerPill, openQuestion } from './AnswerCard';
import { askAboutFile, Changes } from './Changes';
import { Composer } from './Composer';
import { ChangesTabs, Files, type ChangesTab } from './Files';
import { canTakeControl, CockpitDock, controlBadge, linkLabel, ReadOnlyDock } from './Dock';
import { displayPath, sessionName, splitPath } from './folder';
import { History } from './History';
import { PreviewChip } from './PreviewChip';
import { autoDismiss, RecapCard } from './Recap';
import { useKeyboardOpen, websocketUrl } from './hooks';
import type { RunFirstRequest } from './presets';
import { SessionMenu } from './SessionMenu';
import { Snippets } from './Snippets';
import { StateScreen, WindowArt } from './StateScreen';
import { NewWorktree, WorktreeBadge } from './Worktree';

interface Runtime {
  readonly connection: Connection;
  readonly view: TerminalView;
}

type Panel = 'none' | 'composer' | 'history' | 'changes' | 'snippets' | 'menu' | 'worktree';

/** The away recap, once per visit: decided when live, then its changes are read, then shown. */
type Recap = { readonly phase: 'wait' | 'loading' | 'done' } | { readonly phase: 'shown'; readonly line: string };

/** A reconnect shorter than this shows only the amber dot, not the "Reconnecting…" text. */
export const QUIET_RECONNECT_MS = 1_500;

const noSubscribe = () => () => undefined;
const initialStatus = () => INITIAL_STATUS;
const noChanges = (): ChangesView | undefined => undefined;
const noTasks = (): TasksView | undefined => undefined;
const noBranches = (): BranchesView | undefined => undefined;
const noRemoval = (): WorktreeRemoval | undefined => undefined;
/** The Changes screen's last tab per session, while the app is open. */
const lastTabs = new Map<string, ChangesTab>();
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

/** Scrolls the open tab into the strip's view: no vertical page scroll, no focus change. */
export function revealTab(tab: Pick<Element, 'scrollIntoView'>): void {
  const still = globalThis.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true;
  tab.scrollIntoView({ inline: 'nearest', block: 'nearest', behavior: still ? 'auto' : 'smooth' });
}

/** Session tabs. Switching opens a new read-only connection; nothing carries over. */
export function TabBar({ tabs, current, viewed, liveCwd, onOpen, onNew, onMenu }: {
  readonly tabs: ReadonlyArray<BrokerRPCSessionRecord>;
  readonly current: BrokerRPCSessionRecord;
  readonly viewed: ViewedTimes;
  /** The active tab's live session.state cwd, when connected live. */
  readonly liveCwd?: string | undefined;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onNew: () => void;
  readonly onMenu: () => void;
}) {
  const open = useRef<HTMLButtonElement>(null);
  // Also when the open tab moves, e.g. the session list arrives after the screen opened.
  const openIndex = tabs.findIndex((tab) => sameSession(tab, current));
  useEffect(() => {
    if (open.current !== null) revealTab(open.current);
  }, [current.id, current.generation, openIndex]);
  return (
    <nav className="tabs" aria-label="Open sessions">
      <div className="tab-scroll">
        {tabs.map((tab) => {
          const active = sameSession(tab, current);
          // A waiting agent in another tab shows an amber dot instead of the new-output dot.
          const waiting = !active && tab.agent?.state === 'waiting';
          const fresh = !active && !waiting && hasNewOutput(tab, viewed);
          return (
            <button
              key={`${tab.id}/${tab.generation}`}
              ref={active ? open : undefined}
              type="button"
              className={active ? 'tab active' : 'tab'}
              {...(active ? { 'aria-current': 'page' as const } : {})}
              onClick={() => { if (!active) onOpen(tab); }}
            >
              <span className="tab-face">
                {active && <span className="tab-prompt" aria-hidden="true">❯</span>}
                <span className="tab-label">{sessionName(tab, active ? liveCwd : undefined)}</span>
                {fresh && <><span className="dot accent" aria-hidden="true" /><span className="visually-hidden">, new output</span></>}
                {waiting && <><span className="dot amber" aria-hidden="true" /><span className="visually-hidden">, waiting for you</span></>}
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
  session, sessions, settings, fontSize, runFirst, openComposer,
  onOpen, onNew, onSessions, onSettings, onChanged, onStartNew, onStartWorktree, onRevoked, onPreview,
}: {
  readonly session: BrokerRPCSessionRecord;
  readonly sessions: ReadonlyArray<BrokerRPCSessionRecord> | undefined;
  readonly settings: HTTPSettingsResponse | undefined;
  readonly fontSize: number;
  readonly runFirst?: RunFirstRequest;
  /** Open the composer once the terminal is ready (from the preview's "Tell agent"). */
  readonly openComposer?: boolean;
  readonly onOpen: (session: BrokerRPCSessionRecord) => void;
  readonly onNew: () => void;
  readonly onSessions: () => void;
  readonly onSettings: () => void;
  readonly onChanged: () => Promise<void>;
  /** A new session in the same project and shell; `runFirst` is typed once when it opens. */
  readonly onStartNew: (runFirst?: string) => void;
  /** Like onStartNew, in a new git worktree of this session's repository; rejects with the API error. */
  readonly onStartWorktree: (worktree: { readonly name: string; readonly base: string }, runFirst: string | undefined) => Promise<void>;
  readonly onRevoked: (reason: string | undefined) => void;
  /** Absent when this host serves no previews. */
  readonly onPreview?: (port: number) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const area = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const controller = useRef<InputController | undefined>(undefined);
  const initialFont = useRef(fontSize);
  const touchStart = useRef<number | undefined>(undefined);
  const touchLast = useRef<number | undefined>(undefined);
  const wheelCarry = useRef(0);
  const [runtime, setRuntime] = useState<Runtime>();
  const [panel, setPanel] = useState<Panel>(openComposer === true ? 'composer' : 'none');
  // History and Changes replace the whole screen.
  const fullPanel = panel === 'history' || panel === 'changes';
  const [composerVersion, setComposerVersion] = useState(0);
  const [latched, setLatched] = useState({ ctrl: false, alt: false });
  const [runFirstNotice, setRunFirstNotice] = useState<string>();
  // The question whose card the user folded away to look at the terminal.
  const [foldedQuestion, setFoldedQuestion] = useState<string>();
  const keyboard = useKeyboardOpen();
  const keyboardRef = useRef(keyboard);
  keyboardRef.current = keyboard;
  // Terminal links: the latest handler, and a tap that a link took.
  const openPath = useRef<(link: PathLink) => boolean>(() => false);
  const linkTapped = useRef(false);
  // PTY rows measured without the keyboard. The keyboard never shrinks the
  // PTY, so full-screen programs do not redraw each time it opens or closes.
  const restRows = useRef<number | undefined>(undefined);

  const status = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getStatus ?? initialStatus,
  );
  const canSend = canSendWith(status);
  const [slowLink, setSlowLink] = useState(false);
  useEffect(() => {
    setSlowLink(false);
    if (status.link === 'live') {
      return undefined;
    }
    const timer = setTimeout(() => setSlowLink(true), QUIET_RECONNECT_MS);
    return () => clearTimeout(timer);
  }, [status.link]);
  const draft = draftKey(session.id, session.generation);
  const [tab, setTab] = useState<ChangesTab>(() => lastTabs.get(draft) ?? 'changes');
  const current = sessions?.find((item) => sameSession(item, session)) ?? session;
  const projectName = settings?.projects.find((item) => item.id === session.projectId)?.name;
  const shellName = settings?.shellProfiles.find((item) => item.id === session.shellProfileId)?.name;

  // What this device saw last time, read before this visit marks the session viewed.
  const [before] = useState(() => ({ viewed: readViewed(), worked: readWorked() }));
  const [recap, setRecap] = useState<Recap>({ phase: 'wait' });
  const recapFacts = useRef<RecapFacts>(undefined);
  const recapCard = useRef<HTMLDivElement>(null);
  const changesView = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getChanges ?? noChanges,
  );
  const tasks = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getTasks ?? noTasks,
  );
  const branches = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getBranches ?? noBranches,
  );
  const removal = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getWorktreeRemoval ?? noRemoval,
  );

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
    const links = runtime?.view.linkPaths((link) => {
      if (openPath.current(link)) linkTapped.current = true;
    });
    return () => links?.dispose();
  }, [runtime]);

  // A tapped path is resolved inside the tap, so the files root is read ahead, on attach and on each cwd change.
  useEffect(() => {
    if (status.link === 'live') void runtime?.connection.filesRoot();
  }, [runtime, status.link, status.cwd]);

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
  }, [runtime, canSend, fontSize, fullPanel, keyboard]);

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

  // Once live, decide from broker facts only; a recap then reads the changes once.
  useEffect(() => {
    if (runtime === undefined || recap.phase !== 'wait' || status.link !== 'live') {
      return;
    }
    const key = viewedKey(latest.current);
    const facts: RecapFacts = {
      now: Date.now(),
      viewedAt: before.viewed[key],
      workedBefore: before.worked[key],
      agent: status.agent ?? latest.current.agent,
      fresh: hasNewOutput(latest.current, before.viewed),
    };
    const line = recapLine(facts);
    recapFacts.current = facts;
    if (line === undefined) {
      setRecap({ phase: 'done' });
    } else {
      setRecap(runtime.connection.openChanges() ? { phase: 'loading' } : { phase: 'shown', line });
    }
  }, [runtime, recap.phase, status.link, status.agent, before]);

  useEffect(() => {
    const facts = recapFacts.current;
    if (recap.phase !== 'loading' || facts === undefined || changesView?.state === 'loading') {
      return;
    }
    const summary = changesView?.summary;
    const files = summary?.state === 'ready' ? summary.files : [];
    const line = recapLine({
      ...facts,
      changes: {
        files: files.length,
        additions: files.reduce((sum, file) => sum + file.additions, 0),
        deletions: files.reduce((sum, file) => sum + file.deletions, 0),
      },
    });
    setRecap(line === undefined ? { phase: 'done' } : { phase: 'shown', line });
  }, [recap.phase, changesView]);

  useEffect(() => {
    if (recap.phase !== 'shown') {
      return undefined;
    }
    return autoDismiss(
      document,
      (node) => node instanceof Node && recapCard.current?.contains(node) === true,
      () => setRecap({ phase: 'done' }),
    );
  }, [recap.phase]);

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

  function showTab(next: ChangesTab) {
    lastTabs.set(draft, next);
    setTab(next);
  }

  /** Opens the Changes screen on a tab; the dock opens it on the last one. */
  function openChanges(on: ChangesTab = tab) {
    const connection = runtime?.connection;
    if (connection !== undefined && (on === 'changes' ? connection.openChanges() : connection.openFilesFolder(''))) {
      showTab(on);
      input.current?.blur();
      setPanel('changes');
    }
  }

  function switchTab(next: ChangesTab) {
    const connection = runtime?.connection;
    showTab(next);
    if (next === 'changes' && connection?.getChanges() === undefined) connection?.openChanges();
    if (next === 'files' && connection?.getFiles() === undefined) connection?.openFilesFolder('');
  }

  function closeChanges() {
    runtime?.connection.closeChanges();
    runtime?.connection.closeFiles();
    setPanel('none');
  }

  // Only fills the draft; the user reads it and taps Send.
  function askAbout(path: string, selected: ReadonlyArray<DiffLine> = []) {
    askAboutFile(draft, path, selected);
    runtime?.connection.closeChanges();
    runtime?.connection.closeFiles();
    setComposerVersion((value) => value + 1);
    setPanel('composer');
  }

  const normalBuffer = status.buffer === 'normal';
  // Full-screen apps (Claude Code, vim, less) scroll themselves: a swipe or
  // trackpad scroll becomes wheel steps. Needs control, since wheel steps are input.
  const appScroll = !normalBuffer && canSend && !keyboard;

  function scrollApp(travel: number) {
    const { steps, carry } = wheelSteps(wheelCarry.current, travel);
    wheelCarry.current = carry;
    for (let step = 0; step < Math.abs(steps); step++) {
      runtime?.connection.sendWheel(steps > 0 ? 'wheel-up' : 'wheel-down');
    }
  }
  const historyEnabled = status.link === 'live';
  const sheet = panel === 'composer' || panel === 'snippets' || panel === 'menu' || panel === 'worktree';
  const question = panel === 'none' ? openQuestion(status) : undefined;
  // With the keyboard up the card folds to its pill, so typing and keys keep working.
  const cardOpen = question !== undefined && !keyboard && foldedQuestion !== question.id;
  // A tapped path inside the files root opens in Files at its line. Any other
  // tap, and any tap on a dimmed terminal (a sheet or question card on top),
  // acts as before.
  openPath.current = (link) => {
    const connection = runtime?.connection;
    const target = tapTarget(panel === 'none' && !cardOpen, connection?.knownFilesRoot(), status.link === 'live' ? status.cwd : undefined, link);
    if (connection === undefined || target === undefined) return false;
    connection.closeFiles();
    if (!connection.openFilesFile(target.path, target.line)) return false;
    showTab('files');
    input.current?.blur();
    setPanel('changes');
    return true;
  };
  const badge = controlBadge(status);
  const liveCwd = status.link === 'live' ? status.cwd : undefined;
  const cwd = liveCwd ?? current.cwd;
  // The path is the priority once known; it stands in for the project name.
  const path = cwd === undefined ? undefined : displayPath(cwd);
  const placeHead = splitPath(path ?? projectName ?? '');
  const placeTail = [shellName, status.columns === undefined ? undefined : `${status.columns}×${status.rows}`]
    .filter((part) => part !== undefined).join(' · ');
  const showPlace = (placeHead.name !== '' || placeTail !== '') && (status.link === 'live' || !slowLink);

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
            <button type="button" className="primary" onClick={() => onStartNew()}>New session</button>
          </>
        )}
      />
    );
  }

  return (
    <main className={keyboard ? 'live keyboard' : 'live'}>
      {!fullPanel && (
        <>
          <TabBar
            tabs={liveTabs(sessions, session)}
            current={session}
            viewed={readViewed()}
            liveCwd={liveCwd}
            onOpen={onOpen}
            onNew={onNew}
            onMenu={() => setPanel('menu')}
          />
          <div className="status-line" role="status">
            <span className="place">
              <span className={status.link === 'live' ? 'dot green' : 'dot amber'} aria-hidden="true" />
              {showPlace ? (
                // Cut order: the parent folders (from their start), then shell and size, then the last folder.
                <span className="place-text">
                  {placeHead.dir !== '' && <span className="place-dir"><span dir="ltr">{placeHead.dir}</span></span>}
                  <span className="place-end">
                    {placeHead.name !== '' && <span className="place-name"><span dir="ltr">{placeHead.name}</span></span>}
                    {placeTail !== '' && <span className="place-tail">{placeHead.name === '' ? placeTail : ` · ${placeTail}`}</span>}
                  </span>
                </span>
              ) : status.link === 'live' ? 'Connected' : linkLabel(status)}
            </span>
            {current.worktree !== undefined && <WorktreeBadge worktree={current.worktree} />}
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

      {recap.phase === 'shown' && panel === 'none' && (
        <div ref={recapCard} className="recap-anchor">
          <RecapCard
            line={recap.line}
            onSeeChanges={() => { setRecap({ phase: 'done' }); openChanges('changes'); }}
            onDismiss={() => setRecap({ phase: 'done' })}
          />
        </div>
      )}

      <div
        ref={area}
        className={`terminal-area${sheet || cardOpen ? ' dim' : ''}${appScroll ? ' app-scroll' : ''}`}
        hidden={fullPanel}
        onClick={() => {
          const link = linkTapped.current;
          linkTapped.current = false;
          if (sheet) setPanel('none');
          else if (canSend && !link) input.current?.focus();
        }}
        onWheel={(event) => {
          if (appScroll) scrollApp(-event.deltaY);
          else if (event.deltaY < 0 && normalBuffer && area.current?.scrollTop === 0) openHistory();
        }}
        onTouchStart={(event) => {
          touchStart.current = event.touches[0]?.clientY;
          touchLast.current = touchStart.current;
          wheelCarry.current = 0;
        }}
        onTouchMove={(event) => {
          const start = touchStart.current;
          const now = event.touches[0]?.clientY;
          if (appScroll) {
            const last = touchLast.current;
            touchLast.current = now;
            if (last !== undefined && now !== undefined) scrollApp(now - last);
            return;
          }
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
      {panel === 'changes' && runtime !== undefined && (tab === 'changes' ? (
        <Changes connection={runtime.connection} cwd={cwd} switcher={<ChangesTabs tab={tab} onTab={switchTab} />} onAsk={askAbout} onReturn={closeChanges} />
      ) : (
        <Files connection={runtime.connection} switcher={<ChangesTabs tab={tab} onTab={switchTab} />} onAsk={askAbout} onReturn={closeChanges} />
      ))}

      {question !== undefined && status.agent !== undefined && (
        <div className="answer-anchor">
          {cardOpen ? (
            <AnswerCard
              title={current.title}
              question={question}
              stateSince={status.agent.stateSince}
              status={status}
              canTake={canTakeControl(status)}
              onAnswer={(index) => { runtime?.connection.sendAnswer(question.id, index); }}
              onTakeControl={() => runtime?.connection.takeControl()}
              onHide={() => setFoldedQuestion(question.id)}
            />
          ) : (
            <AnswerPill onOpen={() => { input.current?.blur(); setFoldedQuestion(undefined); }} />
          )}
        </div>
      )}

      {panel === 'none' && question === undefined && !keyboard && (
        <PreviewChip previews={status.link === 'live' ? status.previews : current.previews} onOpen={onPreview} />
      )}

      {!fullPanel && (
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
              onSnippets={() => {
                // One read of the project's commands each time the sheet opens.
                runtime?.connection.listTasks();
                setPanel('snippets');
              }}
              onHistory={openHistory}
              onChanges={() => openChanges()}
              onHideKeyboard={() => input.current?.blur()}
            />
          ) : (
            <ReadOnlyDock
              status={status}
              historyEnabled={historyEnabled}
              onTakeControl={() => runtime?.connection.takeControl()}
              onHistory={openHistory}
              onChanges={() => openChanges()}
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
        <Snippets
          target={runtime.connection}
          canSend={canSend}
          busy={!normalBuffer ? 'fullscreen' : status.agent?.state === 'working' || status.agent?.state === 'waiting' ? 'running' : undefined}
          tasks={tasks}
          onRetryTasks={() => runtime.connection.listTasks()}
          onRunTask={onStartNew}
          onClose={() => setPanel('none')}
        />
      )}
      {panel === 'menu' && (
        <SessionMenu
          session={current}
          controlling={status.control === 'controller'}
          running={(status.sessionState ?? current.state) === 'running'}
          removal={removal}
          onRelease={() => runtime?.connection.releaseControl()}
          onNewWorktree={() => {
            // One read of the repository's branches each time the sheet opens.
            runtime?.connection.listBranches();
            setPanel('worktree');
          }}
          onRemoveWorktree={(force) => runtime?.connection.removeWorktree(force) ?? false}
          onSessions={onSessions}
          onSettings={onSettings}
          onChanged={onChanged}
          onAuthFailure={() => onRevoked('This phone is no longer paired with your computer.')}
          onClose={() => setPanel('none')}
        />
      )}
      {panel === 'worktree' && runtime !== undefined && (
        <NewWorktree
          view={branches}
          onRetry={() => runtime.connection.listBranches()}
          onCreate={onStartWorktree}
          onAuthFailure={() => onRevoked('This phone is no longer paired with your computer.')}
          onClose={() => setPanel('none')}
        />
      )}
    </main>
  );
}
