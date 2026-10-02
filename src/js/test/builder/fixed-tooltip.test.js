import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  aboveControl,
  aboveRow,
  gapBetween,
} from '@/components/builder/fixedTooltip.js';

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

// A canvas node's info tooltip goes above the node, or below it without
// room above.
describe('aboveControl', () => {
  const control = { left: 120, top: 200, bottom: 260 };

  it('places it above the control, their left edges in line', () => {
    expect(aboveControl(control, 60, 10, 8)).toEqual({ left: 120, top: 130 });
  });

  it('places it below the control without room above', () => {
    expect(aboveControl(control, 60, 10, 131)).toEqual({ left: 120, top: 270 });
    // Room to the pixel is room.
    expect(aboveControl(control, 60, 10, 130).top).toBe(130);
  });
});

// The pointer crosses the gap between a control and its tooltip on its way
// to the tooltip, which stays meanwhile.
describe('gapBetween', () => {
  const box = (left, top, right, bottom) => ({ left, top, right, bottom });

  it('is the box between a control and a tooltip above or below it', () => {
    const control = box(100, 200, 300, 260);

    expect(gapBetween(control, box(100, 130, 220, 190))).toEqual(
      box(100, 190, 220, 200),
    );
    expect(gapBetween(control, box(150, 270, 400, 330))).toEqual(
      box(150, 260, 300, 270),
    );
  });

  it('is the box between a control and a tooltip beside it', () => {
    const control = box(100, 200, 300, 260);

    expect(gapBetween(control, box(304, 200, 500, 300))).toEqual(
      box(300, 200, 304, 260),
    );
    expect(gapBetween(control, box(0, 180, 96, 240))).toEqual(
      box(96, 200, 100, 240),
    );
  });

  it('is nothing for boxes that touch or overlap, or lie apart both ways', () => {
    const control = box(100, 200, 300, 260);

    expect(gapBetween(control, box(150, 220, 250, 300))).toBeNull();
    expect(gapBetween(control, box(100, 140, 220, 200))).toBeNull();
    expect(gapBetween(control, box(400, 100, 500, 150))).toBeNull();
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

// The tooltip itself, as the canvas uses it for its nodes: above its
// control, after a delay for the pointer, and with an Escape that goes no
// further. The module keeps its pointer tracking in module state, so each
// test loads it afresh, with Vue, and then stubs the browser.
describe('useFixedTooltip', () => {
  const WINDOW = { innerWidth: 1000, innerHeight: 800 };
  const TIP = { offsetWidth: 120, offsetHeight: 60 };
  let listeners;

  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
    listeners = new Map();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  // What useFixedTooltip returns, used in a component as it must be, with
  // the tooltip's element given a size and the box it is placed at.
  async function mount(options) {
    const { createSSRApp, nextTick } = await import('vue');
    const { renderToString } = await import('vue/server-renderer');
    const { useFixedTooltip } = await import(
      '@/components/builder/fixedTooltip.js'
    );
    let tooltip;

    vi.stubGlobal('window', {
      ...WINDOW,
      addEventListener: (type, listener) => {
        listeners.set(type, [...(listeners.get(type) || []), listener]);
      },
      removeEventListener: (type, listener) => {
        listeners.set(
          type,
          (listeners.get(type) || []).filter((entry) => entry !== listener),
        );
      },
    });
    vi.stubGlobal('document', { querySelectorAll: () => [] });
    vi.stubGlobal('requestAnimationFrame', (run) => setTimeout(run, 16));
    vi.stubGlobal('cancelAnimationFrame', (id) => clearTimeout(id));

    await renderToString(
      createSSRApp({
        setup() {
          tooltip = useFixedTooltip(options);

          return () => null;
        },
      }),
    );

    tooltip.setTipEl({
      ...TIP,
      isConnected: true,
      style: { removeProperty: () => {}, setProperty: () => {} },
      getBoundingClientRect: () => {
        const { left, top } = tooltip.tip.value;

        return {
          left,
          top,
          right: left + TIP.offsetWidth,
          bottom: top + TIP.offsetHeight,
        };
      },
    });

    return { tooltip, placed: nextTick };
  }

  function fire(type, event) {
    for (const listener of listeners.get(type) || []) {
      listener(event);
    }
  }

  // A control at a box, on a canvas when one is given.
  function control(rect, canvas = null) {
    const element = {
      rect,
      isConnected: true,
      hovered: true,
      inside: {},
      matches: (selector) => selector === ':hover' && element.hovered,
      contains: (target) => target === element || target === element.inside,
      getBoundingClientRect: () => element.rect,
      closest: (selector) =>
        selector === '.builder-canvas' && canvas
          ? { getBoundingClientRect: () => canvas }
          : null,
    };

    return element;
  }

  const NODE = { left: 100, right: 300, top: 200, bottom: 260 };
  const focus = (target) => ({ type: 'focus', currentTarget: target });

  // The pointer moves onto the control: mouseenter, and the pointermove
  // that comes with it.
  function pointAt(tooltip, target, text, { x = 150, y = 230 } = {}) {
    tooltip.showTip(
      {
        type: 'mouseenter',
        currentTarget: target,
        clientX: x,
        clientY: y,
        timeStamp: 1000,
      },
      text,
    );
    fire('pointermove', { clientX: x, clientY: y, timeStamp: 1001 });
  }

  function place(tooltip) {
    const { left, top } = tooltip.tip.value;

    return { left, top };
  }

  it('goes above its control, the gap away, and below without room above', async () => {
    const { tooltip, placed } = await mount({ side: 'above', gap: 10 });
    const node = control(NODE);

    tooltip.showTip(focus(node), 'info');
    await placed();
    expect(place(tooltip)).toEqual({ left: 100, top: 130 });

    node.rect = { left: 100, right: 300, top: 40, bottom: 100 };
    tooltip.showTip(focus(node), 'info');
    await placed();
    expect(place(tooltip)).toEqual({ left: 100, top: 110 });
  });

  it('takes the gap from a function, when it is placed', async () => {
    let zoom = 1;
    const { tooltip, placed } = await mount({
      side: 'above',
      gap: () => 10 * zoom,
    });
    const node = control(NODE);

    tooltip.showTip(focus(node), 'info');
    await placed();
    expect(place(tooltip).top).toBe(130);

    zoom = 2;
    tooltip.follow();
    vi.advanceTimersByTime(16);
    expect(place(tooltip).top).toBe(120);
  });

  it('stays on the canvas that clips its control, and goes when the control leaves it', async () => {
    const canvas = { left: 250, right: 900, top: 150, bottom: 700 };
    const { tooltip, placed } = await mount({ side: 'above', gap: 10 });
    const node = control(NODE, canvas);

    // The control shows from the canvas's left edge on, and above it the
    // tooltip would leave the canvas, so it goes below.
    tooltip.showTip(focus(node), 'info');
    await placed();
    expect(place(tooltip)).toEqual({ left: 250, top: 270 });

    // Panned to the right, there is room above the part that shows.
    node.rect = { left: 400, right: 600, top: 300, bottom: 360 };
    tooltip.follow();
    vi.advanceTimersByTime(16);
    expect(place(tooltip)).toEqual({ left: 400, top: 230 });

    // Panned out of the canvas, to its left, though still in the window.
    node.rect = { left: 20, right: 220, top: 300, bottom: 360 };
    tooltip.follow();
    vi.advanceTimersByTime(16);
    expect(tooltip.tip.value).toBeNull();
    expect(tooltip.shownOn(node)).toBe(false);
  });

  it('follows nothing while no tooltip shows', async () => {
    const { tooltip } = await mount({ side: 'above' });

    tooltip.follow();

    expect(vi.getTimerCount()).toBe(0);
  });

  it('shows for the pointer only once it has rested on the control for the delay', async () => {
    const { tooltip, placed } = await mount({ side: 'above', delay: 400 });
    const node = control(NODE);

    pointAt(tooltip, node, 'info');
    vi.advanceTimersByTime(399);
    expect(tooltip.tip.value).toBeNull();

    vi.advanceTimersByTime(1);
    await placed();
    expect(tooltip.tip.value.text).toBe('info');
    expect(tooltip.shownOn(node)).toBe(true);
  });

  it('shows nothing for a pointer that left during the delay, or pressed', async () => {
    const { tooltip } = await mount({ side: 'above', delay: 400 });
    const node = control(NODE);

    pointAt(tooltip, node, 'info');
    vi.advanceTimersByTime(200);
    node.hovered = false;
    vi.advanceTimersByTime(400);
    expect(tooltip.tip.value).toBeNull();

    node.hovered = true;
    pointAt(tooltip, node, 'info');
    vi.advanceTimersByTime(200);
    fire('pointerdown', {});
    vi.advanceTimersByTime(400);
    expect(tooltip.tip.value).toBeNull();
    // Nothing is left waiting, or listening for the press.
    expect(vi.getTimerCount()).toBe(0);
    expect(listeners.get('pointerdown')).toEqual([]);
  });

  it('shows at once on focus, whatever the delay', async () => {
    const { tooltip } = await mount({ side: 'above', delay: 400 });
    const node = control(NODE);

    tooltip.showTip(focus(node), 'info');

    expect(tooltip.shownOn(node)).toBe(true);
    expect(tooltip.shownOn(control(NODE))).toBe(false);
  });

  it('shows without a delay unless one is asked for', async () => {
    const { tooltip } = await mount();
    const button = control(NODE);

    pointAt(tooltip, button, 'hint');
    vi.advanceTimersByTime(0);

    expect(tooltip.shownOn(button)).toBe(true);
  });

  it('takes an object as its content, or what gives the content as it opens', async () => {
    const { tooltip } = await mount({ side: 'above', delay: 400 });
    const node = control(NODE);
    const info = { rows: [{ label: 'OS type', lines: ['linux'] }] };
    let current = { rows: [] };

    tooltip.showTip(focus(node), info);
    expect(tooltip.tip.value.text).toEqual(info);

    tooltip.hideTip();
    pointAt(tooltip, node, () => current);
    current = info;
    vi.advanceTimersByTime(400);
    expect(tooltip.tip.value.text).toEqual(info);
  });

  describe('Escape', () => {
    const escape = (target) => ({
      key: 'Escape',
      target,
      stopPropagation: vi.fn(),
    });

    it('pressed on the control only hides the tooltip, when asked to stop there', async () => {
      const { tooltip } = await mount({ side: 'above', stopEscape: true });
      const node = control(NODE);

      for (const target of [node, node.inside]) {
        const event = escape(target);

        tooltip.showTip(focus(node), 'info');
        fire('keydown', event);

        expect(tooltip.tip.value).toBeNull();
        expect(event.stopPropagation).toHaveBeenCalledTimes(1);
      }
    });

    it('pressed elsewhere hides the tooltip and goes on', async () => {
      const { tooltip } = await mount({ side: 'above', stopEscape: true });
      const node = control(NODE);
      const event = escape({});

      tooltip.showTip(focus(node), 'info');
      fire('keydown', event);

      expect(tooltip.tip.value).toBeNull();
      expect(event.stopPropagation).not.toHaveBeenCalled();
    });

    it('goes on from the control too unless asked to stop', async () => {
      const { tooltip } = await mount();
      const button = control(NODE);
      const event = escape(button);

      tooltip.showTip(focus(button), 'hint');
      fire('keydown', event);

      expect(tooltip.tip.value).toBeNull();
      expect(event.stopPropagation).not.toHaveBeenCalled();
    });

    it('is not listened for once the tooltip is hidden, and other keys do nothing', async () => {
      const { tooltip } = await mount({ side: 'above', stopEscape: true });
      const node = control(NODE);
      const other = { key: 'Enter', target: node, stopPropagation: vi.fn() };

      tooltip.showTip(focus(node), 'info');
      fire('keydown', other);
      expect(tooltip.shownOn(node)).toBe(true);
      expect(other.stopPropagation).not.toHaveBeenCalled();

      tooltip.hideTip();
      const event = escape(node);
      fire('keydown', event);
      expect(event.stopPropagation).not.toHaveBeenCalled();
    });
  });

  // WCAG 1.4.13: the pointer can move onto the tooltip.
  it('stays while the pointer crosses the gap to it, and rests on it', async () => {
    const { tooltip, placed } = await mount({ side: 'above', gap: 10 });
    const node = control(NODE);
    const move = (x, y) => fire('pointermove', { clientX: x, clientY: y });

    tooltip.showTip(focus(node), 'info');
    await placed();
    expect(place(tooltip)).toEqual({ left: 100, top: 130 });

    // Off the control into the gap, however long it takes to cross.
    move(150, 195);
    tooltip.scheduleHide();
    vi.advanceTimersByTime(1000);
    expect(tooltip.shownOn(node)).toBe(true);

    // On the tooltip.
    move(150, 160);
    vi.advanceTimersByTime(1000);
    expect(tooltip.shownOn(node)).toBe(true);

    // Beside the gap is off both.
    move(260, 195);
    vi.advanceTimersByTime(200);
    expect(tooltip.tip.value).toBeNull();
  });

  // Two more nodes, to the right of NODE, and a place off all three.
  const SECOND = { left: 500, right: 700, top: 200, bottom: 260 };
  const THIRD = { left: 500, right: 700, top: 400, bottom: 460 };
  const ON_SECOND = { x: 600, y: 230 };
  const ON_THIRD = { x: 600, y: 430 };
  const ELSEWHERE = { clientX: 900, clientY: 600 };

  it('shows for the control the pointer went straight to from another', async () => {
    const { tooltip, placed } = await mount({ side: 'above', delay: 400 });
    const [first, second] = [control(NODE), control(SECOND)];

    pointAt(tooltip, first, 'first');
    vi.advanceTimersByTime(400);
    await placed();
    expect(tooltip.shownOn(first)).toBe(true);

    // Off the first and onto the second in one move: the first's tooltip
    // goes, and the second's still comes once the pointer has rested.
    first.hovered = false;
    tooltip.scheduleHide();
    pointAt(tooltip, second, 'second', ON_SECOND);
    vi.advanceTimersByTime(200);
    expect(tooltip.tip.value).toBeNull();

    vi.advanceTimersByTime(200);
    expect(tooltip.shownOn(second)).toBe(true);
    expect(tooltip.tip.value.text).toBe('second');
  });

  // WCAG 1.4.13: the tooltip of a control lasts as long as its keyboard
  // focus does.
  describe('a control that keeps its tooltip', () => {
    // The pointer rests on another control until its tooltip shows.
    async function restOn(tooltip, placed, target, text, at) {
      pointAt(tooltip, target, text, at);
      vi.advanceTimersByTime(400);
      await placed();
      expect(tooltip.shownOn(target)).toBe(true);
    }

    function leave(tooltip, target) {
      target.hovered = false;
      fire('pointermove', ELSEWHERE);
      tooltip.scheduleHide();
    }

    it('gets it back once the pointer has left another control, with what it shows then', async () => {
      const { tooltip, placed } = await mount({
        side: 'above',
        delay: 400,
        gap: 10,
      });
      const [focused, other] = [control(NODE), control(SECOND)];
      let info = 'as focused';

      tooltip.keep(focused, () => info);
      tooltip.showTip(focus(focused), info);
      await restOn(tooltip, placed, other, 'other', ON_SECOND);
      expect(tooltip.tip.value.text).toBe('other');

      // The pointer may still move onto the other control's tooltip.
      info = 'as edited since';
      other.hovered = false;
      fire('pointermove', { clientX: 600, clientY: 160 });
      tooltip.scheduleHide();
      vi.advanceTimersByTime(1000);
      expect(tooltip.shownOn(other)).toBe(true);

      fire('pointermove', ELSEWHERE);
      vi.advanceTimersByTime(200);
      expect(tooltip.shownOn(focused)).toBe(true);
      expect(tooltip.tip.value.text).toBe('as edited since');

      await placed();
      expect(place(tooltip)).toEqual({ left: 100, top: 130 });
      // Nothing is left waiting to hide it.
      vi.advanceTimersByTime(1000);
      expect(tooltip.shownOn(focused)).toBe(true);
    });

    it('gets it back between two other controls the pointer rests on', async () => {
      const { tooltip, placed } = await mount({ side: 'above', delay: 400 });
      const [focused, second, third] = [
        control(NODE),
        control(SECOND),
        control(THIRD),
      ];

      tooltip.keep(focused, 'focused');
      tooltip.showTip(focus(focused), 'focused');
      await restOn(tooltip, placed, second, 'second', ON_SECOND);

      second.hovered = false;
      tooltip.scheduleHide();
      pointAt(tooltip, third, 'third', ON_THIRD);
      vi.advanceTimersByTime(200);
      expect(tooltip.shownOn(focused)).toBe(true);

      vi.advanceTimersByTime(200);
      expect(tooltip.shownOn(third)).toBe(true);

      leave(tooltip, third);
      vi.advanceTimersByTime(200);
      expect(tooltip.shownOn(focused)).toBe(true);
    });

    it('gets it back no more once it has lost focus', async () => {
      const { tooltip, placed } = await mount({ side: 'above', delay: 400 });
      const [focused, other] = [control(NODE), control(SECOND)];

      tooltip.keep(focused, 'focused');
      tooltip.showTip(focus(focused), 'focused');
      await restOn(tooltip, placed, other, 'other', ON_SECOND);

      // Another control's release changes nothing.
      tooltip.release(other);
      tooltip.release(focused);
      leave(tooltip, other);
      vi.advanceTimersByTime(200);

      expect(tooltip.tip.value).toBeNull();
    });

    it.each([
      ['Escape', () => fire('keydown', { key: 'Escape', target: {} })],
      ['a press', () => fire('pointerdown', {})],
    ])('gets it back no more once %s dismissed it', async (_, dismiss) => {
      const { tooltip, placed } = await mount({ side: 'above', delay: 400 });
      const [focused, other] = [control(NODE), control(SECOND)];

      tooltip.keep(focused, 'focused');
      tooltip.showTip(focus(focused), 'focused');
      dismiss();
      expect(tooltip.tip.value).toBeNull();

      await restOn(tooltip, placed, other, 'other', ON_SECOND);
      leave(tooltip, other);
      vi.advanceTimersByTime(200);

      expect(tooltip.tip.value).toBeNull();
    });

    it('gets none back once it has left the page', async () => {
      const { tooltip, placed } = await mount({ side: 'above', delay: 400 });
      const [focused, other] = [control(NODE), control(SECOND)];

      tooltip.keep(focused, 'focused');
      tooltip.showTip(focus(focused), 'focused');
      await restOn(tooltip, placed, other, 'other', ON_SECOND);

      focused.isConnected = false;
      leave(tooltip, other);
      vi.advanceTimersByTime(200);

      expect(tooltip.tip.value).toBeNull();
    });
  });
});
