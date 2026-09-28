// Widths of the editor's side columns: Add nodes and the Outline at the start,
// the Inspector at the end. The splitters beside the canvas resize them, a
// Widen toggle at each splitter makes its column as wide as it can be, and a
// Hide toggle below it folds the column into a narrow strip (see
// BuilderPanes.vue). Widths are CSS pixels; the limits are in rem, so they
// grow with enlarged text, and always leave the canvas a usable width.
//
// A viewer's widths are kept in localStorage under phenix.builder.panes, per
// browser. Only a side the viewer has resized is stored; the other keeps the
// layout's default width for the window size. A side widened with its toggle
// also keeps the width the toggle restores: {"end": 900, "widened": {"end":
// 352}}, or null there for the default width. Hidden sides are listed:
// {"hidden": ["end"]}.
//
// The minimap's size is kept apart, under phenix.builder.minimap (see the
// end of this file).

export const PANES_STORAGE_KEY = 'phenix.builder.panes';
const PANE_SIDES = ['start', 'end'];

// The narrowest each side column may be, and the canvas between them.
const PANE_MIN_REM = { start: 11, end: 15 };
const CANVAS_MIN_REM = 20;
// The splitter between two columns, which is also the gap between them.
const SPLITTER_REM = 0.75;
// The strip a hidden column folds into, in place of its splitter. It
// matches builder.css.
const STRIP_REM = 2.25;
// One arrow key press.
export const PANE_STEP_REM = 1;

/**
 * The widths a side column may take, beside the other side at its width, or
 * beside the other side's strip while that side is hidden.
 *
 * @param {'start'|'end'} side
 * @param {{layout: number, other: number, rem: number, otherHidden?:
 *   boolean}} context the whole layout's width, the other side column's
 *   width and the root font size, all in pixels, and whether the other side
 *   is hidden
 * @returns {{min: number, max: number}} whole pixels, max never below min
 */
export function paneLimits(side, { layout, other, rem, otherHidden = false }) {
  const min = Math.round(PANE_MIN_REM[side] * rem);
  const gutters = SPLITTER_REM + (otherHidden ? STRIP_REM : SPLITTER_REM);
  const room =
    layout - (otherHidden ? 0 : other) - (CANVAS_MIN_REM + gutters) * rem;

  return { min, max: Math.max(min, Math.floor(room)) };
}

/**
 * @param {number} width
 * @param {{min: number, max: number}} limits
 * @returns {number} the width within the limits, in whole pixels
 */
export function clampPane(width, limits) {
  return Math.round(Math.min(limits.max, Math.max(limits.min, width)));
}

/**
 * The width a key press on a splitter gives its side column, as the APG
 * window splitter pattern has it: the arrow keys move the splitter itself,
 * so Left Arrow narrows the start column and widens the end column; Home and
 * End give the column its smallest and largest width.
 *
 * @param {'start'|'end'} side
 * @param {string} key KeyboardEvent.key
 * @param {number} width current width
 * @param {{min: number, max: number}} limits
 * @param {number} step pixels per arrow key press
 * @returns {number|undefined} undefined for a key the splitter leaves alone
 */
export function paneKeyWidth(side, key, width, limits, step) {
  const toEnd = side === 'start' ? step : -step;

  switch (key) {
    case 'ArrowLeft':
      return clampPane(width - toEnd, limits);
    case 'ArrowRight':
      return clampPane(width + toEnd, limits);
    case 'Home':
      return limits.min;
    case 'End':
      return limits.max;
    default:
      return undefined;
  }
}

// The storage itself can be out of reach: reading window.localStorage throws
// where the browser blocks site data.
function viewerStorage(storage) {
  return storage === undefined ? globalThis.localStorage : storage;
}

function isWidth(value) {
  return Number.isFinite(value) && value > 0;
}

// The sides widened with their toggle, each with the width it goes back to
// (null for the default width). Only a side with a width of its own can be
// widened.
function widenedOf(widened, widths) {
  return Object.fromEntries(
    PANE_SIDES.filter(
      (side) =>
        isWidth(widths[side]) &&
        widened &&
        Object.hasOwn(widened, side) &&
        (widened[side] === null || isWidth(widened[side])),
    ).map((side) => [
      side,
      widened[side] === null ? null : Math.round(widened[side]),
    ]),
  );
}

// The sides hidden with their toggle, in order, from a list or set of them.
function hiddenOf(hidden) {
  const sides = new Set(Array.isArray(hidden) ? hidden : [...(hidden || [])]);

  return PANE_SIDES.filter((side) => sides.has(side));
}

/**
 * The widths this viewer chose, the sides widened with their toggle, and
 * the sides hidden. Nothing is stored in a private window, with site data
 * blocked or cleared, or before a splitter is first moved: the layout's
 * default widths then apply, and both sides show.
 *
 * @param {Storage|null} [storage] localStorage by default
 * @returns {{widths: {start?: number, end?: number},
 *   widened: {start?: number|null, end?: number|null},
 *   hidden: string[]}} pixels; a widened side's width to restore, or null
 *   for its default width; the hidden sides
 */
export function loadPanes(storage) {
  try {
    const saved = JSON.parse(
      viewerStorage(storage)?.getItem(PANES_STORAGE_KEY) || '{}',
    );
    const widths = Object.fromEntries(
      PANE_SIDES.filter((side) => isWidth(saved?.[side])).map((side) => [
        side,
        Math.round(saved[side]),
      ]),
    );

    return {
      widths,
      widened: widenedOf(saved?.widened, widths),
      hidden: Array.isArray(saved?.hidden) ? hiddenOf(saved.hidden) : [],
    };
  } catch {
    return { widths: {}, widened: {}, hidden: [] };
  }
}

