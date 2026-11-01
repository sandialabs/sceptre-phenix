// What the Disks page's details card works out from a disk and its details:
// health, allocation, inject partitions, qcow features, users and commands.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  allocation,
  diskHealth,
  filesystemName,
  healthProblems,
  injectProblems,
  injectTargets,
  partitionTableName,
  qcowFeatures,
  qemuImgInfoCommand,
  usedBy,
} from '@/utils/diskDetails.js';
import { shellQuote, shellWord } from '@/utils/shell.js';
import { disk, diskDetails, partition } from './helpers/fixtures.js';

const kali = disk('/phenix/images/kali.qc2', 'kali.qc2', {
  experiments: [{ name: 'bennu', running: true }],
});

// a use of kali.qc2 by a VM's first drive, injecting into a partition
const use = (vm, injectPartition, fields = {}) => ({
  experiment: 'bennu',
  running: true,
  vm,
  drive: 0,
  injectPartition,
  ...fields,
});

describe('diskHealth', () => {
  const V2 = {
    level: 'warning',
    problem: 'kali.qc2 is a qcow2 version 2 image',
    fix: 'Upgrade it',
    command: 'qemu-img amend -f qcow2 -o compat=1.1 kali.qc2',
  };

  it.each([
    ['a whole image in the files directory', {}, null, 'ok'],
    ['an old version', { formatIssue: V2 }, null, 'warning'],
    [
      'an image outside the files directory',
      { outsideFilesDir: true },
      null,
      'warning',
    ],
    [
      'a VM that injects into a partition the disk lacks',
      {},
      diskDetails({ vms: [use('kali', 3)] }),
      'warning',
    ],
    ['a broken chain', { brokenChain: true }, null, 'danger'],
    [
      'an image qemu refuses',
      { formatIssue: { ...V2, level: 'error' } },
      null,
      'danger',
    ],
    [
      'a broken chain outside the files directory',
      { brokenChain: true, outsideFilesDir: true },
      null,
      'danger',
    ],
  ])('rates %s', (_, fields, details, want) => {
    expect(diskHealth({ ...kali, ...fields }, details)).toBe(want);
  });

  it('lists the problems with their icons, the most serious first', () => {
    const problems = healthProblems(
      {
        ...kali,
        outsideFilesDir: true,
        formatIssue: V2,
        brokenChain: true,
        chainProblem: 'backing file /phenix/images/gone.qc2 is missing',
      },
      diskDetails({ vms: [use('kali', 3)] }),
    );
    expect(problems).toEqual([
      {
        level: 'danger',
        icon: 'times-circle',
        text: 'The backing chain is broken: backing file /phenix/images/gone.qc2 is missing.',
      },
      {
        level: 'warning',
        icon: 'exclamation-triangle',
        text: `Format warning: ${V2.problem}.`,
      },
      {
        level: 'warning',
        icon: 'exclamation-triangle',
        text: 'Outside the standard images directory.',
      },
      {
        level: 'warning',
        icon: 'exclamation-triangle',
        text: 'VM kali (bennu) injects files into partition 3, which is not on this disk.',
      },
    ]);
  });
});

describe('allocation', () => {
  it('gives the space on disk as a percent of the virtual size', () => {
    expect(allocation(kali, diskDetails())).toEqual({
      percent: 20,
      bar: 20,
      text: '20%',
    });
    // the list's sizes until the details load
    expect(
      allocation({ ...kali, size: '512 MiB', virtualSize: '80 GiB' }, null),
    ).toMatchObject({ text: '<1%' });
    expect(allocation({ ...kali, size: '', virtualSize: '' }, null)).toBe(null);
  });

  it('keeps the bar within 0 to 100 and the text to the rounded percent', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 2 ** 45 }),
        fc.integer({ min: 1, max: 2 ** 45 }),
        (used, total) => {
          const got = allocation(
            {},
            { allocatedBytes: used, virtualBytes: total },
          );
          const percent = (used / total) * 100;
          expect(got.percent).toBeCloseTo(percent);
          expect(got.bar).toBe(Math.min(100, percent));
          expect(got.bar).toBeGreaterThan(0);
          expect(got.bar).toBeLessThanOrEqual(100);
          expect(got.text).toBe(
            percent < 1 ? '<1%' : `${Math.round(percent)}%`,
          );
        },
      ),
    );
  });
});

