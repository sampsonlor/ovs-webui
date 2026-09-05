import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { schemas } from '../contracts/core-v0.1.mjs';

// Use this only in build tools, tests and the local integration service.
export function createContractValidator() {
  const ajv = new Ajv2020({
    allErrors: true,
    strict: true,
    code: { source: true, esm: true },
  });
  addFormats(ajv);
  for (const [name, schema] of Object.entries(schemas))
    ajv.addSchema(schema, `#/components/schemas/${name}`);
  return ajv;
}
