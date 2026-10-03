import { useEffect, useRef, useState, type RefObject } from 'react';
import { ApiError, deviceLabelProblem, firstLabel, homeOrigin, updateText } from '../api/http';
import { removeComputer, renameComputer, watchFound, type Computer, type FoundState } from '../computers';
import { foundLink, LINK_PROBLEMS, parseAddComputerLink, type AddComputerLink, type AppLocation } from '../pairing/addComputer';
import { SCAN_ASKING, SCAN_POINT, SCAN_PROBLEMS, startScan, type ScanEnd } from '../pairing/scan';
import { AddComputerSheet } from './AddComputer';
import { cantReach, ReadFailure, type ComputerEntry } from './allSessions';
import { ago, problemText } from './Sessions';
import { Button, Icon, Notice, type IconName } from './ui';

/** The right-hand word for a computer in the list. */
export function onlineText(entry: ComputerEntry): { readonly text: string; readonly tone: 'green' | 'muted' | 'amber' } {
  switch (entry.status.kind) {
    case 'ok': return { text: 'online', tone: 'green' };
    case 'loading': return { text: 'checking', tone: 'muted' };
    case 'unreachable': return { text: 'offline', tone: 'muted' };
    default: return { text: 'needs you', tone: 'amber' };
  }
}

/** The icon in a computer's tile: what is wrong, else a computer (home) or a server. */
function tileIcon(entry: ComputerEntry): IconName {
  switch (entry.status.kind) {
    case 'unreachable': return 'wifi-off';
    case 'pair': return 'lock';
    case 'outdated': return 'download';
    default: return entry.computer.home ? 'monitor' : 'server';
  }
}

/** "2 sessions · vps.tailx.ts.net" ("2 sessions" for home, which has the Home badge), or the problem ("vps · can't reach · last seen 3 h ago"). */
export function computerDetail(entry: ComputerEntry, now: number): string {
  const problem = problemText(entry, now);
  if (problem !== undefined) return problem;
  const count = entry.status.kind === 'ok' ? entry.status.sessions.length : undefined;
  return [
    count === undefined ? undefined : `${count} ${count === 1 ? 'session' : 'sessions'}`,
    entry.computer.home ? undefined : entry.computer.host,
  ].filter((part) => part !== undefined).join(' · ');
}

/**
 * "Termivoa 0.1.0-beta.2+1a2b3c4d5e6f · last seen just now": the build the
 * computer runs (plain text, bounded by the schema) and when this phone last
 * read it. Undefined when it did not answer (computerDetail says why).
 */
