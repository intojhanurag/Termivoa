import type { ChatAgent, ChatBlock, ChatPagePayload, ChatStatePayload } from '../generated/protocol';

/**
 * Chat view, phone side (docs/chat-view.md, ADR 0014). Pull model: the host
 * sends only a small chat.changed; this store asks for the next page with one
 * request in flight. Blocks live in memory only (rule 19): nothing goes to
 * browser storage or the service worker, and React reads only this store's snapshot.
 */

/** Turns asked for on open and per earlier page. */
export const CHAT_OPEN_TURNS = 30;
export const CHAT_PAGE_TURNS = 20;
/** Blocks kept in memory; older ones are dropped and can be paged again. */
export const MAX_CHAT_BLOCKS = 2_000;
/** A chat request with no reply by then counts as lost, and the next one may go. */
export const CHAT_TIMEOUT_MS = 15_000;
/** The host refused because a chat request was already running: ask again after this. */
export const CHAT_RETRY_MS = 1_000;

/** off, unsupported, searching, linked, none, ended (host words), or this phone's own: closed, opening, failed. */
export type ChatLink = 'closed' | 'opening' | 'failed' | 'off' | 'unsupported' | 'searching' | 'linked' | 'none' | 'ended';

export interface ChatView {
  readonly link: ChatLink;
  /** Why link is none (host word). */
  readonly reason: string | undefined;
  readonly agent: ChatAgent | undefined;
  /** In file order, unique by id. */
  readonly blocks: ReadonlyArray<ChatBlock>;
  /** Older blocks exist on the computer. */
  readonly earlier: boolean;
  readonly loadingEarlier: boolean;
  /** The newest blocks were dropped to keep older ones (paged far back): Latest opens again. */
  readonly detached: boolean;
}

export const CLOSED_CHAT: ChatView = {
  link: 'closed', reason: undefined, agent: undefined, blocks: [], earlier: false, loadingEarlier: false, detached: false,
};

export interface ChatTransport {
  /** Sends one session message; the request ID, or undefined when the socket is not open. */
  send(type: 'chat.open' | 'chat.read' | 'chat.close', payload: object): string | undefined;
  emit(): void;
  later(run: () => void, ms: number): () => void;
}

type Request = { readonly id: string; readonly kind: 'open' | 'before' | 'after' };

/** Where a page began: its first block and the cursor for what is older. */
interface Boundary {
  readonly id: string;
  readonly before: string | undefined;
}

export class ChatStore {
  private view: ChatView = CLOSED_CHAT;
  private wanted = false;
  private historyId: string | undefined;
  private before: string | undefined;
  private after: string | undefined;
  private boundaries: Boundary[] = [];
  private request: Request | undefined;
  /** A retry waits (limit_exceeded). */
  private cancelTimer: (() => void) | undefined;
  private stopTimeout: (() => void) | undefined;
  private needOpen = false;
  private dirty = false;
  private wantEarlier = false;
  private online = false;

  constructor(private readonly transport: ChatTransport) {}

  readonly get = (): ChatView => this.view;

  /** Chat is on screen: ask for the latest page (now or once live). */
  open(): void {
    if (this.wanted) return;
    this.wanted = true;
    this.needOpen = true;
    this.set({ ...CLOSED_CHAT, link: 'opening' });
    this.pump();
  }

  /** Chat left the screen: the host stops reading, and every block is dropped. */
  close(): void {
    if (!this.wanted) return;
    this.wanted = false;
    if (this.online) this.transport.send('chat.close', {});
    this.forget();
    this.set(CLOSED_CHAT);
  }

  /** Asks for older blocks, once at a time. */
  pageEarlier(): void {
    if (!this.wanted || this.before === undefined || this.view.loadingEarlier) return;
    this.wantEarlier = true;
    this.set({ ...this.view, loadingEarlier: true });
    this.pump();
  }

  /** After paging far back: drop everything and open again at the latest. */
  latest(): void {
    if (!this.wanted) return;
    this.reopen();
  }

  /** After a failure: try again. */
  retry(): void {
    if (this.wanted && this.view.link === 'failed') this.reopen();
  }

  /** The socket went live (first time or after a reconnect): open again, a fresh latest page, no replay. */
  connected(): void {
    this.online = true;
    if (this.wanted) {
      this.clearRequest();
      this.needOpen = true;
      this.pump();
    }
  }

