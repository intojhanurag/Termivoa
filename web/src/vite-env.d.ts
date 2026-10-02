/// <reference types="vite/client" />

declare module 'virtual:protocol-validators' {
  import type { ValidateFunction } from 'ajv';

  /** Lenient: unknown fields are removed in place; wrong types and enum values fail. */
  export const validateEnvelope: ValidateFunction;
}

declare module 'virtual:http-validators' {
  import type { ValidateFunction } from 'ajv';

  /** Keep in step with HTTP_SCHEMAS in vite.config.ts. */
  export type HttpSchema =
    | 'settingsResponse' | 'namedOption' | 'sessionListResponse' | 'sessionRecord'
    | 'sessionMutationResponse' | 'deviceSummary' | 'pairRedeemResponse' | 'pairPollResponse'
    | 'previewConfigResponse' | 'pushConfigResponse' | 'pushTestResponse' | 'uploadResponse'
    | 'computerListResponse' | 'computer' | 'computerAddResponse' | 'errorResponse';

  /** Lenient like validateEnvelope. */
  export const httpValidators: Readonly<Record<HttpSchema, ValidateFunction>>;
}
