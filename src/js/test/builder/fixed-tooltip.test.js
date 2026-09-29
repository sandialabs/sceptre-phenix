import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

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
