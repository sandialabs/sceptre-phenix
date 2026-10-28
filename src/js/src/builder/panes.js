// Widths of the editor's side columns (BuilderPanes.vue) in CSS pixels. The
// limits are in rem, so they grow with enlarged text and leave the canvas room.
// localStorage phenix.builder.panes keeps only the sides a viewer resized,
// widened or hid: {"end": 900, "widened": {"end": 352}, "hidden": ["end"]}.

import { pageStorage } from './storage.js';

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

// The widths a side column may take beside the other side, at its width or
// as a strip while hidden. max is never below min.
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

// The width a key press on a splitter gives its column (APG window splitter).
// The arrow keys move the splitter, so Left narrows start and widens end.
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

function viewerStorage(storage) {
  return storage === undefined ? pageStorage() : storage;
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

// The widths, widened sides and hidden sides this viewer chose. Without
// storage, the default widths apply and both sides show. A widened side's null
// width restores its default width.
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

// Stores the widths and hidden sides this viewer chose. A side without a width
// takes the default width again.
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

// The minimap keeps Vue Flow's 4:3 shape, and a handle at its top left resizes
// it. phenix.builder.minimap keeps the width a viewer chose. Without one, the
// minimap has Vue Flow's size, which Reset view restores.

export const MINIMAP_STORAGE_KEY = 'phenix.builder.minimap';
export const MINIMAP_DEFAULT_WIDTH = 200;
const MINIMAP_RATIO = 3 / 4;
const MINIMAP_MIN_WIDTH = 120;
const MINIMAP_MAX_WIDTH = 600;
// The most of the canvas's width, and of its height, it may cover.
const MINIMAP_MAX_SHARE = 0.5;
// Vue Flow keeps the minimap 15px in from the canvas's edges, inside a 1px
// frame. The same space above it keeps its handle, at its top left corner,
// and the handle's focus ring on the canvas.
const MINIMAP_ROOM = 2 * (15 + 1);
// One arrow key press.
const MINIMAP_STEP = 16;

// The widths the minimap may take: at most half the canvas's width and height,
// but not below the default unless the canvas is too small to hold it.
export function minimapLimits(pane) {
  const width = pane?.width || 0;
  const height = pane?.height || 0;
  const share = Math.min(width, height / MINIMAP_RATIO) * MINIMAP_MAX_SHARE;
  // A canvas not yet measured sets no bound.
  const fits =
    width && height
      ? Math.min(width - MINIMAP_ROOM, (height - MINIMAP_ROOM) / MINIMAP_RATIO)
      : Infinity;

  return {
    min: MINIMAP_MIN_WIDTH,
    max: Math.max(
      MINIMAP_MIN_WIDTH,
      Math.floor(
        Math.min(
          MINIMAP_MAX_WIDTH,
          fits,
          Math.max(MINIMAP_DEFAULT_WIDTH, share),
        ),
      ),
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

// The width a key press on the minimap's handle gives it. Up and Left make it
// larger, as they move the top left handle out. Home and End give the limits.
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

// The width a drag of the minimap's handle gives it. The minimap keeps its
// shape, so its corner goes to the allowed point nearest the pointer.
export function minimapDragWidth(width, dx, dy, limits) {
  const across = width - dx;
  const down = width * MINIMAP_RATIO - dy;

  return clampPane(
    (across + MINIMAP_RATIO * down) / (1 + MINIMAP_RATIO ** 2),
    limits,
  );
}

/** The minimap width this viewer chose, or null for the default. */
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

/** Stores the minimap width this viewer chose. null forgets it. */
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
