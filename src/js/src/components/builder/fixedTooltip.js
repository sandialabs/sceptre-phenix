// Tooltip for a control that shows a hint on hover and keyboard focus: the
// editor header's buttons and diagram name, the diagram's counts, the
// Palette's entries and help button, the canvas zoom controls, the canvas's
// devices and switches, the toolbar's buttons, the side columns' toggles,
// the Inspector's field labels, and the History dialog's Restore and Delete
// buttons (WCAG 1.4.13). It stays while the pointer moves onto it, across
// the gap between them, until the pointer leaves both, the control loses
// focus, or Escape dismisses it. The pointer on it does not show the
// tooltip of a control beneath it, and neither does a control scrolled under
// a pointer that stays still (see whenPointed). A canvas node keeps its
// tooltip for as long as it has keyboard focus (see keep).
//
// The tooltip is fixed to the viewport so a scrolling side panel cannot clip
// it. On scroll and resize it follows its control, and hides once the
// control scrolls out of view. It lets clicks through to what lies beneath,
// and only repeats text that reaches screen readers as the control's name or
// description, so it is aria-hidden.
//
// The component renders it as
//   <builder-fixed-tooltip :tooltip="tooltip" testid="..." />
// with `tooltip` what useFixedTooltip returned, and binds each control's
// tipEvents(text) with v-on. A tooltip whose content is more than a text
// has a component of its own (see BuilderNodeTooltip.vue).

import { nextTick, onBeforeUnmount, ref } from 'vue';

const TIP_GAP = 4;
const TIP_MARGIN = 8;
const HIDE_DELAY_MS = 200;

function clamp(value, min, max) {
  return Math.max(min, Math.min(value, Math.max(min, max)));
}

/**
 * Where a tooltip goes above its control's table row, so it covers none of
 * the row: its bottom edge just above the row's top edge, centered on the
 * control and kept inside `bounds`. Without room above (the row near the top
 * of `bounds`), it goes just below the row instead.
 *
 * @param {DOMRect} control
 * @param {DOMRect} row
 * @param {{ left: number, right: number, top: number }} bounds
 * @param {{ width: number, height: number }} size the tooltip's
 * @returns {{ left: number, top: number }}
 */
export function aboveRow(control, row, bounds, { width, height }) {
  const top = row.top - height - TIP_GAP;

  return {
    left: clamp(
      control.left + (control.width - width) / 2,
      bounds.left,
      bounds.right - width,
    ),
    top: top >= bounds.top ? top : row.bottom + TIP_GAP,
  };
}

/**
 * Where a tooltip goes above its control: its bottom edge `gap` above the
 * control's top edge, their left edges in line. Without room above (the
 * tooltip's top edge would be higher than `least`), it goes `gap` below the
 * control instead.
 *
 * @param {{ left: number, top: number, bottom: number }} control
 * @param {number} height the tooltip's
 * @param {number} gap
 * @param {number} least the highest the tooltip's top edge may be
 * @returns {{ left: number, top: number }}
 */
export function aboveControl(control, height, gap, least) {
  const top = control.top - height - gap;

  return {
    left: control.left,
    top: top >= least ? top : control.bottom + gap,
  };
}

/**
 * The gap between two boxes that lie apart, one above or beside the other:
 * the box a pointer crosses on its way straight from one to the other.
 *
 * @param {DOMRect} a
 * @param {DOMRect} b
 * @returns {{ left: number, right: number, top: number, bottom: number }|null}
 *   null when they touch or overlap, or lie apart on both axes
 */
export function gapBetween(a, b) {
  const across = {
    left: Math.max(a.left, b.left),
    right: Math.min(a.right, b.right),
  };
  const along = {
    top: Math.max(a.top, b.top),
    bottom: Math.min(a.bottom, b.bottom),
  };
  const beside = across.left > across.right;
  const above = along.top > along.bottom;

  if (beside === above) {
    return null;
  }

  return above
    ? { ...across, top: along.bottom, bottom: along.top }
    : { ...along, left: across.right, right: across.left };
}

