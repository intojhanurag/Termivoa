import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, CONNECTION_UNAVAILABLE, isAuthFailure } from '../api/http';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { useFontSize, useServiceWorkerUpdate, useVisualViewport } from './hooks';
import { Live } from './Live';
import { Sessions } from './Sessions';
import { Settings } from './Settings';
import { Setup } from './Setup';

export interface GrantHolder {
  value: string | undefined;
}

type View =
  | { readonly name: 'boot' }
  | { readonly name: 'offline' }
  | { readonly name: 'setup'; readonly reason?: string }
  | { readonly name: 'sessions'; readonly message?: string }
  | { readonly name: 'live'; readonly session: BrokerRPCSessionRecord }
  | { readonly name: 'settings' };

function pathFor(view: View): string {
  switch (view.name) {
    case 'live': return `/sessions/${encodeURIComponent(view.session.id)}`;
    case 'settings': return '/settings';
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
      toSetup('This device is no longer paired.');
    } else if (error instanceof ApiError && error.code === 'network') {
      setView({ name: 'offline' });
    }
    return error instanceof Error ? error.message : 'Request failed';
  }, [toSetup]);

  const route = useCallback(async () => {
    const path = window.location.pathname;
    if (path === '/settings') {
      setView({ name: 'settings' });
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
    toSetup('You logged out. Pair again to use this device.');
  }, [toSetup]);

  return (
    <div className="app">
      {update.available && (
        <div className="banner" role="status">
          <span>An update is ready.</span>
          <button type="button" onClick={update.reload}>Reload to update</button>
        </div>
      )}
      {view.name === 'boot' && <main className="page"><p role="status">Loading…</p></main>}
      {view.name === 'offline' && (
        <main className="page">
          <h1>{CONNECTION_UNAVAILABLE}</h1>
          <p>This phone cannot reach your computer right now. Nothing can run until the connection returns.</p>
          <button type="button" className="primary" onClick={() => { void boot(); }}>Try again</button>
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
          {...(view.message === undefined ? {} : { message: view.message })}
          onOpen={(session) => navigate({ name: 'live', session })}
          onSettings={() => navigate({ name: 'settings' })}
          onFailure={handleFailure}
        />
      )}
      {view.name === 'live' && (
        <Live
          key={`${view.session.id}/${view.session.generation}`}
          session={view.session}
          settings={settings}
          fontSize={fontSize}
          onSessions={() => navigate({ name: 'sessions' })}
          onStartNew={() => { void startNew(view.session); }}
          onRevoked={(reason) => toSetup(reason ?? 'Access to this computer was revoked.')}
        />
      )}
      {view.name === 'settings' && settings !== undefined && (
        <Settings
          settings={settings}
          fontSize={fontSize}
          onFontSize={setFontSize}
          onBack={() => navigate({ name: 'sessions' })}
          onLogout={logout}
        />
      )}
    </div>
  );
}
