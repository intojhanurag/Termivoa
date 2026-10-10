import '@xterm/xterm/css/xterm.css';
import { lazy, Suspense, useEffect, type ComponentProps, type ComponentType, type ReactNode, useRef, useState, useSyncExternalStore } from 'react';
import { FEATURE_REVISION, type HTTPSettingsResponse, type KeyName } from '../generated/protocol';
import { parseAddComputerLink, type AddComputerLink } from '../pairing/addComputer';
import { sendAgentKey } from '../terminal/agentKeys';
import {
  canSendWith, Connection, initialStatus, noSubscribe, type BranchesView, type ChangesView, type TasksView, type WorktreeRemoval,
} from '../terminal/Connection';
import { draftKey, readDraft, saveDraft } from '../terminal/composer';
import { TouchGestures, sessionSwipe, tapArrows, type GestureHost } from '../terminal/gestures';
import { InputController, NO_MODIFIERS } from '../terminal/InputController';
import { tapTarget, type PathLink } from '../terminal/pathLinks';
import { runFirstCommand } from '../terminal/runFirst';
import { scheduler } from '../terminal/scheduler';
import { AppScroll, followCursor, TerminalView } from '../terminal/TerminalView';
import { hasNewOutput, markViewed, readViewed, readWorked, recapLine, viewedKey, type RecapFacts, type ViewedTimes } from './activity';
import { AddComputerSheet } from './AddComputer';
import { keyOf, sameSession, type HostedSession } from './allSessions';
import { AnswerCard, AnswerPill, openQuestion } from './AnswerCard';
import { loadChunk } from './chunks';
import { Composer } from './Composer';
import type { ChangesTab } from './Files';
import { canTakeControl, CockpitDock, controlBadge, linkLabel, ReadOnlyDock } from './Dock';
import { displayPath, sessionName, splitPath } from './folder';
import { History } from './History';
import { nextFilled, QueueNext, type Queue } from './Inbox';
import { PreviewChip } from './PreviewChip';
import { autoDismiss, RecapCard } from './Recap';
import { useKeyboardOpen, websocketUrl } from './hooks';
import type { RunFirstRequest } from './presets';
import { ComputerTag, ended } from './Sessions';
import { SessionMenu } from './SessionMenu';
import { Snippets } from './Snippets';
import { StateScreen, WindowArt } from './StateScreen';
import { Button } from './ui';
import { NewWorktree, WorktreeBadge } from './Worktree';

// Changes and Files are the only screens that open from a tap and need none of
// the first terminal frame, so they load when first opened.
// The screen replaces the terminal and dock, so a wait or a failed load keeps a Back button.
// React.lazy keeps a rejected promise, so opening Changes after a failed load makes a new lazy.
type ChangesScreenProps = ComponentProps<typeof import('./ChangesScreen').ChangesScreen>;

export function ChangesWait({ failed, onReturn }: { readonly failed: boolean; readonly onReturn: () => void }) {
  return (
    <div className="page">
      <p role="status" className="muted">{failed ? 'Could not load this screen. Check your connection and try again.' : 'Opening…'}</p>
      <button type="button" className="link strong" onClick={onReturn}>Back</button>
    </div>
  );
}

function lazyChangesScreen() {
  return lazy(async (): Promise<{ default: ComponentType<ChangesScreenProps> }> => {
    try {
      return { default: (await loadChunk(() => import('./ChangesScreen'))).ChangesScreen };
    } catch {
      changesLoadFailed = true;
      return { default: ({ onReturn }) => <ChangesWait failed onReturn={onReturn} /> };
    }
  });
}
let ChangesScreen = lazyChangesScreen();
let changesLoadFailed = false;

// Chat (docs/chat-view.md) loads when first shown; a failed load shows the app's update screen.
const ChatScreen = lazy(async () => ({ default: (await loadChunk(() => import('./Chat'))).ChatScreen }));

export type ViewMode = 'chat' | 'terminal';

/** The changed file an edit in Chat names: the same path, or the one that ends with it (the session may sit below the repository top). */
export function changedPath(paths: ReadonlyArray<string>, path: string): string | undefined {
  return paths.find((item) => item === path) ?? paths.find((item) => item.endsWith(`/${path}`));
}

