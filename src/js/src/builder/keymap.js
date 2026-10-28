// Keyboard shortcuts: key specs such as 'Mod+Shift+G' or '?', their labels, and
// how they match key presses. Mod is ⌘ on macOS and Ctrl elsewhere. Letters
// match KeyboardEvent.key, so they follow the layout. Digits and Alt keys match
// KeyboardEvent.code, the physical key. Users can change the keys.

import { reactive, ref } from 'vue';

import { followStorageKey, pageStorage } from './storage.js';
// --- platform ----------------------------------------------------------------

/**
 * The platform the keys are for: 'mac' (macOS and iPadOS) or 'other'.
 * navigator.platform comes first, because it names the real machine even when
 * the user agent is emulated, so labels and keys agree.
 */
export function detectPlatform(nav = globalThis.navigator) {
  const name =
    nav?.platform || nav?.userAgentData?.platform || nav?.userAgent || '';

  return /mac|iphone|ipad|ipod/i.test(name) ? 'mac' : 'other';
}

const platformRef = ref(null);

/**
 * The platform in use, detected on first use. It is reactive, so labels follow
 * setPlatform.
 */
export function currentPlatform() {
  if (!platformRef.value) {
    platformRef.value = detectPlatform();
  }

  return platformRef.value;
}

/**
 * Overrides the detected platform (tests). null detects it again.
 *
 * @param {'mac'|'other'|null} platform
 */
export function setPlatform(platform) {
  platformRef.value =
    platform === 'mac' || platform === 'other' ? platform : null;
}

// --- parsing -----------------------------------------------------------------

const MODIFIERS = {
  mod: 'mod',
  ctrl: 'ctrl',
  control: 'ctrl',
  meta: 'meta',
  cmd: 'meta',
  command: 'meta',
  alt: 'alt',
  option: 'alt',
  shift: 'shift',
};

// Named keys: the KeyboardEvent.key value, then the key cap and the word for
// each platform. The caps follow the keyboards: a Mac's Backspace key is
// labelled delete (⌫) and its Delete key is forward delete (⌦).
const NAMED = {
  Enter: { key: 'Enter', mac: ['↩', 'Return'], other: ['Enter', 'Enter'] },
  Escape: { key: 'Escape', mac: ['esc', 'Escape'], other: ['Esc', 'Escape'] },
  Backspace: {
    key: 'Backspace',
    mac: ['⌫', 'Delete'],
    other: ['Backspace', 'Backspace'],
  },
  Delete: {
    key: 'Delete',
    mac: ['⌦', 'Forward Delete'],
    other: ['Delete', 'Delete'],
  },
  Tab: { key: 'Tab', mac: ['⇥', 'Tab'], other: ['Tab', 'Tab'] },
  Space: { key: ' ', mac: ['Space', 'Space'], other: ['Space', 'Space'] },
  ArrowUp: { key: 'ArrowUp', mac: ['↑', 'Up Arrow'], other: ['↑', 'Up Arrow'] },
  ArrowDown: {
    key: 'ArrowDown',
    mac: ['↓', 'Down Arrow'],
    other: ['↓', 'Down Arrow'],
  },
  ArrowLeft: {
    key: 'ArrowLeft',
    mac: ['←', 'Left Arrow'],
    other: ['←', 'Left Arrow'],
  },
  ArrowRight: {
    key: 'ArrowRight',
    mac: ['→', 'Right Arrow'],
    other: ['→', 'Right Arrow'],
  },
  Home: { key: 'Home', mac: ['↖', 'Home'], other: ['Home', 'Home'] },
  End: { key: 'End', mac: ['↘', 'End'], other: ['End', 'End'] },
  PageUp: { key: 'PageUp', mac: ['⇞', 'Page Up'], other: ['PgUp', 'Page Up'] },
  PageDown: {
    key: 'PageDown',
    mac: ['⇟', 'Page Down'],
    other: ['PgDn', 'Page Down'],
  },
};

for (let n = 1; n <= 12; n += 1) {
  NAMED[`F${n}`] = {
    key: `F${n}`,
    mac: [`F${n}`, `F${n}`],
    other: [`F${n}`, `F${n}`],
  };
}

