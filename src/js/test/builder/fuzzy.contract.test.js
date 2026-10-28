import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  fieldOrder,
  highlightParts,
  matchItem,
  matchName,
} from '@/builder/fuzzy.js';

describe('matching a title', () => {
  const matchTitle = (title, query) => matchItem({ title }, query);

  test('an empty query matches everything, with nothing marked', () => {
    expect(matchTitle('Undo', '')).toEqual({ score: 0, ranges: [] });
    expect(matchTitle('Undo', '   ')).toEqual({ score: 0, ranges: [] });
  });

  test('every word must appear, ignoring case', () => {
    expect(matchTitle('Add device', 'add dev')).toEqual({
      score: expect.any(Number),
      grade: expect.any(Number),
      ranges: [
        [0, 3],
        [4, 7],
      ],
    });
    expect(matchTitle('Add device', 'ADD')?.ranges).toEqual([[0, 3]]);
    expect(matchTitle('Add switch', 'add dev')).toBeNull();
  });

  test('the start of the text ranks first, then a word start, then anywhere', () => {
    const start = matchTitle('Layout', 'lay').score;
    const word = matchTitle('Auto layout', 'lay').score;
    const inside = matchTitle('Display', 'lay').score;

    expect(start).toBeGreaterThan(word);
    expect(word).toBeGreaterThan(inside);
  });

  test('a word start is marked rather than an earlier match inside a word', () => {
    expect(matchTitle('Ungroup group', 'group').ranges).toEqual([[8, 13]]);
  });

  test('otherwise the letters in order, closest together first', () => {
    const group = matchTitle('Group selection', 'grp');
    const ungroup = matchTitle('Ungroup', 'grp');

    expect(group.ranges).toEqual([
      [0, 2],
      [4, 5],
    ]);
    expect(group.score).toBeGreaterThan(ungroup.score);
    // Any word match outranks any letter match.
    expect(matchTitle('Display', 'lay').score).toBeGreaterThan(group.score);
    expect(matchTitle('Undo', 'zq')).toBeNull();
  });
});

describe('matchItem', () => {
  const node = {
    title: 'web-01',
    keywords: ['server', 'frontend'],
    fields: [
      { label: 'Image', value: 'ubuntu.qc2' },
      { label: 'IP address', value: '10.0.1.5' },
      { label: 'MAC address', value: '00:16:3E:0A:01:05' },
    ],
  };

  test('the title first, marked', () => {
    expect(matchItem(node, 'web')).toEqual({
      score: expect.any(Number),
      grade: expect.any(Number),
      ranges: [[0, 3]],
    });
  });

  test('its grade leaves out where the words are, but not how they match', () => {
    const near = matchItem({ title: 'Open draft' }, 'draft');
    const far = matchItem({ title: 'Show My Drafts' }, 'draft');
    const inside = matchItem({ title: 'Redrafts' }, 'draft');

    expect(near.score).toBeGreaterThan(far.score);
    expect(near.grade).toBe(far.grade);
    expect(far.grade).toBeGreaterThan(inside.grade);
  });

  test('then a field, saying which one and where', () => {
    expect(matchItem(node, '10.0.1')).toEqual({
      score: expect.any(Number),
      ranges: [],
      field: { label: 'IP address', value: '10.0.1.5', ranges: [[0, 6]] },
    });
    expect(matchItem(node, '0a:01').field).toEqual({
      label: 'MAC address',
      value: '00:16:3E:0A:01:05',
      ranges: [[9, 14]],
    });
    expect(matchItem(node, 'ubuntu').field.label).toBe('Image');
  });

  test('then keywords, without marking anything', () => {
    expect(matchItem(node, 'frontend')).toEqual({ score: 50, ranges: [] });
    expect(matchItem(node, 'database')).toBeNull();
  });

  test('a title match outranks a field match, which outranks letters', () => {
    const title = matchItem({ title: 'router-1', fields: [] }, 'router');
    const field = matchItem(
      { title: 'gw', fields: [{ label: 'Image', value: 'router.qc2' }] },
      'router',
    );
    const letters = matchItem({ title: 'r-o-u-t-e-r' }, 'router');

    expect(title.score).toBeGreaterThan(field.score);
    expect(field.score).toBeGreaterThan(letters.score);
    expect(letters.ranges).toHaveLength(6);
  });

  test('a field of a lower rank goes before a better match of a higher rank', () => {
    const fields = [
      { label: 'Note', value: 'plc', rank: 7 },
      { label: 'IP address', value: 'the plc net', rank: 1 },
      { label: 'Label', value: 'role=plc', rank: 5 },
    ];
    const match = matchItem({ title: 'x', fields }, 'plc');

    expect(match.field).toEqual({ ...fields[1], ranges: [[4, 7]] });

    // Without ranks, the best score wins, as before.
    const plain = fields.map(({ label, value }) => ({ label, value }));

    expect(matchItem({ title: 'x', fields: plain }, 'plc').field.label).toBe(
      'Note',
    );
  });

  test('fieldOrder orders field matches by rank, and nothing else', () => {
    const at = (rank, score = 700) => ({
      score,
      ranges: [],
      field: { label: 'f', value: 'v', rank, ranges: [] },
    });
    const title = { score: 1200, ranges: [] };

    expect(fieldOrder(at(1, 610), at(4, 900))).toBeLessThan(0);
    expect(fieldOrder(at(4), at(1))).toBeGreaterThan(0);
    expect(fieldOrder(at(2), at(2))).toBe(0);
    expect(fieldOrder(title, at(1))).toBe(0);
    expect(fieldOrder(at(1), { score: 550, ranges: [] })).toBe(0);
  });

  test('a field match stays below every title match, however many words', () => {
    const words = Array.from({ length: 20 }, (_, i) => `w${i}`).join(' ');
    const field = matchItem(
      { title: 'x', fields: [{ label: 'Note', value: words }] },
      words,
    );

    expect(field.score).toBeLessThan(1000);
  });
});

