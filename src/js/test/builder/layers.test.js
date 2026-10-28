import { beforeEach, describe, expect, test } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { layerChoices } from '@/components/builder/inspector/InspectorLayer.vue';
import {
  addLayer,
  addNode,
  createDocument,
  effectiveLayerList,
  effectiveLayers,
  effectiveNodeLayer,
  findNode,
  isLayerId,
  layerCounts,
  layerIdOf,
  layerLabel,
  layerLabelProblem,
  layerName,
  layerTitles,
  layersProblem,
  MAX_LAYER_ID,
  MAX_LAYERS,
  nextLayerId,
  nodeLayer,
  normalizedLayers,
  placeholderLayers,
  relabelLayer,
  removeLayer,
  renumberLayer,
  setLayers,
  setNodeLayer,
  setNodeLayers,
  someNodeOnLayer,
  updateNode,
  withLayerAdded,
  withLayerRelabeled,
  withLayerRemoved,
  withLayerRenumbered,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import {
  nodeOptionsFromTemplate,
  templateDocument,
  templateFromNode,
} from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { errorsOf, sampleDocument } from './fixtures.js';

const PLANT = [
  { id: 4, label: 'Business' },
  { id: 2, label: 'Supervisory control' },
  { id: 1, label: 'Control' },
];

// The layers of a diagram: each an ID and a label, the highest ID at the
// top of the By layer layout.
describe('the layers of a diagram', () => {
  test('are 7 placeholders, the highest ID first, while it defines none', () => {
    const { doc } = sampleDocument();

    expect(placeholderLayers()).toEqual([
      { id: 6, label: 'Layer 6' },
      { id: 5, label: 'Layer 5' },
      { id: 4, label: 'Layer 4' },
      { id: 3, label: 'Layer 3' },
      { id: 2, label: 'Layer 2' },
      { id: 1, label: 'Layer 1' },
      { id: 0, label: 'Layer 0' },
    ]);
    expect(effectiveLayers(doc)).toEqual(placeholderLayers());
    expect(effectiveLayers({ ...doc, layers: [] })).toEqual(
      placeholderLayers(),
    );
    expect('layers' in doc).toBe(false);
  });

  test('are its own, the highest ID first, in whatever order it keeps them', () => {
    const { doc } = sampleDocument();
    const own = { ...doc, layers: [...PLANT].reverse() };

    expect(effectiveLayers(own)).toEqual(PLANT);
    expect(effectiveLayerList(PLANT)).toEqual(PLANT);
    expect(effectiveLayerList(null)).toEqual(placeholderLayers());
  });

  test('label an ID the diagram does not define "Layer N"', () => {
    const doc = { ...createDocument(), layers: PLANT };

    expect(layerLabel(doc, 2)).toBe('Supervisory control');
    expect(layerLabel(doc, 42)).toBe('Layer 42');
    expect(layerLabel(createDocument(), 3)).toBe('Layer 3');
  });

  test('a message names a layer by its label, with "layer" before a label that does not say it', () => {
    const doc = {
      ...createDocument(),
      layers: [...PLANT, { id: 9, label: 'DMZ layer' }],
    };

    expect(layerName(doc, 2)).toBe('layer Supervisory control');
    expect(layerName(doc, 9)).toBe('DMZ layer');
    expect(layerName(doc, 42)).toBe('Layer 42');
    expect(layerName(createDocument(), 0)).toBe('Layer 0');
  });

  test('add the ID to a label that another layer has', () => {
    const doc = {
      ...createDocument(),
      layers: [
        { id: 4, label: 'Control' },
        { id: 3, label: 'Control' },
        { id: 2, label: 'Layer 5' },
        { id: 1, label: 'Field' },
      ],
    };

    expect(layerTitles(doc.layers)).toEqual(
      new Map([
        [4, 'Control (ID 4)'],
        [3, 'Control (ID 3)'],
        [2, 'Layer 5'],
        [1, 'Field'],
      ]),
    );
    expect(layerLabel(doc, 3)).toBe('Control (ID 3)');
    expect(layerLabel(doc, 1)).toBe('Field');
    expect(layerLabel(doc, 5)).toBe('Layer 5 (ID 5)');
    expect(layerLabel(doc, 2)).toBe('Layer 5');
    expect(layerName(doc, 4)).toBe('layer Control (ID 4)');
    // The Inspector's choices add the node's own layer, then name them.
    expect(layerChoices(doc.layers, 5).map((choice) => choice.title)).toEqual([
      'Layer 5 (ID 5)',
      'Control (ID 4)',
      'Control (ID 3)',
      'Layer 5 (ID 2)',
      'Field',
    ]);
  });

  test('take IDs from 0 to 99, as numbers or digits', () => {
    for (const id of [0, 7, MAX_LAYER_ID]) {
      expect(isLayerId(id), id).toBe(true);
    }

    for (const id of [-1, 100, 2.5, '2', null, undefined, NaN]) {
      expect(isLayerId(id), String(id)).toBe(false);
    }

    expect(layerIdOf(3)).toBe(3);
    expect(layerIdOf('3')).toBe(3);
    expect(layerIdOf(' 12 ')).toBe(12);
    expect(layerIdOf('')).toBeNull();
    expect(layerIdOf(null)).toBeNull();
    expect(layerIdOf(undefined)).toBeNull();
    expect(layerIdOf('100')).toBeUndefined();
    expect(layerIdOf('2.5')).toBeUndefined();
    expect(layerIdOf('Control')).toBeUndefined();
  });

  test('refuse what the server refuses, with a sentence for the editor', () => {
    expect(layerLabelProblem('Control')).toBe('');
    expect(layerLabelProblem(' ')).toBe('Type a label for the layer.');
    expect(layerLabelProblem(undefined)).toBe('Type a label for the layer.');
    expect(layerLabelProblem('é'.repeat(32))).toBe('');
    expect(layerLabelProblem('é'.repeat(33))).toBe(
      'A layer label holds at most 64 bytes in UTF-8.',
    );
    expect(layerLabelProblem('Basic\tcontrol')).toBe(
      'A layer label cannot hold control characters, such as a tab.',
    );

    expect(layersProblem(PLANT)).toBe('');
    expect(layersProblem([])).toBe('');
    expect(layersProblem('Control')).toBe('The layers are not a list.');
    expect(
      layersProblem(
        Array.from({ length: MAX_LAYERS + 1 }, (_, id) => ({
          id,
          label: `L${id}`,
        })),
      ),
    ).toBe('A diagram has at most 32 layers.');
    expect(layersProblem([{ id: 100, label: 'Top' }])).toBe(
      'A layer ID is an integer from 0 to 99.',
    );
    expect(
      layersProblem([
        { id: 1, label: 'A' },
        { id: 1, label: 'B' },
      ]),
    ).toBe('Two layers use layer ID 1.');
    expect(layersProblem([{ id: 1, label: '' }])).toBe(
      'Type a label for the layer.',
    );
  });

  test('are kept sorted, the highest ID first, and only the placeholders are none', () => {
    expect(normalizedLayers([...PLANT].reverse())).toEqual(PLANT);
    expect(normalizedLayers([...placeholderLayers()].reverse())).toEqual([]);
  });
});

describe('editing a list of layers', () => {
  test('a new layer takes the next ID, after the placeholders when there are none', () => {
    expect(nextLayerId([])).toBe(7);
    expect(nextLayerId(PLANT)).toBe(5);
    expect(nextLayerId([{ id: MAX_LAYER_ID, label: 'Top' }])).toBe(0);

    const added = withLayerAdded([]);

    expect(added.layer).toEqual({ id: 7, label: 'Layer 7' });
    expect(added.layers.map((layer) => layer.id)).toEqual([
      7, 6, 5, 4, 3, 2, 1, 0,
    ]);
    expect(withLayerAdded(PLANT, 'Enterprise')).toEqual({
      layers: [{ id: 5, label: 'Enterprise' }, ...PLANT],
      layer: { id: 5, label: 'Enterprise' },
    });
    expect(withLayerAdded(PLANT, ' ')).toBeNull();

    const full = Array.from({ length: MAX_LAYERS }, (_, id) => ({
      id,
      label: `L${id}`,
    }));

    expect(nextLayerId(full)).toBeNull();
    expect(withLayerAdded(full)).toBeNull();
  });

  test('a new label, a new ID or a removal applies to the placeholders too', () => {
    expect(withLayerRelabeled([], 3, 'Operations')).toEqual(
      placeholderLayers().map((layer) =>
        layer.id === 3 ? { id: 3, label: 'Operations' } : layer,
      ),
    );
    expect(withLayerRemoved([], 6).map((layer) => layer.id)).toEqual([
      5, 4, 3, 2, 1, 0,
    ]);
    expect(withLayerRenumbered([], 6, 9).map((layer) => layer.id)).toEqual([
      9, 5, 4, 3, 2, 1, 0,
    ]);
  });

  test('a change back to the placeholders leaves none', () => {
    const relabeled = withLayerRelabeled([], 3, 'Operations');

    expect(withLayerRelabeled(relabeled, 3, 'Layer 3')).toEqual([]);
    expect(withLayerRemoved([{ id: 2, label: 'Control' }], 2)).toEqual([]);
  });

  test('refuse an unknown layer, a taken or wrong ID and a wrong label', () => {
    expect(withLayerRelabeled(PLANT, 3, 'Operations')).toBeNull();
    expect(withLayerRelabeled(PLANT, 2, '')).toBeNull();
    expect(withLayerRelabeled(PLANT, 2, 'a\nb')).toBeNull();
    expect(withLayerRenumbered(PLANT, 2, 4)).toBeNull();
    expect(withLayerRenumbered(PLANT, 2, 100)).toBeNull();
    expect(withLayerRenumbered(PLANT, 3, 5)).toBeNull();
    expect(withLayerRenumbered(PLANT, 2, '3')).toEqual([
      { id: 4, label: 'Business' },
      { id: 3, label: 'Supervisory control' },
      { id: 1, label: 'Control' },
    ]);
    expect(withLayerRemoved(PLANT, 3)).toBeNull();
  });
});

describe('the layers of a document', () => {
  test('are written sorted, and left out when there are none', () => {
    const { doc } = sampleDocument();
    const set = setLayers(doc, [...PLANT].reverse());

    expect(set.layers).toEqual(PLANT);
    expect(errorsOf(set)).toEqual([]);
    expect(setLayers(set, PLANT)).toBe(set);
    expect(JSON.stringify(setLayers(set, []))).toBe(JSON.stringify(doc));
    expect(setLayers(doc, [])).toBe(doc);
    expect(setLayers(doc, placeholderLayers())).toBe(doc);
    // A list the server would refuse changes nothing.
    expect(setLayers(set, [{ id: 1, label: '' }])).toBe(set);
  });

  test('a new document starts with the layers it is given', () => {
    expect(createDocument({ layers: PLANT }).layers).toEqual(PLANT);
    expect('layers' in createDocument({ layers: [] })).toBe(false);
    expect('layers' in createDocument({ layers: [{ id: -1 }] })).toBe(false);
  });

  test('a layer is added, relabeled, renumbered and removed', () => {
    const { doc } = sampleDocument();
    const added = addLayer(doc, 'Enterprise');

    expect(added.layer).toEqual({ id: 7, label: 'Enterprise' });
    expect(effectiveLayers(added.doc)).toHaveLength(8);

    const relabeled = relabelLayer(added.doc, 7, 'Enterprise network');

    expect(layerLabel(relabeled, 7)).toBe('Enterprise network');
    expect(relabelLayer(relabeled, 7, 'Enterprise network')).toBe(relabeled);
    expect(relabelLayer(relabeled, 50, 'Nothing')).toBe(relabeled);

    const renumbered = renumberLayer(relabeled, 7, 9);

    expect(layerLabel(renumbered, 9)).toBe('Enterprise network');
    expect(renumberLayer(renumbered, 9, 9)).toBe(renumbered);
    expect(renumberLayer(renumbered, 9, 6)).toBe(renumbered);

    const { doc: removed, nodeIds } = removeLayer(renumbered, 9);

    expect(nodeIds).toEqual([]);
    expect(effectiveLayers(removed)).toEqual(placeholderLayers());
    expect('layers' in removed).toBe(false);
    expect(removeLayer(removed, 50).doc).toBe(removed);
  });

  test('a renumbered layer moves its nodes and templates, and a removed one leaves them at its ID', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    let next = setLayers(doc, PLANT);

    next = setNodeLayer(next, alpha.id, 2);
    next = setNodeLayer(next, sw.id, 2);
    next = setNodeLayer(next, bravo.id, 1);
    next = {
      ...next,
      templates: [
        {
          id: 'f6b0c1a4-3d9e-4c1b-9a51-2f6c7d8e9a01',
          name: 'HMI',
          device: { layer: 2, spec: {} },
        },
      ],
    };

    const renumbered = renumberLayer(next, 2, 3);

    expect(nodeLayer(findNode(renumbered, alpha.id))).toBe(3);
    expect(nodeLayer(findNode(renumbered, sw.id))).toBe(3);
    expect(nodeLayer(findNode(renumbered, bravo.id))).toBe(1);
    expect(renumbered.templates[0].device.layer).toBe(3);
    expect(layerCounts(renumbered)).toEqual(
      new Map([
        [3, 2],
        [1, 1],
      ]),
    );

    const { doc: removed, nodeIds } = removeLayer(renumbered, 3);

    expect(nodeIds.sort()).toEqual([alpha.id, sw.id].sort());
    expect(nodeLayer(findNode(removed, alpha.id))).toBe(3);
    expect(nodeLayer(findNode(removed, sw.id))).toBe(3);
    expect(removed.templates[0].device.layer).toBe(3);
    expect(nodeLayer(findNode(removed, bravo.id))).toBe(1);
    expect(effectiveLayers(removed).some((layer) => layer.id === 3)).toBe(
      false,
    );
    expect(layerLabel(removed, 3)).toBe('Layer 3');
  });

  test('someNodeOnLayer: a node chosen for a layer or matched to one, and it stops at the first', () => {
    const { doc, alpha } = sampleDocument();

    expect(someNodeOnLayer(doc)).toBe(false);
    expect(someNodeOnLayer(setLayers(doc, PLANT))).toBe(false);
    expect(someNodeOnLayer(setNodeLayer(doc, alpha.id, 42))).toBe(true);
    expect(
      someNodeOnLayer(
        setLayers(doc, [
          { id: 1, label: 'Field', matchers: { 'general.hostname': '^bravo' } },
        ]),
      ),
    ).toBe(true);
    expect(someNodeOnLayer(undefined)).toBe(false);

    // The first node at a layer ends the search: the next is never read.
    const first = setNodeLayer(doc, alpha.id, 1);
    const unread = new Proxy(
      {},
      {
        get() {
          throw new Error('read a node after the first at a layer');
        },
      },
    );

    expect(
      someNodeOnLayer({ ...first, nodes: [findNode(first, alpha.id), unread] }),
    ).toBe(true);
  });

  test('an included device keeps its layer when its layer is renumbered or removed', () => {
    const { doc, alpha } = sampleDocument();
    const included = {
      ...setLayers(doc, PLANT),
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? {
              ...node,
              device: { ...node.device, layer: 2, includedFrom: 'plant' },
            }
          : node,
      ),
    };

    expect(nodeLayer(findNode(renumberLayer(included, 2, 3), alpha.id))).toBe(
      2,
    );
    expect(nodeLayer(findNode(removeLayer(included, 2).doc, alpha.id))).toBe(2);
  });
});

