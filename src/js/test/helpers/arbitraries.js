// fast-check arbitraries for values several test files generate: disk lists,
// path segments and ISO 8601 timestamps.
import fc from 'fast-check';

import { disk } from './fixtures.js';

// a file or folder name, now and then with a character minimega refuses
export const pathSegment = fc
  .oneof(
    { weight: 4, arbitrary: fc.stringMatching(/^[a-z0-9][a-z0-9_.-]{0,7}$/) },
    { weight: 1, arbitrary: fc.string({ minLength: 1, maxLength: 6 }) },
  )
  .filter((name) => !name.includes('/') && name !== '.' && name !== '..');

// A disk list as the disks API gives it, every full path distinct: some
// inside filesDir, some outside it, each backed by others or by an image the
// list does not hold. Backing images are picked by index, not .chain, so
// counterexamples shrink.
export const diskListArb = (filesDir = '/phenix/images') =>
  fc
    .tuple(
      fc.uniqueArray(
        fc
          .record({
            inside: fc.boolean(),
            within: fc.array(pathSegment, { minLength: 1, maxLength: 3 }),
            kind: fc.constantFrom('VM', 'ISO'),
          })
          .map(({ inside, within, kind }) => ({
            fullPath: `${inside ? filesDir : '/data'}/${within.join('/')}`,
            relativePath: inside ? within.join('/') : '',
            kind,
          })),
        { selector: (entry) => entry.fullPath, maxLength: 8 },
      ),
      fc.array(fc.array(fc.nat(), { maxLength: 8 }), { maxLength: 8 }),
    )
    .map(([entries, picks]) => {
      const paths = [...entries.map((e) => e.fullPath), `${filesDir}/gone.qc2`];
      return entries.map(({ fullPath, relativePath, kind }, i) => {
        const others = paths.filter((path) => path !== fullPath);
        const backingImages = [
          ...new Set((picks[i] ?? []).map((n) => n % others.length)),
        ]
          .toSorted((a, b) => a - b)
          .map((n) => others[n]);
        return disk(fullPath, relativePath, { kind, backingImages });
      });
    });

// A moment as an ISO 8601 string in any UTC offset, with its epoch ms.
export const isoTimeArb = fc
  .record({
    ms: fc.integer({ min: Date.UTC(2000, 0, 1), max: Date.UTC(2100, 0, 1) }),
    offset: fc.integer({ min: -48, max: 56 }).map((q) => q * 15),
  })
  .map(({ ms, offset }) => {
    const wall = new Date(ms + offset * 60_000).toISOString().slice(0, -1);
    const abs = Math.abs(offset);
    const zone = `${offset < 0 ? '-' : '+'}${String(Math.floor(abs / 60)).padStart(2, '0')}:${String(abs % 60).padStart(2, '0')}`;
    return { time: wall + zone, ms };
  });
