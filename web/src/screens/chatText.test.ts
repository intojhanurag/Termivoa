import { describe, expect, it } from 'vitest';
import type { ChatBlock } from '../generated/protocol';
import { chatItems, cleanText, parseInline, parseMarkdown, safeLink, showHidden, taskWord, toolRow, unavailable } from './chatText';

const own = 'https://mac.tailx.ts.net';

describe('chat text: hidden characters (S3)', () => {
  it('removes bidi controls and zero-width characters from prose, and shows them in paths and summaries', () => {
    const faked = 'src/‮gnp.exe​';
    expect(cleanText(faked)).toBe('src/gnp.exe');
    expect(showHidden(faked)).toBe('src/�gnp.exe�');
    for (const code of [0x200B, 0x200F, 0x202A, 0x202E, 0x2066, 0x2069]) {
      expect(cleanText(`a${String.fromCodePoint(code)}b`)).toBe('ab');
    }
    expect(cleanText('plain café ✓')).toBe('plain café ✓');
  });
});

describe('chat text: links (S3)', () => {
  it('accepts only http and https addresses of another site, without a user name', () => {
    expect(safeLink('https://example.com/a?b=1', own)).toBe('https://example.com/a?b=1');
    expect(safeLink('http://example.com', own)).toBe('http://example.com/');
    expect(safeLink('javascript:alert(1)', own)).toBeUndefined();
    expect(safeLink('data:text/html,<b>x</b>', own)).toBeUndefined();
    expect(safeLink('file:///etc/passwd', own)).toBeUndefined();
    expect(safeLink('https://user:pw@example.com', own)).toBeUndefined();
    // A faked add-computer or pairing link to this app stays text.
    expect(safeLink(`${own}/#add=abc`, own)).toBeUndefined();
    expect(safeLink('not a url', own)).toBeUndefined();
  });

  it('turns [text](url) and bare addresses into links, and anything else into text', () => {
    expect(parseInline('see [docs](https://example.com/d) or https://example.org/x.')).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'link', text: 'docs', url: 'https://example.com/d' },
      { kind: 'text', text: ' or ' },
      { kind: 'link', text: 'https://example.org/x', url: 'https://example.org/x' },
      { kind: 'text', text: '.' },
    ]);
    expect(parseInline('[click](javascript:alert(1))').some((item) => item.kind === 'link')).toBe(false);
  });
});

describe('chat text: markdown', () => {
  it('keeps raw HTML as text and shows no image, only its alt text', () => {
    const blocks = parseMarkdown('<img src=x onerror=alert(1)> ![a diagram](https://example.com/i.png) <script>x</script>');
    expect(blocks).toEqual([{ kind: 'p', children: [{ kind: 'text', text: '<img src=x onerror=alert(1)> a diagram <script>x</script>' }] }]);
  });

  it('reads paragraphs, headings, lists, quotes, code blocks and inline styles', () => {
    const blocks = parseMarkdown('# Plan\n\nFirst **bold** and *soft* and `code`.\nSame paragraph.\n\n- one\n- two\n1. first\n> quoted\n```ts\nconst a = 1;\n```');
    expect(blocks.map((block) => block.kind)).toEqual(['h', 'p', 'list', 'list', 'quote', 'pre']);
    expect(blocks[1]).toEqual({
      kind: 'p',
      children: [
        { kind: 'text', text: 'First ' },
        { kind: 'strong', children: [{ kind: 'text', text: 'bold' }] },
        { kind: 'text', text: ' and ' },
        { kind: 'em', children: [{ kind: 'text', text: 'soft' }] },
        { kind: 'text', text: ' and ' },
        { kind: 'code', text: 'code' },
        { kind: 'text', text: '. Same paragraph.' },
      ],
    });
    expect(blocks[2]).toMatchObject({ kind: 'list', ordered: false, items: [[{ text: 'one' }], [{ text: 'two' }]] });
    expect(blocks[3]).toMatchObject({ kind: 'list', ordered: true });
    expect(blocks[5]).toEqual({ kind: 'pre', text: 'const a = 1;' });
  });

  it('keeps an unclosed code block to the end and snake_case names as text', () => {
    expect(parseMarkdown('```\nopen')).toEqual([{ kind: 'pre', text: 'open' }]);
    expect(parseInline('rate_for_region')).toEqual([{ kind: 'text', text: 'rate_for_region' }]);
  });
});

const tool = (id: string, name: string, status = 'ok', summary = 'src/a.ts'): ChatBlock => ({ id, kind: 'tool_call', tool: { name, status, summary } });

describe('chat list rows', () => {
  it('groups tool and edit rows in one box, folds Task alone, and names the agent once per turn', () => {
    const items = chatItems([
      { id: 'u1', kind: 'user_text', text: 'go' },
      { id: 'a1', kind: 'assistant_text', text: 'ok' },
      tool('t1', 'Read'),
      { id: 'e1', kind: 'edit', edit: { path: 'src/tax.ts', added: 18, removed: 7 } },
      tool('t2', 'Task', 'running', 'Find per-line rounding'),
      tool('t3', 'Bash'),
      { id: 'c1', kind: 'compaction' },
      { id: 'u2', kind: 'user_text', text: 'next' },
      tool('t4', 'Bash', 'running'),
    ]);
    expect(items.map((item) => `${item.key}:${item.kind}:${item.head}`)).toEqual([
      'u1:block:false', 'a1:block:true', 't1:tools:false', 't2:block:false', 't3:tools:false', 'c1:block:false', 'u2:block:false', 't4:tools:true',
    ]);
    const box = items[2];
    expect(box?.kind === 'tools' ? box.blocks.map((block) => block.id) : []).toEqual(['t1', 'e1']);
  });

  it('words each tool row as the screens do: Read, Edit, Ran or Run, and the state', () => {
    expect(toolRow(tool('t', 'Read'))).toEqual({ verb: 'Read', what: 'src/a.ts', word: undefined, tone: 'green' });
    expect(toolRow(tool('t', 'Bash', 'running', 'npm test'))).toEqual({ verb: 'Run', what: 'npm test', word: 'running', tone: 'accent' });
    expect(toolRow(tool('t', 'Bash', 'ok', 'npm test')).verb).toBe('Ran');
    expect(toolRow(tool('t', 'Grep', 'error')).word).toBe('failed');
    expect(toolRow({ id: 'e', kind: 'edit', edit: { path: 'a‮b', added: 1, removed: 0 } })).toMatchObject({ verb: 'Edit', what: 'a�b', tone: 'green' });
    expect([taskWord('ok'), taskWord('running'), taskWord('error'), taskWord(undefined)]).toEqual(['done', 'running', 'failed', 'unknown']);
  });
});

describe('not available (screen 4)', () => {
  it('has plain words for every link and reason, and says how to turn Chat on for off', () => {
    expect(unavailable('off', undefined).why).toContain('Turn it on at the computer with `pterm chat on`.');
    for (const reason of ['cleared', 'several_agents', 'custom_root', 'no_file', 'format_unknown', 'file_refused', 'busy', undefined]) {
      const words = unavailable('none', reason);
      expect(words.lead.length).toBeGreaterThan(20);
      expect(words.why.length).toBeGreaterThan(20);
    }
    expect(new Set(['cleared', 'several_agents', 'custom_root', 'no_file', 'format_unknown', 'file_refused', 'busy'].map((reason) => unavailable('none', reason).why)).size).toBe(7);
    expect(unavailable('unsupported', undefined).why).toContain('Claude Code');
  });
});
