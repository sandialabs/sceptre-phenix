import { describe, expect, test } from 'vitest';

import {
  BOX_GAP,
  seededRandom,
  seedOf,
  separateBoxes,
} from '@/builder/layouts/separate.js';

// Whether two boxes, by top-left corner, come nearer than `gap`.
function tooNear(a, b, gap) {
  return (
    a.x < b.x + b.width + gap - 1e-6 &&
    b.x < a.x + a.width + gap - 1e-6 &&
    a.y < b.y + b.height + gap - 1e-6 &&
    b.y < a.y + a.height + gap - 1e-6
  );
}

function expectApart(items, corners, gap = BOX_GAP) {
  const boxes = items.map((item) => ({ ...item, ...corners.get(item.id) }));

  for (const [index, a] of boxes.entries()) {
    for (const b of boxes.slice(index + 1)) {
      expect(tooNear(a, b, gap), `${a.id} and ${b.id}`).toBe(false);
    }
  }
}

describe('the random source of the point layouts', () => {
  test('gives the same numbers for the same seed', () => {
    const first = seededRandom(seedOf('a\nb'));
    const again = seededRandom(seedOf('a\nb'));
    const other = seededRandom(seedOf('a\nc'));
    const numbers = Array.from({ length: 5 }, first);

    expect(Array.from({ length: 5 }, again)).toEqual(numbers);
    expect(Array.from({ length: 5 }, other)).not.toEqual(numbers);
    for (const number of numbers) {
      expect(number).toBeGreaterThanOrEqual(0);
      expect(number).toBeLessThan(1);
    }
  });
});

describe('separateBoxes', () => {
  test('moves boxes on one point apart, the same way every time', () => {
    const items = Array.from({ length: 12 }, (_, index) => ({
      id: `n${index}`,
      width: 160,
      height: index % 3 ? 96 : 140,
    }));
    const centres = new Map(items.map((item) => [item.id, { x: 0, y: 0 }]));
    const corners = separateBoxes(items, centres);

    expectApart(items, corners);
    expect(separateBoxes(items, centres)).toEqual(corners);
  });

  test('leaves boxes that are apart where they are', () => {
    const items = [
      { id: 'a', width: 160, height: 96 },
      { id: 'b', width: 160, height: 96 },
    ];
    const corners = separateBoxes(
      items,
      new Map([
        ['a', { x: 80, y: 48 }],
        ['b', { x: 480, y: 48 }],
      ]),
    );

    expect(corners).toEqual(
      new Map([
        ['a', { x: 0, y: 0 }],
        ['b', { x: 400, y: 0 }],
      ]),
    );
  });

  test('separates a crowded cluster of many boxes', () => {
    const random = seededRandom(7);
    const items = Array.from({ length: 200 }, (_, index) => ({
      id: `n${index}`,
      width: 120 + Math.round(random() * 80),
      height: 64 + Math.round(random() * 80),
    }));
    const centres = new Map(
      items.map((item) => [item.id, { x: random() * 400, y: random() * 300 }]),
    );

    expectApart(items, separateBoxes(items, centres));
  });
});