// The part of `rect` inside `bounds`.
function clipTo(rect, bounds) {
  return {
    left: Math.max(rect.left, bounds.left),
    right: Math.min(rect.right, bounds.right),
    top: Math.max(rect.top, bounds.top),
    bottom: Math.min(rect.bottom, bounds.bottom),
  };
}

function within(rect, x, y) {
  return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom;
}

// Where and when the pointer last moved, for every tooltip. Scrolling
// moves a control under a pointer that stays still, as Tab does in a
// scrolling panel, and the browser then sends mouseenter with no
// pointermove near it in time. Moving the pointer onto the control sends
// both at once: pointermove just after mouseenter (Chromium) or just before
// it (Firefox). A dialog that closes soon after a click also uncovers a
// control under the still pointer. Focus has moved since the pointer did,
// so the focused control keeps its tooltip.
const MOVE_WINDOW_MS = 100;
let lastPointer = null;
let lastFocus = -Infinity;
let tracking = false;

function trackPointer() {
  if (tracking || typeof window === 'undefined') {
    return;
  }

  tracking = true;
  window.addEventListener(
    'pointermove',
    (event) => {
      lastPointer = {
        x: event.clientX,
        y: event.clientY,
        time: event.timeStamp,
      };
    },
    { capture: true, passive: true },
  );
  window.addEventListener(
    'focusin',
    (event) => {
      lastFocus = event.timeStamp;
    },
    { capture: true, passive: true },
  );
}

// Whether the pointer moved to where the mouseenter happened, when it did,
// and after focus last moved. Within a pixel: a pointer event's position
// has fractions, a mouse event's does not.
function pointerMovedTo({ x, y, time }) {
  return (
    lastPointer !== null &&
    Math.abs(lastPointer.x - x) < 1 &&
    Math.abs(lastPointer.y - y) < 1 &&
    Math.abs(lastPointer.time - time) <= MOVE_WINDOW_MS &&
    lastPointer.time > lastFocus
  );
}

/**
 * Shows a control's tooltip for an event (WCAG 1.4.13 and 2.4.11): at once
 * for focus, and for a mouseenter only once the pointer is known to have
 * moved onto the control (a pointermove may follow it). A mouseenter that
 * scrolling caused shows nothing, and neither does one on a control beneath
 * a tooltip that is showing, which lets the pointer through: the user is
 * reading that tooltip.
 *
 * @param {Event} [event]
 * @param {() => void} show
 */
export function whenPointed(event, show) {
  trackPointer();
  if (event?.type !== 'mouseenter') {
    show();

    return;
  }

  const control = event.currentTarget;
  const at = { x: event.clientX, y: event.clientY, time: event.timeStamp };

  setTimeout(() => {
    const onTip = [
      ...document.querySelectorAll('.builder-tooltip--fixed'),
    ].some((shown) => within(shown.getBoundingClientRect(), at.x, at.y));

    if (pointerMovedTo(at) && !onTip && control?.matches?.(':hover')) {
      show();
    }
  });
}

/**
 * @param {object} [options]
 * @param {'end'|'start'|'below'|'above'} [options.side] the side of the
 *   control the tooltip prefers: 'end' (right) for the left-hand panels,
 *   'start' (left) for the Inspector on the right, so it lies over the
 *   canvas in either case. 'below' for a row of controls such as the
 *   toolbar, so it covers none of the row. 'above' for a node on the
 *   canvas, or below it without room above (see aboveControl)
 * @param {(control: HTMLElement) => HTMLElement|null} [options.beside] the
 *   element the tooltip is placed beside, on the side nearer the control,
 *   for a control with others next to it, such as a table row's buttons:
 *   the tooltip covers none of them, and the pointer reaches it without
 *   crossing another
 * @param {(control: HTMLElement) => HTMLElement|null} [options.above] the
 *   element the tooltip is placed above, such as the control's table row,
 *   once there is no room on `side` of what `beside` names (a narrow
 *   window): it covers none of the row, stays inside the control's dialog,
 *   and the pointer reaches it by moving straight up
 * @param {number} [options.delay] how long, in milliseconds, the pointer
 *   rests on a control before its tooltip shows, for controls the pointer
 *   crosses on its way elsewhere. Focus shows it at once
 * @param {boolean} [options.stopEscape] Escape pressed on the control whose
 *   tooltip shows, or inside it, only hides the tooltip: the key goes no
 *   further, so it does not also do what Escape does there
 * @param {number|(() => number)} [options.gap] the room, in pixels, between
 *   a control and a tooltip above or below it (side 'above')
 */
