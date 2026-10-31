// The diagram's grid size: the server's rule, the store's setGrid, and the
// Grid size field of the Inspector's Diagram section as rendered (typing in
// it is in grid-size-field.test.js).

import { describe, expect, test } from 'vitest';
import { effectScope, nextTick } from 'vue';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import {
  GRID_SIZE_ERROR,
  useGridSizeField,
} from '@/components/builder/inspector/gridSizeField.js';

import {
  DEFAULT_GRID_SIZE,
  isGridSize,
  MAX_GRID_SIZE,
  MIN_GRID_SIZE,
  setGrid,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument, tags } from './fixtures.js';
import { renderWithStore } from './render.js';

const NOT_SIZES = [
  0,
  -16,
  3,
  201,
  1e6,
  0.001,
  12.5,
  NaN,
  Infinity,
  -Infinity,
  '16',
  null,
  undefined,
];

describe('a grid size', () => {
  test('is a whole number from 4 to 200, as the server takes it', () => {
    const { doc } = sampleDocument();
    const codes = (size) =>
      validateDocument(setGridSize(doc, size)).map((issue) => issue.code);

    expect([MIN_GRID_SIZE, MAX_GRID_SIZE]).toEqual([4, 200]);
    for (const size of [4, 16, 24, 200]) {
      expect(isGridSize(size), String(size)).toBe(true);
      expect(codes(size), String(size)).not.toContain('document.grid.invalid');
    }
    for (const size of NOT_SIZES) {
      expect(isGridSize(size), String(size)).toBe(false);
    }
    for (const size of [0, -16, 3, 201, 1e6, 0.001, 12.5]) {
      expect(codes(size), String(size)).toContain('document.grid.invalid');
    }
  });

  test('changes only through setGrid, which keeps the grid’s other fields', () => {
    const { doc } = sampleDocument();
    const next = setGrid(doc, { size: 24 });

    expect(next.grid).toEqual({ ...doc.grid, size: 24 });
    expect(doc.grid.size).toBe(DEFAULT_GRID_SIZE);
    // The same size, or one the server would refuse, changes nothing.
    expect(setGrid(doc, { size: DEFAULT_GRID_SIZE })).toBe(doc);
    for (const size of NOT_SIZES) {
      expect(setGrid(doc, { size }), String(size)).toBe(doc);
    }
  });
});

// The document with its grid size set as it is, a size the server refuses
// too, without setGrid's check.
function setGridSize(doc, size) {
  return { ...doc, grid: { ...doc.grid, size } };
}

describe("the store's setGrid", () => {
  function storeWith(doc) {
    const store = useBuilderStore(createPinia());

    store.commit(doc, '');

    return store;
  }

  test('is one undo step, named by the new size', () => {
    const store = storeWith(sampleDocument().doc);
    const steps = store.history.entries.length;

    expect(store.setGrid({ size: 24 })).toBeTruthy();
    expect(store.doc.grid.size).toBe(24);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe('Changed the grid size to 24');
    expect(store.announcement).toMatch(/^Changed the grid size to 24\b/);

    store.undo();
    expect(store.doc.grid.size).toBe(DEFAULT_GRID_SIZE);
  });

  test('takes no step for no change, a size the server refuses, or a read-only draft', () => {
    const store = storeWith(sampleDocument().doc);
    const steps = store.history.entries.length;

    expect(store.setGrid({ size: DEFAULT_GRID_SIZE })).toBeNull();
    expect(store.setGrid({ size: 0 })).toBeNull();
    store.readOnly = true;
    expect(store.setGrid({ size: 24 })).toBeNull();
    expect(store.error).toBe('This draft is read only.');
    expect(store.doc.grid.size).toBe(DEFAULT_GRID_SIZE);
    expect(store.history.entries).toHaveLength(steps);
  });
});

// The Inspector open on the diagram section.
async function openInspector(doc, { readOnly = false } = {}) {
  const { store, html } = await renderWithStore(BuilderInspector, {
    prepare(store) {
      store.commit(doc, '');
      store.readOnly = readOnly;
      store.select({ nodes: [] });
    },
  });

  return { store, html };
}