const NAMED_ALIASES = {
  esc: 'Escape',
  return: 'Enter',
  del: 'Delete',
  up: 'ArrowUp',
  down: 'ArrowDown',
  left: 'ArrowLeft',
  right: 'ArrowRight',
  pgup: 'PageUp',
  pgdn: 'PageDown',
  space: 'Space',
};

const NAMED_BY_LOWER = Object.fromEntries(
  Object.keys(NAMED).map((name) => [name.toLowerCase(), name]),
);

// The physical key of each unshifted US character, to match a character
// pressed with Alt. Also the unshifted key with which a shifted US character
// is typed, for aria-keyshortcuts (which names the keys pressed, not the
// typed character).
const CHAR_CODES = {
  '-': 'Minus',
  '=': 'Equal',
  '[': 'BracketLeft',
  ']': 'BracketRight',
  '\\': 'Backslash',
  ';': 'Semicolon',
  "'": 'Quote',
  ',': 'Comma',
  '.': 'Period',
  '/': 'Slash',
  '`': 'Backquote',
};

const US_SHIFTED = {
  '~': '`',
  '!': '1',
  '@': '2',
  '#': '3',
  $: '4',
  '%': '5',
  '^': '6',
  '&': '7',
  '*': '8',
  '(': '9',
  ')': '0',
  _: '-',
  '+': '=',
  '{': '[',
  '}': ']',
  '|': '\\',
  ':': ';',
  '"': "'",
  '<': ',',
  '>': '.',
  '?': '/',
};

const parsed = new Map();

/**
 * Parses a key spec into its parts, with `spec` in the canonical modifier order
 * Mod, Ctrl, Meta, Alt, Shift. Null when it is not one key with modifiers.
 */
export function parseKey(spec) {
  if (typeof spec !== 'string' || !spec) {
    return null;
  }

  if (!parsed.has(spec)) {
    parsed.set(spec, parseUncached(spec));
  }

  return parsed.get(spec);
}

function parseUncached(spec) {
  // A trailing '+' is the plus key: 'Mod++' or '+'.
  const plus = spec === '+' || spec.endsWith('++');
  const tokens = (plus ? spec.slice(0, -1) : spec).split('+');

  if (tokens.at(-1) === '') {
    tokens.pop();
  }

  const keyToken = plus ? '+' : tokens.pop();
  const flags = {
    mod: false,
    ctrl: false,
    meta: false,
    alt: false,
    shift: false,
  };

  for (const token of tokens) {
    const modifier = MODIFIERS[token.trim().toLowerCase()];
    if (!modifier || flags[modifier]) {
      return null;
    }
    flags[modifier] = true;
  }

  const key = keyPart(keyToken);
  if (!key) {
    return null;
  }

  // ⌘ and Ctrl are one key on each platform. Thus Mod with either is two
  // names for one key on one platform, and a different chord on the other.
  if (flags.mod && (flags.ctrl || flags.meta)) {
    return null;
  }

  // A character is what Shift typed. 'Shift+?' is '?'.
  if (key.kind === 'char') {
    flags.shift = false;
  }

  const names = [
    flags.mod && 'Mod',
    flags.ctrl && 'Ctrl',
    flags.meta && 'Meta',
    flags.alt && 'Alt',
    flags.shift && 'Shift',
  ].filter(Boolean);

  return Object.freeze({
    spec: [...names, key.key].join('+'),
    key: key.key,
    kind: key.kind,
    ...flags,
  });
}

function keyPart(token) {
  if (typeof token !== 'string' || token.length === 0) {
    return null;
  }

  if (token.length === 1) {
    if (/[a-z]/i.test(token)) {
      return { key: token.toUpperCase(), kind: 'letter' };
    }
    if (/[0-9]/.test(token)) {
      return { key: token, kind: 'digit' };
    }
    if (token === ' ') {
      return { key: 'Space', kind: 'named' };
    }

    return /[\x21-\x7e]/.test(token) ? { key: token, kind: 'char' } : null;
  }

  const lower = token.trim().toLowerCase();
  const name = NAMED_BY_LOWER[lower] || NAMED_ALIASES[lower];

  return name ? { key: name, kind: 'named' } : null;
}

