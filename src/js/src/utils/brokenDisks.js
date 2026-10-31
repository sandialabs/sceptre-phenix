// How the UI shows a start refused for missing disk images: the server's
// broken_disks error (BrokenDisk in openapi.yml), or a bulk VM job's errors.
import { h } from 'vue';

import { plural } from '@/utils/plural.js';

const BROKEN_DISKS_CODE = 'broken_disks';

// the most problems a notification lists before it puts the rest in a
// disclosure
export const SHOWN_DISK_PROBLEMS = 10;

// how a bulk VM job's item error starts (experiment.ErrBrokenDisks in Go)
const JOB_ERROR_PREFIX = 'broken VM disk images: ';

// The broken disks of a JSON error body, or null when it is not a
// broken_disks error that lists them.
export function brokenDisks(body) {
  if (body?.code !== BROKEN_DISKS_CODE) return null;
  const disks = body.details?.disks;
  return Array.isArray(disks) && disks.length ? disks : null;
}

// a broken disk's drive and image, then what is wrong with it
export const diskProblemLine = (disk) =>
  `${disk.drive === 'cdrom' ? 'CD-ROM' : 'disk'} ${disk.image}: ${disk.problem}`;

// The count of the broken disks, and of the VMs when given, as the server
// words it: "2 disks on 1 VM are missing or have a broken backing chain".
export function brokenDisksSummary(disks, vms) {
  const on = vms === undefined ? '' : ` on ${plural(vms, 'VM')}`;
  const verb = disks === 1 ? 'is missing or has' : 'are missing or have';
  return `${plural(disks, 'disk')}${on} ${verb} a broken backing chain`;
}

// The broken disks by VM, in the order given: [{ vm, lines }].
export function disksByVM(disks) {
  const groups = [];
  for (const disk of disks ?? []) {
    let group = groups.at(-1);
    if (group?.vm !== disk.vm) {
      group = { vm: disk.vm, lines: [] };
      groups.push(group);
    }
    group.lines.push(diskProblemLine(disk));
  }
  return groups;
}

// The problems a bulk VM job's item error lists, each without the VM's name
// (the item names it), or null when it is not a broken disks error.
export function jobDiskProblems(error) {
  const text = String(error ?? '');
  if (!text.startsWith(JOB_ERROR_PREFIX)) return null;
  return text
    .slice(JOB_ERROR_PREFIX.length)
    .split(/\. (?=VM )/)
    .map((problem) => problem.replace(/^VM [^:]*: /, ''))
    .filter(Boolean);
}

// a list of VMs, each with a list of its broken disks
const vmList = (disks) =>
  h(
    'ul',
    { class: 'broken-disk-list' },
    disksByVM(disks).map(({ vm, lines }) =>
      h('li', [
        h('b', `VM ${vm}`),
        h(
          'ul',
          lines.map((line) => h('li', line)),
        ),
      ]),
    ),
  );

// A refused start's alert: the title, the count, then each VM and its broken
// disks; past SHOWN_DISK_PROBLEMS, the rest are in a <details> disclosure.
export function brokenDisksBody(title, disks) {
  const vms = new Set(disks.map((disk) => disk.vm)).size;
  const rest = disks.slice(SHOWN_DISK_PROBLEMS);
  return h('div', { role: 'alert', class: 'broken-disks' }, [
    h('span', { class: 'error-title' }, [h('b', 'Error:'), ` ${title}`]),
    h('span', { class: 'error-step' }, brokenDisksSummary(disks.length, vms)),
    vmList(disks.slice(0, SHOWN_DISK_PROBLEMS)),
    ...(rest.length
      ? [
          h('details', { class: 'broken-disks-more' }, [
            h('summary', `Show ${plural(rest.length, 'more disk')}`),
            vmList(rest),
          ]),
        ]
      : []),
  ]);
}
