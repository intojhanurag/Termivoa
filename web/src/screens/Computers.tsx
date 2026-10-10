import { useEffect, useRef, useState, type RefObject } from 'react';
import { ApiError, deviceLabelProblem, firstLabel, homeOrigin, updateText } from '../api/http';
import { removeComputer, renameComputer, watchFound, type Computer, type FoundState } from '../computers';
import { foundLink, LINK_PROBLEMS, parseAddComputerLink, type AddComputerLink, type AppLocation } from '../pairing/addComputer';
import { SCAN_ASKING, SCAN_POINT, SCAN_PROBLEMS, startScan, type ScanEnd } from '../pairing/scan';
import { AddComputerSheet } from './AddComputer';
import { atLeast, cantReach, CHECKING_MIN_MS, ReadFailure, type ComputerEntry } from './allSessions';
import type { ProviderEntry } from './providers';
import { ComputerCard, troubled } from './SettingsComputer';
import { Button, ConfirmPair, Notice, PairVia } from './ui';

/** What Check again says when the read failed. */
export function checkFailureText(computer: Pick<Computer, 'label'>, failure: unknown, onFailure: (error: unknown) => string): string {
  const error = failure instanceof ReadFailure ? failure.error : failure;
  if (error instanceof ApiError && error.code === 'network') return cantReach(computer.label);
  // The refusal text names the computer by its origin; the row shows the name on this phone.
  if (error instanceof ApiError && error.code === 'computer_outdated') return updateText(computer.label);
  return onFailure(failure);
}

/** How long a Check again result stays under its row. */
const RESULT_MS = 8_000;

/** "vps answered in 0.2 s.": seconds with one decimal, never below 0.1. */
export function answeredText(label: string, ms: number): string {
  return `${label} answered in ${Math.max(0.1, Math.round(ms / 100) / 10).toFixed(1)} s.`;
}

/** Check again on one row: working (`text` undefined) or its result. */
interface Check {
  readonly text?: string;
  readonly error?: boolean;
}

type Edit =
  | { readonly kind: 'none' }
  | { readonly kind: 'rename'; readonly computer: Computer; readonly draft: string }
  | { readonly kind: 'remove'; readonly computer: Computer };

/**
 * A scanned or pasted text, checked the same way: the link for the confirm
 * sheet, or the words for what is wrong with it. A QR code is as untrusted
 * as a pasted link.
 */
export function readLink(text: string, app: AppLocation): AddComputerLink | { readonly problem: string } {
  const link = parseAddComputerLink(text, app);
  return 'problem' in link ? { problem: LINK_PROBLEMS[link.problem] } : link;
}

/**
 * Found on your network: computers that wait in `pterm pair --via` (or
 * setup) for this home. Add gives the row's link to the same confirm sheet
 * as a pasted link. Nothing shows before the first answer. A computer this
 * phone uses and reads now (`working`) is not shown; one in the list that
 * lost its access there can be added again. `homeName` is home's machine
 * name, the one `pterm pair --via` takes (never a name given on this phone).
 */
