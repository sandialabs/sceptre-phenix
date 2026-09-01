import { describe, expect, test } from 'vitest';

import {
  applyLayout,
  computeLayout,
  LAYOUT_DEFAULTS,
} from '@/builder/layout.js';
import { groupNodes } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

describe('automatic layout', () => {
  test('is deterministic for the same document', () => {
    const { doc } = sampleDocument();

    expect(computeLayout(doc)).toEqual(computeLayout(doc));
  });

  test('does not depend on node ordering', () => {
    const { doc } = sampleDocument();
    const shuffled = { ...doc, nodes: [...doc.nodes].reverse() };

    expect(computeLayout(shuffled)).toEqual(computeLayout(doc));
  });

  test('snaps positions to the layout grid', () => {
    const { doc } = sampleDocument();
    const positions = Object.values(computeLayout(doc));

    positions.forEach((position) => {
      expect(position.x % LAYOUT_DEFAULTS.grid).toBe(0);
      expect(position.y % LAYOUT_DEFAULTS.grid).toBe(0);
    });
  });

  test('applying layout keeps every node and resizes groups around members', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const laid = applyLayout(grouped);
    const group = laid.nodes.find((node) => node.kind === 'group');

    expect(laid.nodes).toHaveLength(grouped.nodes.length);
    expect(group.size.width).toBeGreaterThan(0);
    expect(group.size.height).toBeGreaterThan(0);
  });

  test('direction is configurable', () => {
    const { doc } = sampleDocument();

    expect(computeLayout(doc, { direction: 'LR' })).not.toEqual(
      computeLayout(doc, { direction: 'TB' }),
    );
  });
});