/**
 * @param {string} spec
 * @returns {string} the spec written the canonical way, or '' when invalid
 */
export function normalizeKey(spec) {
  return parseKey(spec)?.spec || '';
}

/** The modifiers that a spec or parseKey result needs on a platform. */
function keyModifiers(spec, platform = currentPlatform()) {
  const parts = typeof spec === 'string' ? parseKey(spec) : spec;

  if (!parts) {
    return null;
  }

  return {
    ctrl: parts.ctrl || (parts.mod && platform !== 'mac'),
    meta: parts.meta || (parts.mod && platform === 'mac'),
    alt: parts.alt,
    shift: parts.shift,
  };
}

/**
 * Whether two specs are the same keys on a platform, such as 'Mod+K' and
 * 'Meta+K' on macOS.
 */
export function sameKey(a, b, platform = currentPlatform()) {
  const left = parseKey(a);
  const right = parseKey(b);

  if (!left || !right || left.key !== right.key) {
    return false;
  }

  const one = keyModifiers(left, platform);
  const two = keyModifiers(right, platform);

  return (
    one.ctrl === two.ctrl &&
    one.meta === two.meta &&
    one.alt === two.alt &&
    one.shift === two.shift
  );
}

/**
 * Whether a spec is one printable character, with Shift at most, such as '?'.
 * WCAG 2.1.4 requires a way to turn these off: the single-key switch.
 */
export function isCharacterKey(spec) {
  const parts = parseKey(spec);

  return Boolean(
    parts &&
      parts.kind !== 'named' &&
      !parts.mod &&
      !parts.ctrl &&
      !parts.meta &&
      !parts.alt,
  );
}

/**
 * Whether a key types a character in a text field, which then keeps the key. On
 * macOS, Option with a character also types one (⌥E is a dead key). AltGr,
 * reported as Ctrl+Alt, is refused as a shortcut (keyRefusal).
 */
export function typesCharacter(spec, platform = currentPlatform()) {
  const parts = parseKey(spec);

  if (!parts || isCharacterKey(spec)) {
    return Boolean(parts);
  }

  const want = keyModifiers(parts, platform);

  return (
    platform === 'mac' &&
    parts.kind !== 'named' &&
    want.alt &&
    !want.ctrl &&
    !want.meta
  );
}

// --- matching ----------------------------------------------------------------

function asciiLetter(value) {
  return typeof value === 'string' && /^[a-z]$/i.test(value);
}

/**
 * Whether a key press is a spec on a platform. The modifiers must be exactly
 * the spec's, except Shift for a character (Shift is part of typing '?').
 */
export function matchesKey(event, spec, platform = currentPlatform()) {
  const parts = typeof spec === 'string' ? parseKey(spec) : spec;

  if (!parts || !event) {
    return false;
  }

  const want = keyModifiers(parts, platform);
  let ctrl = Boolean(event.ctrlKey);
  let alt = Boolean(event.altKey);

  // AltGr, which Windows reports as Ctrl+Alt, typed the character.
  if (
    parts.kind === 'char' &&
    !want.ctrl &&
    !want.alt &&
    event.getModifierState?.('AltGraph')
  ) {
    ctrl = false;
    alt = false;
  }

  if (
    ctrl !== want.ctrl ||
    Boolean(event.metaKey) !== want.meta ||
    alt !== want.alt ||
    (parts.kind !== 'char' && Boolean(event.shiftKey) !== want.shift)
  ) {
    return false;
  }

  switch (parts.kind) {
    case 'letter':
      // A layout without Latin letters, or Option, types something else.
      // The physical key then decides.
      return !want.alt && asciiLetter(event.key)
        ? event.key.toUpperCase() === parts.key
        : event.code === `Key${parts.key}`;
    case 'digit':
      return (
        event.code === `Digit${parts.key}` ||
        (!want.shift && event.code === `Numpad${parts.key}`)
      );
    case 'char':
      return (
        event.key === parts.key ||
        (want.alt &&
          Boolean(CHAR_CODES[parts.key]) &&
          event.code === CHAR_CODES[parts.key])
      );
    default:
      return event.key === NAMED[parts.key].key;
  }
}

