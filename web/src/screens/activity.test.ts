import { describe, expect, it } from 'vitest';
import {
  agentBadge, hasNewOutput, markViewed, readViewed, readWorked, recapLine, since, span, VIEWED_KEY, viewedKey, WORKED_KEY,
  type RecapFacts,
} from './activity';
import { loadPresets, MAX_PRESETS, presetFrom, PRESETS_KEY, runFirstProblem, savePresets } from './presets';

function memory() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value); },
    values,
  };
}

const session = { id: '00000000-0000-4000-8000-000000000001', generation: '00000000-0000-4000-8000-000000000002' };
const at = (time: string) => Date.parse(`2026-09-27T${time}Z`);

describe('new-output dot', () => {
  it('compares lastOutputAt with the time this device last viewed the session', () => {
    const store = memory();
    const output = { ...session, lastOutputAt: '2026-09-27T10:00:00.000Z' };
    expect(hasNewOutput(output, readViewed(store))).toBe(true);

    markViewed(output, at('10:01:00'), store);
    expect(hasNewOutput(output, readViewed(store))).toBe(false);
    expect(hasNewOutput({ ...output, lastOutputAt: '2026-09-27T10:00:59.999Z' }, readViewed(store))).toBe(false);
    expect(hasNewOutput({ ...output, lastOutputAt: '2026-09-27T10:01:00.001Z' }, readViewed(store))).toBe(true);
  });

  it('shows no dot without lastOutputAt or for a malformed time', () => {
    expect(hasNewOutput(session, {})).toBe(false);
    expect(hasNewOutput({ ...session, lastOutputAt: 'soon' }, {})).toBe(false);
  });

  it('keys viewed times by session id and generation', () => {
    const store = memory();
    markViewed({ ...session, lastOutputAt: '2026-09-27T10:00:00Z' }, at('10:01:00'), store);
    const restarted = { ...session, generation: '00000000-0000-4000-8000-000000000003', lastOutputAt: '2026-09-27T10:00:00Z' };
    expect(hasNewOutput(restarted, readViewed(store))).toBe(true);
    expect(Object.keys(readViewed(store))).toEqual([viewedKey(session)]);
  });

  it('never records a view earlier than output already on screen (phone clock behind host)', () => {
    const store = memory();
    const output = { ...session, lastOutputAt: '2026-09-27T10:05:00Z' };
    markViewed(output, at('10:00:00'), store);
    expect(hasNewOutput(output, readViewed(store))).toBe(false);
  });

  it('bounds stored entries and ignores tampered values', () => {
    const store = memory();
    for (let index = 0; index < 120; index++) {
      markViewed({ id: String(index), generation: 'g' }, index, store);
    }
    const viewed = readViewed(store);
    expect(Object.keys(viewed)).toHaveLength(100);
    expect(viewed['119/g']).toBe(119);
    expect(viewed['0/g']).toBeUndefined();
    store.setItem(VIEWED_KEY, '{"a/b":"x","c/d":5}');
    expect(readViewed(store)).toEqual({ 'c/d': 5 });
  });
});

describe('agent status', () => {
  it('formats compact durations', () => {
    const now = at('12:00:00');
    expect(since('2026-09-27T11:59:48Z', now)).toBe('12s');
    expect(since('2026-09-27T11:58:00Z', now)).toBe('2m');
    expect(since('2026-09-27T10:55:00Z', now)).toBe('1h 5m');
    expect(since('2026-09-27T10:00:00Z', now)).toBe('2h');
    expect(since('2026-09-25T10:00:00Z', now)).toBe('2d');
    expect(since('2026-09-27T12:00:05Z', now)).toBe('0s');
    expect(since(undefined, now)).toBe('');
  });

  it('labels waiting and working, and says "your turn" only for idle after work with unseen output', () => {
    const stateSince = '2026-09-27T10:00:00Z';
    expect(agentBadge({ ...session, agent: { state: 'waiting', stateSince } }, false)).toEqual({ label: 'Waiting', tone: 'amber' });
    expect(agentBadge({ ...session, agent: { state: 'working', stateSince } }, false)).toEqual({ label: 'Working', tone: 'accent' });
    expect(agentBadge({}, true)).toBeUndefined();
    // A shell idle at its prompt (no afterWork from the broker): neutral.
    expect(agentBadge({ ...session, agent: { state: 'idle', stateSince } }, true)).toBeUndefined();
    // Idle after work with unseen output: your turn; once viewed: neutral again.
    const done = { ...session, agent: { state: 'idle' as const, stateSince, afterWork: true } };
    expect(agentBadge(done, true)).toEqual({ label: 'Your turn', tone: 'green' });
    expect(agentBadge(done, false)).toBeUndefined();
  });
});

