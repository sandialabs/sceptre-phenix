// What the canvas gives the nodes that are edited with the pointer on it: the
// resizer of a note, a group, a shape or an icon (NodeResize.vue) and the
// handles of a line's points (LineNode.vue). BuilderCanvas.vue provides it.
// A node drawn without the canvas, as a test renders one, gets no context.
// It then offers neither the resizer nor the handles.

import { computed, inject, toValue } from 'vue';

export const CANVAS_EDITING = Symbol('builder canvas editing');

/**
 * The canvas's editing context {store, flowNode, zoom}, or null outside the
 * canvas. A flow node's computedPosition is where it is drawn, also during a
 * drag.
 */
export function useCanvasEditing() {
  return inject(CANVAS_EDITING, null);
}

/**
 * Whether a selected node shows its resize frame: only on the canvas, in a
 * draft the user can change.
 */
export function useResizeFrame(selected) {
  const canvas = useCanvasEditing();

  return computed(
    () =>
      Boolean(canvas) && Boolean(toValue(selected)) && !canvas.store.readOnly,
  );
}
