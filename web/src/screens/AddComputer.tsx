import { useEffect, useState, useSyncExternalStore } from 'react';
import { ApiError, deviceLabelProblem, firstLabel, homeOrigin } from '../api/http';
import { forgetPending } from '../computers';
import type { TailnetNode } from '../generated/protocol';
import {
  addComputerUrl, cancelAdd, currentAdd, dismissAdd, fromTerminal, prepareAddComputer, startAdd, subscribeAdd, type AddComputerLink,
} from '../pairing/addComputer';
import { CodeTiles } from './Setup';
import { Sheet, SheetHeader } from './Sheet';
import { Notice, PairVia } from './ui';

/**
 * Adding a computer (docs/multi-computer.md, "Pairing a second computer"):
 * a pasted or tapped link opens the confirm sheet, which never adds in one
 * tap; Add loads /computers/add (so home's CSP allows the new computer),
 * where the page shows the full origin again and, only after Continue,
 * redeems the link, shows the code and waits for approval.
 */

/** Plain words for a failed step. Home's own words are shown; a peer's never are (the client replaces them). */
export function addFailure(error: unknown, label: string): string {
  if (error instanceof ApiError) {
    if (error.code === 'network') return `This phone cannot reach ${label}. Check that it is on and in your tailnet.`;
    if (error.code === 'computer_outdated' || error.code === 'app_outdated') return error.message;
    if (error.status === 410 || error.code === 'pairing_expired') return 'This add-computer link expired or was already used.';
    if (error.status === 429 || error.code === 'limit_exceeded') return 'Too many attempts. Wait a minute first.';
    return error.message;
  }
  return `Could not add ${label}.`;
}

/** Who the computer belongs to, as its home's tailscale status says: owner, or tags for a tagged node. */
export function ownerText(node: TailnetNode): { readonly term: 'Owner' | 'Tags'; readonly value: string } {
  return node.owner !== undefined ? { term: 'Owner', value: node.owner } : { term: 'Tags', value: node.tags.join(', ') || 'none' };
}

/** The confirm step: full origin, machine name and owner or tags, then an explicit Add. Cancel deletes the pending entry on home. */
export function AddComputerConfirm({ link, node, onConfirm, onCancel }: {
  readonly link: AddComputerLink;
  readonly node: TailnetNode;
  readonly onConfirm: () => void;
  readonly onCancel: () => void;
}) {
  const label = firstLabel(link.origin);
  const owner = ownerText(node);
  return (
    <>
      <dl className="facts">
        <dt>Address</dt><dd>{link.origin}</dd>
        <dt>Machine</dt><dd>{label}</dd>
        <dt>{owner.term}</dt><dd>{owner.value}</dd>
      </dl>
      <p className="hint-text add-computer-hint">
        Add it only if you just ran <code>pterm setup</code> or <PairVia /> on {label}. Next, this phone shows a code. Check it on {label}.
      </p>
      <div className="row">
        <button type="button" className="primary" onClick={onConfirm}>Add {label}</button>
        <button type="button" className="secondary" onClick={() => { void forgetPending(link.origin); onCancel(); }}>Cancel</button>
      </div>
    </>
  );
}

/**
 * The first step on /computers/add: the full origin, then an explicit
 * Continue before anything is sent to the computer. A link can reach this
 * page in other ways than the confirm sheet, so the page never redeems on
 * load. Cancel deletes the pending entry on home.
 */
export function AddComputerContinue({ link, onContinue, onCancel }: {
  readonly link: AddComputerLink;
  readonly onContinue: () => void;
  readonly onCancel: () => void;
}) {
  const label = firstLabel(link.origin);
  return (
    <>
      <dl className="facts">
        <dt>Address</dt><dd>{link.origin}</dd>
      </dl>
      <p className="hint-text add-computer-hint">
        Continue only if you just ran <code>pterm setup</code> or <PairVia /> on {label}. Next, this phone shows a code. Check it on {label}.
      </p>
      <div className="row">
        <button type="button" className="primary" onClick={onContinue}>Continue</button>
        <button type="button" className="secondary" onClick={() => { void forgetPending(link.origin); onCancel(); }}>Cancel</button>
      </div>
    </>
  );
}

