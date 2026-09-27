import { describe, expect, it } from 'vitest';
import {
  DEFAULT_SNIPPETS,
  loadSnippets,
  MAX_SNIPPETS,
  saveSnippets,
  sendSnippet,
  snippetProblem,
  SNIPPETS_KEY,
  type Snippet,
} from './snippets';

function memory(initial?: string) {
  const values = new Map<string, string>(initial === undefined ? [] : [[SNIPPETS_KEY, initial]]);
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value); },
    values,
  };
}

const snippet = (extra: Partial<Snippet> = {}): Snippet => ({
  id: 'a', title: 'Status', text: 'git status', sendEnter: true, shell: true, ...extra,
});

function target(ok = true) {
  const calls: Array<[string, unknown]> = [];
  return {
    calls,
    sendPaste: (text: string) => { calls.push(['paste', text]); return ok; },
    sendKey: (key: string) => { calls.push(['key', key]); return ok; },
  };
}

describe('snippet storage', () => {
  it('starts with three generic defaults, git status as a shell command with Enter', () => {
    expect(loadSnippets(memory())).toEqual(DEFAULT_SNIPPETS);
    expect(DEFAULT_SNIPPETS.map((item) => item.text)).toEqual([
      'Run the tests and fix any failures', 'Explain the last error in plain words', 'git status',
    ]);
    expect(DEFAULT_SNIPPETS[2]).toMatchObject({ shell: true, sendEnter: true });
  });

  it('saves, reloads, and keeps an empty list empty', () => {
    const store = memory();
    expect(saveSnippets([snippet()], store)).toBeUndefined();
    expect(loadSnippets(store)).toEqual([snippet()]);
    expect(saveSnippets([], store)).toBeUndefined();
    expect(loadSnippets(store)).toEqual([]);
  });

  it('enforces title, text and count bounds', () => {
    expect(snippetProblem('', 'x')).toBe('Enter a title.');
    expect(snippetProblem('t'.repeat(80), 'x')).toBeUndefined();
    expect(snippetProblem('t'.repeat(81), 'x')).toMatch(/shorter title/u);
    expect(snippetProblem('t', '')).toBe('Enter the text to send.');
    expect(snippetProblem('t', 'x'.repeat(64 * 1024))).toBeUndefined();
    expect(snippetProblem('t', 'x'.repeat(64 * 1024 + 1))).toMatch(/64 KiB/u);
    expect(snippetProblem('t', 'a\u0007b')).toMatch(/control/u);
    expect(snippetProblem('t', 'line one\n\tline two')).toBeUndefined();

    const store = memory();
    const many = Array.from({ length: MAX_SNIPPETS + 1 }, (_, index) => snippet({ id: String(index) }));
    expect(saveSnippets(many, store)).toMatch(/50 snippets/u);
    expect(saveSnippets([snippet({ title: 'x'.repeat(81) })], store)).toMatch(/shorter title/u);
    expect(store.values.size).toBe(0);
  });

  it('drops tampered or oversized stored entries and caps the count', () => {
    const stored = [snippet(), { id: 'b', title: 5 }, snippet({ id: 'c', text: 'x'.repeat(70_000) })];
    expect(loadSnippets(memory(JSON.stringify(stored)))).toEqual([snippet()]);
    expect(loadSnippets(memory('not json'))).toEqual(DEFAULT_SNIPPETS);
    const many = Array.from({ length: 60 }, (_, index) => snippet({ id: String(index) }));
    expect(loadSnippets(memory(JSON.stringify(many)))).toHaveLength(MAX_SNIPPETS);
  });

  it('keeps only the first entry of a repeated stored id, so editing one never rewrites another', () => {
    const stored = [snippet(), snippet({ title: 'Other', text: 'rm -rf build' }), snippet({ id: 'b' })];
    expect(loadSnippets(memory(JSON.stringify(stored)))).toEqual([snippet(), snippet({ id: 'b' })]);
  });
});

describe('sending a snippet', () => {
  it('pastes, then presses Enter only when asked', () => {
    const withEnter = target();
    expect(sendSnippet(withEnter, snippet(), true, false)).toBe('sent');
    expect(withEnter.calls).toEqual([['paste', 'git status'], ['key', 'enter']]);

    const pasteOnly = target();
    expect(sendSnippet(pasteOnly, snippet({ sendEnter: false }), true, false)).toBe('sent');
    expect(pasteOnly.calls).toEqual([['paste', 'git status']]);
  });

  it('asks before multi-line text, like the composer', () => {
    const multi = snippet({ text: 'make\nmake install' });
    const sink = target();
    expect(sendSnippet(sink, multi, true, false)).toBe('needs-confirmation');
    expect(sink.calls).toEqual([]);
    expect(sendSnippet(sink, multi, true, true)).toBe('sent');
    expect(sink.calls).toEqual([['paste', 'make\nmake install'], ['key', 'enter']]);
  });

  it('sends nothing without control and no Enter after a failed paste', () => {
    const sink = target();
    expect(sendSnippet(sink, snippet(), false, true)).toBe('disabled');
    expect(sink.calls).toEqual([]);
    const failing = target(false);
    expect(sendSnippet(failing, snippet(), true, false)).toBe('failed');
    expect(failing.calls).toEqual([['paste', 'git status']]);
  });
});
