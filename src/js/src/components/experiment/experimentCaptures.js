// Experiment captures: one packet capture of an experiment's traffic on all
// its hosts, which minimega merges into one file on the head node. These
// pure helpers give what the capture dialog, the Captures tab and the
// WebShark page show for the GET /experiments/{exp}/captures answer.

// Open vSwitch applies at most this many mirrors on one bridge
const MIRROR_LIMIT = 32;
// more than this many mirrors of one experiment is more than half the limit
const HALF_BUDGET = 16;
// this many mirrors in total leaves 4 or fewer
const NEAR_BUDGET = 28;

export const DEFAULT_SNAPLEN = 1600;
export const CAPTURE_FORMATS = ['pcapng', 'pcap'];
export const FORMAT_HELP =
  'pcapng records the host and VM port of each frame, the filter and the drop counts.';

// the support status that the /features list gives when the capture list
// cannot be read
const FEATURE = 'minimega-captures';
const NO_SUPPORT_REASON = 'requires minimega 3.4.0 or later';

// the BPF filters the server accepts: no quotes, backslash or #
const FILTER = /^[A-Za-z0-9 .:/[\]()&|!<>=+\-*,]{0,1024}$/;
const FILTER_MAX = 1024;

// the states of a capture that still writes its file
const RUNNING_STATES = new Set(['starting', 'running', 'stalled', 'stopping']);

// Whether minimega supports experiment captures, and why not, as
// { available, reason }: from the capture list, or from the server's
// features when the list did not say.
export function captureSupport(list, features) {
  const support = list?.support;
  if (support && typeof support.available === 'boolean') {
    return {
      available: support.available,
      reason: support.available ? '' : support.reason || NO_SUPPORT_REASON,
    };
  }
  const available = (features ?? []).includes(FEATURE);
  return { available, reason: available ? '' : NO_SUPPORT_REASON };
}

// The short form of a reason for a menu item: the part before ';'.
export const shortReason = (reason) =>
  (reason || NO_SUPPORT_REASON).split(';')[0].trim();

// The notice the Captures tab shows when minimega lacks the support.
export function unsupportedNotice(reason) {
  const text = (reason || NO_SUPPORT_REASON).trim().replace(/\.$/, '');
  return (
    'Experiment captures are not available. ' +
    `${text.charAt(0).toUpperCase()}${text.slice(1)}. ` +
    'Upgrade minimega on every node, relays included, and restart phēnix. ' +
    'VM interface captures still work, without the new options.'
  );
}

export const isRunning = (capture) =>
  !!capture?.running || RUNNING_STATES.has(capture?.state);

// how many of the list's experiment captures still run
export const runningCount = (captures) =>
  (captures ?? []).filter(isRunning).length;

// The Bulma color of a capture's status tag: running and failed are red,
// full yellow, the others gray.
export function statusType(capture) {
  if (isRunning(capture) || capture?.state === 'failed') return 'is-danger';
  if (capture?.state === 'full') return 'is-warning';
  return null;
}

// what a capture covers: its VLANs or all of them, then its filter
export function captureScope(capture) {
  const vlans = capture?.vlans?.length ? capture.vlans.join(', ') : 'All VLANs';
  return capture?.filter ? `${vlans} · ${capture.filter}` : vlans;
}

// the frames a capture or stream dropped: kernel, pcap and queue drops; a
// stream's Lost count has the mesh losses
export function captureDrops(counters) {
  const c = counters ?? {};
  return Number(c.kdrops ?? 0) + Number(c.pdrops ?? 0) + Number(c.qdrops ?? 0);
}

// a count for people: 1,284,113
export const countText = (value) => Number(value ?? 0).toLocaleString('en-US');

// milliseconds for people: one decimal below 10 ms, else whole; `signed`
// puts + before a positive value and a minus sign before a negative one
export function msText(ms, { signed = false } = {}) {
  const value = Number(ms ?? 0);
  const size = Math.abs(value);
  const digits = size < 10 ? size.toFixed(1) : String(Math.round(size));
  if (!signed) return `${value < 0 ? '−' : ''}${digits} ms`;
  if (value === 0) return '0 ms';
  return `${value < 0 ? '−' : '+'}${digits} ms`;
}

// Whether a filter is one the server accepts; the reason, or '' when it is.
export function filterProblem(filter) {
  const text = filter ?? '';
  if (text.length > FILTER_MAX) {
    return `Use ${FILTER_MAX} characters or fewer.`;
  }
  if (FILTER.test(text)) return '';
  return 'Use only letters, digits, spaces and . : / [ ] ( ) & | ! < > = + - * ,';
}

// Whether a snaplen is a whole number from 14 to 65535; the reason, or ''.
export function snaplenProblem(value) {
  const text = String(value ?? '').trim();
  const n = Number(text);
  if (/^\d+$/.test(text) && n >= 14 && n <= 65535) return '';
  return 'Use a whole number from 14 to 65535.';
}

const pad2 = (n) => String(n).padStart(2, '0');

// the longest capture file name, without its extension, that the server keeps
const MAX_STEM_LENGTH = 200;

// A name as the server makes it a capture file name in a format: each
// character but letters, digits, '.', '_' and '-' becomes '_', a .pcap or
// .pcapng extension goes, then leading dots go, and the rest is cut to 200
// characters. The name always ends in a timestamp, so the stem is never empty.
function captureFileName(name, ext) {
  let stem = name.replace(/[^A-Za-z0-9._-]/gu, '_');
  stem = stem.replace(/\.(pcapng|pcap)$/i, '');
  stem = stem.replace(/^\.+/, '').slice(0, MAX_STEM_LENGTH);
  return `${stem}.${ext}`;
}

