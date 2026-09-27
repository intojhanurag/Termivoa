import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { api, blobToBase64 } from '../api/http';
import { formatSize, PhotoAttacher, type Attachment, type Uploader } from '../terminal/attachments';
import type { Connection } from '../terminal/Connection';
import { checkPaste, lineCount, readDraft, saveDraft } from '../terminal/composer';
import { prepareImage } from '../terminal/image';
import { NO_MODIFIERS } from '../terminal/InputController';
import {
  appendDictation, IDLE_SPEECH, joinWords, recognizerFactory, SpeechController, type RecognizerFactory, type SpeechSnapshot,
} from '../terminal/speech';
import { Sheet, SheetHeader } from './Sheet';

const noSubscribe = () => () => undefined;
const idleSpeech = () => IDLE_SPEECH;

export function Composer({ connection, canSend, draftKey, onClose, speech = recognizerFactory() ?? null, uploader }: {
  readonly connection: Connection;
  readonly canSend: boolean;
  readonly draftKey: string;
  readonly onClose: () => void;
  /** Makes speech recognizers; null where the browser has none. */
  readonly speech?: RecognizerFactory | null;
  readonly uploader?: Uploader;
}) {
  const [text, setText] = useState(() => readDraft(draftKey));
  const [confirming, setConfirming] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [attachment, setAttachment] = useState<Attachment>();
  const [note, setNote] = useState<string>();
  // The draft store is edited too, so a path that arrives after the sheet
  // closed is still in the draft next time.
  const [attacher] = useState(() => new PhotoAttacher({
    setAttachment,
    editText: (edit) => { saveDraft(draftKey, edit(readDraft(draftKey))); setText(edit); },
  }));
  const [voice, setVoice] = useState<SpeechController>();
  const picker = useRef<HTMLInputElement>(null);
  const snapshot = voice?.getSnapshot ?? idleSpeech;
  const heard = useSyncExternalStore(voice?.subscribe ?? noSubscribe, snapshot, snapshot);

  useEffect(() => { saveDraft(draftKey, text); }, [draftKey, text]);
  useEffect(() => { if (!canSend) setConfirming(false); }, [canSend]);
  // The mic stops when the panel closes, the sheet goes away, or the page is hidden.
  useEffect(() => {
    if (voice === undefined) return undefined;
    const hidden = () => { if (document.visibilityState === 'hidden') voice.stop(); };
    const pagehide = () => { voice.stop(); };
    document.addEventListener('visibilitychange', hidden);
    window.addEventListener('pagehide', pagehide);
    return () => {
      document.removeEventListener('visibilitychange', hidden);
      window.removeEventListener('pagehide', pagehide);
      voice.cancel();
    };
  }, [voice]);

  function send(confirmed: boolean) {
    setProblem(undefined);
    switch (checkPaste(text, canSend, confirmed)) {
      case 'needs-confirmation':
        setConfirming(true);
        return;
      case 'too-large':
        setProblem('This draft is larger than 64 KiB. Shorten it before sending.');
        return;
      case 'ready':
        if (connection.sendPaste(text)) {
          connection.sendKey('enter', NO_MODIFIERS);
          setText('');
          attacher.remove();
          setNote(undefined);
        }
        setConfirming(false);
        return;
      default:
    }
  }

  function pick(file: File | undefined) {
    if (file === undefined) return;
    setNote(undefined);
    const upload = uploader ?? (async (blob: Blob) => api.uploadImage(connection.sessionId, {
      generation: connection.generation,
      data: await blobToBase64(blob),
    }));
    void attacher.attach(file, { prepare: prepareImage, upload });
  }

  function openVoice() {
    if (speech === null) return;
    const controller = new SpeechController(speech, navigator.language);
    controller.start(); // inside the tap, so the browser may ask for the mic
    setVoice(controller);
  }

  if (voice !== undefined) {
    return (
      <Sheet labelledBy="composer-title" onClose={onClose}>
        <SheetHeader id="composer-title" title="Compose">
          <button type="button" className="link" onClick={onClose}>Close</button>
        </SheetHeader>
        <VoicePanel
          snapshot={heard}
          onMic={() => { if (heard.state === 'listening') voice.stop(); else voice.start(); }}
          onCancel={() => { setVoice(undefined); }}
          onDone={() => {
            const spoken = joinWords(heard.finalText, heard.interimText);
            setText((current) => appendDictation(current, spoken));
            setVoice(undefined);
          }}
        />
      </Sheet>
    );
  }

  const lines = lineCount(text);
  const busy = attachment?.phase === 'preparing' || attachment?.phase === 'uploading';

  return (
    <Sheet labelledBy="composer-title" onClose={onClose}>
      <SheetHeader id="composer-title" title="Compose">
        <button type="button" className="link" onClick={onClose}>Close</button>
      </SheetHeader>
      <label htmlFor="composer-draft" className="visually-hidden">
        Draft text here. Nothing is sent until you tap Send.
      </label>
      <textarea
        id="composer-draft"
        className="draft composer-input"
        value={text}
        rows={5}
        placeholder="Draft text here. Nothing is sent until you tap Send."
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => { setText(event.target.value); setConfirming(false); }}
      />
      <div className="spread composer-meta">
        {lines > 1 ? (
          <span className="amber note">
            <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
              <path d="M7 1.5 13 12.5H1z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
              <path d="M7 5.5v3M7 10.3v.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
            {lines} lines · you confirm before it sends
          </span>
        ) : <span />}
        <button type="button" className="link" disabled={text === ''} onClick={() => setText('')}>Clear</button>
      </div>
      {!canSend && (
        <p role="status" className="notice">
          Sending is off because you are not connected or do not have control. Your draft is kept.
        </p>
      )}
      {problem !== undefined && <p role="alert" className="error">{problem}</p>}
      {attachment !== undefined && (
        <AttachmentRow attachment={attachment} onRemove={() => { attacher.remove(); setNote('Removed from this message.'); }} />
      )}
      {note !== undefined && <p role="status" className="hint-text">{note}</p>}
      {confirming ? (
        <div role="alertdialog" aria-labelledby="paste-warning" className="confirm stack">
          <p id="paste-warning">
            This draft has {lines} lines. A shell without safe bracketed paste can run each line as a command.
          </p>
          <div className="row">
            <button type="button" className="danger" disabled={!canSend} onClick={() => send(true)}>
              Send {lines} lines
            </button>
            <button type="button" onClick={() => setConfirming(false)}>Cancel</button>
          </div>
        </div>
      ) : (
        <div className="composer-toolbar">
          <input
            ref={picker}
            type="file"
            accept="image/*"
            hidden
            onChange={(event) => { pick(event.target.files?.[0]); event.target.value = ''; }}
          />
          <button type="button" className="tool" disabled={!canSend || busy} onClick={() => picker.current?.click()}>
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
              <path d="m21 11-8.6 8.6a5.5 5.5 0 0 1-7.8-7.8l8.9-8.9a3.7 3.7 0 0 1 5.2 5.2l-8.9 8.9a1.8 1.8 0 0 1-2.6-2.6l8.2-8.2"
                fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            Photo
          </button>
          {speech !== null && (
            <button type="button" className="tool" onClick={openVoice}>
              <MicIcon size={18} />
              Voice
            </button>
          )}
          <button type="button" className="primary send" disabled={!canSend || text === '' || busy} onClick={() => send(false)}>
            Send <span aria-hidden="true">⏎</span>
          </button>
        </div>
      )}
      {speech === null && <p className="hint-text">Tip: use the keyboard's mic to dictate.</p>}
    </Sheet>
  );
}

