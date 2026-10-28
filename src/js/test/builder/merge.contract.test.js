import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  changesFrom,
  clashNote,
  droppedText,
  mergeDocuments,
  mergeNotes,
  mergeScenarios,
  movedText,
  possessive,
  sameIgnoringUndefined,
  valueText,
} from '@/builder/merge.js';
import {
  addNode,
  connect,
  groupNodes,
  nodeLabel,
  placeholderLayers,
  removeElements,
  removeInterface,
  setDocumentInfo,
  setParent,
  ungroup,
  withLayerRelabeled,
} from '@/builder/model.js';
import { parseDocument } from '@/builder/decode.js';
import { validateDocument } from '@/builder/validate.js';

import { canonicalJSON } from '@/builder/digest.js';

import { applyOps, documentOp, makeCounter, scaffold } from './arbitraries.js';
import { sampleDocument, testId } from './fixtures.js';

const loads = (doc) => {
  try {
    parseDocument(doc);

    return true;
  } catch {
    return false;
  }
};

// Errors that a merge of two valid versions can make, and that the strict
// check in the merge review shows (checkedMerge in store.js).
const LEFT_TO_CHECK = new Set([
  'interface.name.duplicate',
  'interface.name.unmatched',
  'edge.interface.connected',
  'edge.network.unknown',
  'switch.network.unknown',
]);

const applyChoices = (result, choices) =>
  mergeDocuments(result.base, result.mine, result.theirs, choices).doc;

// The document with node `id` changed by `change`.
function changeNode(doc, id, change) {
  return {
    ...doc,
    nodes: doc.nodes.map((node) => (node.id === id ? change(node) : node)),
  };
}

// A device renamed as the editor renames one: its label, hostname and spec
// hostname together.
function rename(doc, id, name) {
  return changeNode(doc, id, (node) => ({
    ...node,
    label: name,
    device: {
      ...node.device,
      hostname: name,
      spec: {
        ...node.device.spec,
        general: { ...node.device.spec.general, hostname: name },
      },
    },
  }));
}

function move(doc, id, position) {
  return changeNode(doc, id, (node) => ({ ...node, position }));
}

function remove(doc, id) {
  return {
    ...doc,
    nodes: doc.nodes.filter((node) => node.id !== id),
    edges: doc.edges.filter(
      (edge) => edge.sourceNodeId !== id && edge.targetNodeId !== id,
    ),
  };
}

function nodeOf(doc, id) {
  return doc.nodes.find((node) => node.id === id);
}

function note(id, text) {
  return {
    id,
    kind: 'note',
    position: { x: 400, y: 400 },
    note: { text },
  };
}

