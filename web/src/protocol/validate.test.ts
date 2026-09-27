import { describe, expect, it } from 'vitest';
import { InvalidProtocolMessageError, parseHostMessage } from './validate';

const requestId = '00000000-0000-4000-8000-000000000001';
const brokerGeneration = '00000000-0000-4000-8000-000000000002';

function welcome() {
  return {
    version: 1,
    type: 'welcome',
    requestId,
    payload: {
      brokerGeneration,
      negotiatedVersion: 1,
      capabilityProfile: 'phone-terminal-v1',
      hostBuild: 'test',
      limits: {
        inboundMessageBytes: 262144,
        outboundMessageBytes: 1048576,
        outgoingQueueBytes: 2097152,
        maxTextBytes: 65536,
        maxPasteBytes: 65536,
        minColumns: 20,
        maxColumns: 400,
        minRows: 5,
        maxRows: 200,
        maxHistoryPageRows: 200,
      },
    },
  };
}

describe('parseHostMessage', () => {
  it('returns a schema-validated host message', () => {
    const message = parseHostMessage(JSON.stringify(welcome()));
    expect(message.type).toBe('welcome');
  });

  it('accepts session.state with a waiting agent question', () => {
    const message = parseHostMessage(JSON.stringify({
      version: 1,
      type: 'session.state',
      requestId,
      sessionId: brokerGeneration,
      generation: brokerGeneration,
      payload: {
        state: 'running',
        agent: {
          state: 'waiting',
          stateSince: '2026-09-27T10:00:00.000Z',
          question: {
            id: '0123456789abcdef0123456789abcdef',
            text: 'Do you want to proceed?',
            detail: 'Bash command\nnpm test -- --watch=false',
            options: [{ label: 'Yes' }, { label: 'No' }],
          },
        },
      },
    }));
    expect(message.type === 'session.state' && message.payload.agent?.question?.options).toHaveLength(2);
  });

  it('rejects an agent detail with control characters or blank lines', () => {
    for (const detail of ['rm \u001b[31m', 'a\n\nb', 'a \nb', '\nb']) {
      expect(() => parseHostMessage(JSON.stringify({
        version: 1,
        type: 'session.state',
        requestId,
        sessionId: brokerGeneration,
        generation: brokerGeneration,
        payload: {
          state: 'running',
          agent: {
            state: 'waiting',
            stateSince: '2026-09-27T10:00:00.000Z',
            question: { id: '0123456789abcdef0123456789abcdef', text: 'Proceed?', detail, options: [{ label: 'Yes' }] },
          },
        },
      }))).toThrow(InvalidProtocolMessageError);
    }
  });

  it('rejects client-to-host messages even when their schema is valid', () => {
    expect(() => parseHostMessage(JSON.stringify({
      version: 1,
      type: 'hello',
      requestId,
      payload: {
        clientBuild: 'test',
        clientKind: 'browser',
        supportedVersions: [1],
        capabilityProfile: 'phone-terminal-v1',
      },
    }))).toThrow('not host-to-client');
  });

  it('rejects malformed JSON and additional properties', () => {
    expect(() => parseHostMessage('{')).toThrow('not valid JSON');
    expect(() => parseHostMessage(JSON.stringify({ ...welcome(), unexpected: true })))
      .toThrow(InvalidProtocolMessageError);
  });

  it('rejects non-lowercase canonical UUID spelling', () => {
    expect(() => parseHostMessage(JSON.stringify({
      ...welcome(),
      requestId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA',
    }))).toThrow(InvalidProtocolMessageError);
  });

  it('rejects messages beyond the host byte limit before parsing', () => {
    expect(() => parseHostMessage(`{"padding":"${'x'.repeat(1024 * 1024)}"}`))
      .toThrow('outside');
  });
});
