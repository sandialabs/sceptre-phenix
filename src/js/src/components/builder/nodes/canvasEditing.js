// What the canvas gives the nodes that are edited with the pointer on it: the
// resizer of a note, a group, a shape or an icon (NodeResize.vue) and the
// handles of a line's points (LineNode.vue). BuilderCanvas.vue provides it.
// A node drawn without the canvas, as a test renders one, gets none, and
// then offers neither.

import { computed, inject, toValue } from 'vue';

export const CANVAS_EDITING = Symbol('builder canvas editing');

/**
 * The canvas's editing context, or null outside the canvas.
 *
 * @returns {{store: object, flowNode: (id: string) => object|undefined,
 *   zoom: () => number}|null} store: the Builder store; flowNode: Vue Flow's
 *   node of an id, whose computedPosition is where it is drawn, also while
 *   it is dragged; zoom: the canvas's zoom
 */
export function useCanvasEditing() {
  return inject(CANVAS_EDITING, null);
}

/**
 * Whether a node shows its resize frame (NodeResize.vue): it is selected,
 * on the canvas, in a draft the user can change. The node then hides its
 * selection check mark, which the frame's top right handle would cover.
 *
 * @param {boolean|(() => boolean)} selected whether the node is selected
 * @returns {import('vue').ComputedRef<boolean>} true while the frame shows
 */
export function useResizeFrame(selected) {
  const canvas = useCanvasEditing();

  return computed(
    () =>
      Boolean(canvas) && Boolean(toValue(selected)) && !canvas.store.readOnly,
  );
}
