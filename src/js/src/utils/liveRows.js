// Remembers which rows a page changed from websocket updates, so a list
// requested before an update arrived does not put the old state back: an
// experiment stopped while the list was loading would otherwise show as
// started again until the next reload, a new one would vanish, and a deleted
// one would come back.
export function createLiveRows() {
  const updatedAt = new Map();
  const deletedAt = new Map();

  return {
    // records that the row named `name` was just updated or added
    touch(name) {
      updatedAt.set(name, Date.now());
      deletedAt.delete(name);
    },

    // records that the row named `name` was just removed
    remove(name) {
      deletedAt.set(name, Date.now());
      updatedAt.delete(name);
    },

    // the loaded rows, keeping the current version of each row updated since
    // the load was requested, and rows added or removed since then
    merge(loaded, current, requestedAt) {
      const since = (times, name) => {
        const at = times.get(name);
        return at !== undefined && at >= requestedAt;
      };

      const live = new Map((current ?? []).map((row) => [row.name, row]));
      const rows = loaded
        .filter((row) => !since(deletedAt, row.name))
        .map((row) => {
          const kept = live.get(row.name);
          return kept && since(updatedAt, row.name) ? kept : row;
        });

      const names = new Set(rows.map((row) => row.name));
      for (const row of current ?? []) {
        if (!names.has(row.name) && since(updatedAt, row.name)) rows.push(row);
      }

      return rows;
    },
  };
}
