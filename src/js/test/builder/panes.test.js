import { describe, expect, test } from 'vitest';

import {
  PANES_STORAGE_KEY,
  clampPane,
  loadPanes,
  paneKeyWidth,
  paneLimits,
  savePanes,
} from '@/builder/panes.js';

function fakeStorage(initial = {}) {
  const data = { ...initial };

  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    },
    removeItem: (key) => {
      delete data[key];
    },
  };
}

const throwing = {
  getItem() {
    throw new Error('blocked');
  },
  setItem() {
    throw new Error('blocked');
  },
  removeItem() {
    throw new Error('blocked');
  },
};

describe('side column widths', () => {
  test('leave the canvas 20rem beside the other column', () => {
    // 1400px layout, 16px rem: 1400 - 352 - (20 + 1.5) * 16 = 704.
    expect(paneLimits('start', { layout: 1400, other: 352, rem: 16 })).toEqual({
      min: 176,
      max: 704,
    });
    expect(paneLimits('end', { layout: 1400, other: 240, rem: 16 })).toEqual({
      min: 240,
      max: 816,
    });
    // Enlarged text raises the limits with it.
    expect(paneLimits('end', { layout: 1400, other: 240, rem: 32 })).toEqual({
      min: 480,
      max: 480,
    });
  });

  test('never offer a maximum below the minimum', () => {
    expect(paneLimits('end', { layout: 600, other: 240, rem: 16 })).toEqual({
      min: 240,
      max: 240,
    });
  });

  test('clamp to whole pixels within the limits', () => {
    const limits = { min: 176, max: 704 };

    expect(clampPane(100, limits)).toBe(176);
    expect(clampPane(900, limits)).toBe(704);
    expect(clampPane(300.4, limits)).toBe(300);
  });
});

describe('splitter keys', () => {
  const limits = { min: 176, max: 704 };

  test('arrow keys move the splitter itself', () => {
    // Right Arrow moves the splitter right: a start column widens, an end
    // column narrows.
    expect(paneKeyWidth('start', 'ArrowRight', 240, limits, 16)).toBe(256);
    expect(paneKeyWidth('start', 'ArrowLeft', 240, limits, 16)).toBe(224);
    expect(paneKeyWidth('end', 'ArrowLeft', 352, limits, 16)).toBe(368);
    expect(paneKeyWidth('end', 'ArrowRight', 352, limits, 16)).toBe(336);
  });

  test('stop at the limits, which Home and End reach at once', () => {
    expect(paneKeyWidth('start', 'ArrowLeft', 180, limits, 16)).toBe(176);
    expect(paneKeyWidth('end', 'ArrowLeft', 700, limits, 16)).toBe(704);
    expect(paneKeyWidth('start', 'Home', 400, limits, 16)).toBe(176);
    expect(paneKeyWidth('end', 'End', 400, limits, 16)).toBe(704);
  });

  test('leave other keys alone', () => {
    for (const key of ['ArrowUp', 'ArrowDown', 'Tab', 'a']) {
      expect(paneKeyWidth('start', key, 240, limits, 16)).toBeUndefined();
    }
  });
});

describe('remembered widths', () => {
  const none = { widths: {}, widened: {} };

  test('round-trip through storage under their own key', () => {
    const storage = fakeStorage();

    savePanes({ widths: { start: 300.2, end: 500 } }, storage);
    expect(JSON.parse(storage.data[PANES_STORAGE_KEY])).toEqual({
      start: 300,
      end: 500,
    });
    expect(loadPanes(storage)).toEqual({
      widths: { start: 300, end: 500 },
      widened: {},
    });
  });

  test('a side restored to its default is left out, and none removes the key', () => {
    const storage = fakeStorage();

    savePanes({ widths: { start: 300, end: 500 } }, storage);
    savePanes({ widths: { end: 500 } }, storage);
    expect(loadPanes(storage).widths).toEqual({ end: 500 });
    savePanes({ widths: {} }, storage);
    expect(storage.data).not.toHaveProperty(PANES_STORAGE_KEY);
  });

  test('a widened side keeps the width its toggle restores, or null for the default', () => {
    const storage = fakeStorage();
    const panes = {
      widths: { start: 700, end: 824 },
      widened: { start: 240.4, end: null },
    };

    savePanes(panes, storage);
    expect(JSON.parse(storage.data[PANES_STORAGE_KEY])).toEqual({
      start: 700,
      end: 824,
      widened: { start: 240, end: null },
    });
    expect(loadPanes(storage)).toEqual({
      widths: { start: 700, end: 824 },
      widened: { start: 240, end: null },
    });

    // No longer widened, so nothing is kept for it.
    savePanes({ widths: { end: 808 }, widened: {} }, storage);
    expect(JSON.parse(storage.data[PANES_STORAGE_KEY])).toEqual({ end: 808 });
  });

  test('ignore what is not a width', () => {
    const storage = fakeStorage({
      [PANES_STORAGE_KEY]: JSON.stringify({
        start: 'wide',
        end: -4,
        other: 300,
      }),
    });

    expect(loadPanes(storage)).toEqual(none);
    expect(
      loadPanes(fakeStorage({ [PANES_STORAGE_KEY]: '{not json' })),
    ).toEqual(none);
    // A side is widened only with a width of its own, and back to a width
    // or the default.
    expect(
      loadPanes(
        fakeStorage({
          [PANES_STORAGE_KEY]: JSON.stringify({
            end: 824,
            widened: { start: 240, end: 'narrow' },
          }),
        }),
      ),
    ).toEqual({ widths: { end: 824 }, widened: {} });
  });

  test('missing or blocked storage falls back to the defaults', () => {
    expect(loadPanes(null)).toEqual(none);
    expect(loadPanes(throwing)).toEqual(none);
    expect(() => savePanes({ widths: { start: 300 } }, throwing)).not.toThrow();
    expect(() => savePanes({ widths: { start: 300 } }, null)).not.toThrow();
  });
});
