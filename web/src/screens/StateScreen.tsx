import type { ReactNode } from 'react';

type Tone = 'accent' | 'amber' | 'red';

/**
 * The full-screen layout for states (offline, error, update, lost): a mono
 * status label, a drawing, a short headline and lead, optional detail, and
 * actions pinned to the bottom.
 */
export function StateScreen({ tone, label, art, title, lead, children, actions }: {
  readonly tone: Tone;
  readonly label: string;
  readonly art: ReactNode;
  readonly title: ReactNode;
  readonly lead: string;
  readonly children?: ReactNode;
  readonly actions: ReactNode;
}) {
  return (
    <main className="page state-screen">
      <p className="state-label"><span className={`dot ${tone}`} aria-hidden="true" />{label}</p>
      <div className="state-art" aria-hidden="true">{art}</div>
      <section role="alert" className="state-copy">
        <h1 className="display">{title}</h1>
        <p className="state-lead">{lead}</p>
      </section>
      {children}
      <div className="state-actions">{actions}</div>
    </main>
  );
}

/** Phone and computer with the link between them broken. */
export function LinkArt() {
  return (
    <div className="link-art">
      <svg width="300" height="96" viewBox="0 0 300 96" fill="none">
        <rect x="18" y="14" width="42" height="70" rx="9" stroke="var(--text)" strokeWidth="2.5" />
        <rect x="32" y="20" width="14" height="3" rx="1.5" fill="var(--text)" />
        <rect x="224" y="20" width="62" height="42" rx="5" stroke="var(--text)" strokeWidth="2.5" />
        <path d="M212 70h86" stroke="var(--text)" strokeWidth="2.5" strokeLinecap="round" />
        <path d="M76 49h46" stroke="var(--accent)" strokeWidth="2.5" strokeLinecap="round" strokeDasharray="2 8" />
        <path d="M178 49h32" stroke="var(--line)" strokeWidth="2.5" strokeLinecap="round" strokeDasharray="2 8" />
        <circle cx="150" cy="49" r="17" fill="color-mix(in srgb, var(--amber) 14%, transparent)" stroke="var(--amber)" strokeWidth="2" />
        <path d="M144 43l12 12M156 43l-12 12" stroke="var(--amber)" strokeWidth="2.5" strokeLinecap="round" />
      </svg>
      <div className="link-art-labels"><span>PHONE</span><span>YOUR COMPUTER</span></div>
    </div>
  );
}

/** A terminal window whose right side shows what happened. */
export function WindowArt({ mark }: { readonly mark: 'crash' | 'update' | 'gone' }) {
  return (
    <svg width="168" height="112" viewBox="0 0 168 112" fill="none">
      <rect x="2" y="2" width="164" height="108" rx="12" stroke="var(--text)" strokeWidth="2.5" />
      <path d="M2 28h164" stroke="var(--line)" strokeWidth="2" />
      <circle cx="18" cy="15" r="3.5" fill={mark === 'update' ? 'var(--accent)' : 'var(--red)'} />
      <circle cx="30" cy="15" r="3.5" fill="var(--line)" />
      <circle cx="42" cy="15" r="3.5" fill="var(--line)" />
      <path d="M22 52l12 10-12 10" stroke="var(--accent)" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M44 72h28" stroke="var(--muted)" strokeWidth="2.5" strokeLinecap="round" />
      {mark === 'crash' && <path d="M112 50l24 24M136 50l-24 24" stroke="var(--red)" strokeWidth="3" strokeLinecap="round" />}
      {mark === 'update' && (
        <path d="M137 62a13 13 0 1 1-3.8-9.2M137 46v7.5h-7.5" stroke="var(--accent)" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      )}
      {mark === 'gone' && <path d="M110 62h28" stroke="var(--red)" strokeWidth="3" strokeLinecap="round" />}
    </svg>
  );
}

/** Offline: what to check, and the countdown to the next automatic try. */
export function OfflineScreen({ phoneOnline, retryIn, onRetry }: {
  readonly phoneOnline: boolean;
  readonly retryIn: number;
  readonly onRetry: () => void;
}) {
  return (
    <StateScreen
      tone="amber"
      label="Termivoa · offline"
      art={<LinkArt />}
      title={<>Can't reach<br />your computer</>}
      lead="Your sessions are safe. They keep running."
      actions={(
        <>
          <p className="state-pill" role="status"><span className="spinner" aria-hidden="true" />retrying · {retryIn}s</p>
          <button type="button" className="primary" onClick={onRetry}>Retry now</button>
        </>
      )}
    >
      <ul className="check-log" aria-label="What to check">
        {phoneOnline
          ? <li><span className="mark green">✓</span>phone is online</li>
          : <li><span className="mark red">✕</span>phone is offline</li>}
        <li><span className="mark amber">?</span>tailscale on, both devices</li>
        <li><span className="mark amber">?</span>computer awake, not asleep</li>
      </ul>
    </StateScreen>
  );
}
