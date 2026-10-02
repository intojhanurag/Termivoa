import type {
  BrokerRPCSessionRecord,
  HTTPComputer,
  HTTPComputerAddResponse,
  HTTPComputerListResponse,
  HTTPDeviceSummary,
  HTTPErrorResponse,
  HTTPNamedOption,
  HTTPPairPollResponse,
  HTTPPairRedeemResponse,
  HTTPPreviewConfigResponse,
  HTTPPushConfigResponse,
  HTTPPushTestResponse,
  HTTPSessionListResponse,
  HTTPSessionMutationResponse,
  HTTPSettingsResponse,
  HTTPUploadResponse,
} from '../generated/protocol';
// Standalone code generated at build/test time by vite.config.ts, from the same
// lenient Ajv setup as the WebSocket envelope (validate.ts).
import { httpValidators } from 'virtual:http-validators';

/** The HTTP bodies the app reads, by schema name. Validators are built for each one (vite.config.ts). */
export interface HttpBodies {
  readonly settingsResponse: HTTPSettingsResponse;
  readonly namedOption: HTTPNamedOption;
  readonly sessionListResponse: HTTPSessionListResponse;
  readonly sessionRecord: BrokerRPCSessionRecord;
  readonly sessionMutationResponse: HTTPSessionMutationResponse;
  readonly deviceSummary: HTTPDeviceSummary;
  readonly pairRedeemResponse: HTTPPairRedeemResponse;
  readonly pairPollResponse: HTTPPairPollResponse;
  readonly previewConfigResponse: HTTPPreviewConfigResponse;
  readonly pushConfigResponse: HTTPPushConfigResponse;
  readonly pushTestResponse: HTTPPushTestResponse;
  readonly uploadResponse: HTTPUploadResponse;
  readonly computerListResponse: HTTPComputerListResponse;
  readonly computer: HTTPComputer;
  readonly computerAddResponse: HTTPComputerAddResponse;
  readonly errorResponse: HTTPErrorResponse;
}

export type HttpSchema = keyof HttpBodies;

/** Lists checked item by item: an item that fails is dropped, the rest of the list stays. */
const ITEMS: { readonly [Name in HttpSchema]?: ReadonlyArray<readonly [field: string, item: HttpSchema]> } = {
  settingsResponse: [['projects', 'namedOption'], ['shellProfiles', 'namedOption']],
  sessionListResponse: [['sessions', 'sessionRecord']],
  computerListResponse: [['computers', 'computer']],
};

export class InvalidResponseError extends Error {
  constructor(name: string) {
    super(`Response violates ${name}`);
    this.name = 'InvalidResponseError';
  }
}

/**
 * Validates an HTTP response body in place (unknown fields are removed) and
 * returns it typed. Each item is validated once: the whole body first, and
 * only when that fails are the lists in ITEMS filtered item by item and the
 * body checked again, so one session with, say, a new state value hides only
 * that session.
 */
export function parseHttpBody<Name extends HttpSchema>(name: Name, value: unknown): HttpBodies[Name] {
  if (httpValidators[name](value)) {
    return value as HttpBodies[Name];
  }
  const lists = ITEMS[name] ?? [];
  if (lists.length === 0 || typeof value !== 'object' || value === null) {
    throw new InvalidResponseError(name);
  }
  const record = value as Record<string, unknown>;
  for (const [field, item] of lists) {
    const list = record[field];
    if (Array.isArray(list)) {
      record[field] = list.filter((entry) => httpValidators[item](entry));
    }
  }
  if (!httpValidators[name](value)) {
    throw new InvalidResponseError(name);
  }
  return value as HttpBodies[Name];
}
