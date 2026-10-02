import Ajv2020, { _, type KeywordCxt } from 'ajv/dist/2020';
import { usePattern } from 'ajv/dist/vocabularies/code';

/**
 * Custom keywords are code generators (not runtime `validate` callbacks) so
 * the same Ajv setup can emit standalone validators. The browser only loads
 * the build-time standalone output and never compiles code at runtime, which
 * a CSP without 'unsafe-eval' would block.
 *
 * `lenient` drops unknown fields (where a schema says `additionalProperties:
 * false`) instead of failing, but still rejects wrong types and unknown enum
 * values, so computers on a newer protocol revision stay readable. It relies
 * on `discriminator: true`: without it, the envelope's top-level `oneOf` would
 * strip fields while trying the wrong branches.
 */
export function createProtocolAjv(options: { readonly standalone?: boolean; readonly lenient?: boolean } = {}) {
  const ajv = new Ajv2020({
    // Stop at the first error: the app never shows an error list (a refused
    // message only closes the socket or fails the request), and the
    // standalone validators are smaller and faster this way.
    allErrors: false,
    strict: true,
    discriminator: true,
    ...(options.lenient === true ? { removeAdditional: true } : {}),
    ...(options.standalone === true ? { code: { source: true, esm: true } } : {}),
  });
  ajv.addKeyword({
    keyword: 'maxUtf8Bytes',
    schemaType: 'number',
    type: 'string',
    code(cxt: KeywordCxt) {
      cxt.fail(_`new TextEncoder().encode(${cxt.data}).byteLength > ${cxt.schema as number}`);
    },
  });
  ajv.addKeyword({
    keyword: 'safeDisplayText',
    schemaType: 'boolean',
    type: 'string',
    code(cxt: KeywordCxt) {
      if (cxt.schema !== true) {
        return;
      }
      const control = usePattern(cxt, '\\p{Cc}');
      const edgeWhitespace = usePattern(cxt, '(?:^\\p{White_Space})|(?:\\p{White_Space}$)');
      cxt.fail(_`${control}.test(${cxt.data}) || ${edgeWhitespace}.test(${cxt.data})`);
    },
  });
  return ajv;
}
