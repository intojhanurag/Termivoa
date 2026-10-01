import { describe, expect, it } from 'vitest';
import { findPathLinks, rootRelative, rowCells, rowLinks, tapTarget, type Cell } from './pathLinks';

describe('findPathLinks', () => {
  it('finds a relative path, with and without "./"', () => {
    expect(findPathLinks('src/app.ts')).toEqual([{ start: 0, end: 10, path: 'src/app.ts' }]);
    expect(findPathLinks('./src/app.ts')).toEqual([{ start: 0, end: 12, path: './src/app.ts' }]);
  });

  it('reads ":line" and ":line:column", and the end covers the suffix', () => {
    expect(findPathLinks('src/app.ts:42')).toEqual([{ start: 0, end: 13, path: 'src/app.ts', line: 42 }]);
    expect(findPathLinks('at src/app.ts:42:7 now')).toEqual([{ start: 3, end: 18, path: 'src/app.ts', line: 42, column: 7 }]);
  });

  it('finds an absolute path and a bare file with a known extension', () => {
    expect(findPathLinks('/abs/path/in/root/file.go:10')).toEqual([{ start: 0, end: 28, path: '/abs/path/in/root/file.go', line: 10 }]);
    expect(findPathLinks('open README.md')).toEqual([{ start: 5, end: 14, path: 'README.md' }]);
  });

  it('does not take a bare file with an unknown extension', () => {
    expect(findPathLinks('data.xyz')).toEqual([]);
  });

  it('strips a trailing sentence dot', () => {
    expect(findPathLinks('see src/app.ts.')).toEqual([{ start: 4, end: 14, path: 'src/app.ts' }]);
  });

  it('finds tokens inside parentheses and backticks', () => {
    expect(findPathLinks('(src/app.ts)')).toEqual([{ start: 1, end: 11, path: 'src/app.ts' }]);
    expect(findPathLinks('(src/app.ts:3)')).toEqual([{ start: 1, end: 13, path: 'src/app.ts', line: 3 }]);
    expect(findPathLinks('`src/app.ts:4:2`')).toEqual([{ start: 1, end: 15, path: 'src/app.ts', line: 4, column: 2 }]);
  });

  it('finds several tokens in one line', () => {
    expect(findPathLinks('a/b.ts and c/d.go:5').map((link) => link.path)).toEqual(['a/b.ts', 'c/d.go']);
  });

  it('does not link what only looks like a path', () => {
    for (const text of [
      'https://github.com/a/b.ts',
      'github.com/owner/repo',
      'and/or',
      '2024/01/02',
      'e.g.',
      'v1.2.3',
      '~/x.ts',
      'me@host.com',
      '@types/react',
    ]) {
      expect(findPathLinks(text), text).toEqual([]);
    }
  });

  it('finds Windows paths with a drive and with backslashes', () => {
    expect(findPathLinks('C:\\Users\\me\\app\\src\\a.ts:3')).toEqual([
      { start: 0, end: 26, path: 'C:\\Users\\me\\app\\src\\a.ts', line: 3 },
    ]);
    expect(findPathLinks('src\\a.ts')).toEqual([{ start: 0, end: 8, path: 'src\\a.ts' }]);
  });
});

describe('rootRelative', () => {
  const root = '/Users/me/app';
  const cwd = '/Users/me/app/web';

  it('resolves a relative token against the cwd', () => {
    expect(rootRelative(root, cwd, 'src/a.ts')).toBe('web/src/a.ts');
    expect(rootRelative(root, cwd, './src/a.ts')).toBe('web/src/a.ts');
    expect(rootRelative(root, cwd, '../README.md')).toBe('README.md');
    expect(rootRelative(root, cwd, './x/../y.ts')).toBe('web/y.ts');
  });

  it('takes an absolute token inside the root', () => {
    expect(rootRelative(root, cwd, '/Users/me/app/src/a.ts')).toBe('src/a.ts');
  });

  it('is undefined outside the root, at the root and for a shared prefix', () => {
    expect(rootRelative(root, cwd, '/Users/me/other/a.ts')).toBeUndefined();
    expect(rootRelative(root, cwd, '../../other/x.ts')).toBeUndefined();
    expect(rootRelative(root, cwd, '/Users/me/app')).toBeUndefined();
    expect(rootRelative(root, cwd, '..')).toBeUndefined();
    expect(rootRelative(root, cwd, '/Users/me/app2/x.ts')).toBeUndefined();
  });

  it('needs a cwd only for a relative token', () => {
    expect(rootRelative(root, undefined, 'src/a.ts')).toBeUndefined();
    expect(rootRelative(root, undefined, '/Users/me/app/src/a.ts')).toBe('src/a.ts');
  });

  it('is undefined for ".." above the top of the filesystem', () => {
    expect(rootRelative(root, '/a', '../../x.ts')).toBeUndefined();
    expect(rootRelative(root, cwd, '/../Users/me/app/x.ts')).toBeUndefined();
  });

  it('resolves Windows paths without case', () => {
    const windowsRoot = 'C:/Users/me/app';
    const windowsCwd = 'C:\\Users\\me\\app\\web';
    expect(rootRelative(windowsRoot, windowsCwd, 'src\\a.ts')).toBe('web/src/a.ts');
    expect(rootRelative(windowsRoot, windowsCwd, 'C:\\Users\\me\\app\\src\\a.ts')).toBe('src/a.ts');
    expect(rootRelative(windowsRoot, windowsCwd, 'c:\\users\\ME\\app\\x.ts')).toBe('x.ts');
    expect(rootRelative(windowsRoot, windowsCwd, 'D:\\Users\\me\\app\\x.ts')).toBeUndefined();
  });

  it('keeps the case of the path below a Windows root', () => {
    expect(rootRelative('C:/Users/me/app', undefined, 'c:\\USERS\\me\\APP\\Src\\A.ts')).toBe('Src/A.ts');
  });

  it('does not match a POSIX absolute token against a Windows root', () => {
    expect(rootRelative('C:/Users/me/app', 'C:\\Users\\me\\app', '/Users/me/app/x.ts')).toBeUndefined();
  });

  it('compares case on POSIX', () => {
    expect(rootRelative(root, cwd, '/users/me/app/x.ts')).toBeUndefined();
  });
});

