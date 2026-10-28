// The canvas's coordinate rulers: the step between numbered ticks for a
// zoom, where the ticks are for a pan, and the selected nodes' extent.

import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  RULER_SPACING,
  rulerExtent,
  rulerLabel,
  rulerMinorStep,
  rulerSpan,
  rulerStep,
  rulerTicks,
} from '@/builder/rulers.js';

describe('rulerStep', () => {
  test('is the least 1, 2 or 5 times a power of ten that keeps the numbers apart', () => {
    expect(RULER_SPACING).toBe(64);
    // [zoom, step]: 64 pixels are 32 canvas pixels at 200%, 640 at 10%.
    for (const [zoom, step] of [
      [2, 50],
      [1.5, 50],
      [1.28, 50],
      [1.2, 100],
      [1, 100],
      [0.64, 100],
      [0.5, 200],
      [0.32, 200],
      [0.25, 500],
      [0.2, 500],
      [0.1, 1000],
      [0.05, 2000],
    ]) {
      expect(rulerStep(zoom), `zoom ${zoom}`).toBe(step);
      expect(step * zoom, `zoom ${zoom}`).toBeGreaterThanOrEqual(RULER_SPACING);
    }
  });

  test('keeps a larger font apart too', () => {
    expect(rulerStep(1, 128)).toBe(200);
    expect(rulerStep(2, 8)).toBe(5);
    expect(rulerStep(10, 8)).toBe(1);
  });

  test('takes no zoom as 100%', () => {
    expect(rulerStep(0)).toBe(100);
    expect(rulerStep(-1)).toBe(100);
  });
});

test('small ticks mark fifths, or quarters of a step of 2', () => {
  expect(rulerMinorStep(50)).toBe(10);
  expect(rulerMinorStep(100)).toBe(20);
  expect(rulerMinorStep(200)).toBe(50);
  expect(rulerMinorStep(500)).toBe(100);
  expect(rulerMinorStep(2000)).toBe(500);
});

test('numbers are whole, as Position shows them', () => {
  expect(rulerLabel(0)).toBe('0');
  expect(rulerLabel(-0)).toBe('0');
  expect(rulerLabel(-200)).toBe('-200');
  expect(rulerLabel(1200.0000001)).toBe('1200');
});

describe('rulerTicks', () => {
  test('at 100% from the origin, a number every 100 pixels', () => {
    const ruler = rulerTicks({ offset: 0, zoom: 1, length: 350 });

    expect(ruler.step).toBe(100);
    expect(ruler.ticks).toEqual([
      { value: 0, at: 0, label: '0' },
      { value: 100, at: 100, label: '100' },
      { value: 200, at: 200, label: '200' },
      { value: 300, at: 300, label: '300' },
    ]);
    expect(ruler.minorSize).toBe(20);
    expect(ruler.minorOffset).toBe(0);
  });

  test('follow a pan: canvas pixel v is at v * zoom + offset', () => {
    // Panned right by 130 pixels: 0 is 130 pixels in, and -100 30.
    const right = rulerTicks({ offset: 130, zoom: 1, length: 300 });
    expect(right.ticks.map((tick) => [tick.value, tick.at])).toEqual([
      [-100, 30],
      [0, 130],
      [100, 230],
    ]);
    expect(right.minorOffset).toBe(10);

    // Panned left: the first number in view is 300.
    const left = rulerTicks({ offset: -250, zoom: 1, length: 200 });
    expect(left.ticks.map((tick) => [tick.value, tick.at])).toEqual([
      [300, 50],
      [400, 150],
    ]);
    expect(left.minorOffset).toBe(10);
  });

  test('follow a zoom', () => {
    const ruler = rulerTicks({ offset: 40, zoom: 0.5, length: 500 });

    expect(ruler.step).toBe(200);
    expect(ruler.ticks.map((tick) => [tick.value, tick.at])).toEqual([
      [0, 40],
      [200, 140],
      [400, 240],
      [600, 340],
      [800, 440],
    ]);
    expect(ruler.minorSize).toBe(25);
    expect(ruler.minorOffset).toBe(15);
  });

  test('numbered ticks are at least the spacing apart at every zoom', () => {
    for (const zoom of [0.05, 0.13, 0.2, 0.37, 0.5, 0.99, 1, 1.7, 2]) {
      const { ticks } = rulerTicks({ offset: 17, zoom, length: 4000 });

      for (let index = 1; index < ticks.length; index += 1) {
        expect(
          ticks[index].at - ticks[index - 1].at,
          `zoom ${zoom}`,
        ).toBeGreaterThanOrEqual(RULER_SPACING - 1e-9);
      }
    }
  });

  test('have no numbers for a pane with no size, and a bounded count', () => {
    expect(rulerTicks({ offset: 0, zoom: 1, length: 0 }).ticks).toEqual([]);
    expect(rulerTicks({ offset: NaN, zoom: 1, length: 100 }).ticks).toEqual([]);
    expect(
      rulerTicks({ offset: 0, zoom: 1, length: 1e7, spacing: 1 }).ticks,
    ).toHaveLength(200);
  });
});

