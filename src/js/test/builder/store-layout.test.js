import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import { withGeometry } from '@/builder/layout.js';
import { LayoutError, runLayout } from '@/builder/layouts/index.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

// Auto layout runs the algorithm the settings choose, and may finish later
// (ELK runs in a Web Worker in the browser).
describe('automatic layout in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    store.setDocument(sampleDocument().doc);
  });

  afterEach(() => {
    resetSettings(null);
  });

  test.each(['elk', 'cards', 'dagre', 'standard'])(
    'lays the diagram out with the %s setting',
    async (id) => {
      const doc = store.doc;

      setSetting('layoutAlgorithm', id, null);

      const entry = await store.layout();

      expect(entry).not.toBeNull();
      expect(store.doc).toEqual(withGeometry(doc, await runLayout(id, doc)));
      expect(store.announcement).toBe('Applied automatic layout');
      expect(store.canRestoreLayout).toBe(true);
    },
  );

  test('is busy while it runs, and turns a second request away', async () => {
    const entries = store.history.size;
    const first = store.layout();

    expect(store.layoutRunning).toBe(true);
    expect(await store.layout()).toBeNull();
    expect(store.announcement).toBe('Auto layout is still running.');

    expect(await first).not.toBeNull();
    expect(store.layoutRunning).toBe(false);
    // One commit.
    expect(store.history.size).toBe(entries + 1);
  });

  test('a diagram that changes meanwhile keeps the change', async () => {
    const running = store.layout();
    const added = store.addNode({
      kind: 'device',
      hostname: 'late',
      position: { x: 900, y: 900 },
    });
    const edited = store.doc;

    expect(await running).toBeNull();
    expect(store.doc).toBe(edited);
    expect(
      store.doc.nodes.find((node) => node.id === added.id).position,
    ).toEqual({ x: 900, y: 900 });
    expect(store.announcement).toBe(
      'The diagram changed during Auto layout, so the layout was not applied.',
    );
    expect(store.canRestoreLayout).toBe(false);

    // Panning and zooming are no change.
    const panned = store.layout();

    store.setViewport({ x: 40, y: 20, zoom: 2 });
    expect(await panned).not.toBeNull();
    expect(store.doc.viewport).toEqual({ x: 40, y: 20, zoom: 2 });
  });

  test('a read-only draft is not laid out', async () => {
    const doc = store.doc;

    store.readOnly = true;

    expect(await store.layout()).toBeNull();
    expect(store.error).toBe('This draft is read only.');
    expect(store.doc).toBe(doc);
    expect(store.layoutRunning).toBe(false);
  });

  test('a layout that fails says so, and the next one runs', async () => {
    const doc = store.doc;
    const broken = {
      layout: () =>
        Promise.reject(new LayoutError('The layout took too long.')),
    };

    expect(await store.layout({ algorithm: 'elk', elk: broken })).toBeNull();
    expect(store.error).toBe('Auto layout failed. The layout took too long.');
    expect(store.doc).toBe(doc);
    expect(store.layoutRunning).toBe(false);

    expect(await store.layout({ algorithm: 'cards' })).not.toBeNull();
  });

  test('a layout library’s own error is not shown, but logged', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
    const thrown = new Error(
      'Not possible to find intersection inside of the rectangle',
    );
    const broken = { layout: () => Promise.reject(thrown) };

    try {
      expect(await store.layout({ algorithm: 'elk', elk: broken })).toBeNull();
      expect(store.error).toBe(
        'Auto layout failed. The layout could not be computed.',
      );
      expect(logged).toHaveBeenCalledWith('Auto layout failed.', thrown);
    } finally {
      logged.mockRestore();
    }
  });

  test('a layout stopped as the Builder closes says nothing', async () => {
    const doc = store.doc;
    const stopped = {
      layout: () =>
        Promise.reject(
          new DOMException('The layout was stopped.', 'AbortError'),
        ),
    };

    expect(await store.layout({ algorithm: 'elk', elk: stopped })).toBeNull();
    expect(store.error).toBe('');
    expect(store.doc).toBe(doc);
    expect(store.layoutRunning).toBe(false);
  });
});
