import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Banner, Button, Icon, Notice } from './ui';

describe('Button', () => {
  it('is a plain button when idle', () => {
    expect(renderToStaticMarkup(<Button className="primary">Save</Button>)).toBe('<button type="button" class="primary">Save</button>');
  });

  it('busy: off, aria-busy, a spinner in place of the icon, and both labels in one cell so the width stays', () => {
    const idle = renderToStaticMarkup(<Button className="primary wide" icon={<Icon name="plus" />} busyLabel="Starting…">New</Button>);
    expect(idle).not.toContain('aria-busy');
    expect(idle).not.toContain('disabled');
    expect(idle).toContain('icon-plus');
    expect(idle).toContain('<span class="btn-swap"><span>New</span><span class="off" aria-hidden="true">Starting…</span></span>');

    const busy = renderToStaticMarkup(<Button className="primary wide" icon={<Icon name="plus" />} busy busyLabel="Starting…">New</Button>);
    expect(busy).toMatch(/^<button type="button" class="primary wide" disabled="" aria-busy="true"><svg class="icon icon-spinner"/u);
    expect(busy).not.toContain('icon-plus');
    // The busy label is the one read out; the idle one only keeps the width.
    expect(busy).toContain('<span class="btn-swap"><span class="off" aria-hidden="true">New</span><span>Starting…</span></span>');
  });

  it('busy without an icon keeps its label for screen readers under the spinner', () => {
    const html = renderToStaticMarkup(<Button className="danger" busy>Log out</Button>);
    expect(html).toMatch(/^<button type="button" class="danger busy-over" disabled="" aria-busy="true">Log out<svg class="icon icon-spinner"[^>]*aria-hidden="true"/u);
  });
});

describe('Notice', () => {
  it('shows an icon, the title, the body and one action, in its tone', () => {
    const html = renderToStaticMarkup(
      <Notice tone="warning" icon="wifi-off" role="alert" title="vps · can't reach" action={<button type="button">Retry</button>}>last seen 3 min ago</Notice>,
    );
    expect(html).toMatch(/^<div class="notice warning" role="alert"><span class="notice-icon"><svg class="icon icon-wifi-off"[^>]*aria-hidden="true"/u);
    expect(html).toContain('<p class="notice-title">vps · can&#x27;t reach</p><div class="notice-body">last seen 3 min ago</div><div class="notice-action"><button type="button">Retry</button></div>');
  });

  it('leaves out what it is not given, and picks an icon for the tone', () => {
    const html = renderToStaticMarkup(<Notice tone="working" title="Checking vps…" />);
    expect(html).toContain('icon-spinner');
    expect(html).not.toContain('role=');
    expect(html).not.toContain('notice-body');
    expect(html).not.toContain('notice-action');
    expect(renderToStaticMarkup(<Notice tone="danger">Run it again.</Notice>)).toContain('<div class="notice-text"><div class="notice-body">Run it again.</div></div>');
  });
});

describe('Banner', () => {
  it('says the state with a mark in its tone, as a status', () => {
    const done = renderToStaticMarkup(<Banner tone="success" text="vps is added"><button type="button">Dismiss</button></Banner>);
    expect(done).toMatch(/^<div class="banner success" role="status"><span class="banner-mark"><svg class="icon icon-check"/u);
    expect(done).toContain('<span class="banner-text">vps is added</span><span class="banner-actions"><button type="button">Dismiss</button></span>');
    expect(renderToStaticMarkup(<Banner tone="working" text="Adding vps…" />)).toContain('icon-spinner');
  });
});
