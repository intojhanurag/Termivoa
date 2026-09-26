import { useCallback, useEffect, useState } from 'react';

const FONT_SIZE_KEY = 'pterm.fontSize';
export const DEFAULT_FONT_SIZE = 14;
export const MIN_FONT_SIZE = 10;
export const MAX_FONT_SIZE = 24;

/** Terminal font size is a display preference, never a secret. */
export function useFontSize(): [number, (size: number) => void] {
  const [size, setSize] = useState(() => {
    try {
      const stored = Number(localStorage.getItem(FONT_SIZE_KEY));
      return stored >= MIN_FONT_SIZE && stored <= MAX_FONT_SIZE ? stored : DEFAULT_FONT_SIZE;
    } catch {
      return DEFAULT_FONT_SIZE;
    }
  });
  const update = useCallback((next: number) => {
    const bounded = Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, Math.round(next)));
    setSize(bounded);
    try {
      localStorage.setItem(FONT_SIZE_KEY, String(bounded));
    } catch {
      // Private browsing may refuse storage; the in-memory value still applies.
    }
  }, []);
  return [size, update];
}

/** Keeps the app box equal to the visual viewport so bars stay above the keyboard. */
export function useVisualViewport(): void {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (viewport === null || viewport === undefined) {
      return undefined;
    }
    const apply = () => {
      const style = document.documentElement.style;
      style.setProperty('--viewport-height', `${viewport.height}px`);
      style.setProperty('--viewport-top', `${viewport.offsetTop}px`);
    };
    apply();
    viewport.addEventListener('resize', apply);
    viewport.addEventListener('scroll', apply);
    return () => {
      viewport.removeEventListener('resize', apply);
      viewport.removeEventListener('scroll', apply);
    };
  }, []);
}

/**
 * Registers the asset-only service worker on secure contexts. A waiting update
 * is surfaced to the user; it activates only after an explicit reload.
 */
export function useServiceWorkerUpdate(): { available: boolean; reload: () => void } {
  const [waiting, setWaiting] = useState<ServiceWorker | null>(null);
  useEffect(() => {
    if (!import.meta.env.PROD || !window.isSecureContext || !('serviceWorker' in navigator)) {
      return;
    }
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).then((registration) => {
      const check = () => {
        if (registration.waiting !== null && navigator.serviceWorker.controller !== null) {
          setWaiting(registration.waiting);
        }
      };
      check();
      registration.addEventListener('updatefound', () => {
        const installing = registration.installing;
        installing?.addEventListener('statechange', () => {
          if (installing.state === 'installed') {
            check();
          }
        });
      });
    }).catch(() => undefined);
  }, []);
  const reload = useCallback(() => {
    if (waiting === null) {
      return;
    }
    navigator.serviceWorker.addEventListener('controllerchange', () => window.location.reload(), { once: true });
    waiting.postMessage({ type: 'skip-waiting' });
  }, [waiting]);
  return { available: waiting !== null, reload };
}

export function websocketUrl(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${window.location.host}/api/v1/ws`;
}
