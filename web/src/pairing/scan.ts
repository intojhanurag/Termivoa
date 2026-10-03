/**
 * Scan QR for + Add a computer: the camera inside the app reads the QR code
 * that `pterm pair --via` printed. The decoded text is only text: the caller
 * gives it to parseAddComputerLink, exactly like a pasted link. Frames stay
 * in this page (never sent, never stored).
 */

import { ChunkLoadError, loadChunk } from '../screens/chunks';

export type ScanProblem = 'denied' | 'none' | 'failed' | 'reader';

/** Plain words for each camera problem, shown with the Scan QR and Paste link choices. */
export const SCAN_PROBLEMS: Readonly<Record<ScanProblem, string>> = {
  denied: 'The camera is blocked. Allow the camera for this app in your phone or browser settings, then tap Scan QR. Or paste the link.',
  none: 'No camera found. Paste the link.',
  failed: 'The camera did not start. Close other apps that use it, then tap Scan QR. Or paste the link.',
  reader: 'The QR reader did not load. Reload the app, or paste the link.',
};

export const SCAN_ASKING = 'Allow the camera to scan the QR code.';
export const SCAN_POINT = 'Point the camera at the QR code that pterm pair --via printed.';

export type ScanEnd =
  | { readonly kind: 'text'; readonly text: string }
  | { readonly kind: 'problem'; readonly problem: ScanProblem }
  /** The app went to the background; the camera is off. */
  | { readonly kind: 'hidden' };

/** Reads one video frame; undefined when it shows no QR code. */
export type Decode = (video: HTMLVideoElement) => Promise<string | undefined>;

/** getUserMedia's rejection (or the decoder chunk's) as a problem. */
export function cameraProblem(error: unknown): ScanProblem {
  if (error instanceof ChunkLoadError) return 'reader';
  const name = typeof error === 'object' && error !== null && 'name' in error ? error.name : undefined;
  if (name === 'NotAllowedError' || name === 'SecurityError') return 'denied';
  if (name === 'NotFoundError' || name === 'OverconstrainedError') return 'none';
  return 'failed';
}

interface NativeDetector {
  detect(source: HTMLVideoElement): Promise<ReadonlyArray<{ readonly rawValue: string }>>;
}
interface NativeDetectorClass {
  new (options: { formats: string[] }): NativeDetector;
  getSupportedFormats(): Promise<ReadonlyArray<string>>;
}

/** The longer side of the frame given to the JavaScript decoder; more pixels only cost time. */
const MAX_SIDE = 800;
/** BarcodeDetector failures in a row before the JavaScript decoder takes over. */
const NATIVE_FAILURES = 10;

/**
 * The browser's own BarcodeDetector where it reads QR codes (Chrome on
 * Android); else the `qr` package's decoder, fetched only now (its own
 * chunk). iOS Safari has no BarcodeDetector. A frame with two different
 * codes reads nothing: the user cannot tell which one the app would take.
 */
export async function loadDecoder(): Promise<Decode> {
  const Native = (globalThis as { BarcodeDetector?: NativeDetectorClass }).BarcodeDetector;
  if (Native !== undefined && (await Native.getSupportedFormats().catch((): ReadonlyArray<string> => [])).includes('qr_code')) {
    const detector = new Native({ formats: ['qr_code'] });
    let failures = 0;
    let fallback: Promise<Decode> | undefined;
    return async (video) => {
      if (fallback !== undefined) return (await fallback)(video);
      try {
        const codes = new Set((await detector.detect(video)).map((code) => code.rawValue).filter((text) => text !== ''));
        failures = 0;
        return codes.size === 1 ? [...codes][0] : undefined;
      } catch {
        failures += 1;
        if (failures >= NATIVE_FAILURES) fallback = loadScriptDecoder();
        return undefined;
      }
    };
  }
  return loadScriptDecoder();
}

