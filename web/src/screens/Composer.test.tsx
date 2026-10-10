import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { Connection } from '../terminal/Connection';
import { IDLE_SPEECH, type Recognizer } from '../terminal/speech';
import { Composer, VoicePanel } from './Composer';

const noop = () => undefined;
const connection = {} as Connection;
const recognizer = () => ({}) as Recognizer;

function composer(speech: (() => Recognizer) | null, canSend = true) {
  return renderToStaticMarkup(
    <Composer connection={connection} canSend={canSend} draftKey="s/g" onClose={noop} speech={speech} />,
  );
}

describe('composer toolbar', () => {
  it('offers Photo, Voice and Send, with a hidden image picker', () => {
    const html = composer(recognizer);
    expect(html).toContain('<input type="file" accept="image/*" hidden=""/>');
    expect(html).toMatch(/<button type="button" class="tool">.*Photo<\/button>/u);
    expect(html).toMatch(/<button type="button" class="tool">.*Voice<\/button>/u);
    expect(html).toContain('<button type="button" class="primary send" disabled="">Send <span aria-hidden="true">⏎</span></button>');
    expect(html).not.toContain('Paste into terminal');
    expect(html).not.toContain('keyboard&#x27;s mic');
  });

  it('hides Voice and shows the keyboard tip where speech is not supported', () => {
    const html = composer(null);
    expect(html).toContain('Photo');
    expect(html).not.toContain('Voice');
    expect(html).toContain('Tip: use the keyboard&#x27;s mic to dictate.');
  });

  it('turns Photo off without control', () => {
    expect(composer(recognizer, false)).toMatch(/<button type="button" class="tool" disabled="">.*Photo<\/button>/u);
  });

  it('says why sending is off in a neutral notice with its icon', () => {
    expect(composer(recognizer, false)).toMatch(/<div class="notice neutral" role="status">.*Sending is off/u);
  });
});

describe('voice panel', () => {
  it('shows the running clock, both kinds of text and the privacy note while listening', () => {
    const html = renderToStaticMarkup(
      <VoicePanel
        snapshot={{ state: 'listening', finalText: 'fix the login', interimText: 'bug', elapsed: 7 }}
        onMic={noop} onCancel={noop} onDone={noop}
      />,
    );
    expect(html).toContain('Listening · 0:07');
    expect(html).toContain('class="dot red"');
    expect(html).toContain('<p class="transcript">fix the login <span class="muted">bug</span></p>');
    expect(html).toContain('class="mic listening" aria-label="Stop listening"');
    expect(html).toContain('>Cancel</button>');
    expect(html).toContain('>Done · edit</button>');
    expect(html).toContain('Uses your phone&#x27;s speech service. Nothing is sent until you tap Send.');
  });

  it('asks for a tap when idle and shows the error text', () => {
    const idle = renderToStaticMarkup(<VoicePanel snapshot={IDLE_SPEECH} onMic={noop} onCancel={noop} onDone={noop} />);
    expect(idle).toContain('Tap the mic');
    expect(idle).toContain('class="mic" aria-label="Start listening"');

    const failed = renderToStaticMarkup(
      <VoicePanel snapshot={{ ...IDLE_SPEECH, state: 'error', error: 'No speech heard. Tap the mic and try again.' }}
        onMic={noop} onCancel={noop} onDone={noop} />,
    );
    expect(failed).toContain('<p role="alert" class="error voice-status">No speech heard. Tap the mic and try again.</p>');
    expect(failed).not.toContain('Listening');
  });
});
