import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import { validateDocument } from '@/builder/validate.js';
import bundle from '@/builder/schema/builder-v1.schema.json';

import { sampleDocument } from './fixtures.js';

// The white space a name must not hold (TestNameSpaceCorpus in
// types/builder holds Go to the same list).
const corpus = JSON.parse(
  readFileSync(
    new URL(
      '../../../go/types/builder/testdata/name-spaces.json',
      import.meta.url,
    ),
  ),
);

const codePoint = (text) => Number.parseInt(text.slice(2), 16);
const listed = new Set(corpus.codePoints.map(codePoint));

// The code points the test checks: those of the list, U+0085, and a few
// others that are not white space.
const checked = [...listed, 0x85, 0x41, 0x200b, 0x180e, 0xfffe];

const label = (code) => `U+${code.toString(16).toUpperCase().padStart(4, '0')}`;

describe('white space in a name', () => {
  test('the schema pattern refuses a name with each listed character', () => {
    const pattern = new RegExp(
      bundle.$defs.network.properties.name.pattern,
      'u',
    );

    for (const code of checked) {
      const name = `a${String.fromCodePoint(code)}b`;

      expect([label(code), pattern.test(name)]).toEqual([
        label(code),
        !listed.has(code),
      ]);
    }
  });

  test('validation refuses a network name with each listed character', () => {
    for (const code of checked) {
      const { doc } = sampleDocument();
      const name = `a${String.fromCodePoint(code)}b`;

      doc.networks[0] = { ...doc.networks[0], name };

      const refused = validateDocument(doc).some(
        (issue) => issue.code === 'network.name.whitespace',
      );

      expect([label(code), refused]).toEqual([label(code), listed.has(code)]);
    }
  });
});
