// The description tooltip on an Inspector field's label (WCAG 1.4.13). It is
// aria-hidden, because the field's aria-describedby gives screen readers the
// text.
// Escape on a pointer-shown tooltip does nothing more, so it does not clear the
// canvas selection.

import { useFixedTooltip, whenPointed } from '../fixedTooltip.js';

// Hides the field tooltip showing, if any.
let hideShown = null;
// The fields that focusQuietly is focusing. A field stays in the set until
// its call ends.
const quiet = new WeakSet();

/**
 * Focuses a field and shows no tooltip for this focus, such as the first
 * field of a dialog as it opens. Hover and a later focus show it.
 */
export function focusQuietly(element, options) {
  if (!element) {
    return;
  }

  quiet.add(element);
  try {
    element.focus(options);
  } finally {
    quiet.delete(element);
  }
}

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
    if (!quiet.has(event.target) && event.target?.matches?.(':focus-visible')) {
      show(anchor);
    }
  }

  return { tip, setTipEl, show, scheduleHide, hide: hideTip, onFocusIn };
}
