// What the Disks page's details card shows of a disk image, from the disk
// list (GET disks) and the details the server reads from the image when the
// card opens (GET disks/details, DiskDetails in openapi.yml).
import { parseBytes } from '@/utils/bytes.js';
import { OUTSIDE_GROUP, chainProblem } from '@/utils/diskChain.js';
import { formatIssueLevel, formatIssueName } from '@/utils/diskFormat.js';
import { listText, pluralWord } from '@/utils/plural.js';
import { shellWord } from '@/utils/shell.js';

// each health level's tag, the most serious first
export const HEALTH = {
  danger: { label: 'Error', icon: 'times-circle', type: 'is-danger' },
  warning: {
    label: 'Warning',
    icon: 'exclamation-triangle',
    type: 'is-warning',
  },
  ok: { label: 'OK', icon: 'check-circle', type: 'is-success' },
};

// the partition table schemes, as the card names them
const TABLE_NAMES = {
  mbr: 'MBR',
  gpt: 'GPT',
  none: 'None',
  unknown: 'Unknown',
};

// the name of a partition table scheme, or '' when phēnix did not read it
export const partitionTableName = (scheme) => TABLE_NAMES[scheme] ?? '';

// the file systems, by their blkid names, whose names differ from them
const FILESYSTEM_NAMES = {
  vfat: 'FAT',
  swap: 'Linux swap',
  LVM2_member: 'LVM physical volume',
  crypto_LUKS: 'LUKS',
  iso9660: 'ISO 9660',
  unknown: 'Unknown',
};

// file systems that phēnix cannot inject files into: no files, encrypted, or
// read-only
const NOT_INJECTABLE = [
  'swap',
  'LVM2_member',
  'crypto_LUKS',
  'iso9660',
  'squashfs',
  'unknown',
];

// a file system's name with its version, such as "FAT32" or "LUKS 2"
export function filesystemName(fs) {
  if (!fs?.type) return '';
  if (fs.type === 'vfat' && fs.version) return fs.version;
  const name = FILESYSTEM_NAMES[fs.type] ?? fs.type;
  return fs.version ? `${name} ${fs.version}` : name;
}

// the bytes of a size the list gives, or null when it gives none
const listBytes = (text) => {
  const bytes = parseBytes(text);
  return bytes > 0 ? bytes : null;
};

// How much of its virtual disk an image takes: the space the file takes on
// disk as a percent of the virtual size. `bar` is that percent, at most 100.
// Null when either size is unknown.
export function allocation(disk, details) {
  const used =
    details?.allocatedBytes || details?.fileBytes || listBytes(disk?.size);
  const total = details?.virtualBytes || listBytes(disk?.virtualSize);
  if (!(used > 0) || !(total > 0)) return null;
  const percent = (used / total) * 100;
  return {
    percent,
    bar: Math.min(100, percent),
    text: percent < 1 ? '<1%' : `${Math.round(percent)}%`,
  };
}

// a VM as the card names it: "kali (bennu)"
const vmName = (use) => `${use.vm} (${use.experiment})`;

// The VMs that inject files into each partition of the disk, by partition
// number: only the first drive of a VM gets files, into its inject partition.
export function injectTargets(details) {
  const targets = new Map();
  for (const use of details?.vms ?? []) {
    if (use.drive !== 0 || !(use.injectPartition > 0)) continue;
    const vms = targets.get(use.injectPartition) ?? [];
    targets.set(use.injectPartition, [...vms, vmName(use)]);
  }
  return targets;
}

// why phēnix cannot inject files into a partition, or '' when it can
function injectProblem(partition) {
  if (!partition) return 'which is not on this disk';
  if (partition.extended) {
    return 'an extended partition, which holds no file system';
  }
  const type = partition.filesystem?.type;
  if (!type || type === 'unknown') {
    return 'where phēnix found no file system that it knows';
  }
  if (NOT_INJECTABLE.includes(type)) {
    return `which holds ${filesystemName(partition.filesystem)}, not a file system that phēnix can write files to`;
  }
  return '';
}

