import { parseBytes } from '@/utils/bytes.js';

// the Disks page's counter: disks listed, those in use (as the In Use column),
// and the sum of the virtual sizes it can read, counting those it cannot
export function diskTotals(disks) {
  const totals = { disks: 0, inUse: 0, virtualBytes: 0, unknownSizes: 0 };
  for (const disk of disks ?? []) {
    totals.disks += 1;
    if (disk.inUse) totals.inUse += 1;
    const bytes = parseBytes(disk.virtualSize);
    if (bytes === null) totals.unknownSizes += 1;
    else totals.virtualBytes += bytes;
  }
  return totals;
}
