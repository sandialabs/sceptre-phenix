import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { aboveRow } from '@/components/builder/fixedTooltip.js';

// A tooltip above a table row covers none of the row's controls: in a
// narrow window, Draft History's Restore and Delete tooltips go there.
describe('aboveRow', () => {
  // A dialog from 20 to 300 pixels across and from 20 down, and a row whose
  // Delete, 28 pixels wide, is at its end.
  const bounds = { left: 20, right: 300, top: 20 };
  const row = { top: 200, bottom: 240 };
  const control = { left: 260, width: 28 };

  it('places it just above the row, centered on its control', () => {
    expect(aboveRow(control, row, bounds, { width: 20, height: 30 })).toEqual({
      left: 264,
      top: 166,
    });
  });

  it('keeps it inside the dialog', () => {
    expect(aboveRow(control, row, bounds, { width: 100, height: 30 })).toEqual({
      left: 200,
      top: 166,
    });
    expect(
      aboveRow({ left: 30, width: 28 }, row, bounds, {
        width: 100,
        height: 30,
      }),
    ).toEqual({ left: 20, top: 166 });
  });

  it('places it just below the row without room above', () => {
    const first = { top: 50, bottom: 90 };

    expect(aboveRow(control, first, bounds, { width: 20, height: 30 })).toEqual(
      { left: 264, top: 94 },
    );
    // Room to the pixel is room.
    expect(
      aboveRow(control, { top: 54, bottom: 94 }, bounds, {
        width: 20,
        height: 30,
      }).top,
    ).toBe(20);
  });
});

// whenPointed decides whether a mouseenter is the pointer moving onto a
// control. The module keeps its pointer and focus tracking in module state,
// so each test loads it afresh, then stubs window and document (Vue reads
// document as it loads).
describe('whenPointed', () => {
  let listeners;

  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
    listeners = {};
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  async function load() {
    const { whenPointed } = await import(
      '@/components/builder/fixedTooltip.js'
    );
    vi.stubGlobal('window', {
      addEventListener: (type, listener) => {
        listeners[type] = listener;
      },
    });
    vi.stubGlobal('document', { querySelectorAll: () => [] });

    return whenPointed;
  }

  const control = { matches: () => true };
  const enter = (timeStamp) => ({
    type: 'mouseenter',
    currentTarget: control,
    clientX: 10,
    clientY: 20,
    timeStamp,
  });

  it('shows a tooltip when the pointer moves onto the control', async () => {
    const whenPointed = await load();
    const show = vi.fn();

    whenPointed(enter(1000), show);
    listeners.pointermove({ clientX: 10.4, clientY: 20.2, timeStamp: 1001 });
    vi.runAllTimers();

    expect(show).toHaveBeenCalledTimes(1);
  });

  it('shows at once on focus', async () => {
    const whenPointed = await load();
    const show = vi.fn();

    whenPointed({ type: 'focus' }, show);

    expect(show).toHaveBeenCalledTimes(1);
  });

  it('shows nothing for a control uncovered under the pointer after focus moved', async () => {
    const whenPointed = await load();
    whenPointed({ type: 'focus' }, () => {});
    // A click on a dialog's button, then the dialog closes and focus moves
    // on, uncovering another control under the pointer.
    listeners.pointermove({ clientX: 10, clientY: 20, timeStamp: 1000 });
    listeners.focusin({ timeStamp: 1050 });
    const show = vi.fn();

    whenPointed(enter(1080), show);
    vi.runAllTimers();

    expect(show).not.toHaveBeenCalled();
  });

  it('shows nothing for a control scrolled under a still pointer', async () => {
    const whenPointed = await load();
    whenPointed({ type: 'focus' }, () => {});
    listeners.pointermove({ clientX: 10, clientY: 20, timeStamp: 1000 });
    const show = vi.fn();

    whenPointed(enter(1500), show);
    vi.runAllTimers();

    expect(show).not.toHaveBeenCalled();
  });
});