export function useFixedTooltip({
  side = 'end',
  beside = null,
  above = null,
  delay = 0,
  stopEscape = false,
  gap = TIP_GAP,
} = {}) {
  const tip = ref(null);
  const tipEl = ref(null);

  let anchor = null;
  let hideTimer = null;
  let showTimer = null;

  trackPointer();

  /**
   * Shows a control's tooltip for an event (see whenPointed).
   *
   * @param {Event} event its currentTarget is the control
   * @param {string|object|(() => string|object)} text the tooltip's
   *   content: a text, or what the component that renders the tooltip
   *   draws, or what gives either as the tooltip opens
   */
  function showTip(event, text) {
    const control = event.currentTarget;

    whenPointed(event, () => {
      if (delay > 0 && event.type === 'mouseenter') {
        showLater(control, text);
      } else {
        open(control, text);
      }
    });
  }

  function open(control, text) {
    cancelShow();
    showFor(control, text);
  }

  // Shows the tooltip for a control. A tooltip that waits to show for
  // another control continues to wait.
  function showFor(control, text) {
    cancelHide();
    anchor = control;
    tip.value = {
      text: typeof text === 'function' ? text() : text,
      top: -9999,
      left: -9999,
    };
    listen(true);
    nextTick(placeTip);
  }

  // Once the pointer has rested on the control for the delay. A press
  // during the delay ends the hint, as it does after the tooltip shows.
  function showLater(control, text) {
    cancelShow();
    window.addEventListener('pointerdown', cancelShow, true);
    showTimer = setTimeout(() => {
      cancelShow();

      if (control.isConnected && control.matches(':hover')) {
        open(control, text);
      }
    }, delay);
  }

  function cancelShow() {
    if (showTimer !== null) {
      clearTimeout(showTimer);
      showTimer = null;
      window.removeEventListener('pointerdown', cancelShow, true);
    }
  }

  // The control that keeps its tooltip while it has keyboard focus, and
  // what the tooltip shows for it (see keep).
  let kept = null;

  /**
   * Makes a control's tooltip last as long as its keyboard focus does (WCAG
   * 1.4.13): once the pointer has left another control whose tooltip took
   * its place, and that tooltip, this control's shows again. Call it when
   * the control takes keyboard focus, and call release when it loses it.
   * Dismissing the tooltip, with Escape or a press, ends it too.
   *
   * @param {HTMLElement} control
   * @param {string|object|(() => string|object)} text as showTip takes it
   */
  function keep(control, text) {
    kept = { control, text };
  }

  /**
   * Ends what keep began for this control.
   *
   * @param {HTMLElement} control
   */
  function release(control) {
    if (kept?.control === control) {
      kept = null;
    }
  }

  /**
   * Whether the tooltip is showing for this control.
   *
   * @param {HTMLElement} control
   * @returns {boolean}
   */
  function shownOn(control) {
    return tip.value !== null && anchor === control;
  }

  // Beside the control, on its preferred side, or beside what `beside`
  // names, on the side nearer the control (without room there, beside the
  // control on its other side). Without room on the preferred side (the
  // stacked narrow layout), below it at the end side, or above it at the
  // start side, which keeps the Inspector's field under its label clear.
  // Always inside the viewport. Without room on the preferred side of what
  // `beside` names (a narrow window), above what `above` names instead (see
  // aboveRow).
  function placeTip() {
    if (!tip.value || !anchor?.isConnected) {
      hideTip();
      return;
    }

    const rect = anchor.getBoundingClientRect();
    const canvas = canvasBounds();
    if (!inView(rect, canvas)) {
      hideTip();
      return;
    }

    const element = tipEl.value;
    // Its own width, from before any widening below.
    element?.style.removeProperty('max-width');
    let width = element?.offsetWidth || 0;
    let height = element?.offsetHeight || 0;
    const box = beside?.(anchor)?.getBoundingClientRect() || rect;
    const row = box !== rect && !roomBeside(box, width) && above?.(anchor);
    let toward = side;

    if (box !== rect && side !== 'below') {
      toward = rect.left - box.left <= box.right - rect.right ? 'start' : 'end';
    }

    let left =
      toward === 'start' ? box.left - width - TIP_GAP : box.right + TIP_GAP;
    let top = rect.top;

    if (row) {
      // It takes the dialog's width there, so it wraps less.
      const bounds = dialogBounds();
      element?.style.setProperty(
        'max-width',
        `${bounds.right - bounds.left}px`,
      );
      width = element?.offsetWidth || 0;
      height = element?.offsetHeight || 0;
      ({ left, top } = aboveRow(rect, row.getBoundingClientRect(), bounds, {
        width,
        height,
      }));
    } else if (side === 'above') {
      // Above the part of the control that shows, and inside the canvas
      // that clips it when there is room.
      ({ left, top } = aboveControl(
        canvas ? clipTo(rect, canvas) : rect,
        height,
        typeof gap === 'function' ? gap() : gap,
        Math.max(TIP_MARGIN, canvas?.top ?? 0),
      ));
    } else if (side === 'below') {
      left = rect.left;
      top = rect.bottom + TIP_GAP;
    } else if (toward === 'start' && left < TIP_MARGIN) {
      left = rect.left;
      top = rect.top - height - TIP_GAP;
    } else if (left + width > window.innerWidth - TIP_MARGIN) {
      left = rect.left;
      top = rect.bottom + TIP_GAP;
    }

    if (!row && top + height > window.innerHeight - TIP_MARGIN) {
      top = rect.top - height - TIP_GAP;
    }

    tip.value = {
      ...tip.value,
      left: clamp(left, TIP_MARGIN, window.innerWidth - width - TIP_MARGIN),
      top: clamp(top, TIP_MARGIN, window.innerHeight - height - TIP_MARGIN),
    };
  }

  // A tooltip this wide fits on the preferred side of the box, inside the
  // viewport.
  function roomBeside(box, width) {
    return side === 'start'
      ? box.left - TIP_GAP - width >= TIP_MARGIN
      : box.right + TIP_GAP + width <= window.innerWidth - TIP_MARGIN;
  }

  // The part of the control's dialog, if any, in the viewport, less the
  // margin.
  function dialogBounds() {
    const dialog = anchor.closest('dialog')?.getBoundingClientRect();

    return {
      left: Math.max(0, dialog?.left ?? 0) + TIP_MARGIN,
      right:
        Math.min(window.innerWidth, dialog?.right ?? Infinity) - TIP_MARGIN,
      top: Math.max(0, dialog?.top ?? 0) + TIP_MARGIN,
    };
  }

  // The box of the canvas the control is drawn on, if any: the canvas
  // hides what is panned out of it.
  function canvasBounds() {
    return anchor.closest('.builder-canvas')?.getBoundingClientRect() || null;
  }

  // The control is at least partly visible in the window, in the side
  // panel it scrolls with, if any, and on the canvas that clips it, if any.
  function inView(rect, canvas) {
    const panel = anchor.closest('.builder-layout__side');
    const bounds = panel?.getBoundingClientRect();
    const top = Math.max(0, bounds?.top ?? 0);
    const bottom = Math.min(window.innerHeight, bounds?.bottom ?? Infinity);

    return (
      rect.bottom > top &&
      rect.top < bottom &&
      (!canvas ||
        (rect.right > canvas.left &&
          rect.left < canvas.right &&
          rect.bottom > canvas.top &&
          rect.top < canvas.bottom))
    );
  }

  // Puts the tooltip showing, if any, back beside its control, once the
  // page has drawn where the control now is.
  let frame = null;
  function followAnchor() {
    if (frame === null && tip.value) {
      frame = requestAnimationFrame(() => {
        frame = null;
        placeTip();
      });
    }
  }

  function cancelHide() {
    clearTimeout(hideTimer);
    hideTimer = null;
  }

  // The tooltip lets clicks through, so it cannot use its own hover events.
  // Leaving the control instead waits briefly and keeps the tooltip while
  // the pointer rests over it, or is on its way to it across the gap
  // between the two, however slowly.
  let pointer = null;

  function scheduleHide() {
    cancelHide();
    hideTimer = setTimeout(() => {
      if (pointerOver(tipEl.value) || pointerOver(anchor) || pointerInGap()) {
        scheduleHide();
      } else if (kept && kept.control !== anchor && kept.control.isConnected) {
        showFor(kept.control, kept.text);
      } else {
        close();
      }
    }, HIDE_DELAY_MS);
  }

  function pointerOver(element) {
    if (!pointer || !element?.isConnected) {
      return false;
    }

    return within(element.getBoundingClientRect(), pointer.x, pointer.y);
  }

  function pointerInGap() {
    if (!pointer || !tipEl.value?.isConnected || !anchor?.isConnected) {
      return false;
    }

    const between = gapBetween(
      anchor.getBoundingClientRect(),
      tipEl.value.getBoundingClientRect(),
    );

    return Boolean(between) && within(between, pointer.x, pointer.y);
  }

  function onPointerMove(event) {
    pointer = { x: event.clientX, y: event.clientY };
  }

  // Dismisses the tooltip: the one showing, one waiting to show, and one a
  // control with keyboard focus would get back (see keep).
  function hideTip() {
    cancelShow();
    kept = null;
    close();
  }

  // Hides the tooltip showing. One waiting to show for another control, as
  // the pointer went straight from this one to it, still shows.
  function close() {
    cancelHide();
    if (frame !== null) {
      cancelAnimationFrame(frame);
      frame = null;
    }
    tip.value = null;
    anchor = null;
    listen(false);
  }

  function onKeydown(event) {
    if (event.key !== 'Escape') {
      return;
    }

    if (stopEscape && anchor?.contains(event.target)) {
      event.stopPropagation();
    }

    hideTip();
  }

  function listen(on) {
    const method = on ? 'addEventListener' : 'removeEventListener';

    window[method]('keydown', onKeydown, true);
    window[method]('scroll', followAnchor, true);
    window[method]('resize', followAnchor);
    window[method]('pointermove', onPointerMove, true);
    // Any click, on the canvas or on the control itself, ends the hint.
    window[method]('pointerdown', hideTip, true);
  }

  /**
   * The events that show a control's tooltip on hover and focus, and hide
   * it, to bind with v-on.
   *
   * @param {string|((control: HTMLElement) => string)} text the tooltip's
   *   text, or what gives it from the control when it shows, so it follows
   *   the control's state. None shows no tooltip
   * @param {object} [hooks]
   * @param {(event: Event, text: string) => void} [hooks.onShow] runs as it
   *   shows
   * @param {(event: Event) => void} [hooks.onLeave] runs when the pointer
   *   leaves the control, in place of hiding the tooltip after a delay
   */
  function tipEvents(text, { onShow, onLeave = scheduleHide } = {}) {
    const show = (event) => {
      const shown =
        typeof text === 'function' ? text(event.currentTarget) : text;

      if (shown) {
        onShow?.(event, shown);
        showTip(event, shown);
      }
    };

    return {
      mouseenter: show,
      mouseleave: onLeave,
      focus: show,
      blur: hideTip,
    };
  }

  // The tooltip's element, as a function ref: placing the tooltip measures
  // it.
  function setTipEl(element) {
    tipEl.value = element;
  }

  onBeforeUnmount(hideTip);

  return {
    tip,
    setTipEl,
    showTip,
    scheduleHide,
    hideTip,
    tipEvents,
    shownOn,
    keep,
    release,
    follow: followAnchor,
  };
}
