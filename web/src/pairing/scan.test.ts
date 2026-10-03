import { describe, expect, it, vi } from 'vitest';
import { cameraProblem, SCAN_PROBLEMS, startScan, type Decode, type ScanEnd } from './scan';

/** A camera stream with one track, and a page that can go to the background. */
function setup({ open, decode = async () => undefined }: { readonly open?: () => Promise<MediaStream>; readonly decode?: Decode } = {}) {
  const track = { stop: vi.fn() };
  const stream = { getTracks: () => [track] } as unknown as MediaStream;
  const getUserMedia = vi.fn(open ?? (async () => stream));
  const video = { srcObject: null as unknown, play: vi.fn(async () => undefined) };
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
  return { track, stream, getUserMedia, video, listeners, onEnd, onScanning, stop, hide };
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
    expect(SCAN_PROBLEMS).toEqual({
      denied: 'The camera is blocked. Allow the camera for this app in your phone or browser settings, then tap Scan QR. Or paste the link.',
      none: 'No camera found. Paste the link.',
      failed: 'The camera did not start. Close other apps that use it, then tap Scan QR. Or paste the link.',
    });

    const denied = setup({ open: () => Promise.reject(named('NotAllowedError')) });
    await vi.waitFor(() => expect(denied.onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'denied' }));
    expect(denied.listeners.size).toBe(0);

    // A browser with no camera API (an http page, an old browser).
    const onEnd = vi.fn();
    startScan({ video: { srcObject: null, play: vi.fn() } as never, onScanning: vi.fn(), onEnd, media: undefined, doc: quietDoc() });
    await vi.waitFor(() => expect(onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'none' }));
  });

  it('stops the camera when the decoder cannot load', async () => {
    const track = { stop: vi.fn() };
    const onEnd = vi.fn();
    startScan({
      video: { srcObject: null, play: vi.fn(async () => undefined) } as never,
      onScanning: vi.fn(),
      onEnd,
      media: { getUserMedia: async () => ({ getTracks: () => [track] }) } as never,
      doc: quietDoc(),
      load: () => Promise.reject(new Error('chunk')),
    });
    await vi.waitFor(() => expect(onEnd).toHaveBeenCalledWith({ kind: 'problem', problem: 'failed' }));
    expect(track.stop).toHaveBeenCalledOnce();
  });
});
