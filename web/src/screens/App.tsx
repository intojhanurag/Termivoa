import { lazy, Suspense, useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { api, ApiError, client, CONNECTION_UNAVAILABLE, firstLabel, homeOrigin, isAuthFailure, updateText } from '../api/http';
import { logOut, previewOriginOf, refreshComputers, storedComputers, type Computer } from '../computers';
import { FEATURE_REVISION, type HTTPSettingsResponse } from '../generated/protocol';
import {
  ADD_COMPUTER_PATH, cancelAdd, currentAdd, dismissAdd, PEER_ADD_PATH, resetAdd, subscribeAdd, takeAddComputerFragment,
  type AddComputerLink, type AddTask, type AppLocation,
} from '../pairing/addComputer';
import { adoptFragmentGrant, takeGrantFromFragment, type GrantSlot, type HistoryLike, type LocationLike } from '../pairing/pairing';
import { syncHomePush, syncPeerPush, workersSafe } from '../push';
import { storage } from '../storage';
import { scheduler } from '../terminal/scheduler';
import {
  cantReach, forgetSettings, homeSettings, hosted, keyOf, mergeSessions, OUT_OF_DATE, pairAgain, ReadFailure, useAllSessions, type ComputerStatus, type HostedSession,
} from './allSessions';
import { ErrorBoundary, loadChunk } from './chunks';
import { useFontSize, useServiceWorkerUpdate, useVisualViewport } from './hooks';
import { NewSession } from './NewSession';
import { OfflineScreen } from './StateScreen';
import { isPreviewPort, previewOrigin } from './PreviewChip';
import type { RunFirstRequest } from './presets';
import { GONE, type ProviderTap } from './Herdr';
import { useProviders } from './providers';
import { Sessions } from './Sessions';
import { Setup } from './Setup';
import { Banner } from './ui';

// The terminal (xterm, protocol validators) loads only when a session opens,
// so Setup and Sessions start without it.
const loadLive = () => loadChunk(() => import('./Live'));
const Live = lazy(() => loadLive().then((module) => ({ default: module.Live })));
// A provider terminal (herdr) opens in Live too, so it loads with the terminal.
const loadProviderScreen = () => loadChunk(() => import('./ProviderScreen'));
const ProviderScreen = lazy(() => loadProviderScreen().then((module) => ({ default: module.ProviderScreen })));
// The wall draws live terminals too, so it also loads with xterm.
const Wall = lazy(() => loadChunk(() => import('./Wall')).then((module) => ({ default: module.Wall })));
const Preview = lazy(() => loadChunk(() => import('./Preview')).then((module) => ({ default: module.Preview })));
// Settings opens from a tap and is not needed to show the session list.
const Settings = lazy(() => loadChunk(() => import('./Settings')).then((module) => ({ default: module.Settings })));
// Adding a computer is rare, so its pages load only when opened.
const loadAddComputer = () => loadChunk(() => import('./AddComputer'));
const AddComputerPage = lazy(() => loadAddComputer().then((module) => ({ default: module.AddComputerPage })));
const PeerAddPage = lazy(() => loadAddComputer().then((module) => ({ default: module.PeerAddPage })));

/** Secrets and links read from the URL fragment before anything renders. */
export interface StartupLinks {
  readonly grant: GrantSlot;
  /** `/computers/add#…` on home: the add-computer link to redeem. */
  readonly add?: AddComputerLink | undefined;
  /** `/add-computer#…` opened directly on a peer (for example from the iPhone camera): the whole link, to copy. */
  readonly peerLink?: string | undefined;
}

/**
 * Reads and strips every URL fragment once at start. The add-computer
 * fragment goes first, because takeGrantFromFragment strips all fragments.
 * The peer page's link stays in memory only; it is never redeemed here.
 */
export function takeStartupLinks(location: LocationLike, history: HistoryLike, app: AppLocation): StartupLinks {
  const add = takeAddComputerFragment(location, history, app);
  const peerLink = location.pathname === PEER_ADD_PATH && location.hash.length > 1
    ? `${app.origin}${PEER_ADD_PATH}${location.hash}`
    : undefined;
  const grant = takeGrantFromFragment(location, history);
  return { grant: { value: grant }, add, peerLink };
}

type View =
  | { readonly name: 'boot' }
  | { readonly name: 'offline' }
  | { readonly name: 'setup'; readonly reason?: string }
  /** `tab`: a provider tab to open on (herdr). */
  | { readonly name: 'sessions'; readonly message?: string; readonly tab?: string }
  | { readonly name: 'wall' }
  | { readonly name: 'new'; readonly back?: View; readonly computer?: Computer }
  | { readonly name: 'live'; readonly session: HostedSession; readonly runFirst?: RunFirstRequest; readonly compose?: boolean }
  | { readonly name: 'preview'; readonly session: HostedSession; readonly port: number }
  /**
   * One provider terminal (herdr). `tapped`: 0, or a new number (newTap) for each tap in the app or confirmed
   * notification tap, so it opens at once, also when the tap is for the address already shown.
   */
  | { readonly name: 'provider'; readonly computer: Computer; readonly provider: string; readonly target: string; readonly tapped: number; readonly notice?: string }
  | { readonly name: 'settings' }
  | { readonly name: 'add-computer' }
  | { readonly name: 'peer-add' };

/** While the host is unreachable, try again at most this often in seconds (and at once on network or focus changes). */
export const OFFLINE_RETRY_S = 10;
/** Failed boot attempts that still show "connecting": one quick failure (iOS waking the network) is not offline. */
export const OFFLINE_GRACE_FAILURES = 2;

/**
 * After `failures` failed attempts in a row: whether to show the offline
 * screen, and the seconds until the next try (1, 2, 4, 8, then every 10).
 */
export function offlineRetry(failures: number): { readonly offline: boolean; readonly retryIn: number } {
  return {
    offline: failures >= OFFLINE_GRACE_FAILURES,
    retryIn: Math.min(2 ** Math.max(0, failures - 1), OFFLINE_RETRY_S),
  };
}

const APP_NAME = 'Termivoa';

/** The browser tab title: the screen, or the open session's name. */
export function titleFor(view: View): string {
  switch (view.name) {
    case 'live': return `${view.session.title} · ${APP_NAME}`;
    case 'preview': return `Preview :${view.port} · ${APP_NAME}`;
    case 'provider': return `${view.provider} · ${APP_NAME}`;
    case 'sessions': return `Sessions · ${APP_NAME}`;
    case 'wall': return `Wall · ${APP_NAME}`;
    case 'new': return `New session · ${APP_NAME}`;
    case 'settings': return `Settings · ${APP_NAME}`;
    case 'setup': return `Pair · ${APP_NAME}`;
    case 'add-computer': case 'peer-add': return `Add a computer · ${APP_NAME}`;
    case 'offline': return `${CONNECTION_UNAVAILABLE} · ${APP_NAME}`;
    case 'boot': return APP_NAME;
  }
}

/** Home keeps `/sessions/<id>` (push links keep working); a peer's session is `/c/<host>/sessions/<id>`. */
export function sessionPath(session: Pick<HostedSession, 'id' | 'computer'>): string {
  const id = `/sessions/${encodeURIComponent(session.id)}`;
  return session.computer.home ? id : `/c/${encodeURIComponent(session.computer.host)}${id}`;
}

/** `/p/<provider>/<target>` on home, `/c/<host>/p/…` on a peer (docs/herdr-provider.md, rule 9). */
export function providerPath(computer: Pick<Computer, 'home' | 'host'>, provider: string, target: string): string {
  const path = `/p/${encodeURIComponent(provider)}/${encodeURIComponent(target)}`;
  return computer.home ? path : `/c/${encodeURIComponent(computer.host)}${path}`;
}

export function pathFor(view: View): string {
  switch (view.name) {
    case 'live': return sessionPath(view.session);
    case 'provider': return providerPath(view.computer, view.provider, view.target);
    case 'preview': return `${sessionPath(view.session)}/preview?port=${view.port}`;
    case 'settings': return '/settings';
    case 'wall': return '/wall';
    case 'new': return '/new';
    case 'add-computer': return ADD_COMPUTER_PATH;
    case 'peer-add': return PEER_ADD_PATH;
    case 'setup': return window.location.pathname === '/pair' ? '/pair' : '/';
    default: return '/';
  }
}

export type Route =
  | { readonly name: 'settings' | 'wall' | 'new' | 'add-computer' | 'peer-add' | 'sessions' }
  /** `host` is undefined for home. */
  | { readonly name: 'session'; readonly host: string | undefined; readonly id: string; readonly preview: boolean }
  /** A provider terminal; `host` is undefined for home. */
  | { readonly name: 'provider'; readonly host: string | undefined; readonly provider: string; readonly target: string }
  /** Not an app address: show Sessions at `/`. */
  | { readonly name: 'unknown' };

const HOST = /^[a-z0-9.-]+(?::\d{1,5})?$/u;
/** The schema's providerId and providerTargetId patterns. */
const PROVIDER_ID = /^[a-z][a-z0-9-]{0,31}$/u;
const TARGET_ID = /^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$/u;

function decode(part: string): string | undefined {
  try {
    return decodeURIComponent(part);
  } catch {
    return undefined;
  }
}

/** The screen for an address. */
export function parseRoute(path: string): Route {
  switch (path) {
    case '/': return { name: 'sessions' };
    case '/settings': return { name: 'settings' };
    case '/wall': return { name: 'wall' };
    case '/new': return { name: 'new' };
    case ADD_COMPUTER_PATH: return { name: 'add-computer' };
    case PEER_ADD_PATH: return { name: 'peer-add' };
    default:
  }
  const provider = /^(?:\/c\/([^/]+))?\/p\/([^/]+)\/([^/]+)$/u.exec(path);
  if (provider !== null) {
    const host = provider[1] === undefined ? undefined : decode(provider[1]);
    const id = decode(provider[2] ?? '');
    const target = decode(provider[3] ?? '');
    if ((provider[1] !== undefined && (host === undefined || !HOST.test(host))) || id === undefined || !PROVIDER_ID.test(id) || target === undefined || !TARGET_ID.test(target)) {
      return { name: 'unknown' };
    }
    return { name: 'provider', host, provider: id, target };
  }
  const home = /^\/sessions\/([^/]+)(\/preview)?$/u.exec(path);
  const peer = /^\/c\/([^/]+)\/sessions\/([^/]+)(\/preview)?$/u.exec(path);
  const host = peer?.[1] === undefined ? undefined : decode(peer[1]);
  const id = decode((home ?? peer)?.[home === null ? 2 : 1] ?? '');
  if (id === undefined || id === '' || (home === null && (host === undefined || !HOST.test(host)))) {
    return { name: 'unknown' };
  }
  return { name: 'session', host, id, preview: (home?.[2] ?? peer?.[3]) !== undefined };
}

const SESSION_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;

/**
 * The address a tapped notification asks the open app for (a message from a
 * service worker), or undefined. As strict as the worker: only a session, by
 * lowercase UUID, of home or of a peer in the list; the address is built
 * here, never taken as sent.
 */
export function notificationPath(data: unknown, computers: ReadonlyArray<Computer>): string | undefined {
  const { type, path } = (typeof data === 'object' && data !== null ? data : {}) as { readonly type?: unknown; readonly path?: unknown };
  if (type !== 'open' || typeof path !== 'string') return undefined;
  const target = parseRoute(path);
  if (target.name === 'provider') {
    const computer = computers.find((item) => (target.host === undefined ? item.home : !item.home && item.host === target.host));
    return computer === undefined ? undefined : providerPath(computer, target.provider, target.target);
  }
  if (target.name !== 'session' || target.preview || !SESSION_ID.test(target.id)) return undefined;
  const computer = computers.find((item) => (target.host === undefined ? item.home : !item.home && item.host === target.host));
  return computer === undefined ? undefined : sessionPath({ id: target.id, computer });
}

let taps = 0;
/** A new tap number for a provider view: each tap differs from the last, so the screen sees it. */
export const newTap = (): number => ++taps;

/** A chip moves between tabs of the same workspace: one screen, so it replaces the history entry instead of adding one. */
export function sameScreen(from: View, to: View): boolean {
  return from.name === 'provider' && to.name === 'provider' && from.computer.origin === to.computer.origin && from.provider === to.provider;
}

/** The provider view for an address (a load, back or forward, or a notification tap): it opens at once only for a tap the service worker confirms. */
export async function providerArrival(
  computer: Computer, provider: string, target: string, path: string, ask: (path: string) => Promise<boolean> = askWorkerTap,
): Promise<View> {
  return { name: 'provider', computer, provider, target, tapped: await ask(path) ? newTap() : 0 };
}

/** How long the page waits for the service worker's answer about a tap. */
export const TAP_ASK_MS = 1_500;

/**
 * Whether a notification tap brought this page to `path`: the service worker
 * keeps a one-time record of each tap for about 30 s (sw.js, takeTap). No
 * worker, no answer in time, or no fresh record: false, and the page shows
 * Open. The address itself never says so.
 */
export function askWorkerTap(
  path: string,
  worker: { postMessage(message: unknown, transfer: Transferable[]): void } | null | undefined = globalThis.navigator?.serviceWorker?.controller,
  timeoutMs = TAP_ASK_MS,
): Promise<boolean> {
  if (worker === null || worker === undefined) return Promise.resolve(false);
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    const done = (fresh: boolean) => {
      clearTimeout(timer);
      channel.port1.close();
      resolve(fresh);
    };
    const timer = setTimeout(() => done(false), timeoutMs);
    channel.port1.onmessage = (event: MessageEvent) => {
      const data = event.data as { readonly type?: unknown; readonly fresh?: unknown } | null;
      done(data?.type === 'tap' && data.fresh === true);
    };
    try {
      worker.postMessage({ type: 'tap?', path }, [channel.port2]);
    } catch {
      done(false);
    }
  });
}

