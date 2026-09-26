// Keyboard shortcuts: key specs, the platform's labels for them, matching
// them against key presses, and the shortcuts each browser keeps for itself.
//
// A key spec is a string of modifiers and one key joined by '+', such as
// 'Mod+K', 'Mod+Shift+G', '?', '+', 'Shift+1' or 'Delete'. Modifiers are
// Mod, Ctrl, Meta, Alt and Shift, in any order and case. Mod is the
// platform's command key: ⌘ on macOS, Ctrl on Windows and Linux, and a Mod
// shortcut does not also answer to the other one. The key is a letter, a
// digit, one printable character, or a named key (Enter, Escape, Backspace,
// Delete, Tab, Space, the arrows, Home, End, PageUp, PageDown, F1 to F12).
//
// Letters match on KeyboardEvent.key, so a shortcut follows the layout the
// user types with. Digits, and any key pressed with Alt (Option changes the
// character on macOS), match on KeyboardEvent.code, the physical key, so
// Shift+1 is the same key on a QWERTY and an AZERTY keyboard. A character
// such as '?' or '=' matches the character typed, whatever Shift or AltGr it
// took to type it.
//
// Users can change the shortcuts, per browser: see "Customization" below.

import { reactive, ref } from 'vue';

// --- platform ----------------------------------------------------------------

/**
 * The platform the keys are for: 'mac' (macOS and iPadOS, whose keyboards
 * have ⌘) or 'other' (Windows, Linux, ChromeOS).
 *
 * navigator.platform is read first: it names the machine the keys come from
 * even where the user agent is emulated (Playwright's device profiles report
 * Windows in the user agent and userAgentData on any host), so the labels and
 * the keys agree. userAgentData and the user agent are the fallbacks.
 *
 * @param {Navigator} [nav]
 * @returns {'mac'|'other'}
 */
export function detectPlatform(nav = globalThis.navigator) {
  const name =
    nav?.platform || nav?.userAgentData?.platform || nav?.userAgent || '';

  return /mac|iphone|ipad|ipod/i.test(name) ? 'mac' : 'other';
}

const platformRef = ref(null);

/**
 * The platform in use, detected on first use. Reactive: a label computed
 * from it follows setPlatform.
 *
 * @returns {'mac'|'other'}
 */
export function currentPlatform() {
  if (!platformRef.value) {
    platformRef.value = detectPlatform();
  }

  return platformRef.value;
}

/**
 * Overrides the detected platform (tests); null detects it again.
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

// The physical key of each unshifted US character, for matching one pressed
// with Alt, and the unshifted key a shifted US character is typed with, for
// aria-keyshortcuts (which names the keys pressed, not the character typed).
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
 * Parses a key spec.
 *
 * @param {string} spec
 * @returns {{spec: string, key: string, kind: 'letter'|'digit'|'char'|'named',
 *   mod: boolean, ctrl: boolean, meta: boolean, alt: boolean,
 *   shift: boolean}|null} the parts, and the spec written the canonical way
 *   (modifiers in the order Mod, Ctrl, Meta, Alt, Shift); null when the spec
 *   is not one key with modifiers
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

  // ⌘ and Ctrl are one key on each platform, so Mod with either is two
  // names for one key on one platform and a different chord on the other.
  if (flags.mod && (flags.ctrl || flags.meta)) {
    return null;
  }

  // A character is what Shift typed; 'Shift+?' is '?'.
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

/**
 * The modifiers a spec needs on a platform.
 *
 * @param {string|object} spec a spec or parseKey's result
 * @param {'mac'|'other'} [platform]
 * @returns {{ctrl: boolean, meta: boolean, alt: boolean, shift: boolean}|null}
 */
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
 * Whether two specs are the same keys on a platform: 'Mod+K' and 'Meta+K' on
 * macOS, 'Mod+K' and 'Ctrl+K' elsewhere.
 *
 * @param {string} a
 * @param {string} b
 * @param {'mac'|'other'} [platform]
 * @returns {boolean}
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
 * A shortcut made of one printable character, with Shift at most: '?', '=',
 * 'Shift+1'. WCAG 2.1.4 asks for a way to turn these off, which the
 * single-key switch below is.
 *
 * @param {string} spec
 * @returns {boolean}
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
 * Whether a key types a character in a text field, which then keeps it: a
 * character key (isCharacterKey), and on macOS a letter, digit or character
 * pressed with Option, with or without Shift, but without ⌘ or ⌃ (⌥E is the
 * acute accent's dead key, ⌥/ types ÷). Alt types nothing on Windows and
 * Linux; AltGr, which they report as Ctrl+Alt, is refused as a shortcut
 * (keyRefusal).
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {boolean}
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
 *
 * @param {KeyboardEvent} event
 * @param {string|object} spec a spec or parseKey's result
 * @param {'mac'|'other'} [platform]
 * @returns {boolean}
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
      // A layout without Latin letters, or Option, types something else;
      // the physical key then decides.
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

// macOS writes modifiers as symbols in the order ⌃⌥⇧⌘; Windows and Linux
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
 * The key caps of a spec, one per key, for <kbd> elements: ['⇧', '⌘', 'G']
 * on macOS, ['Ctrl', 'Shift', 'G'] elsewhere.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string[]} [] when the spec is invalid
 */
