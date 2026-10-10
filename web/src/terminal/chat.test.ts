import { readFileSync } from 'node:fs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatBlock, ChatPagePayload } from '../generated/protocol';
import { CHAT_RETRY_MS, CHAT_TIMEOUT_MS, ChatStore, MAX_CHAT_BLOCKS, upsert } from './chat';

const H1 = '0123456789abcdef';
const H2 = 'fedcba9876543210';
const cursor = (n: number) => `c${String(n).padStart(21, '0')}`;
const block = (id: string, text = id): ChatBlock => ({ id, kind: 'assistant_text', text });
const blocks = (from: number, count: number) => Array.from({ length: count }, (_, index) => block(`b${from + index}`));

function harness() {
  let next = 0;
  const sent: Array<{ type: string; payload: Record<string, unknown>; id: string }> = [];
  let online = true;
  const store = new ChatStore({
    send: (type, payload) => {
      if (!online) return undefined;
      const id = `r${++next}`;
      sent.push({ type, payload: payload as Record<string, unknown>, id });
      return id;
    },
    emit: () => undefined,
    later: (run, ms) => {
      const timer = setTimeout(run, ms);
      return () => clearTimeout(timer);
    },
  });
  const last = () => sent[sent.length - 1];
  const page = (fields: Partial<ChatPagePayload> & { blocks: ChatBlock[] }, request = last()) => {
    store.onPage({ historyId: H1, after: cursor(99), complete: true, ...fields }, request?.id ?? '');
  };
  /** Opens and answers with a linked state and one page. */
  const opened = (first: ChatBlock[] = blocks(0, 3), before: string | undefined = cursor(1)) => {
    store.connected();
    store.open();
    const request = last();
    store.onState({ link: 'linked', agent: 'claude', historyId: H1 }, request?.id ?? '');
    page({ blocks: first, ...(before === undefined ? {} : { before }), after: cursor(2) }, request);
  };
  return { store, sent, last, page, opened, setOnline: (value: boolean) => { online = value; } };
}

const ids = (store: ChatStore) => store.get().blocks.map((item) => item.id);

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('chat store: the pull flow', () => {
  it('opens only once live, shows the latest page, and asks for nothing until Chat is on screen', () => {
    const { store, sent, opened } = harness();
    store.connected();
    expect(sent).toEqual([]);
    expect(store.get().link).toBe('closed');
    opened();
    expect(sent.map((item) => item.type)).toEqual(['chat.open']);
    expect(sent[0]?.payload).toEqual({ limit: 30 });
    expect(store.get()).toMatchObject({ link: 'linked', agent: 'claude', earlier: true });
    expect(ids(store)).toEqual(['b0', 'b1', 'b2']);
  });

  it('pulls after chat.changed with one request in flight, and once more for what changed meanwhile', () => {
    const { store, sent, page, opened } = harness();
    opened();
    store.onChanged(H1);
    store.onChanged(H1);
    expect(sent.map((item) => item.type)).toEqual(['chat.open', 'chat.read']);
    expect(sent[1]?.payload).toEqual({ after: cursor(2), limit: 30 });
    page({ blocks: blocks(3, 1), after: cursor(3) });
    // The second notice came while the first read ran: one more read, from the new cursor.
    expect(sent.map((item) => item.type)).toEqual(['chat.open', 'chat.read', 'chat.read']);
    expect(sent[2]?.payload).toEqual({ after: cursor(3), limit: 30 });
    page({ blocks: [], after: cursor(3) });
    expect(sent).toHaveLength(3);
    expect(ids(store)).toEqual(['b0', 'b1', 'b2', 'b3']);
  });

  it('asks again at once when a page is not complete', () => {
    const { store, sent, page, opened } = harness();
    opened();
    store.onChanged(H1);
    page({ blocks: blocks(3, 2), after: cursor(4), complete: false });
    expect(sent.at(-1)?.payload).toEqual({ after: cursor(4), limit: 30 });
  });

  it('replaces a block it holds by id (a tool result updates its call) and never shows one twice', () => {
    const { store, page, opened } = harness();
    opened();
    store.onChanged(H1);
    page({ blocks: [block('b1', 'updated'), block('b3'), block('b3', 'last copy')], after: cursor(3) });
    expect(ids(store)).toEqual(['b0', 'b1', 'b2', 'b3']);
    expect(store.get().blocks[1]?.text).toBe('updated');
    expect(store.get().blocks[3]?.text).toBe('last copy');
    expect(upsert([block('a')], [block('z'), block('a', 'new')], 'start').map((item) => `${item.id}:${item.text}`)).toEqual(['z:z', 'a:new']);
  });

  it('ignores a stale page and one for a request it did not send', () => {
    const { store, page, opened } = harness();
    opened();
    page({ blocks: blocks(10, 1) }, { type: 'chat.read', payload: {}, id: 'other' });
    expect(ids(store)).toEqual(['b0', 'b1', 'b2']);
  });
});

