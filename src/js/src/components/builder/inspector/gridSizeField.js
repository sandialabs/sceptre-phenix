// The Grid size field of the Inspector's Diagram section
// (InspectorDiagram.vue). A size applies when the field changes, as one
// undo step (the store's setGrid). Text that is no size the server takes
// stays in the field, with an error, and changes nothing.

import { computed, ref, watch } from 'vue';

import {
  DEFAULT_GRID_SIZE,
  isGridSize,
  MAX_GRID_SIZE,
  MIN_GRID_SIZE,
} from '@/builder/model.js';

import { readNumberText } from './control.js';

export const GRID_SIZE_ERROR = `Grid size must be a whole number from ${MIN_GRID_SIZE} to ${MAX_GRID_SIZE}.`;

/**
 * The state of the Grid size field. Its input binds gridSizeText in place of
 * the size, and commitGridSize applies that text.
 */
export function useGridSizeField(store) {
  const gridSize = computed(() => store.doc?.grid?.size || DEFAULT_GRID_SIZE);
  // What the field shows: the size, or the text typed in it. Each render
  // writes the bound value into the input, also when the value did not
  // change (Vue patches an input's value on every render). The render that
  // shows an error, or a render for any other change of the section, would
  // otherwise put the size back over the text typed.
  const gridSizeText = ref(String(gridSize.value));
  const gridSizeField = ref(null);
  const gridSizeError = ref('');

  // The field shows the diagram's size again: after an undo, a refusal, or
  // a size written in another way ("16.0" shows as 16).
  function showGridSize() {
    gridSizeError.value = '';
    gridSizeText.value = String(gridSize.value);

    // The text can be the size already, so that no render follows.
    if (gridSizeField.value) {
      gridSizeField.value.value = gridSizeText.value;
    }
  }

  function commitGridSize(field) {
    gridSizeText.value = field.value;

    const size = readNumberText(field.value, false);

    if (!isGridSize(size)) {
      gridSizeError.value = GRID_SIZE_ERROR;

      return;
    }

    if (size !== gridSize.value) {
      store.setGrid({ size });
    }

    showGridSize();
  }

  // An error is for the text in the field, so the field shows the size
  // again, without the error, when the size changes, when the draft becomes
  // read only or can be changed again (a read-only draft shows the size as
  // text), and when another diagram opens, which can have the same size.
  watch([gridSize, () => store.readOnly, () => store.openedSeq], showGridSize);

  return {
    gridSize,
    gridSizeText,
    gridSizeField,
    gridSizeError,
    commitGridSize,
  };
}
