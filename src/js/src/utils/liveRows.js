// Remembers which rows a page changed from websocket updates, so a list the
// server read before an update does not put the old state back: an
// experiment stopped while the list was loading would otherwise show as
// started again until the next reload, a new one would vanish, and a deleted
// one would come back.
//
// Changes are ordered by the server's sequence (see freshness.js): a message
// with seq S changed the row after any list tagged with a lower seq was read,
// and before any list tagged S or higher. A change without a seq (made by the
// page itself, or from an older server) wins over every list until a list
// newer than the last seq the page has seen.
export function createLiveRows() {
  const updatedSeq = new Map();
  const deletedSeq = new Map();
  // the highest seq seen, so a page's own change orders after it
  let latest = -Infinity;

  const seqOf = (seq) => {
    if (seq === undefined || seq === null) {
      return Number.isFinite(latest) ? latest + 1 : Infinity;
    }
    latest = Math.max(latest, seq);
    return seq;
  };

  return {
    // records that the row named `name` was updated or added by a change
    // with this seq
    touch(name, seq) {
      updatedSeq.set(name, seqOf(seq));
      deletedSeq.delete(name);
    },

    // records that the row named `name` was removed by a change with this seq
    remove(name, seq) {
      deletedSeq.set(name, seqOf(seq));
      updatedSeq.delete(name);
    },

    // The loaded rows, keeping the current version of each row changed after
    // the list was read (loadedSeq), and rows added or removed since then.
    // Changes the list already reflects are forgotten. A list without a seq
    // loses to every recorded change.
    merge(loaded, current, loadedSeq) {
      const listSeq = loadedSeq ?? -Infinity;
      const after = (seqs, name) => {
        const seq = seqs.get(name);
        return seq !== undefined && seq > listSeq;
      };

      const live = new Map((current ?? []).map((row) => [row.name, row]));
      const rows = loaded
        .filter((row) => !after(deletedSeq, row.name))
        .map((row) => {
          const kept = live.get(row.name);
          return kept && after(updatedSeq, row.name) ? kept : row;
        });

      const names = new Set(rows.map((row) => row.name));
      for (const row of current ?? []) {
        if (!names.has(row.name) && after(updatedSeq, row.name)) rows.push(row);
      }

      if (Number.isFinite(listSeq)) {
        latest = Math.max(latest, listSeq);
        for (const seqs of [updatedSeq, deletedSeq]) {
          for (const [name, seq] of seqs) if (seq <= listSeq) seqs.delete(name);
        }
      }

      return rows;
    },

    // forgets every change, as after a websocket reconnect: a server that
    // restarted numbers its changes afresh
    reset() {
      updatedSeq.clear();
      deletedSeq.clear();
      latest = -Infinity;
    },
  };
}
