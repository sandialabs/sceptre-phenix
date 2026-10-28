// filepathMatch against Go's path/filepath.Match, which the server's RBAC uses.
import fc from 'fast-check';
import { expect, test } from 'vitest';

import { filepathMatch } from '@/utils/filepathMatch.js';

// pattern, name, and what Go 1.27's filepath.Match answers on Linux
test.each([
  ['.', './', false],
  ['*', 'a/', false],
  ['a/*', 'a/', true],
  ['*', '.', true],
  ['*', '..', true],
  ['*/*', './..', true],
  ['a/*/b', 'a//b', true],
  ['?', '/', false],
  ['a?c', 'abc', true],
  ['a[bc]d', 'acd', true],
  ['a[^bc]d', 'aed', true],
  ['a[^bc]d', 'abd', false],
  ['a[^b]c', 'a/c', true],
  ['[a-c]x', 'bx', true],
  ['[a-c]x', 'dx', false],
  ['\\*', '*', true],
  ['\\*', 'a', false],
  ['a\\[b', 'a[b', true],
  ['{a,b}', 'a', false],
  ['{a,b}', '{a,b}', true],
  ['**', 'a/b', false],
  ['a[', 'a', false],
  ['[a-', 'a', false],
  ['a\\', 'a', false],
  ['[]a]', ']', false],
  ['*x', 'abx', true],
  ['*x', 'a/x', false],
  ['\u00e9?', '\u00e9\u00e9', true],
])('%j matches %j: %s', (pattern, name, want) => {
  expect(filepathMatch(pattern, name)).toBe(want);
});

// Go's rule for literals, * and ?: neither wildcard matches a /
const model = (pattern, name) =>
  new RegExp(
    `^${[...pattern]
      .map((c) =>
        c === '*' ? '[^/]*' : c === '?' ? '[^/]' : c.replace(/[.]/, '\\.'),
      )
      .join('')}$`,
  ).test(name);

const text = (chars, maxLength) =>
  fc.string({ unit: fc.constantFrom(...chars), maxLength });

test('a star or a question mark never matches a slash', () => {
  fc.assert(
    fc.property(text('ab./*?', 6), text('ab./', 6), (pattern, name) => {
      expect(filepathMatch(pattern, name)).toBe(model(pattern, name));
    }),
    {
      examples: [
        ['.', './'],
        ['*', 'a/'],
        ['a/*', 'a/'],
        ['*', '..'],
      ],
    },
  );
});

test('an escaped character matches only itself', () => {
  fc.assert(
    fc.property(
      fc.string({ minLength: 1, maxLength: 1 }),
      text('ab*?', 2),
      (c, s) => {
        expect(filepathMatch(`\\${c}`, c + s)).toBe(s === '');
      },
    ),
  );
});
