// The filter of the Drafts and Published Diagrams tabs (cardFilter.js).

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import {
  filterCards,
  filterItems,
  keepCardFilter,
  readCardFilter,
} from '@/builder/cardFilter.js';
import { layerSetSearch, templateSearch } from '@/builder/librarySearch.js';

import { memoryStorage } from './fixtures.js';

const DRAFTS = [
  {
    id: 'd1',
    title: 'Pump station',
    createdBy: 'alice',
    lastModifiedBy: 'bob',
  },
  { id: 'd2', title: 'Water lab', createdBy: 'erin', lastModifiedBy: 'erin' },
  { id: 'd3', title: 'Grid substation', createdBy: 'bob' },
];
const nameOf = (item) => item.title;
const ids = (items) => items.map((item) => item.id);

describe('filterCards', () => {
  test('an empty filter shows every card', () => {
    expect(filterCards(DRAFTS, '', nameOf)).toBe(DRAFTS);
    expect(filterCards(DRAFTS, '   ', nameOf)).toBe(DRAFTS);
  });

  test('matches the name by words, or by its letters in order', () => {
    expect(ids(filterCards(DRAFTS, 'station', nameOf))).toEqual(['d1', 'd3']);
    expect(ids(filterCards(DRAFTS, 'WATER', nameOf))).toEqual(['d2']);
    expect(ids(filterCards(DRAFTS, 'pmpst', nameOf))).toEqual(['d1']);
    expect(ids(filterCards(DRAFTS, 'zebra', nameOf))).toEqual([]);
  });

  test('matches who created the card and who changed it last', () => {
    expect(ids(filterCards(DRAFTS, 'bob', nameOf))).toEqual(['d1', 'd3']);
    expect(ids(filterCards(DRAFTS, 'erin', nameOf))).toEqual(['d2']);
    expect(ids(filterCards(DRAFTS, 'alice', nameOf))).toEqual(['d1']);
  });

  test('every word must match', () => {
    expect(ids(filterCards(DRAFTS, 'grid erin', nameOf))).toEqual([]);
    expect(ids(filterCards(DRAFTS, 'pump zebra', nameOf))).toEqual([]);
    expect(ids(filterCards(DRAFTS, 'grid sub', nameOf))).toEqual(['d3']);
  });

  test('the words can be spread over the name and the users', () => {
    expect(ids(filterCards(DRAFTS, 'pump alice', nameOf))).toEqual(['d1']);
    expect(ids(filterCards(DRAFTS, 'ALICE pump', nameOf))).toEqual(['d1']);
    expect(ids(filterCards(DRAFTS, 'station bob', nameOf))).toEqual([
      'd1',
      'd3',
    ]);
    expect(ids(filterCards(DRAFTS, 'grid bob', nameOf))).toEqual(['d3']);
    // Who created a draft and who changed it last, together.
    expect(ids(filterCards(DRAFTS, 'alice bob', nameOf))).toEqual(['d1']);
    expect(ids(filterCards(DRAFTS, 'lab erin', nameOf))).toEqual(['d2']);
    // A word is found inside a word, as elsewhere.
    expect(ids(filterCards(DRAFTS, 'ump lic', nameOf))).toEqual(['d1']);
  });

  test('a word that spans the name and a user name matches nothing', () => {
    expect(ids(filterCards(DRAFTS, 'stationalice', nameOf))).toEqual([]);
    expect(ids(filterCards(DRAFTS, 'labe', nameOf))).toEqual([]);
  });

  test('a card without users or a name is still listed or left out safely', () => {
    const items = [{ id: 'x' }, { id: 'y', title: 'Lab' }];

    expect(ids(filterCards(items, 'lab', (item) => item.title || ''))).toEqual([
      'y',
    ]);
  });

  const card = fc.record({
    id: fc.uuid(),
    title: fc.string({ maxLength: 12 }),
    createdBy: fc.option(fc.string({ maxLength: 8 }), { nil: undefined }),
    lastModifiedBy: fc.option(fc.string({ maxLength: 8 }), { nil: undefined }),
  });

  test('keeps the order of the list, and filtering again changes nothing', () => {
    fc.assert(
      fc.property(
        fc.array(card, { maxLength: 20 }),
        fc.string({ maxLength: 6 }),
        (items, query) => {
          const shown = filterCards(items, query, nameOf);
          const positions = shown.map((item) => items.indexOf(item));

          expect(positions.every((at) => at >= 0)).toBe(true);
          expect([...positions].sort((a, b) => a - b)).toEqual(positions);
          expect(filterCards(shown, query, nameOf)).toEqual(shown);
        },
      ),
      { numRuns: 200 },
    );
  });

  test('a word of the name with a user name finds the card', () => {
    const word = fc.stringMatching(/^[a-z]{1,6}$/);

    fc.assert(
      fc.property(word, word, word, (first, second, user) => {
        const item = {
          id: 'x',
          title: `${first} ${second}`,
          createdBy: user,
        };

        expect(filterCards([item], `${second} ${user}`, nameOf)).toEqual([
          item,
        ]);
        expect(filterCards([item], `${user} ${first}`, nameOf)).toEqual([item]);
      }),
      { numRuns: 200 },
    );
  });

  test('a card always matches its own name', () => {
    fc.assert(
      fc.property(
        fc.array(card, { minLength: 1, maxLength: 10 }),
        fc.nat(),
        (items, pick) => {
          const item = items[pick % items.length];

          expect(filterCards(items, item.title, nameOf)).toContain(item);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('filterItems', () => {
  const SETS = [
    { id: 'purdue', name: 'Purdue Model', description: 'ISA-95 levels' },
    { id: 'plant', name: 'Plant zones', description: 'Water plant' },
    { id: 'office', name: 'Office' },
  ];

  test('finds a layer set by its name or its description', () => {
    expect(ids(filterItems(SETS, 'office', layerSetSearch))).toEqual([
      'office',
    ]);
    expect(ids(filterItems(SETS, 'water', layerSetSearch))).toEqual(['plant']);
    expect(ids(filterItems(SETS, 'isa', layerSetSearch))).toEqual(['purdue']);
    expect(ids(filterItems(SETS, 'zebra', layerSetSearch))).toEqual([]);
    expect(filterItems(SETS, ' ', layerSetSearch)).toBe(SETS);
  });

  test('finds a template by the fields that t: in the palette searches', () => {
    const library = {
      collections: [
        {
          id: 'c1',
          source: 'library',
          name: 'Pumps',
          templateIds: ['plc'],
        },
      ],
    };
    const templates = [
      {
        id: 'plc',
        source: 'library',
        name: 'PLC',
        description: 'A controller',
        updatedBy: 'alice',
        device: {
          iconKey: 'plc',
          spec: {
            type: 'VirtualMachine',
            general: { hostname: 'plc-1' },
            hardware: { drives: [{ image: 'plc.qc2' }] },
          },
        },
      },
      { id: 'router', source: 'builtin', name: 'Router', device: {} },
    ];
    const find = (query) =>
      ids(
        filterItems(templates, query, (template) =>
          templateSearch(template, library),
        ),
      );

    expect(find('controller')).toEqual(['plc']);
    expect(find('pumps')).toEqual(['plc']);
    expect(find('plc.qc2')).toEqual(['plc']);
    expect(find('alice')).toEqual(['plc']);
    expect(find('built-in')).toEqual(['router']);
    expect(find('router')).toEqual(['router']);
  });

  test('keeps the order of the list, as filterCards does', () => {
    const set = fc.record({
      id: fc.uuid(),
      name: fc.string({ maxLength: 12 }),
      description: fc.option(fc.string({ maxLength: 12 }), { nil: undefined }),
    });

    fc.assert(
      fc.property(
        fc.array(set, { maxLength: 20 }),
        fc.string({ maxLength: 6 }),
        (items, query) => {
          const shown = filterItems(items, query, layerSetSearch);
          const positions = shown.map((item) => items.indexOf(item));

          expect([...positions].sort((a, b) => a - b)).toEqual(positions);
          expect(filterItems(shown, query, layerSetSearch)).toEqual(shown);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('the kept filter', () => {
  test('is kept per tab, and an empty filter is removed', () => {
    const storage = memoryStorage();

    keepCardFilter('drafts', 'pump', storage);
    keepCardFilter('published', 'lab', storage);
    expect(readCardFilter('drafts', storage)).toBe('pump');
    expect(readCardFilter('published', storage)).toBe('lab');
    expect([...storage.entries.keys()]).toEqual([
      'phenix.builder.filter.drafts',
      'phenix.builder.filter.published',
    ]);

    keepCardFilter('drafts', '', storage);
    expect(readCardFilter('drafts', storage)).toBe('');
    expect(storage.entries.has('phenix.builder.filter.drafts')).toBe(false);
  });

  test('blocked or missing storage keeps nothing and throws nothing', () => {
    const blocked = {
      getItem() {
        throw new Error('blocked');
      },
      setItem() {
        throw new Error('blocked');
      },
      removeItem() {
        throw new Error('blocked');
      },
    };

    expect(() => keepCardFilter('drafts', 'pump', blocked)).not.toThrow();
    expect(() => keepCardFilter('drafts', '', blocked)).not.toThrow();
    expect(readCardFilter('drafts', blocked)).toBe('');
    expect(readCardFilter('drafts', null)).toBe('');
    expect(() => keepCardFilter('drafts', 'pump', null)).not.toThrow();
  });
});