/**
 * Starts a session in the same project and shell as `from` and returns its
 * live view; `runFirst` is typed there once, only if no one else has control.
 * With `worktree`, the host first creates a git worktree from the repository
 * of `from`'s folder and starts the session in it.
 */
export async function startLike(
  from: HostedSession,
  runFirst?: string,
  worktree?: { readonly name: string; readonly base: string },
): Promise<View> {
  const created = await client(from.computer.origin).createSession({
    operationId: crypto.randomUUID(),
    projectId: from.projectId,
    shellProfileId: from.shellProfileId,
    ...(worktree === undefined ? {} : { worktree: { sourceSessionId: from.id, ...worktree } }),
  });
  const session = hosted(created.session, from.computer);
  return runFirst === undefined
    ? { name: 'live', session }
    : { name: 'live', session, runFirst: { command: runFirst, settled: false } };
}

/** The port of a preview address's `?port=`, or undefined unless it is a plain valid port number. */
export function previewPortParam(search: string): number | undefined {
  const raw = new URLSearchParams(search).get('port');
  const port = raw !== null && /^\d{1,5}$/u.test(raw) ? Number(raw) : undefined;
  return isPreviewPort(port) ? port : undefined;
}

/**
 * history.state of the entry the app itself makes when the user taps Open ↗.
 * It stays with that entry through a reload and through Back and Forward; an
 * address opened from a link, a message or another page has none.
 */
