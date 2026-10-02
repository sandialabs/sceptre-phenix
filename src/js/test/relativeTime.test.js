import { expect, test } from 'vitest';
import { relativeTime } from '@/utils/relativeTime.js';

const now = Date.parse('2026-09-25T12:00:00Z');

test.each([
  ['2026-09-25T11:59:30Z', 'just now'],
  ['2026-09-25T11:49:00Z', '11 minutes ago'],
  ['2026-09-25T11:00:00Z', '1 hour ago'],
  ['2026-09-25T00:00:00Z', '12 hours ago'],
  ['2026-09-23T10:00:00Z', '2 days ago'],
  ['2026-09-11T12:00:00Z', '2 weeks ago'],
  ['2026-06-25T12:00:00Z', '3 months ago'],
  ['2024-09-25T12:00:00Z', '2 years ago'],
  // an offset timestamp is compared in UTC: 02:00-07:00 is 09:00Z
  ['2026-09-25T02:00:00-07:00', '3 hours ago'],
])('%s is %s', (time, want) => {
  expect(relativeTime(time, now)).toBe(want);
});

test('a time in the future reads as such', () => {
  expect(relativeTime('2026-09-25T14:00:00Z', now)).toBe('in 2 hours');
});

test('an unparsable time gives nothing', () => {
  expect(relativeTime('not a time', now)).toBe('');
});