  /** The socket closed: replies can no longer arrive. Blocks stay on screen until the fresh page replaces them. */
  disconnected(): void {
    this.online = false;
    this.clearRequest();
    this.wantEarlier = false;
    if (this.view.loadingEarlier) this.set({ ...this.view, loadingEarlier: false });
  }

  /** True when this request ID is a chat request in flight (its error belongs here). */
  owns(requestId: string | undefined): boolean {
    return requestId !== undefined && this.request?.id === requestId;
  }

  onState(state: ChatStatePayload, requestId: string): void {
    if (!this.wanted) return;
    const reply = this.owns(requestId) && this.request?.kind === 'open';
    const history = state.link === 'linked' || state.link === 'ended';
    if (!history || (state.historyId !== undefined && this.historyId !== undefined && state.historyId !== this.historyId)) {
      // Not linked (or linked to another history): nothing held belongs to what is shown now.
      this.forgetBlocks();
    }
    if (reply && !history) this.clearRequest();
    if (history && state.historyId !== undefined && this.historyId === undefined && !reply) {
      // Linked after searching, or a new history: ask for its latest page.
      this.needOpen = true;
    }
    if (history) this.historyId = state.historyId ?? this.historyId;
    if (state.stale === true && history) {
      if (this.after === undefined) this.needOpen = true;
      else this.dirty = true;
    }
    this.set({
      ...this.view,
      link: state.link as ChatLink,
      reason: state.link === 'none' ? state.reason : undefined,
      agent: state.agent ?? (history ? this.view.agent : undefined),
      ...this.cursors(),
    });
    this.pump();
  }

  onChanged(historyId: string): void {
    if (!this.wanted) return;
    if (historyId !== this.historyId) {
      // Reset: the transcript was replaced, rewound or truncated.
      this.forgetBlocks();
      this.historyId = historyId;
      this.needOpen = true;
    } else if (this.view.detached) {
      return;
    } else {
      this.dirty = true;
    }
    this.pump();
  }

  onPage(page: ChatPagePayload, requestId: string): void {
    const request = this.request;
    if (!this.wanted || request === undefined || request.id !== requestId) return;
    this.clearRequest();
    if (this.historyId !== undefined && page.historyId !== this.historyId && request.kind !== 'open') {
      this.forgetBlocks();
      this.historyId = page.historyId;
      this.needOpen = true;
      this.pump();
      return;
    }
    this.historyId = page.historyId;
    let blocks: ChatBlock[];
    let detached = this.view.detached;
    if (request.kind === 'open') {
      blocks = dedupe(page.blocks);
      this.boundaries = [];
      this.before = page.before;
      this.after = page.after;
      detached = false;
      this.dirty = !page.complete;
    } else if (request.kind === 'after') {
      blocks = upsert(this.view.blocks, page.blocks, 'end');
      this.after = page.after;
      this.dirty = this.dirty || !page.complete;
    } else {
      blocks = upsert(this.view.blocks, page.blocks, 'start');
      this.before = page.before;
      this.wantEarlier = false;
    }
    const first = page.blocks[0];
    if (first !== undefined) {
      if (request.kind === 'before') this.boundaries.unshift({ id: first.id, before: page.before });
      else this.boundaries.push({ id: first.id, before: page.before });
    }
    if (blocks.length > MAX_CHAT_BLOCKS) {
      if (request.kind === 'before') {
        // Paged far back: keep the older blocks, drop the newest; Latest opens again.
        blocks = blocks.slice(0, MAX_CHAT_BLOCKS);
        this.after = undefined;
        this.dirty = false;
        detached = true;
      } else {
        blocks = this.trimOldest(blocks);
      }
    }
    this.set({ ...this.view, blocks, detached, loadingEarlier: false, ...this.cursors() });
    this.pump();
  }

  /** The host refused a chat request. */
  onError(code: string): void {
    const request = this.request;
    if (request === undefined) return;
    this.clearRequest();
    if (code === 'history_changed') {
      this.forgetBlocks();
      this.needOpen = true;
      this.pump();
      return;
    }
    if (code === 'limit_exceeded') {
      // Another chat request (or too many opens) ran: ask again a moment later.
      if (request.kind === 'open') this.needOpen = true;
      else if (request.kind === 'after') this.dirty = true;
      else this.wantEarlier = true;
      this.cancelTimer = this.transport.later(() => { this.cancelTimer = undefined; this.pump(); }, CHAT_RETRY_MS);
      return;
    }
    this.wantEarlier = false;
    if (request.kind === 'open') this.set({ ...this.view, link: 'failed', loadingEarlier: false });
    else this.set({ ...this.view, loadingEarlier: false });
  }

