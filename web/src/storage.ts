export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

/**
 * The loop guard of a reload: true once per `value` in this tab (the values
 * are kept space-joined in sessionStorage `key`), false after that, and false
 * when storage cannot remember it (no reload at all then).
 */
export function reloadOnce(key: string, value: string, store: StorageLike | undefined): boolean {
  try {
    if (store === undefined) return false;
    const done = store.getItem(key)?.split(' ') ?? [];
    if (done.includes(value)) return false;
    store.setItem(key, [...done, value].join(' '));
    return true;
  } catch {
    return false;
  }
}

/** Takes `value` out of reloadOnce's set: a later block of it may reload again. */
export function forgetReload(key: string, value: string, store: (StorageLike & Pick<Storage, 'removeItem'>) | undefined): void {
  try {
    const done = store?.getItem(key)?.split(' ') ?? [];
    if (store === undefined || !done.includes(value)) return;
    const rest = done.filter((item) => item !== value);
    if (rest.length === 0) store.removeItem(key);
    else store.setItem(key, rest.join(' '));
  } catch {
    // Nothing to forget.
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
