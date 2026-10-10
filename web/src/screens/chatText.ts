import type { ChatBlock } from '../generated/protocol';

/*
 * Chat view text (docs/chat-view.md, section 14, S3). Transcript text is
 * attacker-influenced: it is parsed here into a small tree that React renders
 * as text. No raw HTML, no images; a link is only an http(s) address of
 * another site, opened after a confirm step.
 */

/** Bidi controls and zero-width characters (U+202A–202E, U+2066–2069, U+200B–200F). */
const HIDDEN = /[​-‏‪-‮⁦-⁩]/gu;

/** Prose: the hidden characters are removed. */
export const cleanText = (text: string): string => text.replace(HIDDEN, '');

/** Paths and summaries: a hidden character is shown as �, so a faked name cannot pass for a real one. */
export const showHidden = (text: string): string => text.replace(HIDDEN, '�');

export type Inline =
  | { readonly kind: 'text'; readonly text: string }
  | { readonly kind: 'code'; readonly text: string }
  | { readonly kind: 'strong' | 'em'; readonly children: ReadonlyArray<Inline> }
  | { readonly kind: 'link'; readonly text: string; readonly url: string };

export type Block =
  | { readonly kind: 'p' | 'h'; readonly children: ReadonlyArray<Inline> }
  | { readonly kind: 'quote'; readonly children: ReadonlyArray<Inline> }
  | { readonly kind: 'list'; readonly ordered: boolean; readonly items: ReadonlyArray<ReadonlyArray<Inline>> }
  | { readonly kind: 'pre'; readonly text: string };

/**
 * The address a link may open, or undefined: http and https only, and never
 * this app's own origin (a faked pairing or add-computer link stays text).
 */
export function safeLink(raw: string, own: string | undefined = globalThis.location?.origin): string | undefined {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return undefined;
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return undefined;
  if (url.username !== '' || url.password !== '') return undefined;
  if (own !== undefined && url.origin === own) return undefined;
  return url.href;
}

// `code`, ![alt](url), [text](url), **strong**, __strong__, *em*, _em_, a bare address.
const INLINE = /`([^`\n]+)`|!\[([^\]\n]*)\]\(([^)\s]*)\)|\[([^\]\n]+)\]\(([^)\s]+)\)|\*\*([^*\n]+)\*\*|__([^_\n]+)__|\*([^*\n]+)\*|\b_([^_\n]+)_\b|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"\]])/gu;

export function parseInline(text: string): Inline[] {
  const out: Inline[] = [];
  let last = 0;
  const push = (item: Inline) => {
    const previous = out[out.length - 1];
    if (item.kind === 'text' && previous?.kind === 'text') out[out.length - 1] = { kind: 'text', text: previous.text + item.text };
    else if (item.kind !== 'text' || item.text !== '') out.push(item);
  };
  for (const match of text.matchAll(INLINE)) {
    const at = match.index;
    push({ kind: 'text', text: text.slice(last, at) });
    last = at + match[0].length;
    const [, code, alt, , label, href, strong1, strong2, em1, em2, bare] = match;
    if (code !== undefined) push({ kind: 'code', text: code });
    else if (alt !== undefined) {
      // No images: the alt text only.
      if (alt !== '') push({ kind: 'text', text: alt });
    } else if (label !== undefined) {
      const url = safeLink(href ?? '');
      push(url === undefined ? { kind: 'text', text: label } : { kind: 'link', text: label, url });
    } else if (strong1 !== undefined || strong2 !== undefined) push({ kind: 'strong', children: parseInline(strong1 ?? strong2 ?? '') });
    else if (em1 !== undefined || em2 !== undefined) push({ kind: 'em', children: parseInline(em1 ?? em2 ?? '') });
    else if (bare !== undefined) {
      const url = safeLink(bare);
      push(url === undefined ? { kind: 'text', text: bare } : { kind: 'link', text: bare, url });
    }
  }
  push({ kind: 'text', text: text.slice(last) });
  return out;
}

const LIST = /^\s{0,3}([-*+]|\d{1,9}[.)])\s+(.*)$/u;
const FENCE = /^\s{0,3}(```|~~~)/u;

