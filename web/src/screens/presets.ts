import { storage, type StorageLike } from '../storage';

/** A saved way to start a session. Plain user text, not a secret. */
export interface LaunchPreset {
  readonly label: string;
  readonly projectId: string;
  readonly shellProfileId: string;
  readonly runFirst?: string;
}

/**
 * A command to type once into a newly created session. `settled` flips to
 * true when it was sent or given up, so it can never be sent twice.
 */
export interface RunFirstRequest {
  readonly command: string;
  settled: boolean;
}

export const PRESETS_KEY = 'pterm.launchPresets';
export const MAX_PRESETS = 20;
export const MAX_PRESET_LABEL = 80;
export const MAX_RUN_FIRST = 1000;
const CONTROLS = /[\u0000-\u001f\u007f-\u009f]/u;

/** "Run first" is typed as one line of text followed by Enter. */
export function runFirstProblem(command: string): string | undefined {
  if ([...command].length > MAX_RUN_FIRST) return `Use a shorter command (${MAX_RUN_FIRST} characters at most).`;
  if (CONTROLS.test(command)) return 'Use one line without control characters.';
  return undefined;
}

function isPreset(value: unknown): value is LaunchPreset {
  if (typeof value !== 'object' || value === null) return false;
  const item = value as Record<string, unknown>;
  return typeof item.label === 'string' && item.label.trim() !== '' && [...item.label].length <= MAX_PRESET_LABEL
    && !CONTROLS.test(item.label) && typeof item.projectId === 'string' && typeof item.shellProfileId === 'string'
    && (item.runFirst === undefined
      || (typeof item.runFirst === 'string' && item.runFirst !== '' && runFirstProblem(item.runFirst) === undefined));
}

export function loadPresets(store: StorageLike | undefined = storage()): LaunchPreset[] {
  try {
    const parsed: unknown = JSON.parse(store?.getItem(PRESETS_KEY) ?? '[]');
    return Array.isArray(parsed) ? parsed.filter(isPreset).slice(0, MAX_PRESETS) : [];
  } catch {
    return [];
  }
}

export function savePresets(list: ReadonlyArray<LaunchPreset>, store: StorageLike | undefined = storage()): string | undefined {
  if (list.length > MAX_PRESETS) return `You can keep ${MAX_PRESETS} presets at most.`;
  if (!list.every(isPreset)) return 'This preset is not valid.';
  try {
    store?.setItem(PRESETS_KEY, JSON.stringify(list));
  } catch {
    return 'This browser did not save the preset.';
  }
  return undefined;
}

/** Builds a preset from the custom form; the label is the command or the project. */
export function presetFrom(projectId: string, shellProfileId: string, runFirst: string, projectName: string): LaunchPreset {
  const command = runFirst.trim();
  const label = [...(command || projectName)].slice(0, MAX_PRESET_LABEL).join('').trim() || 'Session';
  return command === '' ? { label, projectId, shellProfileId } : { label, projectId, shellProfileId, runFirst: command };
}

export function samePreset(a: LaunchPreset, b: LaunchPreset): boolean {
  return a.projectId === b.projectId && a.shellProfileId === b.shellProfileId && a.runFirst === b.runFirst;
}
