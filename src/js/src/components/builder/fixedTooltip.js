// A tooltip that shows on hover and keyboard focus and stays while the pointer
// is on it (WCAG 1.4.13). It is fixed to the viewport so a scrolling panel
// cannot clip it, and aria-hidden because it repeats the control's name.

import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

const TIP_GAP = 4;
const TIP_MARGIN = 8;
const HIDE_DELAY_MS = 200;

function clamp(value, min, max) {
  return Math.max(min, Math.min(value, Math.max(min, max)));
}

/**
 * Where a tooltip goes so it covers none of its control's row: above the row,
 * or below it without room, inside `bounds`.
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
 * Where a tooltip goes above its control, left edges in line, or below it when
 * its top would be higher than `least`.
 */
export function aboveControl(control, height, gap, least) {
  const top = control.top - height - gap;

  return {
    left: control.left,
    top: top >= least ? top : control.bottom + gap,
  };
}

/** The box between two boxes that lie apart on one axis only, or null. */
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

// Where and when the pointer last moved. A mouseenter with no pointermove near
// it came from scrolling or a closed dialog, so it shows nothing. Chromium
// sends the pointermove after mouseenter, and Firefox before it, or a frame
// later when the pointer comes from outside the page.
const MOVE_WINDOW_MS = 100;
let lastPointer = null;
let lastFocus = -Infinity;
let tracking = false;
// The mouseenters that wait for their pointermove (see whenPointed).
const awaitingMove = new Set();

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
      awaitingMove.forEach((waiting) => waiting());
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

// Whether the pointer moved when the mouseenter happened, and after focus
// last moved.
function pointerMovedAt(time) {
  return (
    lastPointer !== null &&
    Math.abs(lastPointer.time - time) <= MOVE_WINDOW_MS &&
    lastPointer.time > lastFocus
  );
}

// Whether the pointer moved to where the mouseenter happened, when it did,
// and after focus last moved. Within a pixel: a pointer event's position
// has fractions, a mouse event's does not.
function pointerMovedTo({ x, y, time }) {
  return (
    pointerMovedAt(time) &&
    Math.abs(lastPointer.x - x) < 1 &&
    Math.abs(lastPointer.y - y) < 1
  );
}

// Runs `then` when a pointermove on the control comes within MOVE_WINDOW_MS
// of the mouseenter. Firefox can give that move another position.
function awaitMove(control, time, then) {
  let timer = null;
  const waiting = () => {
    if (pointerMovedAt(time) && control?.matches?.(':hover')) {
      clearTimeout(timer);
      awaitingMove.delete(waiting);
      then();
    }
  };

  awaitingMove.add(waiting);
  timer = setTimeout(() => awaitingMove.delete(waiting), MOVE_WINDOW_MS);
}

/**
 * Shows a control's tooltip at once for focus, and for a mouseenter only after
 * a pointermove (WCAG 1.4.13, 2.4.11). Scrolling and a shown tooltip block it.
 */
export function whenPointed(event, show) {
  trackPointer();
  if (event?.type !== 'mouseenter') {
    show();

    return;
  }

  const control = event.currentTarget;
  const at = { x: event.clientX, y: event.clientY, time: event.timeStamp };

  const showUnlessOnTip = () => {
    const onTip = [
      ...document.querySelectorAll('.builder-tooltip--fixed'),
    ].some((shown) => within(shown.getBoundingClientRect(), at.x, at.y));

    if (!onTip && control?.matches?.(':hover')) {
      show();
    }
  };

  setTimeout(() => {
    if (pointerMovedTo(at)) {
      showUnlessOnTip();
    } else {
      awaitMove(control, at.time, showUnlessOnTip);
    }
  });
}

/**
 * A tooltip fixed to the viewport. Options: side ('end', 'start', 'below' or
 * 'above'), beside and above (an element to place it by), delay (hover ms),
 * stopEscape (Escape only hides it) and gap (px from the control).
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

  // Shows a control's tooltip for an event (see whenPointed). `text` is a text,
  // what the tooltip component draws, or a function that gives either.
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

  // Keeps a control's tooltip while it has keyboard focus (WCAG 1.4.13), also
  // after another tooltip took its place. Call release when focus leaves.
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

  /** Whether the tooltip shows for this control. */
  function shownOn(control) {
    return tip.value !== null && anchor === control;
  }

  // Beside the control on its preferred side, or beside what `beside` names.
  // Without room, below or above the control, or above what `above` names
  // (see aboveRow). Always inside the viewport.
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

    const stop =
      typeof stopEscape === 'function'
        ? stopEscape(event)
        : stopEscape && anchor?.contains(event.target);

    if (stop) {
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

  // The v-on events that show and hide a control's tooltip. `text` can be a
  // function of the control. hooks.onLeave replaces the delayed hide.
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

/**
 * Makes Escape hide a shown tooltip, not close the dialog (WCAG 1.4.13). Call
 * it in the dialog's setup, before the tooltip adds its own listener.
 */
export function useKeepOpenForTip(isTipShown) {
  function keepOpen(event) {
    if (event.key === 'Escape' && isTipShown()) {
      event.preventDefault();
      event.stopPropagation();
    }
  }

  onMounted(() => {
    window.addEventListener('keydown', keepOpen, true);
  });

  onBeforeUnmount(() => {
    window.removeEventListener('keydown', keepOpen, true);
  });
}
