import Ajv2020 from 'ajv/dist/2020';

const encoder = new TextEncoder();
const controlCharacter = /\p{Cc}/u;
const edgeWhitespace = /(?:^\p{White_Space})|(?:\p{White_Space}$)/u;

export function createProtocolAjv() {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  ajv.addKeyword({
    keyword: 'maxUtf8Bytes',
    schemaType: 'number',
    type: 'string',
    validate: (limit: number, value: string) => encoder.encode(value).byteLength <= limit,
  });
  ajv.addKeyword({
    keyword: 'safeDisplayText',
    schemaType: 'boolean',
    type: 'string',
    validate: (enabled: boolean, value: string) => !enabled || (
      !controlCharacter.test(value) && !edgeWhitespace.test(value)
    ),
  });
  return ajv;
}
