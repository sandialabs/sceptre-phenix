// Text boxes that fit their text (the v-fit-text directive): the Inspector's
// multi-line fields, such as Description, and the diagram's notes.
//
// A box is two lines tall while its text is short (its `rows`). It grows to
// fit the text typed in it, up to MAX_LINES lines, and then scrolls. The
// user can drag it taller (CSS `resize: vertical`), and a box dragged
// taller keeps that height while its text fits in it. A box fits its text
// again when its value changes, as it is typed or loaded, and when its
// width changes, which wraps the text again. A box that is not drawn, such
// as one in a closed section, is fitted when it is drawn.

// The most lines a box grows to before it scrolls.
export const MAX_LINES = 8;

/**
 * The height of a box that fits its text.
 *
 * @param {object} sizes in pixels
 * @param {number} sizes.content the height its text takes, with its padding
 *   and border, and at least that of its rows
 * @param {number} sizes.most the height of MAX_LINES lines, with its padding
 *   and border
 * @param {number} [sizes.dragged] the height the user dragged it to, if any
 * @returns {number}
 */
export function fittedHeight({ content, most, dragged = 0 }) {
  return Math.max(Math.min(content, most), dragged);
}

// For each box: the height this module gave it last, the height the user
// dragged it to, and the value it was fitted to.
const boxes = new WeakMap();

function pixels(value) {
  return Number.parseFloat(value) || 0;
}

// The elements around a box that are scrolled, with their scroll offsets.
// A box made `auto` tall for a moment can shorten the page, which would
// move them.
function scrolledAround(element) {
  const scrolled = [];

  for (let at = element.parentElement; at; at = at.parentElement) {
    if (at.scrollTop) {
      scrolled.push([at, at.scrollTop]);
    }
  }

  return scrolled;
}

export function fitText(element) {
  // A box that is not drawn has no height to measure.
  if (!element?.isConnected || element.getClientRects().length === 0) {
    return;
  }

  const state = boxes.get(element) || { set: '', dragged: 0 };

  // A height other than the one given last is one the user dragged to.
  if (state.set && element.style.height && element.style.height !== state.set) {
    state.dragged = element.getBoundingClientRect().height;
  }

  const style = getComputedStyle(element);
  const frame =
    pixels(style.borderTopWidth) +
    pixels(style.borderBottomWidth) +
    pixels(style.paddingTop) +
    pixels(style.paddingBottom);
  const line =
    pixels(style.lineHeight) || Math.round(pixels(style.fontSize) * 1.5);
  const scrolled = scrolledAround(element);

  element.style.height = 'auto';

  // scrollHeight has the padding but not the border.
  const height = fittedHeight({
    content:
      element.scrollHeight +
      pixels(style.borderTopWidth) +
      pixels(style.borderBottomWidth),
    most: line * MAX_LINES + frame,
    dragged: state.dragged,
  });

  element.style.height = `${Math.ceil(height)}px`;
  scrolled.forEach(([at, top]) => {
    at.scrollTop = top;
  });
  state.set = element.style.height;
  state.value = element.value;
  boxes.set(element, state);
}

// Each box's width, as last fitted, and what watches it.
const widths = new WeakMap();
let resizing = null;

function watchWidth(element) {
  if (typeof ResizeObserver === 'undefined') {
    return;
  }

  resizing ??= new ResizeObserver((entries) => {
    entries.forEach(({ target, contentRect }) => {
      // A height change is the box's own: fitted, or dragged.
      if (widths.get(target) !== contentRect.width) {
        widths.set(target, contentRect.width);
        fitText(target);
      }
    });
  });
  resizing.observe(element);
}

const onInput = (event) => fitText(event.target);

export const vFitText = {
  mounted(element) {
    element.addEventListener('input', onInput);
    watchWidth(element);
    fitText(element);
  },
  // A value loaded into the box, such as after Cancel or Undo. Other
  // updates of the component, and the update after an input that was
  // fitted already, leave the value as fitted and need no new layout.
  updated(element) {
    if (boxes.get(element)?.value !== element.value) {
      fitText(element);
    }
  },
  beforeUnmount(element) {
    element.removeEventListener('input', onInput);
    resizing?.unobserve(element);
  },
};
