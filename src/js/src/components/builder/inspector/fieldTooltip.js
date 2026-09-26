// The description tooltip on an Inspector field's label (WCAG 1.4.13): the
// schema's description of the field, shown while the pointer is over the
// label or the field has keyboard focus. It is the Builder's fixed tooltip
// (see fixedTooltip.js), beside the Inspector over the canvas, so it covers
// no field; it stays while the pointer moves onto it, and Escape dismisses it.
//
// Screen readers get the description once, as the field's accessible
// description (aria-describedby, to a hidden element), so the tooltip itself
// is aria-hidden. One field's tooltip shows at a time: pointing at a label
// while another field has focus replaces that field's tooltip.
//
// InspectorTooltip.vue renders it, so that showing, moving and hiding it
// re-renders only the tooltip, not the field.

import { unref } from 'vue';

import { useFixedTooltip } from '../fixedTooltip.js';

// Hides the field tooltip showing, if any.
let hideShown = null;

/**
 * @param {import('vue').MaybeRef<string>|(() => string)} text the tooltip's
 *   text; none shows no tooltip
 */
export function useFieldTooltip(text) {
  const { tip, tipEl, showTip, scheduleHide, hideTip } = useFixedTooltip({
    side: 'start',
  });

  /**
   * @param {HTMLElement} anchor the label (or legend) the tooltip belongs
   *   to, and is placed beside
   */
  function show(anchor) {
    const content = typeof text === 'function' ? text() : unref(text);

    if (!content || !anchor) {
      return;
    }

    if (hideShown && hideShown !== hideTip) {
      hideShown();
    }

    hideShown = hideTip;
    showTip({ currentTarget: anchor }, content);
  }

  // Focus shows it when the browser shows focus: from the keyboard, and in
  // a text field clicked into, where typing follows, but not in a checkbox
  // or select clicked with the pointer.
  function onFocusIn(event, anchor) {
    if (event.target?.matches?.(':focus-visible')) {
      show(anchor);
    }
  }

  return { tip, tipEl, show, scheduleHide, hide: hideTip, onFocusIn };
}
