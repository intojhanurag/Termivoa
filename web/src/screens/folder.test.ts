import { describe, expect, it } from 'vitest';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { displayPath, folderName, sessionName, splitPath } from './folder';

const base: BrokerRPCSessionRecord = {
  id: '84444444-4444-4444-8444-444444444444',
  generation: '85555555-5555-4555-8555-555555555555',
  title: 'Home',
  projectId: '81111111-1111-4111-8111-111111111111',
  shellProfileId: '82222222-2222-4222-8222-222222222222',
  state: 'running',
  columns: 80,
  rows: 24,
  createdAt: '2026-09-27T10:00:00Z',
  updatedAt: '2026-09-27T10:00:00Z',
};

describe('folderName', () => {
  it('takes the last segment of a POSIX path', () => {
    expect(folderName('/Users/me/src/app')).toBe('app');
    expect(folderName('/Users/me/src/app/')).toBe('app');
  });

  it('takes the last segment of a Windows path', () => {
    expect(folderName('C:\\Users\\me\\x')).toBe('x');
    expect(folderName('C:\\Users\\me\\x\\')).toBe('x');
  });

  it('keeps roots as themselves', () => {
    expect(folderName('/')).toBe('/');
    expect(folderName('C:\\')).toBe('C:\\');
  });

  it('returns the share name of a UNC path', () => {
    expect(folderName('\\\\server\\share')).toBe('share');
  });
});

describe('displayPath', () => {
  it('shortens the home dir on macOS, Linux, and Windows', () => {
    expect(displayPath('/Users/me/src/app')).toBe('~/src/app');
    expect(displayPath('/Users/me')).toBe('~');
    expect(displayPath('/home/me/src/app')).toBe('~/src/app');
    expect(displayPath('/root')).toBe('~');
    expect(displayPath('/root/src')).toBe('~/src');
    expect(displayPath('C:\\Users\\me\\x')).toBe('~\\x');
    expect(displayPath('c:\\USERS\\me\\x')).toBe('~\\x');
  });

  it('leaves non-home paths and other roots alone', () => {
    expect(displayPath('/var/www/app')).toBe('/var/www/app');
    expect(displayPath('D:\\Projects\\app')).toBe('D:\\Projects\\app');
    expect(displayPath('/rootless/app')).toBe('/rootless/app');
    expect(displayPath('/root-backup/app')).toBe('/root-backup/app');
  });

  it('ellipsizes from the left, keeping the tail, once over the budget', () => {
    expect(displayPath('/short/path', 48)).toBe('/short/path');
    const exact = `/${'a'.repeat(47)}`;
    expect(exact.length).toBe(48);
    expect(displayPath(exact, 48)).toBe(exact);
    const long = `/${'a'.repeat(60)}`;
    const result = displayPath(long, 48);
    expect(result.length).toBe(48);
    expect(result.startsWith('…')).toBe(true);
    expect(result.endsWith('a'.repeat(47))).toBe(true);
    // Counts code points: an emoji at the cut is kept whole.
    expect(displayPath(`/var/${'😀'.repeat(50)}`, 10)).toBe(`…${'😀'.repeat(9)}`);
  });
});

describe('splitPath', () => {
  it('splits before the last folder, POSIX and Windows', () => {
    expect(splitPath('~/code/billing-api/services/payments')).toEqual({ dir: '~/code/billing-api/services/', name: 'payments' });
    expect(splitPath('~\\code\\app')).toEqual({ dir: '~\\code\\', name: 'app' });
    expect(splitPath('\\\\server\\share')).toEqual({ dir: '\\\\server\\', name: 'share' });
    expect(splitPath('…ces/payments')).toEqual({ dir: '…ces/', name: 'payments' });
    expect(splitPath('~/app')).toEqual({ dir: '~/', name: 'app' });
  });

  it('keeps a trailing separator with the name and roots whole', () => {
    expect(splitPath('/a/b/')).toEqual({ dir: '/a/', name: 'b/' });
    for (const whole of ['~', '/', '/var', 'C:\\', 'app', '']) {
      expect(splitPath(whole)).toEqual({ dir: '', name: whole });
    }
  });
});

describe('sessionName', () => {
  it('uses the cwd basename when the title is not custom', () => {
    expect(sessionName({ ...base, cwd: '/Users/me/src/app' })).toBe('app');
  });

  it('keeps a custom title even when cwd is known', () => {
    expect(sessionName({ ...base, title: 'My shell', titleIsCustom: true, cwd: '/Users/me/src/app' })).toBe('My shell');
  });

  it('falls back to the title when cwd is absent', () => {
    expect(sessionName(base)).toBe('Home');
  });

  it('prefers the live cwd over the record cwd for the current tab', () => {
    expect(sessionName({ ...base, cwd: '/Users/me/src/app' }, '/Users/me/other')).toBe('other');
  });

  it('passes a hostile cwd through as plain text, not markup', () => {
    // React escapes text children on render; this only guards that folderName
    // does not itself interpret separators inside the hostile fragment.
    const hostile = '/Users/me/<img src=x onerror=alert(1)>';
    expect(sessionName({ ...base, cwd: hostile })).toBe('<img src=x onerror=alert(1)>');
  });
});