describe('inject partitions', () => {
  it("marks each partition that a VM's first drive injects into", () => {
    const details = diskDetails({
      vms: [
        use('kali', 1),
        use('web', 1, { experiment: 'demo' }),
        // a second drive gets no files, and 0 is no inject partition
        use('db', 5, { drive: 1 }),
        use('dns', 0),
      ],
    });
    expect([...injectTargets(details)]).toEqual([
      [1, ['kali (bennu)', 'web (demo)']],
    ]);
    expect(injectProblems(details)).toEqual([]);
  });

  it('says which inject partitions phēnix cannot write files to', () => {
    const details = diskDetails({
      partitions: [
        ...diskDetails().partitions,
        partition(6, 'Linux', '0x83', { logical: true }),
        partition(7, 'Linux LVM', '0x8e', { logical: true, fs: 'LVM2_member' }),
      ],
      vms: [
        use('a', 2),
        use('b', 5),
        use('c', 6),
        use('d', 7),
        use('e', 9),
        use('f', 9),
      ],
    });
    expect(injectProblems(details)).toEqual([
      {
        number: 2,
        text: 'VM a (bennu) injects files into partition 2, an extended partition, which holds no file system.',
      },
      {
        number: 5,
        text: 'VM b (bennu) injects files into partition 5, which holds Linux swap, not a file system that phēnix can write files to.',
      },
      {
        number: 6,
        text: 'VM c (bennu) injects files into partition 6, where phēnix found no file system that it knows.',
      },
      {
        number: 7,
        text: 'VM d (bennu) injects files into partition 7, which holds LVM physical volume, not a file system that phēnix can write files to.',
      },
      {
        number: 9,
        text: 'VMs e (bennu) and f (bennu) inject files into partition 9, which is not on this disk.',
      },
    ]);
  });

  it.each(['none', 'unknown', ''])(
    'finds no problem when phēnix read no MBR or GPT table (%s)',
    (partitionTable) => {
      const details = diskDetails({
        partitionTable,
        partitions: [],
        vms: [use('kali', 3)],
      });
      expect(injectProblems(details)).toEqual([]);
    },
  );
});

describe('names', () => {
  it.each([
    [{ type: 'ext4', version: '1.0' }, 'ext4 1.0'],
    [{ type: 'vfat', version: 'FAT32' }, 'FAT32'],
    [{ type: 'vfat', version: '' }, 'FAT'],
    [{ type: 'crypto_LUKS', version: '2' }, 'LUKS 2'],
    [{ type: 'swap', version: '' }, 'Linux swap'],
    [{ type: 'iso9660', version: '' }, 'ISO 9660'],
    [null, ''],
  ])('names the file system %o %s', (fs, want) => {
    expect(filesystemName(fs)).toBe(want);
  });

  it('names the partition table schemes, and none it does not know', () => {
    expect(
      ['mbr', 'gpt', 'none', 'unknown', '', 'apm'].map(partitionTableName),
    ).toEqual(['MBR', 'GPT', 'None', 'Unknown', '', '']);
  });
});

describe('qcowFeatures', () => {
  it('tags the features that need a note, the most serious first', () => {
    const qcow = {
      ...diskDetails().qcow,
      corrupt: true,
      dirty: true,
      encryption: 'luks',
      externalDataFile: true,
      compression: 'zstd',
      extendedL2: true,
      lazyRefcounts: true,
    };
    expect(qcowFeatures(qcow).map((f) => [f.label, f.type])).toEqual([
      ['Corrupt', 'is-danger'],
      ['LUKS encryption', 'is-danger'],
      ['Not closed cleanly', 'is-warning'],
      ['External data file', 'is-warning'],
      ['zstd compression', 'is-dark'],
      ['Extended L2 entries', 'is-dark'],
      ['Lazy refcounts', 'is-dark'],
    ]);
    expect(qcowFeatures(diskDetails().qcow)).toEqual([]);
    expect(qcowFeatures(null)).toEqual([]);
  });
});

describe('usedBy', () => {
  it('joins the list experiments with the VMs of the details, by name', () => {
    const listed = {
      ...kali,
      experiments: [
        { name: 'demo', running: false },
        { name: 'bennu', running: true },
      ],
    };
    expect(usedBy(listed, diskDetails())).toEqual([
      { name: 'bennu', running: true, vms: [use('kali', 1)] },
      { name: 'demo', running: false, vms: [] },
    ]);
    // before the details load, the list alone
    expect(usedBy(listed, null).map((e) => e.vms)).toEqual([[], []]);
  });
});

// the word a POSIX shell reads from one shellWord, for quoting it allows
function shellRead(word) {
  let out = '';
  for (let i = 0; i < word.length; ) {
    if (word[i] === "'") {
      const end = word.indexOf("'", i + 1);
      out += word.slice(i + 1, end);
      i = end + 1;
    } else if (word[i] === '\\') {
      out += word[i + 1];
      i += 2;
    } else {
      out += word[i];
      i += 1;
    }
  }
  return out;
}

describe('the qemu-img info command', () => {
  it('names the image as one shell word, quoted only when it needs it', () => {
    expect(qemuImgInfoCommand('/phenix/images/kali.qc2')).toBe(
      'qemu-img info --backing-chain -U /phenix/images/kali.qc2',
    );
    expect(qemuImgInfoCommand("/data/kali's disk.qc2")).toBe(
      `qemu-img info --backing-chain -U '/data/kali'\\''s disk.qc2'`,
    );
  });

  it('gives back any path when a shell reads the word', () => {
    fc.assert(
      fc.property(fc.string(), (path) => {
        const word = shellWord(path);
        expect(shellRead(word)).toBe(path);
        if (word !== path) expect(word).toBe(shellQuote(path));
      }),
    );
  });
});
