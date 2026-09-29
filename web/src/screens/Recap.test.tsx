import type { ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { autoDismiss, RECAP_MS, RecapCard } from './Recap';

const line = 'Since you left 32 min ago · worked 14 min · 5 files changed +120 −34 · waiting for you';

describe('recap card', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it('shows the line as a status with See changes and a labelled dismiss', () => {
    const markup = renderToStaticMarkup(<RecapCard line={line} onSeeChanges={() => undefined} onDismiss={() => undefined} />);
    expect(markup).toContain('<section class="recap-card" aria-label="While you were away">');
    expect(markup).toContain(`<p class="recap-line" role="status">${line}</p>`);
    expect(markup).toContain('<button type="button" class="link strong">See changes</button>');
    expect(markup).toContain('<button type="button" class="link recap-close" aria-label="Dismiss">✕</button>');
  });

  it('wires See changes and ✕', () => {
    const onSeeChanges = vi.fn();
    const onDismiss = vi.fn();
    const card = RecapCard({ line, onSeeChanges, onDismiss });
    type Parent = ReactElement<{ children: Array<ReactElement<{ children: unknown; onClick?: () => void }>> }>;
    const actions = (card as Parent).props.children[1] as Parent;
    for (const button of actions.props.children) button.props.onClick?.();
    expect(onSeeChanges).toHaveBeenCalledOnce();
    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it('dismisses itself after 12 s', () => {
    const onDismiss = vi.fn();
    autoDismiss(new EventTarget(), () => false, onDismiss);
    vi.advanceTimersByTime(RECAP_MS - 1);
    expect(onDismiss).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it('dismisses on the first tap elsewhere, not on a tap on the card, and stops after cleanup', () => {
    const page = new EventTarget();
    const onDismiss = vi.fn();
    let inside = true;
    const stop = autoDismiss(page, () => inside, onDismiss);
    page.dispatchEvent(new Event('pointerdown'));
    expect(onDismiss).not.toHaveBeenCalled();
    inside = false;
    page.dispatchEvent(new Event('pointerdown'));
    expect(onDismiss).toHaveBeenCalledOnce();

    stop();
    page.dispatchEvent(new Event('pointerdown'));
    vi.advanceTimersByTime(RECAP_MS);
    expect(onDismiss).toHaveBeenCalledOnce();
  });
});
