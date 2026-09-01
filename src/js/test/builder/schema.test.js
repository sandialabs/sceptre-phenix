import { describe, expect, test } from 'vitest';

import { createFormValidator } from '@/builder/form-validator.js';
import {
  builderSchemaV1,
  BUILDER_SCHEMA_ID,
  deref,
  EXTERNAL_NODE_DEF,
  MINIMEGA_NODE_DEF,
  normalizeSchemaBundle,
  resolveRef,
  schemaForKind,
  specDefName,
  specSchema,
} from '@/builder/schema.js';

describe('bundled builder schema', () => {
  test('is the server schema, keyed by $defs', () => {
    expect(builderSchemaV1.$id).toBe(BUILDER_SCHEMA_ID);
    expect(builderSchemaV1.$schema).toContain('2020-12');
    expect(builderSchemaV1.$defs).toBeTruthy();
    expect(builderSchemaV1.definitions).toBeUndefined();
  });

  test('carries the phenix node schemas the inspector needs', () => {
    expect(builderSchemaV1.$defs[MINIMEGA_NODE_DEF]).toBeTruthy();
    expect(builderSchemaV1.$defs[EXTERNAL_NODE_DEF]).toBeTruthy();
  });

  test('an unusable server payload falls back to the bundle', () => {
    expect(normalizeSchemaBundle(null)).toBe(builderSchemaV1);
    expect(normalizeSchemaBundle({ definitions: {} })).toBe(builderSchemaV1);
    expect(normalizeSchemaBundle({ $defs: { a: {} } }).$defs.a).toEqual({});
  });

  test('refs resolve inside the bundle', () => {
    const resolved = resolveRef(
      builderSchemaV1,
      `#/$defs/${MINIMEGA_NODE_DEF}`,
    );

    expect(resolved.type).toBe('object');
    expect(deref(builderSchemaV1, { $ref: '#/$defs/network' }).type).toBe(
      'object',
    );
  });
});

describe('inspector schemas', () => {
  test('a device exposes the complete phenix node spec', () => {
    const schema = schemaForKind(builderSchemaV1, 'device');
    const spec = schema.properties.spec;

    expect(schema.required).toContain('spec');
    expect(Object.keys(spec.properties)).toEqual(
      expect.arrayContaining(['general', 'hardware', 'network']),
    );
    expect(schema.$defs).toBeTruthy();
  });

  test('external nodes get the external node schema', () => {
    expect(specDefName({ external: true })).toBe(EXTERNAL_NODE_DEF);
    expect(specDefName({ type: 'VirtualMachine' })).toBe(MINIMEGA_NODE_DEF);

    const schema = specSchema(builderSchemaV1, { external: true });

    expect(schema.title).toBe('phenix node spec');
    expect(schema.properties).toBeTruthy();
  });

  test('a switch edits its network, including the VLAN alias', () => {
    const schema = schemaForKind(builderSchemaV1, 'switch');

    expect(schema.title).toBe('Network');
    expect(schema.required).toContain('name');
    expect(schema.properties.alias.title).toBe('VLAN alias');
  });

  test('notes, groups, edges and the document each have a schema', () => {
    ['note', 'group', 'edge', 'document'].forEach((kind) => {
      const schema = schemaForKind(builderSchemaV1, kind);

      expect(schema.type).toBe('object');
      expect(Object.keys(schema.properties).length).toBeGreaterThan(0);
    });
  });

  test('identifiers and geometry are never editable fields', () => {
    ['device', 'switch', 'note', 'group', 'edge'].forEach((kind) => {
      const schema = schemaForKind(builderSchemaV1, kind);

      expect(schema.properties.id).toBeUndefined();
      expect(schema.properties.position).toBeUndefined();
      expect(schema.properties.parentId).toBeUndefined();
    });
  });

  test('every inspector schema compiles with its JSON Forms validator', () => {
    const validator = createFormValidator();

    ['device', 'switch', 'note', 'group', 'edge', 'document'].forEach(
      (kind) => {
        expect(() =>
          validator.compile(schemaForKind(builderSchemaV1, kind)),
        ).not.toThrow();
      },
    );
  });
});
