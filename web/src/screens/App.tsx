import { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { api, ApiError, CONNECTION_UNAVAILABLE, isAuthFailure } from '../api/http';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { ChunkErrorBoundary, loadChunk } from './chunks';
import { useFontSize, useServiceWorkerUpdate, useSessionList, useVisualViewport } from './hooks';
import { NewSession } from './NewSession';
import type { RunFirstRequest } from './presets';
import { Sessions } from './Sessions';
import { Settings } from './Settings';
import { Setup } from './Setup';

// The terminal (xterm, protocol validators) loads only when a session opens,
// so Setup and Sessions start without it.
const loadLive = () => loadChunk(() => import('./Live'));
const Live = lazy(() => loadLive().then((module) => ({ default: module.Live })));

export interface GrantHolder {
  value: string | undefined;
}

type View =
  | { readonly name: 'boot' }
  | { readonly name: 'offline' }
  | { readonly name: 'setup'; readonly reason?: string }
  | { readonly name: 'sessions'; readonly message?: string }
  | { readonly name: 'new'; readonly back?: View }
  | { readonly name: 'live'; readonly session: BrokerRPCSessionRecord; readonly runFirst?: RunFirstRequest }
  | { readonly name: 'settings' };

function pathFor(view: View): string {
  switch (view.name) {
    case 'live': return `/sessions/${encodeURIComponent(view.session.id)}`;
    case 'settings': return '/settings';
    case 'new': return '/new';
    case 'setup': return window.location.pathname === '/pair' ? '/pair' : '/';
    default: return '/';
  }
}

export function App({ grant }: { readonly grant: GrantHolder }) {
  const [view, setView] = useState<View>({ name: 'boot' });
  const [settings, setSettings] = useState<HTTPSettingsResponse>();
  const [fontSize, setFontSize] = useFontSize();
  const update = useServiceWorkerUpdate();
  useVisualViewport();

  const navigate = useCallback((next: View) => {
    const path = pathFor(next);
    if (path !== window.location.pathname) {
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

  const list = useSessionList(view.name === 'sessions' || view.name === 'live', handlePollFailure);

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
    const match = /^\/sessions\/([^/]+)$/u.exec(path);
    if (match?.[1] !== undefined) {
      const id = decodeURIComponent(match[1]);
      try {
        const session = (await api.sessions()).sessions.find((item) => item.id === id);
        setView(session === undefined
          ? { name: 'sessions', message: 'That session no longer exists.' }
          : { name: 'live', session });
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
    try {
      setSettings(await api.settings());
    } catch (error) {
      if (isAuthFailure(error)) {
        toSetup();
      } else {
        setView({ name: 'offline' });
      }
      return;
    }
    await route();
  }, [grant, route, toSetup]);

  useEffect(() => {
    void boot();
    const onPop = () => { void route(); };
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, [boot, route]);

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
      {update.available && (
        <div className="banner" role="status">
          <span>An update is ready.</span>
          <button type="button" onClick={update.reload}>reload to update</button>
        </div>
      )}
      {view.name === 'boot' && <main className="page"><p role="status" className="muted">Loading…</p></main>}
      {view.name === 'offline' && (
        <main className="page">
          <h1>{CONNECTION_UNAVAILABLE}</h1>
          <p className="muted">This phone cannot reach your computer right now. Nothing can run until the connection returns.</p>
          <button type="button" className="primary wide" onClick={() => { void boot(); }}>
            <span aria-hidden="true" className="prompt">❯</span> try again
          </button>
        </main>
      )}
      {view.name === 'setup' && (
        <Setup
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
