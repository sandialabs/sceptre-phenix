// Ticks and numbers of the canvas's coordinate rulers for a pan and a zoom. A
// ruler counts in the canvas pixels of the Inspector's Position. A value v is
// at v * zoom + offset pixels, where offset is the pan of that axis.

// The least pixels between two numbered ticks, for a font of 16 pixels.
export const RULER_SPACING = 64;

// At most this many numbered ticks on one ruler, whatever the zoom and pane.
const MOST_TICKS = 200;

// The step between numbered ticks in canvas pixels: the least 1, 2 or 5 times
// a power of ten whose ticks are at least `spacing` pixels apart at `zoom`.
export function rulerStep(zoom, spacing = RULER_SPACING) {
  const least = spacing / (zoom > 0 ? zoom : 1);
  const power = 10 ** Math.floor(Math.log10(least));

  for (const factor of [1, 2, 5]) {
    if (factor * power >= least * (1 - 1e-9)) {
      return factor * power;
    }
  }

  return 10 * power;
}

/**
 * @param {number} step a step of rulerStep
 * @returns {number} the step between small ticks
 */
export function rulerMinorStep(step) {
  const factor = Math.round(step / 10 ** Math.floor(Math.log10(step)));

  return step / (factor === 2 ? 4 : 5);
}

// Formats a ruler number as the Position fields show it: a whole number, with
// a hyphen-minus for a negative one.
export function rulerLabel(value) {
  const rounded = Math.round(value);

  return String(Object.is(rounded, -0) ? 0 : rounded);
}

// The ticks of one ruler: the numbered ticks (`at` in pixels from its start),
// and the small ticks every `minorSize` pixels from `minorOffset`. Small ticks
// mark a fifth of the step (a quarter of a step of 2).
export function rulerTicks({ offset, zoom, length, spacing = RULER_SPACING }) {
  const scale = zoom > 0 ? zoom : 1;
  const step = rulerStep(scale, spacing);
  const minorSize = rulerMinorStep(step) * scale;
  const ticks = [];

  if (length > 0 && Number.isFinite(offset)) {
    const first = Math.ceil(-offset / scale / step);
    const last = Math.floor((length - offset) / scale / step);

    for (let index = first; index <= last; index += 1) {
      if (ticks.length >= MOST_TICKS) {
        break;
      }

      // Never -0, which Math.ceil gives for a small pan.
      const value = index * step || 0;

      ticks.push({
        value,
        at: value * scale + offset,
        label: rulerLabel(value),
      });
    }
  }

  return {
    step,
    ticks,
    minorSize,
    minorOffset: Number.isFinite(offset)
      ? ((offset % minorSize) + minorSize) % minorSize
      : 0,
  };
}

// The extent of some boxes (the selected nodes) along each axis, or null. A box
// without a width or a height (not measured yet) counts for nothing.
export function rulerExtent(boxes) {
  let extent = null;

  for (const { x, y, width, height } of boxes) {
    if (!(width > 0) || !(height > 0)) {
      continue;
    }

    extent = {
      x: {
        start: Math.min(extent?.x.start ?? x, x),
        end: Math.max(extent?.x.end ?? x + width, x + width),
      },
      y: {
        start: Math.min(extent?.y.start ?? y, y),
        end: Math.max(extent?.y.end ?? y + height, y + height),
      },
    };
  }

  return extent;
}

// Where a span of canvas pixels (a selected node's extent) is on a ruler, in
// pixels from its start, or null for no span.
export function rulerSpan(span, offset, zoom) {
  if (!span || !(span.end >= span.start)) {
    return null;
  }

  return {
    at: span.start * zoom + offset,
    size: (span.end - span.start) * zoom,
  };
}