test('rulerSpan puts a canvas span on the ruler', () => {
  expect(rulerSpan({ start: 100, end: 260 }, 30, 0.5)).toEqual({
    at: 80,
    size: 80,
  });
  expect(rulerSpan(null, 0, 1)).toBeNull();
  expect(rulerSpan({ start: 5, end: 1 }, 0, 1)).toBeNull();
});

describe('rulerExtent', () => {
  test('is null without a measured box', () => {
    expect(rulerExtent([])).toBeNull();
    expect(rulerExtent([{ x: 0, y: 0, width: 0, height: 40 }])).toBeNull();
    expect(rulerExtent([{ x: 0, y: 0 }])).toBeNull();
  });

  test('is one box, or the least extent that holds every measured box', () => {
    expect(rulerExtent([{ x: 10, y: -20, width: 120, height: 40 }])).toEqual({
      x: { start: 10, end: 130 },
      y: { start: -20, end: 20 },
    });
    expect(
      rulerExtent([
        { x: 10, y: -20, width: 120, height: 40 },
        { x: -50, y: 0, width: 0, height: 0 },
        { x: 100, y: 200, width: 60, height: 30 },
      ]),
    ).toEqual({ x: { start: 10, end: 160 }, y: { start: -20, end: 230 } });
  });
});

describe('rulers, for any zoom and pan', () => {
  const zoom = fc.double({ min: 0.05, max: 16, noNaN: true });

  test('the step keeps numbers apart and is 1, 2 or 5 times a power of ten', () => {
    fc.assert(
      fc.property(zoom, fc.integer({ min: 1, max: 400 }), (scale, spacing) => {
        const step = rulerStep(scale, spacing);
        const mantissa = step / 10 ** Math.floor(Math.log10(step));

        expect(step).toBeGreaterThanOrEqual((spacing / scale) * (1 - 1e-9));
        expect(
          [1, 2, 5].some((factor) => Math.abs(mantissa - factor) < 1e-9),
        ).toBe(true);
      }),
      { numRuns: 300 },
    );
  });

  test('the ticks are in order on the ruler, never -0, and the small ticks repeat from its start', () => {
    fc.assert(
      fc.property(
        fc.double({ min: -5000, max: 5000, noNaN: true }),
        zoom,
        fc.integer({ min: 0, max: 4000 }),
        (offset, scale, length) => {
          const { ticks, minorSize, minorOffset } = rulerTicks({
            offset,
            zoom: scale,
            length,
          });

          // Rounding may put the first or last tick a hair off the ruler.
          const wrong = ticks.filter(
            (tick, i) =>
              tick.at < -1e-6 ||
              tick.at > length + 1e-6 ||
              Object.is(tick.value, -0) ||
              tick.label === '-0' ||
              (i > 0 && tick.value <= ticks[i - 1].value),
          );

          expect(ticks.length).toBeLessThanOrEqual(200);
          expect(wrong).toEqual([]);
          expect(minorOffset).toBeGreaterThanOrEqual(0);
          expect(minorOffset).toBeLessThan(minorSize);
        },
      ),
      { numRuns: 300 },
    );
  });
});