export function FoundBlock({ found, homeName, working, app, onAdd }: {
  readonly found: FoundState | undefined;
  readonly homeName: string;
  readonly working: ReadonlySet<string>;
  readonly app: AppLocation;
  readonly onAdd: (link: AddComputerLink) => void;
}) {
  if (found === undefined) return null;
  // The same checks as a pasted link; a row that fails them is not shown.
  const rows = found.kind === 'rows'
    ? found.computers.flatMap((row) => {
      const link = working.has(row.origin) ? undefined : foundLink(row, app);
      return link === undefined ? [] : [{ row, link }];
    })
    : [];
  return (
    <section className="stack found" aria-labelledby="found-heading">
      <h3 id="found-heading" className="caps">Found on your network</h3>
      {found.kind === 'no_status' && <Notice tone="warning">{homeName} cannot read its Tailscale status. Check Tailscale on {homeName}.</Notice>}
      {found.kind === 'rows' && rows.length === 0 && (
        <Notice tone="neutral">
          To add another computer, run <code>pterm setup</code> on it, or <PairVia home={homeName} /> if it is already set up. It shows here while it waits.
        </Notice>
      )}
      {rows.length > 0 && (
        <ul className="computer-list">
          {rows.map(({ row, link }) => (
            <li key={row.origin} className="computer-card found-row">
              <div className="computer-head">
                <span className="row-text">
                  <span className="row-title"><span className="computer-name">{row.label}</span></span>
                  <span className="row-meta">{new URL(row.origin).host}</span>
                </span>
                <button type="button" className="primary compact found-add" aria-label={`Add ${row.label}`} onClick={() => onAdd(link)}>Add</button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** The camera view of Scan QR. The video has no sound and no controls; the line under it says what to do. */
export function ScanView({ scanning, videoRef, onPaste, onCancel }: {
  readonly scanning: boolean;
  readonly videoRef?: RefObject<HTMLVideoElement | null>;
  readonly onPaste: () => void;
  readonly onCancel: () => void;
}) {
  return (
    <div className="stack" role="group" aria-label="Scan QR">
      <video ref={videoRef} className="scan-video" playsInline muted aria-label="Camera view" aria-describedby="scan-help" />
      <p id="scan-help" role="status" className="hint-text">{scanning ? SCAN_POINT : SCAN_ASKING}</p>
      <div className="row">
        <button type="button" className="secondary" autoFocus onClick={onCancel}>Cancel</button>
        <button type="button" className="secondary" onClick={onPaste}>Paste link</button>
      </div>
    </div>
  );
}

/** Runs the camera while it is on screen; it stops on a result, on Cancel, on unmount and in the background. */
function ScanQr({ onEnd, onPaste, onCancel }: {
  readonly onEnd: (end: ScanEnd) => void;
  readonly onPaste: () => void;
  readonly onCancel: () => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const [scanning, setScanning] = useState(false);
  const ended = useRef(onEnd);
  ended.current = onEnd;
  useEffect(() => {
    if (video.current === null) return undefined;
    return startScan({ video: video.current, onScanning: () => setScanning(true), onEnd: (end) => ended.current(end) });
  }, []);
  return <ScanView scanning={scanning} videoRef={video} onPaste={onPaste} onCancel={onCancel} />;
}


/** Check again for each computer: its own result under it, so two checks never overwrite each other. */
function useChecks(entries: ReadonlyArray<ComputerEntry>, onCheck: (origin: string) => Promise<void>, onFailure: (error: unknown) => string) {
  const [checks, setChecks] = useState<ReadonlyMap<string, Check>>(new Map());
  const listed = useRef(entries);
  listed.current = entries;

  function checkAgain(computer: Computer) {
    const put = (check: Check | undefined) => setChecks((current) => {
      const next = new Map(current);
      if (check === undefined) next.delete(computer.origin);
      else next.set(computer.origin, check);
      return next;
    });
    const finish = (check: Check) => {
      // The computer may have been removed while it was read.
      if (!listed.current.some((entry) => entry.computer.origin === computer.origin)) {
        put(undefined);
        return;
      }
      put(check);
      setTimeout(() => setChecks((current) => {
        if (current.get(computer.origin) !== check) return current;
        const next = new Map(current);
        next.delete(computer.origin);
        return next;
      }), RESULT_MS);
    };
    put({});
    const started = performance.now();
    atLeast(onCheck(computer.origin).then(() => performance.now() - started), CHECKING_MIN_MS).then(
      (ms) => finish({ text: answeredText(computer.label, ms) }),
      (failure: unknown) => finish({ text: checkFailureText(computer, failure, onFailure), error: true }),
    );
  }
  return { checks, checkAgain };
}

/** "Checking vps…", then what Check again found. */
function CheckLine({ computer, check }: { readonly computer: Computer; readonly check: Check | undefined }) {
  return <p role="status" className={check?.error === true ? 'error field-note' : 'muted field-note'}>{check === undefined ? '' : (check.text ?? `Checking ${computer.label}…`)}</p>;
}

/**
 * Settings → Computers: a card for each computer (tap it for its page; one that cannot be reached has Check again
 * on its card), Found on your network, and Add a computer: scan the QR code that `pterm pair --via` printed, or
 * paste its link.
 */
export function ComputersSection({ entries, providers, adding: startAdding = false, note, onCheck, onFailure, onOpen }: {
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** The providers' latest reads, for the herdr chips. */
  readonly providers?: ReadonlyArray<ProviderEntry> | undefined;
  /** Opens with Add a computer open, in view. */
  readonly adding?: boolean;
  /** What the last rename or remove on a computer page did ("vps is removed."). */
  readonly note?: { readonly text: string; readonly error: boolean } | undefined;
  /** Reads one computer now (one read per computer at a time); rejects with a ReadFailure. */
  readonly onCheck: (origin: string) => Promise<void>;
  readonly onFailure: (error: unknown) => string;
  /** Opens a computer's page. */
  readonly onOpen: (origin: string) => void;
}) {
  const { checks, checkAgain } = useChecks(entries, onCheck, onFailure);
  const [adding, setAdding] = useState<'closed' | 'choose' | 'scan' | 'paste'>(startAdding ? 'choose' : 'closed');
  const [pasted, setPasted] = useState('');
  const [problem, setProblem] = useState<string>();
  const [confirm, setConfirm] = useState<AddComputerLink>();
  const [found, setFound] = useState<FoundState>();
  const addRef = useRef<HTMLDivElement>(null);
  // Only while this screen is open; watchFound pauses in the background.
  useEffect(() => watchFound(setFound), []);
  useEffect(() => { if (startAdding) addRef.current?.scrollIntoView(); }, [startAdding]);
  const now = Date.now();
  // `pterm pair --via` takes home's machine name, never the name given on this phone (#164).
  const homeName = firstLabel(homeOrigin());
  const working = new Set(entries.filter((entry) => !entry.computer.home && entry.status.kind === 'ok').map((entry) => entry.computer.origin));

  /** Scan and paste end here: the problem, or the confirm sheet. */
  function check(text: string): boolean {
    const link = readLink(text, window.location);
    if ('problem' in link) {
      setProblem(link.problem);
      return false;
    }
    setProblem(undefined);
    setConfirm(link);
    return true;
  }

  function scanned(end: ScanEnd) {
    setAdding('choose');
    if (end.kind === 'problem') setProblem(SCAN_PROBLEMS[end.problem]);
    else if (end.kind === 'text' && check(end.text)) setAdding('closed');
  }

  const open = (next: typeof adding) => { setProblem(undefined); setPasted(''); setAdding(next); };

  return (
    <section className="group stack" aria-labelledby="computers-heading">
      <div className="section-head">
        <h2 id="computers-heading" className="caps">Computers</h2>
        <p className="section-hint">Home serves this app to your phone.</p>
      </div>
      <ul className="computer-list sc-cards">
        {entries.map((entry) => {
          const { computer } = entry;
          const checked = checks.get(computer.origin);
          return (
            <ComputerCard key={computer.origin} entry={entry} providers={providers} now={now} onOpen={() => onOpen(computer.origin)}>
              {troubled(entry) && (
                <div className="sc-card-actions">
                  <Button className="secondary compact" busy={checked !== undefined && checked.text === undefined} busyLabel="Checking…" onClick={() => checkAgain(computer)}>Check again</Button>
                  <CheckLine computer={computer} check={checked} />
                </div>
              )}
            </ComputerCard>
          );
        })}
      </ul>
      <p role="status" className={note?.error === true ? 'error field-note' : 'muted field-note'}>{note?.text ?? ''}</p>
      <FoundBlock found={found} homeName={homeName} working={working} app={globalThis.location} onAdd={setConfirm} />

      <div ref={addRef} id="add-computer" className="stack">
        {adding === 'closed' && <button type="button" className="link accent add-computer" onClick={() => open('choose')}>+ Add a computer</button>}
        {adding === 'choose' && (
          <div className="stack" role="group" aria-label="Add a computer">
            {problem !== undefined && <Notice tone="danger" role="alert" title={problem} />}
            <div className="row">
              <button type="button" className="primary" autoFocus onClick={() => open('scan')}>Scan QR</button>
              <button type="button" className="secondary" onClick={() => open('paste')}>Paste link</button>
              <button type="button" className="link" onClick={() => open('closed')}>Cancel</button>
            </div>
          </div>
        )}
        {adding === 'scan' && <ScanQr onEnd={scanned} onPaste={() => open('paste')} onCancel={() => open('closed')} />}
        {adding === 'paste' && (
          <form
            className="stack"
            onSubmit={(event) => {
              event.preventDefault();
              check(pasted);
            }}
          >
            <label htmlFor="add-link">Add-computer link</label>
            <input
              id="add-link"
              autoFocus
              value={pasted}
              placeholder="https://…/add-computer#…"
              autoCapitalize="off"
              autoCorrect="off"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={problem !== undefined}
              aria-describedby="add-link-help"
              onChange={(event) => { setPasted(event.target.value); setProblem(undefined); }}
            />
            <p id="add-link-help" className={problem === undefined ? 'hint-text' : 'error field-note'}>
              {problem ?? 'Paste the whole link that pterm pair --via printed.'}
            </p>
            <div className="row">
              <button type="submit" className="primary" disabled={pasted.trim() === ''}>Check link</button>
              <button type="button" className="secondary" onClick={() => open('closed')}>Cancel</button>
            </div>
          </form>
        )}
        {/* While adding, or when the Found block has no answer: with one, it says what to run (or lists the computer). */}
        {(adding !== 'closed' || found?.kind !== 'rows') && (
          <div className="stack add-hint">
            <p className="hint-text">On the new computer, run:</p>
            <pre className="command"><PairVia home={homeName} /></pre>
            <p className="hint-text">Then scan its QR code or paste its link. In a Termivoa session, tap the link in the terminal instead.</p>
          </div>
        )}
      </div>
      {confirm !== undefined && <AddComputerSheet link={confirm} onClose={() => setConfirm(undefined)} />}
    </section>
  );
}

/**
 * A computer page's "This computer": its projects, Check again, and (not for home) Rename and Remove. Remove signs
 * this phone out there first; `onRemoved` gets the words for the main page.
 */
export function ComputerActions({ entry, projects, onCheck, onChanged, onFailure, onRemoved }: {
  readonly entry: ComputerEntry;
  /** Its project count, when it answered. */
  readonly projects?: number | undefined;
  readonly onCheck: (origin: string) => Promise<void>;
  /** The stored list changed (renamed, removed). */
  readonly onChanged: () => void;
  readonly onFailure: (error: unknown) => string;
  readonly onRemoved: (text: string) => void;
}) {
  const { computer } = entry;
  const { checks, checkAgain } = useChecks([entry], onCheck, onFailure);
  const [edit, setEdit] = useState<Edit>({ kind: 'none' });
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ readonly text: string; readonly error: boolean }>();
  const renameProblem = edit.kind === 'rename' ? deviceLabelProblem(edit.draft.trim()) : undefined;
  const checked = checks.get(computer.origin);

  function run(work: () => Promise<string | undefined>) {
    setBusy(true);
    setNote(undefined);
    work().then((text) => {
      setEdit({ kind: 'none' });
      onChanged();
      if (text !== undefined) onRemoved(text);
    }, (failure: unknown) => setNote({ text: onFailure(failure), error: true })).finally(() => setBusy(false));
  }

  return (
    <section className="group stack" aria-labelledby="this-computer-heading">
      <h2 id="this-computer-heading" className="caps">This computer</h2>
      <ul className="sc-list">
        {projects !== undefined && (
          <li className="sc-row"><span>Projects</span><span className="sc-value">{projects}</span></li>
        )}
        <li className="sc-item">
          <Button className="sc-row" busy={checked !== undefined && checked.text === undefined} busyLabel="Checking…" disabled={busy} onClick={() => checkAgain(computer)}>Check again</Button>
          <CheckLine computer={computer} check={checked} />
        </li>
        {!computer.home && (
          <li className="sc-item">
            <button type="button" className="sc-row" aria-expanded={edit.kind === 'rename'} onClick={() => setEdit(edit.kind === 'rename' ? { kind: 'none' } : { kind: 'rename', computer, draft: computer.label })}>
              <span>Rename</span><span className="sc-value">{computer.label} ›</span>
            </button>
            {edit.kind === 'rename' && (
              <form
                className="stack sc-inline"
                onSubmit={(event) => {
                  event.preventDefault();
                  const label = edit.draft.trim();
                  if (renameProblem === undefined) run(async () => { await renameComputer(computer.origin, label); return undefined; });
                }}
              >
                <label htmlFor="computer-name">Name on this phone</label>
                <input
                  id="computer-name"
                  value={edit.draft}
                  maxLength={64}
                  autoComplete="off"
                  aria-invalid={renameProblem !== undefined}
                  onChange={(event) => setEdit({ ...edit, draft: event.target.value })}
                />
                {renameProblem !== undefined && <p className="error field-note">{renameProblem}</p>}
                <div className="row">
                  <Button type="submit" className="secondary" busy={busy} disabled={renameProblem !== undefined}>Save name</Button>
                  <button type="button" className="link" onClick={() => setEdit({ kind: 'none' })}>Cancel</button>
                </div>
              </form>
            )}
          </li>
        )}
        {!computer.home && (
          <li className="sc-item">
            {edit.kind === 'remove' ? (
              <div role="alertdialog" aria-labelledby="remove-computer" className="confirm stack sc-inline">
                <p id="remove-computer">
                  Remove {computer.label}? This phone signs out of {computer.label} first. Its sessions keep running there.
                </p>
                <ConfirmPair
                  cancel="Keep"
                  confirm={`Remove ${computer.label}`}
                  busy={busy}
                  onCancel={() => setEdit({ kind: 'none' })}
                  onConfirm={() => run(async () => {
                    const result = await removeComputer(computer);
                    return result.loggedOut ? `${computer.label} is removed.` : `${computer.label} is removed. ${result.hint}`;
                  })}
                />
              </div>
            ) : (
              <button type="button" className="sc-row danger-text" onClick={() => setEdit({ kind: 'remove', computer })}>Remove</button>
            )}
          </li>
        )}
      </ul>
      <p role="status" className={note?.error === true ? 'error field-note' : 'muted field-note'}>{note?.text ?? ''}</p>
    </section>
  );
}
