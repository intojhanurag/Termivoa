import { describe, expect, it } from 'vitest';
import type { HTTPUploadResponse } from '../generated/protocol';
import { ApiError } from '../api/http';
import { formatSize, insertReference, PhotoAttacher, removeReference, type Attachment, type AttachDeps } from './attachments';

const path = '.termivoa/uploads/20260927-143012-a1b2c3d4.png';
const saved: HTTPUploadResponse = { path, size: 421_000, contentType: 'image/png' };
const photo = new File([new Uint8Array(8)], 'IMG_0042.PNG', { type: 'image/png' });
const prepare: AttachDeps['prepare'] = async (file) => ({ blob: file, thumbnail: 'data:image/jpeg;base64,AA==' });

function harness(text = '') {
  const states: Array<Attachment | undefined> = [];
  const draft = { text };
  const attacher = new PhotoAttacher({
    setAttachment: (attachment) => { states.push(attachment); },
    editText: (edit) => { draft.text = edit(draft.text); },
  });
  return { attacher, states, draft };
}

describe('reference text', () => {
  it('inserts @path and one space at the start', () => {
    expect(insertReference('', path)).toBe(`@${path} `);
    expect(insertReference('fix this', path)).toBe(`@${path} fix this`);
  });

  it('removes that exact token wherever it is', () => {
    expect(removeReference(`@${path} fix this`, path)).toBe('fix this');
    expect(removeReference(`see @${path} here`, path)).toBe('see here');
    expect(removeReference(`@${path}x fix`, path)).toBe(`@${path}x fix`);
    expect(removeReference('fix this', path)).toBe('fix this');
  });

  it('formats sizes in KB and MB', () => {
    expect(formatSize(421_888)).toBe('412 KB');
    expect(formatSize(100)).toBe('1 KB');
    expect(formatSize(1.25 * 1024 * 1024)).toBe('1.3 MB');
  });
});

describe('photo attacher', () => {
  it('goes preparing → uploading → done and inserts the reference', async () => {
    const { attacher, states, draft } = harness('fix this');
    const sent: Blob[] = [];
    await attacher.attach(photo, { prepare, upload: async (blob) => { sent.push(blob); return saved; } });
    expect(states.map((state) => state?.phase)).toEqual(['preparing', 'uploading', 'done']);
    expect(states[2]).toEqual({ phase: 'done', name: 'IMG_0042.PNG', thumbnail: 'data:image/jpeg;base64,AA==', path, size: 421_000 });
    expect(sent).toEqual([photo]);
    expect(draft.text).toBe(`@${path} fix this`);
  });

  it('keeps the text and shows the server message when the upload fails', async () => {
    const { attacher, states, draft } = harness('fix this');
    await attacher.attach(photo, {
      prepare,
      upload: () => Promise.reject(new ApiError(409, 'not_controller', 'Take control to attach a photo.')),
    });
    expect(states.at(-1)).toEqual({
      phase: 'failed', name: 'IMG_0042.PNG', thumbnail: 'data:image/jpeg;base64,AA==', message: 'Take control to attach a photo.',
    });
    expect(draft.text).toBe('fix this');
  });

  it('shows the prepare message and never uploads an unreadable photo', async () => {
    const { attacher, states } = harness();
    let uploads = 0;
    await attacher.attach(photo, {
      prepare: () => Promise.reject(new Error('This photo type cannot be read here. Try a JPEG or PNG.')),
      upload: async () => { uploads++; return saved; },
    });
    expect(uploads).toBe(0);
    expect(states.at(-1)).toEqual({ phase: 'failed', name: 'IMG_0042.PNG', message: 'This photo type cannot be read here. Try a JPEG or PNG.' });
  });

  it('remove drops the row and the inserted reference only', async () => {
    const { attacher, states, draft } = harness();
    await attacher.attach(photo, { prepare, upload: async () => saved });
    draft.text += 'fix this';
    attacher.remove();
    expect(states.at(-1)).toBeUndefined();
    expect(draft.text).toBe('fix this');
  });

  it('ignores an upload that finishes after the photo was removed', async () => {
    const { attacher, states, draft } = harness('fix this');
    let finish: (value: HTTPUploadResponse) => void = () => undefined;
    const pending = attacher.attach(photo, { prepare, upload: () => new Promise((resolve) => { finish = resolve; }) });
    await Promise.resolve();
    await Promise.resolve();
    attacher.remove();
    finish(saved);
    await pending;
    expect(states.at(-1)).toBeUndefined();
    expect(draft.text).toBe('fix this');
  });
});
