// Comment tooltip for a canvas node (WCAG 1.4.13).
//
// The tooltip shows while the pointer is over the node or the node has
// keyboard focus. It is rendered inside the node, so moving the pointer onto
// it keeps it open, and Escape closes it without moving focus or changing
// the selection. It only repeats the comment for sighted users: the node's
// accessible name already ends with it.

import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useNode } from '@vue-flow/core';

/**
 * @param {() => boolean} enabled whether the node has a tooltip to show
 */
export function useNodeTooltip(enabled) {
  // Vue Flow's wrapper around the node component is the focusable element.
  const { nodeEl } = useNode();
  const hovered = ref(false);
  const focused = ref(false);
  const dismissed = ref(false);
  const open = computed(
    () => enabled() && (hovered.value || focused.value) && !dismissed.value,
  );

  const onFocus = () => {
    focused.value = true;
    dismissed.value = false;
  };
  const onBlur = () => {
    focused.value = false;
  };

  // Escape closes the tooltip wherever focus is. Pressed on the node itself,
  // it stops there, so the canvas does not also clear the selection.
  const onEscape = (event) => {
    if (event.key !== 'Escape') {
      return;
    }

    dismissed.value = true;

    if (nodeEl.value?.contains(event.target)) {
      event.stopPropagation();
    }
  };

  function listen(element, method) {
    element?.[method]('focus', onFocus);
    element?.[method]('blur', onBlur);
  }

  watch(
    nodeEl,
    (element, previous) => {
      listen(previous, 'removeEventListener');
      listen(element, 'addEventListener');
    },
    { immediate: true },
  );

  watch(open, (value) => {
    const method = value ? 'addEventListener' : 'removeEventListener';
    document[method]('keydown', onEscape, true);
  });

  onBeforeUnmount(() => {
    listen(nodeEl.value, 'removeEventListener');
    document.removeEventListener('keydown', onEscape, true);
  });

  return {
    tooltipOpen: open,
    showTooltip() {
      hovered.value = true;
      dismissed.value = false;
    },
    hideTooltip() {
      hovered.value = false;
    },
  };
}
