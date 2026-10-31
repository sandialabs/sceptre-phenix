// The type a disk shows, such as qcow2 v3, how types sort, and the text and
// names of a format issue.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  FORMATS_DOCS,
  compareDiskTypes,
  diskType,
  formatIssueLevel,
  formatIssueName,
  formatIssueText,
} from '@/utils/diskFormat.js';
import { disk } from './helpers/fixtures.js';

const typed = (name, fields) =>
  disk(`/phenix/images/${name}`, name, { formatIssue: null, ...fields });

describe('diskType', () => {
  it.each([
    ['qcow2 v3', { format: 'qcow2', formatVersion: 3 }],
    ['qcow2 v2', { format: 'qcow2', formatVersion: 2 }],
    ['qcow v1', { format: 'qcow', formatVersion: 1 }],
    ['qcow2', { format: 'qcow2', formatVersion: 0 }],
    ['raw', { format: 'raw', formatVersion: 0 }],
    ['vmdk', { format: 'vmdk', formatVersion: 0 }],
    ['qed', { format: 'qed', formatVersion: 0 }],
    ['vdi', { format: 'vdi' }],
    ['ISO', { kind: 'ISO', format: 'raw', formatVersion: 0 }],
    ['tgz', { kind: 'Container', format: 'raw', formatVersion: 0 }],
    ['unknown', { format: '' }],
    ['unknown', {}],
  ])('is %s for %j', (want, fields) => {
    expect(diskType(typed('kali.qc2', fields))).toBe(want);
  });
});

describe('compareDiskTypes', () => {
  it('sorts types as text, versions by number, then by label', () => {
    const disks = [
      typed('a.vmdk', { format: 'vmdk' }),
      typed('b.qc2', { format: 'qcow2', formatVersion: 3 }),
      typed('c.qc2', { format: 'qcow2', formatVersion: 10 }),
      typed('d.qcow', { format: 'qcow', formatVersion: 1 }),
      typed('e.iso', { kind: 'ISO', format: 'raw' }),
      typed('f.img', { format: 'raw' }),
      typed('a.qc2', { format: 'qcow2', formatVersion: 3 }),
      typed('g.qc2', { format: 'qcow2', formatVersion: 2 }),
    ];

    expect(
      disks.toSorted(compareDiskTypes).map((d) => [diskType(d), d.name]),
    ).toEqual([
      ['ISO', 'e.iso'],
      ['qcow v1', 'd.qcow'],
      ['qcow2 v2', 'g.qc2'],
      ['qcow2 v3', 'a.qc2'],
      ['qcow2 v3', 'b.qc2'],
      ['qcow2 v10', 'c.qc2'],
      ['raw', 'f.img'],
      ['vmdk', 'a.vmdk'],
    ]);
  });

  it('orders any two qcow2 disks by their version, and is antisymmetric', () => {
    fc.assert(
      fc.property(
        fc.nat(20),
        fc.nat(20),
        fc.boolean(),
        (versionA, versionB, sameName) => {
          const a = typed('a.qc2', {
            format: 'qcow2',
            formatVersion: versionA,
          });
          const b = typed(sameName ? 'a.qc2' : 'b.qc2', {
            format: 'qcow2',
            formatVersion: versionB,
          });
          const order = Math.sign(compareDiskTypes(a, b));
          expect(order).toBe(-Math.sign(compareDiskTypes(b, a)) || 0);
          if (versionA > 0 && versionB > 0 && versionA !== versionB) {
            expect(order).toBe(Math.sign(versionA - versionB));
          }
          if (versionA === versionB && sameName) expect(order).toBe(0);
        },
      ),
    );
  });
});

describe('format issues', () => {
  const issue = (fields) => ({
    level: 'warning',
    problem: '/phenix/images/kali.qc2 is a qcow2 version 2 image',
    fix: 'Upgrade it to version 3',
    command: 'qemu-img amend -f qcow2 -o compat=1.1 /phenix/images/kali.qc2',
    ...fields,
  });

  it.each([
    [
      'a warning with a command',
      issue(),
      'warning',
      'Format warning',
      '/phenix/images/kali.qc2 is a qcow2 version 2 image. Upgrade it to ' +
        'version 3: qemu-img amend -f qcow2 -o compat=1.1 ' +
        '/phenix/images/kali.qc2',
    ],
    [
      'an error with no command',
      issue({ level: 'error', fix: 'Write a copy', command: '' }),
      'error',
      'Format error',
      '/phenix/images/kali.qc2 is a qcow2 version 2 image. Write a copy.',
    ],
    ['no issue', null, null, '', ''],
    ['a level the page does not know', issue({ level: 'info' }), null, '', ''],
  ])('reads %s', (_, formatIssue, level, name, text) => {
    const d = typed('kali.qc2', { formatIssue });
    expect([
      formatIssueLevel(d),
      formatIssueName(d),
      formatIssueText(d),
    ]).toEqual([level, name, text]);
  });

  it('gives the problem first, and the whole command last, for any issue', () => {
    const words = fc.stringMatching(/^[a-z0-9 /.<>'-]{1,30}$/);
    fc.assert(
      fc.property(
        fc.record({
          level: fc.constantFrom('warning', 'error'),
          problem: words,
          fix: words,
          command: fc.option(words, { nil: '' }),
        }),
        (formatIssue) => {
          const text = formatIssueText(typed('kali.qc2', { formatIssue }));
          expect(text.startsWith(`${formatIssue.problem}. `)).toBe(true);
          if (formatIssue.command) {
            expect(text.endsWith(`: ${formatIssue.command}`)).toBe(true);
          } else {
            expect(text.endsWith('.')).toBe(true);
          }
        },
      ),
    );
  });

  it('links the docs section of image format versions', () => {
    expect(FORMATS_DOCS).toBe(
      'https://phenix.sceptre.dev/latest/disks/#image-format-versions',
    );
  });
});