type Check =
  | { readonly kind: 'checking' }
  | { readonly kind: 'ready'; readonly node: TailnetNode }
  | { readonly kind: 'error'; readonly message: string };

/**
 * The confirm sheet for an add-computer link, drawn outside the terminal.
 * Home first checks the computer in its own tailscale status (same tailnet,
 * same owner, or tagged) and records it as pending; only then can the user
 * tap Add.
 */
export function AddComputerSheet({ link, onClose, go = (url) => window.location.assign(url) }: {
  readonly link: AddComputerLink;
  readonly onClose: () => void;
  /** Loads the add page (a full load, so home's CSP allows the new computer). */
  readonly go?: (url: string) => void;
}) {
  const [check, setCheck] = useState<Check>({ kind: 'checking' });
  const label = firstLabel(link.origin);
  useEffect(() => {
    let live = true;
    prepareAddComputer(link).then(
      // Closed while home was checking: the pending entry is not needed.
      (answer) => { if (live) setCheck({ kind: 'ready', node: answer.node }); else void forgetPending(link.origin); },
      (error: unknown) => { if (live) setCheck({ kind: 'error', message: addFailure(error, 'your home computer') }); },
    );
    return () => { live = false; };
  }, [link]);
  const close = () => {
    if (check.kind === 'ready') void forgetPending(link.origin);
    onClose();
  };
  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="add-computer-title" onClose={close}>
        <SheetHeader id="add-computer-title" title="Add this computer?">
          <button type="button" className="link" onClick={close}>Close</button>
        </SheetHeader>
        {check.kind === 'checking' && <Notice tone="working" role="status" title={`Checking ${label}…`} />}
        {check.kind === 'error' && <Notice tone="danger" role="alert" title={check.message} />}
        {check.kind === 'ready' && (
          <AddComputerConfirm link={link} node={check.node} onConfirm={() => { cancelAdd(); go(addComputerUrl(link, globalThis.location?.pathname)); }} onCancel={onClose} />
        )}
      </Sheet>
    </div>
  );
}

/**
 * The /computers/add page: Continue, then the add runs outside the page
 * (startAdd), so the user can leave to type y in a terminal of this app and
 * come back; App shows a banner meanwhile.
 */
export function AddComputerPage({ link, notice, deviceLabel, onDone, onSettings, onLeave }: {
  /** Undefined when the page was opened without a valid link (or reloaded: the link is used once). */
  readonly link: AddComputerLink | undefined;
  /** Shown instead of the usual text when there is no link (the app refused it on this load). */
  readonly notice?: string | undefined;
  /** This phone's name on home, sent to the new computer. */
  readonly deviceLabel: string | undefined;
  readonly onDone: () => void;
  readonly onSettings: () => void;
  /** Goes to an app path in this load; the add keeps running behind the banner. */
  readonly onLeave: (path: string) => void;
}) {
  const task = useSyncExternalStore(subscribeAdd, currentAdd, currentAdd);
  const phase = task?.phase;
  const label = task?.label ?? (link === undefined ? '' : firstLabel(link.origin));
  const name = deviceLabel !== undefined && deviceLabelProblem(deviceLabel) === undefined ? deviceLabel : 'Phone';
  const leave = (then: () => void) => () => { dismissAdd(); then(); };

  const title = phase?.kind === 'waiting' ? `Does ${label} show this code?`
    : phase?.kind === 'done' ? `${phase.computer.label} is added` : 'Add a computer';

  return (
    <main className="page screen setup">
      <p className="wordmark"><span className="dot accent" aria-hidden="true" />termivoa</p>
      <h1>{title}</h1>
      {phase === undefined && link === undefined && (
        <>
          <Notice tone="danger" role="alert" title={notice ?? 'This add-computer link is used or incomplete. Scan or paste a new link in Settings, under Computers.'} />
          <button type="button" className="primary" onClick={onSettings}>Settings</button>
        </>
      )}
      {phase === undefined && link !== undefined && (
        <AddComputerContinue link={link} onContinue={() => { void startAdd(link, name); }} onCancel={onSettings} />
      )}
      {phase?.kind === 'redeeming' && <Notice tone="working" role="status" title={`Connecting to ${label}…`} />}
      {phase?.kind === 'waiting' && (
        <section className="stack pair-wait" aria-live="polite">
          <CodeTiles code={phase.pending.verificationCode} />
          <p className="muted">If it matches, type y in the terminal on {label}.</p>
          <div className="waiting">
            <p role="status"><span className="dots" aria-hidden="true"><span /><span /><span /></span>Waiting for {label}</p>
            <button type="button" className="link" onClick={cancelAdd}>Codes don't match? Cancel</button>
          </div>
          <LeaveAdd back={link?.back} onLeave={onLeave} />
        </section>
      )}
      {phase?.kind === 'done' && (
        <>
          <Notice tone="success" title="Its sessions now show in your list." />
          <button type="button" className="primary wide" onClick={leave(onDone)}>Open sessions</button>
        </>
      )}
      {phase?.kind === 'expired' && (
        <Notice tone="danger" role="alert">The code expired or {label} refused it. Run <PairVia /> on {label} again, then tap Add again, or scan or paste the new link.</Notice>
      )}
      {phase?.kind === 'cancelled' && (
        <Notice tone="neutral" icon="x" role="alert">Adding {label} was cancelled. If {label} asks, answer N.</Notice>
      )}
      {phase?.kind === 'error' && <Notice tone="danger" role="alert" title={addFailure(phase.error, label)} />}
      {phase?.kind === 'stale' && (
        <Notice tone="danger" role="alert">{label} stopped waiting. On {label} run <PairVia home={firstLabel(homeOrigin())} />, then tap Add again.</Notice>
      )}
      {(phase?.kind === 'expired' || phase?.kind === 'cancelled' || phase?.kind === 'error' || phase?.kind === 'stale') && (
        <button type="button" className="secondary" onClick={leave(onSettings)}>Settings</button>
      )}
    </main>
  );
}