function MicIcon({ size }: { readonly size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <rect x="9" y="3" width="6" height="11" rx="3" fill="none" stroke="currentColor" strokeWidth="1.8" />
      <path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </svg>
  );
}

function AttachmentRow({ attachment, onRemove }: { readonly attachment: Attachment; readonly onRemove: () => void }) {
  const thumbnail = attachment.phase === 'preparing' ? undefined : attachment.thumbnail;
  return (
    <div className="attachment">
      {thumbnail === undefined ? <span className="thumb" /> : <img className="thumb" src={thumbnail} alt="" />}
      <div className="attachment-text">
        <span className="attachment-name">{attachment.name}</span>
        {attachment.phase === 'done' ? (
          <span className="attachment-status green">✓ on your computer · {formatSize(attachment.size)}</span>
        ) : attachment.phase === 'failed' ? (
          <span role="alert" className="attachment-status error">{attachment.message}</span>
        ) : (
          <span role="status" className="attachment-status">{attachment.phase === 'preparing' ? 'preparing…' : 'uploading…'}</span>
        )}
      </div>
      <button type="button" className="link" aria-label="Remove photo" onClick={onRemove}>✕</button>
    </div>
  );
}

function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

export function VoicePanel({ snapshot, onMic, onCancel, onDone }: {
  readonly snapshot: SpeechSnapshot;
  readonly onMic: () => void;
  readonly onCancel: () => void;
  readonly onDone: () => void;
}) {
  const listening = snapshot.state === 'listening';
  return (
    <div className="voice stack">
      <p className="transcript">
        {snapshot.finalText}
        {snapshot.finalText !== '' && snapshot.interimText !== '' && ' '}
        <span className="muted">{snapshot.interimText}</span>
      </p>
      <button
        type="button"
        className={listening ? 'mic listening' : 'mic'}
        aria-label={listening ? 'Stop listening' : 'Start listening'}
        onClick={onMic}
      >
        <MicIcon size={32} />
      </button>
      {snapshot.state === 'error' ? (
        <p role="alert" className="error voice-status">{snapshot.error}</p>
      ) : (
        <p role="status" className="voice-status">
          {listening ? <><span className="dot red" aria-hidden="true" />LISTENING · {clock(snapshot.elapsed)}</> : 'TAP THE MIC'}
        </p>
      )}
      <div className="voice-actions">
        <button type="button" onClick={onCancel}>Cancel</button>
        <button type="button" className="primary" onClick={onDone}>Done · edit</button>
      </div>
      <p className="hint-text">Uses your phone's speech service. Nothing is sent until you tap Send.</p>
    </div>
  );
}
