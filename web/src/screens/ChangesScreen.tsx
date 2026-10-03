import type { DiffLine } from '../generated/protocol';
import type { Connection } from '../terminal/Connection';
import { askAboutFile, Changes } from './Changes';
import { ChangesTabs, Files, type ChangesTab } from './Files';

/**
 * The Changes | Files screen, one lazy chunk for Live. The two tabs replace
 * each other; asking about a file fills the composer draft (`draft` is its key).
 */
export function ChangesScreen({ connection, tab, cwd, draft, onTab, onAsked, onReturn }: {
  readonly connection: Connection;
  readonly tab: ChangesTab;
  readonly cwd: string | undefined;
  readonly draft: string;
  readonly onTab: (tab: ChangesTab) => void;
  readonly onAsked: () => void;
  readonly onReturn: () => void;
}) {
  const switcher = <ChangesTabs tab={tab} onTab={onTab} />;
  // Only fills the draft; the user reads it and taps Send.
  const onAsk = (path: string, selected: ReadonlyArray<DiffLine> = []) => {
    askAboutFile(draft, path, selected);
    onAsked();
  };
  return tab === 'changes'
    ? <Changes connection={connection} cwd={cwd} switcher={switcher} onAsk={onAsk} onReturn={onReturn} />
    : <Files connection={connection} switcher={switcher} onAsk={onAsk} onReturn={onReturn} />;
}