describe('the Inspector’s diagram Grid size', () => {
  const fieldOf = (html) =>
    tags(html, 'input').find((tag) =>
      tag.includes('data-testid="inspector-grid-size-input"'),
    ) || '';

  test('is a text field with a number keyboard, named by its heading and described by its help', async () => {
    const { doc } = sampleDocument();
    const { html } = await openInspector(setGrid(doc, { size: 24 }));
    const field = fieldOf(html);

    expect(field).toContain('type="text"');
    expect(field).toContain('inputmode="numeric"');
    expect(field).toContain('value="24"');
    expect(field).toContain('aria-labelledby="inspector-grid-size-label"');
    expect(field).toContain('aria-describedby="inspector-grid-size-help"');
    expect(field).not.toContain('aria-invalid');
    expect(html).toMatch(
      /<h3 id="inspector-grid-size-label"[^>]*>\s*Grid size\s*<\/h3>/,
    );
    expect(html).toMatch(
      /<p id="inspector-grid-size-help"[^>]*>\s*The distance between the lines of the grid, from 4 to\s+200 pixels\. When Snap to grid is on in Builder settings,\s+nodes go to this grid\.\s*<\/p>/,
    );
    expect(html).not.toContain('data-testid="inspector-grid-size-value"');
  });

  test('of a read-only draft is text', async () => {
    const { doc } = sampleDocument();
    const { html } = await openInspector(setGrid(doc, { size: 20 }), {
      readOnly: true,
    });

    expect(fieldOf(html)).toBe('');
    expect(html).toMatch(
      /data-testid="inspector-grid-size-value"[^>]*>\s*20 pixels\s*</,
    );
    expect(html).toContain('id="inspector-grid-size-help"');
  });
});

describe('the Grid size field’s error', () => {
  // The field's state on a store, outside a component, as the Inspector
  // has it, with the text "abc" refused.
  function refused() {
    const pinia = createPinia();
    const store = useBuilderStore(pinia);
    const { doc } = sampleDocument();

    store.commit(setGrid(doc, { size: 24 }), '');
    const state = effectScope().run(() => useGridSizeField(store));
    const field = { value: 'abc' };

    state.gridSizeField.value = field;
    state.commitGridSize(field);
    expect(state.gridSizeError.value).toBe(GRID_SIZE_ERROR);
    expect(field.value).toBe('abc');
    expect(state.gridSizeText.value).toBe('abc');

    return { store, state, field };
  }

  test('goes, with the text, when the draft becomes read only or can be changed again', async () => {
    const { store, state, field } = refused();

    store.readOnly = true;
    await nextTick();
    expect(state.gridSizeError.value).toBe('');
    expect(field.value).toBe('24');
    expect(state.gridSizeText.value).toBe('24');

    field.value = 'abc';
    state.commitGridSize(field);
    store.readOnly = false;
    await nextTick();
    expect(state.gridSizeError.value).toBe('');
    expect(field.value).toBe('24');
    expect(state.gridSizeText.value).toBe('24');
  });

  test('goes, with the text, when another diagram of the same size opens', async () => {
    const { store, state, field } = refused();

    store.openedSeq += 1;
    await nextTick();
    expect(state.gridSizeError.value).toBe('');
    expect(field.value).toBe('24');
    expect(state.gridSizeText.value).toBe('24');
  });

  test('goes, with the text, when the size changes', async () => {
    const { store, state, field } = refused();

    store.setGrid({ size: 32 });
    await nextTick();
    expect(state.gridSizeError.value).toBe('');
    expect(field.value).toBe('32');
    expect(state.gridSizeText.value).toBe('32');
  });

  test('stays while nothing it is for changes', async () => {
    const { store, state, field } = refused();

    store.setDiagramNotes(['A note.']);
    await nextTick();
    expect(state.gridSizeError.value).toBe(GRID_SIZE_ERROR);
    expect(field.value).toBe('abc');
    expect(state.gridSizeText.value).toBe('abc');
  });
});
