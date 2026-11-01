// The Logs page's Level column: each level as a short uppercase badge.

// level => [badge text, CSS class]; plog writes these, and minimega's FATAL
// arrives as ERROR
const LEVEL_BADGES = {
  DEBUG: ['DBG', 'level-debug'],
  INFO: ['INFO', 'level-info'],
  WARN: ['WRN', 'level-warn'],
  WARNING: ['WRN', 'level-warn'],
  ERROR: ['ERR', 'level-error'],
};

// The badge of a log entry's level: its short text and the class that colors
// it. An unknown level shows its name uppercased, in the plain tag color.
export function levelBadge(level) {
  const name = String(level ?? '')
    .trim()
    .toUpperCase();
  const known = Object.hasOwn(LEVEL_BADGES, name) ? LEVEL_BADGES[name] : null;
  if (known) return { label: known[0], cls: known[1] };
  return { label: name || 'UNKNOWN', cls: 'level-other' };
}
