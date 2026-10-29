const pad2 = (n) => String(n).padStart(2, '0');

// zero-padded parts of the current local time, for generated filenames
export function timestampParts() {
  const now = new Date();
  return {
    year: now.getFullYear(),
    month: pad2(now.getMonth() + 1),
    day: pad2(now.getDate()),
    hours: pad2(now.getHours()),
    minutes: pad2(now.getMinutes()),
    seconds: pad2(now.getSeconds()),
  };
}

// the local time to the minute as YYYY-MM-DD_HHMM, for generated filenames
export function minuteStamp() {
  const t = timestampParts();
  return `${t.year}-${t.month}-${t.day}_${t.hours}${t.minutes}`;
}

// the local time to the second and a random suffix as
// YYYY-MM-DD_HHMMSS_xxxx, for files that must not replace an earlier one
export function uniqueStamp() {
  const t = timestampParts();
  const [n] = crypto.getRandomValues(new Uint16Array(1));
  const suffix = n.toString(16).padStart(4, '0');
  return `${t.year}-${t.month}-${t.day}_${t.hours}${t.minutes}${t.seconds}_${suffix}`;
}
