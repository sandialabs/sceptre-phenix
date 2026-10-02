import { expect, it } from 'vitest';

import { formattingMixin } from '@/utils/formattingMixin.js';

const { formatLowercase, getBaseName, getUniqueItems } =
  formattingMixin.methods;

it('lowercases text and shows nothing for a missing value', () => {
  expect(formatLowercase('Enterprise')).toBe('enterprise');
  expect(formatLowercase(null)).toBe('');
  expect(formatLowercase(undefined)).toBe('');
});

it('names a disk image by its file name', () => {
  expect(getBaseName('/phenix/images/win10.qc2')).toBe('win10.qc2');
  expect(getBaseName('win10.qc2')).toBe('win10.qc2');
});

it('keeps each item once, sorted, without short ones but "dnb"', () => {
  expect(
    getUniqueItems(['Unknown', 'Logs', 'abc', 'dnb', '', 'Logs', 'ELF']),
  ).toEqual(['Logs', 'Unknown', 'dnb']);
});
