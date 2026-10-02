import { describe, expect, test } from 'vitest';

import {
  addNetwork,
  addNode,
  connect,
  groupNodes,
  removeElements,
} from '@/builder/model.js';
import {
  kindSelection,
  pressSelection,
  selectionItemName,
} from '@/builder/selection.js';

import { sampleDocument } from './fixtures.js';

const none = () => ({ nodes: [], edges: [] });

describe('pressing a node or connection', () => {
  test('names nodes by label and connections by their ends', () => {
    const { doc, alpha, edge } = sampleDocument();

    expect(selectionItemName(doc, { kind: 'nodes', id: alpha.id })).toBe(
      'alpha',
    );
    expect(selectionItemName(doc, { kind: 'edges', id: edge.id })).toBe(
      'the connection between alpha (eth0) and EXP',
    );
    expect(selectionItemName(doc, { kind: 'edges', id: 'gone' })).toBe(
      'the connection',
    );
  });

  test('a plain press selects the item, says "only" when it drops others, and deselects the only item', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const item = { kind: 'nodes', id: alpha.id };

    expect(pressSelection(doc, none(), item)).toEqual({
      selection: { nodes: [alpha.id], edges: [] },
      message: 'Selected alpha',
    });

    // Another item, here a connection, leaves the selection: say so.
    expect(pressSelection(doc, { nodes: [], edges: [edge.id] }, item)).toEqual({
      selection: { nodes: [alpha.id], edges: [] },
      message: 'Selected alpha only',
    });
    expect(
      pressSelection(doc, { nodes: [alpha.id, bravo.id], edges: [] }, item),
    ).toEqual({
      selection: { nodes: [alpha.id], edges: [] },
      message: 'Selected alpha only',
    });

    expect(pressSelection(doc, { nodes: [alpha.id], edges: [] }, item)).toEqual(
      {
        selection: { nodes: [], edges: [] },
        message: 'Deselected alpha',
      },
    );
  });

  // Regression: the outline counted nodes only, so with a connection
  // selected it said "1 node selected" while 2 items were.
  test('an additive press counts nodes and connections', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();
    const item = { kind: 'nodes', id: alpha.id };

    expect(
      pressSelection(doc, { nodes: [], edges: [edge.id] }, item, true),
    ).toEqual({
      selection: { nodes: [alpha.id], edges: [edge.id] },
      message: 'Added alpha to the selection, 2 items selected',
    });
    expect(
      pressSelection(doc, { nodes: [alpha.id], edges: [edge.id] }, item, true),
    ).toEqual({
      selection: { nodes: [], edges: [edge.id] },
      message: 'Removed alpha from the selection, 1 item selected',
    });
    expect(
      pressSelection(doc, { nodes: [alpha.id], edges: [] }, item, true).message,
    ).toBe('Removed alpha from the selection, 0 items selected');
    expect(
      pressSelection(
        doc,
        { nodes: [bravo.id], edges: [edge.id] },
        { kind: 'edges', id: edge.id },
        true,
      ),
    ).toEqual({
      selection: { nodes: [bravo.id], edges: [] },
      message: expect.stringMatching(
        /^Removed the connection between alpha \(eth0\) and .+ from the selection, 1 item selected$/,
      ),
    });
  });

  test('leaves the selection it was given unchanged', () => {
    const { doc, alpha, edge } = sampleDocument();
    const before = { nodes: [alpha.id], edges: [edge.id] };

    pressSelection(doc, before, { kind: 'nodes', id: alpha.id }, true);
    pressSelection(doc, before, { kind: 'edges', id: edge.id });

    expect(before).toEqual({ nodes: [alpha.id], edges: [edge.id] });
  });
});

