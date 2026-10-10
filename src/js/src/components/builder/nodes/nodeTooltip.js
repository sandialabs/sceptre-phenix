// Info tooltip of a device or a switch on the canvas (WCAG 1.4.13).
//
// The canvas has one tooltip for all its nodes (BuilderCanvas.vue provides
// it under NODE_TIP, see fixedTooltip.js). It shows what the node says about
// itself (see nodeInfo.js) after the pointer rests on the node. It shows
// immediately when the node takes keyboard focus. The pointer can move onto
// it. Escape closes it without moving focus or changing the selection.
//
// The tooltip stays while the node has keyboard focus, whatever the pointer
// does. When the pointer rests on another node, that node's tooltip shows.
// This node's tooltip shows again when the pointer leaves that node and its
// tooltip.
//
// The tooltip is hidden from assistive technology. The same facts describe
// the node as one sentence, in a hidden element the node component renders
// beside its node, inside Vue Flow's wrapper:
//   <span :id="infoId" class="builder-node__info" hidden>{{ infoText }}</span>

import { computed, inject, onBeforeUnmount, watch } from 'vue';
import { useNode } from '@vue-flow/core';

import { nodeInfoId } from '@/builder/adapters/vueflow.js';

/** What the canvas provides its nodes' tooltip under. */
export const NODE_TIP = 'builderNodeTip';

/**
 * @param {() => {rows: object[], text: string}} info the node's info (see
 *   deviceInfo and switchInfo)
 * @returns {{infoId: string, infoText: import('vue').ComputedRef<string>}}
 *   the id and the text of the element that describes the node
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
