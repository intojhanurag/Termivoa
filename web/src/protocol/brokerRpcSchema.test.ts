import Ajv2020 from 'ajv/dist/2020';
import { describe, expect, it } from 'vitest';
import brokerRPCSchema from '../../../protocol/v1/broker-rpc.schema.json';
import envelopeSchema from '../../../protocol/v1/envelope.schema.json';

const requestId = '11111111-1111-4111-8111-11111111111a';
const sessionId = '22222222-2222-4222-8222-22222222222a';
const generation = '33333333-3333-4333-8333-33333333333a';
const operationId = '44444444-4444-4444-8444-44444444444a';
const projectId = '55555555-5555-4555-8555-55555555555a';
const shellProfileId = '66666666-6666-4666-8666-66666666666a';

const ajv = new Ajv2020({ allErrors: true, strict: true });
ajv.addSchema(envelopeSchema);
const validate = ajv.compile(brokerRPCSchema);

const record = {
  id: sessionId,
  generation,
  title: 'project shell',
  projectId,
  shellProfileId,
  state: 'running',
  columns: 80,
  rows: 24,
  createdAt: '2026-09-20T10:00:00Z',
  updatedAt: '2026-09-20T10:00:01Z',
  startedAt: '2026-09-20T10:00:01Z',
};

const payloads = {
  'session.list.request': { limit: 50 },
  'session.create.request': { operationId, projectId, shellProfileId },
  'session.rename.request': { operationId, title: 'renamed' },
  'session.terminate.request': { operationId },
  'session.status.request': {},
  'session.list.result': { sessions: [record] },
  'session.create.result': { operationId, session: record },
  'session.rename.result': { operationId, session: record },
  'session.terminate.result': { operationId, session: record },
  'session.status.result': { session: record },
  'broker.error': { code: 'session_missing', message: 'session does not exist' },
} as const;

type MessageType = keyof typeof payloads;

function message(type: MessageType, identity: boolean) {
  return {
    version: 1,
    type,
    requestId,
    ...(identity ? { sessionId, generation } : {}),
    payload: payloads[type],
  };
}

describe('broker RPC identity schema', () => {
  it.each([
    'session.list.request',
    'session.create.request',
    'session.list.result',
    'session.create.result',
  ] satisfies MessageType[])('%s forbids session identity', (type) => {
    expect(validate(message(type, false))).toBe(true);
    expect(validate(message(type, true))).toBe(false);
    expect(validate({ ...message(type, false), sessionId })).toBe(false);
    expect(validate({ ...message(type, false), generation })).toBe(false);
  });

  it.each([
    'session.rename.request',
    'session.terminate.request',
    'session.status.request',
    'session.rename.result',
    'session.terminate.result',
    'session.status.result',
  ] satisfies MessageType[])('%s requires complete session identity', (type) => {
    expect(validate(message(type, true))).toBe(true);
    expect(validate(message(type, false))).toBe(false);
    expect(validate({ ...message(type, false), sessionId })).toBe(false);
    expect(validate({ ...message(type, false), generation })).toBe(false);
  });

  it('allows broker.error with both identity fields or neither', () => {
    expect(validate(message('broker.error', false))).toBe(true);
    expect(validate(message('broker.error', true))).toBe(true);
    expect(validate({ ...message('broker.error', false), sessionId })).toBe(false);
    expect(validate({ ...message('broker.error', false), generation })).toBe(false);
  });

  it('rejects non-lowercase UUID spelling at every schema reference', () => {
    expect(validate({ ...message('session.list.request', false), requestId: requestId.toUpperCase() })).toBe(false);
    expect(validate({
      ...message('session.create.request', false),
      payload: { operationId: operationId.toUpperCase(), projectId, shellProfileId },
    })).toBe(false);
    expect(validate({
      ...message('session.list.result', false),
      payload: { sessions: [{ ...record, id: sessionId.toUpperCase() }] },
    })).toBe(false);
  });
});
