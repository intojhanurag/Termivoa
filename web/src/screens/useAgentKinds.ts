import { useEffect, useRef, useState } from 'react';

/** A quiet settings read settles only after the caller has refreshed the provider entry. */
export type ReadAgentKinds = () => void | Promise<void>;

/** Read on sheet open and, for an item menu, on chooser open; never on chooser close. */
export function useAgentKinds(read: ReadAgentKinds | undefined, chooser?: boolean): boolean {
  const callback = useRef(read);
  callback.current = read;
  const first = useRef(true);
  const [checking, setChecking] = useState(read !== undefined);
  // React can replay mount effects in development. Each mount setup owns
  // its first read; a cleaned-up setup must not issue it afterward.
  useEffect(() => { first.current = true; }, []);
  useEffect(() => {
    const needed = first.current || chooser === true;
    first.current = false;
    if (!needed || callback.current === undefined) { setChecking(false); return; }
    let active = true;
    setChecking(true);
    const settle = () => { if (active) setChecking(false); };
    // A failed quiet read keeps the last known choices; normal host validation
    // still rejects a kind that changed after a read. Never retry a mutation.
    void Promise.resolve().then(() => active ? callback.current?.() : undefined).then(settle, settle);
    return () => { active = false; };
  }, [chooser]);
  return checking;
}
