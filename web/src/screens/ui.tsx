import type { ButtonHTMLAttributes, ReactNode } from 'react';

/** Inline icons (24 px grid, stroked in currentColor). Always decorative: the text next to them says it. */
const PATHS = {
  refresh: <path d="M21 12a9 9 0 1 1-2.6-6.4M21 4v5h-5" />,
  spinner: <><circle className="icon-track" cx="12" cy="12" r="8" /><path d="M12 4a8 8 0 0 1 8 8" /></>,
} as const;

type IconName = keyof typeof PATHS;

export function Icon({ name, size = 18 }: { readonly name: IconName; readonly size?: number }) {
  return (
    <svg className={`icon icon-${name}`} width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {PATHS[name]}
    </svg>
  );
}

/**
 * A button that can be busy: the spinner takes the icon's place, and with a
 * `busyLabel` both labels share one cell, so the width never jumps (without an
 * icon the spinner sits in the busy label's cell). Without an icon or a
 * `busyLabel` the spinner sits over the label, which stays for screen readers.
 * Busy is also disabled (one tap, one action).
 */
export function Button({ busy = false, busyLabel, icon, className, disabled, type = 'button', children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & {
  readonly busy?: boolean;
  readonly busyLabel?: ReactNode;
  readonly icon?: ReactNode;
}) {
  const overlay = busy && icon === undefined && busyLabel === undefined;
  const classes = [className, overlay ? 'busy-over' : undefined].filter((name) => name !== undefined).join(' ');
  const label = busyLabel === undefined ? children : (
    <span className="btn-swap">
      <span {...(busy ? { className: 'off', 'aria-hidden': true } : {})}>{children}</span>
      <span {...(busy ? {} : { className: 'off', 'aria-hidden': true })}>{icon === undefined && <Icon name="spinner" />}{busyLabel}</span>
    </span>
  );
  return (
    <button type={type} {...(classes === '' ? {} : { className: classes })} disabled={disabled === true || busy} {...(busy ? { 'aria-busy': true } : {})} {...rest}>
      {busy && icon !== undefined ? <Icon name="spinner" /> : icon}
      {label}
      {overlay && <Icon name="spinner" />}
    </button>
  );
}

/** How worried to be: cyan working, green done, amber might fix itself, red needs you, grey good to know. */
export type Tone = 'working' | 'success' | 'warning' | 'danger' | 'neutral';

/**
 * `pterm pair --via <home>` as code. It wraps at its spaces like any text;
 * "--via" never breaks between its two dashes (Chrome would).
 */
export function PairVia({ home, className }: { readonly home?: string; readonly className?: string }) {
  return <code {...(className === undefined ? {} : { className })}>pterm pair <span className="nowrap">--via</span>{home === undefined ? '' : ` ${home}`}</code>;
}

/**
 * An inline notice: a rule in the tone's colour, a bold title (what happened),
 * a line (how to fix it) and at most one action. Title or body may be left out
 * when the app has only one sentence to say.
 */
export function Notice({ tone, title, children, action, role, id }: {
  readonly tone: Tone;
  readonly title?: ReactNode;
  readonly children?: ReactNode;
  readonly action?: ReactNode;
  readonly role?: 'alert' | 'status';
  readonly id?: string;
}) {
  return (
    <div className={`notice ${tone}`} {...(role === undefined ? {} : { role })} {...(id === undefined ? {} : { id })}>
      {title !== undefined && <p className="notice-title">{title}</p>}
      {children !== undefined && <div className="notice-body">{children}</div>}
      {action !== undefined && <div className="notice-action">{action}</div>}
    </div>
  );
}

/** A banner over the app for something in progress (working), finished (success) or failed (danger). */
export function Banner({ tone, text, children }: {
  readonly tone: 'working' | 'success' | 'danger';
  readonly text: string;
  readonly children?: ReactNode;
}) {
  return (
    <div className={`banner ${tone}`} role="status">
      {tone === 'working' ? <Icon name="spinner" /> : <span className="banner-dot" aria-hidden="true" />}
      <span className="banner-text">{text}</span>
      {children !== undefined && <span className="banner-actions">{children}</span>}
    </div>
  );
}
