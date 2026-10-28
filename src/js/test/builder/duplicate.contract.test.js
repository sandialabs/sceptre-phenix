// Duplicate draft: the copy's name and its diagram (duplicate.js).

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import { copyName, duplicateDocument } from '@/builder/duplicate.js';
import { MAX_NAME_BYTES, validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

const bytes = (text) => new TextEncoder().encode(text).length;

describe('copyName', () => {
  test('adds (copy), then a number while a draft has the name', () => {
    expect(copyName('Pump station')).toBe('Pump station (copy)');
    expect(copyName('Pump station', ['pump station (COPY)'])).toBe(
      'Pump station (copy 2)',
    );
    expect(
      copyName('Pump station', [
        'Pump station (copy)',
        ' Pump station (copy 2) ',
        'Pump station (copy 4)',
      ]),
    ).toBe('Pump station (copy 3)');
  });

  test('numbers the copy of a copy after the first copy', () => {
    expect(copyName('Lab (copy)', ['Lab (copy)'])).toBe('Lab (copy 2)');
    expect(copyName('Lab (copy 2)', ['Lab (copy)', 'Lab (copy 2)'])).toBe(
      'Lab (copy 3)',
    );
  });

  test('names a diagram without a name', () => {
    expect(copyName('')).toBe('Untitled diagram (copy)');
    expect(copyName('   ', ['Untitled diagram (copy)'])).toBe(
      'Untitled diagram (copy 2)',
    );
  });

  test('cuts a long name by whole characters to fit the limit', () => {
    const name = copyName('é'.repeat(400));

    expect(bytes(name)).toBeLessThanOrEqual(MAX_NAME_BYTES);
    expect(name).toMatch(/^é+ \(copy\)$/);
  });

  test('is never taken, ends in a copy suffix and fits the limit', () => {
    fc.assert(
      fc.property(
        fc.string({ maxLength: 600, size: 'max' }),
        fc.array(fc.string({ maxLength: 40 }), { maxLength: 10 }),
        fc.nat(5),
        (name, others, copies) => {
          // Other drafts, and copies made before this one.
          const taken = [...others];

          for (let n = 0; n < copies; n += 1) {
            taken.push(copyName(name, taken));
          }

          const result = copyName(name, taken);

          expect(
            taken.map((entry) => entry.trim().toLowerCase()),
          ).not.toContain(result.toLowerCase());
          expect(result).toMatch(/ \(copy(?: \d+)?\)$/);
          expect(bytes(result)).toBeLessThanOrEqual(MAX_NAME_BYTES);
        },
      ),
      { numRuns: 200 },
    );
  });
});

// The sample diagram as a draft imported from topology pump-station and
// saved by the server, which stamped it.
function importedDiagram() {
  const { doc } = sampleDocument();

  return {
    ...doc,
    metadata: {
      ...doc.metadata,
      name: 'Pump station',
      description: 'Two PLCs',
      notes: ['Snapshot the PLCs first.'],
      createdBy: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'bob',
      updatedAt: '2026-10-02T09:30:00Z',
    },
    source: {
      kind: 'topology',
      name: 'pump-station',
      apiVersion: 'phenix.sandia.gov/v1',
      digest: `sha256:${'1'.repeat(64)}`,
      importedAt: '2026-10-01T15:04:05Z',
      includeTopologies: ['shared'],
      unresolvedIncludes: ['legacy'],
      warnings: ['one warning'],
    },
  };
}

describe('duplicateDocument', () => {
  test('gives the copy a new id and name, and no stamp', () => {
    const doc = importedDiagram();
    const copy = duplicateDocument(doc, 'Pump station (copy)');

    expect(copy.metadata).toEqual({
      id: copy.metadata.id,
      name: 'Pump station (copy)',
      description: 'Two PLCs',
      notes: ['Snapshot the PLCs first.'],
    });
    expect(copy.metadata.id).not.toBe(doc.metadata.id);
    // The keys keep their order.
    expect(Object.keys(copy.metadata)).toEqual([
      'id',
      'name',
      'description',
      'notes',
    ]);
  });

  test('links the copy to no config, and keeps its included topologies', () => {
    const copy = duplicateDocument(importedDiagram(), 'Copy');

    expect(copy.source).toEqual({
      kind: 'manual',
      includeTopologies: ['shared'],
      unresolvedIncludes: ['legacy'],
    });
    expect(
      duplicateDocument({ ...importedDiagram(), source: undefined }, 'X')
        .source,
    ).toEqual({
      kind: 'manual',
    });
  });

  test('copies the diagram itself unchanged, and does not change the original', () => {
    const doc = importedDiagram();
    const before = structuredClone(doc);
    const copy = duplicateDocument(doc, 'Copy');

    expect(doc).toEqual(before);
    for (const key of ['nodes', 'edges', 'networks', 'layers', 'grid']) {
      expect(copy[key]).toEqual(doc[key]);
    }
    expect(
      validateDocument(copy).filter((issue) => issue.level === 'error'),
    ).toEqual(validateDocument(doc).filter((issue) => issue.level === 'error'));
  });
});
