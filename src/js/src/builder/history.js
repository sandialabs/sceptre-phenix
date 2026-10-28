// Bounded snapshot history for undo/redo, of whole documents so undo restores
// exactly what the user saw. The cap is the server's 50 per draft
// (MaxSnapshots in limits.go). Each entry has a stable commit id, which the
// autosave queue keys its operations on.

import { newId } from './ids.js';

export const DEFAULT_HISTORY_LIMIT = 50;

// The name of an edit that the Builder applied for the user: the Inspector's
// unapplied edits, saved before the user leaves the diagram or before the
// diagram is read whole (see leave.js). The History dialog marks its
// snapshots.
export const SAVED_UNAPPLIED = 'Saved unapplied changes';

/**
 * Whether a snapshot's summary names an edit the Builder applied for the user
 * (SAVED_UNAPPLIED).
 */
export function savedAutomatically(summary) {
  return String(summary || '').startsWith(`${SAVED_UNAPPLIED} `);
}

export class History {
  /**
   * @param {*} initial first snapshot
   * @param {number} [limit] maximum number of retained snapshots
   */
  constructor(initial, limit = DEFAULT_HISTORY_LIMIT) {
    this.limit = Math.max(1, limit);
    this.entries = [{ id: newId(), label: 'initial', snapshot: initial }];
    this.index = 0;
  }

  /**
   * @returns {*} current snapshot
   */
  current() {
    return this.entries[this.index].snapshot;
  }

  /**
   * @returns {{id: string, label: string, snapshot: *}} current entry
   */
  currentEntry() {
    return this.entries[this.index];
  }

  /**
   * Records a new snapshot, dropping any redo entries. Pass `id` when
   * replaying a recovered queue, so local ids survive a reload.
   */
  push(snapshot, label = 'change', id = newId()) {
    const entry = { id, label, snapshot };

    this.entries = this.entries.slice(0, this.index + 1);
    this.entries.push(entry);

    if (this.entries.length > this.limit) {
      this.entries = this.entries.slice(this.entries.length - this.limit);
    }

    this.index = this.entries.length - 1;

    return entry;
  }

  /** @returns {boolean} */
  canUndo() {
    return this.index > 0;
  }

  /** @returns {boolean} */
  canRedo() {
    return this.index < this.entries.length - 1;
  }

  /**
   * @returns {*} snapshot after undo (unchanged when at the oldest entry)
   */
  undo() {
    if (this.canUndo()) {
      this.index -= 1;
    }

    return this.current();
  }

  /**
   * @returns {*} snapshot after redo (unchanged when at the newest entry)
   */
  redo() {
    if (this.canRedo()) {
      this.index += 1;
    }

    return this.current();
  }

  /**
   * Label of the operation that undo would reverse.
   *
   * @returns {string}
   */
  undoLabel() {
    return this.canUndo() ? this.entries[this.index].label : '';
  }

  /**
   * Label of the operation that redo would reapply.
   *
   * @returns {string}
   */
  redoLabel() {
    return this.canRedo() ? this.entries[this.index + 1].label : '';
  }

  /** Discards history and restarts from `snapshot`. */
  reset(snapshot, label = 'initial') {
    this.entries = [{ id: newId(), label, snapshot }];
    this.index = 0;
  }

  /**
   * Restores entries recorded earlier, such as after recovery from IndexedDB.
   */
  restore(entries, index = entries.length - 1) {
    if (!entries || entries.length === 0) {
      return;
    }

    const dropped = Math.max(0, entries.length - this.limit);

    this.entries = entries.slice(dropped);
    this.index = Math.min(
      Math.max(0, index - dropped),
      this.entries.length - 1,
    );
  }

  /**
   * Drops the `count` oldest entries, such as those the server no longer
   * keeps. The current entry is always kept.
   */
  trimOldest(count) {
    const dropped = Math.min(Math.max(0, count), this.index);

    if (dropped > 0) {
      this.entries = this.entries.slice(dropped);
      this.index -= dropped;
    }

    return dropped;
  }

  /**
   * Drops the entries `test` picks, such as those of a deleted snapshot, so
   * undo and redo skip them. The current entry is always kept.
   */
  removeWhere(test) {
    const current = this.entries[this.index];
    const kept = this.entries.filter(
      (entry) => entry === current || !test(entry),
    );
    const dropped = this.entries.length - kept.length;

    if (dropped > 0) {
      this.entries = kept;
      this.index = kept.indexOf(current);
    }

    return dropped;
  }

  /** @returns {number} retained snapshot count */
  get size() {
    return this.entries.length;
  }
}
