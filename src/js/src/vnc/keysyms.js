// The keys that the console toolbar sends (D5). The noVNC package exports
// only its RFB class, so this is a small table of the X11 keysyms
// (X11/keysymdef.h) and the KeyboardEvent codes that noVNC maps to QEMU
// scancodes.

const key = (keysym, code) => Object.freeze({ keysym, code });

const functionKeys = Object.fromEntries(
  Array.from({ length: 12 }, (_, i) => [
    `f${i + 1}`,
    key(0xffbe + i, `F${i + 1}`),
  ]),
);

/** Each key that the toolbar sends: its keysym and its code. */
export const KEYS = Object.freeze({
  escape: key(0xff1b, 'Escape'),
  tab: key(0xff09, 'Tab'),
  delete: key(0xffff, 'Delete'),
  printScreen: key(0xff61, 'PrintScreen'),
  controlLeft: key(0xffe3, 'ControlLeft'),
  controlRight: key(0xffe4, 'ControlRight'),
  altLeft: key(0xffe9, 'AltLeft'),
  altRight: key(0xffea, 'AltRight'),
  shiftLeft: key(0xffe1, 'ShiftLeft'),
  shiftRight: key(0xffe2, 'ShiftRight'),
  superLeft: key(0xffeb, 'MetaLeft'),
  superRight: key(0xffec, 'MetaRight'),
  ...functionKeys,
});

/** The keys that the Hold buttons keep pressed. */
export const HOLD_KEYS = Object.freeze({
  ctrl: KEYS.controlLeft,
  alt: KEYS.altLeft,
  win: KEYS.superLeft,
});

/**
 * The keys that "Release held keys" lets go of: each modifier on both sides,
 * so that a key that the guest thinks is down comes up (minimega#1194).
 */
export const RELEASE_KEYS = Object.freeze([
  KEYS.controlLeft,
  KEYS.controlRight,
  KEYS.altLeft,
  KEYS.altRight,
  KEYS.shiftLeft,
  KEYS.shiftRight,
  KEYS.superLeft,
  KEYS.superRight,
]);

/** The keys of the "Send keys" section of the Keys menu, in order. */
export const SEND_KEYS = Object.freeze([
  { name: 'ctrlAltDel', label: 'Ctrl+Alt+Del' },
  { name: 'escape', label: 'Esc' },
  { name: 'tab', label: 'Tab' },
  { name: 'printScreen', label: 'PrintScreen' },
]);

/** The Ctrl+Alt+F1 to F7 keys of the Keys menu. */
export const CTRL_ALT_F = Object.freeze(
  [1, 2, 3, 4, 5, 6, 7].map((n) => ({
    name: `ctrlAltF${n}`,
    label: `F${n}`,
  })),
);

const down = ({ keysym, code }) => [keysym, code, true];
const up = ({ keysym, code }) => [keysym, code, false];

// presses the modifiers in order, taps the key, and lets go in reverse order
const chord = (modifiers, target) => [
  ...modifiers.map(down),
  down(target),
  up(target),
  ...[...modifiers].reverse().map(up),
];

/**
 * The key events of a named key or key combination, as `[keysym, code, down]`
 * for RFB.sendKey. An unknown name gives no events.
 *
 * @param {string} name for example 'ctrlAltDel', 'ctrlAltF2' or 'escape'
 * @returns {[number, string, boolean][]}
 */
export function keyEvents(name) {
  const ctrlAlt = [KEYS.controlLeft, KEYS.altLeft];
  if (name === 'ctrlAltDel') return chord(ctrlAlt, KEYS.delete);
  const f = /^ctrlAltF([1-7])$/.exec(name);
  if (f) return chord(ctrlAlt, KEYS[`f${f[1]}`]);
  if (['escape', 'tab', 'printScreen'].includes(name)) {
    return chord([], KEYS[name]);
  }
  return [];
}

/**
 * The key events that press or let go of a Hold key.
 *
 * @param {'ctrl'|'alt'|'win'} name
 * @param {boolean} pressed
 */
export function holdEvents(name, pressed) {
  const held = HOLD_KEYS[name];
  if (!held) return [];
  return [pressed ? down(held) : up(held)];
}

/** The key events of "Release held keys". */
export function releaseEvents() {
  return RELEASE_KEYS.map(up);
}