describe('pressing one of the header’s counts', () => {
  // The sample (alpha and bravo, the EXP switch, one connection), with a
  // note, and a group around bravo.
  function diagram() {
    const sample = sampleDocument();
    const noted = addNode(sample.doc, { kind: 'note', text: 'hi' });
    const grouped = groupNodes(noted.doc, [sample.bravo.id]);

    return {
      ...sample,
      doc: grouped.doc,
      note: noted.node,
      group: grouped.group,
    };
  }

  test('selects every item of the kind, in place of the selection', () => {
    const { doc, alpha, bravo, sw, edge, note, group } = diagram();

    expect(kindSelection(doc, 'devices')).toEqual({
      selection: { nodes: [alpha.id, bravo.id], edges: [] },
      message: 'Selected 2 devices.',
    });
    expect(kindSelection(doc, 'switches')).toEqual({
      selection: { nodes: [sw.id], edges: [] },
      message: 'Selected 1 switch.',
    });
    // Connections are every connection, and no node.
    expect(kindSelection(doc, 'links')).toEqual({
      selection: { nodes: [], edges: [edge.id] },
      message: 'Selected 1 connection.',
    });
    expect(kindSelection(doc, 'groups')).toEqual({
      selection: { nodes: [group.id], edges: [] },
      message: 'Selected 1 group.',
    });
    expect(kindSelection(doc, 'notes')).toEqual({
      selection: { nodes: [note.id], edges: [] },
      message: 'Selected 1 note.',
    });
  });

  test('a count of none selects nothing, and says why', () => {
    const empty = { nodes: [], edges: [], networks: [] };

    for (const [key, words] of [
      ['devices', 'devices'],
      ['switches', 'switches'],
      ['networks', 'networks'],
      ['links', 'connections'],
      ['groups', 'groups'],
      ['notes', 'notes'],
    ]) {
      expect(kindSelection(empty, key), key).toEqual({
        selection: null,
        message: `There are no ${words} to select.`,
      });
    }

    // No document yet, and a key that is no count.
    expect(kindSelection(null, 'devices').selection).toBeNull();
    expect(kindSelection(diagram().doc, 'everything')).toEqual({
      selection: null,
      message: '',
    });
  });

  test('the networks count selects the switches that show a network', () => {
    const { doc, sw } = diagram();

    expect(kindSelection(doc, 'networks')).toEqual({
      selection: { nodes: [sw.id], edges: [] },
      message: 'Selected 1 switch of 1 network.',
    });

    // A second switch of the same network, as a pasted one is.
    const second = addNode(doc, {
      kind: 'switch',
      networkId: sw.switch.networkId,
    });

    expect(kindSelection(second.doc, 'networks')).toEqual({
      selection: { nodes: [sw.id, second.node.id], edges: [] },
      message: 'Selected 2 switches of 1 network.',
    });

    // Networks no switch shows are counted, and said.
    const one = addNetwork(second.doc, { name: 'OT' }).doc;

    expect(kindSelection(one, 'networks').message).toBe(
      'Selected 2 switches of 2 networks. 1 network has no switch.',
    );

    const two = addNetwork(one, { name: 'MGMT' }).doc;

    expect(kindSelection(two, 'networks').message).toBe(
      'Selected 2 switches of 3 networks. 2 networks have no switch.',
    );
  });

  test('networks that no switch shows have nothing to select', () => {
    const { doc, sw } = diagram();
    const bare = removeElements(doc, { nodes: [sw.id], edges: [] });

    // Deleting a switch keeps its network.
    expect(bare.networks).toHaveLength(1);
    expect(kindSelection(bare, 'networks')).toEqual({
      selection: null,
      message: 'No switch shows a network, so there is nothing to select.',
    });
    expect(kindSelection(bare, 'switches')).toEqual({
      selection: null,
      message: 'There are no switches to select.',
    });
  });

  test('included devices are selected with the rest', () => {
    const { doc, alpha, bravo, sw } = diagram();
    const included = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === alpha.id
          ? { ...node, device: { ...node.device, includedFrom: 'base' } }
          : node,
      ),
    };

    expect(kindSelection(included, 'devices').selection.nodes).toEqual([
      alpha.id,
      bravo.id,
    ]);
    // A connection of an included device is a connection like any other.
    const more = connect(included, {
      sourceNodeId: bravo.id,
      targetNodeId: sw.id,
    }).doc;

    expect(kindSelection(more, 'links').message).toBe(
      'Selected 2 connections.',
    );
  });
});