describe('mergeDocuments', () => {
  test('different fields of one node merge: one side moves it, the other renames it', () => {
    const { doc: base, alpha } = sampleDocument();
    const mine = move(base, alpha.id, { x: 96, y: 48 });
    const theirs = rename(base, alpha.id, 'alpha-2');

    const result = mergeDocuments(base, mine, theirs);
    const merged = nodeOf(result.doc, alpha.id);

    expect(result.clashes).toEqual([]);
    expect(merged.position).toEqual({ x: 96, y: 48 });
    expect(merged.label).toBe('alpha-2');
    expect(merged.device.hostname).toBe('alpha-2');
    expect(merged.device.spec.general.hostname).toBe('alpha-2');
  });

  test('a field both sides changed to different values clashes; a device name is one choice', () => {
    const { doc: base, alpha } = sampleDocument();
    const mine = rename(base, alpha.id, 'mine');
    const theirs = rename(base, alpha.id, 'theirs');

    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      key: `nodes["${alpha.id}"].name`,
      kind: 'change',
      element: 'alpha',
      field: 'name',
      label: 'alpha name',
      base: 'alpha',
      mine: 'mine',
      theirs: 'theirs',
      mineText: 'mine',
      theirsText: 'theirs',
      summary: 'You and they both changed alpha name.',
    });
    // Until a choice is made, theirs is kept.
    expect(nodeOf(result.doc, alpha.id).device.hostname).toBe('theirs');

    const chosen = nodeOf(
      applyChoices(result, { [result.clashes[0].key]: 'mine' }),
      alpha.id,
    );

    expect(chosen.label).toBe('mine');
    expect(chosen.device.hostname).toBe('mine');
    expect(chosen.device.spec.general.hostname).toBe('mine');
  });

  test('a clash in any field of a device name covers the whole name, which comes from the side chosen', () => {
    const { doc: base, alpha } = sampleDocument();
    const names = (doc) => {
      const node = nodeOf(doc, alpha.id);

      return [
        node.label,
        node.device.hostname,
        node.device.spec.general.hostname,
      ];
    };
    // Mine renames alpha and moves it. Theirs changes only its label.
    const mine = move(rename(base, alpha.id, 'X'), alpha.id, { x: 96, y: 48 });
    const theirs = changeNode(base, alpha.id, (node) => ({
      ...node,
      label: 'Y',
    }));

    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      key: `nodes["${alpha.id}"].name`,
      label: 'alpha name',
      base: 'alpha',
      mine: 'X',
      theirs: 'Y',
    });

    const key = result.clashes[0].key;
    const keptTheirs = applyChoices(result, { [key]: 'theirs' });
    const keptMine = applyChoices(result, { [key]: 'mine' });

    expect(names(keptTheirs)).toEqual(['Y', 'alpha', 'alpha']);
    expect(names(keptMine)).toEqual(['X', 'X', 'X']);
    // Until a choice is made, theirs is kept.
    expect(names(result.doc)).toEqual(['Y', 'alpha', 'alpha']);
    // What else changed on the device merges as before.
    expect(nodeOf(keptTheirs, alpha.id).position).toEqual({ x: 96, y: 48 });
    expect(nodeOf(keptMine, alpha.id).position).toEqual({ x: 96, y: 48 });

    // A clash in a hostname alone covers the label too.
    const hostOnly = changeNode(base, alpha.id, (node) => ({
      ...node,
      device: {
        ...node.device,
        hostname: 'h1',
        spec: {
          ...node.device.spec,
          general: { ...node.device.spec.general, hostname: 'h1' },
        },
      },
    }));
    const byHost = mergeDocuments(base, hostOnly, rename(base, alpha.id, 'Z'));

    expect(byHost.clashes).toHaveLength(1);
    expect(byHost.clashes[0]).toMatchObject({
      key: `nodes["${alpha.id}"].name`,
      label: 'alpha name',
      mine: 'h1',
      theirs: 'Z',
    });
    expect(
      names(applyChoices(byHost, { [byHost.clashes[0].key]: 'mine' })),
    ).toEqual(['alpha', 'h1', 'h1']);
    expect(
      names(applyChoices(byHost, { [byHost.clashes[0].key]: 'theirs' })),
    ).toEqual(['Z', 'Z', 'Z']);
  });

  test('a field both sides changed to the same value does not clash', () => {
    const { doc: base, alpha } = sampleDocument();

    const result = mergeDocuments(
      base,
      rename(base, alpha.id, 'same'),
      rename(base, alpha.id, 'same'),
    );

    expect(result.clashes).toEqual([]);
    expect(nodeOf(result.doc, alpha.id).device.hostname).toBe('same');
  });

  test('elements each side added with different ids are both kept: theirs first, then mine', () => {
    const { doc: base } = sampleDocument();
    const mineA = note(testId(), 'mine A');
    const mineB = note(testId(), 'mine B');
    const theirs = note(testId(), 'theirs');

    const result = mergeDocuments(
      base,
      { ...base, nodes: [mineA, ...base.nodes, mineB] },
      { ...base, nodes: [...base.nodes, theirs] },
    );

    expect(result.clashes).toEqual([]);
    expect(result.doc.nodes.map((node) => node.id)).toEqual([
      ...base.nodes.map((node) => node.id),
      theirs.id,
      mineA.id,
      mineB.id,
    ]);
  });

  test('an element both sides added with one id clashes field by field', () => {
    const { doc: base } = sampleDocument();
    const id = testId();

    const result = mergeDocuments(
      base,
      { ...base, nodes: [...base.nodes, note(id, 'mine')] },
      { ...base, nodes: [...base.nodes, note(id, 'theirs')] },
    );

    expect(result.clashes.map((clash) => clash.key)).toEqual([
      `nodes["${id}"].note.text`,
    ]);
    expect(result.clashes[0].label).toBe('theirs text');
    expect(nodeOf(result.doc, id).position).toEqual({ x: 400, y: 400 });
  });

  test('an element one side deleted and the other left as it was is deleted', () => {
    const { doc: base, bravo } = sampleDocument();
    const mine = remove(base, bravo.id);
    const theirs = setDocumentInfo(base, { description: 'changed elsewhere' });

    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toEqual([]);
    expect(nodeOf(result.doc, bravo.id)).toBeUndefined();
    expect(result.doc.metadata.description).toBe('changed elsewhere');
  });

  test('an element one side deleted and the other changed clashes', () => {
    const { doc: base, bravo } = sampleDocument();
    const deleted = remove(base, bravo.id);
    const renamed = rename(base, bravo.id, 'bravo-2');

    const mineDeleted = mergeDocuments(base, deleted, renamed);

    expect(mineDeleted.clashes).toHaveLength(1);
    expect(mineDeleted.clashes[0]).toMatchObject({
      key: `nodes["${bravo.id}"]`,
      kind: 'removed-mine',
      label: 'bravo',
      mineText: 'delete bravo',
      theirsText: 'keep bravo with their changes',
      summary: 'You deleted bravo. They changed its name.',
    });

    const key = mineDeleted.clashes[0].key;

    expect(
      nodeOf(applyChoices(mineDeleted, { [key]: 'mine' }), bravo.id),
    ).toBeUndefined();
    expect(
      nodeOf(applyChoices(mineDeleted, { [key]: 'theirs' }), bravo.id).device
        .hostname,
    ).toBe('bravo-2');

    const theirsDeleted = mergeDocuments(base, renamed, deleted);

    expect(theirsDeleted.clashes[0]).toMatchObject({
      kind: 'removed-theirs',
      mineText: 'keep bravo with your changes',
      theirsText: 'delete bravo',
      summary: 'They deleted bravo. You changed its name.',
    });
  });

  test('scenarios merge as a set: what either side added or removed', () => {
    const { doc } = sampleDocument();
    const base = { ...doc, scenarios: ['a', 'b'] };

    const result = mergeDocuments(
      base,
      { ...doc, scenarios: ['b', 'c'] },
      { ...doc, scenarios: ['a', 'd'] },
    );

    expect(result.clashes).toEqual([]);
    expect(result.doc.scenarios).toEqual(['d', 'c']);
    // Names are one ignoring case, and an empty list is left out.
    expect(mergeScenarios(['a'], ['a', 'X'], ['a', 'x'])).toEqual(['a', 'x']);
    expect(
      mergeDocuments(base, { ...doc, scenarios: ['b'] }, { ...doc }).doc,
    ).not.toHaveProperty('scenarios');
  });

  test('layers merge by ID, and a list left empty is left out', () => {
    const { doc } = sampleDocument();
    const base = {
      ...doc,
      layers: [
        { id: 3, label: 'Operations' },
        { id: 1, label: 'Control' },
      ],
    };
    const mine = {
      ...doc,
      layers: [
        { id: 3, label: 'Site operations' },
        { id: 1, label: 'Control' },
      ],
    };
    const theirs = {
      ...doc,
      layers: [
        { id: 3, label: 'Operations' },
        { id: 1, label: 'Basic control' },
      ],
    };
    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toEqual([]);
    expect(result.doc.layers).toEqual([
      { id: 3, label: 'Site operations' },
      { id: 1, label: 'Basic control' },
    ]);
    expect(
      mergeDocuments(base, { ...doc, layers: [] }, base).doc,
    ).not.toHaveProperty('layers');
  });

  test('a diagram without layers merges as its placeholder layers', () => {
    const { doc: base } = sampleDocument();
    const mine = { ...base, layers: withLayerRelabeled([], 1, 'Control') };
    const theirs = {
      ...base,
      layers: withLayerRelabeled([], 2, 'Supervisory'),
    };
    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toEqual([]);
    expect(result.doc.layers).toEqual(
      placeholderLayers().map(
        (layer) =>
          ({
            1: { id: 1, label: 'Control' },
            2: { id: 2, label: 'Supervisory' },
          })[layer.id] ?? layer,
      ),
    );
    // A merge back to the placeholder layers leaves the list out.
    expect(
      mergeDocuments(mine, { ...mine, layers: [] }, mine).doc,
    ).not.toHaveProperty('layers');
  });

  test('a position is one value: two moves clash even along different axes', () => {
    const { doc: base, alpha } = sampleDocument();

    const result = mergeDocuments(
      base,
      move(base, alpha.id, { x: 50, y: 0 }),
      move(base, alpha.id, { x: 0, y: 70 }),
    );

    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      key: `nodes["${alpha.id}"].position`,
      label: 'alpha position',
      mineText: 'x 50, y 0',
      theirsText: 'x 0, y 70',
    });
  });

  test('an icon choice shows the label of each icon', () => {
    const { doc: base, alpha } = sampleDocument();
    const icon = (iconKey) =>
      changeNode(base, alpha.id, (node) => ({
        ...node,
        device: { ...node.device, iconKey },
      }));

    const result = mergeDocuments(base, icon('cell-tower'), icon('router'));

    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      label: 'alpha icon',
      mine: 'cell-tower',
      theirs: 'router',
      mineText: 'Cell Tower',
      theirsText: 'Router',
    });
  });

  test("a merged list keeps theirs order, then mine's additions in mine's order", () => {
    const { doc: base } = sampleDocument();
    const added = note(testId(), 'mine');
    const [first, ...rest] = base.nodes;

    const result = mergeDocuments(
      base,
      { ...base, nodes: [added, ...rest, first] },
      base,
    );

    expect(result.clashes).toEqual([]);
    expect(result.doc.nodes.map((node) => node.id)).toEqual([
      ...base.nodes.map((node) => node.id),
      added.id,
    ]);
  });

  test('what the server stamps comes from theirs, and the viewport from mine', () => {
    const { doc } = sampleDocument();
    const stamped = (by, at, viewport) => ({
      ...doc,
      revision: 1,
      metadata: { ...doc.metadata, updatedBy: by, updatedAt: at },
      viewport,
    });

    const result = mergeDocuments(
      stamped('alice', '2026-10-01T10:00:00Z', { x: 0, y: 0, zoom: 1 }),
      stamped('alice', '2026-10-01T10:05:00Z', { x: 10, y: 0, zoom: 2 }),
      stamped('bob', '2026-10-01T10:04:00Z', { x: 0, y: 30, zoom: 1 }),
    );

    expect(result.clashes).toEqual([]);
    expect(result.doc.metadata).toMatchObject({
      id: doc.metadata.id,
      updatedBy: 'bob',
      updatedAt: '2026-10-01T10:04:00Z',
    });
    expect(result.doc.viewport).toEqual({ x: 10, y: 0, zoom: 2 });
  });

  test('interfaces are matched by name, so each side may change a different one', () => {
    const { doc: base, alpha } = sampleDocument();
    const interfaces = (doc) =>
      nodeOf(doc, alpha.id).device.spec.network.interfaces;
    const withInterfaces = (doc, change) =>
      changeNode(doc, alpha.id, (node) => ({
        ...node,
        device: {
          ...node.device,
          spec: {
            ...node.device.spec,
            network: {
              ...node.device.spec.network,
              interfaces: change(interfaces(doc)),
            },
          },
        },
      }));
    const mine = withInterfaces(base, (list) =>
      list.map((entry) => ({ ...entry, address: '10.0.0.5' })),
    );
    const theirs = withInterfaces(base, (list) => [
      ...list,
      { name: 'eth1', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
    ]);

    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toEqual([]);
    expect(interfaces(result.doc)).toEqual([
      expect.objectContaining({ name: 'eth0', address: '10.0.0.5' }),
      expect.objectContaining({ name: 'eth1' }),
    ]);
  });

  test("a switch's label follows its network, whose name clashes once", () => {
    const { doc: base, sw, network } = sampleDocument();
    const renamed = (name) => ({
      ...changeNode(base, sw.id, (node) => ({ ...node, label: name })),
      networks: base.networks.map((entry) =>
        entry.id === network.id ? { ...entry, name } : entry,
      ),
    });

    const result = mergeDocuments(base, renamed('LAN'), renamed('WAN'));

    expect(result.clashes.map((clash) => clash.label)).toEqual([
      'network EXP name',
    ]);
  });

  test('the arguments are left as they were', () => {
    const { doc: base, alpha, bravo } = sampleDocument();
    const mine = move(base, alpha.id, { x: 96, y: 48 });
    const theirs = remove(base, bravo.id);
    const copies = [base, mine, theirs].map((doc) =>
      JSON.parse(JSON.stringify(doc)),
    );

    mergeDocuments(base, mine, theirs);

    expect([base, mine, theirs]).toEqual(copies);
  });

  test('an element added from the model on each side merges with both', () => {
    const { doc: base } = sampleDocument();
    const mine = addNode(base, { kind: 'device', hostname: 'charlie' }).doc;
    const theirs = addNode(base, { kind: 'device', hostname: 'delta' }).doc;

    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toEqual([]);
    expect(
      result.doc.nodes
        .filter((node) => node.kind === 'device')
        .map((node) => node.device.hostname),
    ).toEqual(['alpha', 'bravo', 'delta', 'charlie']);
  });

  test('templates are matched by id', () => {
    const { doc } = sampleDocument();
    const template = (name) => ({
      id: testId(),
      name,
      device: { spec: { general: { hostname: name } } },
    });
    const web = template('web');
    const db = template('db');
    const withTemplates = (...templates) => ({ ...doc, templates });
    const base = withTemplates(web);

    // Each side added one with its own id: both are kept.
    const added = mergeDocuments(doc, withTemplates(web), withTemplates(db));

    expect(added.clashes).toEqual([]);
    expect(added.doc.templates.map((entry) => entry.id)).toEqual([
      db.id,
      web.id,
    ]);

    // One id with different content clashes field by field.
    const both = mergeDocuments(
      base,
      withTemplates({ ...web, name: 'www' }),
      withTemplates({ ...web, name: 'site' }),
    );

    expect(both.clashes.map((clash) => clash.key)).toEqual([
      `templates["${web.id}"].name`,
    ]);

    // Deleted on one side and left as it was on the other: deleted.
    const deleted = mergeDocuments(base, withTemplates(), base);

    expect(deleted.clashes).toEqual([]);
    expect(deleted.doc).not.toHaveProperty('templates');

    // Deleted on one side and changed on the other: a clash.
    const changed = mergeDocuments(
      base,
      withTemplates(),
      withTemplates({ ...web, description: 'Web server' }),
    );

    expect(changed.clashes).toHaveLength(1);
    expect(changed.clashes[0]).toMatchObject({
      key: `templates["${web.id}"]`,
      kind: 'removed-mine',
      label: 'template web',
      summary: 'You deleted template web. They changed its description.',
    });
  });

  test('custom icons are matched by name', () => {
    const { doc } = sampleDocument();
    const logo = { data: 'AAAA' };
    const other = { data: 'BBBB' };
    const withIcons = (icons) => ({ ...doc, icons });
    const base = withIcons({ logo });

    // Both added the same picture under one name: no clash.
    const same = mergeDocuments(
      doc,
      withIcons({ logo }),
      withIcons({ logo: { ...logo } }),
    );

    expect(same.clashes).toEqual([]);
    expect(same.doc.icons).toEqual({ logo });

    // Different pictures under one name clash.
    const different = mergeDocuments(
      doc,
      withIcons({ logo }),
      withIcons({ logo: other }),
    );

    expect(different.clashes).toHaveLength(1);
    expect(different.clashes[0].element).toBe('icon logo');
    expect(different.doc.icons).toEqual({ logo: other });
    expect(
      applyChoices(different, { [different.clashes[0].key]: 'mine' }).icons,
    ).toEqual({ logo });

    // Deleted on one side and left as it was on the other: deleted.
    const deleted = mergeDocuments(base, withIcons({}), base);

    expect(deleted.clashes).toEqual([]);
    expect(deleted.doc).not.toHaveProperty('icons');

    // Deleted on one side and changed on the other: a clash.
    const changed = mergeDocuments(
      base,
      withIcons({ logo: other }),
      withIcons({}),
    );

    expect(changed.clashes).toHaveLength(1);
    expect(changed.clashes[0]).toMatchObject({
      key: 'icons["logo"]',
      kind: 'removed-theirs',
      label: 'icon logo',
      theirsText: 'delete icon logo',
    });
  });

  test("lists without ids are one value each: a line's points and the diagram's notes", () => {
    const { doc } = sampleDocument();
    const lineId = testId();
    const two = [
      { x: 0, y: 0 },
      { x: 100, y: 0 },
    ];
    const bent = [
      { x: 0, y: 0 },
      { x: 100, y: 50 },
    ];
    const withLine = (points, more = {}) => ({
      ...doc,
      nodes: [
        ...doc.nodes,
        {
          id: lineId,
          kind: 'line',
          position: { x: 0, y: 0 },
          line: { points, ...more },
        },
      ],
      metadata: { ...doc.metadata, notes: ['first'] },
    });
    const base = withLine(two);

    // Both changed the points: one clash, however many points changed.
    const points = mergeDocuments(
      base,
      withLine(bent),
      withLine([...two, { x: 200, y: 0 }]),
    );

    expect(points.clashes).toHaveLength(1);
    expect(points.clashes[0]).toMatchObject({
      key: `nodes["${lineId}"].line.points`,
      mineText: '2 items',
      theirsText: '3 items',
    });

    // Only one side changed the points: its list is kept whole.
    const one = mergeDocuments(
      base,
      withLine(bent),
      withLine(two, { color: '#ff0000' }),
    );

    expect(one.clashes).toEqual([]);
    expect(nodeOf(one.doc, lineId).line).toEqual({
      points: bent,
      color: '#ff0000',
    });

    // The diagram's notes are one list.
    const withNotes = (notes) => ({
      ...base,
      metadata: { ...base.metadata, notes },
    });
    const notes = mergeDocuments(
      base,
      withNotes(['first', 'mine']),
      withNotes(['first', 'theirs']),
    );

    expect(notes.clashes).toHaveLength(1);
    expect(notes.clashes[0]).toMatchObject({
      key: 'metadata.notes',
      label: 'Diagram notes',
      mineText: 'first, mine',
      theirsText: 'first, theirs',
    });
    const onlyMine = mergeDocuments(base, withNotes(['first', 'mine']), base);

    expect(onlyMine.doc.metadata.notes).toEqual(['first', 'mine']);
  });

  test('grid, layout and source fields clash when both changed them differently', () => {
    const { doc } = sampleDocument();
    const base = {
      ...doc,
      grid: { enabled: true, size: 20 },
      layout: 'layered',
      source: { kind: 'topology', name: 'plant' },
    };
    const mine = {
      ...base,
      grid: { ...base.grid, size: 10, enabled: false },
      layout: 'tree',
      source: { ...base.source, name: 'mill' },
    };
    const theirs = {
      ...base,
      grid: { ...base.grid, size: 40 },
      layout: 'force',
      source: { ...base.source, name: 'pump', topology: 'plant' },
    };

    const result = mergeDocuments(base, mine, theirs);

    const labels = result.clashes.map((clash) => [clash.key, clash.label]);

    expect(Object.fromEntries(labels)).toEqual({
      'grid.size': 'Grid size',
      layout: 'Diagram layout',
      'source.name': 'Source name',
    });
    // What only one side changed merges.
    expect(result.doc.grid.enabled).toBe(false);
    expect(result.doc.source.topology).toBe('plant');

    const allMine = result.clashes.map((clash) => [clash.key, 'mine']);
    const chosen = applyChoices(result, Object.fromEntries(allMine));

    expect(chosen.grid).toEqual({ enabled: false, size: 10 });
    expect(chosen.layout).toBe('tree');
    expect(chosen.source).toEqual({
      kind: 'topology',
      name: 'mill',
      topology: 'plant',
    });
  });

  test('a connection or a network one side deleted is deleted, unless the other changed it', () => {
    const { doc: base, edge, network } = sampleDocument();
    const withEdge = (change) => ({
      ...base,
      edges: base.edges.flatMap((entry) =>
        entry.id === edge.id ? change(entry) : [entry],
      ),
    });
    const withNetwork = (change) => ({
      ...base,
      networks: base.networks.flatMap((entry) =>
        entry.id === network.id ? change(entry) : [entry],
      ),
    });
    const noEdge = withEdge(() => []);
    const noNetwork = withNetwork(() => []);

    // Deleted on one side, left as it was on the other: deleted.
    const edgeGone = mergeDocuments(base, noEdge, base);

    expect(edgeGone.clashes).toEqual([]);
    expect(edgeGone.doc.edges.map((entry) => entry.id)).not.toContain(edge.id);

    const networkGone = mergeDocuments(base, base, noNetwork);

    expect(networkGone.clashes).toEqual([]);
    expect(networkGone.doc.networks.map((entry) => entry.id)).not.toContain(
      network.id,
    );

    // Deleted on one side, changed on the other: a clash.
    const edgeClash = mergeDocuments(
      base,
      noEdge,
      withEdge((entry) => [{ ...entry, color: '#ff0000' }]),
    );

    expect(edgeClash.clashes).toHaveLength(1);
    expect(edgeClash.clashes[0]).toMatchObject({
      key: `edges["${edge.id}"]`,
      kind: 'removed-mine',
      summary: expect.stringMatching(
        /^You deleted connection from alpha to .+\. They changed its color\.$/,
      ),
    });

    const networkClash = mergeDocuments(
      base,
      withNetwork((entry) => [{ ...entry, description: 'Plant floor' }]),
      noNetwork,
    );

    expect(networkClash.clashes).toHaveLength(1);
    expect(networkClash.clashes[0]).toMatchObject({
      key: `networks["${network.id}"]`,
      kind: 'removed-theirs',
      label: 'network EXP',
      summary: 'They deleted network EXP. You changed its description.',
    });
    const kept = applyChoices(networkClash, {
      [networkClash.clashes[0].key]: 'mine',
    });

    expect(
      kept.networks.find((entry) => entry.id === network.id).description,
    ).toBe('Plant floor');
  });
});

