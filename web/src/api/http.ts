import type {
  ErrorCode,
  HTTPDeviceRenameRequest,
  HTTPDeviceSummary,
  HTTPPairPollRequest,
  HTTPPairPollResponse,
  HTTPPairRedeemRequest,
  HTTPPairRedeemResponse,
  HTTPPreviewConfigResponse,
  HTTPPushConfigResponse,
  HTTPPushSubscribeRequest,
  HTTPPushTestResponse,
  HTTPPushUnsubscribeRequest,
  HTTPSessionCreateRequest,
  HTTPSessionListResponse,
  HTTPSessionMutationResponse,
  HTTPSessionRenameRequest,
  HTTPSessionTerminateRequest,
  HTTPSettingsResponse,
  HTTPUploadRequest,
  HTTPUploadResponse,
  HTTPComputerAddRequest,
  HTTPComputerAddResponse,
  HTTPComputerListResponse,
} from '../generated/protocol';
import { MIN_PROTOCOL_REVISION, PROTOCOL_REVISION } from '../generated/protocol';
import { parseHttpBody, type HttpBodies, type HttpSchema } from '../protocol/httpBody';

export const CONNECTION_UNAVAILABLE = 'Connection unavailable';
export const CLIENT_BUILD = 'phone-terminal-web/0.0.0-private';
/** A request that has not answered by now is treated as a lost connection. */
export const REQUEST_TIMEOUT_MS = 15_000;
/**
 * Small reads answer in well under a second, even over a relay. Right after a
 * phone wakes, one can hang on a network that is not back yet; give up sooner
 * so the next try (which usually works) starts at once.
 */
export const READ_TIMEOUT_MS = 6_000;
/** A photo of up to 5 MiB can take a while on a slow phone link. */
export const UPLOAD_TIMEOUT_MS = 60_000;

const CONTROL = /\p{Cc}/u;
const EDGE_SPACE = /^\p{White_Space}|\p{White_Space}$/u;

/**
 * Mirrors the `deviceLabel` schema: 1–64 characters, at most 64 UTF-8 bytes,
 * no control characters, no leading or trailing whitespace. Returns a short
 * message for the first problem, or undefined when the label is valid.
 */
export function deviceLabelProblem(label: string): string | undefined {
  const length = [...label].length;
  if (length === 0) return 'Enter a name.';
  if (length > 64 || new TextEncoder().encode(label).byteLength > 64) return 'Use a shorter name (64 bytes at most).';
  if (CONTROL.test(label)) return 'Remove control characters from the name.';
  if (EDGE_SPACE.test(label)) return 'Remove spaces at the start or end.';
  return undefined;
}

/** Codes the app adds to the protocol's own error codes. */
export type ClientErrorCode =
  | 'network'
  | 'unknown'
  /** The body broke the HTTP API schema, or was larger than MAX_RESPONSE_BYTES. */
  | 'invalid_response'
  /** The computer's protocolRevision is below this app's MIN_PROTOCOL_REVISION. */
  | 'computer_outdated'
  /** The computer's minProtocolRevision is above this app's PROTOCOL_REVISION: update home. */
  | 'app_outdated';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode | ClientErrorCode,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** Shown for any refusal from a peer: its own error text is never displayed. */
export const PEER_REQUEST_FAILED = 'That computer could not do this.';
export const INVALID_RESPONSE = 'The computer sent a reply this app cannot read.';
/** The largest response body read from any computer. */
export const MAX_RESPONSE_BYTES = 1024 * 1024;

/** "Update Termivoa on vps". */
export const updateText = (label: string): string => `Update Termivoa on ${label}`;

/**
 * Request fields and modes newer than revision 1, by the protocol revision
 * that added them. Go decodes requests strictly, so send one to a computer
 * only when `supports(revision, feature)` is true.
 */
export const FEATURE_REVISION = {
  /** `session.attach` `{thumbnail: {maxUpdatesPerSecond}}`. */
  thumbnail: 1,
} as const;
export type Feature = keyof typeof FEATURE_REVISION;