/** Chat or Terminal per session (keyed like drafts), while the app is open. Never stored. */
const viewModes = new Map<string, ViewMode>();
/** Sessions whose agent Chat cannot read: no switch after leaving the Chat screen. */
const noChat = new Set<string>();

/**
 * The Chat | Terminal switch shows for a session of a computer that has Chat
 * on (revision 8 and settings.chat.enabled), or while Chat is on screen, and
 * not after the computer said it cannot read that session's agent. The host
 * decides what has an agent: the phone's agent state counts any output as
 * work, so it cannot tell a shell from an agent.
 */
export function chatOffered(settings: Pick<HTTPSettingsResponse, 'protocolRevision' | 'chat'> | undefined, facts: {
  readonly chatShown: boolean;
  readonly unsupported: boolean;
  readonly provider: boolean;
}): boolean {
  if (settings === undefined || settings.protocolRevision < FEATURE_REVISION.chat || settings.chat === undefined || facts.provider) return false;
  return facts.chatShown || (settings.chat.enabled && !facts.unsupported);
}

/** The segmented Chat | Terminal switch under the tabs. */
export function ViewSwitch({ mode, onMode }: { readonly mode: ViewMode; readonly onMode: (mode: ViewMode) => void }) {
  return (
    <span className="view-switch" role="group" aria-label="View">
      {(['chat', 'terminal'] as const).map((value) => (
        <button key={value} type="button" className={mode === value ? 'active' : undefined} aria-pressed={mode === value} onClick={() => onMode(value)}>
          {value === 'chat' ? 'Chat' : 'Terminal'}
        </button>
      ))}
    </span>
  );
}

interface Runtime {
  readonly connection: Connection;
  readonly view: TerminalView;
}

type Panel = 'none' | 'composer' | 'history' | 'changes' | 'snippets' | 'menu' | 'worktree' | 'add';

/** The away recap, once per visit: decided when live, then its changes are read, then shown. */
type Recap = { readonly phase: 'wait' | 'loading' | 'done' } | { readonly phase: 'shown'; readonly line: string };

/** A reconnect shorter than this shows only the amber dot, not the "Reconnecting…" text. */
export const QUIET_RECONNECT_MS = 1_500;

const noChanges = (): ChangesView | undefined => undefined;
const noTasks = (): TasksView | undefined => undefined;
const noBranches = (): BranchesView | undefined => undefined;
const noRemoval = (): WorktreeRemoval | undefined => undefined;
/** The Changes screen's last tab per session (keyed like drafts: host/id/generation), while the app is open. */
const lastTabs = new Map<string, ChangesTab>();

/** Tabs: running sessions of every computer, plus the open one even after it exits. */
export function liveTabs(
  sessions: ReadonlyArray<HostedSession> | undefined,
  current: HostedSession,
): HostedSession[] {
  const running = (sessions ?? []).filter((item) => sameSession(item, current) || !ended(item));
  return running.some((item) => sameSession(item, current)) ? running : [current, ...running];
}

