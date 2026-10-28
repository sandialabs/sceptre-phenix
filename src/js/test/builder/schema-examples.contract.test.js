// Every example of the bundle is valid against the schema it documents, under
// ajv 2020-12 as the Inspector compiles it (Go checks the examples exist).

import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { describe, expect, test } from 'vitest';

import bundle from '@/builder/schema/builder-v1.schema.json';

// The phenix config schemas the bundle carries, which document themselves.
const PHENIX_PREFIXES = ['phenix.v1.', 'phenix.v2.'];

// A key as a segment of a JSON pointer.
function segment(key) {
  return String(key).replaceAll('~', '~0').replaceAll('/', '~1');
}

// The definitions and properties the Builder owns, by JSON pointer, as
// TestSchemaDocumentsEveryProperty walks them.
function documentedParts() {
  const parts = [];

  const walk = (schema, pointer, own) => {
    if (!schema || typeof schema !== 'object' || Array.isArray(schema)) {
      return;
    }

    if (own) {
      parts.push({ pointer, schema });
    }

    Object.entries(schema.properties || {}).forEach(([name, property]) =>
      walk(property, `${pointer}/properties/${segment(name)}`, true),
    );
    ['items', 'additionalProperties'].forEach((key) =>
      walk(schema[key], `${pointer}/${key}`, false),
    );
  };

  Object.entries(bundle.properties).forEach(([name, property]) =>
    walk(property, `/properties/${segment(name)}`, true),
  );
  Object.entries(bundle.$defs)
    .filter(
      ([name]) => !PHENIX_PREFIXES.some((prefix) => name.startsWith(prefix)),
    )
    .forEach(([name, def]) => walk(def, `/$defs/${segment(name)}`, true));

  return parts;
}

describe('the examples of the schema bundle', () => {
  const ajv = new Ajv2020({ allErrors: true, strict: false });

  addFormats(ajv);
  ajv.addSchema(bundle);

  const parts = documentedParts();

  test('walks the root, the metadata and every node kind at least', () => {
    expect(parts.length).toBeGreaterThan(100);
  });

  test.each(parts.map((part) => [part.pointer, part]))(
    '%s',
    (pointer, { schema }) => {
      const validate = ajv.compile({ $ref: `${bundle.$id}#${pointer}` });

      (schema.examples || []).forEach((example, index) => {
        expect(
          validate(example),
          `example ${index} of ${pointer}: ${JSON.stringify(validate.errors)}`,
        ).toBe(true);
      });
    },
  );

  test('a value outside a documented schema is refused, so the check means something', () => {
    const validate = ajv.compile({
      $ref: `${bundle.$id}#/$defs/metadata/properties/notes`,
    });

    expect(validate(['a note'])).toBe(true);
    expect(validate([' '])).toBe(false);
    expect(validate(Array(101).fill('note'))).toBe(false);
  });
});