// The file name the server gives a capture that names none:
// <exp>[-<vlan>]-<YYYYmmdd-HHMMSS>.<format>, the VLAN only when there is
// exactly one, made a file name as the server makes it one. The server adds
// -2, -3 and so on when a file already has the name.
export function defaultCaptureName(exp, vlans, format, time = new Date()) {
  const stamp =
    `${time.getFullYear()}${pad2(time.getMonth() + 1)}${pad2(time.getDate())}` +
    `-${pad2(time.getHours())}${pad2(time.getMinutes())}${pad2(time.getSeconds())}`;
  const parts = [exp, ...(vlans?.length === 1 ? vlans : []), stamp];
  const ext = format === 'pcap' ? 'pcap' : 'pcapng';
  return captureFileName(parts.join('-'), ext);
}

const limitOf = (row) => Number(row.limit) || MIRROR_LIMIT;
const pairOf = (row) => `${row.host} / ${row.bridge}`;

// whether a host and bridge already has every mirror Open vSwitch applies
const isFull = (row) => Number(row.total) >= limitOf(row);

// whether a new capture would need a mirror where none is left
export const budgetFull = (budget) => (budget ?? []).some(isFull);

// The notices the capture dialog shows before a start: each host and bridge
// with one more mirror, as the capture would add one there. A full one is
// is-danger, and the start is refused; one past half the limit for the
// experiment, or near the limit, is is-warning. With some VLANs selected, a
// full one is is-warning: the dialog cannot tell which bridges they use.
export function budgetNotices(budget, exp, { someVlans = false } = {}) {
  const notices = [];
  for (const row of budget ?? []) {
    const limit = limitOf(row);
    const total = Number(row.total);
    const mine = Number(row.experiment);
    const others = Math.max(total - mine, 0);
    if (isFull(row)) {
      const refusal = someVlans
        ? 'The start may be refused when the selected VLANs use that bridge. '
        : '';
      notices.push({
        type: someVlans ? 'is-warning' : 'is-danger',
        text:
          `${pairOf(row)} has ${total} of ${limit} Open vSwitch mirrors. ` +
          refusal +
          'Stop a capture or a tap mirror on that host to free one. ' +
          `${exp} uses ${mine}; ${others} are from other experiments or tools.`,
      });
      continue;
    }
    const next = total + 1;
    const half = mine + 1 > HALF_BUDGET;
    const near = next >= NEAR_BUDGET;
    if (!half && !near) continue;
    const parts = [
      `This capture uses mirror ${next} of ${limit} on ${pairOf(row)}.`,
    ];
    if (half) {
      parts.push(
        `${exp} will use more than half of the Open vSwitch mirror limit there.`,
      );
    }
    if (near) {
      const left = limit - next;
      parts.push(
        left === 0
          ? 'No mirror is left there after it.'
          : `${left} ${left === 1 ? 'is' : 'are'} left there after it.`,
      );
    }
    notices.push({ type: 'is-warning', text: parts.join(' ') });
  }
  return notices;
}

// The banner lines of the Captures tab: each host and bridge at the half,
// near or full budget level, as the server leveled it.
export function budgetBanner(budget, exp) {
  const lines = [];
  for (const row of budget ?? []) {
    const limit = limitOf(row);
    const total = Number(row.total);
    const mine = Number(row.experiment);
    if (row.level === 'half') {
      lines.push({
        type: 'is-warning',
        text: `${exp} uses ${mine} of ${limit} mirrors on ${pairOf(row)}.`,
      });
    } else if (row.level === 'near' || row.level === 'full') {
      const left = Math.max(limit - total, 0);
      lines.push({
        type: row.level === 'full' ? 'is-danger' : 'is-warning',
        text:
          `${pairOf(row)} has ${total} of ${limit} Open vSwitch mirrors; ` +
          `${left === 0 ? 'none' : left} ${left === 1 ? 'is' : 'are'} left. ` +
          `${exp} uses ${mine} of them, other experiments and tools ` +
          `${Math.max(total - mine, 0)}. ` +
          `A capture that needs a ${ordinal(limit + 1)} mirror is refused.`,
      });
    }
  }
  return lines;
}

// 33 → 33rd
function ordinal(n) {
  const teen = n % 100 >= 11 && n % 100 <= 13;
  const suffix = teen ? 'th' : ({ 1: 'st', 2: 'nd', 3: 'rd' }[n % 10] ?? 'th');
  return `${n}${suffix}`;
}

// The display filters of a pcapng capture, one for each host and bridge
// that its streams name: { label, filter }. A host with one bridge is
// labeled "<host> only"; one with several names the bridge too.
export function hostFilters(capture) {
  if (capture?.format !== 'pcapng') return [];
  const streams = (capture.streams ?? []).filter((s) => s.host && s.bridge);
  const bridges = new Map();
  for (const s of streams) {
    bridges.set(s.host, (bridges.get(s.host) ?? 0) + 1);
  }
  const seen = new Set();
  return streams.flatMap((s) => {
    const name = `${s.host}/${s.bridge}`;
    if (seen.has(name) || /["\\]/.test(name)) return [];
    seen.add(name);
    return [
      {
        label:
          bridges.get(s.host) > 1
            ? `${s.host} / ${s.bridge} only`
            : `${s.host} only`,
        filter: `frame.interface_name == "${name}"`,
      },
    ];
  });
}
