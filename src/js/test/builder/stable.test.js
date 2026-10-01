import { describe, expect, test } from 'vitest';

import { keepUnchanged, sameValue } from '@/builder/stable.js';

describe('equal values', () => {
  test('arrays and plain objects are compared by their contents', () => {
    expect(
      sameValue(
        { a: [1, { b: 'x' }], c: null },
        { c: null, a: [1, { b: 'x' }] },
      ),
    ).toBe(true);
    expect(sameValue({ a: [1, 2] }, { a: [2, 1] })).toBe(false);
    expect(sameValue({ a: 1 }, { a: 1, b: undefined })).toBe(false);
    expect(sameValue([1], { 0: 1 })).toBe(false);
    expect(sameValue(Number.NaN, Number.NaN)).toBe(true);
  });

  test('anything else is equal only to itself', () => {
    const map = new Map([['a', 1]]);

    expect(sameValue(map, map)).toBe(true);
    expect(sameValue(map, new Map([['a', 1]]))).toBe(false);
    expect(sameValue(new Date(0), new Date(0))).toBe(false);
  });
});

describe('keeping unchanged items', () => {
  test('an item equal to the one it replaces is that same object', () => {
    const before = [
      { id: 'a', label: 'alpha', data: { handles: [{ id: 'h' }] } },
      { id: 'b', label: 'bravo' },
    ];
    const next = [
      { id: 'a', label: 'alpha', data: { handles: [{ id: 'h' }] } },
      { id: 'b', label: 'bravo 2' },
    ];
    const kept = keepUnchanged(next, before);

    expect(kept[0]).toBe(before[0]);
    expect(kept[1]).toBe(next[1]);
  });

  test('a list that changed nothing is the previous list itself', () => {
    const before = [{ id: 'a' }, { id: 'b' }];

    expect(keepUnchanged([{ id: 'a' }, { id: 'b' }], before)).toBe(before);
    // The same items in another order, or one fewer, are a new list.
    expect(keepUnchanged([{ id: 'b' }, { id: 'a' }], before)).not.toBe(before);
    expect(keepUnchanged([{ id: 'a' }], before)).toEqual([before[0]]);
    expect(keepUnchanged([{ id: 'a' }], undefined)).toEqual([{ id: 'a' }]);
  });

  test('nested items are kept the same way, and their parent when all of them are', () => {
    const before = [
      {
        id: 'group',
        children: [
          { id: 'a', children: [] },
          { id: 'b', children: [] },
        ],
      },
    ];
    const same = keepUnchanged(
      [
        {
          id: 'group',
          children: [
            { id: 'a', children: [] },
            { id: 'b', children: [] },
          ],
        },
      ],
      before,
      'children',
    );
    const renamed = keepUnchanged(
      [
        {
          id: 'group',
          children: [
            { id: 'a', children: [] },
            { id: 'b', label: 'new', children: [] },
          ],
        },
      ],
      before,
      'children',
    );

    expect(same).toBe(before);
    expect(renamed[0]).not.toBe(before[0]);
    expect(renamed[0].children[0]).toBe(before[0].children[0]);
    expect(renamed[0].children[1].label).toBe('new');
  });
});