export function supports(revision: number | undefined, feature: Feature): boolean {
  return revision !== undefined && revision >= FEATURE_REVISION[feature];
}

/** Why the app must refuse a computer at this revision, or undefined when it may use it. */
export function revisionProblem(
  settings: Pick<HTTPSettingsResponse, 'protocolRevision' | 'minProtocolRevision'>,
): 'computer_outdated' | 'app_outdated' | undefined {
  if (settings.protocolRevision < MIN_PROTOCOL_REVISION) return 'computer_outdated';
  if (settings.minProtocolRevision > PROTOCOL_REVISION) return 'app_outdated';
  return undefined;
}

/** The origin that served this app (the home computer); '' outside a browser. */
export function homeOrigin(): string {
  return globalThis.location?.origin ?? '';
}

/** The first DNS label of an origin: `https://vps.tailx.ts.net` is "vps". */
export function firstLabel(origin: string): string {
  try {
    return new URL(origin).hostname.split('.')[0] ?? origin;
  } catch {
    return origin;
  }
}

type Method = 'GET' | 'POST' | 'PATCH' | 'DELETE';

interface RequestOptions {
  readonly body?: unknown;
  readonly timeoutMs?: number;
  /** False for routes that still work on a refused (outdated) peer. */
  readonly gated?: boolean;
}

/** Reads at most `limit` bytes of a body, or throws. */
async function readText(response: Response, limit: number): Promise<string> {
  if (Number(response.headers.get('Content-Length') ?? 0) > limit) {
    throw new Error('too large');
  }
  const reader = response.body?.getReader();
  if (reader === undefined) return '';
  const decoder = new TextDecoder();
  let size = 0;
  let text = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) return text + decoder.decode();
    size += value.byteLength;
    if (size > limit) {
      await reader.cancel();
      throw new Error('too large');
    }
    text += decoder.decode(value, { stream: true });
  }
}

/**
 * The HTTP API of one computer. Every response is validated with the lenient
 * HTTP API schema validators (unknown fields dropped, wrong types refused) and
 * read up to MAX_RESPONSE_BYTES.
 */
export interface Client {
  /** The canonical origin, for example `https://vps.tailx.ts.net`. */
  readonly origin: string;
  /** True for the computer that served this app; requests stay same-origin. */
  readonly home: boolean;
  /** The protocol revision from the last good settings read; undefined until then (home: this build's). */
  revision(): number | undefined;
  /** Whether a newer request field or mode may be sent to this computer. */
  supports(feature: Feature): boolean;
  /**
   * Reads settings. On a peer, also checks the protocol revision: a peer out
   * of range throws ApiError `computer_outdated` or `app_outdated`, and every
   * later request but settings and pairing throws the same until a read passes.
   */
  settings(): Promise<HTTPSettingsResponse>;
  sessions(): Promise<HTTPSessionListResponse>;
  createSession(body: HTTPSessionCreateRequest): Promise<HTTPSessionMutationResponse>;
  renameSession(id: string, body: HTTPSessionRenameRequest): Promise<HTTPSessionMutationResponse>;
  terminateSession(id: string, body: HTTPSessionTerminateRequest): Promise<HTTPSessionMutationResponse>;
  renameDevice(body: HTTPDeviceRenameRequest): Promise<HTTPDeviceSummary>;
  logout(): Promise<undefined>;
  pairRedeem(body: HTTPPairRedeemRequest): Promise<HTTPPairRedeemResponse>;
  pairPoll(body: HTTPPairPollRequest): Promise<HTTPPairPollResponse>;
  previewConfig(): Promise<HTTPPreviewConfigResponse>;
  pushConfig(): Promise<HTTPPushConfigResponse>;
  pushSubscribe(body: HTTPPushSubscribeRequest): Promise<undefined>;
  pushUnsubscribe(body: HTTPPushUnsubscribeRequest): Promise<undefined>;
  pushTest(): Promise<HTTPPushTestResponse>;
  uploadImage(id: string, body: HTTPUploadRequest): Promise<HTTPUploadResponse>;
  /** Home only: this device's confirmed computers. */
  computers(): Promise<HTTPComputerListResponse>;
  /** Home only: records a pending computer, confirms it, or renames a confirmed one. */
  saveComputer(body: HTTPComputerAddRequest): Promise<HTTPComputerAddResponse>;
  /** Home only: removes `https://{host}` from this device's list. */
  deleteComputer(host: string): Promise<undefined>;
}

