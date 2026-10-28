// A commit that waits while a select's value is being chosen. Keys change a
// closed select at each step on Windows and Linux, and each commit is an undo
// step, a save and an announcement.
// So a change after a key press waits until the caller flushes it. A pointer
// choice commits at once.

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
 * What a key press does to a held choice: 'hold' for a key that steps the
 * select, 'flush' for any other key, '' for a modifier alone.
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
 * Holds a stepped value until flush. `change` commits at once unless a key went
 * down since the last commit or pointer press.
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
