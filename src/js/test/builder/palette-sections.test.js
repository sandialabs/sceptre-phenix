// Which sections of templates the palette shows collapsed, as this browser
// keeps them (see paletteSections.js).

import { describe, expect, test } from 'vitest';

import {
  MAX_COLLAPSED_SECTIONS,
  PALETTE_STORAGE_KEY,
  loadCollapsedSections,
  saveCollapsedSections,
  toggledSections,
} from '@/builder/paletteSections.js';

import { memoryStorage } from './fixtures.js';

describe('the collapsed sections of the palette', () => {
  test('a press collapses a section, and a second press expands it', () => {
    expect(toggledSections([], 'library:plant')).toEqual(['library:plant']);
    expect(toggledSections(['library:plant'], 'library')).toEqual([
      'library:plant',
      'library',
    ]);
    expect(
      toggledSections(['library:plant', 'library'], 'library:plant'),
    ).toEqual(['library']);
  });

  test('they are kept by id, and an empty list removes the key', () => {
    const storage = memoryStorage();

    saveCollapsedSections(['library:plant', 'preloaded:kit'], storage);
    expect(JSON.parse(storage.getItem(PALETTE_STORAGE_KEY))).toEqual({
      collapsed: ['library:plant', 'preloaded:kit'],
    });
    expect(loadCollapsedSections(storage)).toEqual([
      'library:plant',
      'preloaded:kit',
    ]);

    saveCollapsedSections([], storage);
    expect(storage.getItem(PALETTE_STORAGE_KEY)).toBeNull();
    expect(loadCollapsedSections(storage)).toEqual([]);
  });

  test('only the newest ids are kept', () => {
    const storage = memoryStorage();
    const ids = Array.from(
      { length: MAX_COLLAPSED_SECTIONS + 2 },
      (_, index) => `library:c${index}`,
    );

    saveCollapsedSections(ids, storage);

    const kept = loadCollapsedSections(storage);

    expect(kept).toHaveLength(MAX_COLLAPSED_SECTIONS);
    expect(kept[0]).toBe('library:c2');
    expect(kept.at(-1)).toBe(ids.at(-1));
  });

  test('a value it did not write, or no storage, collapses nothing', () => {
    for (const value of ['not json', '{"collapsed": "other"}', '[1, 2]']) {
      expect(
        loadCollapsedSections(memoryStorage({ [PALETTE_STORAGE_KEY]: value })),
        value,
      ).toEqual([]);
    }

    // Ids that are not text are left out, and each id counts once.
    expect(
      loadCollapsedSections(
        memoryStorage({
          [PALETTE_STORAGE_KEY]: JSON.stringify({
            collapsed: ['library', 7, '', 'library', null],
          }),
        }),
      ),
    ).toEqual(['library']);

    expect(loadCollapsedSections(null)).toEqual([]);

    const blocked = {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('full');
      },
      removeItem: () => {
        throw new Error('blocked');
      },
    };

    expect(loadCollapsedSections(blocked)).toEqual([]);
    expect(() => saveCollapsedSections(['library'], blocked)).not.toThrow();
    expect(() => saveCollapsedSections([], blocked)).not.toThrow();
  });
});
