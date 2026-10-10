import { describe, expect, test } from 'vitest';

import {
  applyChoices,
  changesFrom,
  droppedText,
  mergeDocuments,
  mergeScenarios,
  possessive,
  sameValue,
  valueText,
} from '@/builder/merge.js';
import {
  addNode,
  connect,
  nodeLabel,
  removeInterface,
  renameInterface,
  setDocumentInfo,
} from '@/builder/model.js';

import { sampleDocument, testId } from './fixtures.js';

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
    expect(result.mineChanges.map((change) => change.label)).toEqual([
      'alpha position',
    ]);
    expect(result.theirChanges.map((change) => change.label)).toContain(
      'alpha name',
    );
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
    // Mine renames alpha and moves it; theirs changes only its label.
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
      summary: 'You deleted bravo; they changed its name.',
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
      summary: 'They deleted bravo; you changed its name.',
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
      summary: 'You deleted template web; they changed its description.',
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
      grid: { enabled: true, size: 20, snap: true },
      layout: 'layered',
      source: { kind: 'topology', name: 'plant' },
    };
    const mine = {
      ...base,
      grid: { ...base.grid, size: 10, snap: false },
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
    expect(result.doc.grid.snap).toBe(false);
    expect(result.doc.source.topology).toBe('plant');

    const allMine = result.clashes.map((clash) => [clash.key, 'mine']);
    const chosen = applyChoices(result, Object.fromEntries(allMine));

    expect(chosen.grid).toEqual({ enabled: true, size: 10, snap: false });
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
        /^You deleted connection from alpha to .+; they changed its color\.$/,
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
      summary: 'They deleted network EXP; you changed its description.',
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
        /^They deleted bravo; you changed .+ and connected it\.$/,
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
      `Dropped the connection from bravo to ${nodeLabel(sw)}: the device or interface it connects was deleted.`,
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
      summary: `You deleted ${name}; they connected it.`,
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
      summary: 'They removed bravo interface eth0; you connected it.',
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
      sameValue({ a: 1, b: [1, { c: 2 }] }, { b: [1, { c: 2 }], a: 1 }),
    ).toBe(true);
    expect(sameValue({ a: 1, b: undefined }, { a: 1 })).toBe(true);
    expect(sameValue([1, 2], [2, 1])).toBe(false);
  });

  test('who a merge takes changes from', () => {
    expect(changesFrom('bob', 'alice')).toBe('bob');
    expect(changesFrom('alice', 'alice')).toBe('another tab');
    expect(changesFrom('', 'alice')).toBe('another editor');
    expect(possessive('another tab')).toBe("another tab's");
  });
});
