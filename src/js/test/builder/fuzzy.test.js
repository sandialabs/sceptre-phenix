import { describe, expect, test } from 'vitest';

import { highlightParts, matchItem, matchText } from '@/builder/fuzzy.js';

describe('matchText', () => {
  test('an empty query matches everything, with nothing marked', () => {
    expect(matchText('Undo', '')).toEqual({ score: 0, ranges: [] });
    expect(matchText('Undo', '   ')).toEqual({ score: 0, ranges: [] });
  });

  test('every word must appear, ignoring case', () => {
    expect(matchText('Add device', 'add dev')).toEqual({
      score: expect.any(Number),
      ranges: [
        [0, 3],
        [4, 7],
      ],
    });
    expect(matchText('Add device', 'ADD')?.ranges).toEqual([[0, 3]]);
    expect(matchText('Add switch', 'add dev')).toBeNull();
  });

  test('the start of the text ranks first, then a word start, then anywhere', () => {
    const start = matchText('Layout', 'lay').score;
    const word = matchText('Auto layout', 'lay').score;
    const inside = matchText('Display', 'lay').score;

    expect(start).toBeGreaterThan(word);
    expect(word).toBeGreaterThan(inside);
  });

  test('a word start is marked rather than an earlier match inside a word', () => {
    expect(matchText('Ungroup group', 'group').ranges).toEqual([[8, 13]]);
  });

  test('otherwise the letters in order, closest together first', () => {
    const group = matchText('Group selection', 'grp');
    const ungroup = matchText('Ungroup', 'grp');

    expect(group.ranges).toEqual([
      [0, 2],
      [4, 5],
    ]);
    expect(group.score).toBeGreaterThan(ungroup.score);
    // Any word match outranks any letter match.
    expect(matchText('Display', 'lay').score).toBeGreaterThan(group.score);
    expect(matchText('Group selection', 'grp', { letters: false })).toBeNull();
    expect(matchText('Undo', 'zq')).toBeNull();
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
      ranges: [[0, 3]],
    });
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
