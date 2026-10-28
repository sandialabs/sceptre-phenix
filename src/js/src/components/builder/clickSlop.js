// When a press and release on the canvas is a click, and when a drag of
// nodes is a click whose pointer wobbled. BuilderCanvas.vue uses it.

// How far the pointer may wander between press and release and still make
// a click, in CSS pixels on the screen, at any zoom. A finger or pen drifts more.
export const CLICK_SLOP_PX = 4;
export const TOUCH_SLOP_PX = 8;

/** The client point of a mouse, pointer or touch event, or null when none. */
export function pointOf(event) {
  const point = event?.changedTouches?.[0] || event;

  return typeof point?.clientX === 'number'
    ? { x: point.clientX, y: point.clientY }
    : null;
}

/** The point of a press (pointerdown or touchstart), with its input's slop. */
export function pressOf(event) {
  const point = pointOf(event);

  if (!point) {
    return null;
  }

  const touch =
    event.pointerType === 'touch' ||
    event.pointerType === 'pen' ||
    Boolean(event.changedTouches);

  return { ...point, slop: touch ? TOUCH_SLOP_PX : CLICK_SLOP_PX };
}

/** Whether a later event of the press is within its slop. False when unknown. */
export function endedNear(start, event) {
  const end = pointOf(event);

  return Boolean(
    start &&
      end &&
      Math.hypot(end.x - start.x, end.y - start.y) <=
        (start.slop ?? CLICK_SLOP_PX),
  );
}

/**
 * Whether a node drag that ended is a click. Vue Flow starts a drag on each
 * press, so a click is a drag that never left the slop of the press.
 */
export function dragIsClick({ start, event, strayed }) {
  return !strayed && endedNear(start, event);
}
