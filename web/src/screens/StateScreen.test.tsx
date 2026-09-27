import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { OfflineScreen } from './StateScreen';

describe('offline screen', () => {
  it('shows the countdown and what to check', () => {
    const html = renderToStaticMarkup(<OfflineScreen phoneOnline retryIn={7} onRetry={() => undefined} />);
    expect(html).toMatch(/retrying · (<!-- -->)?7(<!-- -->)?s/u);
    expect(html).toContain('phone is online');
    expect(html).toContain('tailscale on, both devices');
    expect(html).toContain('>Retry now</button>');
  });

  it('says so when the phone itself has no network', () => {
    const html = renderToStaticMarkup(<OfflineScreen phoneOnline={false} retryIn={10} onRetry={() => undefined} />);
    expect(html).toContain('phone is offline');
    expect(html).not.toContain('phone is online');
  });
});
