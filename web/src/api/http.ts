import type {
  ErrorCode,
  HTTPPairPollRequest,
  HTTPPairPollResponse,
  HTTPPairRedeemRequest,
  HTTPPairRedeemResponse,
  HTTPSessionCreateRequest,
  HTTPSessionListResponse,
  HTTPSessionMutationResponse,
  HTTPSessionRenameRequest,
  HTTPSessionTerminateRequest,
  HTTPSettingsResponse,
} from '../generated/protocol';

export const CONNECTION_UNAVAILABLE = 'Connection unavailable';

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

async function request<T>(method: 'GET' | 'POST' | 'PATCH', path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', cache: 'no-store' };
  if (method !== 'GET') {
    // Every mutation carries the JSON type and the fixed gateway header.
    init.headers = { 'Content-Type': 'application/json', 'X-Pterm-Request': '1' };
    init.body = JSON.stringify(body ?? {});
  }
  let response: Response;
  try {
    response = await fetch(path, init);
  } catch {
    throw new ApiError(0, 'network', CONNECTION_UNAVAILABLE);
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
  logout: () => request<undefined>('POST', '/api/v1/logout', {}),
  pairRedeem: (body: HTTPPairRedeemRequest) =>
    request<HTTPPairRedeemResponse>('POST', '/api/v1/pair/redeem', body),
  pairPoll: (body: HTTPPairPollRequest) =>
    request<HTTPPairPollResponse>('POST', '/api/v1/pair/poll', body),
};

export function isAuthFailure(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 401 || error.code === 'unauthorized' || error.code === 'revoked');
}