/**
 * The way out of the waiting page, to type y in a terminal of this app: back
 * to the session the add started from, else Sessions. The add keeps running.
 */
export function LeaveAdd({ back, onLeave }: { readonly back: string | undefined; readonly onLeave: (path: string) => void }) {
  const terminal = fromTerminal(back) && back !== undefined;
  return (
    <button type="button" className="secondary wide" onClick={() => onLeave(terminal ? back : '/')}>
      {terminal ? 'Back to terminal' : 'Sessions'}
    </button>
  );
}

/** The app origin a link was made for (its `via`), as a label; undefined when missing or not https. */
export function viaLabel(link: string | undefined): string | undefined {
  if (link === undefined) return undefined;
  const via = new URLSearchParams(link.slice(link.indexOf('#') + 1)).get('via');
  try {
    return via !== null && new URL(via).protocol === 'https:' ? firstLabel(via) : undefined;
  } catch {
    return undefined;
  }
}

/**
 * `/add-computer` on a peer, when its link is opened directly (the iPhone
 * Camera app opens a scanned QR code in Safari, another cookie jar). It
 * never redeems the grant: the QR code must be scanned, or the link pasted,
 * in the app on the computer it names. The link was read from the fragment
 * at start and is only copied, never sent.
 */
export function PeerAddPage({ link }: { readonly link: string | undefined }) {
  const [note, setNote] = useState<string>();
  const via = viaLabel(link);
  return (
    <main className="page screen setup">
      <p className="wordmark"><span className="dot accent" aria-hidden="true" />termivoa</p>
      <h1>Add this computer</h1>
      {link === undefined ? (
        <Notice tone="danger" role="alert">This link is incomplete. Run <PairVia /> on this computer again.</Notice>
      ) : (
        <>
          <p>This page cannot add the computer. Open your Termivoa app{via === undefined ? '' : ` on ${via}`}, then:</p>
          <p className="strong">Settings → + Add a computer → Scan QR</p>
          <p>Scan the same QR code there. Or copy this link and tap <strong>Paste link</strong> there.</p>
          <button
            type="button"
            className="primary wide"
            onClick={() => {
              navigator.clipboard.writeText(link).then(
                () => setNote('Link copied.'),
                () => setNote('Could not copy. Copy the link from the terminal instead.'),
              );
            }}
          >Copy link</button>
          <p role="status" className="muted field-note">{note ?? ''}</p>
          <p className="hint-text">The link works once and expires after two minutes.</p>
        </>
      )}
    </main>
  );
}