describe('chat store: reset, stale and reconnect', () => {
  it('drops everything and opens again on a new historyId', () => {
    const { store, sent, opened } = harness();
    opened();
    store.onChanged(H2);
    expect(ids(store)).toEqual([]);
    expect(sent.at(-1)).toMatchObject({ type: 'chat.open', payload: { limit: 30 } });
  });

  it('drops the blocks when the state names another history or is no longer linked', () => {
    const { store, sent, opened } = harness();
    opened();
    store.onState({ link: 'linked', agent: 'claude', historyId: H2 }, 'pushed');
    expect(ids(store)).toEqual([]);
    expect(sent.at(-1)?.type).toBe('chat.open');
    const second = harness();
    second.opened();
    second.store.onState({ link: 'off' }, 'pushed');
    expect(second.store.get()).toMatchObject({ link: 'off', blocks: [] });
    second.store.onState({ link: 'none', reason: 'cleared' }, 'pushed');
    expect(second.store.get()).toMatchObject({ link: 'none', reason: 'cleared' });
  });

  it('pages again from the last cursor when the host says stale', () => {
    const { store, sent, opened } = harness();
    opened();
    store.onState({ link: 'linked', agent: 'claude', historyId: H1, stale: true }, 'pushed');
    expect(sent.at(-1)).toMatchObject({ type: 'chat.read', payload: { after: cursor(2) } });
    expect(ids(store)).toEqual(['b0', 'b1', 'b2']);
  });

  it('opens again after a reconnect for a fresh latest page, and replays nothing', () => {
    const { store, sent, page, opened, setOnline } = harness();
    opened();
    store.onChanged(H1);
    const lost = sent.at(-1);
    setOnline(false);
    store.disconnected();
    // A late reply of the old socket counts for nothing.
    page({ blocks: blocks(50, 1) }, lost);
    setOnline(true);
    store.connected();
    expect(sent.map((item) => item.type)).toEqual(['chat.open', 'chat.read', 'chat.open']);
    page({ blocks: blocks(1, 2), before: cursor(7), after: cursor(8) });
    expect(ids(store)).toEqual(['b1', 'b2']);
  });

  it('opens again on history_changed and retries a refused read after a moment', () => {
    const { store, sent, opened } = harness();
    opened();
    store.onChanged(H1);
    store.onError('limit_exceeded');
    expect(sent).toHaveLength(2);
    vi.advanceTimersByTime(CHAT_RETRY_MS);
    expect(sent.at(-1)).toMatchObject({ type: 'chat.read', payload: { after: cursor(2) } });
    store.onError('history_changed');
    expect(ids(store)).toEqual([]);
    expect(sent.at(-1)?.type).toBe('chat.open');
  });

  it('says it failed when the open is refused or not answered, and Try again opens again', () => {
    const { store, sent } = harness();
    store.connected();
    store.open();
    vi.advanceTimersByTime(CHAT_TIMEOUT_MS);
    expect(store.get().link).toBe('failed');
    store.retry();
    expect(sent.map((item) => item.type)).toEqual(['chat.open', 'chat.open']);
  });

  it('close tells the host and forgets every block', () => {
    const { store, sent, opened } = harness();
    opened();
    store.close();
    expect(sent.at(-1)).toMatchObject({ type: 'chat.close', payload: {} });
    expect(store.get()).toMatchObject({ link: 'closed', blocks: [] });
    store.onChanged(H1);
    expect(sent.at(-1)?.type).toBe('chat.close');
  });
});

describe('chat store: paging and the 2,000-block cap', () => {
  it('pages earlier blocks on top, one request at a time, until the start', () => {
    const { store, sent, page, opened } = harness();
    opened(blocks(10, 2), cursor(1));
    store.pageEarlier();
    store.pageEarlier();
    expect(sent.filter((item) => item.payload.before !== undefined)).toHaveLength(1);
    expect(sent.at(-1)?.payload).toEqual({ before: cursor(1), limit: 20 });
    page({ blocks: [...blocks(8, 2), block('b10', 'dup')] });
    expect(ids(store)).toEqual(['b8', 'b9', 'b10', 'b11']);
    expect(store.get()).toMatchObject({ earlier: false, loadingEarlier: false });
  });

  it('keeps at most 2,000 blocks: the oldest go, cut at a page start whose cursor pages them in again', () => {
    const { store, page, opened } = harness();
    opened(blocks(0, 150), cursor(1000));
    let at = 150;
    let pages = 0;
    while (at < MAX_CHAT_BLOCKS + 100) {
      store.onChanged(H1);
      page({ blocks: blocks(at, 150), before: cursor(1000 + at), after: cursor(2000 + at) });
      at += 150;
      pages++;
    }
    expect(pages).toBeGreaterThan(10);
    const held = store.get().blocks;
    expect(held.length).toBeLessThanOrEqual(MAX_CHAT_BLOCKS);
    expect(held.at(-1)?.id).toBe(`b${at - 1}`);
    const first = Number(held[0]?.id.slice(1));
    // The cut is at a page start, and its cursor is the one to page with.
    expect(first % 150).toBe(0);
    expect(store.get().earlier).toBe(true);
    store.pageEarlier();
  });

  it('paging far back drops the newest instead, and Latest opens again', () => {
    const { store, sent, page, opened } = harness();
    opened(blocks(5000, 200), cursor(1));
    for (let round = 0; round < 10; round++) {
      store.pageEarlier();
      page({ blocks: blocks(4800 - round * 200, 200), before: cursor(2 + round) });
    }
    expect(store.get().blocks).toHaveLength(MAX_CHAT_BLOCKS);
    expect(store.get().detached).toBe(true);
    // New blocks at the end cannot join a list that lost its end.
    const count = sent.length;
    store.onChanged(H1);
    expect(sent).toHaveLength(count);
    store.latest();
    expect(sent.at(-1)?.type).toBe('chat.open');
    expect(store.get().blocks).toEqual([]);
  });
});

describe('chat data stays in memory', () => {
  it('never touches localStorage, sessionStorage, IndexedDB or the Cache API', () => {
    for (const file of ['chat.ts', '../screens/Chat.tsx', '../screens/chatText.ts']) {
      const code = readFileSync(new URL(file, import.meta.url), 'utf8');
      expect(code).not.toMatch(/localStorage|sessionStorage|indexedDB|caches\./u);
    }
  });
});
