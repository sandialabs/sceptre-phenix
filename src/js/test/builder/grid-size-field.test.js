// @vitest-environment happy-dom
// The Grid size field of the Inspector's Diagram section, mounted and typed
// in: a size applies on change or Enter, and other text stays with an error.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount } from '@vue/test-utils';

import InspectorDiagram from '@/components/builder/inspector/InspectorDiagram.vue';

import { DEFAULT_GRID_SIZE } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import { mountInspector, type } from './inspector-mount.js';

enableAutoUnmount(afterEach);

const ERROR = 'Grid size must be a whole number from 4 to 200.';

// The Diagram section on the sample document.
async function openDiagram() {
  const { doc } = sampleDocument();
  const { wrapper, store } = await mountInspector({
    doc,
    component: InspectorDiagram,
  });

  return {
    wrapper,
    store,
    field: wrapper.get('[data-testid="inspector-grid-size-input"]'),
    error: () => wrapper.find('[data-testid="inspector-grid-size-error"]'),
  };
}

describe('the Inspector’s diagram Grid size', () => {
  test('applies a size the server takes as one step, and keeps other text with an error', async () => {
    const { store, field, error } = await openDiagram();
    const steps = store.history.entries.length;

    // Spaces around the number do not refuse it.
    await field.setValue(' 24 ');
    expect(store.doc.grid.size).toBe(24);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe('Changed the grid size to 24');
    expect(field.element.value).toBe('24');
    expect(field.attributes('aria-invalid')).toBeUndefined();
    expect(error().exists()).toBe(false);

    // Text that is no size stays, with an error, and changes nothing. The
    // input binds that text, so the render that shows the error keeps it.
    for (const text of ['0', '-8', '3', '201', '12.5', 'abc', '', '1e999']) {
      await field.setValue(text);
      expect(field.element.value, text).toBe(text);
      expect(field.attributes('aria-invalid'), text).toBe('true');
      expect(field.attributes('aria-describedby'), text).toBe(
        'inspector-grid-size-help inspector-grid-size-error',
      );
      expect(error().text(), text).toBe(ERROR);
      expect(store.doc.grid.size).toBe(24);
    }
    expect(store.history.entries).toHaveLength(steps + 1);

    // The same size, typed and entered, takes no step, and clears the error.
    await type(field, '24.0');
    await field.trigger('keydown', { key: 'Enter' });
    expect(field.element.value).toBe('24');
    expect(field.attributes('aria-invalid')).toBeUndefined();
    expect(error().exists()).toBe(false);
    expect(store.history.entries).toHaveLength(steps + 1);
  });

  test('Enter applies a size before the field changes', async () => {
    const { store, field } = await openDiagram();

    await type(field, '32');
    await field.trigger('keydown', { key: 'Enter' });
    expect(store.doc.grid.size).toBe(32);
    expect(store.history.undoLabel()).toBe('Changed the grid size to 32');

    // The change that follows as focus leaves takes no second step.
    const steps = store.history.entries.length;

    await field.trigger('change');
    expect(store.history.entries).toHaveLength(steps);
  });

  test('a size the store refuses puts the diagram’s size back in the field', async () => {
    const { store, field, error } = await openDiagram();

    store.resolvingConflict = true;
    await field.setValue('32');
    expect(store.doc.grid.size).toBe(DEFAULT_GRID_SIZE);
    expect(field.element.value).toBe(String(DEFAULT_GRID_SIZE));
    expect(error().exists()).toBe(false);
  });

  // Text the browser put in the field without an input event, as a value
  // replaced while it had focus: the size it commits shows as written.
  test('a size written another way shows as the size, with no input event before it', async () => {
    const { store, field } = await openDiagram();

    field.element.value = String(DEFAULT_GRID_SIZE);
    field.element.value = `${DEFAULT_GRID_SIZE}.0`;
    await field.trigger('change');
    expect(field.element.value).toBe(String(DEFAULT_GRID_SIZE));
    expect(store.doc.grid.size).toBe(DEFAULT_GRID_SIZE);
  });
});
