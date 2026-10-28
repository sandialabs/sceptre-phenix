// The keys of the Inspector's combobox fields (combobox.js).

import { describe, expect, test } from 'vitest';

import { comboboxKey } from '@/builder/combobox.js';

describe('the combobox keys', () => {
  test('keys open, move through, take and close the list', () => {
    const key = (name, state, extra = {}) =>
      comboboxKey({ key: name, ...extra }, state);
    const open = { expanded: true, active: 1, count: 3 };
    const closed = { expanded: false, active: -1, count: 3 };

    expect(key('ArrowDown', closed)).toEqual({
      action: 'move',
      index: 0,
      prevent: true,
    });
    expect(key('ArrowDown', open).index).toBe(2);
    expect(key('ArrowDown', { ...open, active: 2 }).index).toBe(0);
    expect(key('ArrowUp', closed).index).toBe(2);
    expect(key('ArrowUp', { ...open, active: 0 }).index).toBe(2);
    expect(key('ArrowDown', closed, { altKey: true }).action).toBe('open');
    expect(key('Enter', open)).toEqual({
      action: 'choose',
      index: 1,
      prevent: true,
    });
    expect(key('Enter', closed).action).toBe('add');
    expect(key('Escape', open).action).toBe('close');
    // A closed list leaves Escape to the dialog.
    expect(key('Escape', closed)).toBeNull();
    expect(key('Tab', open)).toEqual({ action: 'close', prevent: false });
    expect(key('ArrowDown', { ...closed, count: 0 })).toBeNull();
    expect(key('ArrowDown', open, { ctrlKey: true })).toBeNull();
    expect(key('a', open)).toBeNull();
  });

  test('keys that compose text with an IME go to the browser', () => {
    const open = { expanded: true, active: 1, count: 3 };

    for (const name of ['Enter', 'ArrowDown', 'ArrowUp', 'Escape', 'Tab']) {
      expect(comboboxKey({ key: name, isComposing: true }, open)).toBeNull();
      expect(comboboxKey({ key: name, keyCode: 229 }, open)).toBeNull();
    }
  });
});
