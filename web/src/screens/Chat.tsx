import { useVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import type { ChatBlock } from '../generated/protocol';
import { AGENT_KEYS, sendAgentKey } from '../terminal/agentKeys';
import type { ChatView } from '../terminal/chat';
import type { Connection, ConnectionStatus } from '../terminal/Connection';
import { checkPaste, readDraft, saveDraft } from '../terminal/composer';
import { NO_MODIFIERS } from '../terminal/InputController';
import { since } from './activity';
import {
  chatItems, cleanText, parseInline, parseMarkdown, showHidden, taskWord, toolRow, unavailable,
  type Block, type ChatItem, type Inline,
} from './chatText';
import { canTakeControl } from './Dock';
import { formatSize } from './folder';
import { Sheet, SheetHeader } from './Sheet';
import '../styles/chat.css';

/*
 * The Chat view (docs/chat-view.md, section 9; Paper screens in docs/evidence/chat-view/). Loaded only when Chat is
 * first shown. Chat text never enters terminal state; it is rendered as text or sanitized markdown.
 */

/** Rows of this many pixels or less from the bottom count as "at the bottom". */
const BOTTOM_SLACK = 48;

export interface ChatScreenProps {
  readonly connection: Connection;
  readonly status: ConnectionStatus;
  /** settings.chat.enabled of the session's computer: off shows screen 4 and asks the computer for nothing. */
  readonly enabled: boolean;
  readonly draftKey: string;
  /** The message box: false while the answer card or a sheet stands in for it. */
  readonly bar: boolean;
  readonly onShowTerminal: () => void;
  /** Opens the Changes screen on this path (relative to the session's folder). */
  readonly onOpenChanges: (path: string) => void;
  /** Opens the Compose sheet (multi-line drafts, voice, photos). */
  readonly onCompose: () => void;
}

export function ChatScreen(props: ChatScreenProps) {
  const { connection, enabled } = props;
  const view = useSyncExternalStore(connection.subscribe, connection.getChat);
  useEffect(() => {
    if (!enabled) return undefined;
    connection.chat.open();
    return () => connection.chat.close();
  }, [connection, enabled]);
  const link = enabled ? view.link : 'off';
  if (link === 'linked' || link === 'ended') return <ChatList {...props} view={view} />;
  if (link === 'opening' || link === 'closed') {
    return <section className="chat chat-wait" aria-label="Chat"><p role="status" className="muted">Opening chat…</p></section>;
  }
  return <NotAvailable link={link} reason={view.reason} onShowTerminal={props.onShowTerminal} onRetry={() => connection.chat.retry()} />;
}

// ---------------------------------------------------------------- Screen 4

export function NotAvailable({ link, reason, onShowTerminal, onRetry }: {
  readonly link: string;
  readonly reason: string | undefined;
  readonly onShowTerminal: () => void;
  readonly onRetry: () => void;
}) {
  const [why, setWhy] = useState(false);
  const words = unavailable(link, reason);
  const searching = link === 'searching';
  return (
    <section className="chat chat-none" aria-labelledby="chat-none-title">
      <span className="chat-none-art" aria-hidden="true">
        <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 5h16v11H9l-5 4z" />
          <path className="chat-none-slash" d="M3 3l18 18" />
        </svg>
      </span>
      <h2 id="chat-none-title" className="chat-none-title">{searching ? 'Looking for the conversation' : 'Chat isn’t available for this session'}</h2>
      <p className="chat-none-lead" role="status">{words.lead}</p>
      <div className="chat-none-actions">
        <button type="button" className="primary" onClick={onShowTerminal}>Show terminal</button>
        {link === 'failed'
          ? <button type="button" onClick={onRetry}>Try again</button>
          : <button type="button" aria-haspopup="dialog" onClick={() => setWhy(true)}>Why?</button>}
      </div>
      <p className="chat-none-foot">Chat works today with Claude Code.</p>
      {why && (
        <Sheet labelledBy="chat-why-title" onClose={() => setWhy(false)}>
          <SheetHeader id="chat-why-title" title="Why is Chat not available?">
            <button type="button" className="link" onClick={() => setWhy(false)}>Close</button>
          </SheetHeader>
          <p className="chat-why"><Inlines items={parseInline(words.why)} /></p>
        </Sheet>
      )}
    </section>
  );
}

// ---------------------------------------------------------------- The list

function ChatList({ connection, status, view, draftKey, bar, onOpenChanges, onCompose }: ChatScreenProps & { readonly view: ChatView }) {
  const scroller = useRef<HTMLDivElement>(null);
  const items = useMemo(() => chatItems(view.blocks), [view.blocks]);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(timer);
  }, []);
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set());
  const toggle = (id: string) => setOpen((current) => {
    const next = new Set(current);
    if (!next.delete(id)) next.add(id);
    return next;
  });
  const [link, setLink] = useState<string>();
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 96,
    getItemKey: (index) => items[index]?.key ?? index,
    overscan: 6,
  });

  // Sticky to the bottom while at the bottom; else count what arrived below ("N new").
  const atBottom = useRef(true);
  const [seen, setSeen] = useState<string>();
  const lastKey = items[items.length - 1]?.key;
  const total = virtualizer.getTotalSize();
  useLayoutEffect(() => {
    const element = scroller.current;
    if (element !== null && atBottom.current) element.scrollTop = element.scrollHeight;
  }, [lastKey, total, bar]);
  // An earlier page went on top: keep what the user was reading in place.
  const firstKey = items[0]?.key;
  const previous = useRef({ firstKey, total });
  useLayoutEffect(() => {
    const element = scroller.current;
    const before = previous.current;
    if (element !== null && !atBottom.current && before.firstKey !== undefined && before.firstKey !== firstKey
      && items.some((item) => item.key === before.firstKey)) {
      element.scrollTop += total - before.total;
    }
    previous.current = { firstKey, total };
  }, [firstKey, total, items]);
  const seenAt = seen === undefined ? -1 : items.findIndex((item) => item.key === seen);
  const fresh = seen === undefined || seenAt < 0 ? 0 : items.length - 1 - seenAt;

  function onScroll() {
    const element = scroller.current;
    if (element === null) return;
    const bottom = element.scrollHeight - element.scrollTop - element.clientHeight <= BOTTOM_SLACK;
    if (bottom !== atBottom.current) {
      atBottom.current = bottom;
      setSeen(bottom ? undefined : lastKey);
    }
  }

  function toBottom() {
    const element = scroller.current;
    atBottom.current = true;
    setSeen(undefined);
    if (element !== null) element.scrollTop = element.scrollHeight;
  }

  const agent = view.agent ?? 'agent';
  const working = status.agent?.state === 'working';
  return (
    <section className="chat" aria-label="Chat">
      <div ref={scroller} className="chat-scroll" onScroll={onScroll}>
        {view.earlier && !view.detached && (
          <button type="button" className="chat-earlier" disabled={view.loadingEarlier} onClick={() => connection.chat.pageEarlier()}>
            {view.loadingEarlier ? 'Loading earlier messages…' : <><span aria-hidden="true">↑ </span>Earlier messages</>}
          </button>
        )}
        {items.length === 0 && <p className="chat-empty muted" role="status">No messages yet.</p>}
        <div className="chat-rows" style={{ height: total }}>
          {virtualizer.getVirtualItems().map((row) => {
            const item = items[row.index];
            if (item === undefined) return null;
            return (
              <div key={row.key} ref={virtualizer.measureElement} data-index={row.index} className="chat-row" style={{ transform: `translateY(${row.start}px)` }}>
                <Item item={item} agent={agent} now={now} open={open} onToggle={toggle} onLink={setLink} onOpenChanges={onOpenChanges} />
              </div>
            );
          })}
        </div>
        {view.link === 'ended' && <p className="chat-ended muted">The agent exited. This is its last conversation.</p>}
      </div>
      {view.detached ? (
        <button type="button" className="chat-new" onClick={() => { atBottom.current = true; setSeen(undefined); connection.chat.latest(); }}>
          Latest <span aria-hidden="true">↓</span>
        </button>
      ) : fresh > 0 && (
        <button type="button" className="chat-new" aria-label={`${fresh} new ${fresh === 1 ? 'message' : 'messages'}, go to the newest`} onClick={toBottom}>
          {fresh} new <span aria-hidden="true">↓</span>
        </button>
      )}
      {working && (
        <div className="chat-working">
          <WorkingLine since={status.agent?.stateSince} />
          <button
            type="button"
            className="key chat-esc"
            aria-label="Escape, stop the agent"
            disabled={status.control !== 'controller'}
            onClick={() => { const esc = AGENT_KEYS[0]; if (esc !== undefined) sendAgentKey(connection, esc); }}
          >esc stop</button>
        </div>
      )}
      {bar && <ChatBar connection={connection} status={status} agent={agent} draftKey={draftKey} onCompose={onCompose} />}
      {link !== undefined && <LinkSheet url={link} onClose={() => setLink(undefined)} />}
    </section>
  );
}

