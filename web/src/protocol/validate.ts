import envelopeSchema from '../../../protocol/v1/envelope.schema.json';
import type {
  AccessRevokedPayload,
  ControlChangedPayload,
  ErrorPayload,
  HistoryChangedPayload,
  HistoryPagePayload,
  InputAcceptedPayload,
  InputRejectedPayload,
  ScreenPatchPayload,
  SessionStatePayload,
  SnapshotBeginPayload,
  SnapshotEndPayload,
  SnapshotRowsPayload,
  WelcomePayload,
} from '../generated/protocol';
import { createProtocolAjv } from './ajv';

const MAX_HOST_MESSAGE_BYTES = 1024 * 1024;

type HostPayloads = {
  readonly welcome: WelcomePayload;
  readonly 'snapshot.begin': SnapshotBeginPayload;
  readonly 'snapshot.rows': SnapshotRowsPayload;
  readonly 'snapshot.end': SnapshotEndPayload;
  readonly 'screen.patch': ScreenPatchPayload;
  readonly 'history.page': HistoryPagePayload;
  readonly 'history.changed': HistoryChangedPayload;
  readonly 'control.changed': ControlChangedPayload;
  readonly 'input.accepted': InputAcceptedPayload;
  readonly 'input.rejected': InputRejectedPayload;
  readonly 'session.state': SessionStatePayload;
  readonly 'access.revoked': AccessRevokedPayload;
  readonly error: ErrorPayload;
};

export type ValidatedHostMessage = {
  readonly [Type in keyof HostPayloads]: {
    readonly version: 1;
    readonly type: Type;
    readonly requestId: string;
    readonly sessionId?: string;
    readonly generation?: string;
    readonly payload: HostPayloads[Type];
  }
}[keyof HostPayloads];

const hostTypes = new Set<keyof HostPayloads>([
  'welcome',
  'snapshot.begin',
  'snapshot.rows',
  'snapshot.end',
  'screen.patch',
  'history.page',
  'history.changed',
  'control.changed',
  'input.accepted',
  'input.rejected',
  'session.state',
  'access.revoked',
  'error',
]);

const ajv = createProtocolAjv();
const validateEnvelope = ajv.compile(envelopeSchema);
const encoder = new TextEncoder();

export class InvalidProtocolMessageError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidProtocolMessageError';
  }
}

/** Parse and validate one host message before it reaches any display state. */
export function parseHostMessage(text: string): ValidatedHostMessage {
  const byteLength = encoder.encode(text).byteLength;
  if (byteLength === 0 || byteLength > MAX_HOST_MESSAGE_BYTES) {
    throw new InvalidProtocolMessageError(
      `Host message size ${byteLength} is outside 1..${MAX_HOST_MESSAGE_BYTES} bytes`,
    );
  }

  let value: unknown;
  try {
    value = JSON.parse(text) as unknown;
  } catch {
    throw new InvalidProtocolMessageError('Host message is not valid JSON');
  }

  if (!validateEnvelope(value)) {
    const details = validateEnvelope.errors
      ?.slice(0, 3)
      .map((error) => `${error.instancePath || '/'} ${error.message ?? 'is invalid'}`)
      .join('; ');
    throw new InvalidProtocolMessageError(`Host message violates protocol v1${details ? `: ${details}` : ''}`);
  }

  const message = value as { readonly type: string };
  if (!hostTypes.has(message.type as keyof HostPayloads)) {
    throw new InvalidProtocolMessageError(`Message type ${message.type} is not host-to-client`);
  }
  return value as ValidatedHostMessage;
}