export function keyCaps(spec, platform = currentPlatform()) {
  return caps(spec, platform);
}

/**
 * The label of a spec, as tooltips and hints show it: '⇧⌘G' on macOS,
 * 'Ctrl+Shift+G' elsewhere.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string}
 */
export function keyLabel(spec, platform = currentPlatform()) {
  return caps(spec, platform).join(platform === 'mac' ? '' : '+');
}

/**
 * The label for running text: keyLabel, with named keys as words ('⌘A',
 * 'Return', 'Forward Delete'; 'Ctrl+A', 'Enter', 'Delete').
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string}
 */
export function keyText(spec, platform = currentPlatform()) {
  return caps(spec, platform, { keyWords: true }).join(
    platform === 'mac' ? '' : '+',
  );
}

/**
 * The spec in words, for text only screen readers get: 'Shift+Command+G',
 * 'Ctrl+Shift+G'.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string}
 */
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
 * The aria-keyshortcuts value of a spec: 'Meta+K' on macOS, 'Control+K'
 * elsewhere. The value names the physical keys pressed, not the character
 * they type: "If the character used is determined by a modifier key, the
 * author MUST specify the actual key used to generate the character ... the
 * percent sign "%" can be input by pressing Shift+5. The correct way to
 * specify this shortcut is "Shift+5". It is incorrect to specify "%" or
 * "Shift+%"." (WAI-ARIA 1.2, aria-keyshortcuts,
 * https://www.w3.org/TR/wai-aria-1.2/#aria-keyshortcuts; MDN's
 * aria-keyshortcuts page gives "Shift+2" for "@" the same way). So a
 * character typed with Shift is written as its US keys: '?' is 'Shift+/'
 * and '+' is 'Shift+='. Modifiers are UI Events key names and come first,
 * and the space bar is 'Space', as the definition asks.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string} '' when the spec is invalid
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
    what: 'go back and forward',
    by: 'page',
    on: 'other',
  },
  {
    keys: ['Meta+[', 'Meta+]', 'Meta+Left', 'Meta+Right'],
    what: 'go back and forward',
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

// Keys that already move around and operate the Builder's controls. The
// controls take them with modifiers too (⌘⌫ deletes on the canvas, ⌥↑ moves
// nodes, ⌘Return selects an outline row), and before the view's shortcuts
// see them, so none of them is a shortcut with any modifiers.
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
 * Why the browser or the OS keeps a key from the page, if it does: the keys
 * no default shortcut may use and no user may choose.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string} the reason, or '' when the key is free
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
 * Why a key cannot be chosen as a shortcut, if it cannot: a reserved key
 * (reservedReason), Ctrl+Alt on Windows and Linux (AltGr types characters
 * with it), a letter without Ctrl, ⌘ or Alt (typing, and screen reader
 * navigation keys), or a key the Builder's controls use, with any
 * modifiers.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {string} the reason, or '' when the key may be chosen
 */
