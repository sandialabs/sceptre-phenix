import fc from 'fast-check';
import { describe, expect, test } from 'vitest';
import { formatVersion } from '@/utils/version.js';

const upstream = 'sandialabs/sceptre-phenix';

// v<major>.<minor>.<patch>, with an optional prerelease and build suffix
const releaseTag = fc
  .tuple(
    fc.nat(),
    fc.nat(),
    fc.nat(),
    fc.option(fc.stringMatching(/^[0-9A-Za-z.-]{1,8}$/), { nil: '' }),
    fc.option(fc.stringMatching(/^[0-9A-Za-z.-]{1,8}$/), { nil: '' }),
  )
  .map(
    ([major, minor, patch, pre, meta]) =>
      `v${major}.${minor}.${patch}${pre && `-${pre}`}${meta && `+${meta}`}`,
  );

// branch names have no dots, so none is a release; the rest are near misses
const otherTag = fc.oneof(
  fc.stringMatching(/^[A-Za-z0-9_/-]{1,16}$/),
  fc.constantFrom(
    'tag not set',
    '1.2.3',
    'V1.2.3',
    'v1.2',
    'v1.2.3.4',
    'v1.2.3-',
    'v1.2.3+',
    'v1.2.3 ',
    'release-v1.2.3',
  ),
);

const forkRepo = fc
  .stringMatching(/^[A-Za-z0-9-]{1,10}\/[A-Za-z0-9._-]{1,12}$/)
  .filter((repo) => repo !== upstream);
const notFork = fc.constantFrom(undefined, '', 'repo not set', upstream);
const repoArb = fc.oneof(
  forkRepo.map((repo) => ({ repo, fork: true })),
  notFork.map((repo) => ({ repo, fork: false })),
);

const commitArb = fc.stringMatching(/^[0-9a-f]{7,40}$/);
const buildDateArb = fc
  .date({
    min: new Date('2000-01-01T00:00:00Z'),
    max: new Date('2100-01-01T00:00:00Z'),
    noInvalidDate: true,
  })
  .map((d) =>
    d
      .toISOString()
      .replace('T', ' ')
      .replace(/\.\d+Z$/, ' UTC'),
  );

const readVersion = (text) => {
  const match = text.match(
    /^(Version|Branch) (.+?)(?: \[(.+)\])? \(commit ([0-9a-f]+), built on (.+)\)$/,
  );
  expect(match).not.toBeNull();
  const [, kind, tag, repo, commit, buildDate] = match;
  return { kind, tag, repo, commit, buildDate };
};

describe('formatVersion', () => {
  const build = {
    commit: 'abc1234',
    buildDate: '2026-09-09 01:43:21 UTC',
  };

  test('includes a release version with commit and build date', () => {
    expect(formatVersion({ ...build, tag: 'v2026.08.26' })).toBe(
      'Version v2026.08.26 (commit abc1234, built on 2026-09-09 01:43:21 UTC)',
    );
  });

  test('includes an upstream branch name with commit and build date', () => {
    expect(
      formatVersion({
        ...build,
        tag: 'main',
        repo: 'sandialabs/sceptre-phenix',
      }),
    ).toBe('Branch main (commit abc1234, built on 2026-09-09 01:43:21 UTC)');
  });

  test('includes a fork repo with branch builds', () => {
    expect(
      formatVersion({
        ...build,
        tag: 'main',
        repo: 'GhostofGoes/sceptre-phenix',
      }),
    ).toBe(
      'Branch main [ghostofgoes/sceptre-phenix] (commit abc1234, built on 2026-09-09 01:43:21 UTC)',
    );
  });

  test('reads a release tag as a version, whichever repo built it', () => {
    fc.assert(
      fc.property(
        releaseTag,
        repoArb,
        commitArb,
        buildDateArb,
        (tag, { repo }, commit, buildDate) => {
          expect(
            readVersion(formatVersion({ commit, tag, buildDate, repo })),
          ).toEqual({
            kind: 'Version',
            tag,
            repo: undefined,
            commit,
            buildDate,
          });
        },
      ),
    );
  });

  test('reads any other tag as a branch, naming the repo only for a fork', () => {
    fc.assert(
      fc.property(
        otherTag,
        repoArb,
        commitArb,
        buildDateArb,
        (tag, { repo, fork }, commit, buildDate) => {
          expect(
            readVersion(formatVersion({ commit, tag, buildDate, repo })),
          ).toEqual({
            kind: 'Branch',
            tag,
            repo: fork ? repo.toLowerCase() : undefined,
            commit,
            buildDate,
          });
        },
      ),
    );
  });
});