describe('interfaces and the connections that use them', () => {
  // The names of a device's interface handles and of its spec interfaces.
  function interfaceNames(doc, id) {
    const { device } = nodeOf(doc, id);

    return {
      handles: device.interfaces.map((handle) => handle.name),
      spec: device.spec.network.interfaces.map((iface) => iface.name),
    };
  }

  // The document with interface `handleId` of device `id` named `name`: its
  // handle and its spec entry, as the Inspector renames one.
  function renameInterface(doc, id, handleId, name) {
    const old = nodeOf(doc, id).device.interfaces.find(
      (handle) => handle.id === handleId,
    ).name;

    return changeNode(doc, id, (node) => ({
      ...node,
      device: {
        ...node.device,
        interfaces: node.device.interfaces.map((handle) =>
          handle.id === handleId ? { ...handle, name } : handle,
        ),
        spec: {
          ...node.device.spec,
          network: {
            ...node.device.spec.network,
            interfaces: node.device.spec.network.interfaces.map((iface) =>
              iface.name === old ? { ...iface, name } : iface,
            ),
          },
        },
      },
    }));
  }

  // The document with the spec interface `name` of device `id` changed.
  function changeInterface(doc, id, name, change) {
    return changeNode(doc, id, (node) => ({
      ...node,
      device: {
        ...node.device,
        spec: {
          ...node.device.spec,
          network: {
            ...node.device.spec.network,
            interfaces: node.device.spec.network.interfaces.map((iface) =>
              iface.name === name ? { ...iface, ...change } : iface,
            ),
          },
        },
      },
    }));
  }

  // The sample diagram with bravo's eth0 connected to the switch.
  function connectBravo(doc, sw, bravo) {
    return connect(doc, {
      sourceNodeId: bravo.id,
      sourceHandleId: bravo.device.interfaces[0].id,
      targetNodeId: sw.id,
    });
  }

  test('an interface both renamed differently is one clash on its name, and the side chosen names the one interface', () => {
    const { doc: base, alpha } = sampleDocument();
    const handle = alpha.device.interfaces[0];
    const mine = renameInterface(base, alpha.id, handle.id, 'lan0');
    const theirs = renameInterface(base, alpha.id, handle.id, 'wan0');

    const result = mergeDocuments(base, mine, theirs);

    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      key: `nodes["${alpha.id}"].device.interfaces["${handle.id}"].name`,
      label: 'alpha interface eth0 name',
      mineText: 'lan0',
      theirsText: 'wan0',
      summary: 'You and they both changed alpha interface eth0 name.',
    });

    const { key } = result.clashes[0];

    // Theirs until a choice is made, and one interface whichever is kept.
    expect(interfaceNames(result.doc, alpha.id)).toEqual({
      handles: ['wan0'],
      spec: ['wan0'],
    });

    const kept = applyChoices(result, { [key]: 'mine' });
    const theirsKept = applyChoices(result, { [key]: 'theirs' });
    const specs = nodeOf(kept, alpha.id).device.spec.network.interfaces;

    expect(interfaceNames(kept, alpha.id)).toEqual({
      handles: ['lan0'],
      spec: ['lan0'],
    });
    expect(interfaceNames(theirsKept, alpha.id)).toEqual({
      handles: ['wan0'],
      spec: ['wan0'],
    });
    // The connection still uses the interface, and nothing the merge
    // matched the interfaces by is left on them.
    expect(kept.edges).toEqual(base.edges);
    expect(specs.flatMap(Object.getOwnPropertySymbols)).toEqual([]);
  });

  test('an interface one side renamed and the other changed is one interface with both changes', () => {
    const { doc: base, alpha } = sampleDocument();
    const handle = alpha.device.interfaces[0];
    const mine = renameInterface(base, alpha.id, handle.id, 'lan0');
    const theirs = changeInterface(base, alpha.id, 'eth0', {
      address: '10.0.0.5',
    });

    const result = mergeDocuments(base, mine, theirs);
    const specs = nodeOf(result.doc, alpha.id).device.spec.network.interfaces;

    expect(result.clashes).toEqual([]);
    expect(specs).toEqual([
      expect.objectContaining({ name: 'lan0', address: '10.0.0.5' }),
    ]);
    expect(interfaceNames(result.doc, alpha.id).handles).toEqual(['lan0']);
  });

  test('a device one side deleted and the other connected clashes: deleting it drops the connection, which the merge names', () => {
    const { doc: base, sw, bravo } = sampleDocument();
    const connected = connectBravo(base, sw, bravo);
    const deleted = remove(base, bravo.id);

    const result = mergeDocuments(base, connected.doc, deleted);

    // Connecting set bravo's eth0 VLAN too, which keeping it keeps.
    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      key: `nodes["${bravo.id}"]`,
      kind: 'removed-theirs',
      label: 'bravo',
      mineText: 'keep bravo with your changes',
      theirsText: 'delete bravo and drop your connection to it',
      summary: expect.stringMatching(
        /^They deleted bravo\. You changed .+ and connected it\.$/,
      ),
    });

    const { key } = result.clashes[0];
    const dropped = mergeDocuments(base, connected.doc, deleted, {
      [key]: 'theirs',
    });

    expect(nodeOf(dropped.doc, bravo.id)).toBeUndefined();
    expect(dropped.doc.edges.map((entry) => entry.id)).toEqual(
      base.edges.map((entry) => entry.id),
    );
    expect(dropped.dropped).toEqual([
      {
        key: `edges["${connected.edge.id}"]`,
        label: `connection from bravo to ${nodeLabel(sw)}`,
      },
    ]);
    expect(droppedText(dropped.dropped)).toBe(
      `Dropped the connection from bravo to ${nodeLabel(sw)}: someone deleted the device or interface that it connects.`,
    );
    // theirs is what a merge with no choice keeps.
    expect(result.dropped).toEqual(dropped.dropped);

    const kept = mergeDocuments(base, connected.doc, deleted, {
      [key]: 'mine',
    });

    expect(nodeOf(kept.doc, bravo.id)).toBeDefined();
    expect(kept.doc.edges.map((entry) => entry.id)).toContain(
      connected.edge.id,
    );
    expect(kept.dropped).toEqual([]);

    // The other way round.
    const [mirrored] = mergeDocuments(base, deleted, connected.doc).clashes;

    expect(mirrored).toMatchObject({
      kind: 'removed-mine',
      mineText: 'delete bravo and drop their connection to it',
      theirsText: 'keep bravo with their changes',
    });
  });

  test('a node one side deleted and the other connected without changing it clashes too', () => {
    const { doc: base, sw, bravo } = sampleDocument();
    const name = nodeLabel(sw);
    const connected = connectBravo(base, sw, bravo);
    const deleted = remove(base, sw.id);

    const result = mergeDocuments(base, deleted, connected.doc);
    const clash = result.clashes.find(
      (entry) => entry.key === `nodes["${sw.id}"]`,
    );

    expect(clash).toMatchObject({
      kind: 'removed-mine',
      mineText: `delete ${name} and drop their connection to it`,
      theirsText: `keep ${name} with their connection`,
      summary: `You deleted ${name}. They connected it.`,
    });

    const gone = applyChoices(result, { [clash.key]: 'mine' });

    expect(nodeOf(gone, sw.id)).toBeUndefined();
    expect(gone.edges).toEqual([]);
  });

  test('an interface one side removed and the other connected is one clash: removing it drops the connection', () => {
    const { doc: base, sw, bravo } = sampleDocument();
    const handle = bravo.device.interfaces[0];
    const connected = connectBravo(base, sw, bravo);
    const removed = removeInterface(base, bravo.id, handle.id);

    const result = mergeDocuments(base, connected.doc, removed);

    expect(result.clashes).toHaveLength(1);
    expect(result.clashes[0]).toMatchObject({
      key: `nodes["${bravo.id}"].device.interfaces["${handle.id}"]`,
      kind: 'removed-theirs',
      label: 'bravo interface eth0',
      mineText: 'keep bravo interface eth0 with your connection',
      theirsText: 'remove bravo interface eth0 and drop your connection to it',
      summary: 'They removed bravo interface eth0. You connected it.',
    });

    const { key } = result.clashes[0];
    const removing = mergeDocuments(base, connected.doc, removed, {
      [key]: 'theirs',
    });

    expect(interfaceNames(removing.doc, bravo.id)).toEqual({
      handles: [],
      spec: [],
    });
    expect(removing.dropped.map((entry) => entry.key)).toEqual([
      `edges["${connected.edge.id}"]`,
    ]);

    const keeping = applyChoices(result, { [key]: 'mine' });

    expect(interfaceNames(keeping, bravo.id)).toEqual({
      handles: ['eth0'],
      spec: ['eth0'],
    });
    expect(keeping.edges.map((entry) => entry.id)).toContain(connected.edge.id);
  });
});