// --- labels ------------------------------------------------------------------

// macOS writes modifiers as symbols in the order ⌃⌥⇧⌘. Windows and Linux
// write them as words in the order Ctrl, Alt, Shift.
const MAC_MODIFIERS = [
  ['ctrl', '⌃', 'Control'],
  ['alt', '⌥', 'Option'],
  ['shift', '⇧', 'Shift'],
  ['meta', '⌘', 'Command'],
];

const OTHER_MODIFIERS = [
  ['ctrl', 'Ctrl', 'Ctrl'],
  ['alt', 'Alt', 'Alt'],
  ['shift', 'Shift', 'Shift'],
  ['meta', 'Meta', 'Meta'],
];

function keyCap(parts, platform, word) {
  if (parts.kind === 'named') {
    return NAMED[parts.key][platform === 'mac' ? 'mac' : 'other'][word ? 1 : 0];
  }

  // A true minus sign: screen readers say "minus", not "dash".
  return parts.key === '-' ? '−' : parts.key;
}

function caps(spec, platform, { words = false, keyWords = words } = {}) {
  const parts = typeof spec === 'string' ? parseKey(spec) : spec;

  if (!parts) {
    return [];
  }

  const want = keyModifiers(parts, platform);
  const order = platform === 'mac' ? MAC_MODIFIERS : OTHER_MODIFIERS;

  return [
    ...order
      .filter(([flag]) => want[flag])
      .map(([, symbol, word]) => (words ? word : symbol)),
    keyCap(parts, platform, keyWords),
  ];
}

/**
 * The <kbd> key caps of a spec: ['⇧', '⌘', 'G'] on macOS, ['Ctrl', 'Shift',
 * 'G'] elsewhere.
 */
export function keyCaps(spec, platform = currentPlatform()) {
  return caps(spec, platform);
}

/**
 * The label of a spec for tooltips and hints: '⇧⌘G' on macOS, 'Ctrl+Shift+G'
 * elsewhere.
 */
export function keyLabel(spec, platform = currentPlatform()) {
  return caps(spec, platform).join(platform === 'mac' ? '' : '+');
}

/**
 * The label for running text: keyLabel with named keys as words, such as
 * 'Return'.
 */
export function keyText(spec, platform = currentPlatform()) {
  return caps(spec, platform, { keyWords: true }).join(
    platform === 'mac' ? '' : '+',
  );
}

/** The spec in words for screen readers: 'Shift+Command+G', 'Ctrl+Shift+G'. */
export function spokenKey(spec, platform = currentPlatform()) {
  return caps(spec, platform, { words: true }).join('+');
}

const ARIA_MODIFIERS = [
  ['ctrl', 'Control'],
  ['alt', 'Alt'],
  ['shift', 'Shift'],
  ['meta', 'Meta'],
];

/**
 * The aria-keyshortcuts value of a spec, or ''. WAI-ARIA 1.2 requires the
 * physical keys, not the typed character, so '?' becomes the US 'Shift+/'.
 * Modifiers are UI Events key names and come first.
 */
export function ariaKey(spec, platform = currentPlatform()) {
  const parts = parseKey(spec);

  if (!parts) {
    return '';
  }

  const want = keyModifiers(parts, platform);
  let key = parts.key;

  if (parts.kind === 'char' && US_SHIFTED[key]) {
    want.shift = true;
    key = US_SHIFTED[key];
  }

  return [
    ...ARIA_MODIFIERS.filter(([flag]) => want[flag]).map(([, name]) => name),
    key,
  ].join('+');
}

// --- keys a page cannot or must not take ---------------------------------------

