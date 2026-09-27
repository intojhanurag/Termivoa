/// <reference types="vite/client" />

declare module 'virtual:protocol-validators' {
  import type { ValidateFunction } from 'ajv';

  export const validateEnvelope: ValidateFunction;
}