export function keyRefusal(spec, platform = currentPlatform()) {
  const parts = parseKey(spec);

  if (!parts) {
    return 'That is not a key combination the Builder can use.';
  }

  const reserved = reservedReason(spec, platform);
  if (reserved) {
    return reserved;
  }

  const want = keyModifiers(parts, platform);
  const label = keyLabel(spec, platform);

  if (platform !== 'mac' && want.ctrl && want.alt) {
    return `${label} is not used: Ctrl+Alt types characters on many keyboard layouts.`;
  }

  if (parts.kind === 'letter' && !want.ctrl && !want.meta && !want.alt) {
    const mod = platform === 'mac' ? '⌘' : 'Ctrl';

    return `Letters without ${mod} are for typing, and screen readers use them to move through the page.`;
  }

  if (parts.kind === 'named' && OPERATING_KEYS.has(parts.key)) {
    const alone = keyText(parts.key, platform);

    // The key alone is named once; with modifiers, the key too.
    return label === keyLabel(parts.key, platform)
      ? `${label} already moves around or operates the Builder's controls.`
      : `${label} already moves around or operates the Builder's controls, which take ${alone} with any modifiers.`;
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
 * The spec for a key press, for recording a new shortcut: the platform's
 * command key becomes Mod, and a key pressed with Alt is named by its
 * physical key. '' while only modifiers are down, or for a key no spec can
 * name.
 *
 * @param {KeyboardEvent} event
 * @param {'mac'|'other'} [platform]
 * @returns {string}
 */
export function eventToKey(event, platform = currentPlatform()) {
  if (!event?.key || MODIFIER_KEYS.has(event.key) || event.isComposing) {
    return '';
  }

  let key;
  const code = String(event.code || '');

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

// Shortcuts are customized per browser, under this key in localStorage:
//   { "keys": { "<command id>": ["Mod+Shift+L"], "<command id>": [] },
//     "singleKeys": false }
// A command listed under "keys" uses those keys instead of its defaults; an
// empty list leaves it without a shortcut. "singleKeys": false turns off every
// shortcut that is one character (isCharacterKey). Storage that is blocked or
// full is not an error: the settings then last as long as the page.
export const SHORTCUTS_STORAGE_KEY = 'phenix.builder.shortcuts';

/**
 * The customization in effect. Reactive, so hints computed from it update as
 * soon as a shortcut changes. Change it only through the functions below.
 */
export const keymapState = reactive({ overrides: {}, singleKeys: true });

// Reading localStorage throws where site data is blocked.
function browserStorage() {
  try {
    return globalThis.window?.localStorage || null;
  } catch {
    return null;
  }
}

function cleanKeys(keys) {
  return Array.isArray(keys)
    ? [...new Set(keys.map((key) => normalizeKey(key)).filter(Boolean))]
    : null;
}

/**
 * Reads the customization; anything unreadable is left at the defaults.
 *
 * @param {Storage|null} [storage] localStorage by default
 */
export function loadShortcutSettings(storage = browserStorage()) {
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
 * The keys a command was given in place of its defaults.
 *
 * @param {string} id command id
 * @returns {string[]|undefined} undefined when it keeps its defaults; [] when
 *   it was left without a shortcut
 */
export function shortcutOverride(id) {
  return keymapState.overrides[id];
}

/**
 * Gives a command these keys in place of its defaults; [] leaves it without
 * a shortcut. Invalid specs are dropped. Check keyRefusal and conflicts
 * first: this stores what it is given.
 *
 * @param {string} id command id
 * @param {string[]} keys
 * @param {Storage|null} [storage]
 * @returns {string[]} the keys stored
 */
export function setShortcut(id, keys, storage = browserStorage()) {
  const clean = cleanKeys(keys) || [];

  keymapState.overrides = { ...keymapState.overrides, [id]: clean };
  saveShortcutSettings(storage);

  return clean;
}

/**
 * Leaves a command without a shortcut.
 *
 * @param {string} id command id
 * @param {Storage|null} [storage]
 */
export function unbindShortcut(id, storage = browserStorage()) {
  setShortcut(id, [], storage);
}

/**
 * Gives a command its default keys back.
 *
 * @param {string} id command id
 * @param {Storage|null} [storage]
 */
export function resetShortcut(id, storage = browserStorage()) {
  const rest = { ...keymapState.overrides };

  delete rest[id];
  keymapState.overrides = rest;
  saveShortcutSettings(storage);
}

/**
 * Gives every command its default keys back. The single-key switch is a
 * setting of its own and stays as it is.
 *
 * @param {Storage|null} [storage]
 */
export function resetAllShortcuts(storage = browserStorage()) {
  keymapState.overrides = {};
  saveShortcutSettings(storage);
}

/**
 * Turns the one-character shortcuts on or off (WCAG 2.1.4).
 *
 * @param {boolean} on
 * @param {Storage|null} [storage]
 */
export function setSingleKeyShortcuts(on, storage = browserStorage()) {
  keymapState.singleKeys = Boolean(on);
  saveShortcutSettings(storage);
}

/**
 * Follows changes made in another tab of the Builder.
 *
 * @param {Window} [target]
 * @returns {() => void} stops following
 */
export function followShortcutSettings(target = globalThis.window) {
  const onStorage = (event) => {
    if (event.key === SHORTCUTS_STORAGE_KEY || event.key === null) {
      loadShortcutSettings();
    }
  };

  target?.addEventListener?.('storage', onStorage);

  return () => target?.removeEventListener?.('storage', onStorage);
}

/**
 * The bindings that already use a key on a platform.
 *
 * @param {string} spec
 * @param {{id: string, keys: string[]}[]} bindings
 * @param {'mac'|'other'} [platform]
 * @returns {string[]} their ids
 */
export function findConflicts(spec, bindings, platform = currentPlatform()) {
  return bindings
    .filter((binding) =>
      (binding.keys || []).some((key) => sameKey(spec, key, platform)),
    )
    .map((binding) => binding.id);
}

// The customization is a preference of this browser: logout keeps it (see
// session.js).
loadShortcutSettings();