/** "Working · 6s", from the agent state (the terminal's spinner), not from the transcript. */
function WorkingLine({ since: from }: { readonly since: string | undefined }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const elapsed = since(from, now);
  return <p role="status"><span className="dot accent" aria-hidden="true" />Working{elapsed === '' ? '' : ` · ${elapsed}`}</p>;
}

function Item({ item, agent, now, open, onToggle, onLink, onOpenChanges }: {
  readonly item: ChatItem;
  readonly agent: string;
  readonly now: number;
  readonly open: ReadonlySet<string>;
  readonly onToggle: (id: string) => void;
  readonly onLink: (url: string) => void;
  readonly onOpenChanges: (path: string) => void;
}) {
  const head = item.head ? (
    <p className="chat-head">
      <span className="chat-mark" aria-hidden="true">◆</span>
      <span className="chat-who">{agent}</span>
      {item.kind === 'block' && item.block.at !== undefined && <span className="chat-ago">{since(item.block.at, now)}</span>}
    </p>
  ) : null;
  if (item.kind === 'tools') {
    return (
      <>
        {head}
        <ul className="chat-tools" aria-label="Tools">
          {item.blocks.map((block) => <ToolRow key={block.id} block={block} open={open.has(block.id)} onToggle={() => onToggle(block.id)} onOpenChanges={onOpenChanges} />)}
        </ul>
      </>
    );
  }
  const { block } = item;
  switch (block.kind) {
    case 'user_text':
      return (
        <div className="chat-you">
          <p className="chat-meta">You{block.at === undefined ? '' : ` · ${since(block.at, now)}`}</p>
          <p className="chat-bubble">{cleanText(block.text ?? '')}</p>
          <Cut bytes={block.truncatedBytes} />
        </div>
      );
    case 'assistant_text':
      return <>{head}<Markdown text={block.text ?? ''} onLink={onLink} /><Cut bytes={block.truncatedBytes} /></>;
    case 'compaction':
      return (
        <p className="chat-divider" role="separator" aria-label="Conversation compacted">
          <span>Conversation compacted{block.at === undefined ? '' : ` · ${since(block.at, now)} ago`}</span>
        </p>
      );
    case 'status':
      return <>{head}<p className="chat-status"><span className="chat-status-mark" aria-hidden="true">◷</span>{cleanText(block.text ?? '')}</p></>;
    case 'omitted':
      return <>{head}<p className="chat-omitted">{block.text === undefined || block.text === '' ? 'Part of the conversation is not shown here. The terminal has it.' : cleanText(block.text)}</p></>;
    case 'tool_call':
      // Only Task reaches here (other tools are grouped): folded, its summary and state only.
      return <>{head}<TaskRow block={block} open={open.has(block.id)} onToggle={() => onToggle(block.id)} /></>;
    default:
      return null;
  }
}

