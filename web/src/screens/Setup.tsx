import { useEffect, useRef, useState } from 'react';
import { ApiError, deviceLabelProblem } from '../api/http';
import { claimGrant, PairingFlow, type PendingPairing } from '../pairing/pairing';
import type { GrantHolder } from './App';

type Phase = 'none' | 'label' | 'redeeming' | 'waiting' | 'expired' | 'error' | 'cancelled';

function defaultLabel(): string {
  const agent = navigator.userAgent;
  if (/iPhone/u.test(agent)) return 'iPhone';
  if (/iPad/u.test(agent)) return 'iPad';
  if (/Android/u.test(agent)) return 'Android phone';
  return 'Browser';
}

/** A plain-language reason a pairing attempt failed. The UI adds "run pterm pair again". */
export function pairingFailure(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'network') return 'This phone cannot reach your computer.';
    if (error.status === 410 || error.status === 401 || error.code === 'pairing_expired') {
      return 'This pairing link expired or was already used.';
    }
    if (error.status === 429 || error.code === 'limit_exceeded') return 'Too many pairing attempts. Wait a minute first.';
  }
  return 'Pairing did not work.';
}

/** The verification code as digit tiles, grouped like the computer prints it ("485-431"). */
export function CodeTiles({ code }: { readonly code: string }) {
  const groups = code.split('-');
  return (
    <p className="code-tiles">
      <span className="visually-hidden">Verification code {code}</span>
      {groups.map((group, index) => (
        <span key={index} className="code-group" aria-hidden="true">
          {index > 0 && <span className="code-dash" />}
          {[...group].map((digit, at) => <span key={at} className="code-tile">{digit}</span>)}
        </span>
      ))}
    </p>
  );
}

function Step({ label, title }: { readonly label: string; readonly title: string }) {
  return (
    <div className="step-head">
      <p className="step-label">{label}</p>
      <h1 className="display">{title}</h1>
    </div>
  );
}

export function Setup({ grant, reason, onPaired }: {
  readonly grant: GrantHolder;
  readonly reason?: string;
  readonly onPaired: () => void;
}) {
  const [phase, setPhase] = useState<Phase>(grant.value === undefined ? 'none' : 'label');
  const [label, setLabel] = useState(defaultLabel);
  const [pending, setPending] = useState<PendingPairing>();
  const [error, setError] = useState<string>();
  const flow = useRef<PairingFlow | undefined>(undefined);

  useEffect(() => () => flow.current?.cancel(), []);

  const trimmed = label.trim();
  const labelProblem = deviceLabelProblem(trimmed);
  const labelValid = labelProblem === undefined;

  async function pair() {
    const claim = claimGrant(grant, labelValid);
    if ('problem' in claim) {
      if (claim.problem === 'missing') {
        // Never a silent tap: say the link must be made again.
        setPhase('none');
      }
      return;
    }
    const secret = claim.secret;
    const pairing = new PairingFlow();
    flow.current = pairing;
    setPhase('redeeming');
    try {
      const redeemed = await pairing.redeem(secret, trimmed);
      setPending(redeemed);
      setPhase('waiting');
      const outcome = await pairing.poll(redeemed);
      setPending(undefined);
      if (outcome === 'approved') {
        onPaired();
      } else if (outcome === 'expired') {
        setPhase('expired');
      }
    } catch (failure) {
      setPending(undefined);
      setError(pairingFailure(failure));
      setPhase('error');
    }
  }

  const title = phase === 'waiting' ? 'Does your computer show this code?'
    : phase === 'label' || phase === 'redeeming' ? 'Name this phone' : 'Pair this phone';

  return (
    <main className="page screen setup">
      <p className="wordmark"><span className="dot accent" aria-hidden="true" />termivoa</p>
      <Step label={phase === 'waiting' ? 'Step 2 of 2' : 'Step 1 of 2'} title={title} />
      {reason !== undefined && <p role="status" className="notice">{reason}</p>}
      {phase === 'none' && (
        <>
          {window.location.pathname === '/pair' ? (
            <p role="alert" className="error">
              This pairing link is incomplete or was already opened. Run <code>pterm pair</code> again on your
              computer and open the new link.
            </p>
          ) : <p className="muted">This phone is not paired with your computer yet.</p>}
          <ol className="steps">
            <li>On your computer, run <code>pterm pair</code>.</li>
            <li>Scan the QR code or open the link it shows on this phone.</li>
            <li>Check the code on both screens, then approve it on your computer.</li>
          </ol>
          <p className="hint-text">The link works once and expires after two minutes.</p>
        </>
      )}
      {phase === 'label' && (
        <form
          className="stack"
          onSubmit={(event) => {
            event.preventDefault();
            void pair();
          }}
        >
          <label htmlFor="device-label">Device name</label>
          <input
            id="device-label"
            value={label}
            maxLength={64}
            autoComplete="off"
            aria-describedby="device-label-help"
            aria-invalid={!labelValid}
            onChange={(event) => setLabel(event.target.value)}
          />
          <p id="device-label-help" className={labelValid ? 'hint-text' : 'error field-note'}>
            {labelProblem ?? 'Your computer shows this name in pterm devices. You can change it later in Settings.'}
          </p>
          <button type="submit" className="primary wide" disabled={!labelValid}>
            <span aria-hidden="true" className="prompt">❯</span> Pair this device
          </button>
        </form>
      )}
      {phase === 'redeeming' && <p role="status" className="muted">Checking the pairing link…</p>}
      {phase === 'waiting' && pending !== undefined && (
        <section className="stack pair-wait" aria-live="polite">
          <CodeTiles code={pending.verificationCode} />
          <p className="muted">If it matches, type y in the terminal on your computer:</p>
          <pre className="command prompt-preview"><code>
            <span className="muted">Phone “{trimmed}” shows code {pending.verificationCode}.</span>{'\n'}
            Approve? [y/N] <span className="accent strong">y</span><span className="cursor" aria-hidden="true" />
          </code></pre>
          <p className="hint-text">If your computer is not asking, run this on it:</p>
          <pre className="command"><code>pterm pair approve {pending.pendingId}</code></pre>
          <div className="waiting">
            <p role="status"><span className="dots" aria-hidden="true"><span /><span /><span /></span>Waiting for your computer</p>
            <button
              type="button"
              className="link"
              onClick={() => {
                flow.current?.cancel();
                setPending(undefined);
                setPhase('cancelled');
              }}
            >Codes don't match? Cancel</button>
          </div>
        </section>
      )}
      {phase === 'cancelled' && (
        <p role="alert" className="error">
          Pairing cancelled. This phone stays unpaired; if your computer asks, answer N. Run <code>pterm pair</code> again
          to start over.
        </p>
      )}
      {phase === 'expired' && (
        <p role="alert" className="error">
          This pairing request expired or was refused. Run <code>pterm pair</code> again on your computer.
        </p>
      )}
      {phase === 'error' && (
        <p role="alert" className="error">
          {error} Run <code>pterm pair</code> again on your computer.
        </p>
      )}
    </main>
  );
}
