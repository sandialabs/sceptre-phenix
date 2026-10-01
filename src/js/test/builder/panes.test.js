import { describe, expect, test } from 'vitest';

import {
  MINIMAP_DEFAULT_WIDTH,
  MINIMAP_STORAGE_KEY,
  PANES_STORAGE_KEY,
  clampPane,
  loadMinimap,
  loadPanes,
  minimapDragWidth,
  minimapKeyWidth,
  minimapLimits,
  minimapSize,
  paneKeyWidth,
  paneLimits,
  saveMinimap,
  savePanes,
} from '@/builder/panes.js';

import { memoryStorage } from './fixtures.js';

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

  test('beside a hidden column, leave room for its strip instead', () => {
    // 1400 - (20 + 0.75 + 2.25) * 16 = 1032, whatever the hidden column's
    // width.
    for (const other of [0, 352]) {
      expect(
        paneLimits('start', {
          layout: 1400,
          other,
          rem: 16,
          otherHidden: true,
        }),
      ).toEqual({ min: 176, max: 1032 });
    }
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
  const none = { widths: {}, widened: {}, hidden: [] };

  test('round-trip through storage under their own key', () => {
    const storage = memoryStorage();

    savePanes({ widths: { start: 300.2, end: 500 } }, storage);
    expect(JSON.parse(storage.entries.get(PANES_STORAGE_KEY))).toEqual({
      start: 300,
      end: 500,
    });
    expect(loadPanes(storage)).toEqual({
      widths: { start: 300, end: 500 },
      widened: {},
      hidden: [],
    });
  });

  test('hidden sides are kept with the widths, from a set or a list', () => {
    const storage = memoryStorage();

    savePanes({ widths: {}, hidden: new Set(['end']) }, storage);
    expect(JSON.parse(storage.entries.get(PANES_STORAGE_KEY))).toEqual({
      hidden: ['end'],
    });
    expect(loadPanes(storage)).toEqual({ ...none, hidden: ['end'] });

    savePanes({ widths: { end: 500 }, hidden: ['end', 'start'] }, storage);
    expect(loadPanes(storage)).toEqual({
      widths: { end: 500 },
      widened: {},
      hidden: ['start', 'end'],
    });

    // Shown again, with no width of its own: nothing is left to keep.
    savePanes({ widths: {}, hidden: [] }, storage);
    expect(storage.entries.has(PANES_STORAGE_KEY)).toBe(false);
  });

  test('a side restored to its default is left out, and none removes the key', () => {
    const storage = memoryStorage();

    savePanes({ widths: { start: 300, end: 500 } }, storage);
    savePanes({ widths: { end: 500 } }, storage);
    expect(loadPanes(storage).widths).toEqual({ end: 500 });
    savePanes({ widths: {} }, storage);
    expect(storage.entries.has(PANES_STORAGE_KEY)).toBe(false);
  });

  test('a widened side keeps the width its toggle restores, or null for the default', () => {
    const storage = memoryStorage();
    const panes = {
      widths: { start: 700, end: 824 },
      widened: { start: 240.4, end: null },
    };

    savePanes(panes, storage);
    expect(JSON.parse(storage.entries.get(PANES_STORAGE_KEY))).toEqual({
      start: 700,
      end: 824,
      widened: { start: 240, end: null },
    });
    expect(loadPanes(storage)).toEqual({
      widths: { start: 700, end: 824 },
      widened: { start: 240, end: null },
      hidden: [],
    });

    // No longer widened, so nothing is kept for it.
    savePanes({ widths: { end: 808 }, widened: {} }, storage);
    expect(JSON.parse(storage.entries.get(PANES_STORAGE_KEY))).toEqual({
      end: 808,
    });
  });

  test('ignore what is not a width', () => {
    const storage = memoryStorage({
      [PANES_STORAGE_KEY]: JSON.stringify({
        start: 'wide',
        end: -4,
        other: 300,
        hidden: 'end',
      }),
    });

    expect(loadPanes(storage)).toEqual(none);
    expect(
      loadPanes(memoryStorage({ [PANES_STORAGE_KEY]: '{not json' })),
    ).toEqual(none);
    // A side is widened only with a width of its own, and back to a width
    // or the default.
    expect(
      loadPanes(
        memoryStorage({
          [PANES_STORAGE_KEY]: JSON.stringify({
            end: 824,
            widened: { start: 240, end: 'narrow' },
          }),
        }),
      ),
    ).toEqual({ widths: { end: 824 }, widened: {}, hidden: [] });
    // Only the two sides can be hidden.
    expect(
      loadPanes(
        memoryStorage({
          [PANES_STORAGE_KEY]: JSON.stringify({ hidden: ['middle', 'end'] }),
        }),
      ).hidden,
    ).toEqual(['end']);
  });

  test('missing or blocked storage falls back to the defaults', () => {
    expect(loadPanes(null)).toEqual(none);
    expect(loadPanes(throwing)).toEqual(none);
    expect(() => savePanes({ widths: { start: 300 } }, throwing)).not.toThrow();
    expect(() => savePanes({ widths: { start: 300 } }, null)).not.toThrow();
  });
});

