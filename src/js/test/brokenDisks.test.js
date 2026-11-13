// How a start refused for broken disk images reads: the server's broken_disks
// error, and the item errors of a bulk VM start or restart job.
import fc from 'fast-check';
import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';

import {
  SHOWN_DISK_PROBLEMS,
  brokenDisks,
  brokenDisksBody,
  brokenDisksSummary,
  disksByVM,
  jobDiskProblems,
} from '@/utils/brokenDisks.js';

const MISSING = 'backing file /phenix/images/ro_kali.qc2 is missing';

const problem = (vm, image, fields = {}) => ({
  vm,
  drive: 'disk',
  image,
  path: `/phenix/images/${image}`,
  missing: '/phenix/images/ro_kali.qc2',
  problem: MISSING,
  ...fields,
});

const disks = [
  problem('host-01', 'kali.qc2'),
  problem('host-01', 'tools.iso', {
    drive: 'cdrom',
    problem: 'CD-ROM image /phenix/images/tools.iso is missing',
  }),
  problem('host-02', 'win/overlay.qc2', {
    missing: '',
    problem: 'backing chain loops back to /phenix/images/win/overlay.qc2',
  }),
];

// mounts the notification body of a refused start
const render = (title, list) =>
  mount(
    { render: () => brokenDisksBody(title, list) },
    { attachTo: document.body },
  );

// each VM heading and its lines, from a list the body draws
const listed = (list) =>
  list.findAll(':scope > li').map((li) => ({
    vm: li.find('b').text(),
    lines: li.findAll('li').map((item) => item.element.textContent),
  }));

// a broken disk on one of a few VMs, its problem text free of the job's
// separator
const problemArb = fc.record({
  vm: fc.constantFrom('a', 'b', 'host-01'),
  drive: fc.constantFrom('disk', 'cdrom'),
  image: fc.stringMatching(/^[a-z0-9_/.-]{1,10}$/),
  problem: fc
    .string({ minLength: 1, maxLength: 20 })
    .filter((s) => !s.includes('. VM ')),
});

describe('brokenDisks', () => {
  it('gives the list of a broken_disks error only', () => {
    const body = { message: 'm', code: 'broken_disks', details: { disks } };
    expect(brokenDisks(body)).toBe(disks);
    expect(brokenDisks({ ...body, code: 'invalid_request' })).toBe(null);
    expect(brokenDisks({ ...body, details: {} })).toBe(null);
    expect(brokenDisks({ ...body, details: { disks: [] } })).toBe(null);
    expect(brokenDisks(null)).toBe(null);
  });
});

describe('brokenDisksSummary', () => {
  it.each([
    [1, 1, 'qemu cannot open 1 disk on 1 VM'],
    [3, 2, 'qemu cannot open 3 disks on 2 VMs'],
    [2, undefined, 'qemu cannot open 2 disks'],
  ])('counts %s disks on %s VMs as the server does', (n, vms, want) => {
    expect(brokenDisksSummary(n, vms)).toBe(want);
  });
});

describe('disksByVM', () => {
  it('groups each run of a VM, naming the drive, image and problem', () => {
    expect(disksByVM(disks)).toEqual([
      {
        vm: 'host-01',
        lines: [
          `disk kali.qc2: ${MISSING}`,
          'CD-ROM tools.iso: CD-ROM image /phenix/images/tools.iso is missing',
        ],
      },
      {
        vm: 'host-02',
        lines: [
          'disk win/overlay.qc2: backing chain loops back to /phenix/images/win/overlay.qc2',
        ],
      },
    ]);
  });

  it('keeps every disk once, in order, under its own VM', () => {
    fc.assert(
      fc.property(fc.array(problemArb, { maxLength: 30 }), (list) => {
        const groups = disksByVM(list);
        expect(groups.flatMap((g) => g.lines.map(() => g.vm))).toEqual(
          list.map((d) => d.vm),
        );
        expect(groups.flatMap((g) => g.lines)).toEqual(
          list.map(
            (d) =>
              `${d.drive === 'cdrom' ? 'CD-ROM' : 'disk'} ${d.image}: ${d.problem}`,
          ),
        );
        groups.slice(1).forEach((g, i) => expect(g.vm).not.toBe(groups[i].vm));
      }),
    );
  });
});

