import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError, CONNECTION_UNAVAILABLE, isAuthFailure } from '../api/http';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { ChunkErrorBoundary, loadChunk } from './chunks';
import { useFontSize, useServiceWorkerUpdate, useSessionList, useVisualViewport } from './hooks';
import { adoptFragmentGrant, type GrantSlot } from '../pairing/pairing';
import { NewSession } from './NewSession';
import { OfflineScreen } from './StateScreen';
import { isPreviewPort, previewOrigin } from './PreviewChip';
import type { RunFirstRequest } from './presets';
import { Sessions } from './Sessions';
import { Settings } from './Settings';
import { Setup } from './Setup';

// The terminal (xterm, protocol validators) loads only when a session opens,
// so Setup and Sessions start without it.
const loadLive = () => loadChunk(() => import('./Live'));
const Live = lazy(() => loadLive().then((module) => ({ default: module.Live })));
const Preview = lazy(() => loadChunk(() => import('./Preview')).then((module) => ({ default: module.Preview })));

export type GrantHolder = GrantSlot;

type View =
  | { readonly name: 'boot' }
  | { readonly name: 'offline' }
  | { readonly name: 'setup'; readonly reason?: string }
  | { readonly name: 'sessions'; readonly message?: string }
  | { readonly name: 'new'; readonly back?: View }
  | { readonly name: 'live'; readonly session: BrokerRPCSessionRecord; readonly runFirst?: RunFirstRequest; readonly compose?: boolean }
  | { readonly name: 'preview'; readonly session: BrokerRPCSessionRecord; readonly port: number }
  | { readonly name: 'settings' };

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

const APP_NAME = 'Phone Terminal';

/** The browser tab title: the screen, or the open session's name. */
export function titleFor(view: View): string {
  switch (view.name) {
    case 'live': return `${view.session.title} · ${APP_NAME}`;
    case 'preview': return `Preview :${view.port} · ${APP_NAME}`;
    case 'sessions': return `Sessions · ${APP_NAME}`;
    case 'new': return `New session · ${APP_NAME}`;
    case 'settings': return `Settings · ${APP_NAME}`;
    case 'setup': return `Pair · ${APP_NAME}`;
    case 'offline': return `${CONNECTION_UNAVAILABLE} · ${APP_NAME}`;
    case 'boot': return APP_NAME;
  }
}

export function pathFor(view: View): string {
  switch (view.name) {
    case 'live': return `/sessions/${encodeURIComponent(view.session.id)}`;
    case 'preview': return `/sessions/${encodeURIComponent(view.session.id)}/preview?port=${view.port}`;
    case 'settings': return '/settings';
    case 'new': return '/new';
    case 'setup': return window.location.pathname === '/pair' ? '/pair' : '/';
    default: return '/';
  }
}

/** The port of a preview address's `?port=`, or undefined unless it is a plain valid port number. */
export function previewPortParam(search: string): number | undefined {
  const raw = new URLSearchParams(search).get('port');
  const port = raw !== null && /^\d{1,5}$/u.test(raw) ? Number(raw) : undefined;
  return isPreviewPort(port) ? port : undefined;
}

