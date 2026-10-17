import fc from 'fast-check';
import { expect, test } from 'vitest';

import { relativeTime } from '@/utils/relativeTime.js';
import { isoTimeArb } from './helpers/arbitraries.js';

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

test('a half second rounds the same way ahead and behind', () => {
  expect(relativeTime(now - 59_500, now)).toBe('1 minute ago');
  expect(relativeTime(now + 59_500, now)).toBe('in 1 minute');
});

test('an unparsable time gives nothing', () => {
  expect(relativeTime('not a time', now)).toBe('');
});

// counts past 999 are digit-grouped, as in "in 7,978 years"
const reading =
  /^(?:just now|(\d{1,3}(?:,\d{3})*) (minute|hour|day|week|month|year)s? ago|in (\d{1,3}(?:,\d{3})*) (minute|hour|day|week|month|year)s?)$/;
const UNIT_ORDER = ['minute', 'hour', 'day', 'week', 'month', 'year'];
// how long ago a reading says, comparable as [unit rank, count]
const rank = (text) => {
  const [, count, unit] = text.match(reading);
  return unit
    ? [UNIT_ORDER.indexOf(unit), Number(count.replaceAll(',', ''))]
    : [-1, 0];
};
const DAY = 24 * 3600;
// spans in seconds, often a second either side of a unit boundary
const span = fc.oneof(
  fc.nat(5 * 365 * DAY),
  fc
    .tuple(
      fc.constantFrom(60, 3600, DAY, 7 * DAY, 30 * DAY, 365 * DAY),
      fc.integer({ min: 1, max: 3 }),
      fc.integer({ min: -1, max: 1 }),
    )
    .map(([unit, n, off]) => unit * n + off),
);

test('any text gives nothing or a reading', () => {
  fc.assert(
    fc.property(
      fc.oneof(
        fc.string(),
        isoTimeArb.map((t) => t.time),
      ),
      (time) => {
        const text = relativeTime(time, now);
        expect(text === '' || reading.test(text)).toBe(true);
      },
    ),
  );
});

test('a time reads the same in any UTC offset', () => {
  fc.assert(
    fc.property(isoTimeArb, span, fc.boolean(), ({ time, ms }, s, ahead) => {
      const at = ms + (ahead ? s : -s) * 1000;
      expect(relativeTime(time, at)).toBe(
        relativeTime(new Date(ms).toISOString(), at),
      );
    }),
  );
});

test('an older time never reads as more recent', () => {
  const age = fc.tuple(span, fc.nat(999)).map(([s, ms]) => s * 1000 + ms);
  fc.assert(
    fc.property(age, age, (a, b) => {
      const [younger, older] = a <= b ? [a, b] : [b, a];
      const [ry, cy] = rank(relativeTime(now - younger, now));
      const [ro, co] = rank(relativeTime(now - older, now));
      expect(ro > ry || (ro === ry && co >= cy)).toBe(true);
    }),
  );
});

test('a time as far ahead reads as the same span in the future', () => {
  fc.assert(
    fc.property(span, fc.nat(999), (s, ms) => {
      const past = relativeTime(now - s * 1000 - ms, now);
      const future = relativeTime(now + s * 1000 + ms, now);
      expect(future).toBe(
        past === 'just now' ? past : `in ${past.replace(/ ago$/, '')}`,
      );
    }),
  );
});

test('a count past 999 is digit-grouped', () => {
  expect(relativeTime('9999-09-25T12:00:00Z', now)).toBe('in 7,978 years');
});
