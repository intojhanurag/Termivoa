/// <reference types="vite/client" />

declare module 'virtual:protocol-validators' {
  import type { ValidateFunction } from 'ajv';

  /** Lenient: unknown fields are removed in place; wrong types and enum values fail. */
  export const validateEnvelope: ValidateFunction;
}

declare module 'virtual:http-validators' {
  import type { ValidateFunction } from 'ajv';

  /** Lenient like validateEnvelope; one per HttpBodies name. */
  export const httpValidators: Readonly<Record<import('./protocol/httpBody').HttpSchema, ValidateFunction>>;
}