/**
 * Keeps the widths this viewer chose, and the sides hidden; a side without
 * a width is left out, and takes the default width again.
 *
 * @param {{widths: {start?: number, end?: number},
 *   widened?: {start?: number|null, end?: number|null},
 *   hidden?: Iterable<string>}} panes as loadPanes returns them
 * @param {Storage|null} [storage] localStorage by default
 */
export function savePanes({ widths, widened, hidden } = {}, storage) {
  const kept = Object.fromEntries(
    PANE_SIDES.filter((side) => Number.isFinite(widths?.[side])).map((side) => [
      side,
      Math.round(widths[side]),
    ]),
  );
  const toggled = widenedOf(widened, kept);
  const folded = hiddenOf(hidden);

  if (Object.keys(toggled).length) {
    kept.widened = toggled;
  }

  if (folded.length) {
    kept.hidden = folded;
  }

  try {
    const target = viewerStorage(storage);

    if (Object.keys(kept).length) {
      target?.setItem(PANES_STORAGE_KEY, JSON.stringify(kept));
    } else {
      target?.removeItem(PANES_STORAGE_KEY);
    }
  } catch {
    // The widths still apply until the Builder is left.
  }
}

// --- the minimap ---------------------------------------------------------------

// The canvas's minimap, at its bottom right, keeps Vue Flow's shape: 4 wide
// to 3 high. A handle at its top left corner resizes it (see
// BuilderCanvas.vue). Sizes are CSS pixels, given by the minimap's width.
//
// The width a viewer chose is kept in localStorage under
// phenix.builder.minimap, per browser: {"width": 280}. Without one, the
// minimap has Vue Flow's own size, which Reset view puts back.

export const MINIMAP_STORAGE_KEY = 'phenix.builder.minimap';
export const MINIMAP_DEFAULT_WIDTH = 200;
const MINIMAP_RATIO = 3 / 4;
const MINIMAP_MIN_WIDTH = 120;
const MINIMAP_MAX_WIDTH = 600;
// The most of the canvas's width, and of its height, it may cover.
const MINIMAP_MAX_SHARE = 0.5;
// One arrow key press.
export const MINIMAP_STEP = 16;

/**
 * The widths the minimap may take on a canvas: at most half the canvas's
 * width and half its height, but never less than the default.
 *
 * @param {{width: number, height: number}} pane the canvas, in pixels
 * @returns {{min: number, max: number}} whole pixels
 */
export function minimapLimits(pane) {
  const share =
    Math.min(pane?.width || 0, (pane?.height || 0) / MINIMAP_RATIO) *
    MINIMAP_MAX_SHARE;

  return {
    min: MINIMAP_MIN_WIDTH,
    max: Math.min(
      MINIMAP_MAX_WIDTH,
      Math.max(MINIMAP_DEFAULT_WIDTH, Math.floor(share)),
    ),
  };
}

/**
 * @param {number} width
 * @returns {{width: number, height: number}} the minimap's size at that
 *   width, in whole pixels
 */
export function minimapSize(width) {
  return { width, height: Math.round(width * MINIMAP_RATIO) };
}

/**
 * The width a key press on the minimap's handle gives it. The arrow keys
 * move the handle, at the minimap's top left corner, as they move a
 * splitter: Up and Left make the minimap larger, Down and Right smaller.
 * Home and End give it its smallest and largest width.
 *
 * @param {string} key KeyboardEvent.key
 * @param {number} width current width
 * @param {{min: number, max: number}} limits
 * @param {number} [step] pixels per arrow key press
 * @returns {number|undefined} undefined for a key the handle leaves alone
 */
export function minimapKeyWidth(key, width, limits, step = MINIMAP_STEP) {
  switch (key) {
    case 'ArrowUp':
    case 'ArrowLeft':
      return clampPane(width + step, limits);
    case 'ArrowDown':
    case 'ArrowRight':
      return clampPane(width - step, limits);
    case 'Home':
      return limits.min;
    case 'End':
      return limits.max;
    default:
      return undefined;
  }
}

/**
 * The width a drag of the minimap's handle gives it. The minimap keeps its
 * shape, so its corner goes to the point nearest the pointer that the shape
 * allows.
 *
 * @param {number} width the width when the drag began
 * @param {number} dx how far the pointer has moved right, in pixels
 * @param {number} dy how far the pointer has moved down, in pixels
 * @param {{min: number, max: number}} limits
 * @returns {number}
 */
export function minimapDragWidth(width, dx, dy, limits) {
  const across = width - dx;
  const down = width * MINIMAP_RATIO - dy;

  return clampPane(
    (across + MINIMAP_RATIO * down) / (1 + MINIMAP_RATIO ** 2),
    limits,
  );
}

/**
 * The minimap width this viewer chose, or null for the default.
 *
 * @param {Storage|null} [storage] localStorage by default
 * @returns {number|null}
 */
export function loadMinimap(storage) {
  try {
    const saved = JSON.parse(
      viewerStorage(storage)?.getItem(MINIMAP_STORAGE_KEY) || '{}',
    );

    return isWidth(saved?.width) ? Math.round(saved.width) : null;
  } catch {
    return null;
  }
}

/**
 * Keeps the minimap width this viewer chose; null forgets it.
 *
 * @param {number|null} width
 * @param {Storage|null} [storage] localStorage by default
 */
export function saveMinimap(width, storage) {
  try {
    const target = viewerStorage(storage);

    if (isWidth(width)) {
      target?.setItem(
        MINIMAP_STORAGE_KEY,
        JSON.stringify({ width: Math.round(width) }),
      );
    } else {
      target?.removeItem(MINIMAP_STORAGE_KEY);
    }
  } catch {
    // The size still applies until the Builder is left.
  }
}