export function App({ grant }: { readonly grant: GrantHolder }) {
  const [view, setView] = useState<View>({ name: 'boot' });
  const [settings, setSettings] = useState<HTTPSettingsResponse>();
  // Previews are optional: until (or unless) the config answers, there are none.
  const [previewAt, setPreviewAt] = useState<string>();
  const previewConfig = useRef<Promise<string | undefined>>(Promise.resolve(undefined));
  const [fontSize, setFontSize] = useFontSize();
  const update = useServiceWorkerUpdate();
  useVisualViewport();

  const navigate = useCallback((next: View) => {
    const path = pathFor(next);
    if (path !== window.location.pathname + window.location.search) {
      window.history.pushState(null, '', path);
    }
    setView(next);
  }, []);

  const toSetup = useCallback((reason?: string) => {
    if (window.location.pathname !== '/pair') {
      window.history.replaceState(null, '', '/');
    }
    setSettings(undefined);
    setView(reason === undefined ? { name: 'setup' } : { name: 'setup', reason });
  }, []);

  const handleFailure = useCallback((error: unknown): string => {
    if (isAuthFailure(error)) {
      toSetup('This phone is no longer paired with your computer.');
    } else if (error instanceof ApiError && error.code === 'network') {
      setView({ name: 'offline' });
    }
    return error instanceof Error ? error.message : 'Request failed';
  }, [toSetup]);

  // A failed background refresh only matters when access is gone; the list
  // shows "cannot reach" itself and the live terminal has its own link state.
  const handlePollFailure = useCallback((error: unknown) => {
    if (isAuthFailure(error)) {
      toSetup('This phone is no longer paired with your computer.');
    }
  }, [toSetup]);

  const list = useSessionList(view.name === 'sessions' || view.name === 'live' || view.name === 'preview', handlePollFailure);

  const route = useCallback(async () => {
    const path = window.location.pathname;
    if (path === '/settings') {
      setView({ name: 'settings' });
      return;
    }
    if (path === '/new') {
      setView({ name: 'new' });
      return;
    }
    const match = /^\/sessions\/([^/]+)(\/preview)?$/u.exec(path);
    if (match?.[1] !== undefined) {
      const id = decodeURIComponent(match[1]);
      try {
        const session = (await api.sessions()).sessions.find((item) => item.id === id);
        const port = match[2] === undefined ? undefined : previewPortParam(window.location.search);
        if (session === undefined) {
          setView({ name: 'sessions', message: 'That session no longer exists.' });
        } else if (port !== undefined && await previewConfig.current !== undefined) {
          setView({ name: 'preview', session, port });
        } else {
          // A bad port or no preview origin falls back to the terminal.
          if (match[2] !== undefined) window.history.replaceState(null, '', pathFor({ name: 'live', session }));
          setView({ name: 'live', session });
        }
      } catch (error) {
        handleFailure(error);
      }
      return;
    }
    if (path !== '/') {
      window.history.replaceState(null, '', '/');
    }
    setView({ name: 'sessions' });
  }, [handleFailure]);

  const boot = useCallback(async () => {
    if (grant.value !== undefined) {
      setView({ name: 'setup' });
      return;
    }
    setView({ name: 'boot' });
    for (let failures = 1; ; failures++) {
      try {
        setSettings(await api.settings());
        break;
      } catch (error) {
        if (isAuthFailure(error)) {
          toSetup();
          return;
        }
        const next = offlineRetry(failures);
        if (next.offline) {
          setView({ name: 'offline' });
          return;
        }
        await new Promise((resolve) => { setTimeout(resolve, next.retryIn * 1000); });
      }
    }
    // Never blocks boot: a failed or empty answer means no previews.
    previewConfig.current = api.previewConfig().then((config) => previewOrigin(config.origin), () => undefined);
    void previewConfig.current.then(setPreviewAt);
    await route();
  }, [grant, route, toSetup]);

  // A new pairing link opened in this same tab only changes the fragment, so
  // the page does not reload. Browsers fire popstate and then hashchange for
  // it; whichever runs first takes the grant and shows pairing.
  const [grantEpoch, setGrantEpoch] = useState(0);
  const adoptLinkGrant = useCallback(() => {
    if (!adoptFragmentGrant(grant, window.location, window.history)) {
      return false;
    }
    setGrantEpoch((epoch) => epoch + 1);
    setView({ name: 'setup' });
    return true;
  }, [grant]);

  useEffect(() => {
    void boot();
    const onPop = () => {
      if (!adoptLinkGrant()) {
        void route();
      }
    };
    const onHashChange = () => { adoptLinkGrant(); };
    window.addEventListener('popstate', onPop);
    window.addEventListener('hashchange', onHashChange);
    return () => {
      window.removeEventListener('popstate', onPop);
      window.removeEventListener('hashchange', onHashChange);
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
    }
  }, [view.name]);

  const refreshSettings = useCallback(async () => {
    setSettings(await api.settings());
  }, []);

  const openSession = useCallback((session: BrokerRPCSessionRecord) => navigate({ name: 'live', session }), [navigate]);
  const toSessions = useCallback(() => navigate({ name: 'sessions' }), [navigate]);

  const startNew = useCallback(async (from: BrokerRPCSessionRecord) => {
    try {
      const created = await api.createSession({
        operationId: crypto.randomUUID(),
        projectId: from.projectId,
        shellProfileId: from.shellProfileId,
      });
      navigate({ name: 'live', session: created.session });
    } catch (error) {
      navigate({ name: 'sessions', message: handleFailure(error) });
    }
  }, [handleFailure, navigate]);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch (error) {
      if (!isAuthFailure(error)) {
        throw error;
      }
    }
    window.history.replaceState(null, '', '/');
    toSetup('You logged out. This phone no longer has access.');
  }, [toSetup]);

  return (
    <div className="app">
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
            window.history.replaceState(null, '', '/');
            void boot();
          }}
        />
      )}
      {view.name === 'sessions' && settings !== undefined && (
        <Sessions
          settings={settings}
          list={list}
          {...(view.message === undefined ? {} : { message: view.message })}
          onOpen={openSession}
          onNew={() => navigate({ name: 'new', back: { name: 'sessions' } })}
          onSettings={() => navigate({ name: 'settings' })}
          onRefreshSettings={refreshSettings}
          onFailure={handleFailure}
          {...(update.available ? { onUpdate: update.reload } : {})}
        />
      )}
      {view.name === 'new' && settings !== undefined && (
        <NewSession
          settings={settings}
          onCancel={() => navigate(view.back ?? { name: 'sessions' })}
          onStarted={(session, command) => {
            void list.reload();
            navigate(command === undefined
              ? { name: 'live', session }
              : { name: 'live', session, runFirst: { command, settled: false } });
          }}
          onRefreshSettings={refreshSettings}
          onFailure={handleFailure}
        />
      )}
      {view.name === 'live' && (
        <ChunkErrorBoundary key={`${view.session.id}/${view.session.generation}`}>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening terminal…</p></main>}>
            <Live
              session={view.session}
              sessions={list.sessions}
              settings={settings}
              fontSize={fontSize}
              {...(view.runFirst === undefined ? {} : { runFirst: view.runFirst })}
              {...(view.compose === true ? { openComposer: true } : {})}
              {...(previewAt === undefined ? {} : { onPreview: (port: number) => {
                void list.reload();
                navigate({ name: 'preview', session: view.session, port });
              } })}
              onOpen={openSession}
              onNew={() => navigate({ name: 'new', back: { name: 'live', session: view.session } })}
              onSessions={toSessions}
              onSettings={() => navigate({ name: 'settings' })}
              onChanged={list.reload}
              onStartNew={() => { void startNew(view.session); }}
              onRevoked={(reason) => toSetup(reason ?? 'Access to this computer was revoked.')}
            />
          </Suspense>
        </ChunkErrorBoundary>
      )}
      {view.name === 'preview' && previewAt !== undefined && (
        <ChunkErrorBoundary key={`${view.session.id}/${view.session.generation}/preview`}>
          <Suspense fallback={<main className="page"><p role="status" className="muted">Opening preview…</p></main>}>
            <Preview
              session={view.session}
              sessions={list.sessions}
              origin={previewAt}
              port={view.port}
              onBack={() => navigate({ name: 'live', session: view.session })}
              onTell={() => navigate({ name: 'live', session: view.session, compose: true })}
            />
          </Suspense>
        </ChunkErrorBoundary>
      )}
      {view.name === 'settings' && settings !== undefined && (
        <Settings
          settings={settings}
          fontSize={fontSize}
          onFontSize={setFontSize}
          onBack={toSessions}
          onRenamed={(device) => setSettings({ ...settings, device })}
          onFailure={handleFailure}
          onLogout={logout}
        />
      )}
    </div>
  );
}