describe('matchName and matchItem without the name', () => {
  const node = {
    title: 'web-01',
    keywords: ['server', 'frontend'],
    fields: [{ label: 'Image', value: 'ubuntu.qc2' }],
  };

  test('matchName matches the title by words, then letter by letter, only', () => {
    expect(matchName(node, 'web')).toEqual(matchItem(node, 'web'));
    expect(matchName(node, 'wb1')).toEqual(
      matchItem({ title: 'web-01' }, 'wb1'),
    );
    expect(matchName(node, 'ubuntu')).toBeNull();
    expect(matchName(node, 'frontend')).toBeNull();
    expect(matchName(node, '')).toEqual({ score: 0, ranges: [] });
  });

  test('name: false skips the title, but not the fields and keywords', () => {
    const rest = (query) => matchItem(node, query, { name: false });

    // The title counts only with the keywords, at the bottom.
    expect(rest('web')).toEqual({ score: 50, ranges: [] });
    expect(rest('wb1')).toBeNull();
    expect(rest('ubuntu')).toEqual(matchItem(node, 'ubuntu'));
    expect(rest('frontend')).toEqual({ score: 50, ranges: [] });
    // The words can still spread over the title and the keywords.
    expect(rest('web server')).toEqual({ score: 50, ranges: [] });
  });

  test('the keywords of an item are read once, for every query', () => {
    let reads = 0;
    const keywords = new Proxy(['server', 'frontend'], {
      get(target, key, receiver) {
        reads += /^\d+$/.test(String(key)) ? 1 : 0;

        return Reflect.get(target, key, receiver);
      },
    });
    const item = { title: 'web-01', keywords };

    for (const query of ['f', 'fr', 'front', 'frontend', 'web server']) {
      expect(matchItem(item, query)).toEqual({ score: 50, ranges: [] });
    }
    expect(reads).toBe(keywords.length);
  });
});