export const PREVIEW_TAP = { previewTap: true } as const;

/**
 * Whether a loaded address may show a peer's preview: only an entry that a
 * tap in this app made, and only a port that session lists now. A peer knows
 * its own session IDs, so a link alone must never put its page in the app;
 * anything else opens the session's terminal.
 */
export function peerPreviewOnLoad(state: unknown, previews: ReadonlyArray<{ readonly port: number }> | undefined, port: number): boolean {
  const tapped = typeof state === 'object' && state !== null && (state as { readonly previewTap?: unknown }).previewTap === true;
  return tapped && previews?.some((item) => item.port === port) === true;
}

/**
 * Where a session's preview is framed from (`origin`), or what to say in
 * place of the frame (`notice`); neither while a peer has not answered yet.
 * Home: its configured origin. A peer never needs home's previews: its origin
 * is computed, and it has one only while the app's own poll of that peer
 * answers, at FEATURE_REVISION.peerPreview or higher (its listener has the rules the frame needs, docs/peer-preview.md). The app cannot see what the
 * frame is answered, so a peer that refuses the phone or cannot be reached
 * loses its frame.
 */
export function previewFor(
  computer: Computer,
  status: ComputerStatus | undefined,
  homePreviewAt: string | undefined,
): { readonly origin?: string | undefined; readonly notice?: string } {
  if (computer.home) return { origin: previewOriginOf(computer, homePreviewAt) };
  switch (status?.kind) {
    case 'ok': return status.settings.protocolRevision >= FEATURE_REVISION.peerPreview
      ? { origin: previewOriginOf(computer, undefined) }
      : { notice: updateText(computer.label) };
    case 'pair': return { notice: pairAgain(computer.label) };
    case 'unreachable': return { notice: cantReach(computer.label) };
    case 'blocked': return { notice: `${cantReach(computer.label)}. ${OUT_OF_DATE}` };
    case 'outdated': return { notice: status.message };
    default: return {};
  }
}

const UNPAIRED = 'This phone is no longer paired with your computer.';

/** sessionStorage: this tab already reloaded once to drop a stray service worker (the loop guard), and which link it refused. */
export const STRAY_RELOAD_KEY = 'pterm.strayReload';
export const STRAY_PAIR = 'For safety, this pairing link was not used. Run pterm pair on your computer again.';
export const strayAdd = (home: string): string =>
  `For safety, this add-computer link was not used. Run pterm pair --via ${home} on the new computer again, then scan or paste the new link.`;

export interface StrayCheck {
  /** False when a stray service worker was removed or still controls this page. */
  readonly safe: boolean;
  /** Reload now, so the stray no longer controls the page. */
  readonly reload: boolean;
  /** The link this load (or the load before the reload) did not use. */
  readonly refused: 'pair' | 'add' | undefined;
}

type SessionStore = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