describe('minimap size', () => {
  test('keeps its shape, 4 wide to 3 high', () => {
    expect(minimapSize(MINIMAP_DEFAULT_WIDTH)).toEqual({
      width: 200,
      height: 150,
    });
    expect(minimapSize(281)).toEqual({ width: 281, height: 211 });
  });

  test('covers at most half the canvas each way, and never less than the default', () => {
    // Half of 800 wide, and of 700 high: 350 high is 466 wide.
    expect(minimapLimits({ width: 800, height: 700 })).toEqual({
      min: 120,
      max: 400,
    });
    expect(minimapLimits({ width: 1600, height: 600 })).toEqual({
      min: 120,
      max: 400,
    });
    // A large canvas stops at 600; a small one, or none yet, at the default.
    expect(minimapLimits({ width: 3000, height: 2000 }).max).toBe(600);
    expect(minimapLimits({ width: 300, height: 200 }).max).toBe(200);
    expect(minimapLimits({ width: 0, height: 0 }).max).toBe(200);
  });

  test('stays below the default on a canvas too short to hold it', () => {
    // 1280x1024 at 400%: the default's 152px would rise 18px above a 149px
    // canvas. 149 less 32px of margins holds 117px, 156 wide.
    expect(minimapLimits({ width: 286, height: 149 })).toEqual({
      min: 120,
      max: 156,
    });
    // Narrow too: 200 less 32px.
    expect(minimapLimits({ width: 200, height: 400 }).max).toBe(168);
    // Never below the smallest size.
    expect(minimapLimits({ width: 286, height: 100 }).max).toBe(120);
  });

  test('arrow keys move the handle at its top left corner', () => {
    const limits = { min: 120, max: 400 };

    // Up and Left take the corner away from the minimap: larger.
    expect(minimapKeyWidth('ArrowUp', 200, limits)).toBe(216);
    expect(minimapKeyWidth('ArrowLeft', 200, limits)).toBe(216);
    expect(minimapKeyWidth('ArrowDown', 200, limits)).toBe(184);
    expect(minimapKeyWidth('ArrowRight', 200, limits, 10)).toBe(190);
    // Within the limits, which Home and End reach at once.
    expect(minimapKeyWidth('ArrowUp', 395, limits)).toBe(400);
    expect(minimapKeyWidth('ArrowDown', 125, limits)).toBe(120);
    expect(minimapKeyWidth('Home', 300, limits)).toBe(120);
    expect(minimapKeyWidth('End', 300, limits)).toBe(400);
    for (const key of ['Enter', 'Tab', 'a', 'PageUp']) {
      expect(minimapKeyWidth(key, 200, limits)).toBeUndefined();
    }
  });

  test('a drag takes the corner to the pointer, as near as the shape allows', () => {
    const limits = { min: 120, max: 400 };

    // Up and left along the minimap's diagonal: exactly under the pointer.
    expect(minimapDragWidth(200, -40, -30, limits)).toBe(240);
    expect(minimapDragWidth(200, 40, 30, limits)).toBe(160);
    // Straight up grows it too, by less than straight along the diagonal.
    expect(minimapDragWidth(200, 0, -50, limits)).toBe(224);
    // Moving along the other diagonal leaves it as it was.
    expect(minimapDragWidth(200, 30, -40, limits)).toBe(200);
    // Within the limits.
    expect(minimapDragWidth(200, -900, -900, limits)).toBe(400);
    expect(minimapDragWidth(200, 900, 900, limits)).toBe(120);
  });

  test('is remembered under its own key, and forgotten for the default', () => {
    const storage = memoryStorage();

    expect(loadMinimap(storage)).toBeNull();
    saveMinimap(280.4, storage);
    expect(JSON.parse(storage.entries.get(MINIMAP_STORAGE_KEY))).toEqual({
      width: 280,
    });
    expect(loadMinimap(storage)).toBe(280);
    saveMinimap(null, storage);
    expect(storage.entries.has(MINIMAP_STORAGE_KEY)).toBe(false);

    for (const stored of ['{not json', '{"width":"wide"}', '{"width":-5}']) {
      expect(
        loadMinimap(memoryStorage({ [MINIMAP_STORAGE_KEY]: stored })),
      ).toBeNull();
    }
    expect(loadMinimap(null)).toBeNull();
    expect(loadMinimap(throwing)).toBeNull();
    expect(() => saveMinimap(300, throwing)).not.toThrow();
  });
});