// spread: the words of the last resort can be in the fields too (the
// drafts filter's name and user names).
describe('matchItem with spread', () => {
  const card = {
    title: 'Pump station',
    fields: [
      { label: 'Created by', value: 'alice' },
      { label: 'Changed by', value: 'bob' },
    ],
  };

  test('finds the words over the title and the fields, at the bottom', () => {
    expect(matchItem(card, 'pump alice')).toBeNull();
    expect(matchItem(card, 'pump alice', { spread: true })).toEqual({
      score: 50,
      ranges: [],
    });
    expect(matchItem(card, 'alice bob', { spread: true })).toEqual({
      score: 50,
      ranges: [],
    });
    expect(matchItem(card, 'pump zebra', { spread: true })).toBeNull();
  });

  test('ranks a match of the title or of one field as without it', () => {
    for (const query of ['pump', 'alice', 'pmpst', 'zebra']) {
      expect(matchItem(card, query, { spread: true })).toEqual(
        matchItem(card, query),
      );
    }
  });

  test('keeps the keywords of an item', () => {
    const node = { ...card, keywords: ['plant'] };

    expect(matchItem(node, 'plant bob', { spread: true })).toEqual({
      score: 50,
      ranges: [],
    });
    expect(matchItem(node, 'plant bob')).toBeNull();
  });
});

// Aliases are words that stand for the title: "export" for Download.
describe('aliases', () => {
  const download = { title: 'Download…', aliases: ['export'] };
  const gexf = {
    title: 'Download Gephi (GEXF)',
    aliases: ['export'],
    keywords: ['save', 'file', 'graph', 'analyze'],
  };
  const png = {
    title: 'Download PNG',
    aliases: ['export'],
    keywords: ['save', 'file', 'image', 'picture'],
  };

  test('a word that starts an alias matches as a word of the title, unmarked', () => {
    expect(matchItem(download, 'export')).toEqual({
      score: 1150,
      grade: 1200,
      ranges: [],
    });
    // From its start only, as a word is. A part of it elsewhere matches
    // as part of a keyword does, at the bottom.
    expect(matchItem(download, 'exp')).toEqual({
      score: 1150,
      grade: 1200,
      ranges: [],
    });
    expect(matchItem(download, 'port')).toEqual({ score: 50, ranges: [] });
    expect(matchItem(download, 'import')).toBeNull();
    expect(matchItem(download, 'EXPORT').grade).toBe(1200);
  });

  test('with a word of the title, that word is marked', () => {
    expect(matchItem(gexf, 'export gexf')).toEqual({
      score: 1334,
      grade: 1400,
      ranges: [[16, 20]],
    });
    // The plainer command matches only through its keywords, at the bottom.
    expect(
      matchItem(
        { ...download, keywords: ['save', 'file', 'gexf'] },
        'export gexf',
      ),
    ).toEqual({ score: 50, ranges: [] });
    expect(matchItem(png, 'export png').ranges).toEqual([[9, 12]]);
  });

  test('a true title match outranks an alias match', () => {
    const title = matchItem({ title: 'Export settings' }, 'export');
    const inside = matchItem({ title: 'Reexport' }, 'export');
    const alias = matchItem(download, 'export');

    expect(title.grade).toBeGreaterThan(alias.grade);
    expect(title.score).toBeGreaterThan(alias.score);
    // An alias is a word at a word start: above a match inside a word,
    // and above every field and letter match.
    expect(alias.grade).toBeGreaterThan(inside.grade);
    expect(alias.score).toBeGreaterThan(
      matchItem({ title: 'e-x-p-o-r-t' }, 'export').score,
    );
    // The title is tried first for each word.
    expect(
      matchItem({ title: 'Export', aliases: ['export'] }, 'export'),
    ).toEqual({ score: 1300, grade: 1300, ranges: [[0, 6]] });
  });

  test('as a last resort the words may be spread over title, aliases and keywords', () => {
    // A word of the title and a keyword.
    expect(matchItem(png, 'download image')).toEqual({ score: 50, ranges: [] });
    expect(matchItem(png, 'save png')).toEqual({ score: 50, ranges: [] });
    // An alias and a keyword.
    expect(matchItem(png, 'export image')).toEqual({ score: 50, ranges: [] });
    expect(matchItem(gexf, 'export image')).toBeNull();
    // Keywords alone, as before.
    expect(matchItem(png, 'picture')).toEqual({ score: 50, ranges: [] });
    // An item with neither aliases nor keywords gains nothing.
    expect(matchItem({ title: 'Download PNG' }, 'download image')).toBeNull();
  });

  test('items without aliases score as before', () => {
    expect(matchItem({ title: 'Auto layout' }, 'lay')).toEqual({
      score: 1195,
      grade: 1200,
      ranges: [[5, 8]],
    });
    expect(matchItem({ title: 'Layout', aliases: [] }, 'lay')).toEqual(
      matchItem({ title: 'Layout' }, 'lay'),
    );
    expect(
      matchItem(
        { title: 'Publish…', keywords: ['topology', 'config'] },
        'send',
      ),
    ).toBeNull();
    expect(
      matchItem(
        { title: 'Publish…', aliases: ['send'], keywords: ['topology'] },
        'send',
      ).score,
    ).toBe(1150);
  });
});

