import { useEffect, useRef, useState } from 'react';
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

const SAFE_LABEL = /^[^\p{Cc}\s](?:[^\p{Cc}]{0,62}[^\p{Cc}\s])?$/u;

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
  const labelValid = SAFE_LABEL.test(trimmed) && new TextEncoder().encode(trimmed).byteLength <= 64;

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
      setError(failure instanceof Error ? failure.message : 'Pairing failed');
      setPhase('error');
    }
  }

  return (
    <main className="page">
      <h1>Pair this device</h1>
      {reason !== undefined && <p role="status" className="notice">{reason}</p>}
      {phase === 'none' && (
        <>
          <p>This browser is not paired with your computer.</p>
          <ol className="steps">
            <li>On your computer, run <code>pterm pair</code>.</li>
            <li>Scan the QR code or open the link it shows on this phone.</li>
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
            onChange={(event) => setLabel(event.target.value)}
          />
          {!labelValid && <p className="error">Use 1–64 characters without control characters.</p>}
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
          This pairing request expired or was refused. Run <code>pterm pair</code> on your computer again.
        </p>
      )}
      {phase === 'error' && (
        <p role="alert" className="error">
          {error} Run <code>pterm pair</code> on your computer to get a new link.
        </p>
      )}
    </main>
  );
}
