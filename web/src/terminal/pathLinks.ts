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
 * A run of path characters, then an optional ":line", ":line:column" or tsc's
 * "(line,column)". It never starts inside a word, a path or after ":" (so not
 * inside a URL), and may start with "./", "../", "/" or a Windows drive.
 */
const TOKEN = /(?<![\p{L}\p{N}_.@+~:/\\-])((?:[A-Za-z]:[\\/]|\.{1,2}[\\/]|\/)?[\p{L}\p{N}_.@+-]+(?:[\\/][\p{L}\p{N}_.@+-]+)*)(?::(\d{1,7})(?::(\d{1,5}))?|\((\d{1,7})(?:,(\d{1,5}))?\))?/gu;

/** A relative first folder named like a domain, as in "github.com/owner/repo"; domains are lower case. */
const DOMAIN = /^[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}$/u;

/** A file extension has a letter, so "2.5" and "18.2" are not file names. */
const EXTENSION = /\.([\p{L}\p{N}_]*\p{L}[\p{L}\p{N}_]*)$/u;

/**
 * Path-like tokens in one line of terminal text. Conservative: a token needs a
 * slash and a file name with an extension (or a "./", "../", "/" or drive
 * start), or, without a slash, a known extension. URLs are not links.
 */
export function findPathLinks(text: string): PathLink[] {
  const links: PathLink[] = [];
  const urls = findAddComputerLinks(text);
  for (const match of text.matchAll(TOKEN)) {
    // A part of an add-computer link (its "/add-computer" or an unencoded via) is not a path.
    if (urls.some((url) => match.index < url.end && url.start < match.index + match[0].length)) {
      continue;
    }
    // A sentence may end right after a path: "see src/app.ts."
    const path = (match[1] ?? '').replace(/\.+$/u, '');
    const whole = path === match[1];
    const parts = path.split(/[\\/]/u);
    const name = parts.at(-1) ?? '';
    // A dot file (".env", ".bin") has no extension.
    const extension = name.startsWith('.') ? undefined : EXTENSION.exec(name)?.[1];
    const anchored = /^(?:[A-Za-z]:[\\/]|\.{1,2}[\\/]|\/)/u.test(path);
    const link = parts.length > 1
      ? name !== '' && (anchored || (extension !== undefined && extension.length <= 10 && !DOMAIN.test(parts[0] ?? '')))
      : extension !== undefined && EXTENSIONS.has(extension.toLowerCase());
    if (!link) {
      continue;
    }
    const line = whole ? Number(match[2] ?? match[4] ?? 0) : 0;
    const column = whole ? Number(match[3] ?? match[5] ?? 0) : 0;
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

/**
 * Where a tap on a terminal link goes: the root-relative path and line, or
 * undefined when the tap is not for a link (links off, root not known yet,
 * outside the root), so it acts as a plain tap. Decided at once, inside the
 * tap, since iOS raises the keyboard only during the gesture.
 */
export function tapTarget(
  linksOn: boolean,
  root: string | undefined,
  cwd: string | undefined,
  link: PathLink,
): { readonly path: string; readonly line: number | undefined } | undefined {
  const path = linksOn && root !== undefined ? rootRelative(root, cwd, link.path) : undefined;
  return path === undefined ? undefined : { path, line: link.line };
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

/** The parts of an xterm Terminal that bufferRow reads. */
export interface BufferLike<C extends Cell = Cell> {
  readonly cols: number;
  readonly buffer: {
    readonly active: {
      getLine(y: number): { readonly length: number; getCell(x: number): C | undefined } | undefined;
    };
  };
}

/**
 * The cells of buffer row y (1-based), or undefined past the buffer. Only the
 * first `cols` cells: after a narrower resize a line keeps its old length,
 * and that blank tail would hide that the row is full (a wrapped link).
 */
export function bufferRow<C extends Cell>(terminal: BufferLike<C>, y: number): C[] | undefined {
  const row = terminal.buffer.active.getLine(y - 1);
  if (row === undefined) return undefined;
  const cells: C[] = [];
  for (let x = 0; x < Math.min(row.length, terminal.cols); x++) {
    const cell = row.getCell(x);
    if (cell !== undefined) cells.push(cell);
  }
  return cells;
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

/** A link in one buffer row, with its xterm range: 1-based cells, end inclusive. */
export interface RowLink {
  readonly link: PathLink;
  readonly text: string;
  readonly range: { readonly start: { readonly x: number; readonly y: number }; readonly end: { readonly x: number; readonly y: number } };
}

/** The path links in buffer row y (1-based), placed by cell, so wide characters before a link do not shift it. */
export function rowLinks(cells: ReadonlyArray<Cell>, y: number): RowLink[] {
  const { text, columns } = rowCells(cells);
  return findPathLinks(text).map((link) => ({
    link,
    text: text.slice(link.start, link.end),
    // The end column is where the next character starts, which is the last cell 1-based.
    range: { start: { x: (columns[link.start] ?? 0) + 1, y }, end: { x: columns[link.end] ?? 0, y } },
  }));
}

/**
 * An add-computer link candidate, as `pterm pair --via` prints it: https, a
 * lower-case host, "/add-computer#" and a fragment of URL-safe characters.
 * It never starts inside a word, and a fragment longer than 512 characters
 * is no link at all. The caller checks it with parseAddComputerLink.
 */
const ADD_COMPUTER = /(?<![\p{L}\p{N}])https:\/\/[a-z0-9][a-z0-9.-]{0,252}\/add-computer#[A-Za-z0-9\-_.~%=&+:/]{1,512}(?![A-Za-z0-9\-_.~%=&+:/])/gu;

/** Add-computer link candidates in terminal text, by UTF-16 offsets (end exclusive). A trailing sentence dot is not part of one. */
export function findAddComputerLinks(text: string): Array<{ start: number; end: number; url: string }> {
  const links: Array<{ start: number; end: number; url: string }> = [];
  for (const match of text.matchAll(ADD_COMPUTER)) {
    const url = match[0].replace(/[.,)'"`]+$/u, '');
    links.push({ start: match.index, end: match.index + url.length, url });
  }
  return links;
}

/** A cell range in the buffer: 1-based cells, end inclusive, as xterm takes it. */
export interface CellRange {
  readonly start: { readonly x: number; readonly y: number };
  readonly end: { readonly x: number; readonly y: number };
}

/** A row that output continues past: its last cell is not blank (or is the right half of a wide character). */
function full(cells: ReadonlyArray<Cell>): boolean {
  const last = cells.at(-1);
  return last !== undefined && (last.getWidth() === 0 || last.getChars().trim() !== '');
}

/**
 * The add-computer link candidates in consecutive buffer rows, the first at
 * row firstY (1-based). The broker sends frames without wrap marks, so a full
 * row joins the next into one line; a row that is not full ends the line. A
 * link may span rows; its range is placed by cell, like rowLinks.
 */
export function joinedRowLinks(
  rows: ReadonlyArray<ReadonlyArray<Cell>>,
  firstY: number,
): Array<{ readonly url: string; readonly range: CellRange }> {
  const found: Array<{ readonly url: string; readonly range: CellRange }> = [];
  let text = '';
  // For each UTF-16 offset in text: its 1-based row and its row's column map and offset.
  let places: Array<{ readonly y: number; readonly columns: number[]; readonly index: number }> = [];
  const flush = () => {
    for (const link of findAddComputerLinks(text)) {
      const first = places[link.start];
      const last = places[link.end - 1];
      if (first === undefined || last === undefined) continue;
      found.push({
        url: link.url,
        // The column after the last character is its last cell, 1-based.
        range: {
          start: { x: (first.columns[first.index] ?? 0) + 1, y: first.y },
          end: { x: last.columns[last.index + 1] ?? 0, y: last.y },
        },
      });
    }
    text = '';
    places = [];
  };
  for (const [index, cells] of rows.entries()) {
    const row = rowCells(cells);
    const y = firstY + index;
    for (let offset = 0; offset < row.text.length; offset++) {
      places.push({ y, columns: row.columns, index: offset });
    }
    text += row.text;
    if (!full(cells)) flush();
  }
  flush();
  return found;
}

/** Whether two cell ranges share a cell; a range that spans rows covers each row between in full. */
export function overlaps(a: CellRange, b: CellRange): boolean {
  const before = (p: { x: number; y: number }, q: { x: number; y: number }) => p.y < q.y || (p.y === q.y && p.x < q.x);
  return !before(a.end, b.start) && !before(b.end, a.start);
}
