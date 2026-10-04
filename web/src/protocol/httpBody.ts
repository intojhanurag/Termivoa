import type {
  BrokerRPCSessionRecord,
  HTTPComputer,
  HTTPComputerAddResponse,
  HTTPComputerFoundResponse,
  HTTPComputerListResponse,
  HTTPDeviceSummary,
  HTTPErrorResponse,
  HTTPFoundComputer,
  HTTPNamedOption,
  HTTPPairPollResponse,
  HTTPPairRedeemResponse,
  HTTPPreviewConfigResponse,
  HTTPProviderCreateResponse,
  HTTPProviderGroup,
  HTTPProviderInfo,
  HTTPProviderItem,
  HTTPProviderOpenResponse,
  HTTPProviderSnapshotResponse,
  HTTPProviderTarget,
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
  readonly computerFoundResponse: HTTPComputerFoundResponse;
  readonly foundComputer: HTTPFoundComputer;
  readonly errorResponse: HTTPErrorResponse;
  readonly providerInfo: HTTPProviderInfo;
  readonly providerSnapshotResponse: HTTPProviderSnapshotResponse;
  readonly providerGroup: HTTPProviderGroup;
  readonly providerItem: HTTPProviderItem;
  readonly providerTarget: HTTPProviderTarget;
  readonly providerOpenResponse: HTTPProviderOpenResponse;
  readonly providerCreateResponse: HTTPProviderCreateResponse;
}

export type HttpSchema = keyof HttpBodies;

/** Lists checked item by item: an item that fails is dropped, the rest of the list stays. */
const ITEMS: { readonly [Name in HttpSchema]?: ReadonlyArray<readonly [field: string, item: HttpSchema]> } = {
  settingsResponse: [['projects', 'namedOption'], ['shellProfiles', 'namedOption'], ['providers', 'providerInfo']],
  sessionListResponse: [['sessions', 'sessionRecord']],
  computerListResponse: [['computers', 'computer']],
  computerFoundResponse: [['computers', 'foundComputer']],
  // A peer's provider list is checked per group, per item and per target: one bad target hides only itself.
  providerSnapshotResponse: [['groups', 'providerGroup']],
  providerGroup: [['items', 'providerItem']],
  providerItem: [['targets', 'providerTarget']],
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
      // An item with lists of its own is filtered the same way before it is judged.
      record[field] = list.filter((entry) => {
        try {
          parseHttpBody(item, entry);
          return true;
        } catch {
          return false;
        }
      });
    }
  }
  if (!httpValidators[name](value)) {
    throw new InvalidResponseError(name);
  }
  return value as HttpBodies[Name];
}
