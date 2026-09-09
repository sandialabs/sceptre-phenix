import { describe, expect, test } from 'vitest';

import { createDocument } from '@/builder/model.js';
import {
  isValidDocument,
  MAX_VLAN_ALIAS,
  validateDocument,
} from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

function errorsFor(doc) {
  return validateDocument(doc).filter((issue) => issue.level === 'error');
}

function paths(doc) {
  return errorsFor(doc).map((issue) => issue.path);
}

describe('document validation', () => {
  test('a well formed document has no errors', () => {
    const { doc } = sampleDocument();

    expect(errorsFor(doc)).toEqual([]);
    expect(isValidDocument(doc)).toBe(true);
  });

  test('the schema URI and revision are checked', () => {
    const doc = { ...createDocument(), $schema: 'other', revision: 2 };

    expect(paths(doc)).toEqual(expect.arrayContaining(['$schema', 'revision']));
  });

  test('issues are sorted by path', () => {
    const doc = { ...createDocument(), $schema: '', revision: 0, id: '' };
    const sorted = [...paths(doc)].sort();

    expect(paths(doc)).toEqual(sorted);
  });

  test('network names must be unique and whitespace free', () => {
    const doc = {
      ...createDocument(),
      networks: [
        { id: 'n1', name: 'EXP' },
        { id: 'n2', name: 'exp' },
        { id: 'n3', name: 'has space' },
      ],
    };

    expect(paths(doc)).toEqual(
      expect.arrayContaining(['networks[1].name', 'networks[2].name']),
    );
  });

  test('vlan aliases are bounded', () => {
    const doc = {
      ...createDocument(),
      networks: [
        { id: 'n1', name: 'A', alias: 0 },
        { id: 'n2', name: 'B', alias: MAX_VLAN_ALIAS + 1 },
      ],
    };

    expect(paths(doc)).toEqual(
      expect.arrayContaining(['networks[0].alias', 'networks[1].alias']),
    );
  });

  test('an edge must join a device to a switch', () => {
    const { doc, alpha, bravo, network } = sampleDocument();
    const broken = {
      ...doc,
      edges: [
        {
          id: 'e-bad',
          sourceNodeId: alpha.id,
          targetNodeId: bravo.id,
          networkId: network.id,
        },
      ],
    };

    expect(
      errorsFor(broken)
        .map((issue) => issue.message)
        .join(' '),
    ).toMatch(/switch/i);
  });

  test('an edge network must match the switch network', () => {
    const { doc, edge } = sampleDocument();
    const broken = {
      ...doc,
      networks: [...doc.networks, { id: 'other', name: 'OTHER' }],
      edges: [{ ...edge, networkId: 'other' }],
    };

    expect(paths(broken)).toEqual(
      expect.arrayContaining(['edges[0].networkId']),
    );
  });

  test('a handle may only be used by one edge', () => {
    const { doc, edge } = sampleDocument();
    const broken = {
      ...doc,
      edges: [edge, { ...edge, id: 'e-dup' }],
    };

    expect(errorsFor(broken).length).toBeGreaterThan(0);
  });

  test('unknown icon keys are rejected', () => {
    const { doc, alpha } = sampleDocument();
    const broken = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, iconKey: 'nope' } }
          : node,
      ),
    };

    expect(
      errorsFor(broken)
        .map((issue) => issue.message)
        .join(' '),
    ).toMatch(/icon/i);
  });

  test('editing warnings do not block saving', () => {
    const { doc, bravo } = sampleDocument();
    const issues = validateDocument(doc);
    const warnings = issues.filter((issue) => issue.level === 'warning');

    expect(bravo).toBeTruthy();
    expect(warnings.length).toBeGreaterThan(0);
    expect(isValidDocument(doc)).toBe(true);
  });
});