test('highlightParts splits text into marked and plain runs', () => {
  expect(
    highlightParts('Group selection', [
      [0, 2],
      [4, 5],
    ]),
  ).toEqual([
    { text: 'Gr', match: true },
    { text: 'ou', match: false },
    { text: 'p', match: true },
    { text: ' selection', match: false },
  ]);
  expect(highlightParts('Undo')).toEqual([{ text: 'Undo', match: false }]);
  expect(highlightParts('')).toEqual([]);
});

describe('ranges mark whole letters of the original text', () => {
  const marked = (title, query) =>
    highlightParts(title, matchItem({ title }, query).ranges)
      .filter((part) => part.match)
      .map((part) => part.text);

  test('a letter outside the Basic Multilingual Plane is marked whole', () => {
    expect(matchItem({ title: '😀 x a' }, '😀a').ranges).toEqual([
      [0, 2],
      [5, 6],
    ]);
    expect(marked('😀 x a', '😀a')).toEqual(['😀', 'a']);
  });

  test('a letter that is longer in lower case shifts no range after it', () => {
    expect(marked('İstanbul', 'bul')).toEqual(['bul']);
    expect(matchItem({ title: 'İx' }, 'x').ranges).toEqual([[1, 2]]);
    expect(marked('İx', 'İ')).toEqual(['İ']);
  });

  const letters = fc
    .array(fc.constantFrom('a', 'b', '😀', '😁', 'İ', ' ', '-', 'x'), {
      maxLength: 8,
    })
    .map((list) => list.join(''));
  const loneSurrogate =
    /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/;

  // Ranges sorted, apart, inside the text. Runs whole letters that join back.
  const wellFormed = (text, ranges) => {
    ranges.forEach(([start, end], i) => {
      expect(start).toBeGreaterThanOrEqual(i ? ranges[i - 1][1] + 1 : 0);
      expect(end).toBeGreaterThan(start);
      expect(end).toBeLessThanOrEqual(text.length);
    });
    const parts = highlightParts(text, ranges);

    expect(parts.map((part) => part.text).join('')).toBe(text);
    for (const part of parts) {
      expect(part.text).not.toBe('');
      expect(part.text).not.toMatch(loneSurrogate);
    }
  };

  test('in a title, by words or by letters', () => {
    fc.assert(
      fc.property(letters, letters, (title, query) => {
        const found = matchItem({ title }, query);

        if (found) {
          wellFormed(title, found.ranges);
        }
        const name = matchName({ title }, query);

        if (name) {
          wellFormed(title, name.ranges);
        }
      }),
      { numRuns: 500 },
    );
  });

  test('in a field', () => {
    fc.assert(
      fc.property(letters, letters, (value, query) => {
        const found = matchItem(
          { title: '', fields: [{ label: 'Field', value }] },
          query,
        );

        if (found?.field) {
          wellFormed(value, found.field.ranges);
        }
      }),
      { numRuns: 300 },
    );
  });
});
