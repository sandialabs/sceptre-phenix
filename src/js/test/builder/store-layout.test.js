import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// The real layouts, which a test can replace for one run.
vi.mock('@/builder/layouts/index.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, runLayout: vi.fn(actual.runLayout) };
});

import { withGeometry } from '@/builder/layout.js';
import {
  LAYOUT_ALGORITHMS,
  LayoutError,
  runLayout,
} from '@/builder/layouts/index.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

const labelOf = (id) => LAYOUT_ALGORITHMS.find((a) => a.id === id).label;

// Auto layout runs the draft's own layout, or else the one the settings
// choose, and may finish later (ELK runs in a Web Worker in the browser).
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
      expect(store.announcement).toBe(`Applied ${labelOf(id)} layout`);
      expect(store.canRestoreLayout).toBe(true);
      // The setting is the viewer's; the draft keeps no choice of its own.
      expect(store.doc.layout).toBeUndefined();
      expect(store.currentLayout).toBe(id);
    },
  );

  test('the draft’s own layout comes before the setting', async () => {
    setSetting('layoutAlgorithm', 'standard', null);
    expect(store.currentLayout).toBe('standard');

    store.setDocument({ ...sampleDocument().doc, layout: 'cards' });
    expect(store.currentLayout).toBe('cards');
    await store.layout();
    expect(runLayout).toHaveBeenLastCalledWith('cards', expect.anything(), {});
    expect(store.announcement).toBe('Applied Network cards layout');

    // One this Builder does not know is ignored, and kept.
    store.setDocument({ ...sampleDocument().doc, layout: 'radial' });
    expect(store.currentLayout).toBe('standard');
    await store.layout();
    expect(runLayout).toHaveBeenLastCalledWith(
      'standard',
      expect.anything(),
      {},
    );
    expect(store.doc.layout).toBe('radial');
  });

  test('a chosen layout is kept with the draft in the same commit', async () => {
    const before = store.doc;
    const entries = store.history.size;

    await store.layout({ algorithm: 'dagre' });

    expect(store.doc.layout).toBe('dagre');
    expect(store.currentLayout).toBe('dagre');
    expect(store.doc).toEqual({
      ...withGeometry(before, await runLayout('dagre', before)),
      layout: 'dagre',
    });
    expect(store.history.size).toBe(entries + 1);

    // Chosen again, it runs again: laid out already, it changes nothing.
    expect(await store.layout({ algorithm: 'dagre' })).toBeNull();
    expect(store.announcement).toBe('The diagram is already laid out.');
    expect(store.history.size).toBe(entries + 1);

    // One undo takes back the positions and the choice.
    store.undo();
    expect(store.doc).toEqual(before);
    expect(store.currentLayout).toBe('elk');

    // Restore puts the choice back too.
    await store.layout({ algorithm: 'cards' });
    expect(store.doc.layout).toBe('cards');
    store.restoreLayout();
    expect(store.doc).toEqual(before);
  });

  test('choosing the layout already in place keeps the choice alone', async () => {
    await store.layout();
    const laid = store.doc;

    expect(await store.layout({ algorithm: 'elk' })).not.toBeNull();
    expect(store.announcement).toBe('Chose ELK layered layout');
    expect(store.doc).toEqual({ ...laid, layout: 'elk' });
  });

  test('keeps the routes a layout draws, and drops stale ones', async () => {
    const { doc, edge } = sampleDocument();
    const stale = [
      { x: 1, y: 1 },
      { x: 2, y: 2 },
    ];

    store.setDocument({
      ...doc,
      edges: doc.edges.map((entry) => ({ ...entry, route: stale })),
    });
    const before = store.doc;
    const route = [
      { x: 10, y: 20 },
      { x: 40, y: 20 },
      { x: 40, y: 60 },
    ];

    runLayout.mockImplementationOnce(async () => ({
      positions: {},
      routes: { [edge.id]: route, gone: route },
    }));
    await store.layout();
    expect(store.doc.edges.find((e) => e.id === edge.id).route).toEqual(route);

    // A layout that draws none drops the routes it would leave behind.
    runLayout.mockImplementationOnce(async () => ({ positions: {} }));
    await store.layout();
    expect(store.doc.edges.every((e) => e.route === undefined)).toBe(true);

    // Restore puts the routes back as they were.
    store.restoreLayout();
    expect(store.doc.edges.find((e) => e.id === edge.id).route).toEqual(route);
    store.undo();
    store.undo();
    store.undo();
    expect(store.doc).toEqual(before);
  });

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
