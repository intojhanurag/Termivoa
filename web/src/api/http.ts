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
} from '../generated/protocol';

export const CONNECTION_UNAVAILABLE = 'Connection unavailable';
export const CLIENT_BUILD = 'phone-terminal-web/0.0.0-private';
/** A request that has not answered by now is treated as a lost connection. */
export const REQUEST_TIMEOUT_MS = 15_000;
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

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode | 'network' | 'unknown',
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

async function request<T>(
  method: 'GET' | 'POST' | 'PATCH',
  path: string,
  body?: unknown,
  timeoutMs = REQUEST_TIMEOUT_MS,
): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', cache: 'no-store' };
  if (method !== 'GET') {
    // Every mutation carries the JSON type and the fixed gateway header.
    init.headers = { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' };
    init.body = JSON.stringify(body ?? {});
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  init.signal = controller.signal;
  let response: Response;
  try {
    response = await fetch(path, init);
  } catch {
    throw new ApiError(0, 'network', CONNECTION_UNAVAILABLE);
  } finally {
    clearTimeout(timer);
  }
  if (!response.ok) {
    let code: ApiError['code'] = 'unknown';
    let message = `Request failed (${response.status})`;
    try {
      const failure = await response.json() as { error?: { code?: ErrorCode; message?: string } };
      code = failure.error?.code ?? code;
      message = failure.error?.message ?? message;
    } catch {
      // Keep the generic message; bodies are never rendered as markup.
    }
    throw new ApiError(response.status, code, message);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return await response.json() as T;
}

const sessionPath = (id: string) => `/api/v1/sessions/${encodeURIComponent(id)}`;

export const api = {
  settings: () => request<HTTPSettingsResponse>('GET', '/api/v1/settings'),
  sessions: () => request<HTTPSessionListResponse>('GET', '/api/v1/sessions'),
  createSession: (body: HTTPSessionCreateRequest) =>
    request<HTTPSessionMutationResponse>('POST', '/api/v1/sessions', body),
  renameSession: (id: string, body: HTTPSessionRenameRequest) =>
    request<HTTPSessionMutationResponse>('PATCH', sessionPath(id), body),
  terminateSession: (id: string, body: HTTPSessionTerminateRequest) =>
    request<HTTPSessionMutationResponse>('POST', `${sessionPath(id)}/terminate`, body),
  renameDevice: (body: HTTPDeviceRenameRequest) =>
    request<HTTPDeviceSummary>('PATCH', '/api/v1/device', body),
  logout: () => request<undefined>('POST', '/api/v1/logout', {}),
  pairRedeem: (body: HTTPPairRedeemRequest) =>
    request<HTTPPairRedeemResponse>('POST', '/api/v1/pair/redeem', body),
  pairPoll: (body: HTTPPairPollRequest) =>
    request<HTTPPairPollResponse>('POST', '/api/v1/pair/poll', body),
  previewConfig: () => request<HTTPPreviewConfigResponse>('GET', '/api/v1/preview'),
  pushConfig: () => request<HTTPPushConfigResponse>('GET', '/api/v1/push'),
  pushSubscribe: (body: HTTPPushSubscribeRequest) => request<undefined>('POST', '/api/v1/push/subscribe', body),
  pushUnsubscribe: (body: HTTPPushUnsubscribeRequest) => request<undefined>('POST', '/api/v1/push/unsubscribe', body),
  pushTest: () => request<HTTPPushTestResponse>('POST', '/api/v1/push/test', {}),
  uploadImage: (id: string, body: HTTPUploadRequest) =>
    request<HTTPUploadResponse>('POST', `${sessionPath(id)}/uploads`, body, UPLOAD_TIMEOUT_MS),
};

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