function Cut({ bytes }: { readonly bytes: number | undefined }) {
  return bytes === undefined || bytes <= 0 ? null : <p className="chat-cut muted">{formatSize(bytes)} more not shown. The terminal has it.</p>;
}

function ToolRow({ block, open, onToggle, onOpenChanges }: {
  readonly block: ChatBlock;
  readonly open: boolean;
  readonly onToggle: () => void;
  readonly onOpenChanges: (path: string) => void;
}) {
  const row = toolRow(block);
  const { edit } = block;
  const preview = block.tool?.result?.preview;
  const body = edit?.hunk ?? preview;
  const counts = edit === undefined ? '' : `, ${edit.added} added, ${edit.removed} removed`;
  const label = `${row.verb} ${row.what}${counts}${row.word === undefined ? '' : `, ${row.word}`}`;
  return (
    <li className="chat-tool">
      <button type="button" className="chat-tool-row" aria-label={label} {...(body === undefined ? {} : { 'aria-expanded': open })} onClick={onToggle}>
        <span className={`dot ${row.tone}`} aria-hidden="true" />
        <span className="chat-verb">{row.verb}</span>
        <span className="chat-what" dir="ltr">{row.what}</span>
        {edit !== undefined && <span className="chat-counts"><span className="green">+{edit.added}</span> <span className="red">−{edit.removed}</span></span>}
        {row.word !== undefined && <span className={`chat-word ${row.tone}`}>{row.word}</span>}
        {body !== undefined && <span className={open ? 'chat-chevron open' : 'chat-chevron'} aria-hidden="true">›</span>}
      </button>
      {open && body !== undefined && (
        <div className="chat-tool-body">
          {edit?.hunk !== undefined ? <Hunk text={edit.hunk} /> : <pre className="chat-preview">{cleanText(body)}</pre>}
          {block.tool?.result?.truncated === true && <p className="chat-cut muted">Shortened: {formatSize(block.tool.result.bytes)} in all. The terminal has the rest.</p>}
          {edit !== undefined && (
            <button type="button" className="link accent chat-open-changes" onClick={() => onOpenChanges(edit.path)}>Open in Changes ›</button>
          )}
        </div>
      )}
    </li>
  );
}

