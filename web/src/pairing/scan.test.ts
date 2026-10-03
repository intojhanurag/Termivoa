import { encodeQR } from 'qr';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChunkLoadError } from '../screens/chunks';
import { cameraProblem, loadDecoder, SCAN_PROBLEMS, startScan, type Decode, type ScanEnd } from './scan';

/** A camera stream with one track that the system can end, and a page that can go to the background. */
function setup({ open, decode = async () => undefined, play = async () => undefined }: {
  readonly open?: () => Promise<MediaStream>;
  readonly decode?: Decode;
  readonly play?: () => Promise<void>;
} = {}) {
  const ended = new Set<() => void>();
  const track = { stop: vi.fn(), addEventListener: (_type: 'ended', listener: () => void) => { ended.add(listener); } };
  const stream = { getTracks: () => [track] } as unknown as MediaStream;
  const getUserMedia = vi.fn(open ?? (async () => stream));
  const video = { srcObject: null as unknown, play: vi.fn(play) };
  const listeners = new Set<() => void>();
  const doc = {
    visibilityState: 'visible' as DocumentVisibilityState,
    addEventListener: (_type: string, listener: () => void) => { listeners.add(listener); },
    removeEventListener: (_type: string, listener: () => void) => { listeners.delete(listener); },
  };
  const onEnd = vi.fn<(end: ScanEnd) => void>();
  const onScanning = vi.fn();
  const stop = startScan({
    video: video as never, onScanning, onEnd, media: { getUserMedia } as never, doc: doc as never, load: async () => decode, everyMs: 1,
  });
  const hide = () => { doc.visibilityState = 'hidden'; for (const listener of [...listeners]) listener(); };
  const endTrack = () => { for (const listener of ended) listener(); };
  return { track, stream, getUserMedia, video, listeners, onEnd, onScanning, stop, hide, endTrack };
}

const named = (name: string) => Object.assign(new Error(name), { name });
const quietDoc = () => ({ visibilityState: 'visible', addEventListener: vi.fn(), removeEventListener: vi.fn() }) as never;

describe('Scan QR camera', () => {
  it('asks for the rear camera without sound, and stops it as soon as a code is read', async () => {
    const frames = [undefined, undefined, 'https://vps.tailx.ts.net/add-computer#x'];
    const scan = setup({ decode: async () => frames.shift() });
    await vi.waitFor(() => expect(scan.onEnd).toHaveBeenCalledWith({ kind: 'text', text: 'https://vps.tailx.ts.net/add-computer#x' }));
    expect(scan.getUserMedia).toHaveBeenCalledWith({ audio: false, video: { facingMode: { ideal: 'environment' } } });
    expect(scan.onScanning).toHaveBeenCalledOnce();
    expect(scan.track.stop).toHaveBeenCalledOnce();
    expect(scan.video.srcObject).toBeNull();
    expect(scan.listeners.size).toBe(0);
    expect(scan.onEnd).toHaveBeenCalledOnce();
  });

  it('stops the camera on Cancel and unmount, without a result', async () => {
    const scan = setup();
    await vi.waitFor(() => expect(scan.onScanning).toHaveBeenCalled());
    expect(scan.video.srcObject).toBe(scan.stream);
    scan.stop();
    expect(scan.track.stop).toHaveBeenCalledOnce();
    expect(scan.video.srcObject).toBeNull();
    scan.hide();
    expect(scan.onEnd).not.toHaveBeenCalled();
  });

  it('stops a camera that opens after Cancel (the permission prompt was still open)', async () => {
    let allow: (stream: MediaStream) => void = () => undefined;
    const late = { stop: vi.fn() };
    const scan = setup({ open: () => new Promise((resolve) => { allow = resolve; }) });
    scan.stop();
    allow({ getTracks: () => [late] } as unknown as MediaStream);
    await vi.waitFor(() => expect(late.stop).toHaveBeenCalledOnce());
    expect(scan.video.play).not.toHaveBeenCalled();
    expect(scan.onScanning).not.toHaveBeenCalled();
    expect(scan.onEnd).not.toHaveBeenCalled();
  });

  it('stops the camera when the app goes to the background', async () => {
    const scan = setup();
    await vi.waitFor(() => expect(scan.onScanning).toHaveBeenCalled());
    scan.hide();
    expect(scan.track.stop).toHaveBeenCalledOnce();
    expect(scan.onEnd).toHaveBeenCalledWith({ kind: 'hidden' });
  });

  it('names each camera problem', async () => {
    expect(cameraProblem(named('NotAllowedError'))).toBe('denied');
    expect(cameraProblem(named('SecurityError'))).toBe('denied');
    expect(cameraProblem(named('NotFoundError'))).toBe('none');
    expect(cameraProblem(named('OverconstrainedError'))).toBe('none');
    expect(cameraProblem(named('NotReadableError'))).toBe('failed');
    expect(cameraProblem(undefined)).toBe('failed');
    expect(cameraProblem(new ChunkLoadError(new TypeError('Failed to fetch dynamically imported module')))).toBe('reader');
    expect(SCAN_PROBLEMS).toEqual({
      denied: 'The camera is blocked. Allow the camera for this app in your phone or browser settings, then tap Scan QR. Or paste the link.',
      none: 'No camera found. Paste the link.',
      failed: 'The camera did not start. Close other apps that use it, then tap Scan QR. Or paste the link.',
      reader: 'The QR reader did not load. Reload the app, or paste the link.',
    });

    const denied = setup({ open: () => Promise.reject(named('NotAllowedError')) });
    await vi.waitFor(() => expect(denied.onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'denied' }));
    expect(denied.listeners.size).toBe(0);

    // A browser with no camera API (an http page, an old browser).
    const onEnd = vi.fn();
    startScan({ video: { srcObject: null, play: vi.fn() } as never, onScanning: vi.fn(), onEnd, media: undefined, doc: quietDoc() });
    await vi.waitFor(() => expect(onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'none' }));
  });

  it('says the QR reader did not load (not a camera problem) when its chunk cannot load, and stops the camera', async () => {
    const track = { stop: vi.fn(), addEventListener: vi.fn() };
    const onEnd = vi.fn();
    startScan({
      video: { srcObject: null, play: vi.fn(async () => undefined) } as never,
      onScanning: vi.fn(),
      onEnd,
      media: { getUserMedia: async () => ({ getTracks: () => [track] }) } as never,
      doc: quietDoc(),
      load: () => Promise.reject(new ChunkLoadError(new TypeError('Failed to fetch dynamically imported module'))),
    });
    await vi.waitFor(() => expect(onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'reader' }));
    expect(track.stop).toHaveBeenCalledOnce();

    // The JavaScript decoder that replaces a failing BarcodeDetector cannot load either.
    const scan = setup({ decode: () => Promise.reject(new ChunkLoadError(new TypeError('x'))) });
    await vi.waitFor(() => expect(scan.onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'reader' }));
    expect(scan.track.stop).toHaveBeenCalledOnce();
  });

  it('says the camera did not start when play() fails, also with NotAllowedError (the camera was allowed)', async () => {
    const scan = setup({ play: () => Promise.reject(named('NotAllowedError')) });
    await vi.waitFor(() => expect(scan.onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'failed' }));
    expect(scan.track.stop).toHaveBeenCalledOnce();
    expect(scan.onScanning).not.toHaveBeenCalled();
  });

  it('ends with a camera problem when the camera stops by itself (another app took it), not a frozen frame', async () => {
    const scan = setup();
    await vi.waitFor(() => expect(scan.onScanning).toHaveBeenCalled());
    scan.endTrack();
    expect(scan.onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'failed' });
    expect(scan.track.stop).toHaveBeenCalledOnce();
    expect(scan.video.srcObject).toBeNull();
  });
});

