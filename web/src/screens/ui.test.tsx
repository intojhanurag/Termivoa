import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Banner, Button, CONFIRM_AFTER_MS, ConfirmPair, confirmReady, Icon, Notice } from './ui';

describe('Button', () => {
  it('is a plain button when idle', () => {
    expect(renderToStaticMarkup(<Button className="primary">Save</Button>)).toBe('<button type="button" class="primary">Save</button>');
  });

  it('busy: off, aria-busy, a spinner in place of the icon, and both labels in one cell so the width stays', () => {
    const idle = renderToStaticMarkup(<Button className="primary wide" icon={<Icon name="refresh" />} busyLabel="Starting…">New</Button>);
    expect(idle).not.toContain('aria-busy');
    expect(idle).not.toContain('disabled');
    expect(idle).toContain('icon-refresh');
    expect(idle).toContain('<span class="btn-swap"><span>New</span><span class="off" aria-hidden="true">Starting…</span></span>');

    const busy = renderToStaticMarkup(<Button className="primary wide" icon={<Icon name="refresh" />} busy busyLabel="Starting…">New</Button>);
    expect(busy).toMatch(/^<button type="button" class="primary wide" disabled="" aria-busy="true"><svg class="icon icon-spinner"/u);
    expect(busy).not.toContain('icon-refresh');
    // The busy label is the one read out; the idle one only keeps the width.
    expect(busy).toContain('<span class="btn-swap"><span class="off" aria-hidden="true">New</span><span>Starting…</span></span>');
  });

  it('busy without an icon but with a busy label puts the spinner in that label\'s cell, so the width stays', () => {
    const idle = renderToStaticMarkup(<Button className="secondary" busyLabel="Checking…">Check again</Button>);
    expect(idle).toMatch(/^<button type="button" class="secondary"><span class="btn-swap"><span>Check again<\/span><span class="off" aria-hidden="true"><svg class="icon icon-spinner"/u);
    const busy = renderToStaticMarkup(<Button className="secondary" busy busyLabel="Checking…">Check again</Button>);
    expect(busy).toMatch(/^<button type="button" class="secondary" disabled="" aria-busy="true"><span class="btn-swap"><span class="off" aria-hidden="true">Check again<\/span><span><svg class="icon icon-spinner"[^]*?<\/svg>Checking…<\/span><\/span><\/button>$/u);
  });

  it('busy without an icon keeps its label for screen readers under the spinner', () => {
    const html = renderToStaticMarkup(<Button className="danger" busy>Log out</Button>);
    expect(html).toMatch(/^<button type="button" class="danger busy-over" disabled="" aria-busy="true">Log out<svg class="icon icon-spinner"[^>]*aria-hidden="true"/u);
  });
});

describe('Notice', () => {
  it('shows the title, the body and one action, in its tone', () => {
    const html = renderToStaticMarkup(
      <Notice tone="warning" role="alert" title="vps · can't reach" action={<button type="button">Retry</button>}>last seen 3 min ago</Notice>,
    );
    expect(html).toBe('<div class="notice warning" role="alert"><p class="notice-title">vps · can&#x27;t reach</p><div class="notice-body">last seen 3 min ago</div><div class="notice-action"><button type="button">Retry</button></div></div>');
  });

  it('leaves out what it is not given', () => {
    const html = renderToStaticMarkup(<Notice tone="working" title="Checking vps…" />);
    expect(html).not.toContain('<svg');
    expect(html).not.toContain('role=');
    expect(html).not.toContain('notice-body');
    expect(html).not.toContain('notice-action');
    expect(renderToStaticMarkup(<Notice tone="danger">Run it again.</Notice>)).toBe('<div class="notice danger"><div class="notice-body">Run it again.</div></div>');
  });
});

describe('ConfirmPair', () => {
  it('takes no confirm tap for 400 ms after it shows (a double tap on the menu)', () => {
    expect(CONFIRM_AFTER_MS).toBe(400);
    expect(confirmReady(1_000, 1_000)).toBe(false);
    expect(confirmReady(1_000, 1_399)).toBe(false);
    expect(confirmReady(1_000, 1_400)).toBe(true);
  });

  it('puts the way out first and the danger button second', () => {
    expect(renderToStaticMarkup(<ConfirmPair cancel="Keep running" confirm="Terminate session" onCancel={() => undefined} onConfirm={() => undefined} />))
      .toBe('<div class="row confirm-pair"><button type="button">Keep running</button><button type="button" class="danger">Terminate session</button></div>');
  });
});

describe('Banner', () => {
  it('says the state with a dot (a spinner while it works), as a status', () => {
    const done = renderToStaticMarkup(<Banner tone="success" text="vps is added"><button type="button">Dismiss</button></Banner>);
    expect(done).toMatch(/^<div class="banner success" role="status"><span class="banner-dot" aria-hidden="true"><\/span>/u);
    expect(done).toContain('<span class="banner-text">vps is added</span><span class="banner-actions"><button type="button">Dismiss</button></span>');
    expect(renderToStaticMarkup(<Banner tone="working" text="Adding vps…" />)).toContain('icon-spinner');
  });
});