function createClient(origin: string, home: boolean): Client {
  let revision: number | undefined;
  let refusal: ApiError | undefined;

  /** Sends one request and returns the body of a 2xx answer as text. */
  async function call(method: Method, path: string, options: RequestOptions): Promise<{ status: number; text: string }> {
    if (options.gated !== false && refusal !== undefined) throw refusal;
    const init: RequestInit = home
      ? { method, credentials: 'same-origin', cache: 'no-store' }
      // A peer: its own cookie, and never a redirect to somewhere else.
      : { method, mode: 'cors', credentials: 'include', cache: 'no-store', redirect: 'error' };
    if (method !== 'GET') {
      // Every mutation carries the JSON type and the fixed gateway header.
      init.headers = { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' };
      init.body = JSON.stringify(options.body ?? {});
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), options.timeoutMs ?? REQUEST_TIMEOUT_MS);
    init.signal = controller.signal;
    let response: Response;
    let text: string;
    try {
      response = await fetch(home ? path : `${origin}${path}`, init);
      text = await readText(response, MAX_RESPONSE_BYTES);
    } catch (error) {
      if (error instanceof Error && error.message === 'too large') {
        throw new ApiError(0, 'invalid_response', INVALID_RESPONSE);
      }
      throw new ApiError(0, 'network', CONNECTION_UNAVAILABLE);
    } finally {
      clearTimeout(timer);
    }
    if (!response.ok) {
      throw failure(response.status, text);
    }
    return { status: response.status, text };
  }

  async function request<Name extends HttpSchema>(
    method: Method, path: string, schema: Name, options: RequestOptions = {},
  ): Promise<HttpBodies[Name]> {
    const { status, text } = await call(method, path, options);
    return parse(schema, status, text);
  }

  function parse<Name extends HttpSchema>(schema: Name, status: number, text: string): HttpBodies[Name] {
    try {
      return parseHttpBody(schema, JSON.parse(text));
    } catch {
      throw new ApiError(status, 'invalid_response', INVALID_RESPONSE);
    }
  }

  function refuse(problem: 'computer_outdated' | 'app_outdated'): never {
    revision = undefined;
    refusal = new ApiError(0, problem, updateText(firstLabel(problem === 'computer_outdated' ? origin : homeOrigin())));
    throw refusal;
  }

  /** For routes that answer 204: the body, if any, is ignored. */
  async function empty(method: Method, path: string, options: RequestOptions = {}): Promise<undefined> {
    await call(method, path, options);
    return undefined;
  }

  function failure(status: number, text: string): ApiError {
    let code: ApiError['code'] = 'unknown';
    let message = `Request failed (${status})`;
    try {
      const body = parseHttpBody('errorResponse', JSON.parse(text));
      code = body.error.code;
      message = body.error.message;
    } catch {
      // Keep the generic message; bodies are never rendered as markup.
    }
    // A peer's text could be phishing inside this app: show only our own words.
    return new ApiError(status, code, home ? message : PEER_REQUEST_FAILED);
  }

  const sessionPath = (id: string) => `/api/v1/sessions/${encodeURIComponent(id)}`;
  const read = { timeoutMs: READ_TIMEOUT_MS };
  const send = (body: unknown) => ({ body });

  return {
    origin,
    home,
    revision: () => revision ?? (home ? PROTOCOL_REVISION : undefined),
    supports(feature) {
      return supports(this.revision(), feature);
    },
    async settings() {
      const { status, text } = await call('GET', '/api/v1/settings', { ...read, gated: false });
      // A peer from before multi-computer sends no protocolRevision: it needs an update, not "can't reach".
      if (!home && !hasRevision(text)) refuse('computer_outdated');
      const settings = parse('settingsResponse', status, text);
      const problem = home ? undefined : revisionProblem(settings);
      if (problem !== undefined) refuse(problem);
      revision = settings.protocolRevision;
      refusal = undefined;
      return settings;
    },
    sessions: () => request('GET', '/api/v1/sessions', 'sessionListResponse', read),
    createSession: (body) => request('POST', '/api/v1/sessions', 'sessionMutationResponse', send(body)),
    renameSession: (id, body) => request('PATCH', sessionPath(id), 'sessionMutationResponse', send(body)),
    terminateSession: (id, body) => request('POST', `${sessionPath(id)}/terminate`, 'sessionMutationResponse', send(body)),
    renameDevice: (body) => request('PATCH', '/api/v1/device', 'deviceSummary', send(body)),
    // Logout and pairing work on an outdated peer, so it can always be removed or paired.
    logout: () => empty('POST', '/api/v1/logout', { body: {}, gated: false }),
    pairRedeem: (body) => request('POST', '/api/v1/pair/redeem', 'pairRedeemResponse', { body, gated: false }),
    pairPoll: (body) => request('POST', '/api/v1/pair/poll', 'pairPollResponse', { body, gated: false }),
    previewConfig: () => request('GET', '/api/v1/preview', 'previewConfigResponse'),
    pushConfig: () => request('GET', '/api/v1/push', 'pushConfigResponse'),
    pushSubscribe: (body) => empty('POST', '/api/v1/push/subscribe', send(body)),
    pushUnsubscribe: (body) => empty('POST', '/api/v1/push/unsubscribe', send(body)),
    pushTest: () => request('POST', '/api/v1/push/test', 'pushTestResponse', send({})),
    uploadImage: (id, body) => request('POST', `${sessionPath(id)}/uploads`, 'uploadResponse', { body, timeoutMs: UPLOAD_TIMEOUT_MS }),
    computers: () => request('GET', '/api/v1/computers', 'computerListResponse', read),
    saveComputer: (body) => request('POST', '/api/v1/computers', 'computerAddResponse', send(body)),
    deleteComputer: (host) => empty('DELETE', `/api/v1/computers/${encodeURIComponent(host)}`, send({})),
  };
}

