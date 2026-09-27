# Broker management RPC v1

`broker-rpc.schema.json` is the canonical session-management contract used on
the restricted local IPC connection after `ipc.hello` / `ipc.welcome` succeeds.
Every JSON message is one payload inside the existing four-byte big-endian
length-prefixed transport. The transport limits remain 256 KiB toward the
broker and 1 MiB away from it.

The envelope contains `version`, `type`, `requestId`, `payload`, and session
identity where required. Every UUID uses lowercase canonical spelling.
`requestId` correlates one response to one request.
It is not an idempotency key. Create, rename, and terminate each carry a
separate canonical UUID `operationId`, echoed by successful mutation results,
so the broker can durably deduplicate a retry after the caller loses a response.

| Request | Result | Session identity | Payload boundary |
| --- | --- | --- | --- |
| `session.list.request` | `session.list.result` | none | Pages contain at most 200 records; cursors are opaque bounded base64url text. |
| `session.create.request` | `session.create.result` | none | Request contains only `operationId`, saved `projectId`, and saved `shellProfileId`; the result record supplies the new identity. |
| `session.rename.request` | `session.rename.result` | required | Mutation includes `operationId`; a requested title is trimmed, control-free display text up to 128 UTF-8 bytes. Returned records preserve the store's existing 1..256-byte, NUL-free title contract. |
| `session.terminate.request` | `session.terminate.result` | required | Mutation includes `operationId`; terminate remains distinct from detach and Ctrl+C. |
| `session.status.request` | `session.status.result` | required | Empty request payload; result returns one bounded lifecycle record. |

Network-facing callers never supply executable paths, arguments, working
directories, or environment values. The broker resolves the saved project and
shell-profile IDs from broker-owned storage before constructing a process spec.
Those unsafe fields are absent from the schema and strict decoding rejects them
as unknown fields.

The broker replies with the matching `*.result` type or `broker.error`. Errors
reuse the canonical stable v1 code enum. Management-specific additions are
`invalid_request`, `operation_conflict`, `project_missing`, and
`shell_profile_missing`; common codes such as `protocol_mismatch`,
`session_missing`, `generation_changed`, `session_lost`, `limit_exceeded`,
`host_unavailable`, and `internal_error` retain their existing meanings. Error
messages are bounded display text and must not contain stack traces, secrets,
paths, environment values, or terminal content.

The Go decoder additionally rejects wrong-direction messages, unknown or
duplicate JSON fields (including case-variant shadows), duplicate session
identities in one page, non-canonical UUIDs, invalid lifecycle/timestamp
combinations, mismatched result identities, oversized payloads, and trailing
JSON values. The canonical schema independently requires both session identity
fields for rename/terminate/status, forbids them for list/create, and permits a
broker error to carry either both fields or neither. The TypeScript Ajv factory
implements the schema's `maxUtf8Bytes` and `safeDisplayText` annotations so
non-ASCII request and record titles have the same byte boundaries as Go.

These validators deliberately validate one message at a time. The IPC
client (`internal/localclient`) retains in-flight request state and rejects a
response unless its `requestId` matches, its result type corresponds to the
request type, and a mutation result's `operationId` matches the request. That
cross-message correlation lives in the client, not in these validators.
A protocol-valid request is not authorization: later dispatch must still apply
the authenticated caller's permissions and resolve every saved ID inside the
broker.