/** Agent markdown to a small tree: paragraphs, headings, lists, quotes and code blocks. */
export function parseMarkdown(source: string): Block[] {
  const lines = cleanText(source).split('\n');
  const blocks: Block[] = [];
  let paragraph: string[] = [];
  const flush = () => {
    if (paragraph.length > 0) blocks.push({ kind: 'p', children: parseInline(paragraph.join(' ')) });
    paragraph = [];
  };
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index] ?? '';
    const fence = FENCE.exec(line);
    if (fence !== null) {
      flush();
      const code: string[] = [];
      index++;
      while (index < lines.length && !(lines[index] ?? '').trimStart().startsWith(fence[1] ?? '```')) code.push(lines[index++] ?? '');
      blocks.push({ kind: 'pre', text: code.join('\n') });
      continue;
    }
    if (line.trim() === '') {
      flush();
      continue;
    }
    const heading = /^\s{0,3}#{1,6}\s+(.*)$/u.exec(line);
    if (heading !== null) {
      flush();
      blocks.push({ kind: 'h', children: parseInline(heading[1] ?? '') });
      continue;
    }
    const quote = /^\s{0,3}>\s?(.*)$/u.exec(line);
    if (quote !== null) {
      flush();
      blocks.push({ kind: 'quote', children: parseInline(quote[1] ?? '') });
      continue;
    }
    const item = LIST.exec(line);
    if (item !== null) {
      flush();
      const ordered = /\d/u.test(item[1] ?? '');
      const previous = blocks[blocks.length - 1];
      const entry = parseInline(item[2] ?? '');
      if (previous?.kind === 'list' && previous.ordered === ordered) {
        blocks[blocks.length - 1] = { ...previous, items: [...previous.items, entry] };
      } else {
        blocks.push({ kind: 'list', ordered, items: [entry] });
      }
      continue;
    }
    paragraph.push(line.trim());
  }
  flush();
  return blocks;
}

// ---------------------------------------------------------------- The list

export type ChatItem =
  | { readonly key: string; readonly kind: 'block'; readonly block: ChatBlock; readonly head: boolean }
  | { readonly key: string; readonly kind: 'tools'; readonly blocks: ReadonlyArray<ChatBlock>; readonly head: boolean };

const isTool = (block: ChatBlock) => (block.kind === 'tool_call' && block.tool?.name !== 'Task') || block.kind === 'edit';

/**
 * Blocks to list rows, keyed by the first block's id: tool and edit rows next
 * to each other share one box. `head`: the first agent row after your
 * message carries the agent's name.
 */
export function chatItems(blocks: ReadonlyArray<ChatBlock>): ChatItem[] {
  const items: ChatItem[] = [];
  let needHead = true;
  for (const block of blocks) {
    if (block.kind === 'user_text') {
      items.push({ key: block.id, kind: 'block', block, head: false });
      needHead = true;
      continue;
    }
    if (block.kind === 'compaction') {
      items.push({ key: block.id, kind: 'block', block, head: false });
      continue;
    }
    const head = needHead;
    needHead = false;
    const previous = items[items.length - 1];
    if (isTool(block)) {
      if (previous?.kind === 'tools') items[items.length - 1] = { ...previous, blocks: [...previous.blocks, block] };
      else items.push({ key: block.id, kind: 'tools', blocks: [block], head });
      continue;
    }
    items.push({ key: block.id, kind: 'block', block, head });
  }
  return items;
}

export type ToolTone = 'green' | 'accent' | 'red' | 'muted';

