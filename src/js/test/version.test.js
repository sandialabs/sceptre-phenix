import { describe, expect, test } from 'vitest';
import { formatVersion } from '@/utils/version.js';

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
});
