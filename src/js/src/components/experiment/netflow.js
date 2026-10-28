import { markRaw } from 'vue';

// The lines of the running experiment page's netflow tab: captured flows and
// the markers the page adds as the capture starts and stops.

// flows kept for the netflow tab, and how many of them it shows at once
export const NETFLOW_MAX_LINES = 10000;
export const NETFLOW_SHOWN_LINES = 500;
const IP_PROTOCOLS = { 1: 'ICMP', 6: 'TCP', 17: 'UDP', 58: 'ICMPv6' };

let netflowLineID = 0;
const clockTime = () => new Date().toLocaleTimeString();

// a captured flow as the netflow tab shows it; markRaw: flows never change
export function netflowLine(flow) {
  return markRaw({
    id: ++netflowLineID,
    time: clockTime(),
    src: `${flow.src}:${flow.sport}`,
    dst: `${flow.dst}:${flow.dport}`,
    proto: IP_PROTOCOLS[flow.proto] ?? String(flow.proto),
    packets: flow.packets,
    bytes: flow.bytes,
  });
}

export function netflowMarker(text) {
  return markRaw({ id: ++netflowLineID, time: clockTime(), marker: text });
}

// adds lines to `all`, keeping the most recent NETFLOW_MAX_LINES
export function appendNetflowLines(all, lines) {
  all.push(...lines);
  if (all.length > NETFLOW_MAX_LINES) {
    all.splice(0, all.length - NETFLOW_MAX_LINES);
  }
}

// every line matching the search text, oldest first
export function matchNetflow(lines, search) {
  const text = (search ?? '').trim().toLowerCase();
  if (!text) return lines;
  return lines.filter((line) =>
    [line.marker, line.src, line.dst, line.proto, line.time]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
      .includes(text),
  );
}

// what the tab says it shows of `matches` lines out of `total`
export function netflowSummary(matches, total) {
  const shown = Math.min(matches, NETFLOW_SHOWN_LINES);
  const of =
    matches === total ? `${matches}` : `${matches} matching (of ${total})`;
  return shown < matches
    ? `Newest ${shown} of ${of} lines, newest first`
    : `${of} lines, newest first`;
}
