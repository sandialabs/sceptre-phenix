// Display helpers shared by the drafts landing and the History dialog.

const TIMESTAMP = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'short',
});

// History snapshots are often seconds apart; without the seconds, several
// entries would read the same.
const TIMESTAMP_SECONDS = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'medium',
});

/**
 * Formats a server timestamp for people, in their locale. Missing or
 * unparsable values give an empty string, so callers can drop the text.
 *
 * @param {string|number|Date} [value]
 * @param {{ seconds?: boolean }} [options] seconds: include the seconds.
 * @returns {string}
 */
export function formatTimestamp(value, { seconds = false } = {}) {
  if (!value) {
    return '';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '';
  }

  return (seconds ? TIMESTAMP_SECONDS : TIMESTAMP).format(date);
}