describe('while you were away', () => {
  const stateSince = '2026-09-27T10:00:00Z';
  const now = at('12:00:00');
  const facts = (extra: Partial<RecapFacts>): RecapFacts => ({
    now,
    viewedAt: at('11:28:00'),
    workedBefore: 100,
    agent: { state: 'idle', stateSince, workedSeconds: 100 + 14 * 60 },
    fresh: false,
    ...extra,
  });

  it('stores the worked seconds next to the viewed time, by session and generation', () => {
    const store = memory();
    markViewed({ ...session, agent: { state: 'working', stateSince, workedSeconds: 42 } }, at('10:00:00'), store);
    markViewed({ id: 'other', generation: 'g' }, at('10:01:00'), store);
    expect(readWorked(store)).toEqual({ [viewedKey(session)]: 42 });
    // A later view without a total keeps the last one seen.
    markViewed(session, at('10:02:00'), store);
    expect(readWorked(store)).toEqual({ [viewedKey(session)]: 42 });
    store.setItem(WORKED_KEY, '{"a/b":"x"}');
    expect(readWorked(store)).toEqual({});
  });

  it('formats spans in short words', () => {
    expect(span(40.9)).toBe('40 s');
    expect(span(14 * 60 + 5)).toBe('14 min');
    expect(span(2 * 3600)).toBe('2 h');
    expect(span(2 * 3600 + 5 * 60)).toBe('2 h 5 min');
    expect(span(3 * 86400)).toBe('3 d');
    expect(span(-5)).toBe('0 s');
  });

  it('tells how long ago you left, how long the agent worked, the changes and whose turn it is', () => {
    expect(recapLine(facts({
      agent: { state: 'waiting', stateSince, workedSeconds: 100 + 14 * 60 },
      changes: { files: 5, additions: 120, deletions: 34 },
    }))).toBe('Since you left 32 min ago · worked 14 min · 5 files changed +120 −34 · waiting for you');
    expect(recapLine(facts({ changes: { files: 1, additions: 2, deletions: 0 } })))
      .toBe('Since you left 32 min ago · worked 14 min · 1 file changed +2 −0');
    expect(recapLine(facts({ agent: { state: 'idle', stateSince, afterWork: true, workedSeconds: 100 }, fresh: true })))
      .toBe('Since you left 32 min ago · your turn');
  });

  it('stays quiet without a minute of work or a turn for you', () => {
    expect(recapLine(facts({ agent: { state: 'working', stateSince, workedSeconds: 159 } }))).toBeUndefined();
    expect(recapLine(facts({ agent: { state: 'idle', stateSince, workedSeconds: 160 } }))).toBe('Since you left 32 min ago · worked 1 min');
    // Idle after work that this device already saw is not "your turn".
    expect(recapLine(facts({ agent: { state: 'idle', stateSince, afterWork: true, workedSeconds: 100 } }))).toBeUndefined();
    expect(recapLine(facts({ agent: undefined }))).toBeUndefined();
  });

  it('leaves out each part it has no facts for', () => {
    // First visit on this device: no time, no worked total, but still waiting.
    expect(recapLine(facts({ viewedAt: undefined, workedBefore: undefined, agent: { state: 'waiting', stateSince, workedSeconds: 900 } })))
      .toBe('Waiting for you');
    // No total from the broker, or a smaller one after a broker restart: no worked part.
    expect(recapLine(facts({ agent: { state: 'waiting', stateSince } }))).toBe('Since you left 32 min ago · waiting for you');
    expect(recapLine(facts({ workedBefore: 5000 }))).toBeUndefined();
    // Not a repository or a clean tree: no changes part.
    expect(recapLine(facts({ changes: { files: 0, additions: 0, deletions: 0 } }))).toBe('Since you left 32 min ago · worked 14 min');
  });
});

describe('launch presets', () => {
  it('start empty and save a custom session as a preset', () => {
    const store = memory();
    expect(loadPresets(store)).toEqual([]);
    const preset = presetFrom('p', 'zsh', '  claude ', 'api-server');
    expect(preset).toEqual({ label: 'claude', projectId: 'p', shellProfileId: 'zsh', runFirst: 'claude' });
    expect(presetFrom('p', 'zsh', '', 'api-server')).toEqual({ label: 'api-server', projectId: 'p', shellProfileId: 'zsh' });
    expect(savePresets([preset], store)).toBeUndefined();
    expect(loadPresets(store)).toEqual([preset]);
  });

  it('keeps run first to one bounded line and caps the list', () => {
    expect(runFirstProblem('npm run dev')).toBeUndefined();
    expect(runFirstProblem('a\nb')).toMatch(/one line/u);
    expect(runFirstProblem('x'.repeat(1001))).toMatch(/shorter/u);
    const store = memory();
    const many = Array.from({ length: MAX_PRESETS + 1 }, (_, index) => presetFrom('p', 'z', `c${index}`, ''));
    expect(savePresets(many, store)).toMatch(/20 presets/u);
    store.setItem(PRESETS_KEY, JSON.stringify([{ label: 'x', projectId: 'p', shellProfileId: 'z', runFirst: 'a\nb' }]));
    expect(loadPresets(store)).toEqual([]);
  });
});
