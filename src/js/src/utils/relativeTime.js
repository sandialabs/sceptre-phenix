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

  const seconds = Math.round((now - then) / 1000);
  const abs = Math.abs(seconds);

  for (const [unit, size] of UNITS) {
    if (abs >= size) {
      return format.format(-Math.trunc(seconds / size), unit);
    }
  }

  return 'just now';
}
