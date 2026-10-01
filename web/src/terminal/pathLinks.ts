/** A path-like token in terminal text, by UTF-16 offsets into that text. */
export interface PathLink {
  readonly start: number;
  /** Exclusive; covers the ":line:column" suffix too. */
  readonly end: number;
  readonly path: string;
  readonly line?: number;
  readonly column?: number;
}

/** File extensions that make a token without a slash a link, like "README.md". */
const EXTENSIONS = new Set([
  'c', 'cc', 'cfg', 'cjs', 'conf', 'cpp', 'cs', 'css', 'cts', 'dart', 'ex', 'exs', 'go', 'gradle', 'h', 'hpp', 'html',
  'ini', 'java', 'js', 'json', 'jsonc', 'jsx', 'kt', 'kts', 'less', 'lock', 'lua', 'md', 'mdx', 'mjs', 'mod', 'mts', 'php',
  'pl', 'proto', 'ps1', 'py', 'rb', 'rs', 'sass', 'scss', 'sh', 'sql', 'sum', 'svelte', 'svg', 'swift', 'toml', 'ts',
  'tsx', 'txt', 'vue', 'xml', 'yaml', 'yml', 'zig', 'zsh',
]);

/**
 * A run of path characters with an optional ":line" and ":line:column". It
 * never starts inside a word, a path or after ":" (so not inside a URL), and
 * may start with "./", "../", "/" or a Windows drive.
 */
const TOKEN = /(?<![\w.@+~:/\\-])((?:[A-Za-z]:[\\/]|\.{1,2}[\\/]|\/)?[\w.@+-]+(?:[\\/][\w.@+-]+)*)(?::(\d{1,7}))?(?::(\d{1,5}))?/gu;

/** A relative first folder named like a domain, as in "github.com/owner/repo". */
const DOMAIN = /^[\w-]+(?:\.[\w-]+)*\.[A-Za-z]{2,}$/u;

/**
 * Path-like tokens in one line of terminal text. Conservative: a token needs a
 * slash and a file name with an extension (or a "./", "../", "/" or drive
 * start), or, without a slash, a known extension. URLs are not links.
 */
export function findPathLinks(text: string): PathLink[] {
  const links: PathLink[] = [];
  for (const match of text.matchAll(TOKEN)) {
    // A sentence may end right after a path: "see src/app.ts."
    const path = (match[1] ?? '').replace(/\.+$/u, '');
    const whole = path === match[1];
    const parts = path.split(/[\\/]/u);
    const name = parts.at(-1) ?? '';
    const extension = /\.([A-Za-z0-9]{1,10})$/u.exec(name)?.[1];
    const anchored = /^(?:[A-Za-z]:[\\/]|\.{1,2}[\\/]|\/)/u.test(path);
    const link = parts.length > 1
      ? name !== '' && (anchored || (extension !== undefined && !DOMAIN.test(parts[0] ?? '')))
      : extension !== undefined && EXTENSIONS.has(extension.toLowerCase());
    if (!link) {
      continue;
    }
    const line = whole ? Number(match[2] ?? 0) : 0;
    const column = whole ? Number(match[3] ?? 0) : 0;
    links.push({
      start: match.index,
      end: match.index + (whole ? match[0].length : path.length),
      path,
      ...(line > 0 ? { line } : {}),
      ...(line > 0 && column > 0 ? { column } : {}),
    });
  }
  return links;
}

/** "/a/b/../c" as ["/", "a", "c"] style: the prefix ("/", "C:/" or "") and the clean segments; undefined above the top. */
function normalize(path: string): { readonly prefix: string; readonly parts: string[] } | undefined {
  const slashed = path.replace(/\\/gu, '/');
  const prefix = /^[A-Za-z]:\//u.exec(slashed)?.[0] ?? (slashed.startsWith('/') ? '/' : '');
  const parts: string[] = [];
  for (const part of slashed.slice(prefix.length).split('/')) {
    if (part === '..') {
      if (parts.pop() === undefined) return undefined;
    } else if (part !== '' && part !== '.') {
      parts.push(part);
    }
  }
  return { prefix, parts };
}

/**
 * The path relative to the files root, with "/" separators, for a token the
 * terminal shows. A relative token is relative to the session's cwd. Returns
 * undefined when it is outside the root, is the root itself, or cannot be
 * resolved. Windows roots ("C:/Users/me/app") compare without case.
 */
export function rootRelative(root: string, cwd: string | undefined, token: string): string | undefined {
  const absolute = /^(?:[A-Za-z]:)?[\\/]/u.test(token);
  if (!absolute && cwd === undefined) {
    return undefined;
  }
  const full = normalize(absolute ? token : `${cwd}/${token}`);
  const top = normalize(root);
  if (full === undefined || top === undefined || full.prefix === '' || full.parts.length <= top.parts.length) {
    return undefined;
  }
  const windows = top.prefix.length > 1;
  const same = (a: string, b: string) => (windows ? a.toLowerCase() === b.toLowerCase() : a === b);
  if (!same(full.prefix, top.prefix) || top.parts.some((part, index) => !same(part, full.parts[index] ?? ''))) {
    return undefined;
  }
  return full.parts.slice(top.parts.length).join('/');
}

/** One xterm buffer cell, as far as link ranges need it. */
export interface Cell {
  getChars(): string;
  getWidth(): number;
}

/**
 * A row's text and, for each UTF-16 offset in it, the 0-based cell column it
 * starts in, plus one entry past the end. Wide characters take two columns,
 * so offsets and columns differ after them.
 */
export function rowCells(cells: ReadonlyArray<Cell>): { readonly text: string; readonly columns: number[] } {
  let text = '';
  const columns: number[] = [];
  let column = 0;
  for (const [x, cell] of cells.entries()) {
    const width = cell.getWidth();
    if (width === 0) {
      continue;
    }
    const chars = cell.getChars() || ' ';
    for (let unit = 0; unit < chars.length; unit++) {
      columns.push(x);
    }
    text += chars;
    column = x + width;
  }
  columns.push(column);
  return { text, columns };
}
