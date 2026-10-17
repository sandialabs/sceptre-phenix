import fc from 'fast-check';
import { expect, it } from 'vitest';

import { formattingMixin } from '@/utils/formattingMixin.js';

const { formatLowercase, getBaseName, getUniqueItems } =
  formattingMixin.methods;

const noSlash = fc.string({ unit: 'binary' }).filter((s) => !s.includes('/'));

it('lowercases text and shows nothing for a missing value', () => {
  expect(formatLowercase('Enterprise')).toBe('enterprise');
  expect(formatLowercase(null)).toBe('');
  expect(formatLowercase(undefined)).toBe('');
});

it('names a disk image by its file name', () => {
  expect(getBaseName('/phenix/images/win10.qc2')).toBe('win10.qc2');
  // any folders, absolute or relative, even empty ones
  fc.assert(
    fc.property(fc.array(noSlash), noSlash, (folders, name) => {
      expect(getBaseName([...folders, name].join('/'))).toBe(name);
    }),
  );
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
