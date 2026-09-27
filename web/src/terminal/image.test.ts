import { describe, expect, it } from 'vitest';
import {
  chooseOutput, fitSize, JPEG_QUALITY, MAX_UPLOAD_BYTES, prepareImage, TOO_LARGE, UNREADABLE,
  type Canvas, type ImageDeps, type OutputType,
} from './image';

type Drawn = { width: number; height: number; background: string | undefined; type?: OutputType; quality?: number };

/** A fake canvas that records what was drawn and returns blobs of the chosen sizes. */
function fakeDeps(width: number, height: number, sizes: Partial<Record<OutputType, number>>) {
  const drawn: Drawn[] = [];
  let closed = false;
  const deps: ImageDeps = {
    createImageBitmap: async () => ({ width, height, close: () => { closed = true; } }),
    createCanvas(canvasWidth, canvasHeight): Canvas {
      const record: Drawn = { width: canvasWidth, height: canvasHeight, background: undefined };
      drawn.push(record);
      return {
        draw(_image, background) { record.background = background; },
        async toBlob(type, quality) {
          record.type = type;
          if (quality !== undefined) record.quality = quality;
          return new Blob([new Uint8Array(sizes[type] ?? 10)], { type });
        },
        toDataURL: (type) => `data:${type};base64,AA==`,
      };
    },
  };
  return { deps, drawn, closed: () => closed };
}

const file = (type: string) => new File([new Uint8Array(4)], 'photo', { type });

describe('image size math', () => {
  it('keeps the aspect ratio, never upscales, rounds and stays at least 1 px', () => {
    expect(fitSize(4032, 3024)).toEqual({ width: 2048, height: 1536 });
    expect(fitSize(3024, 4032)).toEqual({ width: 1536, height: 2048 });
    expect(fitSize(800, 600)).toEqual({ width: 800, height: 600 });
    expect(fitSize(1000, 333, 96)).toEqual({ width: 96, height: 32 });
    expect(fitSize(10000, 1, 96)).toEqual({ width: 96, height: 1 });
    expect(fitSize(0, 0)).toEqual({ width: 1, height: 1 });
  });

  it('keeps PNG as PNG and turns everything else into JPEG', () => {
    expect(chooseOutput('image/png')).toBe('image/png');
    expect(chooseOutput('image/jpeg')).toBe('image/jpeg');
    expect(chooseOutput('image/heic')).toBe('image/jpeg');
    expect(chooseOutput('')).toBe('image/jpeg');
  });
});

describe('prepareImage', () => {
  it('redraws a camera photo as a shrunk JPEG over white, plus a small thumbnail', async () => {
    const fake = fakeDeps(4032, 3024, { 'image/jpeg': 900_000 });
    const prepared = await prepareImage(file('image/jpeg'), fake.deps);
    expect(prepared.blob.type).toBe('image/jpeg');
    expect(prepared.blob.size).toBe(900_000);
    expect(prepared.thumbnail).toMatch(/^data:image\/jpeg;base64,/u);
    expect(fake.drawn[0]).toEqual({ width: 2048, height: 1536, background: '#fff', type: 'image/jpeg', quality: JPEG_QUALITY });
    expect(fake.drawn[1]).toMatchObject({ width: 96, height: 72 });
    expect(fake.closed()).toBe(true);
  });

  it('keeps a screenshot as PNG with its transparency', async () => {
    const fake = fakeDeps(1179, 2556, { 'image/png': 400_000 });
    const prepared = await prepareImage(file('image/png'), fake.deps);
    expect(prepared.blob.type).toBe('image/png');
    expect(fake.drawn[0]).toMatchObject({ width: 945, height: 2048, background: undefined, type: 'image/png' });
    expect(fake.drawn).toHaveLength(2);
  });

  it('falls back to JPEG on white when the PNG is over 5 MiB', async () => {
    const fake = fakeDeps(2000, 2000, { 'image/png': MAX_UPLOAD_BYTES + 1, 'image/jpeg': 700_000 });
    const prepared = await prepareImage(file('image/png'), fake.deps);
    expect(prepared.blob.type).toBe('image/jpeg');
    expect(fake.drawn.map((item) => [item.type, item.background])).toEqual([
      ['image/png', undefined], ['image/jpeg', '#fff'], [undefined, '#fff'],
    ]);
  });

  it('fails with a short message when even the JPEG is too large', async () => {
    const fake = fakeDeps(2000, 2000, { 'image/jpeg': MAX_UPLOAD_BYTES + 1 });
    await expect(prepareImage(file('image/jpeg'), fake.deps)).rejects.toThrow(TOO_LARGE);
    expect(fake.closed()).toBe(true);
  });

  it('says the type cannot be read when decoding fails', async () => {
    const deps: ImageDeps = { ...fakeDeps(1, 1, {}).deps, createImageBitmap: () => Promise.reject(new Error('decode')) };
    await expect(prepareImage(file('image/heic'), deps)).rejects.toThrow(UNREADABLE);
  });
});