function Hunk({ text }: { readonly text: string }) {
  return (
    <pre className="chat-hunk">
      {cleanText(text).split('\n').map((line, index) => (
        // Lines of one hunk never move: the index is a stable key.
        <span key={index} className={line.startsWith('+') ? 'add' : line.startsWith('-') ? 'del' : line.startsWith('@@') ? 'at' : undefined}>{`${line}\n`}</span>
      ))}
    </pre>
  );
}

function TaskRow({ block, open, onToggle }: { readonly block: ChatBlock; readonly open: boolean; readonly onToggle: () => void }) {
  const status = block.tool?.status;
  const word = taskWord(status);
  const tone = status === 'ok' ? 'green' : status === 'running' ? 'accent' : status === 'error' ? 'red' : 'muted';
  const summary = showHidden(block.tool?.summary ?? '');
  const preview = block.tool?.result?.preview;
  return (
    <div className="chat-task">
      <button type="button" className="chat-task-row" aria-label={`Task ${summary}, ${word}`} {...(preview === undefined ? {} : { 'aria-expanded': open })} onClick={onToggle}>
        <span className="chat-verb">Task</span>
        <span className="chat-what">{summary}</span>
        <span className={`chat-word ${tone}`}><span className={`dot ${tone}`} aria-hidden="true" /> {word}</span>
      </button>
      {open && preview !== undefined && <pre className="chat-preview">{cleanText(preview)}</pre>}
    </div>
  );
}

// ---------------------------------------------------------------- Markdown and links

export function Markdown({ text, onLink }: { readonly text: string; readonly onLink: (url: string) => void }) {
  const blocks = useMemo(() => parseMarkdown(text), [text]);
  return <div className="chat-text">{blocks.map((block, index) => <MarkdownBlock key={index} block={block} onLink={onLink} />)}</div>;
}

function MarkdownBlock({ block, onLink }: { readonly block: Block; readonly onLink: (url: string) => void }) {
  switch (block.kind) {
    case 'pre': return <pre className="chat-code">{block.text}</pre>;
    case 'h': return <p className="chat-h"><Inlines items={block.children} onLink={onLink} /></p>;
    case 'quote': return <blockquote><Inlines items={block.children} onLink={onLink} /></blockquote>;
    case 'list': {
      const children = block.items.map((item, index) => <li key={index}><Inlines items={item} onLink={onLink} /></li>);
      return block.ordered ? <ol>{children}</ol> : <ul>{children}</ul>;
    }
    default: return <p><Inlines items={block.children} onLink={onLink} /></p>;
  }
}

