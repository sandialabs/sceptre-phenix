import { compare } from '@/utils/compare.js';
import { rfc3339Ms } from '@/utils/relativeTime.js';

// A SCORCH run archives what it collected each time it finishes, in the
// experiment's files directory, as scorch-run-<run>_<start>.tgz.
const ARCHIVE = /^scorch-run-(\d+)_([^/]+)\.tgz$/;

// the start stamp, as 2006-01-02T15-04-05Z0700 writes it
const STAMP =
  /^(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})(?:(Z)|([+-]\d{2})(\d{2}))$/;

// the RFC 3339 time an archive's start stamp names, or null
function stampTime(stamp) {
  const m = STAMP.exec(stamp);
  if (!m) return null;
  const [, date, h, min, s, utc, zoneH, zoneM] = m;
  return `${date}T${h}:${min}:${s}${utc ? 'Z' : `${zoneH}:${zoneM}`}`;
}

// Run `run`'s archives among the listed files, newest first, each with
// `started`: the RFC 3339 time from its name, or else the file's date.
export function runArchives(files, run) {
  const id = String(run);
  const started = (file) => rfc3339Ms(file.started) ?? -Infinity;
  return (files ?? [])
    .flatMap((file) => {
      const m = ARCHIVE.exec(file.path ?? '');
      if (!m || m[1] !== id || file.isDir) return [];
      return [{ ...file, started: stampTime(m[2]) ?? file.date ?? '' }];
    })
    .toSorted((a, b) => started(b) - started(a) || compare(b.path, a.path));
}
