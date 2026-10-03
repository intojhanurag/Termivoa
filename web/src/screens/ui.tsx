import type { ButtonHTMLAttributes, ReactNode } from 'react';

/** Inline icons (24 px grid, stroked in currentColor). Always decorative: the text next to them says it. */
const PATHS = {
  plus: <path d="M12 5v14M5 12h14" />,
  refresh: <path d="M21 12a9 9 0 1 1-2.6-6.4M21 4v5h-5" />,
  chevron: <path d="M9 6l6 6-6 6" />,
  check: <path d="M5 12.5l4.5 4.5L19 7.5" />,
  x: <path d="M7 7l10 10M17 7L7 17" />,
  download: <path d="M12 4v11M7 10l5 5 5-5M5 20h14" />,
  'wifi-off': <path d="M2 8.8a15 15 0 0 1 20 0M5.5 12.5a10 10 0 0 1 13 0M9 16.2a5 5 0 0 1 6 0M12 20h.01M3 3l18 18" />,
  lock: <><rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 0 1 7.5-2" /></>,
  monitor: <><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></>,
  server: <><rect x="3" y="4" width="18" height="7" rx="1.6" /><rect x="3" y="13" width="18" height="7" rx="1.6" /><path d="M7 7.5h.01M7 16.5h.01" /></>,
  spinner: <><circle className="icon-track" cx="12" cy="12" r="8" /><path d="M12 4a8 8 0 0 1 8 8" /></>,
} as const;

export type IconName = keyof typeof PATHS;

export function Icon({ name, size = 18 }: { readonly name: IconName; readonly size?: number }) {
  return (
    <svg className={`icon icon-${name}`} width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {PATHS[name]}
    </svg>
  );
}

/**
 * A button that can be busy: the spinner takes the icon's place, and with a
 * `busyLabel` both labels share one cell, so the width never jumps. Without an
 * icon the spinner sits over the label, which stays for screen readers.
 * Busy is also disabled (one tap, one action) but keeps its colour.
 */
export function Button({ busy = false, busyLabel, icon, className, disabled, type = 'button', children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & {
  readonly busy?: boolean;
  readonly busyLabel?: ReactNode;
  readonly icon?: ReactNode;
}) {
  const overlay = busy && icon === undefined;
  const classes = [className, overlay ? 'busy-over' : undefined].filter((name) => name !== undefined).join(' ');
  const label = busyLabel === undefined ? children : (
    <span className="btn-swap">
      <span {...(busy ? { className: 'off', 'aria-hidden': true } : {})}>{children}</span>
      <span {...(busy ? {} : { className: 'off', 'aria-hidden': true })}>{busyLabel}</span>
    </span>
  );
  return (
    <button type={type} {...(classes === '' ? {} : { className: classes })} disabled={disabled === true || busy} {...(busy ? { 'aria-busy': true } : {})} {...rest}>
      {busy && !overlay ? <Icon name="spinner" /> : icon}
      {label}
      {overlay && <Icon name="spinner" />}
    </button>
  );
}

/** How worried to be: cyan working, green done, amber might fix itself, red needs you, grey good to know. */
export type Tone = 'working' | 'success' | 'warning' | 'danger' | 'neutral';

const TONE_ICON: Readonly<Record<Tone, IconName>> = {
  working: 'spinner', success: 'check', warning: 'wifi-off', danger: 'x', neutral: 'download',
};

/**
 * An inline notice: an icon in a tinted square, a bold title (what happened),
 * a line (how to fix it) and at most one action. Title or body may be left out
 * when the app has only one sentence to say.
 */
export function Notice({ tone, icon, title, children, action, role, id }: {
  readonly tone: Tone;
  readonly icon?: IconName;
  readonly title?: ReactNode;
  readonly children?: ReactNode;
  readonly action?: ReactNode;
  readonly role?: 'alert' | 'status';
  readonly id?: string;
}) {
  return (
    <div className={`notice ${tone}`} {...(role === undefined ? {} : { role })} {...(id === undefined ? {} : { id })}>
      <span className="notice-icon"><Icon name={icon ?? TONE_ICON[tone]} /></span>
      <div className="notice-text">
        {title !== undefined && <p className="notice-title">{title}</p>}
        {children !== undefined && <div className="notice-body">{children}</div>}
        {action !== undefined && <div className="notice-action">{action}</div>}
      </div>
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
      {tone === 'working' ? <Icon name="spinner" /> : <span className="banner-mark"><Icon name={tone === 'success' ? 'check' : 'x'} size={13} /></span>}
      <span className="banner-text">{text}</span>
      {children !== undefined && <span className="banner-actions">{children}</span>}
    </div>
  );
}
