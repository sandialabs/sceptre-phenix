// Widths of the editor's side columns: Add nodes and the Outline at the start,
// the Inspector at the end. The splitters beside the canvas resize them, and
// a Widen toggle at each splitter makes its column as wide as it can be (see
// BuilderPanes.vue). Widths are CSS pixels; the limits are in rem, so they
// grow with enlarged text, and always leave the canvas a usable width.
//
// A viewer's widths are kept in localStorage under phenix.builder.panes, per
// browser. Only a side the viewer has resized is stored; the other keeps the
// layout's default width for the window size. A side widened with its toggle
// also keeps the width the toggle restores: {"end": 900, "widened": {"end":
// 352}}, or null there for the default width.

export const PANES_STORAGE_KEY = 'phenix.builder.panes';
const PANE_SIDES = ['start', 'end'];

// The narrowest each side column may be, and the canvas between them.
const PANE_MIN_REM = { start: 11, end: 15 };
const CANVAS_MIN_REM = 20;
// The splitter between two columns, which is also the gap between them.
const SPLITTER_REM = 0.75;
// One arrow key press.
export const PANE_STEP_REM = 1;

/**
 * The widths a side column may take, beside the other side at its width.
 *
 * @param {'start'|'end'} side
 * @param {{layout: number, other: number, rem: number}} context the whole
 *   layout's width, the other side column's width and the root font size,
 *   all in pixels
 * @returns {{min: number, max: number}} whole pixels, max never below min
 */
export function paneLimits(side, { layout, other, rem }) {
  const min = Math.round(PANE_MIN_REM[side] * rem);
  const room = layout - other - (CANVAS_MIN_REM + 2 * SPLITTER_REM) * rem;

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

/**
 * The widths this viewer chose, and the sides widened with their toggle.
 * Nothing is stored in a private window, with site data blocked or cleared,
 * or before a splitter is first moved: the layout's default widths then
 * apply.
 *
 * @param {Storage|null} [storage] localStorage by default
 * @returns {{widths: {start?: number, end?: number},
 *   widened: {start?: number|null, end?: number|null}}} pixels; a widened
 *   side's width to restore, or null for its default width
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

    return { widths, widened: widenedOf(saved?.widened, widths) };
  } catch {
    return { widths: {}, widened: {} };
  }
}

/**
 * Keeps the widths this viewer chose; a side without one is left out, and
 * takes the default width again.
 *
 * @param {{widths: {start?: number, end?: number},
 *   widened?: {start?: number|null, end?: number|null}}} panes as loadPanes
 *   returns them
 * @param {Storage|null} [storage] localStorage by default
 */
export function savePanes({ widths, widened } = {}, storage) {
  const kept = Object.fromEntries(
    PANE_SIDES.filter((side) => Number.isFinite(widths?.[side])).map((side) => [
      side,
      Math.round(widths[side]),
    ]),
  );
  const toggled = widenedOf(widened, kept);

  if (Object.keys(toggled).length) {
    kept.widened = toggled;
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
