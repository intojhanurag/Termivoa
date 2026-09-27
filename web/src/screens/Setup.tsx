import { useEffect, useRef, useState } from 'react';
import { ApiError, deviceLabelProblem } from '../api/http';
import { PairingFlow, type PendingPairing } from '../pairing/pairing';
import type { GrantHolder } from './App';

type Phase = 'none' | 'label' | 'redeeming' | 'waiting' | 'expired' | 'error';

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
    const secret = grant.value;
    grant.value = undefined;
    if (secret === undefined || !labelValid) {
      return;
    }
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

  return (
    <main className="page">
      <h1>Pair this device</h1>
      {reason !== undefined && <p role="status" className="notice">{reason}</p>}
      {phase === 'none' && (
        <>
          {window.location.pathname === '/pair' ? (
            <p role="alert" className="error">
              This pairing link is incomplete or was already opened. Run <code>pterm pair</code> again on your
              computer and open the new link.
            </p>
          ) : <p>This phone is not paired with your computer yet.</p>}
          <ol className="steps">
            <li>On your computer, run <code>pterm pair</code>.</li>
            <li>Scan the QR code or open the link it shows on this phone.</li>
            <li>Check the code on both screens, then approve it on your computer.</li>
          </ol>
          <p className="muted">The link works once and expires after two minutes.</p>
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
          <p id="device-label-help" className={labelValid ? 'muted field-note' : 'error field-note'}>
            {labelProblem ?? 'Your computer shows this name in pterm devices. You can change it later in Settings.'}
          </p>
          <button type="submit" className="primary" disabled={!labelValid}>Pair this device</button>
        </form>
      )}
      {phase === 'redeeming' && <p role="status">Checking the pairing link…</p>}
      {phase === 'waiting' && pending !== undefined && (
        <section className="stack" aria-live="polite">
          <p>Verification code</p>
          <p className="code" aria-label={`Verification code ${pending.verificationCode}`}>{pending.verificationCode}</p>
          <p>Check that your computer shows the same code. Then run this on your computer:</p>
          <pre className="command"><code>pterm pair approve {pending.pendingId}</code></pre>
          <p role="status" className="muted">Waiting for approval…</p>
        </section>
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
