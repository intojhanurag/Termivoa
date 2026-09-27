import type { HTTPUploadResponse } from '../generated/protocol';
import type { PreparedImage } from './image';

export type Uploader = (blob: Blob) => Promise<HTTPUploadResponse>;

/** One photo in the composer. The name is the phone's file name, for display only; it is never sent. */
export type Attachment =
  | { readonly phase: 'preparing'; readonly name: string }
  | { readonly phase: 'uploading'; readonly name: string; readonly thumbnail: string }
  | { readonly phase: 'done'; readonly name: string; readonly thumbnail: string; readonly path: string; readonly size: number }
  | { readonly phase: 'failed'; readonly name: string; readonly thumbnail?: string; readonly message: string };

export type AttachmentSink = {
  readonly setAttachment: (attachment: Attachment | undefined) => void;
  readonly editText: (edit: (text: string) => string) => void;
};

export type AttachDeps = {
  readonly prepare: (file: Blob) => Promise<PreparedImage>;
  readonly upload: Uploader;
};

/** Puts `@path ` at the start, so the user just types the rest of the message. */
export function insertReference(text: string, path: string): string {
  return `@${path} ${text}`;
}

/** Removes the exact `@path ` token wherever it is. */
export function removeReference(text: string, path: string): string {
  return text.replaceAll(`@${path} `, '');
}

export function formatSize(bytes: number): string {
  return bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Runs one photo at a time through prepare → upload and, on success, inserts
 * its path into the draft. A newer photo or a removal makes older runs stale.
 */
export class PhotoAttacher {
  private run = 0;
  private current: Attachment | undefined;

  constructor(private readonly sink: AttachmentSink) {}

  async attach(file: File, deps: AttachDeps): Promise<void> {
    const run = ++this.run;
    const show = (attachment: Attachment) => {
      if (run !== this.run) return false;
      this.current = attachment;
      this.sink.setAttachment(attachment);
      return true;
    };
    const { name } = file;
    let thumbnail: string | undefined;
    show({ phase: 'preparing', name });
    try {
      const prepared = await deps.prepare(file);
      thumbnail = prepared.thumbnail;
      if (!show({ phase: 'uploading', name, thumbnail })) return;
      const saved = await deps.upload(prepared.blob);
      if (!show({ phase: 'done', name, thumbnail, path: saved.path, size: saved.size })) return;
      this.sink.editText((text) => insertReference(text, saved.path));
    } catch (error) {
      const message = error instanceof Error ? error.message : 'The photo could not be sent.';
      show({ phase: 'failed', name, message, ...(thumbnail === undefined ? {} : { thumbnail }) });
    }
  }

  /** Drops the row and its `@path ` text. The file on the computer stays. */
  remove(): void {
    this.run++;
    const current = this.current;
    this.current = undefined;
    this.sink.setAttachment(undefined);
    if (current?.phase === 'done') {
      this.sink.editText((text) => removeReference(text, current.path));
    }
  }
}