describe('jobDiskProblems', () => {
  it("reads a bulk VM job's error for broken disks, without the VM's name", () => {
    expect(
      jobDiskProblems(
        `broken VM disk images: VM host-01: disk kali.qc2: ${MISSING}. ` +
          'VM host-01: CD-ROM tools.iso: CD-ROM image /phenix/images/tools.iso is missing',
      ),
    ).toEqual([
      `disk kali.qc2: ${MISSING}`,
      'CD-ROM tools.iso: CD-ROM image /phenix/images/tools.iso is missing',
    ]);
  });

  it('leaves any other error alone', () => {
    expect(jobDiskProblems('vm is locked')).toBe(null);
    expect(jobDiskProblems(null)).toBe(null);
  });

  it('gives back the problems the server joined, as the server joins them', () => {
    fc.assert(
      fc.property(
        fc.constantFrom('a', 'host-01'),
        fc.array(problemArb, { minLength: 1, maxLength: 6 }),
        (vm, list) => {
          const lines = disksByVM(list.map((d) => ({ ...d, vm })))[0].lines;
          const error = `broken VM disk images: ${lines.map((l) => `VM ${vm}: ${l}`).join('. ')}`;
          expect(jobDiskProblems(error)).toEqual(lines);
        },
      ),
    );
  });
});

describe('brokenDisksBody', () => {
  it('is an alert with the title, the count, then each VM and its disks', () => {
    const body = render('unable to start experiment foo', disks);
    const alert = body.find('[role="alert"]');

    expect(alert.find('.error-title').text()).toBe(
      'Error: unable to start experiment foo',
    );
    expect(alert.find('.error-step').text()).toBe(
      'qemu cannot open 3 disks on 2 VMs',
    );
    expect(listed(alert.find('.broken-disk-list'))).toEqual(
      disksByVM(disks).map(({ vm, lines }) => ({ vm: `VM ${vm}`, lines })),
    );
    expect(alert.find('details').exists()).toBe(false);
    body.unmount();
  });

  it('draws server text as text, never as markup', () => {
    const body = render('<img src=x>', [
      problem('<b>vm</b>', '<i>x</i>.qc2', { problem: '<script>bad</script>' }),
    ]);
    expect(body.find('img, script, i').exists()).toBe(false);
    expect(body.text()).toContain('<script>bad</script>');
    body.unmount();
  });

  it(`lists ${SHOWN_DISK_PROBLEMS} disks, and every other one in a disclosure the keyboard reaches`, () => {
    fc.assert(
      fc.property(
        fc.array(problemArb, { minLength: 1, maxLength: 25 }),
        (list) => {
          const body = render('unable to start experiment foo', list);
          const [shown, more] = body.findAll('.broken-disk-list');
          const lines = (el) => (el ? listed(el).flatMap((g) => g.lines) : []);
          const all = disksByVM(list).flatMap((g) => g.lines);

          expect(lines(shown)).toEqual(all.slice(0, SHOWN_DISK_PROBLEMS));
          expect(lines(more)).toEqual(all.slice(SHOWN_DISK_PROBLEMS));
          const details = body.find('details');
          expect(details.exists()).toBe(list.length > SHOWN_DISK_PROBLEMS);
          if (details.exists()) {
            const rest = list.length - SHOWN_DISK_PROBLEMS;
            expect(details.find('summary').text()).toBe(
              `Show ${rest} more disk${rest === 1 ? '' : 's'}`,
            );
          }
          body.unmount();
        },
      ),
      { numRuns: 30 },
    );
  });
});
