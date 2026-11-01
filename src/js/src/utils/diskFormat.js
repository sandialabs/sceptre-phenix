// The type of a disk image, such as "qcow2 v3", from the format and version
// that the disk list (GET disks) gives, and the issue the server found with
// that format, if any.
import { diskLabel } from '@/utils/diskChain.js';
import { docsPage } from '@/utils/docs.js';

// the docs section that gives the fix for each image format
export const FORMATS_DOCS = `${docsPage('disks')}#image-format-versions`;

// the formats whose version the type shows
const VERSIONED = ['qcow2', 'qcow'];

// each issue level's short name, the accessible name of its icon
const LEVEL_NAMES = { error: 'Format error', warning: 'Format warning' };

// What the Type column shows for a disk: ISO for a CD-ROM image, tgz for a
// container image, else the format with its version when it has one, such as
// "qcow2 v3" or "raw". It is "unknown" when the server does not know it.
export function diskType(disk) {
  if (disk?.kind === 'ISO') return 'ISO';
  if (disk?.kind === 'Container') return 'tgz';
  const format = String(disk?.format ?? '');
  if (!format) return 'unknown';
  const version = Number(disk?.formatVersion) || 0;
  return VERSIONED.includes(format) && version > 0
    ? `${format} v${version}`
    : format;
}

// Orders disks by the type the Type column shows, versions by number, then by
// label so that the order is the same each time.
export function compareDiskTypes(a, b) {
  return (
    diskType(a).localeCompare(diskType(b), 'en-US', {
      numeric: true,
      sensitivity: 'base',
    }) || diskLabel(a).localeCompare(diskLabel(b))
  );
}

// the level of a disk's format issue, 'error' or 'warning', or null for none
export function formatIssueLevel(disk) {
  const level = disk?.formatIssue?.level;
  return Object.hasOwn(LEVEL_NAMES, level) ? level : null;
}

// the accessible name of a disk's format issue icon, or '' for none
export const formatIssueName = (disk) =>
  LEVEL_NAMES[formatIssueLevel(disk)] ?? '';

// A disk's format issue as one text, the problem then the fix and its
// command, or '' for none.
export function formatIssueText(disk) {
  if (!formatIssueLevel(disk)) return '';
  const { problem = '', fix = '', command = '' } = disk.formatIssue;
  const sentences = [problem, fix].filter(Boolean);
  if (command) return `${sentences.join('. ')}: ${command}`;
  return sentences.length ? `${sentences.join('. ')}.` : '';
}
