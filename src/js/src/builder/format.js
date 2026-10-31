// Timestamp display helpers, shared by the drafts landing, the History
// dialog, the Inspector and the editor.

const TIMESTAMP = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'short',
});

// History snapshots are often seconds apart. Without the seconds, several
// entries would show the same text.
const TIMESTAMP_SECONDS = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'medium',
});

/**
 * Formats a server timestamp in the user's locale, or '' for a missing or
 * unparsable value.
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
