import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import { createDocument } from '@/builder/model.js';
import { validateDocument } from '@/builder/validate.js';

function groupId(index) {
  return `00000000-0000-4000-8000-${String(index).padStart(12, '0')}`;
}

// A document of a group for each entry of parents, the index of the
// group's parent among them or -1 for none.
function withGroups(parents) {
  const doc = createDocument();

  doc.nodes = parents.map((parent, index) => ({
    id: groupId(index),
    kind: 'group',
    label: '',
    position: { x: index, y: 0 },
    group: { title: `group ${index}` },
    ...(parent >= 0 ? { parentId: groupId(parent) } : {}),
  }));

  return doc;
}

function cyclePaths(doc) {
  return validateDocument(doc)
    .filter((issue) => issue.code === 'node.parent.cycle')
    .map((issue) => issue.path)
    .sort();
}

// The groups whose chain of parents comes back to a group, found by a walk
// from each group.
function walkedCyclePaths(parents) {
  const paths = [];

  parents.forEach((parent, index) => {
    if (parent < 0 || parent === index) {
      return;
    }

    const seen = new Set([index]);

    for (let current = parent; current >= 0; current = parents[current]) {
      if (seen.has(current)) {
        paths.push(`nodes[${index}].parentId`);
        break;
      }

      seen.add(current);
    }
  });

  return paths.sort();
}

describe('group parents', () => {
  test('a cycle is reported at each group whose parents lead into it', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 12 }).chain((count) =>
          fc.array(fc.integer({ min: -1, max: count - 1 }), {
            minLength: count,
            maxLength: count,
          }),
        ),
        (parents) => {
          expect(cyclePaths(withGroups(parents))).toEqual(
            walkedCyclePaths(parents),
          );
        },
      ),
      { numRuns: 200 },
    );
  });

  // A walk from each group takes more than a minute here.
  test('a long chain of groups is checked in one pass', () => {
    const count = 50000;
    const doc = withGroups(Array.from({ length: count }, (_, i) => i - 1));

    expect(cyclePaths(doc)).toEqual([]);
  });
});
