import { readonly, ref } from 'vue';

// "12 hours ago", "2 days ago": how long before now a time was, in its
// largest whole unit. Times are parsed with their timezone (the API sends
// UTC), so the result is right wherever the browser is.
const UNITS = [
  ['year', 365 * 24 * 60 * 60],
  ['month', 30 * 24 * 60 * 60],
  ['week', 7 * 24 * 60 * 60],
  ['day', 24 * 60 * 60],
  ['hour', 60 * 60],
  ['minute', 60],
];

const format = new Intl.RelativeTimeFormat('en', { numeric: 'always' });

export function relativeTime(time, now = Date.now()) {
  const then = new Date(time).getTime();
  if (Number.isNaN(then)) return '';

  const sign = now < then ? 1 : -1;
  const abs = Math.round(Math.abs(now - then) / 1000);

  for (const [unit, size] of UNITS) {
    if (abs >= size) {
      return format.format(sign * Math.trunc(abs / size), unit);
    }
  }

  return 'just now';
}

// an RFC 3339 time, as the server records start and update times
const RFC3339 =
  /^\d{4}-\d{2}-\d{2}[Tt ]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;

// The time in milliseconds, or null for an empty or malformed one; Date.parse
// alone would read text such as "1" as a date.
export function rfc3339Ms(raw) {
  if (typeof raw !== 'string' || !RFC3339.test(raw)) return null;
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? null : ms;
}

// An RFC 3339 time as the server wrote it, to the second, split after the
// date so a narrow column can wrap it there: ["2026-10-28T", "14:03:22Z"].
export function isoTimeParts(raw) {
  const text = raw.replace(/^(.{19})\.\d+/, '$1');
  return [text.slice(0, 11), text.slice(11)];
}

const clockNow = ref(Date.now());
let watchers = 0;
let clock;

// the current time in milliseconds, kept current while anything watches it
export const now = readonly(clockNow);

// Updates `now` every 30 seconds until every watcher has called the function
// this returns.
export function watchNow() {
  if (watchers++ === 0) {
    clockNow.value = Date.now();
    clock = setInterval(() => (clockNow.value = Date.now()), 30000);
  }
  let watching = true;
  return () => {
    if (!watching) return;
    watching = false;
    if (--watchers === 0) clearInterval(clock);
  };
}
