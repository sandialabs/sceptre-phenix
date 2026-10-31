// The snapshots that the next change of the open draft removes. After an undo
// or a restore, the next save removes every snapshot after the cursor, also
// other users' (AppendSnapshot in api/builder/service.go).

import { count } from './announce.js';

// The newer snapshots that the next change removes, or null when it removes
// none or the editor does not show the cursor's snapshot. `newest` is null
// when `history` is not the server's list now.
export function newerSnapshots(head, history, shown) {
  if (!head || !shown || shown !== head.snapshotId) {
    return null;
  }

  const newer = head.snapshots - 1 - head.cursor;

  if (!(newer > 0)) {
    return null;
  }

  const listed =
    Array.isArray(history) &&
    history.length === head.snapshots &&
    history[head.cursor]?.id === head.snapshotId;

  return { count: newer, newest: listed ? history.at(-1) : null };
}

// The server's snapshot list after one of its answers: a saved snapshot
// replaces those after the current one, the oldest go to match the server's
// length, and the current one is marked. It returns `history` when unchanged.
export function historyAfter(history, head, saved = null) {
  const list = Array.isArray(history) ? history : [];

  if (!head) {
    return history;
  }

  let next = list;

  if (
    saved?.id === head.snapshotId &&
    !list.some(({ id }) => id === saved.id)
  ) {
    const current = list.findIndex((entry) => entry.current);

    next = [...(current >= 0 ? list.slice(0, current + 1) : list), saved];
  }

  if (next.length > head.snapshots) {
    next = next.slice(next.length - head.snapshots);
  }

  if (
    next.some(
      (entry) => Boolean(entry.current) !== (entry.id === head.snapshotId),
    )
  ) {
    next = next.map((entry) => ({
      ...entry,
      current: entry.id === head.snapshotId,
    }));
  }

  return next === list ? history : next;
}

/** What the editor says while the next change removes newer snapshots. */
export function newerNotice(newer, { me = '', time = String } = {}) {
  const { newest } = newer;
  const user = newest?.createdBy || '';
  const when = newest?.createdAt ? time(newest.createdAt) : '';
  const latest = [user && `by ${user === me ? 'you' : user}`, when]
    .filter(Boolean)
    .join(', ');
  const text = `Your next change removes ${count(newer.count, 'newer snapshot')}${
    latest ? ` (latest ${latest})` : ''
  }.`;

  return newer.saved
    ? `${text} The latest is saved as draft ${newer.saved}.`
    : text;
}

// What a History Restore says of the snapshots after it, without a period, or
// '' for the newest snapshot. A change after the restore removes them.
export function restoreNote(index, total) {
  const newer = total - 1 - index;

  return newer > 0
    ? `Your next change after this restore removes ${count(newer, 'newer snapshot')}`
    : '';
}
