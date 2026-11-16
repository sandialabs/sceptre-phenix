// The Experiments table's sorts and page totals.

import { compare } from './compare.js';
import { rfc3339Ms } from './relativeTime.js';

const byName = (a, b) => (a.name ?? '').localeCompare(b.name ?? '');

// A Buefy custom-sort on `key(row)` in the direction asked, ties always by
// name from A to Z.
function sortOn(key) {
  return (a, b, isAsc) => {
    const diff = compare(key(a), key(b));
    if (diff) return isAsc ? diff : -diff;
    return byName(a, b);
  };
}

const vlanCount = (exp) => exp.vlan_count ?? 0;

// by how many VLANs the experiments have
export const sortByVlans = sortOn(vlanCount);

// by when the experiments started, those never started or unreadable first
export const sortByStartTime = sortOn(
  (exp) => rfc3339Ms(exp.start_time) ?? -Infinity,
);

// The page's totals: experiments, those running, their VMs, and the VLANs of
// all of them, as the VLANs column counts them.
export function experimentTotals(experiments) {
  const totals = { experiments: 0, running: 0, runningVms: 0, vlans: 0 };
  for (const exp of experiments) {
    totals.experiments += 1;
    totals.vlans += vlanCount(exp);
    if (exp.running) {
      totals.running += 1;
      totals.runningVms += exp.vm_count ?? 0;
    }
  }
  return totals;
}
