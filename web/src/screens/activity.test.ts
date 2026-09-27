import { describe, expect, it } from 'vitest';
import { hasNewOutput, markViewed, readViewed, VIEWED_KEY, viewedKey } from './activity';
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