// From Chromium's BrowserCommandController::IsReservedCommandOrKey and the
// keys Firefox marks reserved="true" in browser-sets.inc.xhtml: the browser
// handles them before the page sees them. Then keys the page could take but
// must not (zoom, reload, the address bar, full screen), and the macOS
// system keys. `on` limits an entry to one platform.
const RESERVED_KEYS = [
  { keys: ['Mod+T'], what: 'open a new tab', by: 'browser' },
  { keys: ['Mod+N'], what: 'open a new window', by: 'browser' },
  {
    keys: ['Mod+Shift+N', 'Mod+Shift+P'],
    what: 'open a private window',
    by: 'browser',
  },
  { keys: ['Mod+W', 'Ctrl+F4'], what: 'close the tab', by: 'browser' },
  {
    keys: ['Mod+Shift+W', 'Alt+F4'],
    what: 'close the window',
    by: 'browser',
  },
  { keys: ['Mod+Shift+T'], what: 'reopen a closed tab', by: 'browser' },
  {
    keys: [
      'Ctrl+Tab',
      'Ctrl+Shift+Tab',
      'Ctrl+PageUp',
      'Ctrl+PageDown',
      ...Array.from({ length: 9 }, (_, n) => `Mod+${n + 1}`),
    ],
    what: 'switch tabs',
    by: 'browser',
  },
  {
    // ⇧⌘[ and ⇧⌘] type { and }.
    keys: ['Meta+{', 'Meta+}', 'Meta+Alt+Left', 'Meta+Alt+Right'],
    what: 'switch tabs',
    by: 'browser',
    on: 'mac',
  },
  {
    keys: Array.from({ length: 9 }, (_, n) => `Alt+${n + 1}`),
    what: 'switch tabs',
    by: 'browser',
    on: 'other',
  },
  { keys: ['Meta+Q'], what: 'quit', by: 'browser', on: 'mac' },
  { keys: ['Ctrl+Shift+Q'], what: 'quit', by: 'browser', on: 'other' },
  {
    keys: ['Mod+=', 'Mod++', 'Mod+-', 'Mod+0'],
    what: 'zoom, which people rely on to enlarge text',
    by: 'page',
  },
  {
    keys: ['Mod+R', 'Mod+Shift+R', 'F5', 'Ctrl+F5', 'Shift+F5'],
    what: 'reload the page',
    by: 'page',
  },
  { keys: ['F6', 'Mod+L'], what: 'reach the address bar', by: 'page' },
  {
    keys: ['Alt+Left', 'Alt+Right'],
    what: 'go to the previous or next page',
    by: 'page',
    on: 'other',
  },
  {
    keys: ['Meta+[', 'Meta+]', 'Meta+Left', 'Meta+Right'],
    what: 'go to the previous or next page',
    by: 'page',
    on: 'mac',
  },
  { keys: ['F11'], what: 'show the page full screen', by: 'page', on: 'other' },
  {
    keys: ['Meta+Ctrl+F'],
    what: 'show the page full screen',
    by: 'page',
    on: 'mac',
  },
  {
    keys: ['Meta+Space', 'Ctrl+Space'],
    what: 'search and switch input sources',
    by: 'mac',
    on: 'mac',
  },
  {
    keys: ['Meta+Tab', 'Meta+Shift+Tab', 'Meta+`', 'Meta+Shift+`'],
    what: 'switch apps and windows',
    by: 'mac',
    on: 'mac',
  },
  {
    keys: ['Meta+H', 'Meta+Alt+H', 'Meta+M'],
    what: 'hide and minimize windows',
    by: 'mac',
    on: 'mac',
  },
  {
    keys: ['Meta+Shift+3', 'Meta+Shift+4', 'Meta+Shift+5'],
    what: 'take screenshots',
    by: 'mac',
    on: 'mac',
  },
  {
    keys: ['Meta+Alt+Escape'],
    what: 'force apps to quit',
    by: 'mac',
    on: 'mac',
  },
];

// Keys that already move through and operate the Builder's controls. The
// controls also take them with modifiers (⌘⌫ deletes on the canvas, ⇧↑ moves
// nodes), before the view's shortcuts get them. Thus none of them is a
// shortcut with any modifiers.
const OPERATING_KEYS = new Set([
  'Tab',
  'Enter',
  'Space',
  'Escape',
  'Backspace',
  'Delete',
  'ArrowUp',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
  'Home',
  'End',
  'PageUp',
  'PageDown',
  'F2',
]);

