import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

export function createFormValidator() {
  const validator = new Ajv2020({
    allErrors: true,
    verbose: true,
    strict: false,
    addUsedSchema: false,
  });

  addFormats(validator);

  return validator;
}
