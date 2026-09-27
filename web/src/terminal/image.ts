/** The broker refuses larger uploads (decoded bytes). */
export const MAX_UPLOAD_BYTES = 5 * 1024 * 1024;
export const MAX_EDGE = 2048;
export const THUMBNAIL_EDGE = 96;
export const JPEG_QUALITY = 0.85;

export const UNREADABLE = 'This photo type cannot be read here. Try a JPEG or PNG.';
export const TOO_LARGE = 'This photo is still larger than 5 MB after shrinking. Try a smaller one.';
export const NOT_ENCODED = 'This photo could not be prepared. Try another one.';

export type OutputType = 'image/png' | 'image/jpeg';

export type Bitmap = { readonly width: number; readonly height: number; close(): void };

export interface Canvas {
  /** Draws the image scaled to fill the canvas, over the background color when one is given. */
  draw(image: Bitmap, background?: string): void;
  toBlob(type: OutputType, quality?: number): Promise<Blob | null>;
  toDataURL(type: OutputType, quality?: number): string;
}

export type ImageDeps = {
  readonly createImageBitmap: (file: Blob) => Promise<Bitmap>;
  readonly createCanvas: (width: number, height: number) => Canvas;
};

export type PreparedImage = {
  readonly blob: Blob;
  /** A data: URL; the page CSP blocks blob: images. */
  readonly thumbnail: string;
};

/** Scales to fit a max edge, keeping the aspect ratio; never upscales. */
export function fitSize(width: number, height: number, max = MAX_EDGE): { width: number; height: number } {
  const scale = Math.min(1, max / Math.max(width, height, 1));
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

/** Screenshots (PNG, maybe transparent) stay PNG; camera photos and everything else become JPEG. */
export function chooseOutput(inputType: string): OutputType {
  return inputType === 'image/png' ? 'image/png' : 'image/jpeg';
}

export const browserImageDeps: ImageDeps = {
  // Applies the EXIF orientation by default.
  createImageBitmap: (file) => createImageBitmap(file),
  createCanvas(width, height) {
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d');
    if (context === null) throw new Error(NOT_ENCODED);
    return {
      draw(image, background) {
        context.imageSmoothingQuality = 'high';
        if (background !== undefined) {
          context.fillStyle = background;
          context.fillRect(0, 0, width, height);
        }
        context.drawImage(image as ImageBitmap, 0, 0, width, height);
      },
      toBlob: (type, quality) => new Promise((resolve) => { canvas.toBlob(resolve, type, quality); }),
      toDataURL: (type, quality) => canvas.toDataURL(type, quality),
    };
  },
};

/**
 * Decodes and redraws the photo, which drops EXIF and GPS data, and shrinks it
 * to at most MAX_EDGE. Throws an Error with a short user-facing message.
 */
export async function prepareImage(file: Blob, deps: ImageDeps = browserImageDeps): Promise<PreparedImage> {
  let bitmap: Bitmap;
  try {
    bitmap = await deps.createImageBitmap(file);
  } catch {
    throw new Error(UNREADABLE);
  }
  try {
    const render = (type: OutputType, edge: number) => {
      const size = fitSize(bitmap.width, bitmap.height, edge);
      const canvas = deps.createCanvas(size.width, size.height);
      // JPEG has no transparency: paint white first so clear pixels do not turn black.
      canvas.draw(bitmap, type === 'image/jpeg' ? '#fff' : undefined);
      return canvas;
    };
    const type = chooseOutput(file.type);
    let blob = await render(type, MAX_EDGE).toBlob(type, type === 'image/jpeg' ? JPEG_QUALITY : undefined);
    if (type === 'image/png' && (blob === null || blob.size > MAX_UPLOAD_BYTES)) {
      blob = await render('image/jpeg', MAX_EDGE).toBlob('image/jpeg', JPEG_QUALITY);
    }
    if (blob === null) throw new Error(NOT_ENCODED);
    if (blob.size > MAX_UPLOAD_BYTES) throw new Error(TOO_LARGE);
    const thumbnail = render('image/jpeg', THUMBNAIL_EDGE).toDataURL('image/jpeg', 0.7);
    return { blob, thumbnail };
  } finally {
    bitmap.close();
  }
}
