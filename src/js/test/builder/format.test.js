import { describe, expect, test } from 'vitest';

import { formatTimestamp } from '@/builder/format.js';
import { rowTarget } from '@/builder/roving.js';

describe('formatTimestamp', () => {
  test('formats a server timestamp in the reader locale, without the raw ISO text', () => {
    const value = '2026-09-24T05:50:22.279213Z';
    const text = formatTimestamp(value);

    expect(text).toBe(
      new Intl.DateTimeFormat(undefined, {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(new Date(value)),
    );
    expect(text).not.toContain('T05:50');
    expect(text).toMatch(/2026/);
  });

  test('includes the seconds when asked, so close snapshots differ', () => {
    const first = formatTimestamp('2026-09-24T05:50:22Z', { seconds: true });
    const second = formatTimestamp('2026-09-24T05:50:41Z', { seconds: true });

    expect(first).toBe(
      new Intl.DateTimeFormat(undefined, {
        dateStyle: 'medium',
        timeStyle: 'medium',
      }).format(new Date('2026-09-24T05:50:22Z')),
    );
    expect(first).not.toBe(second);
    expect(formatTimestamp('2026-09-24T05:50:22Z')).toBe(
      formatTimestamp('2026-09-24T05:50:41Z'),
    );
  });

  test('gives an empty string for missing or unparsable values', () => {
    expect(formatTimestamp()).toBe('');
    expect(formatTimestamp('')).toBe('');
    expect(formatTimestamp('not a date')).toBe('');
    expect(formatTimestamp('not a date', { seconds: true })).toBe('');
  });
});

describe('rowTarget', () => {
  test('Left and Right move one item and wrap at the ends', () => {
    expect(rowTarget('ArrowRight', 0, 3)).toBe(1);
    expect(rowTarget('ArrowRight', 2, 3)).toBe(0);
    expect(rowTarget('ArrowLeft', 1, 3)).toBe(0);
    expect(rowTarget('ArrowLeft', 0, 3)).toBe(2);
  });

  test('Home and End jump to the first and last item', () => {
    expect(rowTarget('Home', 2, 3)).toBe(0);
    expect(rowTarget('End', 0, 3)).toBe(2);
  });

  test('other keys, an empty row and an unknown position do not move', () => {
    expect(rowTarget('ArrowDown', 0, 3)).toBeUndefined();
    expect(rowTarget('Tab', 0, 3)).toBeUndefined();
    expect(rowTarget('End', 0, 0)).toBeUndefined();
    expect(rowTarget('Home', -1, 3)).toBeUndefined();
  });
});
