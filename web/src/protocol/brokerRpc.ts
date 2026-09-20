import brokerRPCSchema from '../../../protocol/v1/broker-rpc.schema.json';
import envelopeSchema from '../../../protocol/v1/envelope.schema.json';
import type { BrokerRPCMessageType } from '../generated/protocol';
import { createProtocolAjv } from './ajv';

export type ValidatedBrokerRPCMessage = {
  readonly version: 1;
  readonly type: BrokerRPCMessageType;
  readonly requestId: string;
  readonly sessionId?: string;
  readonly generation?: string;
  readonly payload: Readonly<Record<string, unknown>>;
};

const ajv = createProtocolAjv();
ajv.addSchema(envelopeSchema);

/** Validate a management message, including UTF-8 byte and safe-text rules. */
export const validateBrokerRPCMessage = ajv.compile<ValidatedBrokerRPCMessage>(brokerRPCSchema);
