// Values this browser remembers in localStorage. Storage can be missing or
// throw (private windows, blocked site data); reads then find nothing and
// writes are dropped, so a value lasts only for the visit.

// the stored string, or null
export function readPref(key) {
  try {
    return globalThis.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

export function writePref(key, value) {
  try {
    globalThis.localStorage?.setItem(key, String(value));
  } catch {
    // see above
  }
}

export function removePref(key) {
  try {
    globalThis.localStorage?.removeItem(key);
  } catch {
    // see above
  }
}

// The live tile limit of the VNC tab and the VM Tiles page: the most live VNC
// connections that the tiles of one browser tab keep open.
const LIVE_TILE_LIMIT_KEY = 'phenix.vnc.liveTileLimit';

/** The choices of the "Live tile limit" option. */
export const LIVE_TILE_LIMITS = [4, 8, 16, 24, 32];

/** The live tile limit when the browser keeps no valid choice. */
export const DEFAULT_LIVE_TILE_LIMIT = 16;

/**
 * The live tile limit that this browser keeps. A stored value that is not one
 * of LIVE_TILE_LIMITS gives DEFAULT_LIVE_TILE_LIMIT.
 *
 * @returns {number}
 */
export function loadLiveTileLimit() {
  const stored = readPref(LIVE_TILE_LIMIT_KEY);
  return (
    LIVE_TILE_LIMITS.find((limit) => String(limit) === stored) ??
    DEFAULT_LIVE_TILE_LIMIT
  );
}

/**
 * Keeps the live tile limit. A value that is not one of LIVE_TILE_LIMITS is
 * not kept.
 *
 * @param {number} limit
 */
export function saveLiveTileLimit(limit) {
  if (LIVE_TILE_LIMITS.includes(limit)) writePref(LIVE_TILE_LIMIT_KEY, limit);
}

// The settings of the VNC console page, for each user of this browser.
const vncSettingsKey = (username) => `phenix.vnc.settings.${username ?? ''}`;

const VNC_SCALINGS = ['fit', 'actual', 'remote'];

/**
 * The console settings when the browser keeps none: Fit, and the noVNC
 * defaults for quality and compression.
 */
export const DEFAULT_VNC_SETTINGS = Object.freeze({
  scaling: 'fit',
  quality: 6,
  compression: 2,
});

const level = (value) =>
  Number.isInteger(value) && value >= 0 && value <= 9 ? value : null;

/**
 * The console settings that this browser keeps for the user. A value that is
 * not valid gives its default.
 *
 * @param {string} username
 * @returns {{ scaling: 'fit'|'actual'|'remote', quality: number,
 *   compression: number }}
 */
export function loadVncSettings(username) {
  let stored;
  try {
    stored = JSON.parse(readPref(vncSettingsKey(username)) ?? 'null');
  } catch {
    stored = null;
  }
  if (!stored || typeof stored !== 'object' || Array.isArray(stored)) {
    stored = {};
  }
  return {
    scaling: VNC_SCALINGS.includes(stored.scaling)
      ? stored.scaling
      : DEFAULT_VNC_SETTINGS.scaling,
    quality: level(stored.quality) ?? DEFAULT_VNC_SETTINGS.quality,
    compression: level(stored.compression) ?? DEFAULT_VNC_SETTINGS.compression,
  };
}

/**
 * Keeps the console settings of the user.
 *
 * @param {string} username
 * @param {{ scaling: string, quality: number, compression: number }} settings
 */
export function saveVncSettings(username, { scaling, quality, compression }) {
  writePref(
    vncSettingsKey(username),
    JSON.stringify({ scaling, quality, compression }),
  );
}

// The console page hides its top bar, for each browser.
const VNC_TOOLBAR_HIDDEN_KEY = 'phenix.vnc.toolbarHidden';

/**
 * Whether this browser keeps the top bar of the console page hidden.
 *
 * @returns {boolean}
 */
export function loadVncToolbarHidden() {
  return readPref(VNC_TOOLBAR_HIDDEN_KEY) === 'true';
}

/**
 * Keeps whether the top bar of the console page is hidden.
 *
 * @param {boolean} hidden
 */
export function saveVncToolbarHidden(hidden) {
  writePref(VNC_TOOLBAR_HIDDEN_KEY, Boolean(hidden));
}
