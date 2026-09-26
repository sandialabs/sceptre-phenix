// A commit that waits while its value is still being chosen, for a field
// whose every change commits without Apply (the device Icon, see
// BuilderInspector). The arrow keys, Home, End, Page Up, Page Down and
// type-ahead change a closed select at every step on Windows and Linux, and
// each commit is an Undo step, a draft save and an announcement. A screen
// reader reads each option as it is reached, so its user's steps come far
// apart, and no pause tells the last step from the others. So a change that
// follows a key press is held until the caller flushes it, once the choice
// is made: focus leaves the field, Enter, Apply. A change with no key press
// before it, a choice from the list with the pointer, commits at once.

// Keys that step a closed select to another option.
const STEP_KEYS = [
  'ArrowUp',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
  'Home',
  'End',
  'PageUp',
  'PageDown',
];

// Keys that do nothing on their own, or start a character yet to come.
const IDLE_KEYS = [
  'Shift',
  'Control',
  'Alt',
  'AltGraph',
  'Meta',
  'OS',
  'Fn',
  'CapsLock',
  'NumLock',
  'Dead',
  'Process',
  'Unidentified',
];

/**
 * What a key press on a select does to a choice held for it: 'hold' for a
 * key that steps the select to another option (see STEP_KEYS, and a
 * character typed to find one), 'flush' for any other key (Enter, Escape,
 * Tab, a shortcut: whatever it does finds the choice made), and '' for a
 * modifier pressed on its own.
 *
 * @param {KeyboardEvent} event
 * @returns {'hold'|'flush'|''}
 */
export function keyEffect(event) {
  const key = event?.key || '';

  if (!key || IDLE_KEYS.includes(key) || event.isComposing) {
    return '';
  }

  if (event.ctrlKey || event.metaKey) {
    return 'flush';
  }

  return STEP_KEYS.includes(key) || key.length === 1 ? 'hold' : 'flush';
}

/**
 * @template T
 * @param {(value: T) => boolean} commit commits a value, and says whether it
 *   changed anything
 * @returns {{key: () => void, point: () => void, change: (value: T) => boolean,
 *   flush: () => boolean, readonly held: T|null}}
 *   `key` says a key that steps the field went down, and `point` that the
 *   pointer was pressed on it; `change` puts a value in place of the one
 *   held, and commits it at once unless a key went down since the last
 *   commit or pointer press, returning what `commit` returned, or false
 *   while it holds the value; `flush` commits the value held at once, and
 *   returns what `commit` returned, or false with nothing held; `held` is
 *   the value waiting, or null
 */
export function heldCommit(commit) {
  let held = null;
  let keyed = false;

  function flush() {
    const value = held;

    held = null;
    keyed = false;

    return value === null ? false : commit(value);
  }

  return {
    key() {
      keyed = true;
    },
    point() {
      keyed = false;
    },
    change(value) {
      held = value;

      return keyed ? false : flush();
    },
    flush,
    get held() {
      return held;
    },
  };
}
