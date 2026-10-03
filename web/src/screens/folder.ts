import type { BrokerRPCSessionRecord } from '../generated/protocol';

/**
 * Last path segment of an absolute path, POSIX or Windows. Handles trailing
 * separators and roots: "/" stays "/", a Windows drive root like "C:\" stays
 * "C:\", and a UNC path with a share ("\\server\share") returns the share.
 */
export function folderName(cwd: string): string {
  if (cwd === '' || cwd === '/' || /^[A-Za-z]:[\\/]$/u.test(cwd)) {
    return cwd;
  }
  const trimmed = cwd.replace(/[\\/]+$/u, '');
  if (trimmed === '') {
    return cwd.slice(0, 1);
  }
  const lastSeparator = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
  return lastSeparator === -1 ? trimmed : trimmed.slice(lastSeparator + 1);
}

// ponytail: pattern-matches common home prefixes only; the phone has no way to
// ask the host what its actual home dir is, so another user's home (e.g.
// /Users/someoneelse) also collapses to ~. Upgrade if the host ever reports it.
const HOME_PATTERNS = [
  /^\/Users\/[^/]+/u, // macOS
  /^\/home\/[^/]+/u, // Linux
  /^\/root(?=\/|$)/u,
  /^[A-Za-z]:\\users\\[^\\]+/ui, // Windows, case-insensitive drive letter and "Users"
];

function shortenHome(path: string): string {
  for (const pattern of HOME_PATTERNS) {
    const match = pattern.exec(path);
    if (match !== null) {
      return `~${path.slice(match[0].length)}`;
    }
  }
  return path;
}

/** Keeps the tail: "…" plus the last maxChars - 1 characters. Unchanged if it already fits. */
function leftEllipsis(text: string, maxChars: number): string {
  const characters = Array.from(text); // Code points, so an emoji is never split.
  return characters.length <= maxChars ? text : `…${characters.slice(characters.length - (maxChars - 1)).join('')}`;
}

/** The live folder for the status line: home shortened to "~", ellipsized from the left. */
export function displayPath(cwd: string, maxChars = 48): string {
  return leftEllipsis(shortenHome(cwd), maxChars);
}

/**
 * Splits a display path before its last folder, so the status line can cut the
 * parent first. dir + name === path. A one-character parent ("/var") is not
 * worth cutting and stays with the name.
 */
export function splitPath(path: string): { readonly dir: string; readonly name: string } {
  const match = /^(.+[\\/])([^\\/]+[\\/]*)$/u.exec(path);
  return match === null ? { dir: '', name: path } : { dir: match[1] ?? '', name: match[2] ?? '' };
}

/**
 * The name to show for a session. A user-set title always wins; otherwise the
 * live folder's basename stands in for the default project-name title.
 * liveCwd is the current tab's live session.state cwd, which is fresher than
 * the session record's cwd; pass it only for the tab actually being viewed.
 */
export function sessionName(record: BrokerRPCSessionRecord, liveCwd?: string): string {
  if (record.titleIsCustom === true) {
    return record.title;
  }
  const cwd = liveCwd ?? record.cwd;
  return cwd === undefined || cwd === '' ? record.title : folderName(cwd);
}

/** "0 B", "900 B", "1.5 KB", "12 KB", "3.4 MB": 1024-based, like ls -h. */
export function formatSize(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value < 10 ? value.toFixed(1).replace(/\.0$/u, '') : Math.round(value)} ${units[unit]}`;
}