/**
 * Runs after the stray-worker cleanup and before any pairing request. A stray
 * worker that controls this page sees every request until the next load, so
 * when the load is not safe, no grant of this load is redeemed: the pairing
 * grant and the add-computer link are dropped (they are already stripped from
 * the URL, so they are lost, which is the safe failure). Then the tab reloads
 * once; a second unsafe load in the same tab does not reload again (loop
 * guard), and storage that cannot remember the reload means no reload.
 */
export function guardStartup(
  slots: { readonly grant: GrantSlot; readonly add: { value: AddComputerLink | undefined } },
  safe: boolean,
  given: SessionStore | undefined,
): StrayCheck {
  // Storage that throws (some private modes) is no storage: no reload, but the same message.
  let store = given;
  let before: string | null = null;
  try {
    before = store?.getItem(STRAY_RELOAD_KEY) ?? null;
  } catch {
    store = undefined;
  }
  const remembered = before === 'pair' || before === 'add' ? before : undefined;
  if (safe) {
    try {
      store?.removeItem(STRAY_RELOAD_KEY);
    } catch {
      // Nothing to forget.
    }
    return { safe, reload: false, refused: remembered };
  }
  const refused = slots.grant.value !== undefined ? 'pair' : slots.add.value !== undefined ? 'add' : remembered;
  slots.grant.value = undefined;
  slots.add.value = undefined;
  if (store === undefined || before !== null) return { safe, reload: false, refused };
  try {
    store.setItem(STRAY_RELOAD_KEY, refused ?? '');
  } catch {
    return { safe, reload: false, refused };
  }
  return { safe, reload: true, refused };
}

/**
 * A pairing link opened later in this same tab (only the fragment changes,
 * no load). After an unsafe start the workers are checked again, so the link
 * is used once no stray worker controls the page any more, without waiting
 * for a full reload.
 */
export async function guardLink(
  slots: Parameters<typeof guardStartup>[0],
  unsafe: boolean,
  check: () => Promise<boolean> = workersSafe,
  store: SessionStore | undefined = storage('sessionStorage'),
): Promise<StrayCheck> {
  return unsafe ? guardStartup(slots, await check(), store) : { safe: true, reload: false, refused: undefined };
}

/** Home's preview origin. read() starts a fresh read (home answered); origin() waits for it, or starts one (a preview link opened before home answered). A failed read means no previews. */
export function homePreview(
  onOrigin: (origin: string | undefined) => void,
  load: () => Promise<{ readonly origin?: string | undefined }> = () => api.previewConfig(),
): { readonly read: () => Promise<string | undefined>; readonly origin: () => Promise<string | undefined> } {
  let current: Promise<string | undefined> | undefined;
  const read = () => {
    const next = load().then((config) => previewOrigin(config.origin), () => undefined);
    current = next;
    void next.then(onOrigin);
    return next;
  };
  return { read, origin: () => current ?? read() };
}

/**
 * Home's first good answer in a visit, or after a re-pair or log out in the
 * same tab (reset): its settings once, and its computer list until one read
 * of it works (a failed read is tried again on the next good answer).
 */
export function homeAdoption(deps: {
  readonly settings: (answer: HTTPSettingsResponse) => void;
  readonly list: (computers: Computer[]) => void;
  readonly readList?: () => Promise<Computer[]>;
}): { readonly answer: (answer: HTTPSettingsResponse) => void; readonly reset: () => void } {
  let adopted = false;
  let list: 'unread' | 'reading' | 'read' = 'unread';
  // A read started before a reset never lands after it.
  let epoch = 0;
  return {
    answer(answer) {
      if (!adopted) {
        adopted = true;
        deps.settings(answer);
      }
      if (list !== 'unread') return;
      list = 'reading';
      const at = epoch;
      (deps.readList ?? refreshComputers)().then(
        (computers) => {
          if (at !== epoch) return;
          list = 'read';
          deps.list(computers);
        },
        () => { if (at === epoch) list = 'unread'; },
      );
    },
    reset() {
      adopted = false;
      list = 'unread';
      epoch += 1;
    },
  };
}

/** Home's new list dropped the computer of the open Live view or preview: go to Sessions with a short notice. */
export function leaveRemoved(view: View, computers: ReadonlyArray<Computer>): View | undefined {
  const computer = view.name === 'live' || view.name === 'preview' ? view.session.computer : view.name === 'provider' ? view.computer : undefined;
  if (computer === undefined || computers.some((item) => item.origin === computer.origin)) return undefined;
  return { name: 'sessions', message: `${computer.label} is no longer in your list.` };
}

export interface StartDeps {
  /** Computers in the stored list, home included. */
  readonly computers: number;
  readonly homeSettings: () => Promise<HTTPSettingsResponse>;
  /** Home answered: use its settings, its computer list and its preview origin. */
  readonly onHome: (settings: HTTPSettingsResponse) => void;
  readonly onSetup: () => void;
  readonly onOffline: () => void;
  readonly route: () => Promise<void>;
  readonly sleep: (ms: number) => Promise<void>;
}

/**
 * Opens the app. With one computer, as before multi-computer: wait for home
 * (Setup on a 401, the offline screen after a short grace), then route. With
 * peers stored, home is one more computer: route at once (so the app and a
 * deep link to a peer open even while home sleeps), and home's settings come
 * from its first poll.
 */
export async function startApp(deps: StartDeps): Promise<void> {
  if (deps.computers > 1) {
    await deps.route();
    return;
  }
  for (let failures = 1; ; failures++) {
    try {
      deps.onHome(await deps.homeSettings());
      break;
    } catch (error) {
      if (isAuthFailure(error)) {
        deps.onSetup();
        return;
      }
      const next = offlineRetry(failures);
      if (next.offline) {
        deps.onOffline();
        return;
      }
      await deps.sleep(next.retryIn * 1000);
    }
  }
  await deps.route();
}

/**
 * The add of this load while the user is on another screen (for example in a
 * terminal, to type y on the new computer): waiting, added, or not added.
 * Nothing after a Cancel or once closed.
 */
