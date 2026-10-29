// Finding a SCORCH run's artifact archives among an experiment's files: only
// that run's, newest first, each with the time the run started.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { rfc3339Ms } from '@/utils/relativeTime.js';
import { runArchives } from '@/utils/scorchArtifacts.js';
import { isoTimeArb } from './helpers/arbitraries.js';

// a file as the experiment files listing gives it
const file = (path, fields = {}) => ({
  name: path.split('/').at(-1),
  path,
  date: '2026-01-01T00:00:00Z',
  size: 1,
  categories: [],
  plainText: false,
  isDir: false,
  ...fields,
});

// an ISO 8601 time as the server stamps an archive's name with it,
// 2006-01-02T15-04-05Z0700
const stamp = (time) => {
  const wall = time
    .slice(0, 19)
    .replace(/T(\d{2}):(\d{2}):(\d{2})/, 'T$1-$2-$3');
  const zone = time.slice(-6);
  return wall + (zone === '+00:00' ? 'Z' : zone.replace(':', ''));
};

const archive = (run, time) => file(`scorch-run-${run}_${stamp(time)}.tgz`);

describe('runArchives', () => {
  it("lists the run's archives newest first, each with its start time", () => {
    const files = [
      file('scorch-run-1_2026-10-28T09-00-00Z.tgz'),
      file('scorch-run-1_2026-10-29T14-03-22Z.tgz', { size: 2048 }),
      file('scorch-run-1_2026-10-29T16-00-00+0200.tgz'),
      // another run's, a run directory's file, and files of other names
      file('scorch-run-10_2026-10-30T00-00-00Z.tgz'),
      file('scorch-run-0_2026-10-30T00-00-00Z.tgz'),
      file('scorch/run-1/scorch-run-1_2026-10-30T00-00-00Z.tgz'),
      file('scorch-run-1_2026-10-30T00-00-00Z.tgz.partial'),
      file('scorch-run-1.tgz'),
    ];

    expect(
      runArchives(files, 1).map(({ path, started, size }) => [
        path,
        started,
        size,
      ]),
    ).toEqual([
      ['scorch-run-1_2026-10-29T14-03-22Z.tgz', '2026-10-29T14:03:22Z', 2048],
      [
        'scorch-run-1_2026-10-29T16-00-00+0200.tgz',
        '2026-10-29T16:00:00+02:00',
        1,
      ],
      ['scorch-run-1_2026-10-28T09-00-00Z.tgz', '2026-10-28T09:00:00Z', 1],
    ]);
  });

  it("falls back to the file's date when its name has no start time", () => {
    const odd = file('scorch-run-2_yesterday.tgz', {
      date: '2026-10-29T10:00:00Z',
    });
    expect(runArchives([odd], 2).map((a) => a.started)).toEqual([
      '2026-10-29T10:00:00Z',
    ]);
  });

  it('lists nothing before the files are listed', () => {
    expect(runArchives(null, 0)).toEqual([]);
  });

  it("never lists another run's archive, as run 1's for run 10's", () => {
    const runs = fc.uniqueArray(fc.nat({ max: 120 }), {
      minLength: 1,
      maxLength: 6,
    });
    const made = runs.chain((ids) =>
      fc.tuple(
        fc.constant(ids),
        fc.array(
          fc.record({
            run: fc.constantFrom(...ids),
            time: isoTimeArb,
          }),
          { maxLength: 12 },
        ),
      ),
    );

    fc.assert(
      fc.property(made, fc.nat({ max: 120 }), ([ids, archives], asked) => {
        const files = archives.map(({ run, time }) => archive(run, time.time));
        for (const run of [...ids, asked]) {
          const got = runArchives(files, run);
          const want = files.filter((_, i) => archives[i].run === run);
          expect(got.map((a) => a.path).toSorted()).toEqual(
            want.map((f) => f.path).toSorted(),
          );

          // newest first, each at the second its name gives
          const times = got.map((a) => rfc3339Ms(a.started));
          expect(times).toEqual(times.toSorted((a, b) => b - a));
          for (const a of got) {
            const i = files.findIndex((f) => f.path === a.path);
            const ms = archives[i].time.ms;
            expect(rfc3339Ms(a.started)).toBe(ms - (ms % 1000));
          }
        }
      }),
    );
  });
});
