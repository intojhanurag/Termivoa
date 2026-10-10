import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { Computer } from '../computers';
import { hosted, type ComputerEntry } from './allSessions';
import { amberPill, Drawer, DrawerBar, needsByComputer, stateLine } from './Drawer';
import { inboxItems } from './Inbox';
import { mac, okEntry, ready, vps } from './providerFixtures';

const pi: Computer = { origin: 'https://pi.tailx.ts.net', host: 'pi.tailx.ts.net', label: 'pi', color: '#7BD88F', home: false };
const noop = () => undefined;
const session = (computer: Computer, state: 'waiting' | 'working') => hosted({
  id: `84444444-4444-4444-8444-00000000000${state === 'waiting' ? 1 : 2}`, generation: '85555555-5555-4555-8555-555555555555', title: state,
  projectId: 'p', shellProfileId: 's', state: 'running', columns: 80, rows: 24, createdAt: '2026-10-04T08:00:00Z', updatedAt: '2026-10-04T08:00:00Z',
  agent: { state, stateSince: '2026-10-04T09:00:00Z' },
}, computer);

describe('needsByComputer and amberPill', () => {
  // mac: 1 blocked herdr item; vps: 1 waiting session.
  const counts = needsByComputer(inboxItems([session(vps, 'waiting'), session(vps, 'working')], [ready(mac)], {}));

  it('counts what waits, per computer', () => {
    expect([...counts]).toEqual([[vps.host, 1], [mac.host, 1]]);
  });

  it('shows another computer that needs you, never the chosen one, and none in All computers', () => {
    expect(amberPill([mac, vps], counts, mac.host)).toEqual({ computer: vps, count: 1 });
    expect(amberPill([mac, vps], counts, vps.host)).toEqual({ computer: mac, count: 1 });
    expect(amberPill([mac, vps], counts, undefined)).toBeUndefined();
    expect(amberPill([mac, pi], new Map([[mac.host, 2]]), mac.host)).toBeUndefined();
  });

  it('is a button that says the count in words', () => {
    const html = renderToStaticMarkup(<DrawerBar chosen={mac} open={false} amber={{ computer: vps, count: 2 }} onOpen={noop} onChoose={noop} />);
    expect(html).toContain('<button type="button" class="drawer-amber"><span class="dot amber" aria-hidden="true"></span><bdi>vps</bdi><span class="drawer-count" aria-hidden="true">2</span><span class="visually-hidden">, 2 need you</span></button>');
  });
});

describe('stateLine', () => {
  it('says sessions and working agents, or the trouble', () => {
    const entry = (status: ComputerEntry['status']): ComputerEntry => ({ computer: vps, status });
    const ok = okEntry(vps);
    if (ok.status.kind !== 'ok') throw new Error('fixture');
    expect(stateLine(entry({ ...ok.status, sessions: [session(vps, 'working')] }), [])).toBe('1 session · 1 working');
    expect(stateLine(okEntry(mac), [ready(mac), ready(vps)])).toBe('0 sessions · 2 working');
    expect(stateLine(entry({ kind: 'unreachable' }), [])).toBe("can't reach");
    expect(stateLine(entry({ kind: 'blocked' }), [])).toBe("can't reach");
    expect(stateLine(entry({ kind: 'pair' }), [])).toBe('pair again');
  });
});

describe('Drawer', () => {
  const entries: ComputerEntry[] = [okEntry(mac), okEntry(vps), { computer: pi, status: { kind: 'unreachable' } }];
  const html = ({ needs, chosen }: { needs: number | undefined; chosen: string | undefined } = { needs: 3, chosen: vps.host }) => renderToStaticMarkup(
    <Drawer
      entries={entries} chosen={chosen} counts={new Map([[mac.host, 1], [vps.host, 2]])} needs={needs} line={(entry) => `${entry.computer.label} line`}
      onChoose={noop} onNeeds={noop} onWall={noop} onSettings={noop} onAddComputer={noop} onClose={noop}
    />,
  );

  it('is a labelled dialog with All computers and one row per computer: tile, label, state line, amber count', () => {
    const page = html();
    expect(page).toContain('role="dialog" aria-modal="true" aria-labelledby="drawer-title" tabindex="-1"><h2 id="drawer-title" class="caps">Computers</h2>');
    expect(page).toContain('aria-current="false"><span class="drawer-tile all" aria-hidden="true">');
    expect(page).toContain('<span class="drawer-label">All computers</span></span><span class="drawer-count" aria-hidden="true">3</span><span class="visually-hidden">, 3 need you</span>');
    expect(page).toContain('<button type="button" class="drawer-row is-ok" aria-current="true"><span class="drawer-tile" aria-hidden="true">V</span><span class="drawer-text"><span class="drawer-label"><bdi>vps</bdi></span><span class="drawer-state"><span class="visually-hidden">, </span>vps line</span></span><span class="drawer-count" aria-hidden="true">2</span>');
    expect(page).toContain('<span class="drawer-count" aria-hidden="true">1</span><span class="visually-hidden">, 1 needs you</span>');
    expect(page).toContain('class="drawer-row is-unreachable" aria-current="false"><span class="drawer-tile" aria-hidden="true">P</span>');
    expect(page.match(/aria-current="true"/gu)).toHaveLength(1);
  });

  it('ends with Needs you (count), Wall, Settings and + Add a computer', () => {
    const page = html();
    const at = (text: string) => page.indexOf(text);
    expect(page).toContain('<span class="drawer-label">Needs you</span><span class="drawer-count" aria-hidden="true">3</span>');
    expect(at('>Needs you<')).toBeLessThan(at('>Wall<'));
    expect(at('>Wall<')).toBeLessThan(at('>Settings<'));
    expect(at('>Settings<')).toBeLessThan(at('>+ Add a computer<'));
    expect(html({ needs: undefined, chosen: vps.host })).not.toContain('Needs you');
    expect(html({ needs: 3, chosen: undefined })).toContain('aria-current="true"><span class="drawer-tile all"');
  });
});
