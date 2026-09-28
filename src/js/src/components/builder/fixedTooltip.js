// Tooltip for a control that shows a hint on hover and keyboard focus: the
// Palette's entries and help button, the canvas zoom controls, the
// toolbar's buttons, and the Inspector's field labels (WCAG 1.4.13). It stays while the pointer moves onto it, until the pointer
// leaves both, the control loses focus, or Escape dismisses it. The pointer
// on it does not bring up the tooltip of a control beneath it, and neither
// does a control scrolled under a pointer that stays still (see
// whenPointed).
//
// The tooltip is fixed to the viewport so a scrolling side panel cannot clip
// it; on scroll and resize it follows its control, and hides once the
// control scrolls out of view. It lets clicks through to what lies beneath,
// and only repeats text that reaches screen readers as the control's name or
// description, so it is aria-hidden.
//
// The component renders it as
//   <div v-if="tip" ref="tipEl" class="builder-tooltip builder-tooltip--fixed"
//     aria-hidden="true" :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
//     {{ tip.text }}
//   </div>
// and binds showTip(event, text) to mouseenter and focus, scheduleHide to
// mouseleave, and hideTip to blur.

import { nextTick, onBeforeUnmount, ref } from 'vue';

const TIP_GAP = 4;
const TIP_MARGIN = 8;
const HIDE_DELAY_MS = 200;

function clamp(value, min, max) {
  return Math.max(min, Math.min(value, Math.max(min, max)));
}

function within(rect, x, y) {
  return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom;
}

// Where and when the pointer last moved, for every tooltip. Scrolling
// moves a control under a pointer that stays still, as Tab does in a
// scrolling panel, and the browser then sends mouseenter with no
// pointermove near it in time. Moving the pointer onto the control sends
// both at once: pointermove just after mouseenter (Chromium) or just before
// it (Firefox).
const MOVE_WINDOW_MS = 100;
let lastPointer = null;
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
}

// Whether the pointer moved to where the mouseenter happened, when it did.
// Within a pixel: a pointer event's position has fractions, a mouse
// event's does not.
function pointerMovedTo({ x, y, time }) {
  return (
    lastPointer !== null &&
    Math.abs(lastPointer.x - x) < 1 &&
    Math.abs(lastPointer.y - y) < 1 &&
    Math.abs(lastPointer.time - time) <= MOVE_WINDOW_MS
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
 * @param {'end'|'start'|'below'} [options.side] the side of the control the
 *   tooltip prefers: 'end' (right) for the left-hand panels, 'start' (left)
 *   for the Inspector on the right, so it lies over the canvas in either
 *   case; 'below' for a row of controls such as the toolbar, so it covers
 *   none of the row
 * @param {(control: HTMLElement) => HTMLElement|null} [options.beside] the
 *   element the tooltip is placed beside, on the side nearer the control,
 *   for a control with others next to it, such as a table row's buttons:
 *   the tooltip covers none of them, and the pointer reaches it without
 *   crossing another
 */
export function useFixedTooltip({ side = 'end', beside = null } = {}) {
  const tip = ref(null);
  const tipEl = ref(null);

  let anchor = null;
  let hideTimer = null;

  trackPointer();

  function showTip(event, text) {
    const control = event.currentTarget;

    whenPointed(event, () => {
      cancelHide();
      anchor = control;
      tip.value = { text, top: -9999, left: -9999 };
      listen(true);
      nextTick(placeTip);
    });
  }

  // Beside the control, on its preferred side, or beside what `beside`
  // names, on the side nearer the control; when there is no room there (the
  // stacked narrow layout), below it at the end side, or above it at the
  // start side, which keeps the Inspector's field under its label clear;
  // always inside the viewport.
  function placeTip() {
    if (!tip.value || !anchor?.isConnected) {
      hideTip();
      return;
    }

    const rect = anchor.getBoundingClientRect();
    if (!inView(rect)) {
      hideTip();
      return;
    }

    const width = tipEl.value?.offsetWidth || 0;
    const height = tipEl.value?.offsetHeight || 0;
    const box = beside?.(anchor)?.getBoundingClientRect() || rect;
    let toward = side;

    if (box !== rect && side !== 'below') {
      toward = rect.left - box.left <= box.right - rect.right ? 'start' : 'end';
    }

    let left =
      toward === 'start' ? box.left - width - TIP_GAP : box.right + TIP_GAP;
    let top = rect.top;

    if (side === 'below') {
      left = rect.left;
      top = rect.bottom + TIP_GAP;
    } else if (toward === 'start' && left < TIP_MARGIN) {
      left = rect.left;
      top = rect.top - height - TIP_GAP;
    } else if (left + width > window.innerWidth - TIP_MARGIN) {
      left = rect.left;
      top = rect.bottom + TIP_GAP;
    }

    if (top + height > window.innerHeight - TIP_MARGIN) {
      top = rect.top - height - TIP_GAP;
    }

    tip.value = {
      ...tip.value,
      left: clamp(left, TIP_MARGIN, window.innerWidth - width - TIP_MARGIN),
      top: clamp(top, TIP_MARGIN, window.innerHeight - height - TIP_MARGIN),
    };
  }

  // The control is at least partly visible in the window and in the side
  // panel it scrolls with, if any.
  function inView(rect) {
    const panel = anchor.closest('.builder-layout__side');
    const bounds = panel?.getBoundingClientRect();
    const top = Math.max(0, bounds?.top ?? 0);
    const bottom = Math.min(window.innerHeight, bounds?.bottom ?? Infinity);

    return rect.bottom > top && rect.top < bottom;
  }

  let frame = null;
  function followAnchor() {
    if (frame === null) {
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
  // the pointer rests over it.
  let pointer = null;

  function scheduleHide() {
    cancelHide();
    hideTimer = setTimeout(() => {
      if (pointerOver(tipEl.value) || pointerOver(anchor)) {
        scheduleHide();
      } else {
        hideTip();
      }
    }, HIDE_DELAY_MS);
  }

  function pointerOver(element) {
    if (!pointer || !element?.isConnected) {
      return false;
    }

    return within(element.getBoundingClientRect(), pointer.x, pointer.y);
  }

  function onPointerMove(event) {
    pointer = { x: event.clientX, y: event.clientY };
  }

  function hideTip() {
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
    if (event.key === 'Escape') {
      hideTip();
    }
  }

  function listen(on) {
    const method = on ? 'addEventListener' : 'removeEventListener';

    window[method]('keydown', onKeydown, true);
    window[method]('scroll', followAnchor, true);
    window[method]('resize', followAnchor);
    window[method]('pointermove', onPointerMove, true);
    // Any click, on the canvas or on the control itself, is done with the
    // hint.
    window[method]('pointerdown', hideTip, true);
  }

  onBeforeUnmount(hideTip);

  return { tip, tipEl, showTip, scheduleHide, hideTip };
}