// The inject partitions of the VMs using the disk that it does not have, or
// that hold no file system phēnix can write to: { number, text } each, by
// number. Empty when phēnix did not read an MBR or GPT table.
export function injectProblems(details) {
  if (!['mbr', 'gpt'].includes(details?.partitionTable)) return [];
  const partitions = details.partitions ?? [];
  const problems = [];
  for (const [number, vms] of injectTargets(details)) {
    const why = injectProblem(partitions.find((p) => p.number === number));
    if (!why) continue;
    const verb = vms.length === 1 ? 'injects' : 'inject';
    problems.push({
      number,
      text: `${pluralWord(vms.length, 'VM')} ${listText(vms)} ${verb} files into partition ${number}, ${why}.`,
    });
  }
  return problems.sort((a, b) => a.number - b.number);
}

// The problems of a disk, the most serious first: { level, icon, text }
// each, from the list and, once loaded, its details.
export function healthProblems(disk, details) {
  const problems = [];
  if (disk?.brokenChain) {
    problems.push({
      level: 'danger',
      text: `The backing chain is broken: ${chainProblem(disk)}.`,
    });
  }
  const formatLevel = formatIssueLevel(disk);
  if (formatLevel) {
    problems.push({
      level: formatLevel === 'error' ? 'danger' : 'warning',
      text: `${formatIssueName(disk)}: ${disk.formatIssue.problem}.`,
    });
  }
  if (disk?.outsideFilesDir) {
    problems.push({ level: 'warning', text: `${OUTSIDE_GROUP}.` });
  }
  for (const { text } of injectProblems(details)) {
    problems.push({ level: 'warning', text });
  }
  return problems
    .map((p) => ({ ...p, icon: HEALTH[p.level].icon }))
    .sort((a, b) => (a.level === b.level ? 0 : a.level === 'danger' ? -1 : 1));
}

// the health level of a disk: its most serious problem's, or 'ok'
export const diskHealth = (disk, details) =>
  healthProblems(disk, details)[0]?.level ?? 'ok';

// The features of a qcow header that need a note, as tags: { label, type,
// icon }, the most serious first.
export function qcowFeatures(qcow) {
  if (!qcow) return [];
  const danger = { type: 'is-danger', icon: 'times-circle' };
  const warning = { type: 'is-warning', icon: 'exclamation-triangle' };
  const info = { type: 'is-dark', icon: 'info-circle' };
  const encryption = { aes: 'AES encryption', luks: 'LUKS encryption' };
  return [
    qcow.corrupt && { label: 'Corrupt', ...danger },
    encryption[qcow.encryption] && {
      label: encryption[qcow.encryption],
      ...danger,
    },
    qcow.dirty && { label: 'Not closed cleanly', ...warning },
    qcow.externalDataFile && { label: 'External data file', ...warning },
    qcow.compression === 'zstd' && { label: 'zstd compression', ...info },
    qcow.extendedL2 && { label: 'Extended L2 entries', ...info },
    qcow.lazyRefcounts && { label: 'Lazy refcounts', ...info },
  ].filter(Boolean);
}

// The experiments that use a disk, by name: { name, running, vms } each,
// where vms are the VMs whose drives name the disk. An experiment that only
// the list names uses the disk through an image built on it.
export function usedBy(disk, details) {
  const byName = new Map();
  const entry = (name, running) => {
    if (!byName.has(name)) byName.set(name, { name, running, vms: [] });
    return byName.get(name);
  };
  for (const exp of disk?.experiments ?? []) entry(exp.name, !!exp.running);
  for (const use of details?.vms ?? []) {
    entry(use.experiment, !!use.running).vms.push(use);
  }
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name));
}

// the command that shows what qemu-img reads from an image and its backing
// chain; -U reads an image that a running VM holds
export const qemuImgInfoCommand = (path) =>
  `qemu-img info --backing-chain -U ${shellWord(path)}`;
