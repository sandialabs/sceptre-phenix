// Info tooltip of a device or a switch on the canvas (WCAG 1.4.13), one for all
// nodes (NODE_TIP). It is aria-hidden, because a hidden element inside Vue
// Flow's wrapper describes the node with the same facts.

import { computed, inject, onBeforeUnmount, watch } from 'vue';
import { useNode } from '@vue-flow/core';

import { nodeInfoId } from '@/builder/adapters/vueflow.js';

/** What the canvas provides its nodes' tooltip under. */
export const NODE_TIP = 'builderNodeTip';

/**
 * The id and text of the hidden element that describes the node (see deviceInfo
 * and switchInfo).
 */
export function useNodeInfo(info) {
  // Vue Flow's wrapper around the node component is the focusable element,
  // and the one the tooltip is placed by.
  const { id, nodeEl } = useNode();
  const tooltip = inject(NODE_TIP, null);
  const content = computed(info);

  // A pointer that enters with a button down is dragging a node or a new
  // connection.
  function onEnter(event) {
    if (event.buttons === 0) {
      tooltip?.showTip(event, () => content.value);
    }
  }

  function onLeave(event) {
    const element = event.currentTarget;

    if (tooltip?.shownOn(element) && !element.matches(':focus-visible')) {
      tooltip.scheduleHide();
    }
  }

  // A click also focuses the node, but must not show the tooltip. Only focus
  // that the browser shows (focus from the keyboard) shows the tooltip. The
  // node then keeps the tooltip until it loses focus.
  function onFocus(event) {
    if (event.currentTarget.matches(':focus-visible')) {
      tooltip?.keep(event.currentTarget, () => content.value);
      tooltip?.showTip(event, content.value);
    }
  }

  // Hide the node's tooltip and release the node's claim on it. Another
  // node's tooltip stays.
  function hide(element) {
    if (!element) {
      return;
    }

    tooltip?.release(element);

    if (tooltip?.shownOn(element)) {
      tooltip.hideTip();
    }
  }

  const onBlur = (event) => hide(event.currentTarget);

  function listen(element, method) {
    element?.[method]('mouseenter', onEnter);
    element?.[method]('mouseleave', onLeave);
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

  // An edit made while the tooltip shows (Undo, a paste) shows in it.
  watch(content, (next) => {
    const element = nodeEl.value;

    if (element && tooltip?.shownOn(element)) {
      tooltip.showTip({ currentTarget: element }, next);
    }
  });

  onBeforeUnmount(() => {
    listen(nodeEl.value, 'removeEventListener');
    hide(nodeEl.value);
  });

  return {
    infoId: nodeInfoId(id),
    infoText: computed(() => content.value.text),
  };
}
