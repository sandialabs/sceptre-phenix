// Builder Flow settings: how this viewer likes the editor, whatever the
// diagram. The Settings dialog (BuilderSettings.vue) changes them.
//
// They are kept per browser in localStorage under phenix.builder.settings,
// as one JSON object that holds the settings changed from their defaults:
//   { "layoutAlgorithm": "dagre", "showMinimap": false }
// Every value is one of the choices listed below, so nothing about the user
// or their work (names, drafts, tokens) can be stored there, and logout
// keeps the key (see session.js). The theme and the keyboard shortcuts keep
// keys of their own (theme.js, keymap.js); the dialog shows them with these.
//
// Storage that is blocked or full is not an error: the settings then last
// as long as the page. A stored setting this Builder does not know, from
// another version of it, or a value it no longer offers, is dropped, and
// the setting's default applies.

import { reactive } from 'vue';

import { DEFAULT_LAYOUT_ALGORITHM, layoutAlgorithm } from './layouts/index.js';

export const SETTINGS_STORAGE_KEY = 'phenix.builder.settings';

// The zoom a diagram opens with: 100% from the diagram's origin, or the
// whole diagram fitted to the canvas.
export const OPEN_ZOOMS = Object.freeze(['actual', 'fit']);

const isBoolean = (value) => typeof value === 'boolean';

// Each setting's default, and the values it takes.
const SETTINGS = {
  // The algorithm Auto layout arranges the diagram with (layouts/index.js).
  layoutAlgorithm: {
    default: DEFAULT_LAYOUT_ALGORITHM,
    valid: (value) => Boolean(layoutAlgorithm(value)),
  },
  // Whether the canvas shows the minimap. The toolbar's Minimap button
  // shows or hides it until the next diagram opens.
  showMinimap: { default: true, valid: isBoolean },
  // The zoom a diagram opens with, which Reset view goes back to.
  openZoom: { default: 'actual', valid: (value) => OPEN_ZOOMS.includes(value) },
  // Motion reduced whatever the system asks for: the canvas pans and zooms
  // at once, and nothing animates.
  reduceMotion: { default: false, valid: isBoolean },
};

export const SETTING_DEFAULTS = Object.freeze(
  Object.fromEntries(
    Object.entries(SETTINGS).map(([key, setting]) => [key, setting.default]),
  ),
);

/**
 * The settings in effect. Reactive, so what reads them follows a change.
 * Change them only through setSetting and resetSettings.
 */
export const builderSettings = reactive({ ...SETTING_DEFAULTS });

// Reading localStorage throws where site data is blocked.
function browserStorage() {
  try {
    return globalThis.window?.localStorage || null;
  } catch {
    return null;
  }
}

/**
 * @param {string} key
 * @param {*} value
 * @returns {boolean} whether `key` names a setting and `value` is one of
 *   its values
 */
export function isSettingValue(key, value) {
  return Object.hasOwn(SETTINGS, key) && SETTINGS[key].valid(value);
}

// The stored settings this Builder knows, with values it offers.
function readStored(storage) {
  let stored;

  try {
    stored = JSON.parse(storage?.getItem(SETTINGS_STORAGE_KEY) || 'null');
  } catch {
    stored = null;
  }

  if (!stored || typeof stored !== 'object' || Array.isArray(stored)) {
    return {};
  }

  return Object.fromEntries(
    Object.keys(SETTINGS)
      .filter((key) => isSettingValue(key, stored[key]))
      .map((key) => [key, stored[key]]),
  );
}

// Keeps the settings changed from their defaults; none removes the key.
function writeStored(settings, storage) {
  const changed = Object.fromEntries(
    Object.entries(settings).filter(
      ([key, value]) => value !== SETTINGS[key].default,
    ),
  );

  try {
    if (Object.keys(changed).length) {
      storage?.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(changed));
    } else {
      storage?.removeItem(SETTINGS_STORAGE_KEY);
    }
  } catch {
    // Blocked or full: the settings still apply to this page.
  }
}

/**
 * Reads the stored settings; any not stored, or unreadable, takes its
 * default. Only the settings whose value changes are written, so what
 * follows one setting (the view's minimap) hears nothing when another tab
 * changes another.
 *
 * @param {Storage|null} [storage] localStorage by default
 * @returns {object} builderSettings
 */
export function loadSettings(storage = browserStorage()) {
  const loaded = { ...SETTING_DEFAULTS, ...readStored(storage) };

  for (const [key, value] of Object.entries(loaded)) {
    if (builderSettings[key] !== value) {
      builderSettings[key] = value;
    }
  }

  return builderSettings;
}

/**
 * Changes a setting and keeps it. What is stored is read first, so a
 * setting changed in another tab meanwhile is kept too.
 *
 * @param {string} key
 * @param {*} value
 * @param {Storage|null} [storage] localStorage by default
 * @returns {boolean} false, and nothing changed or stored, for a key that
 *   is no setting or a value it does not take
 */
export function setSetting(key, value, storage = browserStorage()) {
  if (!isSettingValue(key, value)) {
    return false;
  }

  builderSettings[key] = value;
  writeStored({ ...readStored(storage), [key]: value }, storage);

  return true;
}

/**
 * @returns {boolean} whether every setting has its default
 */
export function settingsAtDefaults() {
  return Object.entries(SETTING_DEFAULTS).every(
    ([key, value]) => builderSettings[key] === value,
  );
}

/**
 * Gives every setting its default back, and forgets the stored ones.
 *
 * @param {Storage|null} [storage] localStorage by default
 */
export function resetSettings(storage = browserStorage()) {
  Object.assign(builderSettings, SETTING_DEFAULTS);
  writeStored({}, storage);
}

/**
 * Follows changes made in another tab of the Builder.
 *
 * @param {Window} [target]
 * @param {Storage|null} [storage] what to read then, localStorage by default
 * @returns {() => void} stops following
 */
export function followSettings(target = globalThis.window, storage) {
  const onStorage = (event) => {
    if (event.key === SETTINGS_STORAGE_KEY || event.key === null) {
      loadSettings(storage === undefined ? browserStorage() : storage);
    }
  };

  target?.addEventListener?.('storage', onStorage);

  return () => target?.removeEventListener?.('storage', onStorage);
}

// The settings are preferences of this browser: logout keeps them (see
// session.js). Every Builder tab follows the others.
loadSettings();
followSettings();