/** Session tabs. Switching opens a new read-only connection; nothing carries over. */
export function TabBar({ tabs, current, viewed, liveCwd, onOpen, onNew, onMenu }: {
  readonly tabs: ReadonlyArray<HostedSession>;
  readonly current: HostedSession;
  readonly viewed: ViewedTimes;
  /** The active tab's live session.state cwd, when connected live. */
  readonly liveCwd?: string | undefined;
  readonly onOpen: (session: HostedSession) => void;
  readonly onNew: () => void;
  readonly onMenu: () => void;
}) {
  const open = useRef<HTMLButtonElement>(null);
  // Also when the open tab moves, e.g. the session list arrives after the screen opened.
  const openIndex = tabs.findIndex((tab) => sameSession(tab, current));
  // Only as far as needed: no vertical page scroll, no focus change. Smooth
  // unless the user prefers reduced motion (CSS scroll-behavior on .tab-scroll).
  useEffect(() => {
    open.current?.scrollIntoView({ inline: 'nearest', block: 'nearest' });
  }, [current.id, current.generation, current.computer.host, openIndex]);
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
              key={`${keyOf(tab)}/${tab.generation}`}
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

/**
 * A click on the terminal area. A link tap (path or add-computer) opens its
 * sheet or panel on mouseup, and the click of that same tap follows: the link
 * consumes it, so it never closes what it just opened. Otherwise a click
 * closes an open sheet, or focuses the input.
 */
export function areaClick(linkTook: boolean, sheet: boolean, canSend: boolean): 'none' | 'close' | 'focus' {
  if (linkTook) return 'none';
  if (sheet) return 'close';
  return canSend ? 'focus' : 'none';
}

/** A click this soon after a gesture took its touch belongs to that touch. */
const TOOK_TAP_MS = 600;

/** The session ended for good. New session stays busy until the new one opens (or fails), so one tap starts one session. */
export function SessionLost({ title, where, onSessions, onStartNew }: {
  readonly title: string;
  readonly where: string;
  readonly onSessions: () => void;
  readonly onStartNew: () => Promise<void>;
}) {
  const [starting, setStarting] = useState(false);
  return (
    <StateScreen
      tone="red"
      label="Termivoa · session lost"
      art={<WindowArt mark="gone" />}
      title={<>This session<br />is gone</>}
      lead={`“${title}” ended on ${where}. Nothing was re-run.`}
      actions={(
        <>
          <button type="button" className="state-pill" onClick={onSessions}>sessions</button>
          <Button
            className="primary"
            busy={starting}
            busyLabel="Starting…"
            onClick={() => { setStarting(true); void onStartNew().finally(() => setStarting(false)); }}
          >New session</Button>
        </>
      )}
    />
  );
}

/**
 * Live for a provider terminal (docs/herdr-provider.md, section 6): the
 * workspace's tabs as chips in place of the session tabs, and none of the
 * features that need a Termivoa project (history, rename, terminate, photos,
 * project commands, preview, worktrees, start like this).
 */
export interface ProviderLive {
  /** "herdr". */
  readonly name: string;
  /** "shop · silver-meadow", each name isolated (bdi): names come from the provider. */
  readonly place: ReactNode;
  /** The chips row (ProviderChips). */
  readonly chips: ReactNode;
  /** "mac shows this terminal at phone size while it is open here." */
  readonly sizeLine: string;
  /** Opens the same terminal again after the attach ended. */
  readonly onOpenAgain: () => Promise<void>;
  /** The attach ended (Closed here): the screen must not pause or open again on hide and show. */
  readonly onEnded?: () => void;
}

/** The attach ended (the pane closed, or another client holds it): say so, and offer Open again. One tap opens once. */
export function ProviderEnded({ onOpenAgain, onSessions }: { readonly onOpenAgain: () => Promise<void>; readonly onSessions: () => void }) {
  const [opening, setOpening] = useState(false);
  return (
    <section className="dock readonly" aria-label="Terminal closed">
      <p className="controller-line muted" role="status"><span className="dot muted" aria-hidden="true" />Closed here</p>
      <p className="watching">This terminal is no longer open on this phone. Its row in Sessions stays.</p>
      <Button
        className="primary take"
        busy={opening}
        busyLabel="Opening…"
        onClick={() => { setOpening(true); void onOpenAgain().finally(() => setOpening(false)); }}
      >Open again</Button>
      <div className="spread"><span /><button type="button" className="link" onClick={onSessions}>Sessions</button></div>
    </section>
  );
}

export function Live({
  session, sessions, settings, multi = false, fontSize, gestures, runFirst, openComposer, provider, queue,
  onFontSize, onOpen, onNew, onSessions, onSettings, onChanged, onStartNew, onStartWorktree, onRevoked, onPreview,
}: {
  readonly session: HostedSession;
  /** Every computer's sessions, for the tabs. */
  readonly sessions: ReadonlyArray<HostedSession> | undefined;
  /** The settings of the session's own computer (project and shell names). */
  readonly settings: HTTPSettingsResponse | undefined;
  /** The phone uses more than one computer: name this one in the status line. */
  readonly multi?: boolean;
  readonly fontSize: number;
  /** The Settings switch "Terminal gestures" (docs/gestures.md). */
  readonly gestures: boolean;
  /** A pinch sets the text size, once on lift. */
  readonly onFontSize: (size: number) => void;
  readonly runFirst?: RunFirstRequest;
  /** Open the composer once the terminal is ready (from the preview's "Tell agent"). */
  readonly openComposer?: boolean;
  readonly onOpen: (session: HostedSession) => void;
  readonly onNew: () => void;
  readonly onSessions: () => void;
  readonly onSettings: () => void;
  readonly onChanged: () => Promise<void>;
  /** A new session in the same project and shell; `runFirst` is typed once when it opens. Settles when it opened or failed. */
  readonly onStartNew: (runFirst?: string) => Promise<void>;
  /** Like onStartNew, in a new git worktree of this session's repository; rejects with the API error. */
  readonly onStartWorktree: (worktree: { readonly name: string; readonly base: string }, runFirst: string | undefined) => Promise<void>;
  readonly onRevoked: (reason: string | undefined) => void;
  /** Absent when this host serves no previews. */
  readonly onPreview?: (port: number) => void;
  /** Present for a provider terminal (herdr). */
  readonly provider?: ProviderLive | undefined;
  /** Present while a "Needs you" queue run is on: the Next control. A run starts from the inbox or a notification tap, and stays on for a session opened from the tabs here. */
  readonly queue?: Queue | undefined;
}) {
  const host = useRef<HTMLDivElement>(null);
  const area = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const controller = useRef<InputController | undefined>(undefined);
  const initialFont = useRef(fontSize);
  const touchStart = useRef<number | undefined>(undefined);
  const touchLast = useRef<number | undefined>(undefined);
  const appScroller = useRef(new AppScroll());
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
  // Terminal links: the latest handlers, and a tap that a link took.
  const openPath = useRef<(link: PathLink) => boolean>(() => false);
  const openAddLink = useRef<(url: string) => boolean>(() => false);
  // An add-computer link tapped in the terminal: confirmed in a sheet outside it.
  const [addLink, setAddLink] = useState<AddComputerLink>();
  const linkTapped = useRef(false);
  // When a touch gesture last took its touch, and its pill ("← → ↑ ↓", "16 px"); the gesture itself lives in refs.
  const gestureTook = useRef(-Infinity);
  const [pill, setPill] = useState<string>();
  const gestureHost = useRef<GestureHost>(undefined);
  const swipe = useRef<{ readonly x: number; readonly y: number; readonly at: number }>(undefined);
  // PTY rows measured without the keyboard. The keyboard never shrinks the
  // PTY, so full-screen programs do not redraw each time it opens or closes.
  const restRows = useRef<number | undefined>(undefined);

  const status = useSyncExternalStore(
    runtime?.connection.subscribe ?? noSubscribe,
    runtime?.connection.getStatus ?? initialStatus,
  );
  const canSend = canSendWith(status);
  // The host accepted this phone's answer here: Next stands out until the session asks something else. Nothing moves by itself.
  const [answeredId, setAnsweredId] = useState<string>();
  const sentId = status.answer?.phase === 'sent' ? status.answer.questionId : undefined;
  useEffect(() => { if (sentId !== undefined) setAnsweredId(sentId); }, [sentId]);
  const answered = nextFilled(answeredId, status.agent?.state === 'waiting' ? status.agent.question?.id : undefined);
  const [slowLink, setSlowLink] = useState(false);
  useEffect(() => {
    setSlowLink(false);
    if (status.link === 'live') {
      return undefined;
    }
    const timer = setTimeout(() => setSlowLink(true), QUIET_RECONNECT_MS);
    return () => clearTimeout(timer);
  }, [status.link]);
  const draft = draftKey(session);
  const [tab, setTab] = useState<ChangesTab>(() => lastTabs.get(draft) ?? 'changes');
  const [, setModeTick] = useState(0);
  const setMode = (mode: ViewMode) => {
    viewModes.set(draft, mode);
    if (mode === 'terminal' && runtime?.connection.getChat().link === 'unsupported') noChat.add(draft);
    setModeTick((tick) => tick + 1);
  };
  const current = sessions?.find((item) => sameSession(item, session)) ?? session;
  const wantChat = viewModes.get(draft) === 'chat';
  const offered = chatOffered(settings, {
    chatShown: wantChat,
    unsupported: noChat.has(draft),
    provider: provider !== undefined,
  });
  const chatMode = offered && wantChat;

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
  // Not a provider terminal: each open is a new session ID, and would push stored sessions out of the list.
  const stored = provider === undefined;
  useEffect(() => { if (stored) markViewed(latest.current); }, [current, stored]);
  useEffect(() => () => { if (stored) markViewed(latest.current); }, [stored]);

  // The open terminal comes first: Wall tiles pause while it is open.
  const activeKey = keyOf(session);
  useEffect(() => {
    scheduler.setActive(activeKey);
    return () => scheduler.setActive(undefined);
  }, [activeKey]);

  const origin = session.computer.origin;
  const peer = !session.computer.home;
  useEffect(() => {
    const element = host.current;
    if (element === null) {
      return undefined;
    }
    const view = new TerminalView(initialFont.current);
    view.open(element);
    const connection = new Connection({
      url: websocketUrl(origin),
      sessionId: session.id,
      generation: session.generation,
      display: view.renderer,
      gate: scheduler.connectGate(origin, true),
      peer,
    });
    connection.start();
    setRuntime({ connection, view });
    return () => {
      connection.detach();
      view.dispose();
      setRuntime(undefined);
    };
  }, [origin, peer, session.id, session.generation]);

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
    // Terminal output is untrusted: only a link this app would accept is a link, and a tap only opens the confirm sheet.
    const adds = runtime?.view.linkAddComputer(
      (url) => !('problem' in parseAddComputerLink(url, window.location)),
      (url) => { if (openAddLink.current(url)) linkTapped.current = true; },
    );
    return () => {
      links?.dispose();
      adds?.dispose();
    };
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
  }, [runtime, canSend, fontSize, fullPanel, keyboard, chatMode]);

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

  // Once live, decide from broker facts only; a recap then reads the changes once. Nobody asked for that read: quiet,
  // so a terminal with no folder to read (a herdr terminal outside git) shows no error.
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
      setRecap(runtime.connection.openChanges(true) ? { phase: 'loading' } : { phase: 'shown', line });
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
    if (provider === undefined && runtime?.connection.openHistory() === true) {
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
    if (changesLoadFailed) {
      ChangesScreen = lazyChangesScreen();
      changesLoadFailed = false;
    }
    if (connection !== undefined && (on === 'changes' ? connection.openChanges() : connection.openFilesFolder(''))) {
      showTab(on);
      input.current?.blur();
      setPanel('changes');
    }
  }

  function switchTab(next: ChangesTab) {
    const connection = runtime?.connection;
    showTab(next);
    if (next === 'changes' && connection?.needsChanges() === true) connection.openChanges();
    if (next === 'files' && connection?.getFiles() === undefined) connection?.openFilesFolder('');
  }

  // Chat's Open in Changes: the edit's path is relative to the session's folder, the list's to the repository.
  const changesFile = useRef<string | undefined>(undefined);
  useEffect(() => {
    const want = changesFile.current;
    if (want === undefined || changesView === undefined || changesView.state === 'loading') return;
    changesFile.current = undefined;
    const files = changesView.summary?.state === 'ready' ? changesView.summary.files : [];
    const found = changedPath(files.map((file) => file.path), want);
    if (found !== undefined) runtime?.connection.openChangesFile(found);
  }, [changesView, runtime]);

  function closeChanges() {
    runtime?.connection.closeChanges();
    runtime?.connection.closeFiles();
    setPanel('none');
  }

  // The screen has filled the draft; the user reads it and taps Send.
  function onAsked() {
    runtime?.connection.closeChanges();
    runtime?.connection.closeFiles();
    setComposerVersion((value) => value + 1);
    setPanel('composer');
  }

  // Touch gestures: native listeners, so a held or pinching touch can prevent scroll and page zoom.
  useEffect(() => {
    const element = area.current;
    if (element === null) return undefined;
    const machine = new TouchGestures(() => gestureHost.current as GestureHost);
    const points = (list: TouchList) => Array.from(list, (touch) => ({ id: touch.identifier, x: touch.clientX, y: touch.clientY }));
    const start = (event: TouchEvent) => {
      // A new touch: a gesture's taken tap whose click never came must not eat this one.
      gestureTook.current = -Infinity;
      machine.start(points(event.touches));
    };
    const move = (event: TouchEvent) => { if (machine.move(points(event.touches)) && event.cancelable) event.preventDefault(); };
    const end = (event: TouchEvent) => { if (machine.end(points(event.touches)) && event.cancelable) event.preventDefault(); };
    const cancel = () => machine.cancel();
    // Safari's own pinch zoom: off on the terminal while gestures are on.
    const zoom = (event: Event) => { if (gestureHost.current?.enabled() === true) event.preventDefault(); };
    const hidden = () => { if (document.visibilityState === 'hidden') machine.cancel(); };
    // Only move and end may prevent their default; start and cancel never do.
    const options = { passive: false };
    element.addEventListener('touchstart', start, { passive: true });
    element.addEventListener('touchmove', move, options);
    element.addEventListener('touchend', end, options);
    element.addEventListener('touchcancel', cancel, { passive: true });
    element.addEventListener('gesturestart', zoom, options);
    element.addEventListener('gesturechange', zoom, options);
    document.addEventListener('visibilitychange', hidden);
    return () => {
      element.removeEventListener('touchstart', start);
      element.removeEventListener('touchmove', move);
      element.removeEventListener('touchend', end);
      element.removeEventListener('touchcancel', cancel);
      element.removeEventListener('gesturestart', zoom);
      element.removeEventListener('gesturechange', zoom);
      document.removeEventListener('visibilitychange', hidden);
      machine.cancel();
    };
  }, []);

  const normalBuffer = status.buffer === 'normal';
  // Full-screen apps (Claude Code, vim, less) scroll themselves: a swipe or
  // trackpad scroll becomes wheel steps. Needs control, since wheel steps are input.
  const appScroll = !normalBuffer && canSend && !keyboard;

  // Wheel steps go at a grid cell (the swipe's start, or the pointer), so tmux and vim scroll that pane.
  const sendWheel = (button: 'wheel-up' | 'wheel-down', cell: { row: number; column: number } | undefined) => {
    runtime?.connection.sendWheel(button, cell);
  };
  const historyEnabled = status.link === 'live';
  const sheet = panel === 'composer' || panel === 'snippets' || panel === 'menu' || panel === 'worktree' || panel === 'add';
  const question = panel === 'none' ? openQuestion(status) : undefined;
  // With the keyboard up the card folds to its pill, so typing and keys keep working.
  const cardOpen = question !== undefined && !keyboard && foldedQuestion !== question.id;
  const canInput = canSend && !sheet && !cardOpen && !fullPanel;
  // Terminal gestures (docs/gestures.md) are off while Chat is on screen.
  const terminalGestures = gestures && !chatMode;
  gestureHost.current = {
    enabled: () => terminalGestures,
    canInput: () => canInput,
    cell: () => runtime?.view.cellSize(),
    send: (key) => runtime?.connection.sendKey(key, NO_MODIFIERS) ?? false,
    drag: (phase, { x, y }) => {
      if (phase === 'start') {
        touchStart.current = y;
        touchLast.current = y;
        appScroller.current.start(runtime?.view.cellAt(x, y, true));
        return;
      }
      if (appScroll) {
        const last = touchLast.current;
        touchLast.current = y;
        if (last !== undefined) appScroller.current.move(y - last, sendWheel);
        return;
      }
      const start = touchStart.current;
      if (start !== undefined && y - start > 48 && normalBuffer && area.current?.scrollTop === 0) {
        touchStart.current = undefined;
        openHistory();
      }
    },
    fontSize: () => fontSize,
    setFontSize: onFontSize,
    pill: setPill,
    tookTap: () => { gestureTook.current = performance.now(); },
    now: () => performance.now(),
    later: (run, ms) => {
      const timer = setTimeout(run, ms);
      return () => clearTimeout(timer);
    },
  };

  /** Tap to place the cursor: arrows along the cursor's row, only where they surely land (tapArrows). */
  function placeCursor(x: number, y: number) {
    const cell = runtime?.view.cellAt(x, y);
    const cursor = runtime?.connection.cursor();
    if (runtime === undefined || cell === undefined || cursor === undefined) return;
    const arrows = tapArrows({ buffer: status.buffer, cursor }, cell, runtime.view.rowCells(cell.row));
    for (let step = 0; step < Math.abs(arrows); step++) {
      if (!runtime.connection.sendKey(arrows > 0 ? 'arrow-right' : 'arrow-left', NO_MODIFIERS)) return;
    }
  }

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
  openAddLink.current = (url) => {
    const link = panel === 'none' && !cardOpen ? parseAddComputerLink(url, window.location) : undefined;
    if (link === undefined || 'problem' in link) return false;
    input.current?.blur();
    setAddLink(link);
    setPanel('add');
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

  const providerEnded = provider !== undefined && (status.link === 'lost' || status.sessionState === 'exited' || status.sessionState === 'exiting');
  const onProviderEnded = provider?.onEnded;
  useEffect(() => { if (providerEnded) onProviderEnded?.(); }, [providerEnded, onProviderEnded]);
  if (status.link === 'lost' && provider === undefined) {
    const lost = <SessionLost title={session.title} where={multi ? session.computer.label : 'your computer'} onSessions={onSessions} onStartNew={onStartNew} />;
    // In a queue run the way on stays: Next goes to what still waits.
    return queue === undefined ? lost : <><QueueNext queue={queue} />{lost}</>;
  }

  return (
    <main className={keyboard ? 'live keyboard' : 'live'}>
      {!fullPanel && (
        <>
          {provider === undefined ? (
            <TabBar
              tabs={liveTabs(sessions, session)}
              current={session}
              viewed={readViewed()}
              liveCwd={liveCwd}
              onOpen={onOpen}
              onNew={onNew}
              onMenu={() => setPanel('menu')}
            />
          ) : provider.chips}
          <div
            className="status-line"
            role="status"
            onTouchStart={(event) => {
              const touch = event.touches[0];
              swipe.current = event.touches.length === 1 && touch !== undefined ? { x: touch.clientX, y: touch.clientY, at: performance.now() } : undefined;
            }}
            onTouchEnd={(event) => {
              const from = swipe.current;
              const touch = event.changedTouches[0];
              swipe.current = undefined;
              // Provider chips are not session tabs.
              if (!terminalGestures || provider !== undefined || from === undefined || touch === undefined) return;
              const step = sessionSwipe(touch.clientX - from.x, touch.clientY - from.y, performance.now() - from.at, from.x, window.innerWidth);
              const tabs = liveTabs(sessions, session);
              const at = tabs.findIndex((tab) => sameSession(tab, session));
              const next = step === 0 || at < 0 ? undefined : tabs[at + step];
              if (next !== undefined) onOpen(next);
            }}
          >
            <span className="place">
              <span className={status.link === 'live' ? 'dot accent' : 'dot amber'} aria-hidden="true" />
              {multi && <span className="place-computer"><ComputerTag computer={session.computer} /> · </span>}
              {provider !== undefined ? (
                <><span className="home-badge">{provider.name}</span><span className="place-tail">{status.link === 'live' ? provider.place : linkLabel(status)}</span></>
              ) : showPlace ? (
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
            {offered && <ViewSwitch mode={chatMode ? 'chat' : 'terminal'} onMode={setMode} />}
            <span className={`badge-text ${badge.tone}`}>{badge.text}</span>
          </div>
          {provider !== undefined && !providerEnded && <p className="hint-text size-line">{provider.sizeLine}</p>}
          {queue !== undefined && <QueueNext queue={queue} answered={answered} />}
        </>
      )}

      {status.notice !== undefined && (
        <div className="banner warning" role="alert">
          <span className="banner-dot" aria-hidden="true" />
          <span className="banner-text">{status.notice}</span>
          <span className="banner-actions"><button type="button" className="link" onClick={() => runtime?.connection.dismissNotice()}>Dismiss</button></span>
        </div>
      )}
      {runFirstNotice !== undefined && (
        <div className="banner warning" role="alert">
          <span className="banner-dot" aria-hidden="true" />
          <span className="banner-text">{runFirstNotice}</span>
          <span className="banner-actions"><button type="button" className="link" onClick={() => setRunFirstNotice(undefined)}>Dismiss</button></span>
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
        className={`terminal-area${sheet || cardOpen ? ' dim' : ''}${appScroll ? ' app-scroll' : ''}${gestures ? ' gestures' : ''}`}
        hidden={fullPanel || chatMode}
        onClick={(event) => {
          // A gesture (trackpad, pinch, two-finger tap) takes its click as a link does.
          const action = areaClick(linkTapped.current || performance.now() - gestureTook.current < TOOK_TAP_MS, sheet, canSend);
          linkTapped.current = false;
          gestureTook.current = -Infinity;
          if (action === 'close') setPanel('none');
          else if (action === 'focus') {
            // Only with the keyboard already up: the first tap raises it, and moves nothing.
            if (gestures && keyboardRef.current && canInput && status.link === 'live') placeCursor(event.clientX, event.clientY);
            input.current?.focus();
          }
        }}
        onWheel={(event) => {
          if (appScroll) appScroller.current.wheel(-event.deltaY, runtime?.view.cellAt(event.clientX, event.clientY, true), sendWheel);
          else if (event.deltaY < 0 && normalBuffer && area.current?.scrollTop === 0) openHistory();
        }}
      >
        {/* Pinned to the top of the terminal, away from the cursor and prompt (usually low with the keyboard up). */}
        {pill !== undefined && <div className="gesture-pill-anchor" aria-hidden="true"><div className="gesture-pill">{pill}</div></div>}
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

      {chatMode && !fullPanel && runtime !== undefined && (
        <Suspense fallback={<section className="chat chat-wait" aria-label="Chat"><p role="status" className="muted">Opening chat…</p></section>}>
          <ChatScreen
            connection={runtime.connection}
            status={status}
            enabled={settings?.chat?.enabled === true}
            draftKey={draft}
            bar={panel === 'none' && question === undefined}
            onShowTerminal={() => setMode('terminal')}
            onOpenChanges={(path) => {
              // The host reads one changes request at a time: the file opens once the list is there.
              changesFile.current = path;
              openChanges('changes');
            }}
            onCompose={() => setPanel('composer')}
          />
        </Suspense>
      )}
      {panel === 'history' && runtime !== undefined && (
        <History connection={runtime.connection} fontSize={fontSize} onReturn={closeHistory} />
      )}
      {panel === 'changes' && runtime !== undefined && (
        <Suspense fallback={<ChangesWait failed={false} onReturn={closeChanges} />}>
          <ChangesScreen connection={runtime.connection} tab={tab} cwd={cwd} draft={draft} onTab={switchTab} onAsked={onAsked} onReturn={closeChanges} />
        </Suspense>
      )}

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

      {panel === 'none' && question === undefined && !keyboard && provider === undefined && !chatMode && (
        <PreviewChip previews={status.link === 'live' ? status.previews : current.previews} onOpen={onPreview} />
      )}

      {!fullPanel && !chatMode && (
        <div className={sheet ? 'dock-slot covered' : 'dock-slot'}>
          {providerEnded ? (
            <ProviderEnded onOpenAgain={provider.onOpenAgain} onSessions={onSessions} />
          ) : status.control === 'controller' ? (
            <CockpitDock
              compact={keyboard}
              disabled={!canSend}
              latched={latched}
              alternate={!normalBuffer}
              historyEnabled={historyEnabled}
              history={provider === undefined}
              onAgentKey={(key) => { if (runtime !== undefined) sendAgentKey(runtime.connection, key); }}
              onKey={(key: KeyName) => controller.current?.pressKey(key)}
              onLatch={(modifier) => controller.current?.toggleLatch(modifier)}
              onWheel={(button) => runtime?.connection.sendWheel(button)}
              onType={() => { setPanel('none'); input.current?.focus(); }}
              onCompose={() => setPanel('composer')}
              onSnippets={() => {
                // One read of the project's commands each time the sheet opens.
                if (provider === undefined) runtime?.connection.listTasks();
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
              history={provider === undefined}
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
          origin={origin}
          canSend={canSend}
          draftKey={draft}
          photos={provider === undefined}
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
          onRunTask={provider === undefined ? onStartNew : undefined}
          onClose={() => setPanel('none')}
        />
      )}
      {panel === 'menu' && (
        <SessionMenu
          session={current}
          origin={origin}
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
      {panel === 'add' && addLink !== undefined && (
        <AddComputerSheet link={addLink} onClose={() => { setAddLink(undefined); setPanel('none'); }} />
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
