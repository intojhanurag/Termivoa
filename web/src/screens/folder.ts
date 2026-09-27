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
