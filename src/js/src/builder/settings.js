// Builder settings: how this viewer likes the editor, whatever the diagram
// (BuilderSettings.vue). localStorage phenix.builder.settings holds only the
// changed values, each a listed choice or a ranged number, so it holds no user
// data and logout keeps it. An unknown key or value falls back to the default.

import { reactive } from 'vue';

import { DEFAULT_LAYOUT_ALGORITHM, layoutAlgorithm } from './layouts/index.js';
import { ICON_SIZES } from './model.js';
import { followStorageKey, pageStorage } from './storage.js';

export const SETTINGS_STORAGE_KEY = 'phenix.builder.settings';

// The value of nodeIconSize that uses the diagram's icon sizes.
export const ICON_SIZE_OFF = 'off';

// The zoom a diagram opens with: 100% from the diagram's origin, the whole
// diagram fitted to the canvas, or a percentage of the viewer's own, from
// the diagram's origin too.
const OPEN_ZOOMS = Object.freeze(['actual', 'fit', 'custom']);

// The percentages the custom zoom takes: the canvas's own least and most
// zoom, in steps.
export const OPEN_ZOOM_PERCENT = Object.freeze({ min: 20, max: 200, step: 5 });

const isBoolean = (value) => typeof value === 'boolean';

const isZoomPercent = (value) =>
  Number.isInteger(value) &&
  value >= OPEN_ZOOM_PERCENT.min &&
  value <= OPEN_ZOOM_PERCENT.max &&
  value % OPEN_ZOOM_PERCENT.step === 0;

// Each setting's default, and the values it takes.
const SETTINGS = {
  // The layout a layout run uses on a draft that has none of its own, which
  // the draft then keeps (layouts/index.js, documentLayout).
  layoutAlgorithm: {
    default: DEFAULT_LAYOUT_ALGORITHM,
    valid: (value) => Boolean(layoutAlgorithm(value)),
  },
  // Whether the canvas shows the minimap. The toolbar's Minimap button
  // shows or hides it until the next diagram opens.
  showMinimap: { default: true, valid: isBoolean },
  // Whether devices, switches and groups show their icons on the canvas and
  // in image downloads. Purely visual: the document keeps every icon
  // (nodes/nodeIconSize.js). The palette's view.nodeIcons toggles it.
  showNodeIcons: { default: true, valid: isBoolean },
  // Whether devices and groups show their descriptions on the canvas and in
  // drawings. A node can also hide its own (drawnNodeComment in model.js).
  showNodeDescriptions: { default: true, valid: isBoolean },
  // Whether the canvas shows the connections. Purely visual: the document
  // keeps them (BuilderCanvas.vue). The palette's view.connections toggles it.
  showConnections: { default: true, valid: isBoolean },
  // The size every device, switch and group draws its icon at on this
  // viewer's canvas and in image downloads, one of ICON_SIZES. Off uses
  // the sizes the diagram names (see nodeIconSize in model.js). The
  // diagram never keeps this size.
  nodeIconSize: {
    default: ICON_SIZE_OFF,
    valid: (value) => value === ICON_SIZE_OFF || ICON_SIZES.includes(value),
  },
  // Whether a connection to a network with a subnet gives a static
  // interface with no address the next free address (see connect in
  // model.js). Assign addresses does so whatever this is.
  assignAddressesOnConnect: { default: true, valid: isBoolean },
  // Whether the canvas shows X and Y rulers along its top and left edges,
  // in the units of a node's Position (BuilderCanvas.vue, rulers.js).
  showCoordinates: { default: false, valid: isBoolean },
  // Whether drags, drops from the palette, edits of a line's points and the
  // positions of new nodes go to the diagram's grid, in steps of its grid
  // size (BuilderCanvas.vue, LineNode.vue, freeSpot in model.js). Off, they
  // go to whole pixels. The palette's view.snapToGrid toggles it.
  snapToGrid: { default: false, valid: isBoolean },
  // The zoom a diagram opens with, which Reset view goes back to.
  openZoom: { default: 'actual', valid: (value) => OPEN_ZOOMS.includes(value) },
  // The percentage a diagram opens at while openZoom is custom.
  openZoomPercent: { default: 100, valid: isZoomPercent },
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

// An icon size as the Settings dialog names it: Small, Medium or Large.
const sizeTitle = (size) => `${size[0].toUpperCase()}${size.slice(1)}`;

// The icon size that nodeIconSize gives every node on the canvas, or undefined
// while it is Off. Reactive.
export function iconSizeOverride() {
  const size = builderSettings.nodeIconSize;

  return ICON_SIZES.includes(size) ? size : undefined;
}

// What an Inspector Icon size field says while nodeIconSize overrides it, or
// '' while it is Off. Reactive.
export function iconSizeOverrideNote() {
  const size = iconSizeOverride();

  return size
    ? `Your Node icon size setting (${sizeTitle(size)}) overrides this on your canvas.`
    : '';
}

/** What the Builder says when the palette changes the setting nodeIconSize. */
export function iconSizeMessage(size) {
  return ICON_SIZES.includes(size)
    ? `Node icons are ${sizeTitle(size)} on your canvas.`
    : 'Node icons use the sizes of the diagram.';
}

/** Whether `key` names a setting and `value` is one of its values. */
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

// Stores the settings changed from their defaults. When there are none, it
// removes the key.
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

// Reads the stored settings, with defaults for the rest. It writes only the
// settings whose value changes, so a watcher of one setting gets no signal
// when another tab changes a different one.
export function loadSettings(storage = pageStorage()) {
  const loaded = { ...SETTING_DEFAULTS, ...readStored(storage) };

  for (const [key, value] of Object.entries(loaded)) {
    if (builderSettings[key] !== value) {
      builderSettings[key] = value;
    }
  }

  return builderSettings;
}

// Changes a setting and keeps it. It reads the stored settings first, so it
// keeps another tab's change. It returns false for an unknown key or value.
export function setSetting(key, value, storage = pageStorage()) {
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
 * Sets every setting to its default, and forgets the stored settings.
 *
 * @param {Storage|null} [storage] localStorage by default
 */
export function resetSettings(storage = pageStorage()) {
  Object.assign(builderSettings, SETTING_DEFAULTS);
  writeStored({}, storage);
}

/** Follows changes made to the settings in another tab of the Builder. */
export function followSettings(target = globalThis.window, storage) {
  const from = () => (storage === undefined ? pageStorage() : storage);
  return followStorageKey(
    SETTINGS_STORAGE_KEY,
    () => loadSettings(from()),
    target,
  );
}

// The settings are preferences of this browser: logout keeps them (see
// session.js). Every Builder tab follows the others for as long as the page
// is open, so the settings are current wherever the Builder shows next.
loadSettings();
followSettings();
