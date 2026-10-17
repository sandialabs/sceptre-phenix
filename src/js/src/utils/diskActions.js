import { baseName, findDisk, minimegaSafe } from '@/utils/diskChain.js';
import { actionState } from '@/utils/operations.js';

// Which disk actions a role may take on a disk in its current state, and why
// not. The Disks page's details window and each row's buttons share these.

// What each disk action needs: the RBAC verb, the disk name the role is
// checked against (none to create a disk), whether the action reads the disk
// into a new one, so the role must also be allowed to get it, whether
// minimega runs it on the disk's path, whether a running experiment using the
// disk rules it out, and, for actions only VM disks allow, the word for a
// disk they were used on.
const byName = (disk) => disk.name;
export const DISK_ACTION_RULES = {
  snapshot: {
    verb: 'create',
    reads: true,
    minimega: true,
    idle: true,
    vmOnly: 'snapshotted',
  },
  commit: {
    verb: 'update',
    // committing writes the image into its backing image, so the role
    // must be allowed to update both
    name: (disk) => [disk.name, baseName(disk.backingImages[0])],
    minimega: true,
    idle: true,
    vmOnly: 'committed',
  },
  rebase: {
    verb: 'update',
    name: byName,
    minimega: true,
    idle: true,
    vmOnly: 'rebased',
  },
  clone: { verb: 'create', reads: true },
  resize: { verb: 'update', name: byName, minimega: true, idle: true },
  download: { verb: 'get', name: byName },
  rename: { verb: 'update', name: byName, idle: true },
  delete: { verb: 'delete', name: byName, idle: true },
};

const NO_PERMISSION = "you don't have permission";

// Why the server refuses every action on a disk it lists as read-only:
// phēnix acts only on images in the minimega files directory, and not on
// the paths there the disk list leaves out, such as hidden files and the
// folders it keeps for experiment files.
export const readOnlyReason = (disk) =>
  disk.outsideFilesDir
    ? 'outside the standard images directory'
    : 'at a path the disk list leaves out';

// the running experiments using a disk, which hold it in use
export function diskRunningText(disk) {
  const running = (disk.experiments ?? []).filter((exp) => exp.running);
  return running.length
    ? running.map((exp) => exp.name).join(', ')
    : 'a running experiment';
}

// Why an action can't be used on a disk, or null when it can. `disks` is the
// disk list, which holds the disk's backing image; `can(verb, name)` answers
// whether the role may take the verb on the disks named name (or on any disk
// when name is undefined).
export function diskDisabledReason(action, disk, { disks = [], can }) {
  const rule = DISK_ACTION_RULES[action];

  if (disk.readOnly) {
    return `the disk is ${readOnlyReason(disk)}`;
  }
  if (rule.minimega && !minimegaSafe(disk.fullPath)) {
    return "minimega cannot use the disk's path";
  }
  if (rule.idle && disk.inUse) {
    return `the disk is in use by ${diskRunningText(disk)}`;
  }
  if (rule.vmOnly && disk.kind != 'VM') {
    return `only VM disks can be ${rule.vmOnly}`;
  }
  if (action === 'commit') {
    if (!disk.backingImages?.length) {
      return 'the disk has no backing image to commit into';
    }
    const backing = findDisk(disks, disk.backingImages[0]);
    if (backing?.readOnly) {
      return `its backing image is ${readOnlyReason(backing)}`;
    }
  }
  const permitted =
    (rule.name
      ? [rule.name(disk)].flat().every((name) => can(rule.verb, name))
      : can(rule.verb)) &&
    (!rule.reads || can('get', disk.name));
  return permitted ? null : NO_PERMISSION;
}

// Whether and how to offer a disk action ({ name, label, description, icon })
// on a disk, as actionState describes it; options as for diskDisabledReason.
export function diskActionState(action, disk, opts) {
  const reason = diskDisabledReason(action.name, disk, opts);
  return actionState(action, {
    permitted: reason !== NO_PERMISSION,
    blocked: reason === NO_PERMISSION ? null : reason,
  });
}