/**
 * Why the browser or the OS keeps a key from the page, or '' when the key is
 * free.
 */
export function reservedReason(spec, platform = currentPlatform()) {
  const label = keyLabel(spec, platform);

  for (const entry of RESERVED_KEYS) {
    if (entry.on && entry.on !== platform) {
      continue;
    }
    if (!entry.keys.some((reserved) => sameKey(spec, reserved, platform))) {
      continue;
    }

    switch (entry.by) {
      case 'browser':
        return `Browsers keep ${label} to ${entry.what} and never pass it to the page.`;
      case 'mac':
        return `macOS uses ${label} to ${entry.what}.`;
      default:
        return `Browsers use ${label} to ${entry.what}.`;
    }
  }

  return '';
}

/**
 * Why a key cannot be a shortcut, or ''. Refused are reserved keys, Ctrl+Alt
 * off macOS (AltGr), keys the Builder's controls use, and a letter alone unless
 * `letters` allows it for a canvas-only command.
 */
export function keyRefusal(
  spec,
  platform = currentPlatform(),
  { letters = false } = {},
) {
  const parts = parseKey(spec);

  if (!parts) {
    return 'That is not a key combination that the Builder can use.';
  }

  const reserved = reservedReason(spec, platform);
  if (reserved) {
    return reserved;
  }

  const want = keyModifiers(parts, platform);
  const label = keyLabel(spec, platform);

  if (platform !== 'mac' && want.ctrl && want.alt) {
    return `The Builder does not use ${label}: Ctrl+Alt types characters on many keyboard layouts.`;
  }

  if (
    parts.kind === 'letter' &&
    !want.ctrl &&
    !want.meta &&
    !want.alt &&
    !letters
  ) {
    const mod = platform === 'mac' ? '⌘' : 'Ctrl';

    return `Letters without ${mod} are for typing. Screen readers use them to move through the page.`;
  }

  if (parts.kind === 'named' && OPERATING_KEYS.has(parts.key)) {
    const alone = keyText(parts.key, platform);

    // The key alone is named once. With modifiers, the key is also named.
    return label === keyLabel(parts.key, platform)
      ? `${label} already moves focus between the Builder's controls or operates them.`
      : `${label} already moves focus between the Builder's controls or operates them. The controls take ${alone} with any modifiers.`;
  }

  return '';
}

// --- recording ---------------------------------------------------------------

const MODIFIER_KEYS = new Set([
  'Control',
  'Meta',
  'Alt',
  'AltGraph',
  'Shift',
  'CapsLock',
  'OS',
  'Hyper',
  'Super',
  'Fn',
]);

const CODE_CHARS = Object.fromEntries(
  Object.entries(CHAR_CODES).map(([char, code]) => [code, char]),
);

/**
 * The spec for a key press, to record a new shortcut, or '' while only
 * modifiers are down. Alt keys are named by their physical key.
 */
export function eventToKey(event, platform = currentPlatform()) {
  if (!event?.key || MODIFIER_KEYS.has(event.key) || event.isComposing) {
    return '';
  }

  let key;
  const code = String(event.code || '');

  // 'Shift+5' is the 5 above the letters (matchesKey), so no spec names a
  // numpad digit pressed with Shift.
  if (event.shiftKey && /^Numpad[0-9]$/.test(code)) {
    return '';
  }

  if (asciiLetter(event.key) && !event.altKey) {
    key = event.key.toUpperCase();
  } else if (/^Key[A-Z]$/.test(code)) {
    key = code.slice(3);
  } else if (/^(Digit|Numpad)[0-9]$/.test(code)) {
    key = code.slice(-1);
  } else if (event.altKey && CODE_CHARS[code]) {
    key = CODE_CHARS[code];
  } else if (event.key === ' ') {
    key = 'Space';
  } else if (event.key.length === 1) {
    key = event.key;
  } else {
    key = keyPart(event.key)?.kind === 'named' ? keyPart(event.key).key : '';
  }

  if (!key) {
    return '';
  }

  const command = platform === 'mac' ? event.metaKey : event.ctrlKey;
  const other = platform === 'mac' ? event.ctrlKey : event.metaKey;

  return normalizeKey(
    [
      command && 'Mod',
      other && (platform === 'mac' ? 'Ctrl' : 'Meta'),
      event.altKey && 'Alt',
      event.shiftKey && 'Shift',
      key,
    ]
      .filter(Boolean)
      .join('+'),
  );
}

