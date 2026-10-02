// A VM's disk image and the images backing it, from the disk list the Disks
// page shows (GET disks), which already carries each image's backing chain,
// and how pages name and offer the images in that list. Each image is known
// by its full path: two images may share a file name in different folders.
import { recentPage } from '@/utils/pageCache.js';
import { pageFetchers } from '@/utils/pageData.js';

// a disk list this recent is reused rather than asked for again
export const DISK_LIST_REUSE_MS = 60 * 1000;

// the name of the group of images outside the minimega files directory
export const OUTSIDE_GROUP = 'Outside the standard images directory';

// The disk list, from the Disks page's cache when recent, joining its request
// when one is running, or else loading it into that cache.
export function diskList() {
  return recentPage('disks', pageFetchers.disks, DISK_LIST_REUSE_MS);
}

// a path's file name
export const baseName = (path) => String(path ?? '').replace(/^.*\//, '');

// What a path must not hold for minimega to take it as one literal path, as
// the server checks it (minimegaSafe in src/go/api/disk/paths.go): globMeta
// in src/go/util/file/delete.go (glob syntax, quotes and the comment sign),
// control characters and white space, `$`, which minimega expands, and `,`,
// which separates a VM disk's options.
const MINIMEGA_UNSAFE = /[*?[\]\\{}"'`#$,\p{Cc}\p{White_Space}]/u;

// whether minimega can take the path as a VM's disk
export const minimegaSafe = (path) => !MINIMEGA_UNSAFE.test(String(path));

// How pages name a disk: its path within the minimega files directory, or its
// full path when it is outside it.
export const diskLabel = (disk) => disk?.relativePath || disk?.fullPath || '';

// A disk in the list, by its full path.
export function findDisk(disks, path) {
  if (!path || !Array.isArray(disks)) return null;
  return disks.find((disk) => disk.fullPath === path) ?? null;
}

// The images backing a VM's disk, nearest first down to the base image, each
// by its label, or its full path when the list does not hold it; empty when
// it has none or is not in the list.
export function backingChain(disks, path) {
  return (findDisk(disks, path)?.backingImages ?? []).map(
    (image) => diskLabel(findDisk(disks, image)) || image,
  );
}

// The minimega files directory as the list shows it: the full path of a disk
// inside it, less that disk's path within it. Empty when the list holds no
// such disk.
export function filesDir(disks) {
  for (const disk of disks ?? []) {
    const within = disk.relativePath;
    if (within && disk.fullPath?.endsWith(`/${within}`)) {
      return disk.fullPath.slice(0, -within.length - 1);
    }
  }
  return '';
}

// The disks as a picker's options, valued by full path, named by label and
// sorted by it: those inside the minimega files directory and those outside
// it. An image minimega cannot take is offered disabled.
export function diskOptions(disks) {
  const options = (disks ?? [])
    .filter((disk) => disk.fullPath)
    .map((disk) => ({ disk, label: diskLabel(disk) }))
    .sort((a, b) => a.label.localeCompare(b.label))
    .map(({ disk, label }) => {
      const usable = minimegaSafe(disk.fullPath);
      return {
        value: disk.fullPath,
        label: usable ? label : `${label} (minimega cannot use this name)`,
        disabled: !usable,
        outside: !!disk.outsideFilesDir,
      };
    });
  return {
    inside: options.filter((option) => !option.outside),
    outside: options.filter((option) => option.outside),
  };
}

// Why a VM's disk needs a warning: 'outside' the minimega files directory,
// or 'unlisted' when the list is known and does not hold it. Null when it
// needs none or the list is not known (null), as for a role that may not list
// disks.
export function diskWarning(disks, path) {
  if (!path || !Array.isArray(disks)) return null;
  const disk = findDisk(disks, path);
  if (!disk) return 'unlisted';
  return disk.outsideFilesDir ? 'outside' : null;
}

// the ISO images in a disk list, for the CD-ROM picker
export const isoDisks = (disks) =>
  (disks ?? []).filter((disk) => disk.kind === 'ISO' && disk.fullPath);
