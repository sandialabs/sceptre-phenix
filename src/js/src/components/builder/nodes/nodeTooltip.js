// Info tooltip of a device or a switch on the canvas (WCAG 1.4.13).
//
// The canvas has one tooltip for all its nodes (BuilderCanvas.vue provides
// it under NODE_TIP; see fixedTooltip.js). It shows what the node says about
// itself (see nodeInfo.js) once the pointer has rested on the node, and at
// once when the node takes keyboard focus. The pointer can move onto it, and
// Escape closes it without moving focus or changing the selection. It stays
// while the node has keyboard focus, whatever the pointer does: resting on
// another node shows that node's tooltip, and this one's is back once the
// pointer has left that node and its tooltip.
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

  // A click focuses the node too, and must not bring the tooltip up: only
  // focus the browser shows, which is focus from the keyboard, does. The
  // tooltip is then the node's to keep until it loses focus.
  function onFocus(event) {
    if (event.currentTarget.matches(':focus-visible')) {
      tooltip?.keep(event.currentTarget, () => content.value);
      tooltip?.showTip(event, content.value);
    }
  }

  // The node's tooltip goes, and so does its claim on it. Another node's
  // tooltip stays.
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
