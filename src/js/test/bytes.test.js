// Sizes for people: byte counts, and the MiB minimega gives memory in.
import fc from 'fast-check';
import { expect, it } from 'vitest';

import { bytesText, mebibytesText } from '@/utils/bytes.js';
import { formattingMixin } from '@/utils/formattingMixin.js';

it.each([
  [512, '512 B'],
  [1536, '1.5 KiB'],
  [3 * 1024 ** 3, '3.0 GiB'],
])('writes %d bytes as %s', (bytes, want) => {
  expect(bytesText(bytes)).toBe(want);
});

it('writes any byte count in the largest unit it fills, to a tenth', () => {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  // counts near each unit's start, where the unit changes
  const near = fc
    .tuple(fc.integer({ min: 1, max: 4 }), fc.integer({ min: -9, max: 9 }))
    .map(([unit, off]) => 1024 ** unit + off);
  fc.assert(
    fc.property(fc.oneof(fc.maxSafeNat(), near), (bytes) => {
      const [value, unitName] = bytesText(bytes).split(' ');
      const unit = units.indexOf(unitName);
      expect(unit).toBeGreaterThanOrEqual(0);
      // shown as at least 1 of its unit, and below 1024 unless the largest
      if (unit > 0) expect(Number(value)).toBeGreaterThanOrEqual(1);
      if (unit < units.length - 1) expect(Number(value)).toBeLessThan(1024);
      expect(
        Math.abs(Number(value) * 1024 ** unit - bytes),
      ).toBeLessThanOrEqual(0.05 * 1024 ** unit);
    }),
  );
});

it('writes 1 MiB less a byte as 1.0 MiB', () => {
  expect(bytesText(1024 ** 2 - 1)).toBe('1.0 MiB');
});

it.each([
  [512, '512.0 MiB'],
  [1536, '1.5 GiB'],
  [2048, '2.0 GiB'],
  [3 * 1024 ** 2 + 512 * 1024, '3.5 TiB'],
])('writes %d MiB of memory as %s', (mebibytes, want) => {
  expect(mebibytesText(mebibytes)).toBe(want);
  expect(formattingMixin.methods.formatRAM(mebibytes)).toBe(want);
});

it('writes memory to a tenth of its unit, not rounded to a whole one', () => {
  fc.assert(
    fc.property(fc.integer({ min: 1, max: 1024 ** 3 }), (mebibytes) => {
      const [value, unitName] = mebibytesText(mebibytes).split(' ');
      const unit = ['MiB', 'GiB', 'TiB'].indexOf(unitName);
      expect(unit).toBeGreaterThanOrEqual(0);
      expect(
        Math.abs(Number(value) * 1024 ** unit - mebibytes),
      ).toBeLessThanOrEqual(0.05 * 1024 ** unit);
    }),
  );
});