export function healthText(entry: ComputerEntry, now: number): string | undefined {
  const { status } = entry;
  return status.kind === 'ok' ? `Termivoa ${status.settings.hostBuild} · ${ago(status.at, now)}` : undefined;
}

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
        <Notice tone="neutral" icon="monitor">
          To add another computer, run <code>pterm setup</code> on it, or <code>pterm pair --via {homeName}</code> if it is already set up. It shows here while it waits.
        </Notice>
      )}
      {rows.length > 0 && (
        <ul className="computer-list">
          {rows.map(({ row, link }) => (
            <li key={row.origin} className="computer-card found-row">
              <div className="computer-head">
                <span className="computer-tile" aria-hidden="true"><Icon name="monitor" /></span>
                <span className="row-text">
                  <span className="row-title"><span className="computer-name">{row.label}</span></span>
                  <span className="row-meta">{new URL(row.origin).host}</span>
                </span>
                <button type="button" className="pill-lit found-add" aria-label={`Add ${row.label}`} onClick={() => onAdd(link)}><Icon name="plus" />Add</button>
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

/**
 * Settings → Computers: every computer with online or offline, its version
 * and when it last answered, Check again, rename, remove (signs this phone
 * out there first), and Add a computer: scan the QR code that
 * `pterm pair --via` printed, or paste its link.
 */
export function ComputersSection({ entries, onCheck, onChanged, onFailure }: {
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** Reads one computer now (one read per computer at a time); rejects with a ReadFailure. */
  readonly onCheck: (origin: string) => Promise<void>;
  /** The stored list changed (renamed, removed). */
  readonly onChanged: () => void;
  readonly onFailure: (error: unknown) => string;
}) {
  const [edit, setEdit] = useState<Edit>({ kind: 'none' });
  const [busy, setBusy] = useState(false);
  const [checks, setChecks] = useState<ReadonlyMap<string, Check>>(new Map());
  const listed = useRef(entries);
  listed.current = entries;
  const [note, setNote] = useState<{ readonly text: string; readonly error: boolean }>();
  const [adding, setAdding] = useState<'closed' | 'choose' | 'scan' | 'paste'>('closed');
  const [pasted, setPasted] = useState('');
  const [problem, setProblem] = useState<string>();
  const [confirm, setConfirm] = useState<AddComputerLink>();
  const [found, setFound] = useState<FoundState>();
  // Only while this screen is open; watchFound pauses in the background.
  useEffect(() => watchFound(setFound), []);
  const now = Date.now();
  const home = entries.find((entry) => entry.computer.home)?.computer;
  const working = new Set(entries.filter((entry) => !entry.computer.home && entry.status.kind === 'ok').map((entry) => entry.computer.origin));
  const renameProblem = edit.kind === 'rename' ? deviceLabelProblem(edit.draft.trim()) : undefined;

  function run(work: () => Promise<string | undefined>) {
    setBusy(true);
    setNote(undefined);
    work().then((text) => {
      setEdit({ kind: 'none' });
      onChanged();
      if (text !== undefined) setNote({ text, error: false });
    }, (failure: unknown) => setNote({ text: onFailure(failure), error: true })).finally(() => setBusy(false));
  }

  /** Each row has its own result, so two checks never overwrite each other (or the rename/remove note). */
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
    onCheck(computer.origin).then(
      () => finish({ text: `${computer.label} answered.` }),
      (failure: unknown) => finish({ text: checkFailureText(computer, failure, onFailure), error: true }),
    );
  }

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
      <h2 id="computers-heading" className="caps">Computers</h2>
      <p className="hint-text">Home serves this app to your phone.</p>
      <ul className="computer-list">
        {entries.map((entry) => {
          const { computer } = entry;
          const online = onlineText(entry);
          const health = healthText(entry, now);
          // Empty for home while it loads: "checking" on the right says it.
          const detail = computerDetail(entry, now);
          const checked = checks.get(computer.origin);
          const working = checked !== undefined && checked.text === undefined;
          return (
            <li key={computer.origin} className={`computer-card is-${entry.status.kind}`}>
              <div className="computer-head">
                <span className="computer-tile" aria-hidden="true"><Icon name={tileIcon(entry)} /></span>
                <span className="row-text">
                  <span className="row-title">
                    <span className="computer-dot" style={{ background: computer.color }} aria-hidden="true" />
                    <span className="computer-name">{computer.label}</span>
                    {computer.home && <span className="badge accent">Home</span>}
                  </span>
                  {detail !== '' && <span className="row-meta">{detail}</span>}
                  {health !== undefined && <span className="row-meta">{health}</span>}
                </span>
                <span className={`computer-state ${online.tone}`}>{online.text}</span>
              </div>
              {edit.kind === 'rename' && edit.computer.origin === computer.origin && (
                <form
                  className="stack"
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
              {edit.kind === 'remove' && edit.computer.origin === computer.origin && (
                <div role="alertdialog" aria-labelledby="remove-computer" className="confirm stack">
                  <p id="remove-computer">
                    Remove {computer.label}? This phone signs out of {computer.label} first. Its sessions keep running there.
                  </p>
                  <div className="row">
                    <Button
                      className="danger"
                      busy={busy}
                      onClick={() => run(async () => {
                        const result = await removeComputer(computer);
                        return result.loggedOut ? `${computer.label} is removed.` : `${computer.label} is removed. ${result.hint}`;
                      })}
                    >Remove {computer.label}</Button>
                    <button type="button" className="secondary" onClick={() => setEdit({ kind: 'none' })}>Keep</button>
                  </div>
                </div>
              )}
              <p role="status" className={checked?.error === true ? 'error field-note' : 'muted field-note'}>{checked === undefined ? '' : (checked.text ?? `Checking ${computer.label}…`)}</p>
              {edit.kind === 'none' && (
                <div className="row computer-actions">
                  <Button
                    className={entry.status.kind === 'ok' || entry.status.kind === 'loading' ? 'link' : 'secondary compact'}
                    icon={<Icon name="refresh" />}
                    busy={working}
                    busyLabel="Checking…"
                    disabled={busy}
                    onClick={() => checkAgain(computer)}
                  >Check again</Button>
                  {!computer.home && (
                    <>
                      <button type="button" className="link" onClick={() => setEdit({ kind: 'rename', computer, draft: computer.label })}>Rename</button>
                      <button type="button" className="link danger-text" onClick={() => setEdit({ kind: 'remove', computer })}>Remove</button>
                    </>
                  )}
                </div>
              )}
            </li>
          );
        })}
      </ul>
      <p role="status" className={note?.error === true ? 'error field-note' : 'muted field-note'}>{note?.text ?? ''}</p>
      <FoundBlock found={found} homeName={firstLabel(homeOrigin())} working={working} app={globalThis.location} onAdd={setConfirm} />

      {adding === 'closed' && <button type="button" className="secondary add-computer" onClick={() => open('choose')}>+ Add a computer</button>}
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
      <div className="card stack">
        <p className="strong">On the new computer, run:</p>
        <pre className="command"><code>pterm pair --via {home?.label ?? '<this computer>'}</code></pre>
        <p className="hint-text">
          Then tap <strong>+ Add a computer</strong> and scan the QR code it shows, or paste the link it prints. If you run it in a Termivoa session, tap the link in the terminal instead.
        </p>
      </div>
      {confirm !== undefined && <AddComputerSheet link={confirm} onClose={() => setConfirm(undefined)} />}
    </section>
  );
}
