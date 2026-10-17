// The description tooltip on an Inspector field's label (WCAG 1.4.13): the
// schema's description of the field, shown while the pointer is over the
// label or the field has keyboard focus. It is the Builder's fixed tooltip
// (see fixedTooltip.js), beside the Inspector over the canvas, so it covers
// no field. It stays while the pointer moves onto it, and Escape dismisses it.
// Escape that dismisses a tooltip the pointer showed does nothing more:
// keyboard focus can be on the canvas, where Escape would clear the
// selection, and with it the Inspector that the tooltip is part of.
//
// Screen readers get the description once, as the field's accessible
// description (aria-describedby, to a hidden element), so the tooltip itself
// is aria-hidden. One field's tooltip shows at a time: pointing at a label
// while another field has focus replaces that field's tooltip, unless the
// pointer is on that tooltip, which may cover the label.
//
// InspectorTooltip.vue renders it, so that showing, moving and hiding it
// re-renders only the tooltip, not the field.

import { useFixedTooltip, whenPointed } from '../fixedTooltip.js';

// Hides the field tooltip showing, if any.
let hideShown = null;

/**
 * @param {() => object|null} getContent the tooltip's content, what
 *   InspectorTooltip draws ({path, text}). None shows no tooltip
 */
export function useFieldTooltip(getContent) {
  // Whether the pointer showed the tooltip, and not keyboard focus.
  let pointed = false;
  const { tip, setTipEl, showTip, scheduleHide, hideTip } = useFixedTooltip({
    side: 'start',
    stopEscape: () => pointed,
  });

  /**
   * @param {HTMLElement} anchor the label (or legend) the tooltip belongs
   *   to, and is placed beside
   * @param {Event} [event] the mouseenter that shows it (see whenPointed)
   */
  function show(anchor, event) {
    const content = getContent();

    if (!content || !anchor) {
      return;
    }

    whenPointed(event, () => {
      if (hideShown && hideShown !== hideTip) {
        hideShown();
      }

      hideShown = hideTip;
      pointed = event?.type === 'mouseenter';
      showTip({ currentTarget: anchor }, content);
    });
  }

  // Focus shows it when the browser shows focus: from the keyboard, and in
  // a text field clicked into, where typing follows, but not in a checkbox
  // or select clicked with the pointer.
  function onFocusIn(event, anchor) {
    if (event.target?.matches?.(':focus-visible')) {
      show(anchor);
    }
  }

  return { tip, setTipEl, show, scheduleHide, hide: hideTip, onFocusIn };
}
