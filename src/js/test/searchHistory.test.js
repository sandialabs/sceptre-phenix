// The experiment pages' search history.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { addSearch } from '@/utils/searchHistory.js';

const kept = (search) => search.trim().length >= 4 || search.trim() === 'dnb';

describe('addSearch', () => {
  it.each([
    [
      'adds a search, trimmed, as the newest',
      ['vm-1'],
      ' vm-2 ',
      ['vm-1', 'vm-2'],
    ],
    [
      'moves a repeated search to the newest',
      ['vm-1', 'vm-2'],
      'vm-1',
      ['vm-2', 'vm-1'],
    ],
    ['keeps "dnb", the search for VMs set not to boot', [], 'dnb', ['dnb']],
    ['leaves out a short search', ['vm-1'], ' ab ', ['vm-1']],
    ['leaves out an empty search', ['vm-1'], '', ['vm-1']],
    ['drops the oldest once full', ['zzzz', 'vm-1'], 'aaaa', ['vm-1', 'aaaa']],
  ])('%s', (_, history, search, want) => {
    expect(addSearch(history, search, 2)).toEqual(want);
  });

  it('holds each kept search once, newest last, up to its limit', () => {
    const search = fc.oneof(
      fc.constantFrom('', 'ab', 'dnb', ' dnb '),
      fc.string({ maxLength: 6 }),
    );
    fc.assert(
      fc.property(
        fc.array(search, { maxLength: 30 }),
        fc.integer({ min: 1, max: 12 }),
        (searches, limit) => {
          let history = [];
          for (const s of searches) {
            const before = history;
            history = addSearch(history, s, limit);
            expect(new Set(history).size).toBe(history.length);
            expect(history.length).toBeLessThanOrEqual(limit);
            if (!kept(s)) {
              expect(history).toBe(before);
              continue;
            }
            // nothing is lost but the oldest, and only to make room
            expect(history.at(-1)).toBe(s.trim());
            const others = before.filter((h) => h !== s.trim());
            expect(history.slice(0, -1)).toEqual(
              others.slice(Math.max(0, others.length - (limit - 1))),
            );
          }
        },
      ),
    );
  });
});
