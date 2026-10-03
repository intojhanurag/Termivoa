export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

/**
 * The loop guard of a reload: true once per `value` in this tab (it is kept
 * in sessionStorage `key`), false after that, and false when storage cannot
 * remember it (no reload at all then).
 */
export function reloadOnce(key: string, value: string, store: StorageLike | undefined): boolean {
  try {
    if (store === undefined || store.getItem(key) === value) return false;
    store.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

/** localStorage (or sessionStorage), or undefined where reading it throws (some private modes). */
export function storage(kind: 'localStorage' | 'sessionStorage' = 'localStorage'): Storage | undefined {
  try {
    return globalThis[kind];
  } catch {
    return undefined;
  }
}