describe('groups and the nodes in them', () => {
  // The sample with alpha in group Zone A, and bravo in no group.
  function grouped() {
    const { doc, alpha, bravo } = sampleDocument();
    const { doc: base, group } = groupNodes(doc, [alpha.id], {
      title: 'Zone A',
    });

    return { base, group, alpha, bravo };
  }

  // The document with node `id` in group `parentId`, the group as it was.
  function putIn(doc, id, parentId) {
    return changeNode(doc, id, (node) => ({ ...node, parentId }));
  }

  const parentProblems = (doc) =>
    validateDocument(doc).filter((issue) =>
      issue.code.startsWith('node.parent.'),
    );

  test('a group one side ungrouped and the other put a node in clashes: deleting it moves the node out, which the merge names', () => {
    const { base, group, alpha, bravo } = grouped();
    const ungrouped = ungroup(base, group.id);
    const filled = putIn(base, bravo.id, group.id);

    const result = mergeDocuments(base, ungrouped, filled);

    expect(result.clashes).toEqual([
      expect.objectContaining({
        key: `nodes["${group.id}"]`,
        kind: 'removed-mine',
        label: 'Zone A',
        mineText: 'delete Zone A and move their node out of it',
        theirsText: 'keep Zone A with their node in it',
        summary: 'You deleted Zone A. They put 1 node in it.',
      }),
    ]);
    // theirs is what a merge with no choice keeps: the group, with bravo in
    // it. Ungroup took alpha out of it.
    expect(nodeOf(result.doc, group.id)).toBeDefined();
    expect(nodeOf(result.doc, bravo.id).parentId).toBe(group.id);
    expect(nodeOf(result.doc, alpha.id).parentId).toBeUndefined();
    expect(result.moved).toEqual([]);

    const deleted = mergeDocuments(base, ungrouped, filled, {
      [`nodes["${group.id}"]`]: 'mine',
    });

    expect(nodeOf(deleted.doc, group.id)).toBeUndefined();
    expect(nodeOf(deleted.doc, bravo.id).parentId).toBeUndefined();
    expect(deleted.moved).toEqual([
      {
        key: `nodes["${bravo.id}"]`,
        label: 'bravo out of Zone A',
        reason: 'deleted',
      },
    ]);
    expect(movedText(deleted.moved)).toBe(
      'Moved bravo out of Zone A: someone deleted its group.',
    );
    expect(parentProblems(deleted.doc)).toEqual([]);
    expect(
      validateDocument(deleted.doc).filter((i) => i.level === 'error'),
    ).toEqual([]);

    // The other way round.
    const [mirrored] = mergeDocuments(base, filled, ungrouped).clashes;

    expect(mirrored).toMatchObject({
      kind: 'removed-theirs',
      mineText: 'keep Zone A with your node in it',
      theirsText: 'delete Zone A and move your node out of it',
      summary: 'They deleted Zone A. You put 1 node in it.',
    });
  });

  test('a group one side changed as it put nodes in names the change', () => {
    const { base, group, bravo } = grouped();
    // setParent grows the group to hold bravo.
    const filled = setParent(base, bravo.id, group.id);
    const removed = removeElements(base, { nodes: [group.id] });

    const [clash] = mergeDocuments(base, removed, filled).clashes;

    expect(clash).toMatchObject({
      key: `nodes["${group.id}"]`,
      mineText: 'delete Zone A and move their node out of it',
      theirsText: 'keep Zone A with their changes',
      summary: expect.stringMatching(
        /^You deleted Zone A\. They changed .+ and put 1 node in it\.$/,
      ),
    });
  });

  test('a node whose group a choice deleted moves to the group that held it, as Ungroup moves it', () => {
    const { base: inner, group, bravo } = grouped();
    const { doc: base, group: outer } = groupNodes(inner, [group.id], {
      title: 'Site',
    });
    const ungrouped = ungroup(base, group.id);
    const filled = putIn(base, bravo.id, group.id);

    const merged = mergeDocuments(base, ungrouped, filled, {
      [`nodes["${group.id}"]`]: 'mine',
    });

    expect(nodeOf(merged.doc, bravo.id).parentId).toBe(outer.id);
    expect(merged.moved.map((entry) => entry.label)).toEqual([
      'bravo out of Zone A',
    ]);
    expect(parentProblems(merged.doc)).toEqual([]);
  });

  test('a member one side changed in a group the other deleted moves out when it is kept, though no clash is about the group', () => {
    const { base, group, alpha } = grouped();
    // The other side deleted the group with alpha in it, as Delete does.
    const removed = removeElements(base, { nodes: [group.id] });
    const moved = move(base, alpha.id, { x: 40, y: 40 });

    const result = mergeDocuments(base, removed, moved);

    expect(result.clashes.map((clash) => clash.key)).toEqual([
      `nodes["${alpha.id}"]`,
    ]);
    expect(nodeOf(result.doc, group.id)).toBeUndefined();
    expect(nodeOf(result.doc, alpha.id).parentId).toBeUndefined();
    expect(result.moved).toEqual([
      {
        key: `nodes["${alpha.id}"]`,
        label: 'alpha out of Zone A',
        reason: 'deleted',
      },
    ]);
    expect(parentProblems(result.doc)).toEqual([]);
  });

  test('two groups each side put inside the other clash on each group, and no choice keeps both', () => {
    const group = (doc, title, x) =>
      addNode(doc, {
        kind: 'group',
        title,
        position: { x, y: 0 },
        size: { width: 100, height: 100 },
      });
    const first = group(sampleDocument().doc, 'A', 600);
    const second = group(first.doc, 'B', 800);
    const [base, a, b] = [second.doc, first.node, second.node];
    const mine = putIn(base, a.id, b.id);
    const theirs = putIn(base, b.id, a.id);

    const result = mergeDocuments(base, mine, theirs);
    const keyA = `nodes["${a.id}"].parentId`;
    const keyB = `nodes["${b.id}"].parentId`;

    expect(result.clashes).toEqual([
      expect.objectContaining({
        key: keyA,
        label: 'A group',
        mineText: 'B',
        theirsText: 'no group',
        summary:
          'You put A in B. They put B inside A. Builder cannot keep both.',
      }),
      expect.objectContaining({
        key: keyB,
        label: 'B group',
        mineText: 'no group',
        theirsText: 'A',
        summary:
          'They put B in A. You put A inside B. Builder cannot keep both.',
      }),
    ]);

    // Theirs both ways, mine both ways, and each one alone.
    expect(nodeOf(result.doc, a.id).parentId).toBeUndefined();
    expect(nodeOf(result.doc, b.id).parentId).toBe(a.id);
    expect(result.moved).toEqual([]);

    const mineBoth = mergeDocuments(base, mine, theirs, {
      [keyA]: 'mine',
      [keyB]: 'mine',
    });

    expect(nodeOf(mineBoth.doc, a.id).parentId).toBe(b.id);
    expect(nodeOf(mineBoth.doc, b.id).parentId).toBeUndefined();

    // Both moves kept would put the groups inside each other, so one moves
    // out again, which the merge names.
    const both = mergeDocuments(base, mine, theirs, {
      [keyA]: 'mine',
      [keyB]: 'theirs',
    });

    expect(parentProblems(both.doc)).toEqual([]);
    expect(both.moved).toHaveLength(1);
    expect(both.moved[0].reason).toBe('nested');
    expect(movedText(both.moved)).toMatch(
      /^Moved (A out of B|B out of A): groups cannot be inside each other\.$/,
    );
  });

  test('a group one side took out of a node before it put the node in that group does not clash', () => {
    const { base: inner, group } = grouped();
    const { doc: base, group: outer } = groupNodes(inner, [group.id], {
      title: 'Site',
    });
    // Zone A out of Site, then Site in Zone A.
    const mine = putIn(putIn(base, group.id, undefined), outer.id, group.id);

    const result = mergeDocuments(base, mine, base);

    expect(result.clashes).toEqual([]);
    expect(nodeOf(result.doc, outer.id).parentId).toBe(group.id);
    expect(nodeOf(result.doc, group.id).parentId).toBeUndefined();
  });

  test('the notes of a merge name the connections dropped, then the nodes moved', () => {
    expect(mergeNotes({})).toBe('');
    expect(
      mergeNotes({
        dropped: [{ key: 'edges["e"]', label: 'connection from a to b' }],
        moved: [
          { key: 'nodes["x"]', label: 'x out of G', reason: 'deleted' },
          { key: 'nodes["y"]', label: 'y out of H', reason: 'deleted' },
          { key: 'nodes["g"]', label: 'G out of H', reason: 'nested' },
        ],
      }),
    ).toBe(
      'Dropped the connection from a to b: someone deleted the device or interface that it connects. Moved x out of G and y out of H: someone deleted their groups. Moved G out of H: groups cannot be inside each other.',
    );
  });
});

