import { baseName, findDisk, minimegaSafe } from '@/utils/diskChain.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { actionState } from '@/utils/operations.js';
import { pluralWord } from '@/utils/plural.js';

// Which disk actions a role may take on a disk in its current state, and why
// not. The Disks page's details window and each row's buttons share these.

// What each disk action needs: its RBAC verb and the disk name checked (none
// to create one), whether it reads the disk into a new one (so needs get too),
// whether minimega runs it on the path, whether a running experiment using the
// disk rules it out, and, for VM-disk-only actions, the word for a used disk.
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

const NO_PERMISSION = 'you do not have permission';

// Why the server refuses every action on a disk it lists as read-only:
// phēnix acts only on images in the minimega files directory, and not on
// the paths there the disk list leaves out, such as hidden files and the
// folders it keeps for experiment files.
const readOnlyReason = (disk) =>
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

// the code of a delete the server refused because other images or
// experiments use the disk
const DISK_IN_USE_CODE = 'disk_in_use';

// the most images and experiments the delete dialog names
export const SHOWN_DISK_USERS = 10;

// The images and experiments that use a disk, from the JSON body of a refused
// delete, or null when it is not a disk_in_use error. hidden counts the ones
// the role may not see.
export function diskUsers(body) {
  if (body?.code !== DISK_IN_USE_CODE) return null;
  const details = body.details ?? {};
  return {
    images: Array.isArray(details.images) ? details.images : [],
    experiments: Array.isArray(details.experiments) ? details.experiments : [],
    hidden: Number.isInteger(details.hidden) ? details.hidden : 0,
  };
}

// The HTML of the dialog that asks to delete a disk in use: what uses the
// disk, at most SHOWN_DISK_USERS of them. label gives an image's name from its
// path. Every name is escaped.
export function diskUsersHTML(name, users, label = (path) => path) {
  const items = [
    ...users.images.map((path) => `image <b>${escapeHTML(label(path))}</b>`),
    ...users.experiments.map(
      (exp) =>
        `experiment <b>${escapeHTML(exp.name)}</b>${exp.running ? ' (running)' : ''}`,
    ),
  ];
  const shown = items.slice(0, SHOWN_DISK_USERS);
  const more = users.hidden + items.length - shown.length;
  if (more) {
    const what = pluralWord(
      more,
      'image or experiment',
      'images or experiments',
    );
    shown.push(
      shown.length ? `and ${more} more` : `${more} ${what} that you cannot see`,
    );
  }
  const running = users.experiments.some((exp) => exp.running)
    ? '<p>minimega can hold the disk open while an experiment runs.</p>'
    : '';
  return (
    `<p>These images and experiments use <b>${escapeHTML(name)}</b>. ` +
    'If you delete it, they stop working.</p>' +
    `<div class="content"><ul>${shown.map((item) => `<li>${item}</li>`).join('')}</ul></div>` +
    running
  );
}
