// Bounded snapshot history for undo/redo.
//
// The builder keeps whole-document snapshots (documents are small and plain
// JSON) which keeps the semantics obvious: undo restores exactly what the user
// saw. The stack is capped at the same 100 entries the server retains per
// draft, so local history and server history stay comparable.
//
// Every entry carries a stable commit id. The autosave queue keys its ordered
// operations on that id, which is what lets a distinct commit map to exactly
// one persisted snapshot without coalescing.

import { newId } from './ids.js';

export const DEFAULT_HISTORY_LIMIT = 100;

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
   * Records a new snapshot, dropping any redo entries.
   *
   * @param {*} snapshot
   * @param {string} [label] short description used for accessible announcements
   * @param {string} [id] explicit commit id (used when replaying a recovered
   *   queue so local ids survive a reload)
   * @returns {{id: string, label: string, snapshot: *}} the recorded entry
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

  /**
   * Discards history and restarts from `snapshot`.
   *
   * @param {*} snapshot
   * @param {string} [label]
   */
  reset(snapshot, label = 'initial') {
    this.entries = [{ id: newId(), label, snapshot }];
    this.index = 0;
  }

  /**
   * Restores a previously recorded list of entries, e.g. after recovering the
   * local operation log from IndexedDB.
   *
   * @param {{id: string, label: string, snapshot: *}[]} entries
   * @param {number} [index] cursor position, defaults to the newest entry
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

  /** @returns {number} retained snapshot count */
  get size() {
    return this.entries.length;
  }
}
