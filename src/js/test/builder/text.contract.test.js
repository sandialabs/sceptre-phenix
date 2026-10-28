// Rules of the text helpers (text.js) that hold for any text: Go's white
// space, UTF-8 lengths and capitals.

import { Buffer } from 'node:buffer';

import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import { capitalize, isBlank, trimSpace, utf8Length } from '@/builder/text.js';

// Any text, and text mostly of the white space Go and JavaScript disagree on.
const text = fc.oneof(
  fc.string({ unit: 'binary' }),
  fc.string({
    unit: fc.constantFrom(
      ' ',
      '\t',
      '\u0085',
      '\ufeff',
      '\u3000',
      'a',
      'A',
      'ß',
      'é',
      '😀',
    ),
  }),
);

describe('text helpers, for any text', () => {
  test('text is blank exactly when trimming leaves nothing', () => {
    fc.assert(
      fc.property(text, (value) => {
        expect(isBlank(value)).toBe(trimSpace(value) === '');
      }),
      { numRuns: 300 },
    );
  });

  test('trimming agrees with String.prototype.trim but for U+0085 and U+FEFF', () => {
    fc.assert(
      fc.property(text, (value) => {
        fc.pre(!/[\u0085\ufeff]/.test(value));
        expect(trimSpace(value)).toBe(value.trim());
      }),
      { numRuns: 300 },
    );
    expect(trimSpace('\u0085x\ufeff')).toBe('x\ufeff');
  });

  test('trimming twice changes nothing', () => {
    fc.assert(
      fc.property(text, (value) => {
        const once = trimSpace(value);

        expect(trimSpace(once)).toBe(once);
      }),
      { numRuns: 300 },
    );
  });

  test('the UTF-8 length is the byte length of the encoded text', () => {
    fc.assert(
      fc.property(text, (value) => {
        expect(utf8Length(value)).toBe(Buffer.byteLength(value));
      }),
      { numRuns: 300 },
    );
  });

  test('capitalizing twice changes nothing', () => {
    fc.assert(
      fc.property(text, (value) => {
        const once = capitalize(value);

        expect(capitalize(once)).toBe(once);
      }),
      { numRuns: 300 },
    );
  });
});
