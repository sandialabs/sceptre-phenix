// Splits a Logs page message into its text and its key=value fields.
//
// The server sends each log entry's message as one string: the text, then
// each field as ` key=value` (Go's %v, so a value can hold spaces), and an
// optional traceback after a newline (plog's LogEntry and uiHandler).

// a field's key and its `=`: a letter or `_`, then letters, digits, `_`,
// `.` and `-`, so `user.name=` and `event.type=` match, but not `a==b`
const KEY = /[A-Za-z_][\w.-]*=(?!=)/y;
const CLOSE = { '[': ']', '{': '}', '(': ')' };

// the length of the `key=` that starts at i after a space or at the start,
// or 0 when no field starts there
function keyAt(msg, i) {
  if (i > 0 && msg[i - 1] !== ' ') return 0;
  KEY.lastIndex = i;
  return KEY.test(msg) ? KEY.lastIndex - i : 0;
}

// Where the value that starts at `start` ends: before a newline or before
// the space ahead of the next field. With `nested`, a quoted value and the
// inside of brackets do not end it. Returns -1 when a bracket or quote is
// still open at the end, or a quote closes inside a word.
function valueEnd(msg, start, nested) {
  const open = [];
  let i = start;
  if (nested && msg[i] === '"') {
    for (i += 1; i < msg.length && msg[i] !== '"'; i++) {
      if (msg[i] === '\\') i++;
    }
    i++;
    if (i > msg.length || !/^[ \n]?$/.test(msg[i] ?? '')) return -1;
  }
  for (; i < msg.length; i++) {
    const c = msg[i];
    if (nested && CLOSE[c]) open.push(CLOSE[c]);
    else if (nested && c === open.at(-1)) open.pop();
    else if (open.length > 0) continue;
    else if (c === '\n') return i;
    else if (c === ' ' && keyAt(msg, i + 1) > 0) return i;
  }
  return open.length > 0 ? -1 : msg.length;
}

/**
 * Splits a log message into the parts the details dialog shows.
 *
 * `text` is the message before its first field, without trailing space.
 * `fields` are the `{ key, value }` pairs in their order; a value keeps its
 * brackets and quotes, as in `user.roles=[Global Admin]` or `msg="a b"`.
 * `trailing` is what follows the newline after the last field (a traceback),
 * or null. A message with no fields is all `text`.
 *
 * @param {string} msg
 * @returns {{ text: string, fields: { key: string, value: string }[], trailing: string | null }}
 */
export function parseLogMessage(msg) {
  const s = String(msg ?? '');
  let first = -1;
  for (let i = 0; i < s.length && first < 0; i++) {
    if (keyAt(s, i) > 0) first = i;
  }
  if (first < 0) return { text: s.trimEnd(), fields: [], trailing: null };

  const fields = [];
  let trailing = null;
  let i = first;
  while (i < s.length) {
    const keyLength = keyAt(s, i);
    const start = i + keyLength;
    let end = valueEnd(s, start, true);
    if (end < 0) end = valueEnd(s, start, false);
    fields.push({ key: s.slice(i, start - 1), value: s.slice(start, end) });
    if (s[end] === '\n') {
      trailing = s.slice(end + 1);
      break;
    }
    i = end + 1;
  }
  return { text: s.slice(0, first).trimEnd(), fields, trailing };
}
