// When a press and release, or a drag of nodes that wobbled, is a click
// (clickSlop.js). What the canvas then does is left to the Playwright specs.

import { describe, expect, test } from 'vitest';

import {
  CLICK_SLOP_PX,
  dragIsClick,
  endedNear,
  pointOf,
  pressOf,
  TOUCH_SLOP_PX,
} from '@/components/builder/clickSlop.js';

const at = (x, y) => ({ clientX: x, clientY: y });

describe('the point of an event', () => {
  test('is the client point of a mouse or pointer event', () => {
    expect(pointOf(at(12, 34))).toEqual({ x: 12, y: 34 });
  });

  test('is the first changed touch of a touch event', () => {
    expect(pointOf({ changedTouches: [at(5, 6), at(7, 8)] })).toEqual({
      x: 5,
      y: 6,
    });
  });

  test('is null for no event, or one without a point', () => {
    expect(pointOf()).toBeNull();
    expect(pointOf({ detail: 0 })).toBeNull();
  });
});

describe('a press', () => {
  test('of a mouse has the mouse slop', () => {
    expect(pressOf({ ...at(1, 2), pointerType: 'mouse' })).toEqual({
      x: 1,
      y: 2,
      slop: CLICK_SLOP_PX,
    });
    expect(pressOf(at(1, 2)).slop).toBe(CLICK_SLOP_PX);
  });

  test('of a finger or pen has the larger touch slop', () => {
    expect(TOUCH_SLOP_PX).toBe(8);
    expect(pressOf({ ...at(1, 2), pointerType: 'touch' }).slop).toBe(
      TOUCH_SLOP_PX,
    );
    expect(pressOf({ ...at(1, 2), pointerType: 'pen' }).slop).toBe(
      TOUCH_SLOP_PX,
    );
    expect(pressOf({ changedTouches: [at(1, 2)] }).slop).toBe(TOUCH_SLOP_PX);
  });

  test('is null without a point', () => {
    expect(pressOf()).toBeNull();
    expect(pressOf({ detail: 0 })).toBeNull();
  });
});

describe('a release near its press', () => {
  const start = { x: 100, y: 100 };

  test('is within CLICK_SLOP_PX, along any line', () => {
    expect(CLICK_SLOP_PX).toBe(4);
    expect(endedNear(start, at(100, 100))).toBe(true);
    expect(endedNear(start, at(100 + CLICK_SLOP_PX, 100))).toBe(true);
    expect(endedNear(start, at(102, 103))).toBe(true);
    expect(endedNear(start, at(100 + CLICK_SLOP_PX + 1, 100))).toBe(false);
    expect(endedNear(start, at(103, 103))).toBe(false);
  });

  test('is within the slop of the press when it has one', () => {
    const touch = { ...start, slop: TOUCH_SLOP_PX };

    expect(endedNear(touch, at(107, 100))).toBe(true);
    expect(endedNear(touch, at(105, 106))).toBe(true);
    expect(endedNear(touch, at(109, 100))).toBe(false);
  });

  test('is not known without both points', () => {
    expect(endedNear(null, at(100, 100))).toBe(false);
    expect(endedNear(start, {})).toBe(false);
  });
});

describe('a drag of nodes that ended', () => {
  const start = { x: 100, y: 100 };

  test('is a click when the pointer wobbled up to 4 pixels and stayed there', () => {
    for (const [x, y] of [
      [100, 100],
      [101, 100],
      [100, 102],
      [102, 102],
      [104, 100],
    ]) {
      expect(dragIsClick({ start, event: at(x, y) }), `${x},${y}`).toBe(true);
      expect(dragIsClick({ start, event: at(x, y), strayed: false })).toBe(
        true,
      );
    }
  });

  test('is a move when the pointer went farther, also 7 pixels at any zoom', () => {
    expect(dragIsClick({ start, event: at(105, 100) })).toBe(false);
    expect(dragIsClick({ start, event: at(107, 100) })).toBe(false);
    expect(dragIsClick({ start, event: at(137, 121) })).toBe(false);
  });

  test('is a move when the pointer went out of the slop and came back', () => {
    expect(dragIsClick({ start, event: at(101, 100), strayed: true })).toBe(
      false,
    );
  });

  test('of a finger is a click up to 8 pixels', () => {
    const touch = pressOf({ ...at(100, 100), pointerType: 'touch' });

    expect(dragIsClick({ start: touch, event: at(107, 100) })).toBe(true);
    expect(dragIsClick({ start: touch, event: at(109, 100) })).toBe(false);
  });

  test('without a known press, is a move', () => {
    expect(dragIsClick({ start: null, event: at(101, 100) })).toBe(false);
  });
});