export function AddBanner({ task, onOpen }: { readonly task: AddTask; readonly onOpen: () => void }) {
  const { label, phase } = task;
  if (task.dismissed === true || phase.kind === 'cancelled') return null;
  const running = phase.kind === 'redeeming' || phase.kind === 'waiting';
  return (
    <Banner
      tone={running ? 'working' : phase.kind === 'done' ? 'success' : 'danger'}
      text={running ? `Adding ${label}… approve on ${label}` : phase.kind === 'done' ? `${label} is added` : `${label} was not added`}
    >
      {phase.kind !== 'done' && <button type="button" className="link" onClick={onOpen}>Open</button>}
      {running
        ? <button type="button" className="link" onClick={cancelAdd}>Cancel</button>
        : <button type="button" className="link" onClick={dismissAdd}>Dismiss</button>}
    </Banner>
  );
}

/** Screens that show the add banner: the app's own screens, not pairing, start-up or the add page itself. */
const BANNER_VIEWS: ReadonlySet<View['name']> = new Set(['sessions', 'wall', 'new', 'live', 'preview', 'settings', 'provider']);

export function App({ grant, add, peerLink }: StartupLinks) {
  const [view, setView] = useState<View>({ name: 'boot' });
  // The add-computer link, dropped when this load is not safe (guardStartup).
  const [addSlot] = useState(() => ({ value: add }));
  const [refusedAdd, setRefusedAdd] = useState(false);
  // True after a load that was not safe and did not reload: grants that arrive later are dropped too.
  const unsafe = useRef(false);
  const [settings, setSettings] = useState<HTTPSettingsResponse>();
  // Home first, then the peers; replaced from home on every load.
  const [computers, setComputers] = useState<ReadonlyArray<Computer>>(storedComputers);
  const computersRef = useRef(computers);
  computersRef.current = computers;
  // Previews are optional: until (or unless) the config answers, there are none.
  const [previewAt, setPreviewAt] = useState<string>();
  const [preview] = useState(() => homePreview(setPreviewAt));
  const viewRef = useRef(view);
  viewRef.current = view;
  const adding = useSyncExternalStore(subscribeAdd, currentAdd, currentAdd);
  const [fontSize, setFontSize] = useFontSize();
  const update = useServiceWorkerUpdate();
  useVisualViewport();

  // In the background every Wall tile pauses and polls wait; back in front,
  // settings are read again (a peer refreshes its cookie on that read).
  useEffect(() => {
    const apply = () => {
      const visible = document.visibilityState === 'visible';
      if (visible) forgetSettings();
      scheduler.setForeground(visible);
    };
    apply();
    document.addEventListener('visibilitychange', apply);
    return () => document.removeEventListener('visibilitychange', apply);
  }, []);

  const navigate = useCallback((next: View) => {
    const path = pathFor(next);
    if (path !== window.location.pathname + window.location.search) {
      window.history[sameScreen(viewRef.current, next) ? 'replaceState' : 'pushState'](next.name === 'preview' ? PREVIEW_TAP : null, '', path);
    }
    setView(next);
  }, []);

  // Home's first good answer in this visit (at start, or when it comes back
  // later): its settings, its computer list and its preview origin. A new
  // pairing or log out starts over (reset).
  const [home] = useState(() => homeAdoption({
    settings: (answer) => {
      setSettings(answer);
      void preview.read();
      // Log out or a revoke deleted home's copy of this browser's subscription.
      void syncHomePush();
    },
    // The stored list is used until home answers; then home's list replaces it.
    list: (list) => {
      setComputers(list);
      const next = leaveRemoved(viewRef.current, list);
      if (next !== undefined) navigate(next);
    },
  }));

  const toSetup = useCallback((reason?: string) => {
    resetAdd();
    home.reset();
    if (window.location.pathname !== '/pair') {
      window.history.replaceState(null, '', '/');
    }
    setSettings(undefined);
    setView(reason === undefined ? { name: 'setup' } : { name: 'setup', reason });
  }, [home]);

  /**
   * The message for a failed request. Only home's 401 ends the app's access;
   * a peer's 401 means "pair again" for that computer alone.
   */
  const handleFailure = useCallback((failure: unknown, from?: Computer): string => {
    // A failed reload names the computer it came from.
    const error = failure instanceof ReadFailure ? failure.error : failure;
    const computer = failure instanceof ReadFailure ? failure.computer : from;
    const peer = computer !== undefined && !computer.home ? computer : undefined;
    if (isAuthFailure(error)) {
      if (peer !== undefined) return pairAgain(peer.label);
      toSetup(UNPAIRED);
    } else if (error instanceof ApiError && error.code === 'network' && peer === undefined && computersRef.current.length < 2) {
      setView({ name: 'offline' });
    }
    return error instanceof Error ? error.message : 'Request failed';
  }, [toSetup]);

  const onHomeAuth = useCallback(() => toSetup(UNPAIRED), [toSetup]);
  const polling = view.name === 'sessions' || view.name === 'wall' || view.name === 'live' || view.name === 'preview'
    || view.name === 'new' || view.name === 'settings' || view.name === 'add-computer' || view.name === 'provider';
  const all = useAllSessions(polling, computers, onHomeAuth, adding?.phase.kind === 'done' ? adding.origin : undefined);
  // Providers (herdr) are read only while Sessions, a provider terminal or Settings (Integrations) shows; their failures never reach `all`.
  const providers = useProviders(view.name === 'sessions' || view.name === 'provider' || view.name === 'settings', all.entries);
  // Push from peers follows the list: at start, when a computer is added or
  // removed (log out included), and when one stops or starts being usable
  // (can't reach, pair again, outdated), so a peer that was offline when push
  // was turned on is subscribed when it comes back.
  const peerPush = all.entries.filter((entry) => !entry.computer.home)
    .map((entry) => `${entry.computer.origin} ${entry.status.kind === 'ok' || entry.status.kind === 'loading' || entry.status.kind === 'connecting' ? '' : entry.status.kind}`).join(',');
  useEffect(() => { void syncPeerPush(computersRef.current.filter((computer) => !computer.home)); }, [peerPush]);
  // Sessions and the Wall sort waiting first; Live's tabs keep each computer's own order.
  const sessions = mergeSessions(all.entries);
  const tabs = all.entries.flatMap((entry) => (entry.status.kind === 'ok' ? entry.status.sessions : []));
  const answered = all.entries.some((entry) => entry.status.kind === 'ok');
  const homeEntry = all.entries.find((entry) => entry.computer.home);
  const reload = all.reload;
  /** Reads one computer again after a change; a failed read shows on its row, not here. */
  const refresh = useCallback((origin: string) => reload(origin).catch(() => undefined), [reload]);

  // Each good home poll (`at` changes; settings are cached between polls).
  const homeAnswer = homeSettings(all.entries);
  const homeAt = homeEntry?.status.kind === 'ok' ? homeEntry.status.at : undefined;
  useEffect(() => {
    if (homeAnswer !== undefined) home.answer(homeAnswer);
  }, [homeAnswer, homeAt, home]);

  const route = useCallback(async () => {
    const target = parseRoute(window.location.pathname);
    switch (target.name) {
      case 'settings': setView({ name: 'settings' }); return;
      case 'wall': setView({ name: 'wall' }); return;
      case 'new': setView({ name: 'new' }); return;
      case 'add-computer': setView({ name: 'add-computer' }); return;
      case 'peer-add': setView({ name: 'peer-add' }); return;
      case 'sessions': setView({ name: 'sessions' }); return;
      case 'unknown':
        window.history.replaceState(null, '', '/');
        setView({ name: 'sessions' });
        return;
      default:
    }
    const computer = computersRef.current.find((item) => (target.host === undefined ? item.home : item.host === target.host));
    if (target.name === 'provider') {
      if (computer === undefined) {
        window.history.replaceState(null, '', '/');
        setView({ name: 'sessions', message: 'That computer is not in your list.' });
        return;
      }
      // Opens at once only when the service worker confirms a notification tap; a link or a reload shows Open.
      setView(await providerArrival(computer, target.provider, target.target, window.location.pathname));
      return;
    }
    if (computer === undefined) {
      window.history.replaceState(null, '', '/');
      setView({ name: 'sessions', message: 'That computer is not in your list.' });
      return;
    }
    try {
      const list = await scheduler.serial(computer.origin, () => client(computer.origin).sessions());
      const found = list.sessions.find((item) => item.id === target.id);
      const port = target.preview ? previewPortParam(window.location.search) : undefined;
      if (found === undefined) {
        setView({ name: 'sessions', message: 'That session no longer exists.' });
        return;
      }
      const session = hosted(found, computer);
      // A home preview link may open before home's first answer (peers stored): read the config now.
      // A peer's preview needs nothing from home, but it needs the tap that opened it.
      if (port !== undefined && (computer.home ? await preview.origin() !== undefined : peerPreviewOnLoad(window.history.state, found.previews, port))) {
        setView({ name: 'preview', session, port });
      } else {
        // A bad port, no preview origin, or a peer's preview address that no tap here opened falls back to the terminal.
        if (target.preview) window.history.replaceState(null, '', pathFor({ name: 'live', session }));
        setView({ name: 'live', session });
      }
    } catch (error) {
      const message = handleFailure(error, computer);
      if (!isAuthFailure(error) || !computer.home) setView({ name: 'sessions', message });
    }
  }, [handleFailure, preview]);

  const boot = useCallback(async () => {
    // Before anything else, and before any pairing request: drop service
    // workers that this app did not register (a compromised home may have),
    // and use no grant on a load that one of them may still see.
    const check = guardStartup({ grant, add: addSlot }, await workersSafe(), storage('sessionStorage'));
    unsafe.current = !check.safe;
    if (check.reload) {
      window.location.reload();
      return;
    }
    if (peerLink !== undefined || window.location.pathname === PEER_ADD_PATH) {
      setView({ name: 'peer-add' });
      return;
    }
    if (check.refused === 'pair') {
      setView({ name: 'setup', reason: STRAY_PAIR });
      return;
    }
    if (check.refused === 'add') setRefusedAdd(true);
    if (grant.value !== undefined) {
      setView({ name: 'setup' });
      return;
    }
    setView({ name: 'boot' });
    await startApp({
      computers: computersRef.current.length,
      homeSettings: () => api.settings(),
      onHome: home.answer,
      onSetup: () => toSetup(),
      onOffline: () => setView({ name: 'offline' }),
      route,
      sleep: (ms) => new Promise((resolve) => { setTimeout(resolve, ms); }),
    });
  }, [addSlot, home, grant, peerLink, route, toSetup]);

  // A new pairing link opened in this same tab only changes the fragment, so
  // the page does not reload. Browsers fire popstate and then hashchange for
  // it; whichever runs first takes the grant and shows pairing.
  const [grantEpoch, setGrantEpoch] = useState(0);
  const adoptLinkGrant = useCallback(() => {
    if (!adoptFragmentGrant(grant, window.location, window.history)) {
      return false;
    }
    void guardLink({ grant, add: addSlot }, unsafe.current).then((check) => {
      unsafe.current = !check.safe;
      if (check.reload) {
        window.location.reload();
      } else if (!check.safe) {
        // A stray worker still sees this page: the grant was not used.
        setView({ name: 'setup', reason: STRAY_PAIR });
      } else {
        setGrantEpoch((epoch) => epoch + 1);
        setView({ name: 'setup' });
      }
    });
    return true;
  }, [addSlot, grant]);

  useEffect(() => {
    void boot();
    const onPop = () => {
      if (!adoptLinkGrant()) {
        void route();
      }
    };
    const onHashChange = () => { adoptLinkGrant(); };
    // A tapped notification while the app is open: its worker sends the
    // address (a peer's worker cannot navigate this window itself).
    const onWorkerMessage = (event: MessageEvent) => {
      const path = notificationPath(event.data, computersRef.current);
      if (path === undefined) return;
      if (path !== window.location.pathname) window.history.pushState(null, '', path);
      void route();
    };
    window.addEventListener('popstate', onPop);
    window.addEventListener('hashchange', onHashChange);
    navigator.serviceWorker?.addEventListener('message', onWorkerMessage);
    return () => {
      window.removeEventListener('popstate', onPop);
      window.removeEventListener('hashchange', onHashChange);
      navigator.serviceWorker?.removeEventListener('message', onWorkerMessage);
    };
  }, [adoptLinkGrant, boot, route]);

  useEffect(() => { document.title = titleFor(view); }, [view]);

  // Offline: keep trying quietly, and at once when the network or the app comes back.
  const offline = view.name === 'offline';
  const [retryIn, setRetryIn] = useState(OFFLINE_RETRY_S);
  const [phoneOnline, setPhoneOnline] = useState(() => navigator.onLine);
  useEffect(() => {
    if (!offline) {
      return undefined;
    }
    // Boot already failed OFFLINE_GRACE_FAILURES times before showing this screen.
    let failures = OFFLINE_GRACE_FAILURES;
    let left = offlineRetry(failures).retryIn;
    setRetryIn(left);
    let busy = false;
    const attempt = async () => {
      if (busy || document.visibilityState !== 'visible') {
        return;
      }
      busy = true;
      try {
        await api.settings();
      } catch (error) {
        if (error instanceof ApiError && error.code === 'network') {
          return;
        }
      } finally {
        busy = false;
      }
      void boot();
    };
    const retry = () => {
      left = offlineRetry(failures).retryIn;
      setRetryIn(left);
      void attempt();
    };
    // Back on the network or in front: try now and start the backoff over.
    const now = () => {
      failures = 1;
      retry();
    };
    const tick = () => {
      if (document.visibilityState !== 'visible') return;
      left -= 1;
      if (left <= 0) {
        failures += 1;
        retry();
      } else {
        setRetryIn(left);
      }
    };
    const onOnline = () => { setPhoneOnline(true); now(); };
    const onOffline = () => setPhoneOnline(false);
    const timer = setInterval(tick, 1000);
    window.addEventListener('online', onOnline);
    window.addEventListener('offline', onOffline);
    document.addEventListener('visibilitychange', now);
    return () => {
      clearInterval(timer);
      window.removeEventListener('online', onOnline);
      window.removeEventListener('offline', onOffline);
      document.removeEventListener('visibilitychange', now);
    };
  }, [offline, boot]);

  // Fetch the terminal code in the background once the session list is up,
  // so opening a session goes straight to the live terminal.
  useEffect(() => {
    if (view.name === 'sessions' || view.name === 'new') {
      loadLive().catch(() => undefined);
      if (view.name === 'sessions') loadProviderScreen().catch(() => undefined);
    }
  }, [view.name]);

  const openSession = useCallback((session: HostedSession) => navigate({ name: 'live', session }), [navigate]);
  // A tap on a provider row or chip in the app: open at once.
  const openTarget = useCallback((tap: ProviderTap) => navigate({ name: 'provider', computer: tap.computer, provider: tap.provider, target: tap.targetId, tapped: newTap() }), [navigate]);
  const toSessions = useCallback(() => navigate({ name: 'sessions' }), [navigate]);
  const reloadComputers = useCallback(() => setComputers(storedComputers()), []);
  // An add that finished in the background: its computer joins the list.
  const added = adding?.phase.kind === 'done';
  useEffect(() => { if (added) reloadComputers(); }, [added, reloadComputers]);

  const startNew = useCallback(async (from: HostedSession, runFirst?: string) => {
    try {
      navigate(await startLike(from, runFirst));
      void refresh(from.computer.origin);
    } catch (error) {
      navigate({ name: 'sessions', message: handleFailure(error, from.computer) });
    }
  }, [handleFailure, navigate, refresh]);
  // The sheet shows a failure itself, so it can suggest another name.
  const startWorktree = useCallback(async (
    from: HostedSession,
    worktree: { readonly name: string; readonly base: string },
    runFirst: string | undefined,
  ) => {
    navigate(await startLike(from, runFirst, worktree));
    void refresh(from.computer.origin);
  }, [navigate, refresh]);

  // Access to one computer ended. Only home's ends the app's access.
  const revoked = useCallback((computer: Computer, reason: string | undefined) => {
    if (computer.home) {
      toSetup(reason ?? 'Access to this computer was revoked.');
      return;
    }
    navigate({ name: 'sessions', message: pairAgain(computer.label) });
    void refresh(computer.origin);
  }, [navigate, refresh, toSetup]);

  // Home's Log out does not reach the peers, so sign out there first.
  const entries = all.entries;
  const logout = useCallback(async () => {
    cancelAdd();
    const note = await logOut(entries.filter((entry) => !entry.computer.home)
      .map((entry) => ({ computer: entry.computer, reachable: entry.status.kind !== 'unreachable' })));
    setComputers(storedComputers());
    window.history.replaceState(null, '', '/');
    toSetup(note);
  }, [entries, toSetup]);

  const liveComputer = view.name === 'live' || view.name === 'preview' ? view.session.computer : undefined;
  const liveStatus = liveComputer === undefined ? undefined : all.entries.find((entry) => entry.computer.origin === liveComputer.origin)?.status;

  return (
    <div className="app">
      {adding !== undefined && BANNER_VIEWS.has(view.name) && (
        <AddBanner task={adding} onOpen={() => navigate({ name: 'add-computer' })} />
      )}
      {view.name === 'boot' && (
        <main className="page state-screen">
          <p className="state-label" role="status"><span className="dot accent pulse" aria-hidden="true" />Termivoa · connecting</p>
        </main>
      )}
      {view.name === 'offline' && (
        <OfflineScreen phoneOnline={phoneOnline} retryIn={retryIn} onRetry={() => { void boot(); }} />
      )}
      {view.name === 'setup' && (
        <Setup
          key={grantEpoch}
          grant={grant}
          {...(view.reason === undefined ? {} : { reason: view.reason })}
          onPaired={() => {
            // A new pairing in this tab: home's answer is used again.
            home.reset();
            window.history.replaceState(null, '', '/');
            void boot();
          }}
        />
      )}
      {(view.name === 'peer-add' || view.name === 'add-computer') && (
        <ErrorBoundary>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening…</p></main>}>
            {view.name === 'peer-add' ? <PeerAddPage link={peerLink} /> : (
              <AddComputerPage
                link={addSlot.value}
                {...(refusedAdd ? { notice: strayAdd(firstLabel(homeOrigin())) } : {})}
                deviceLabel={settings?.device.label}
                onDone={() => {
                  reloadComputers();
                  navigate({ name: 'sessions' });
                }}
                onSettings={() => navigate({ name: 'settings' })}
                onLeave={(path) => {
                  window.history.pushState(null, '', path);
                  void route();
                }}
              />
            )}
          </Suspense>
        </ErrorBoundary>
      )}
      {view.name === 'sessions' && (
        <Sessions
          entries={all.entries}
          sessions={sessions}
          {...(view.message === undefined ? {} : { message: view.message })}
          providers={providers.entries}
          tab={view.tab}
          onOpenTarget={openTarget}
          onCreated={(tap, notice, key) => {
            void providers.reload(key);
            navigate({ name: 'provider', computer: tap.computer, provider: tap.provider, target: tap.targetId, tapped: newTap(), ...(notice === undefined ? {} : { notice }) });
          }}
          onProviderRetry={providers.reload}
          offerPush={homeEntry?.status.kind === 'ok'}
          onOpen={openSession}
          onNew={() => navigate({ name: 'new', back: { name: 'sessions' } })}
          onWall={() => navigate({ name: 'wall' })}
          onSettings={() => navigate({ name: 'settings' })}
          onReload={() => all.reload()}
          onRetry={all.retry}
          onFailure={handleFailure}
          {...(update.available ? { onUpdate: update.reload } : {})}
        />
      )}
      {view.name === 'wall' && (
        <ErrorBoundary>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening wall…</p></main>}>
            <Wall
              sessions={answered ? sessions : undefined}
              multi={computers.length > 1}
              onOpen={openSession}
              onNew={() => navigate({ name: 'new', back: { name: 'wall' } })}
              onList={toSessions}
            />
          </Suspense>
        </ErrorBoundary>
      )}
      {view.name === 'new' && (
        <NewSession
          entries={all.entries}
          {...(view.back?.name === 'live' ? { initial: view.back.session.computer } : {})}
          onCancel={() => navigate(view.back ?? { name: 'sessions' })}
          onStarted={(session, command) => {
            void refresh(session.computer.origin);
            navigate(command === undefined
              ? { name: 'live', session }
              : { name: 'live', session, runFirst: { command, settled: false } });
          }}
          onReload={(origin) => all.reload(origin)}
          onFailure={handleFailure}
        />
      )}
      {view.name === 'live' && (
        <ErrorBoundary key={`${keyOf(view.session)}/${view.session.generation}`}>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening terminal…</p></main>}>
            <Live
              session={view.session}
              sessions={answered ? tabs : undefined}
              settings={liveStatus?.kind === 'ok' ? liveStatus.settings : view.session.computer.home ? settings : undefined}
              multi={computers.length > 1}
              fontSize={fontSize}
              {...(view.runFirst === undefined ? {} : { runFirst: view.runFirst })}
              {...(view.compose === true ? { openComposer: true } : {})}
              {...(previewFor(view.session.computer, liveStatus, previewAt).origin === undefined ? {} : { onPreview: (port: number) => {
                void refresh(view.session.computer.origin);
                navigate({ name: 'preview', session: view.session, port });
              } })}
              onOpen={openSession}
              onNew={() => navigate({ name: 'new', back: { name: 'live', session: view.session } })}
              onSessions={toSessions}
              onSettings={() => navigate({ name: 'settings' })}
              onChanged={() => refresh(view.session.computer.origin)}
              onStartNew={(runFirst) => startNew(view.session, runFirst)}
              onStartWorktree={(worktree, runFirst) => startWorktree(view.session, worktree, runFirst)}
              onRevoked={(reason) => revoked(view.session.computer, reason)}
            />
          </Suspense>
        </ErrorBoundary>
      )}
      {view.name === 'preview' && (
        <ErrorBoundary key={`${keyOf(view.session)}/${view.session.generation}/preview`}>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening preview…</p></main>}>
            <Preview
              session={view.session}
              sessions={tabs.filter((item) => item.computer.origin === view.session.computer.origin)}
              {...previewFor(view.session.computer, liveStatus, previewAt)}
              port={view.port}
              onBack={() => navigate({ name: 'live', session: view.session })}
              onTell={() => navigate({ name: 'live', session: view.session, compose: true })}
            />
          </Suspense>
        </ErrorBoundary>
      )}
      {view.name === 'provider' && (
        <ErrorBoundary key={`${view.computer.host}/${view.provider}/${view.target}`}>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening terminal…</p></main>}>
            <ProviderScreen
              computer={view.computer}
              provider={view.provider}
              targetId={view.target}
              tapped={view.tapped}
              {...(view.notice === undefined ? {} : { notice: view.notice })}
              entry={providers.entries.find((entry) => entry.computer.origin === view.computer.origin && entry.info.id === view.provider)}
              fontSize={fontSize}
              multi={computers.length > 1}
              onChip={(target) => openTarget({ computer: view.computer, provider: view.provider, targetId: target })}
              onGone={() => {
                // Replace the dead address, so Back does not return to it.
                window.history.replaceState(null, '', '/');
                setView({ name: 'sessions', tab: view.provider, message: GONE });
              }}
              onSessions={toSessions}
              onSettings={() => navigate({ name: 'settings' })}
              onFailure={(error) => handleFailure(error, view.computer)}
              onRevoked={(reason) => revoked(view.computer, reason)}
            />
          </Suspense>
        </ErrorBoundary>
      )}
      {view.name === 'settings' && (
        <ErrorBoundary>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening settings…</p></main>}>
            <Settings
              settings={settings}
              entries={all.entries}
              providers={providers.entries}
              fontSize={fontSize}
              onFontSize={setFontSize}
              onBack={toSessions}
              onRenamed={(device) => { if (settings !== undefined) setSettings({ ...settings, device }); }}
              onFailure={handleFailure}
              onLogout={logout}
              onCheck={(origin) => all.reload(origin)}
              onProviderReload={providers.reload}
              onComputersChanged={reloadComputers}
            />
          </Suspense>
        </ErrorBoundary>
      )}
    </div>
  );
}
