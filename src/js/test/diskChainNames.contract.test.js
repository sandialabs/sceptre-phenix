// Which code points the disk pickers refuse in an image path, checked against
// every code point in the cases shared with the server's rule.
import { readFileSync } from 'node:fs';

import { describe, expect, it, vi } from 'vitest';

// the real store makes a browser router, and this project has no window
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));

import { minimegaSafe } from '@/utils/diskChain.js';

describe('minimegaSafe', () => {
  // The cases the server's rule is tested against too (see names_test.go).
  const cases = JSON.parse(
    readFileSync(
      new URL(
        '../../go/api/disk/testdata/minimega_names.json',
        import.meta.url,
      ),
      'utf8',
    ),
  );
  const codePoint = (name) => parseInt(name.replace(/^U\+/, ''), 16);
  const refusedCodes = new Set(
    cases.refused.flatMap(({ from, to = from }) => {
      const first = codePoint(from);
      return Array.from(
        { length: codePoint(to) - first + 1 },
        (_, i) => first + i,
      );
    }),
  );

  it('refuses the code points the shared cases refuse, and only those', () => {
    expect(refusedCodes).toContain('#'.codePointAt(0));
    const wrong = [];
    for (let code = 0; code <= 0x10ffff && wrong.length < 20; code++) {
      const path = `/phenix/images/a${String.fromCodePoint(code)}b.qc2`;
      if (minimegaSafe(path) === refusedCodes.has(code)) {
        wrong.push(`U+${code.toString(16).toUpperCase().padStart(4, '0')}`);
      }
    }
    expect(wrong).toEqual([]);
  });
});
