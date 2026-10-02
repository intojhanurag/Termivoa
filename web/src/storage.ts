export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

/** localStorage (or sessionStorage), or undefined where reading it throws (some private modes). */
export function storage(kind: 'localStorage' | 'sessionStorage' = 'localStorage'): Storage | undefined {
  try {
    return globalThis[kind];
  } catch {
    return undefined;
  }
}
