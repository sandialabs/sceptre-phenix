import { readFileSync } from 'node:fs';

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import {
  foldKey,
  goEqualFold,
  goToLower,
  goTrimSpace,
  trimASCIISpace,
} from '@/builder/goCase.js';
import table from '@/builder/schema/go-case.json';
import { trimSpace } from '@/builder/text.js';

// Texts and what Go made of them (TestFoldCorpus in types/builder).
const corpus = JSON.parse(
  readFileSync(
    new URL(
      '../../../go/types/builder/testdata/fold-corpus.json',
      import.meta.url,
    ),
  ),
);

describe('the case mapping shared with the server', () => {
  test('is of the Unicode version of the corpus', () => {
    expect(table.unicode).toBe(corpus.unicode);
  });

  test.each(corpus.fold.map((entry) => [JSON.stringify(entry.in), entry]))(
    'goToLower and foldKey of %s',
    (_, entry) => {
      expect(goToLower(entry.in)).toBe(entry.lower);
      expect(foldKey(entry.in)).toBe(entry.key);
    },
  );

  test.each(
    corpus.equalFold.map((entry) => [
      `${JSON.stringify(entry.a)}, ${JSON.stringify(entry.b)}`,
      entry,
    ]),
  )('goEqualFold(%s)', (_, entry) => {
    expect(goEqualFold(entry.a, entry.b)).toBe(entry.equal);
    expect(goEqualFold(entry.b, entry.a)).toBe(entry.equal);
  });

  test('maps a lone surrogate as Go decodes it from JSON, to U+FFFD', () => {
    expect(goToLower('A\ud800')).toBe('a�');
    expect(goEqualFold('\udc00', '�')).toBe(true);
    expect(foldKey(' \ud800X ')).toBe('�x');
  });

  test('trims what strings.TrimSpace trims, and no more', () => {
    expect(goTrimSpace('\u0085　eth0 \t')).toBe('eth0');
    expect(goTrimSpace('﻿eth0​')).toBe('﻿eth0​');
    expect(foldKey(null)).toBe('');
  });

  test('trims as text.js trimSpace does, and trimASCIISpace only ASCII', () => {
    const unit = fc.constantFrom(
      ...' \t\n\v\f\r\u0085\u00a0\u2003\u3000\ufeff\u200ba\ud800',
    );

    fc.assert(
      fc.property(fc.string({ unit }), (text) => {
        expect(goTrimSpace(text)).toBe(trimSpace(text));
        expect(trimASCIISpace(text)).toBe(
          text.replace(/^[\t\n\v\f\r ]+|[\t\n\v\f\r ]+$/g, ''),
        );
      }),
      { numRuns: 300 },
    );
  });

  // A regular expression that trims both ends takes time that grows with
  // the square of a run of white space inside the text.
  test('trims a text with a long run of white space inside in one pass', () => {
    const text = `a${' '.repeat(300000)}b`;

    expect(goTrimSpace(` ${text}\u3000`)).toBe(text);
    expect(trimASCIISpace(`\v${text} `)).toBe(text);
  });

  test('foldKey is the same for texts that goEqualFold takes as one, in ASCII', () => {
    fc.assert(
      fc.property(fc.string({ unit: 'binary-ascii' }), (text) => {
        expect(goEqualFold(text, text.toUpperCase())).toBe(true);
        expect(foldKey(text.toUpperCase())).toBe(foldKey(text));
      }),
      { numRuns: 200 },
    );
  });

  test('goToLower keeps the length of a text in characters', () => {
    fc.assert(
      fc.property(fc.string({ unit: 'grapheme' }), (text) => {
        expect([...goToLower(text)]).toHaveLength([...text].length);
      }),
      { numRuns: 200 },
    );
  });
});
