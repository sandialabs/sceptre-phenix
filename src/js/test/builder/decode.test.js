import { describe, expect, test } from 'vitest';

import {
  decodeDocument,
  parseDocument,
  parseImport,
} from '@/builder/decode.js';
import { SCHEMA_URI } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

describe('strict decoding', () => {
  test('round trips a valid document', () => {
    const { doc } = sampleDocument();
    const decoded = parseDocument(JSON.parse(JSON.stringify(doc)));

    expect(decoded).toEqual(doc);
  });

  test('unknown document fields are rejected, never dropped', () => {
    const { doc } = sampleDocument();

    expect(() => decodeDocument({ ...doc, extra: true })).toThrowError(
      /unknown field/i,
    );
  });

  test('unknown node fields are rejected', () => {
    const { doc } = sampleDocument();
    const payload = JSON.parse(JSON.stringify(doc));

    payload.nodes[0].vlan = 'EXP';

    expect(() => decodeDocument(payload)).toThrowError(/unknown field/i);
  });

  test('accepts complete server source provenance', () => {
    const { doc } = sampleDocument();
    const payload = {
      ...doc,
      source: {
        kind: 'topology',
        name: 'generated',
        apiVersion: 'phenix.sandia.gov/v1',
        topology: 'base',
        importedAt: '2026-08-28T20:00:00Z',
        digest: `sha256:${'a'.repeat(64)}`,
        updatedAt: '2026-08-28T19:00:00Z',
        warnings: ['Generated layout'],
      },
    };

    expect(() => decodeDocument(payload)).not.toThrow();
  });

  test('the schema URI and revision are enforced', () => {
    const { doc } = sampleDocument();

    expect(() => decodeDocument({ ...doc, $schema: 'x' })).toThrowError(
      /schema/i,
    );
    expect(() => decodeDocument({ ...doc, revision: 2 })).toThrowError(
      /revision/i,
    );
    expect(SCHEMA_URI).toContain('/schemas/builder/v1');
  });

  test('a node must carry exactly one payload', () => {
    const { doc } = sampleDocument();
    const payload = JSON.parse(JSON.stringify(doc));

    payload.nodes[0].note = { text: 'x' };

    expect(() => decodeDocument(payload)).toThrowError(/exactly one/i);
  });
});

describe('import', () => {
  test('accepts JSON and YAML builder documents', () => {
    const { doc } = sampleDocument();
    const json = parseImport(JSON.stringify(doc));

    expect(json.ok).toBe(true);
    expect(json.document.id).toBe(doc.id);

    const yaml = parseImport(
      `$schema: ${doc.$schema}\nrevision: 1\nid: ${doc.id}\nnodes: []\nnetworks: []\nedges: []\nviewport: {x: 0, y: 0, zoom: 1}\ngrid: {enabled: true, size: 16, snap: true}\n`,
    );

    expect(yaml.ok).toBe(true);
  });

  test('refuses phenix Topology and Experiment configs with guidance', () => {
    const result = parseImport(
      JSON.stringify({ kind: 'Topology', metadata: { name: 'x' } }),
    );

    expect(result.ok).toBe(false);
    expect(result.code).toBe('unsupported-kind');
    expect(result.error).toMatch(/generate/i);
  });

  test('reports invalid documents instead of repairing them', () => {
    const { doc } = sampleDocument();
    const payload = JSON.parse(JSON.stringify(doc));

    payload.edges[0].networkId = 'missing';

    const result = parseImport(JSON.stringify(payload));

    expect(result.ok).toBe(false);
    expect(result.issues.length).toBeGreaterThan(0);
  });

  test('empty and unparseable input is named', () => {
    expect(parseImport('  ').code).toBe('empty');
    expect(parseImport('{"a": ').code).toBe('parse');
  });

  test('raw mode returns the parsed value for scenario uploads', () => {
    const result = parseImport(
      'apiVersion: phenix.sandia.gov/v1\nkind: Scenario\n',
      {
        as: 'raw',
      },
    );

    expect(result.ok).toBe(true);
    expect(result.value.kind).toBe('Scenario');
  });

  test('YAML imports stay JSON-compatible', () => {
    const result = parseImport('created: 2026-08-28T12:00:00Z\n', {
      as: 'raw',
    });

    expect(result.ok).toBe(true);
    expect(result.value.created).toBe('2026-08-28T12:00:00Z');
  });

  test('imports enforce size and expected config kind', () => {
    expect(parseImport('12345', { as: 'raw', maxBytes: 4 }).code).toBe(
      'too-large',
    );
    expect(
      parseImport('kind: Topology\n', {
        as: 'raw',
        expectedKind: 'Scenario',
      }).code,
    ).toBe('unsupported-kind');
  });
});
