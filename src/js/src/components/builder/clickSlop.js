// When a press and release on the canvas is a click, and when a drag of
// nodes is a click whose pointer wobbled. BuilderCanvas.vue uses it.

// How far the pointer may wander between press and release and still make
// a click, in CSS pixels on the screen, at any zoom. A finger or pen drifts more.
export const CLICK_SLOP_PX = 4;
export const TOUCH_SLOP_PX = 8;

/**
 * The client point of a mouse, pointer or touch event.
 *
 * @param {Event} [event]
 * @returns {{x: number, y: number}|null} null for an event without one, as
 *   a click from assistive technology can be
 */
export function pointOf(event) {
  const point = event?.changedTouches?.[0] || event;

  return typeof point?.clientX === 'number'
    ? { x: point.clientX, y: point.clientY }
    : null;
}

/**
 * The point of a press, with the slop for its input type.
 *
 * @param {Event} [event] the pointerdown or touchstart of the press
 * @returns {{x: number, y: number, slop: number}|null}
 */
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

/**
 * Whether the pointer of an event is within the slop of the press:
 * start.slop, or CLICK_SLOP_PX when the press has none.
 *
 * @param {{x: number, y: number, slop?: number}|null} start the press
 * @param {Event} [event] a later event of the same press
 * @returns {boolean} false when either point is not known
 */
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
 * Whether a drag of nodes that ended is a click, which moves nothing. Vue
 * Flow starts a drag on the press, so each click on a node comes as a
 * drag. It is a click when the pointer never went out of the slop of the
 * press, with or without the grid.
 *
 * @param {object} drag
 * @param {{x: number, y: number, slop?: number}|null} drag.start the press
 * @param {Event} [drag.event] the event that ended the drag
 * @param {boolean} [drag.strayed] whether the pointer went out of the slop
 *   during the drag
 * @returns {boolean}
 */
export function dragIsClick({ start, event, strayed }) {
  return !strayed && endedNear(start, event);
}
