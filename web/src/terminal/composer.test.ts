import { describe, expect, it } from 'vitest';
import { checkPaste, draftKey, lineCount, readDraft, saveDraft } from './composer';

const id = '00000000-0000-4000-8000-000000000001';
const on = (generation: string, host = 'mac.tailx.ts.net') => ({ id, generation, computer: { host } });

describe('composer rules', () => {
  it('requires explicit confirmation before multiline paste', () => {
    expect(checkPaste('ls -la', true, false)).toBe('ready');
    expect(checkPaste('echo one\necho two', true, false)).toBe('needs-confirmation');
    expect(checkPaste('echo one\r\necho two', true, false)).toBe('needs-confirmation');
    expect(checkPaste('echo one\necho two', true, true)).toBe('ready');
    expect(lineCount('a\nb\r\nc')).toBe(3);
  });

  it('disables sending while disconnected and keeps the draft', () => {
    const key = draftKey(on('00000000-0000-4000-8000-000000000002'));
    saveDraft(key, 'git status');
    expect(checkPaste(readDraft(key), false, true)).toBe('disabled');
    expect(readDraft(key)).toBe('git status');
  });

  it('ties drafts to a session generation', () => {
    const first = draftKey(on('00000000-0000-4000-8000-000000000003'));
    const replacement = draftKey(on('00000000-0000-4000-8000-000000000004'));
    saveDraft(first, 'make test');
    expect(readDraft(replacement)).toBe('');
    saveDraft(first, '');
    expect(readDraft(first)).toBe('');
  });

  it('keys drafts by computer, so the same session id on two computers never shares one', () => {
    const generation = '00000000-0000-4000-8000-000000000005';
    const mac = draftKey(on(generation));
    const vps = draftKey(on(generation, 'vps.tailx.ts.net'));
    expect(mac).toBe(`mac.tailx.ts.net/${id}/${generation}`);
    saveDraft(mac, 'make test');
    expect(readDraft(vps)).toBe('');
    saveDraft(mac, '');
  });

  it('rejects empty and oversized paste locally', () => {
    expect(checkPaste('', true, false)).toBe('empty');
    expect(checkPaste('x'.repeat(64 * 1024 + 1), true, true)).toBe('too-large');
  });
});
