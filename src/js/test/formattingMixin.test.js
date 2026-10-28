import fc from 'fast-check';
import { expect, it } from 'vitest';

import { formattingMixin } from '@/utils/formattingMixin.js';

const { formatLowercase, formatUptime, getUniqueItems } =
  formattingMixin.methods;

it('lowercases text and shows nothing for a missing value', () => {
  expect(formatLowercase('Enterprise')).toBe('enterprise');
  expect(formatLowercase(null)).toBe('');
  expect(formatLowercase(undefined)).toBe('');
});

it.each([
  [3723, '01:02:03'],
  [86400 + 3723, '1 day, 01:02:03'],
  [2 * 86400, '2 days, 00:00:00'],
])('shows an uptime of %s seconds as %s', (seconds, text) => {
  expect(formatUptime(seconds)).toBe(text);
});

it('keeps each item once, sorted, without short ones but "dnb"', () => {
  expect(
    getUniqueItems(['Unknown', 'Logs', 'abc', 'dnb', '', 'Logs', 'ELF']),
  ).toEqual(['Logs', 'Unknown', 'dnb']);

  const item = fc.oneof(
    fc.constantFrom('dnb', 'DNB', 'dn', 'Logs', 'logs', 'abc', ''),
    fc.string({ unit: 'binary', maxLength: 6 }),
  );
  const kept = (s) => s.length >= 4 || s === 'dnb';
  fc.assert(
    fc.property(fc.array(item), (items) => {
      const unique = getUniqueItems(items);
      // strictly ascending: sorted, and no item twice
      unique.slice(1).forEach((s, i) => expect(unique[i] < s).toBe(true));
      expect(new Set(unique)).toEqual(new Set(items.filter(kept)));
    }),
  );
});
