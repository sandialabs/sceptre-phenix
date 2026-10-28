// The Experiments table's sorts, start time text and page totals.

// an RFC 3339 time, as the server records an experiment's start
const RFC3339 =
  /^\d{4}-\d{2}-\d{2}[Tt ]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;

// The start time in milliseconds, or null for an empty or malformed one;
// Date.parse alone would read text such as "1" as a date.
export function startTimeMs(raw) {
  if (typeof raw !== 'string' || !RFC3339.test(raw)) return null;
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? null : ms;
}

const formatters = new Map();

// one formatter per time zone (undefined is the browser's)
function formatter(timeZone) {
  if (!formatters.has(timeZone)) {
    formatters.set(
      timeZone,
      new Intl.DateTimeFormat('en-US', {
        timeZone,
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hourCycle: 'h23',
        timeZoneName: 'short',
      }),
    );
  }
  return formatters.get(timeZone);
}

// "14:03:22 28-10-2026 (MDT)": the start time in 24-hour time in `timeZone`
// (the browser's by default), or null when it is empty or malformed.
export function startTimeText(raw, timeZone) {
  const ms = startTimeMs(raw);
  if (ms === null) return null;
  const parts = Object.fromEntries(
    formatter(timeZone)
      .formatToParts(new Date(ms))
      .map(({ type, value }) => [type, value]),
  );
  const year = parts.year.padStart(4, '0');
  return (
    `${parts.hour}:${parts.minute}:${parts.second} ` +
    `${parts.day}-${parts.month}-${year} (${parts.timeZoneName})`
  );
}

const byName = (a, b) => (a.name ?? '').localeCompare(b.name ?? '');
const compare = (x, y) => (x < y ? -1 : x > y ? 1 : 0);

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
  (exp) => startTimeMs(exp.start_time) ?? -Infinity,
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
