import { useEffect, useRef, useState } from 'react';
import { ApiError, deviceLabelProblem, firstLabel } from '../api/http';
import type { Computer } from '../computers';
import type { TailnetNode } from '../generated/protocol';
import { AddComputerFlow, addComputerUrl, prepareAddComputer, type AddComputerLink } from '../pairing/addComputer';
import type { PendingPairing } from '../pairing/pairing';
import { CodeTiles } from './Setup';
import { Sheet, SheetHeader } from './Sheet';

/**
 * Adding a computer (docs/multi-computer.md, "Pairing a second computer"):
 * a pasted or tapped link opens the confirm sheet, which never adds in one
 * tap; Add loads /computers/add (so home's CSP allows the new computer),
 * where the page redeems the link, shows the code and waits for approval.
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

/** The confirm step: full origin, machine name and owner or tags, then an explicit Add. */
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
      <p className="hint-text">
        Add it only if you just ran <code>pterm pair --via</code> on {label}. Next, this phone shows a code. Check it on {label}.
      </p>
      <div className="row">
        <button type="button" className="primary" onClick={onConfirm}>Add {label}</button>
        <button type="button" onClick={onCancel}>Cancel</button>
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
      (answer) => { if (live) setCheck({ kind: 'ready', node: answer.node }); },
      (error: unknown) => { if (live) setCheck({ kind: 'error', message: addFailure(error, 'your home computer') }); },
    );
    return () => { live = false; };
  }, [link]);
  return (
    <div className="sheet-backdrop">
      <Sheet labelledBy="add-computer-title" onClose={onClose}>
        <SheetHeader id="add-computer-title" title="Add this computer?">
          <button type="button" className="link" onClick={onClose}>Close</button>
        </SheetHeader>
        {check.kind === 'checking' && <p role="status" className="muted">Checking {label}…</p>}
        {check.kind === 'error' && <p role="alert" className="error">{check.message}</p>}
        {check.kind === 'ready' && (
          <AddComputerConfirm link={link} node={check.node} onConfirm={() => go(addComputerUrl(link))} onCancel={onClose} />
        )}
      </Sheet>
    </div>
  );
}

type Phase =
  | { readonly kind: 'redeeming' }
  | { readonly kind: 'waiting'; readonly pending: PendingPairing }
  | { readonly kind: 'done'; readonly computer: Computer }
  | { readonly kind: 'expired' | 'cancelled' }
  | { readonly kind: 'error'; readonly message: string };

/** The /computers/add page: redeem, show the code, wait for approval, confirm. */
export function AddComputerPage({ link, deviceLabel, onDone, onSettings }: {
  /** Undefined when the page was opened without a valid link (or reloaded: the link is used once). */
  readonly link: AddComputerLink | undefined;
  /** This phone's name on home, sent to the new computer. */
  readonly deviceLabel: string | undefined;
  readonly onDone: () => void;
  readonly onSettings: () => void;
}) {
  const [phase, setPhase] = useState<Phase>({ kind: 'redeeming' });
  const flow = useRef<AddComputerFlow>(undefined);
  const label = link === undefined ? '' : firstLabel(link.origin);
  const name = deviceLabel !== undefined && deviceLabelProblem(deviceLabel) === undefined ? deviceLabel : 'Phone';

  useEffect(() => {
    // Once per link, also when React runs effects twice in development.
    if (link === undefined || flow.current !== undefined) return;
    const next = new AddComputerFlow(link.origin, link.grant);
    flow.current = next;
    void (async () => {
      try {
        const pending = await next.redeem(name);
        setPhase({ kind: 'waiting', pending });
        const outcome = await next.finish(pending);
        setPhase(outcome.kind === 'confirmed' ? { kind: 'done', computer: outcome.computer } : { kind: outcome.kind });
      } catch (error) {
        setPhase({ kind: 'error', message: addFailure(error, label) });
      }
    })();
  }, [label, link, name]);

  const title = phase.kind === 'waiting' ? `Does ${label} show this code?`
    : phase.kind === 'done' ? `${phase.computer.label} is added` : 'Add a computer';

  return (
    <main className="page screen setup">
      <p className="wordmark"><span className="dot accent" aria-hidden="true" />termivoa</p>
      <h1>{title}</h1>
      {link === undefined && (
        <>
          <p role="alert" className="error">This add-computer link is used or incomplete. Paste the link again in Settings, under Computers.</p>
          <button type="button" className="primary" onClick={onSettings}>Settings</button>
        </>
      )}
      {link !== undefined && phase.kind === 'redeeming' && <p role="status" className="muted">Connecting to {label}…</p>}
      {phase.kind === 'waiting' && (
        <section className="stack pair-wait" aria-live="polite">
          <CodeTiles code={phase.pending.verificationCode} />
          <p className="muted">If it matches, type y in the terminal on {label}.</p>
          <div className="waiting">
            <p role="status"><span className="dots" aria-hidden="true"><span /><span /><span /></span>Waiting for {label}</p>
            <button type="button" className="link" onClick={() => { flow.current?.cancel(); setPhase({ kind: 'cancelled' }); }}>
              Codes don't match? Cancel
            </button>
          </div>
        </section>
      )}
      {phase.kind === 'done' && (
        <>
          <p className="muted">Its sessions now show in your list.</p>
          <button type="button" className="primary wide" onClick={onDone}>Open sessions</button>
        </>
      )}
      {phase.kind === 'expired' && (
        <p role="alert" className="error">The code expired or {label} refused it. Run <code>pterm pair --via</code> on {label} again and paste the new link.</p>
      )}
      {phase.kind === 'cancelled' && (
        <p role="alert" className="error">Adding {label} was cancelled. If {label} asks, answer N.</p>
      )}
      {phase.kind === 'error' && <p role="alert" className="error">{phase.message}</p>}
      {(phase.kind === 'expired' || phase.kind === 'cancelled' || phase.kind === 'error') && (
        <button type="button" onClick={onSettings}>Settings</button>
      )}
    </main>
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
 * camera opens it in Safari, another cookie jar). It never redeems the
 * grant: the link must be pasted in the app on the computer it names. The
 * link was read from the fragment at start and is only copied, never sent.
 */
export function PeerAddPage({ link }: { readonly link: string | undefined }) {
  const [note, setNote] = useState<string>();
  const via = viaLabel(link);
  return (
    <main className="page screen setup">
      <p className="wordmark"><span className="dot accent" aria-hidden="true" />termivoa</p>
      <h1>Add this computer</h1>
      {link === undefined ? (
        <p role="alert" className="error">This link is incomplete. Run <code>pterm pair --via</code> on this computer again.</p>
      ) : (
        <>
          <p>{via === undefined ? 'Open your Termivoa app and paste this link there.' : `Open your Termivoa app on ${via} and paste this link there.`}</p>
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
          <p className="hint-text">This page does not add the computer. The link works once and expires after two minutes.</p>
        </>
      )}
    </main>
  );
}