export function Inlines({ items, onLink }: { readonly items: ReadonlyArray<Inline>; readonly onLink?: (url: string) => void }): ReactNode {
  return items.map((item, index) => {
    switch (item.kind) {
      case 'code': return <code key={index}>{item.text}</code>;
      case 'strong': return <strong key={index}><Inlines items={item.children} {...(onLink === undefined ? {} : { onLink })} /></strong>;
      case 'em': return <em key={index}><Inlines items={item.children} {...(onLink === undefined ? {} : { onLink })} /></em>;
      case 'link':
        // A button, not an anchor: nothing opens without the confirm sheet, which shows the full address.
        return onLink === undefined ? <span key={index}>{item.text}</span> : (
          <button key={index} type="button" className="chat-link" aria-haspopup="dialog" aria-label={`Link: ${item.text}, ${item.url}`} onClick={() => onLink(item.url)}>
            {item.text}
          </button>
        );
      default: return <span key={index}>{item.text}</span>;
    }
  });
}

/** Confirm before leaving the app: the full address, then Open (new tab, noopener noreferrer). */
export function LinkSheet({ url, onClose }: { readonly url: string; readonly onClose: () => void }) {
  return (
    <Sheet labelledBy="chat-link-title" onClose={onClose}>
      <SheetHeader id="chat-link-title" title="Open this link?" />
      <p className="hint-text">It came from the agent’s conversation. Check the address before you open it.</p>
      <p className="chat-link-url" dir="ltr">{showHidden(url)}</p>
      <div className="sheet-actions">
        <button type="button" onClick={onClose}>Cancel</button>
        <button
          type="button"
          className="primary"
          onClick={() => { window.open(url, '_blank', 'noopener,noreferrer'); onClose(); }}
        >Open</button>
      </div>
    </Sheet>
  );
}

// ---------------------------------------------------------------- The message box

/** The message box: the Compose draft and input path (paste, then Enter), only with control. */
function ChatBar({ connection, status, agent, draftKey, onCompose }: {
  readonly connection: Connection;
  readonly status: ConnectionStatus;
  readonly agent: string;
  readonly draftKey: string;
  readonly onCompose: () => void;
}) {
  const [text, setText] = useState(() => readDraft(draftKey));
  useEffect(() => { saveDraft(draftKey, text); }, [draftKey, text]);
  const controller = status.control === 'controller';
  if (!controller) {
    return (
      <div className="chat-bar">
        <button type="button" className="primary chat-take" disabled={!canTakeControl(status)} onClick={() => connection.takeControl()}>
          {status.control === 'acquiring' ? 'Taking control…' : 'Take control to send'}
        </button>
      </div>
    );
  }
  const canSend = status.link === 'live';
  function send() {
    const check = checkPaste(text, canSend, false);
    if (check === 'ready') {
      if (connection.sendPaste(text)) {
        connection.sendKey('enter', NO_MODIFIERS);
        setText('');
      }
    } else if (check === 'needs-confirmation' || check === 'too-large') {
      // Compose has the multi-line confirmation and the size note.
      onCompose();
    }
  }
  return (
    <form className="chat-bar" onSubmit={(event) => { event.preventDefault(); send(); }}>
      <label htmlFor="chat-message" className="visually-hidden">Message {agent}</label>
      <textarea
        id="chat-message"
        className="chat-input"
        rows={1}
        value={text}
        placeholder={`Message ${agent}…`}
        enterKeyHint="send"
        onChange={(event) => setText(event.target.value)}
      />
      <button type="button" className="round chat-round" aria-label="Compose: voice, photo, several lines" onClick={onCompose}>
        <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true">
          <rect x="9" y="3" width="6" height="11" rx="3" fill="none" stroke="currentColor" strokeWidth="1.8" />
          <path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
      </button>
      <button type="submit" className="primary round chat-send" aria-label="Send" disabled={!canSend || text === ''}>
        <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
          <path d="M9 15V3m0 0L4 8m5-5 5 5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
    </form>
  );
}
