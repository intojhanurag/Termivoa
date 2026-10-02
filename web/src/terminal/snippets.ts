import type { KeyName, Modifiers } from '../generated/protocol';
import { checkPaste, MAX_PASTE_BYTES, type PasteCheck } from './composer';
import { NO_MODIFIERS } from './InputController';
import { storage, type StorageLike } from '../storage';

/** Saved text the user sends often. Plain user text, not a secret. */
export interface Snippet {
  readonly id: string;
  readonly title: string;
  readonly text: string;
  /** Press Enter after pasting. */
  readonly sendEnter: boolean;
  /** Display only: marks a shell command rather than an agent prompt. */
  readonly shell: boolean;
}

export const SNIPPETS_KEY = 'pterm.snippets';
export const MAX_SNIPPETS = 50;
export const MAX_SNIPPET_TITLE = 80;
// Same characters the broker refuses in a paste; tab and line breaks are fine.
const PASTE_CONTROLS = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/u;

export const DEFAULT_SNIPPETS: ReadonlyArray<Snippet> = [
  { id: 'default-tests', title: 'Run tests and fix failures', text: 'Run the tests and fix any failures', sendEnter: true, shell: false },
  { id: 'default-explain', title: 'Explain the last error', text: 'Explain the last error in plain words', sendEnter: true, shell: false },
  { id: 'default-git-status', title: 'git status', text: 'git status', sendEnter: true, shell: true },
];

/** Why a snippet cannot be saved, or undefined when it is valid. */
export function snippetProblem(title: string, text: string): string | undefined {
  const trimmed = title.trim();
  if (trimmed === '') return 'Enter a title.';
  if ([...trimmed].length > MAX_SNIPPET_TITLE) return `Use a shorter title (${MAX_SNIPPET_TITLE} characters at most).`;
  if (text === '') return 'Enter the text to send.';
  if (new TextEncoder().encode(text).byteLength > MAX_PASTE_BYTES) return 'This text is larger than 64 KiB.';
  if (PASTE_CONTROLS.test(text) || PASTE_CONTROLS.test(trimmed)) return 'Remove control characters.';
  return undefined;
}

function isSnippet(value: unknown): value is Snippet {
  if (typeof value !== 'object' || value === null) return false;
  const item = value as Record<string, unknown>;
  return typeof item.id === 'string' && typeof item.title === 'string' && typeof item.text === 'string'
    && typeof item.sendEnter === 'boolean' && typeof item.shell === 'boolean'
    && item.title === item.title.trim() && snippetProblem(item.title, item.text) === undefined;
}

/** Stored snippets; the defaults until the user saves a list. Invalid entries are dropped. */
export function loadSnippets(store: StorageLike | undefined = storage()): Snippet[] {
  let raw: string | null = null;
  try {
    raw = store?.getItem(SNIPPETS_KEY) ?? null;
  } catch {
    // Storage can be refused (private browsing); fall back to the defaults.
  }
  if (raw === null) return [...DEFAULT_SNIPPETS];
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...DEFAULT_SNIPPETS];
    // Edit and delete work by id, so a repeated id keeps only its first entry.
    const ids = new Set<string>();
    return parsed.filter(isSnippet).filter((item) => !ids.has(item.id) && ids.add(item.id) !== undefined)
      .slice(0, MAX_SNIPPETS);
  } catch {
    return [...DEFAULT_SNIPPETS];
  }
}

/** Saves the list. Returns a problem when it breaks a bound; nothing is written then. */
export function saveSnippets(list: ReadonlyArray<Snippet>, store: StorageLike | undefined = storage()): string | undefined {
  if (list.length > MAX_SNIPPETS) return `You can keep ${MAX_SNIPPETS} snippets at most.`;
  for (const item of list) {
    const problem = snippetProblem(item.title, item.text) ?? (isSnippet(item) ? undefined : 'This snippet is not valid.');
    if (problem !== undefined) return problem;
  }
  try {
    store?.setItem(SNIPPETS_KEY, JSON.stringify(list));
  } catch {
    return 'This browser did not save the snippets.';
  }
  return undefined;
}

export interface SnippetTarget {
  sendPaste(text: string): boolean;
  sendKey(key: KeyName, modifiers: Modifiers): boolean;
}

/**
 * Sends a snippet as a paste, then Enter when the snippet asks for it.
 * Multi-line text needs the same explicit confirmation as the composer.
 */
export function sendSnippet(
  target: SnippetTarget,
  snippet: Snippet,
  canSend: boolean,
  confirmed: boolean,
): PasteCheck | 'sent' | 'failed' {
  const check = checkPaste(snippet.text, canSend, confirmed);
  if (check !== 'ready') return check;
  if (!target.sendPaste(snippet.text)) return 'failed';
  if (snippet.sendEnter && !target.sendKey('enter', NO_MODIFIERS)) return 'failed';
  return 'sent';
}