describe('rowCells', () => {
  const cell = (chars: string, width = 1): Cell => ({ getChars: () => chars, getWidth: () => width });

  it('gives the text and one column per UTF-16 unit, plus the end', () => {
    expect(rowCells([cell('a'), cell('b')])).toEqual({ text: 'ab', columns: [0, 1, 2] });
  });

  it('turns an empty cell into a space', () => {
    expect(rowCells([cell('a'), cell(''), cell('b')])).toEqual({ text: 'a b', columns: [0, 1, 2, 3] });
  });

  it('skips the continuation cell after a wide character', () => {
    expect(rowCells([cell('a'), cell('日', 2), cell('', 0), cell('b')])).toEqual({ text: 'a日b', columns: [0, 1, 3, 4] });
  });

  it('gives both units of a surrogate pair the same column', () => {
    expect(rowCells([cell('a'), cell('😀', 2), cell('', 0), cell('b')])).toEqual({ text: 'a😀b', columns: [0, 1, 1, 3, 4] });
  });

  it('is empty for no cells', () => {
    expect(rowCells([])).toEqual({ text: '', columns: [0] });
  });
});

describe('findPathLinks, stricter names', () => {
  it('needs a letter in the extension and no leading dot', () => {
    for (const text of ['1/2.5', 'react/18.2', 'node_modules/.bin', 'see 3/4.0 here']) {
      expect(findPathLinks(text), text).toEqual([]);
    }
    expect(findPathLinks('./.env').map((link) => link.path)).toEqual(['./.env']);
  });

  it('takes letters beyond ASCII as part of one name', () => {
    expect(findPathLinks('docs/résumé.md')).toEqual([{ start: 0, end: 14, path: 'docs/résumé.md' }]);
  });

  it('takes a dotted folder that is not a lower-case domain', () => {
    expect(findPathLinks('MyApp.Api/Controllers/X.cs').map((link) => link.path)).toEqual(['MyApp.Api/Controllers/X.cs']);
    expect(findPathLinks('golang.org/x/tools.go')).toEqual([]);
  });

  it('reads tsc\'s "(line,column)"', () => {
    expect(findPathLinks('src/app.ts(10,5): error')).toEqual([{ start: 0, end: 16, path: 'src/app.ts', line: 10, column: 5 }]);
    expect(findPathLinks('src/app.ts(7)')).toEqual([{ start: 0, end: 13, path: 'src/app.ts', line: 7 }]);
  });
});

describe('tapTarget', () => {
  const link = findPathLinks('src/a.ts:9')[0]!;
  const root = '/Users/me/app';
  const cwd = '/Users/me/app/web';

  it('opens a path inside the root at its line', () => {
    expect(tapTarget(true, root, cwd, link)).toEqual({ path: 'web/src/a.ts', line: 9 });
  });

  it('leaves the tap alone when links are off, the root is not known, or the path is outside it', () => {
    expect(tapTarget(false, root, cwd, link)).toBeUndefined();
    expect(tapTarget(true, undefined, cwd, link)).toBeUndefined();
    expect(tapTarget(true, root, cwd, findPathLinks('/dev/null')[0]!)).toBeUndefined();
    expect(tapTarget(true, root, undefined, link)).toBeUndefined();
  });
});

describe('rowLinks', () => {
  const cells = (text: string): Cell[] => Array.from(text).flatMap((char) => {
    const wide = /[\u3000-\u9fff]/u.test(char);
    const cell = { getChars: () => char, getWidth: () => (wide ? 2 : 1) };
    return wide ? [cell, { getChars: () => '', getWidth: () => 0 }] : [cell];
  });

  it('gives 1-based cell ranges with an inclusive end', () => {
    expect(rowLinks(cells('at src/a.ts:3 x'), 4)).toEqual([
      { link: { start: 3, end: 13, path: 'src/a.ts', line: 3 }, text: 'src/a.ts:3', range: { start: { x: 4, y: 4 }, end: { x: 13, y: 4 } } },
    ]);
  });

  it('places a link after wide characters by cell, not by character', () => {
    const [found] = rowLinks(cells('漢字 src/w.ts'), 1);
    expect(found?.text).toBe('src/w.ts');
    expect(found?.range).toEqual({ start: { x: 6, y: 1 }, end: { x: 13, y: 1 } });
  });
});