/** A camera frame (RGBA) that shows `text` as a real QR code, made with the `qr` package's own encoder. */
function qrFrame(text: string) {
  const bits = encodeQR(text, 'raw');
  const scale = 4;
  const quiet = 4;
  const side = (bits.length + 2 * quiet) * scale;
  const data = new Uint8ClampedArray(side * side * 4).fill(255);
  bits.forEach((row, y) => row.forEach((dark, x) => {
    if (!dark) return;
    for (let dy = 0; dy < scale; dy += 1) {
      for (let dx = 0; dx < scale; dx += 1) {
        const at = (((y + quiet) * scale + dy) * side + (x + quiet) * scale + dx) * 4;
        data.fill(0, at, at + 3);
      }
    }
  }));
  return { width: side, height: side, data, colorSpace: 'srgb' } as ImageData;
}

/** A page whose canvas returns `frame`; counts the canvas size changes (each one clears and reallocates it). */
function stubCanvas(frame: ImageData) {
  let width = 0;
  let height = 0;
  const resized = vi.fn();
  const canvas = {
    get width() { return width; },
    set width(value: number) { width = value; resized(); },
    get height() { return height; },
    set height(value: number) { height = value; resized(); },
    getContext: () => ({ drawImage: vi.fn(), getImageData: () => frame }),
  };
  vi.stubGlobal('document', { createElement: () => canvas });
  return resized;
}

const link = 'https://vps.tailx.ts.net/add-computer#grant=GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG';

describe('Scan QR decoder', () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it('reads a real QR code with the JavaScript decoder, and sizes the canvas only when the frame size changes', async () => {
    const frame = qrFrame(link);
    const resized = stubCanvas(frame);
    vi.stubGlobal('BarcodeDetector', undefined);
    const decode = await loadDecoder();
    const video = { videoWidth: frame.width, videoHeight: frame.height } as HTMLVideoElement;
    expect(await decode(video)).toBe(link);
    expect(await decode(video)).toBe(link);
    expect(resized).toHaveBeenCalledTimes(2);
  });

  it('reads nothing from a frame with two different codes, and one code seen twice', async () => {
    const frames = [[{ rawValue: link }, { rawValue: 'https://evil.tailx.ts.net/add-computer#x' }], [{ rawValue: link }, { rawValue: link }]];
    vi.stubGlobal('BarcodeDetector', Object.assign(function Detector() { return { detect: async () => frames.shift() ?? [] }; }, {
      getSupportedFormats: async () => ['qr_code'],
    }));
    const decode = await loadDecoder();
    const video = {} as HTMLVideoElement;
    expect(await decode(video)).toBeUndefined();
    expect(await decode(video)).toBe(link);
    expect(await decode(video)).toBeUndefined();
  });

  it('switches to the JavaScript decoder after 10 BarcodeDetector failures in a row', async () => {
    const frame = qrFrame(link);
    stubCanvas(frame);
    const detect = vi.fn(() => Promise.reject(new Error('detect failed')));
    vi.stubGlobal('BarcodeDetector', Object.assign(function Detector() { return { detect }; }, {
      getSupportedFormats: async () => ['qr_code'],
    }));
    const decode = await loadDecoder();
    const video = { videoWidth: frame.width, videoHeight: frame.height } as HTMLVideoElement;
    for (let frameNumber = 0; frameNumber < 10; frameNumber += 1) expect(await decode(video)).toBeUndefined();
    expect(await decode(video)).toBe(link);
    expect(detect).toHaveBeenCalledTimes(10);
  });
});
