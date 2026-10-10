import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { LinkSheet, Markdown, NotAvailable } from './Chat';

const noop = () => undefined;

describe('the Chat screen', () => {
  it('renders agent markdown as text: no raw HTML, no image, links as buttons that open nothing by themselves', () => {
    const html = renderToStaticMarkup(
      <Markdown text={'Done. <img src=x onerror=alert(1)> ![chart](https://example.com/c.png)\n\nSee [the docs](https://example.com/docs).'} onLink={noop} />,
    );
    expect(html).not.toContain('<img');
    expect(html).not.toContain('<a ');
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
    expect(html).toContain('chart');
    expect(html).toContain('<button type="button" class="chat-link" aria-haspopup="dialog" aria-label="Link: the docs, https://example.com/docs">the docs</button>');
  });

  it('asks before opening a link and shows its full address', () => {
    const html = renderToStaticMarkup(<LinkSheet url="https://example.com/a/very/long/path?q=1" onClose={noop} />);
    expect(html).toContain('role="dialog"');
    expect(html).toContain('Open this link?');
    expect(html).toContain('<p class="chat-link-url" dir="ltr">https://example.com/a/very/long/path?q=1</p>');
    expect(html).toContain('>Cancel</button>');
    expect(html).toContain('>Open</button>');
  });

  it('shows screen 4 with Show terminal and Why? for each state that has no chat', () => {
    for (const [link, reason] of [['off', undefined], ['unsupported', undefined], ['none', 'cleared'], ['none', 'busy']] as const) {
      const html = renderToStaticMarkup(<NotAvailable link={link} reason={reason} onShowTerminal={noop} onRetry={noop} />);
      expect(html).toContain('Chat isn’t available for this session');
      expect(html).toContain('<button type="button" class="primary">Show terminal</button>');
      expect(html).toContain('<button type="button" aria-haspopup="dialog">Why?</button>');
    }
    const failed = renderToStaticMarkup(<NotAvailable link="failed" reason={undefined} onShowTerminal={noop} onRetry={noop} />);
    expect(failed).toContain('>Try again</button>');
    expect(renderToStaticMarkup(<NotAvailable link="searching" reason={undefined} onShowTerminal={noop} onRetry={noop} />)).toContain('Looking for the conversation');
  });
});