/** One tool row: its verb, what it worked on, its state word and the dot colour. */
export function toolRow(block: ChatBlock): { readonly verb: string; readonly what: string; readonly word: string | undefined; readonly tone: ToolTone } {
  const status = block.tool?.status ?? (block.kind === 'edit' ? 'ok' : 'unknown');
  const tone: ToolTone = status === 'ok' ? 'green' : status === 'running' ? 'accent' : status === 'error' ? 'red' : 'muted';
  const name = block.tool?.name ?? 'Edit';
  const verb = block.kind === 'edit' ? 'Edit'
    : name === 'Bash' ? (status === 'running' ? 'Run' : 'Ran')
      : name;
  const what = showHidden(block.edit?.path ?? block.tool?.summary ?? '');
  const word = status === 'running' ? 'running' : status === 'error' ? 'failed' : status === 'unknown' ? 'unknown' : undefined;
  return { verb, what, word, tone };
}

/** The Task row's state word. */
export const taskWord = (status: string | undefined): string => (
  status === 'ok' ? 'done' : status === 'running' ? 'running' : status === 'error' ? 'failed' : 'unknown'
);

// ---------------------------------------------------------------- Not available

export interface Unavailable {
  readonly lead: string;
  /** The Why? sheet, in plain words; `code` in backticks. */
  readonly why: string;
}

/** Screen 4 words for each link that shows no chat. */
export function unavailable(link: string, reason: string | undefined): Unavailable {
  if (link === 'off') {
    return {
      lead: 'Chat is off on this computer. The terminal has everything.',
      why: 'Chat is off on this computer. Turn it on at the computer with `pterm chat on`. Only the computer itself can turn Chat on; any phone can turn it off.',
    };
  }
  if (link === 'unsupported') {
    return {
      lead: 'Termivoa can’t read this agent’s conversation, so it shows nothing rather than a guess. The terminal has everything.',
      why: 'Chat works today with Claude Code on macOS and Linux. This session runs another agent, or no agent Termivoa can read.',
    };
  }
  if (link === 'searching') {
    return {
      lead: 'Looking for this agent’s conversation…',
      why: 'The agent runs, but its conversation file is not there yet. It appears after the agent writes its first message.',
    };
  }
  if (link === 'failed') {
    return {
      lead: 'Chat could not load. The terminal has everything.',
      why: 'The computer did not answer the chat request. Check the connection and try again.',
    };
  }
  switch (reason) {
    case 'cleared':
      return {
        lead: 'The agent started a new conversation, and Termivoa can’t tell which file belongs to it, so it shows nothing rather than a guess.',
        why: 'After `/clear` the agent writes a new conversation file. Termivoa links a file only when it is sure. Use the terminal for this conversation.',
      };
    case 'several_agents':
      return {
        lead: 'More than one agent runs in this session, so Termivoa can’t tell whose conversation to show.',
        why: 'Chat links one agent per session. Run one agent per session to use Chat.',
      };
    case 'custom_root':
      return {
        lead: 'This agent keeps its conversations in a moved folder, which Chat does not read yet.',
        why: 'The agent uses a custom folder (for example `CLAUDE_CONFIG_DIR`). Chat reads only the agent’s usual folder in this version.',
      };
    case 'no_file':
      return {
        lead: 'Termivoa can’t find exactly one conversation file for this agent, so it shows nothing rather than a guess.',
        why: 'Chat needs exactly one conversation file that matches the running agent. None matched, or more than one did.',
      };
    case 'format_unknown':
      return {
        lead: 'This agent’s conversation file is in a format Termivoa does not read yet.',
        why: 'The agent may be a newer version. Update Termivoa on the computer.',
      };
    case 'file_refused':
      return {
        lead: 'The conversation file failed a safety check, so Termivoa did not read it.',
        why: 'Termivoa reads a conversation only from a plain file that belongs to you and that others cannot change. This file is a link, has more than one name, or has other owners or permissions.',
      };
    case 'busy':
      return {
        lead: 'This computer already follows the most chats it allows.',
        why: 'A computer follows at most 8 chats at a time. Close Chat on another session, then try again.',
      };
    default:
      return {
        lead: 'Termivoa can’t tell which conversation belongs to this terminal, so it shows nothing rather than a guess. The terminal has everything.',
        why: 'Chat shows a conversation only when it is sure which file belongs to this session.',
      };
  }
}