/** True when a settings body is a JSON object with a numeric protocolRevision. */
function hasRevision(text: string): boolean {
  try {
    const body: unknown = JSON.parse(text);
    return typeof body === 'object' && body !== null && typeof (body as { protocolRevision?: unknown }).protocolRevision === 'number';
  } catch {
    return true; // Not JSON: the schema check reports it as invalid_response.
  }
}

const clients = new Map<string, Client>();

/**
 * The client for one computer by its canonical origin; undefined or the home
 * origin gives the home client. Clients are kept per origin, so each keeps
 * its computer's protocol revision.
 */
export function client(origin?: string): Client {
  const home = homeOrigin();
  const key = origin === undefined || origin === home ? '' : origin;
  let found = clients.get(key);
  if (found === undefined) {
    found = createClient(key === '' ? home : key, key === '');
    clients.set(key, found);
  }
  return found;
}

/** Drops a removed peer's client (and its revision). Home's client is kept. */
export function forgetClient(origin: string): void {
  if (origin !== homeOrigin()) clients.delete(origin);
}

/** The home computer's client: same-origin, as before multi-computer. */
export const api: Client = client();

/** Standard padded base64 of a blob, built in chunks so large files do not overflow the call stack. */
export async function blobToBase64(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let binary = '';
  for (let start = 0; start < bytes.length; start += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(start, start + 0x8000));
  }
  return btoa(binary);
}

export function isAuthFailure(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 401 || error.code === 'unauthorized' || error.code === 'revoked');
}
