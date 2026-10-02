import { describe, expect, test } from 'vitest';

import { highlightParts, matchItem } from '@/builder/fuzzy.js';

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
    // From its start only, as a word is; a part of it elsewhere matches
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
      matchItem({ title: 'Share…', keywords: ['access', 'people'] }, 'send'),
    ).toBeNull();
    expect(
      matchItem(
        { title: 'Share…', aliases: ['send'], keywords: ['access'] },
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