describe('merge helpers', () => {
  test('values as a choice shows them', () => {
    expect(valueText(undefined)).toBe('none');
    expect(valueText('')).toBe('empty');
    expect(valueText('router')).toBe('router');
    expect(valueText(true)).toBe('on');
    expect(valueText({ x: 1.234, y: 2 })).toBe('x 1.23, y 2');
    expect(valueText({ width: 160, height: 80 })).toBe('160 wide, 80 high');
    expect(valueText(['a', 'b'])).toBe('a, b');
    expect(
      valueText([
        { x: 0, y: 0 },
        { x: 1, y: 1 },
      ]),
    ).toBe('2 items');
    expect(valueText('x'.repeat(100))).toHaveLength(80);
  });

  test('two values are the same whatever the order of their keys', () => {
    expect(
      sameIgnoringUndefined(
        { a: 1, b: [1, { c: 2 }] },
        { b: [1, { c: 2 }], a: 1 },
      ),
    ).toBe(true);
    expect(sameIgnoringUndefined({ a: 1, b: undefined }, { a: 1 })).toBe(true);
    expect(sameIgnoringUndefined([1, 2], [2, 1])).toBe(false);
  });

  test('who a merge takes changes from', () => {
    expect(changesFrom('bob', 'alice')).toBe('bob');
    expect(changesFrom('alice', 'alice')).toBe('another tab');
    expect(changesFrom('', 'alice')).toBe('another editor');
    expect(possessive('another tab')).toBe("another tab's");
  });

  // One Apply is one unsaved change but can change two fields, so the
  // conflict note counts fields, as the merge dialog does.
  test('the conflict note counts the fields that clash, not changes', () => {
    expect(clashNote(2, 'alice')).toBe(
      'You and alice changed 2 fields differently. Review and merge to choose which version to keep.',
    );
    expect(clashNote(1, 'another editor')).toBe(
      'You and another editor changed 1 field differently. Review and merge to choose which version to keep.',
    );
    expect(clashNote(2, 'alice')).not.toMatch(/\bchanges?\b/);
    expect(clashNote(0, 'another tab')).toBe(
      "Builder cannot merge your changes and another tab's as they are. Review and merge shows why.",
    );
  });
});