// The layer of a device or a switch: an ID, which the diagram need not
// define, kept in the diagram only.
describe('the layer of a node', () => {
  test('is set on a device and a switch, and removed again', () => {
    const { doc, alpha, sw } = sampleDocument();
    const set = setNodeLayer(setNodeLayer(doc, alpha.id, 0), sw.id, '42');

    expect(findNode(set, alpha.id).device.layer).toBe(0);
    expect(findNode(set, sw.id).switch.layer).toBe(42);
    expect(nodeLayer(findNode(set, alpha.id))).toBe(0);
    expect(errorsOf(set)).toEqual([]);

    // None leaves the document as it was, byte for byte.
    const cleared = setNodeLayer(setNodeLayer(set, alpha.id, null), sw.id, '');

    expect(JSON.stringify(cleared)).toBe(JSON.stringify(doc));
  });

  test('changes nothing for another kind, a wrong ID or the same layer', () => {
    const { doc, alpha } = sampleDocument();
    const note = addNode(doc, { kind: 'note', text: 'Read me' });
    const set = setNodeLayer(doc, alpha.id, 2);

    expect(setNodeLayer(note.doc, note.node.id, 2)).toBe(note.doc);
    expect(setNodeLayer(doc, alpha.id, 100)).toBe(doc);
    expect(setNodeLayer(doc, alpha.id, 'Layer 2')).toBe(doc);
    expect(setNodeLayer(doc, alpha.id, 2.5)).toBe(doc);
    expect(setNodeLayer(set, alpha.id, 2)).toBe(set);
    expect(setNodeLayer(set, alpha.id, '2')).toBe(set);
    expect(setNodeLayer(doc, alpha.id, null)).toBe(doc);
    expect(setNodeLayer(doc, 'nonesuch', 2)).toBe(doc);
    expect(nodeLayer(note.node)).toBeNull();
  });

  test('is set on several nodes in one pass, naming those that changed', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    const note = addNode(doc, { kind: 'note', text: 'Read me' });
    const once = setNodeLayer(note.doc, bravo.id, 2);
    const { doc: next, changed } = setNodeLayers(
      once,
      [sw.id, note.node.id, bravo.id, alpha.id, 'nonesuch'],
      '2',
    );

    // In the document's order. Bravo is at 2 already, a note takes none.
    expect(changed).toEqual([sw.id, alpha.id]);
    expect(
      [alpha.id, bravo.id, sw.id].map((id) => nodeLayer(findNode(next, id))),
    ).toEqual([2, 2, 2]);
    expect(findNode(next, note.node.id)).toBe(findNode(once, note.node.id));

    // Nothing to change: the same document, no ids.
    expect(setNodeLayers(next, [alpha.id, bravo.id], 2)).toEqual({
      doc: next,
      changed: [],
    });
    expect(setNodeLayers(next, [alpha.id], 'Layer 2').doc).toBe(next);
    expect(setNodeLayers(next, [], 3).doc).toBe(next);
  });

  test('changes nothing for a device included from another topology', () => {
    const { doc, alpha } = sampleDocument();
    const included = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, includedFrom: 'plant' } }
          : node,
      ),
    };

    expect(setNodeLayer(included, alpha.id, 1)).toBe(included);
  });

  test('stays when the Inspector applies other fields, and an empty one goes', () => {
    const { doc, alpha, sw } = sampleDocument();
    const set = setNodeLayer(setNodeLayer(doc, alpha.id, 0), sw.id, 2);
    const applied = updateNode(
      updateNode(set, alpha.id, { device: { fillColor: '#eef4fb' } }),
      sw.id,
      { switch: { outlineColor: '#1f7a5a' } },
    );

    expect(findNode(applied, alpha.id).device.layer).toBe(0);
    expect(findNode(applied, sw.id).switch.layer).toBe(2);

    const emptied = updateNode(
      updateNode(applied, alpha.id, { device: { layer: '' } }),
      sw.id,
      { switch: { layer: '' } },
    );

    expect('layer' in findNode(emptied, alpha.id).device).toBe(false);
    expect('layer' in findNode(emptied, sw.id).switch).toBe(false);
  });

  test('a new device or switch takes one, and a wrong one is none', () => {
    const { doc } = sampleDocument();
    const device = addNode(doc, { kind: 'device', layer: 0 });
    const hub = addNode(device.doc, { kind: 'switch', layer: 3 });
    const other = addNode(hub.doc, { kind: 'device', layer: 100 });
    const otherHub = addNode(other.doc, { kind: 'switch', layer: '3' });

    expect(device.node.device.layer).toBe(0);
    expect(hub.node.switch.layer).toBe(3);
    expect('layer' in other.node.device).toBe(false);
    expect('layer' in otherHub.node.switch).toBe(false);
    expect(errorsOf(otherHub.doc)).toEqual([]);
  });

  test('a copy keeps it', () => {
    const { doc, alpha, sw } = sampleDocument();
    const set = setNodeLayer(setNodeLayer(doc, alpha.id, 0), sw.id, 2);
    const pasted = pasteClipboard(
      set,
      copySelection(set, { nodes: [alpha.id, sw.id] }),
    );
    const copies = pasted.nodeIds.map((id) => findNode(pasted.doc, id));

    expect(copies.map(nodeLayer).sort()).toEqual([0, 2]);
  });

  test('a template keeps it, and a device made from the template takes it', () => {
    const { doc, alpha } = sampleDocument();
    const set = setNodeLayer(doc, alpha.id, 0);
    const template = templateFromNode(findNode(set, alpha.id), {
      name: 'PLC',
    });

    expect(template.device.layer).toBe(0);

    const made = addNode(doc, nodeOptionsFromTemplate(template, doc));

    expect(made.node.device.layer).toBe(0);
  });

  test('the template editor’s document has the layers of the diagram', () => {
    const { doc, alpha } = sampleDocument();
    const template = templateFromNode(
      findNode(setNodeLayer(doc, alpha.id, 2), alpha.id),
      { name: 'PLC' },
    );
    const edited = templateDocument(template, null, { layers: PLANT });

    expect(edited.layers).toEqual(PLANT);
    expect(layerLabel(edited, nodeLayer(edited.nodes[0]))).toBe(
      'Supervisory control',
    );
    expect('layers' in templateDocument(template)).toBe(false);
  });

  test('the template editor’s document matches layers by the diagram’s conflict rule', () => {
    const { doc, alpha } = sampleDocument();
    const template = templateFromNode(findNode(doc, alpha.id), {
      name: 'PLC',
    });
    const layers = [
      { id: 1, label: 'Control', matchers: { 'general.hostname': '^alpha$' } },
      { id: 3, label: 'Operations', matchers: { 'general.hostname': '.' } },
    ];
    const layerOf = (options) => {
      const edited = templateDocument(template, null, options);

      return effectiveNodeLayer(edited, edited.nodes[0]);
    };

    expect(layerOf({ layers })).toBe(1);
    expect(layerOf({ layers, matchConflict: 'highest' })).toBe(3);
  });

  test('one out of range is refused, as the server refuses it', () => {
    const { doc, alpha, sw } = sampleDocument();
    const wrong = {
      ...doc,
      nodes: doc.nodes.map((node) => {
        if (node.id === alpha.id) {
          return { ...node, device: { ...node.device, layer: 100 } };
        }

        return node.id === sw.id
          ? { ...node, switch: { ...node.switch, layer: '2' } }
          : node;
      }),
    };
    const codes = validateDocument(wrong).map((issue) => issue.code);

    expect(codes).toEqual(
      expect.arrayContaining([
        'device.layer.out-of-range',
        'switch.layer.out-of-range',
      ]),
    );
  });
});