// --- customization -------------------------------------------------------------

// Per-browser shortcut settings in localStorage: {"keys": {"<command id>":
// ["Mod+Shift+L"]}, "singleKeys": false} An empty list means no shortcut. If
// storage fails, settings last as long as the page.
export const SHORTCUTS_STORAGE_KEY = 'phenix.builder.shortcuts';

/**
 * The customization in effect. Reactive, so hints computed from it update as
 * soon as a shortcut changes. Change it only through the functions below.
 */
export const keymapState = reactive({ overrides: {}, singleKeys: true });

function cleanKeys(keys) {
  return Array.isArray(keys)
    ? [...new Set(keys.map((key) => normalizeKey(key)).filter(Boolean))]
    : null;
}

/**
 * Reads the customization. Anything unreadable stays at the defaults.
 *
 * @param {Storage|null} [storage] localStorage by default
 */
export function loadShortcutSettings(storage = pageStorage()) {
  let stored;

  try {
    stored = JSON.parse(storage?.getItem(SHORTCUTS_STORAGE_KEY) || 'null');
  } catch {
    stored = null;
  }

  const overrides = {};
  const keys = stored && typeof stored.keys === 'object' ? stored.keys : {};

  for (const [id, list] of Object.entries(keys || {})) {
    const clean = cleanKeys(list);
    if (clean) {
      overrides[id] = clean;
    }
  }

  keymapState.overrides = overrides;
  keymapState.singleKeys = stored?.singleKeys !== false;
}

function saveShortcutSettings(storage) {
  try {
    storage?.setItem(
      SHORTCUTS_STORAGE_KEY,
      JSON.stringify({
        keys: keymapState.overrides,
        singleKeys: keymapState.singleKeys,
      }),
    );
  } catch {
    // Blocked or full: the settings still apply to this page.
  }
}

/**
 * The keys that replace a command's defaults: undefined for the defaults, []
 * for none.
 */
export function shortcutOverride(id) {
  return keymapState.overrides[id];
}

/**
 * Gives a command these keys in place of its defaults, and drops invalid specs.
 * It stores what it gets, so check keyRefusal and conflicts first.
 */
export function setShortcut(id, keys, storage = pageStorage()) {
  const clean = cleanKeys(keys) || [];

  keymapState.overrides = { ...keymapState.overrides, [id]: clean };
  saveShortcutSettings(storage);

  return clean;
}

/** Gives a command its default keys back. */
export function resetShortcut(id, storage = pageStorage()) {
  const rest = { ...keymapState.overrides };

  delete rest[id];
  keymapState.overrides = rest;
  saveShortcutSettings(storage);
}

/**
 * Gives every command its default keys back. The single-key switch stays as it
 * is.
 */
export function resetAllShortcuts(storage = pageStorage()) {
  keymapState.overrides = {};
  saveShortcutSettings(storage);
}

/** Turns the one-character shortcuts on or off (WCAG 2.1.4). */
export function setSingleKeyShortcuts(on, storage = pageStorage()) {
  keymapState.singleKeys = Boolean(on);
  saveShortcutSettings(storage);
}

/**
 * Follows changes made in another Builder tab. Returns a function that stops.
 */
export function followShortcutSettings(target = globalThis.window) {
  return followStorageKey(
    SHORTCUTS_STORAGE_KEY,
    () => loadShortcutSettings(),
    target,
  );
}

// The customization is a preference of this browser: logout keeps it (see
// session.js).
loadShortcutSettings();