  /** Sends what the view waits for next, unless a chat request is in flight. */
  private pump(): void {
    if (!this.wanted || !this.online || this.request !== undefined || this.cancelTimer !== undefined) return;
    let kind: Request['kind'] | undefined;
    let payload: object | undefined;
    if (this.needOpen) {
      kind = 'open';
      payload = { limit: CHAT_OPEN_TURNS };
    } else if (this.dirty && this.after !== undefined && !this.view.detached) {
      kind = 'after';
      payload = { after: this.after, limit: CHAT_OPEN_TURNS };
    } else if (this.wantEarlier && this.before !== undefined) {
      kind = 'before';
      payload = { before: this.before, limit: CHAT_PAGE_TURNS };
    }
    if (kind === undefined || payload === undefined) return;
    const id = this.transport.send(kind === 'open' ? 'chat.open' : 'chat.read', payload);
    if (id === undefined) return;
    if (kind === 'open') this.needOpen = false;
    if (kind === 'after') this.dirty = false;
    this.request = { id, kind };
    this.stopTimeout = this.transport.later(() => {
      if (this.request?.id === id) this.onError('timeout');
    }, CHAT_TIMEOUT_MS);
  }

  private clearRequest(): void {
    this.request = undefined;
    this.stopTimeout?.();
    this.stopTimeout = undefined;
    this.cancelTimer?.();
    this.cancelTimer = undefined;
  }

  private reopen(): void {
    this.clearRequest();
    this.forgetBlocks();
    this.needOpen = true;
    this.set({ ...this.view, link: 'opening' });
    this.pump();
  }

  /** Drops the oldest blocks down to a page start, so its cursor pages them in again. */
  private trimOldest(blocks: ChatBlock[]): ChatBlock[] {
    const target = blocks.length - MAX_CHAT_BLOCKS;
    const index = new Map(blocks.map((block, at) => [block.id, at]));
    const keep = this.boundaries.find((boundary) => (index.get(boundary.id) ?? -1) >= target);
    const from = keep === undefined ? target : index.get(keep.id) ?? target;
    this.boundaries = this.boundaries.filter((boundary) => (index.get(boundary.id) ?? -1) >= from);
    this.before = keep === undefined ? this.before : keep.before;
    return blocks.slice(from);
  }

  private cursors(): Pick<ChatView, 'earlier'> {
    return { earlier: this.before !== undefined };
  }

  private forgetBlocks(): void {
    this.historyId = undefined;
    this.before = undefined;
    this.after = undefined;
    this.boundaries = [];
    this.dirty = false;
    this.wantEarlier = false;
    this.view = { ...this.view, blocks: [], earlier: false, loadingEarlier: false, detached: false };
  }

  private forget(): void {
    this.clearRequest();
    this.forgetBlocks();
    this.needOpen = false;
  }

  private set(view: ChatView): void {
    this.view = view;
    this.transport.emit();
  }
}

function dedupe(blocks: ReadonlyArray<ChatBlock>): ChatBlock[] {
  return upsert([], blocks, 'end');
}

/** Adds blocks at one end; a block whose id is held replaces it in place (a tool result updates its call). */
export function upsert(held: ReadonlyArray<ChatBlock>, incoming: ReadonlyArray<ChatBlock>, end: 'start' | 'end'): ChatBlock[] {
  const result = [...held];
  const at = new Map(result.map((block, index) => [block.id, index]));
  const fresh: ChatBlock[] = [];
  const freshAt = new Map<string, number>();
  for (const block of incoming) {
    const index = at.get(block.id);
    const inPage = freshAt.get(block.id);
    if (index !== undefined) result[index] = block;
    else if (inPage !== undefined) fresh[inPage] = block;
    else {
      freshAt.set(block.id, fresh.length);
      fresh.push(block);
    }
  }
  return end === 'end' ? [...result, ...fresh] : [...fresh, ...result];
}