describe('layers in the store', () => {
  let store;
  let sample;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    sample = sampleDocument();
    store.setDocument(sample.doc);
  });

  test('a node’s layer is one undo step, announced with the layer’s title', () => {
    const before = store.doc;

    store.setLayers(PLANT);

    const withLayers = store.doc;

    expect(store.setNodeLayer(sample.alpha.id, 2)).not.toBeNull();
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBe(2);
    expect(store.announcement).toContain(
      'Changed the layer of Device alpha to Supervisory control',
    );

    // The same layer again is no step.
    expect(store.setNodeLayer(sample.alpha.id, '2')).toBeNull();

    expect(store.setNodeLayer(sample.sw.id, 5)).not.toBeNull();
    expect(store.announcement).toContain('to Layer 5');
    expect(store.setNodeLayer(sample.sw.id, null)).not.toBeNull();
    expect(store.announcement).toContain('Removed the layer of Switch');

    store.undo();
    store.undo();
    store.undo();
    expect(store.doc).toEqual(withLayers);
    store.undo();
    expect(store.doc).toEqual(before);

    store.redo();
    store.redo();
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBe(2);
  });

  test('each change to the list of layers is one undo step, with its own label', () => {
    expect(store.addLayer('Enterprise')).toEqual({
      id: 7,
      label: 'Enterprise',
    });
    expect(store.history.undoLabel()).toBe('Added layer Enterprise');

    store.setNodeLayer(sample.alpha.id, 7);

    expect(store.relabelLayer(7, 'Enterprise network')).not.toBeNull();
    expect(store.history.undoLabel()).toBe(
      'Renamed layer Enterprise to Enterprise network',
    );
    expect(store.relabelLayer(7, 'Enterprise network')).toBeNull();
    expect(store.relabelLayer(7, '')).toBeNull();

    expect(store.renumberLayer(7, 8)).not.toBeNull();
    expect(store.history.undoLabel()).toBe(
      'Changed the ID of layer Enterprise network to 8',
    );
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBe(8);
    expect(store.renumberLayer(8, 6)).toBeNull();

    expect(store.removeLayer(8)).not.toBeNull();
    expect(store.history.undoLabel()).toBe(
      'Removed layer Enterprise network. 1 node stays at Layer 8',
    );
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBe(8);
    expect(store.removeLayer(8)).toBeNull();

    expect(store.removeLayer(3)).not.toBeNull();
    expect(store.history.undoLabel()).toBe('Removed Layer 3');
    expect(store.relabelLayer(2, 'Control')).not.toBeNull();
    expect(store.history.undoLabel()).toBe('Renamed Layer 2 to Control');
    store.undo();

    store.undo();
    store.undo();
    expect(layerLabel(store.doc, 8)).toBe('Enterprise network');
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBe(8);

    expect(store.setLayers([])).not.toBeNull();
    expect(effectiveLayers(store.doc)).toEqual(placeholderLayers());
    expect(store.setLayers([])).toBeNull();
  });

  test('a read-only draft keeps its layers', () => {
    store.readOnly = true;

    expect(store.setNodeLayer(sample.alpha.id, 1)).toBeNull();
    expect(store.addLayer()).toBeNull();
    expect(store.relabelLayer(3, 'Operations')).toBeNull();
    expect(store.renumberLayer(3, 9)).toBeNull();
    expect(store.removeLayer(3)).toBeNull();
    expect(store.setLayers(PLANT)).toBeNull();
    expect(nodeLayer(findNode(store.doc, sample.alpha.id))).toBeNull();
    expect('layers' in store.doc).toBe(false);
  });
});
