import Ajv2020, { _, type KeywordCxt } from 'ajv/dist/2020';
import { usePattern } from 'ajv/dist/vocabularies/code';

/**
 * Custom keywords are code generators (not runtime `validate` callbacks) so
 * the same Ajv setup can emit standalone validators. The browser only loads
 * the build-time standalone output and never compiles code at runtime, which
 * a CSP without 'unsafe-eval' would block.
 */
export function createProtocolAjv(options: { readonly standalone?: boolean } = {}) {
  const ajv = new Ajv2020({
    allErrors: true,
    strict: true,
    discriminator: true,
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
