// Remembers which rows a page changed from websocket updates, so a list read
// before an update cannot put the old state back. Changes are ordered by the
// server's seq (see freshness.js); one without a seq wins over every list until
// a list newer than the last seq seen (or the next list with one) arrives.
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
          for (const [name, seq] of seqs) {
            if (seq <= listSeq) seqs.delete(name);
            // a change made before any seq was seen orders after this list
            else if (seq === Infinity) seqs.set(name, listSeq + 1);
          }
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
