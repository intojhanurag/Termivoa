export const MAX_PASTE_BYTES = 64 * 1024;

/**
 * Composer drafts live only in memory and are keyed by computer, session and
 * generation, so a replacement session never inherits text meant for a lost
 * one, and the same session id on two computers never shares a draft.
 */
const drafts = new Map<string, string>();

export function draftKey(session: {
  readonly id: string;
  readonly generation: string;
  readonly computer: { readonly host: string };
}): string {
  return `${session.computer.host}/${session.id}/${session.generation}`;
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

/** Decides whether "Send" may paste the draft now. Never sends by itself. */
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