/** The `qr` package's decoder on a canvas copy of the frame; a failed chunk load rejects with ChunkLoadError. */
async function loadScriptDecoder(): Promise<Decode> {
  // `.then(({ decodeQR }) …)` keeps the rest of decode.js out of the chunk (tree shaking).
  const decodeQR = await loadChunk(() => import('qr/decode.js').then(({ decodeQR: read }) => read));
  const canvas = document.createElement('canvas');
  const context = canvas.getContext('2d', { willReadFrequently: true });
  return async (video) => {
    const scale = Math.min(1, MAX_SIDE / Math.max(video.videoWidth, video.videoHeight));
    const width = Math.floor(video.videoWidth * scale);
    const height = Math.floor(video.videoHeight * scale);
    if (context === null || width === 0 || height === 0) return undefined;
    // Setting a size clears and reallocates the canvas: only when it changes.
    if (canvas.width !== width) canvas.width = width;
    if (canvas.height !== height) canvas.height = height;
    context.drawImage(video, 0, 0, width, height);
    try {
      return decodeQR(context.getImageData(0, 0, width, height)) || undefined;
    } catch {
      // No QR code in this frame.
      return undefined;
    }
  };
}

type VideoLike = Pick<HTMLVideoElement, 'srcObject' | 'play'>;

/**
 * Opens the rear camera in `video` and reads frames until one shows a QR
 * code. The camera stops, and `onEnd` is called once, when a code is read, on
 * a camera or decoder problem (also when the camera stops by itself), and when
 * the app goes to the background. The returned
 * function stops it without a call (Cancel, unmount); it is safe at any time,
 * also while the permission prompt is open.
 */
export function startScan({ video, onScanning, onEnd, media = navigator.mediaDevices, doc = document, load = loadDecoder, everyMs = 150 }: {
  readonly video: VideoLike;
  /** The camera runs and frames are being read. */
  readonly onScanning: () => void;
  readonly onEnd: (end: ScanEnd) => void;
  readonly media?: Pick<MediaDevices, 'getUserMedia'> | undefined;
  readonly doc?: Pick<Document, 'visibilityState' | 'addEventListener' | 'removeEventListener'>;
  readonly load?: () => Promise<Decode>;
  readonly everyMs?: number;
}): () => void {
  let live = true;
  let stream: MediaStream | undefined;
  const hidden = () => { if (doc.visibilityState === 'hidden') end({ kind: 'hidden' }); };
  const stop = () => {
    live = false;
    doc.removeEventListener('visibilitychange', hidden);
    for (const track of stream?.getTracks() ?? []) track.stop();
    stream = undefined;
    video.srcObject = null;
  };
  const end = (result: ScanEnd) => {
    if (!live) return;
    stop();
    onEnd(result);
  };
  doc.addEventListener('visibilitychange', hidden);

  void (async () => {
    try {
      if (media?.getUserMedia === undefined) {
        end({ kind: 'problem', problem: 'none' });
        return;
      }
      const loading = load();
      loading.catch(() => undefined);
      const opened = await media.getUserMedia({ audio: false, video: { facingMode: { ideal: 'environment' } } });
      if (!live) {
        // Stopped while the permission prompt was open.
        for (const track of opened.getTracks()) track.stop();
        return;
      }
      stream = opened;
      // Another app took the camera, or the system stopped it: the frame would freeze.
      for (const track of opened.getTracks()) track.addEventListener('ended', () => end({ kind: 'problem', problem: 'failed' }));
      video.srcObject = opened;
      try {
        await video.play();
      } catch {
        // The camera is allowed; a play() rejection (also NotAllowedError) is not a permission problem.
        end({ kind: 'problem', problem: 'failed' });
        return;
      }
      const decode = await loading;
      if (live) onScanning();
      while (live) {
        const text = await decode(video as HTMLVideoElement);
        if (text !== undefined) {
          end({ kind: 'text', text });
          return;
        }
        await new Promise((resolve) => { setTimeout(resolve, everyMs); });
      }
    } catch (error) {
      end({ kind: 'problem', problem: cameraProblem(error) });
    }
  })();
  return stop;
}
