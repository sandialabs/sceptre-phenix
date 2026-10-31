import { parseBytes } from '@/utils/bytes.js';

// the Disks page's counter: disks, those in use (as the In Use column), and the
// sum of the sizes on disk it can read, counting unreadable ones
export function diskTotals(disks) {
  const totals = {
    disks: 0,
    inUse: 0,
    sizeBytes: 0,
    unknownDiskSizes: 0,
  };
  for (const disk of disks ?? []) {
    totals.disks += 1;
    if (disk.inUse) totals.inUse += 1;
    const onDisk = parseBytes(disk.size);
    if (onDisk === null) totals.unknownDiskSizes += 1;
    else totals.sizeBytes += onDisk;
  }
  return totals;
}