describe('merge laws, for any edits', () => {
  const edits = fc.array(documentOp, { maxLength: 12, size: 'max' });

  // A base after edits A, and mine and theirs after edits B and C on it.
  const versions = fc.tuple(edits, edits, edits).map(([a, b, c]) => {
    const counter = makeCounter();
    const base = applyOps(scaffold(counter), a, counter);

    return {
      base,
      mine: applyOps(base, b, counter),
      theirs: applyOps(base, c, counter),
    };
  });
  const same = (a, b) => expect(canonicalJSON(a)).toBe(canonicalJSON(b));

  test('a side that changed nothing takes the other side whole', () => {
    fc.assert(
      fc.property(versions, ({ base, mine, theirs }) => {
        const ours = mergeDocuments(base, mine, base);
        const others = mergeDocuments(base, base, theirs);

        expect(ours.clashes).toEqual([]);
        same(ours.doc, mine);
        expect(others.clashes).toEqual([]);
        same(others.doc, theirs);
      }),
      { numRuns: 100 },
    );
  });

  test('merging a version with itself gives that version', () => {
    fc.assert(
      fc.property(versions, ({ mine }) => {
        const merged = mergeDocuments(mine, mine, mine);

        expect(merged.clashes).toEqual([]);
        same(merged.doc, mine);
      }),
      { numRuns: 100 },
    );
  });

  test('changes no version it merges', () => {
    fc.assert(
      fc.property(versions, ({ base, mine, theirs }) => {
        const before = [base, mine, theirs].map(canonicalJSON);

        mergeDocuments(base, mine, theirs);
        expect([base, mine, theirs].map(canonicalJSON)).toEqual(before);
      }),
      { numRuns: 100 },
    );
  });

  // The same, from a base with three devices each in a group of its own,
  // so the edits move nodes between groups and put groups in groups.
  const groupedScaffold = (counter) => {
    const doc = scaffold(counter);

    return doc.nodes
      .filter((node) => node.kind === 'device')
      .reduce((next, device) => groupNodes(next, [device.id]).doc, doc);
  };

  test('the grouped base has three devices, each in a group of its own', () => {
    const { nodes } = groupedScaffold(makeCounter());
    const groups = nodes.filter((node) => node.kind === 'group');
    const devices = nodes.filter((node) => node.kind === 'device');

    expect(groups).toHaveLength(3);
    expect(groups.every((group) => !group.parentId)).toBe(true);
    expect(new Set(devices.map((device) => device.parentId))).toEqual(
      new Set(groups.map((group) => group.id)),
    );
  });

  const groupedVersions = fc
    .tuple(edits, edits, edits, fc.array(fc.boolean(), { minLength: 1 }))
    .map(([a, b, c, picks]) => {
      const counter = makeCounter();
      const base = applyOps(groupedScaffold(counter), a, counter);

      return {
        base,
        mine: applyOps(base, b, counter),
        theirs: applyOps(base, c, counter),
        picks,
      };
    });

  test('whatever the choices, each node is in a group the merged diagram has, and no groups are inside each other', () => {
    fc.assert(
      fc.property(groupedVersions, ({ base, mine, theirs, picks }) => {
        const { clashes } = mergeDocuments(base, mine, theirs);
        const keys = (list) => list.map((clash) => clash.key).sort();

        // Each side finds the same clashes about groups.
        expect(keys(mergeDocuments(base, theirs, mine).clashes)).toEqual(
          keys(clashes),
        );
        const choices = Object.fromEntries(
          clashes.map((clash, index) => [
            clash.key,
            picks[index % picks.length] ? 'mine' : 'theirs',
          ]),
        );
        const { doc } = mergeDocuments(base, mine, theirs, choices);
        const nodes = new Map(doc.nodes.map((node) => [node.id, node]));

        doc.nodes.forEach((node) => {
          const seen = new Set([node.id]);

          for (let id = node.parentId; id; id = nodes.get(id).parentId) {
            expect(nodes.get(id)?.kind).toBe('group');
            expect(seen.has(id)).toBe(false);
            seen.add(id);
          }
        });
        expect(
          validateDocument(doc).filter((issue) =>
            issue.code.startsWith('node.parent.'),
          ),
        ).toEqual([]);
        // When each version loads, the strict check that the merge review
        // runs finds only the errors the merge leaves to it.
        if ([base, mine, theirs].every((version) => loads(version))) {
          expect(
            validateDocument(doc)
              .filter((issue) => issue.level === 'error')
              .filter((issue) => !LEFT_TO_CHECK.has(issue.code)),
          ).toEqual([]);
        }
      }),
      { numRuns: 300 },
    );
  });

  test('swapping the sides finds the same clashes, and keeps the same elements without one', () => {
    const elements = (doc) =>
      [...doc.nodes, ...doc.edges, ...doc.networks]
        .map((entry) => entry.id)
        .sort();

    fc.assert(
      fc.property(versions, ({ base, mine, theirs }) => {
        const one = mergeDocuments(base, mine, theirs);
        const two = mergeDocuments(base, theirs, mine);
        const keys = (result) =>
          result.clashes.map((clash) => clash.key).sort();

        expect(keys(two)).toEqual(keys(one));
        if (!one.clashes.length) {
          expect(elements(two.doc)).toEqual(elements(one.doc));
        }
      }),
      { numRuns: 100 },
    );
  });

  // Scenario names with no two the same but for case.
  const names = fc.uniqueArray(
    fc.constantFrom('a', 'B', 'b', 'c', 'D', 'd', 'e'),
    {
      maxLength: 5,
      selector: (name) => name.toLowerCase(),
    },
  );
  const folded = (list) => list.map((name) => name.toLowerCase()).sort();

  test('mergeScenarios takes a changed side whole, never twice, either way round', () => {
    fc.assert(
      fc.property(names, names, names, (base, mine, theirs) => {
        expect(mergeScenarios(base, base, theirs)).toEqual(theirs);
        expect(folded(mergeScenarios(base, mine, base))).toEqual(folded(mine));
        const merged = folded(mergeScenarios(base, mine, theirs));

        expect(new Set(merged).size).toBe(merged.length);
        expect(folded(mergeScenarios(base, theirs, mine))).toEqual(merged);
      }),
      { numRuns: 300 },
    );
  });
});
