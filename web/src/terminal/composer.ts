export const MAX_PASTE_BYTES = 64 * 1024;

/**
 * Composer drafts live only in memory and are keyed by session generation, so
 * a replacement session never inherits text meant for a lost one.
 */
const drafts = new Map<string, string>();

export function draftKey(sessionId: string, generation: string): string {
  return `${sessionId}/${generation}`;
}

export function readDraft(key: string): string {
  return drafts.get(key) ?? '';
}

export function saveDraft(key: string, text: string): void {
  if (text === '') {
    drafts.delete(key);
  } else {
    drafts.set(key, text);
  }
}

export type PasteCheck = 'disabled' | 'empty' | 'too-large' | 'needs-confirmation' | 'ready';

/** Decides whether "Paste into terminal" may send now. Never sends by itself. */
export function checkPaste(text: string, canSend: boolean, confirmed: boolean): PasteCheck {
  if (!canSend) {
    return 'disabled';
  }
  if (text === '') {
    return 'empty';
  }
  if (new TextEncoder().encode(text).byteLength > MAX_PASTE_BYTES) {
    return 'too-large';
  }
  if (/[\r\n]/u.test(text) && !confirmed) {
    return 'needs-confirmation';
  }
  return 'ready';
}

export function lineCount(text: string): number {
  return text.split(/\r\n|\r|\n/u).length;
}
