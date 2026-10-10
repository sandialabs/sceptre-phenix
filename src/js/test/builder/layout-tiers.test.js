import { describe, expect, test } from 'vitest';

import { withGeometry } from '@/builder/layout.js';
import { GRID } from '@/builder/layouts/common.js';
import { runLayout } from '@/builder/layouts/index.js';
import { tierGraph, tierKind } from '@/builder/layouts/tiers.js';
import {
  addNetwork,
  addNode,
  connect,
  createDocument,
  groupNodes,
  setParent,
  setPurdueLevel,
  sizeOf,
} from '@/builder/model.js';

// A small plant: an enterprise host at level 5, a firewall at level 3.5, a
// historian at level 3, an HMI at level 2, two PLCs at level 1 and a sensor
// at level 0, joined by one switch for each pair of levels. The switches
// have no Purdue layer of their own. A note and a device on no network join
// nothing.
const PLANT = [
  ['ERP', '5', ['IT']],
  ['FW', '3.5', ['IT', 'OPS']],
  ['HISTORIAN', '3', ['OPS']],
  ['HMI', '2', ['OPS', 'CONTROL']],
  ['PLC-1', '1', ['CONTROL', 'FIELD']],
  ['PLC-2', '1', ['CONTROL']],
  ['SENSOR', '0', ['FIELD']],
];

function plant({ levels = true } = {}) {
  let doc = createDocument({ name: 'plant' });
  const sw = {};
  const dev = {};

  for (const name of ['IT', 'OPS', 'CONTROL', 'FIELD']) {
    const network = addNetwork(doc, { name });
    const added = addNode(network.doc, {
      kind: 'switch',
      networkId: network.network.id,
    });

    doc = added.doc;
    sw[name] = added.node;
  }

  for (const [hostname, level, networks] of PLANT) {
    const added = addNode(doc, {
      kind: 'device',
      hostname,
      ...(hostname === 'FW' ? { look: { iconKey: 'firewall' } } : {}),
      ...(levels ? { purdueLevel: level } : {}),
    });

    doc = added.doc;
    dev[hostname] = added.node;
    for (const name of networks) {
      doc = connect(doc, {
        sourceNodeId: added.node.id,
        targetNodeId: sw[name].id,
      }).doc;
    }
  }

  const spare = addNode(doc, { kind: 'device', hostname: 'SPARE' });

  doc = spare.doc;
  dev.SPARE = spare.node;
  doc = addNode(doc, { kind: 'note', text: 'Read me' }).doc;

  return { doc, sw, dev };
}

const boxOf = (node) => ({ ...node.position, ...sizeOf(node) });
const inside = (outer, inner) =>
  inner.x >= outer.x &&
  inner.y >= outer.y &&
  inner.x + inner.width <= outer.x + outer.width &&
  inner.y + inner.height <= outer.y + outer.height;
const overlaps = (a, b) =>
  a.x < b.x + b.width &&
  b.x < a.x + a.width &&
  a.y < b.y + b.height &&
  b.y < a.y + a.height;

async function laidOut(doc, options) {
  return withGeometry(doc, await runLayout('tiers', doc, options));
}

// Every node on the grid, on no other node, and every group around its
// members.
function expectTidy(doc) {
  const byId = new Map(doc.nodes.map((node) => [node.id, node]));
  const ancestors = (node) => {
    const found = [];

    for (let at = byId.get(node.parentId); at; at = byId.get(at.parentId)) {
      found.push(at.id);
    }

    return found;
  };

  for (const node of doc.nodes) {
    expect(node.position.x % GRID).toBe(0);
    expect(node.position.y % GRID).toBe(0);

    for (const other of doc.nodes) {
      if (node === other) {
        continue;
      }

      if (ancestors(other).includes(node.id)) {
        expect(inside(boxOf(node), boxOf(other))).toBe(true);
      } else if (!ancestors(node).includes(other.id)) {
        expect(overlaps(boxOf(node), boxOf(other))).toBe(false);
      }
    }
  }
}

// The top and the bottom of a node, as laid out.
function rows(laid) {
  const at = (node) => boxOf(laid.nodes.find((n) => n.id === node.id));

  return {
    top: (node) => at(node).y,
    bottom: (node) => at(node).y + at(node).height,
  };
}

describe('the Layered by tier layout', () => {
  test('puts each Purdue layer below the higher ones', async () => {
    const { doc, dev } = plant();
    const laid = await laidOut(doc);
    const { top, bottom } = rows(laid);
    const order = ['ERP', 'FW', 'HISTORIAN', 'HMI', 'PLC-1', 'SENSOR'];

    // Level 5 above level 0, and every level in between in its turn.
    expect(bottom(dev.ERP)).toBeLessThan(top(dev.SENSOR));
    order.slice(1).forEach((name, index) => {
      expect(bottom(dev[order[index]]), name).toBeLessThanOrEqual(
        top(dev[name]),
      );
    });
    // Two nodes of one level share a row.
    expect(top(dev['PLC-1'])).toBe(top(dev['PLC-2']));
    expectTidy(laid);
  });

  test('puts a switch without a Purdue layer at the higher layer it joins', async () => {
    const { doc, sw, dev } = plant();
    const { top, bottom } = rows(await laidOut(doc));

    // CONTROL joins HMI (level 2) and the PLCs (level 1): it is in level
    // 2, below the HMI, which is nearer the firewall, and above the PLCs.
    expect(bottom(dev.HMI)).toBeLessThanOrEqual(top(sw.CONTROL));
    expect(bottom(sw.CONTROL)).toBeLessThanOrEqual(top(dev['PLC-1']));
    // IT joins ERP (level 5) and the firewall (level 3.5): it is in level
    // 5, above the firewall, and above ERP as a switch is above a host.
    expect(bottom(sw.IT)).toBeLessThanOrEqual(top(dev.ERP));
    expect(bottom(dev.ERP)).toBeLessThanOrEqual(top(dev.FW));
  });

  test('puts what joins no network below the rest', async () => {
    const { doc, dev } = plant();
    const laid = await laidOut(doc);
    const { top, bottom } = rows(laid);
    const note = laid.nodes.find((node) => node.kind === 'note');

    expect(top(dev.SPARE)).toBeGreaterThanOrEqual(bottom(dev.SENSOR));
    expect(note.position.y).toBeGreaterThanOrEqual(bottom(dev.SENSOR));
  });

  test('lays out a diagram without any Purdue layer by the kind of each node', async () => {
    const { doc, sw, dev } = plant({ levels: false });
    const laid = await laidOut(doc);
    const { top, bottom } = rows(laid);

    // The firewall is the root: its switches are below it, and the
    // devices on them below the switches.
    expect(bottom(dev.FW)).toBeLessThanOrEqual(top(sw.IT));
    expect(bottom(dev.FW)).toBeLessThanOrEqual(top(sw.OPS));
    expect(bottom(sw.IT)).toBeLessThanOrEqual(top(dev.ERP));
    expect(bottom(sw.OPS)).toBeLessThanOrEqual(top(dev.HMI));
    expect(bottom(dev.HMI)).toBeLessThanOrEqual(top(sw.CONTROL));
    expect(laid.nodes).toHaveLength(doc.nodes.length);
    expectTidy(laid);
  });

  test('starts from the selected node', async () => {
    const { doc, sw, dev } = plant({ levels: false });
    const { top, bottom } = rows(
      await laidOut(doc, { selected: [dev.SENSOR.id] }),
    );

    expect(bottom(dev.SENSOR)).toBeLessThanOrEqual(top(sw.FIELD));
    expect(bottom(sw.FIELD)).toBeLessThanOrEqual(top(dev['PLC-1']));
    expect(bottom(dev['PLC-1'])).toBeLessThanOrEqual(top(dev.FW));
  });

  test('is the same for the same document', async () => {
    const { doc } = plant();
    const first = await runLayout('tiers', doc);

    expect(await runLayout('tiers', doc)).toEqual(first);

    // A laid-out diagram lays out the same again.
    const laid = await laidOut(doc);

    expect(await laidOut(laid)).toEqual(laid);
  });

  test('keeps groups around their members, nested groups too', async () => {
    const { doc, dev } = plant();
    const control = groupNodes(doc, [dev['PLC-1'].id, dev['PLC-2'].id]);
    const inner = groupNodes(control.doc, [dev.SENSOR.id]);
    const nested = setParent(inner.doc, inner.group.id, control.group.id);
    const laid = await laidOut(nested);
    const find = (node) => laid.nodes.find((n) => n.id === node.id);
    const { top, bottom } = rows(laid);

    expectTidy(laid);
    expect(inside(boxOf(find(control.group)), boxOf(find(inner.group)))).toBe(
      true,
    );
    // A group is sized after its members, and in the group, the layers
    // keep their order.
    expect(find(control.group).size).not.toEqual(control.group.size);
    expect(bottom(dev['PLC-1'])).toBeLessThanOrEqual(top(dev.SENSOR));
    // The group is at the layer of its highest member, below the HMI.
    expect(bottom(dev.HMI)).toBeLessThanOrEqual(top(control.group));
  });

  test('runs the connections into a group in one graph with it', () => {
    const { doc, sw, dev } = plant();
    const grouped = groupNodes(doc, [dev['PLC-1'].id, dev['PLC-2'].id]);
    const graph = tierGraph(grouped.doc);
    const group = graph.children.find((child) => child.id === grouped.group.id);
    const ends = (edges) =>
      edges.map((edge) => [edge.sources[0], edge.targets[0]]);

    expect(graph.layoutOptions['elk.hierarchyHandling']).toBe(
      'INCLUDE_CHILDREN',
    );
    expect(graph.layoutOptions['elk.direction']).toBe('DOWN');
    // Into the group from CONTROL, held by the root, which holds both ends,
    // and run from the higher end down.
    expect(ends(graph.edges)).toContainEqual([sw.CONTROL.id, dev['PLC-1'].id]);
    expect(ends(graph.edges)).toContainEqual([dev.HMI.id, sw.CONTROL.id]);
    expect(group.children.map((child) => child.id)).toEqual([
      dev['PLC-1'].id,
      dev['PLC-2'].id,
    ]);
    // Each node's partition is its tier: level 1 is the sixth from the top.
    expect(group.layoutOptions['elk.partitioning.partition']).toBe(5);
  });

  test('a Purdue layer set on a switch wins over the one it would take', async () => {
    const { doc, sw, dev } = plant();
    const partition = (graph) =>
      graph.children.find((child) => child.id === sw.CONTROL.id).layoutOptions[
        'elk.partitioning.partition'
      ];

    // Without one, CONTROL takes level 2 from the HMI, and is above the
    // PLCs at level 1.
    const before = rows(await laidOut(doc));

    expect(partition(tierGraph(doc))).toBe(4);
    expect(before.bottom(sw.CONTROL)).toBeLessThanOrEqual(
      before.top(dev['PLC-1']),
    );

    // At level 0, it is in that tier, below the PLCs.
    const lowered = setPurdueLevel(doc, sw.CONTROL.id, '0');
    const after = rows(await laidOut(lowered));

    expect(partition(tierGraph(lowered))).toBe(6);
    expect(after.top(sw.CONTROL)).toBeGreaterThanOrEqual(
      after.bottom(dev['PLC-1']),
    );
    expect(after.top(sw.CONTROL)).toBeGreaterThanOrEqual(
      after.bottom(dev['PLC-2']),
    );
  });

  // Every connection joins a device and a switch, so the kind of a node
  // decides no connection that the distance from the root does not.
  test('puts a router below the switch that leads to it from the firewall', async () => {
    let doc = createDocument({ name: 'chain' });
    const make = (options) => {
      const added = addNode(doc, options);

      doc = added.doc;

      return added.node;
    };
    const firewall = make({
      kind: 'device',
      hostname: 'FW',
      look: { iconKey: 'firewall' },
    });
    const dmz = make({ kind: 'switch', networkName: 'DMZ' });
    const router = make({
      kind: 'device',
      hostname: 'RTR',
      look: { iconKey: 'router' },
    });
    const lan = make({ kind: 'switch', networkName: 'LAN' });
    const host = make({ kind: 'device', hostname: 'PC' });

    for (const [a, b] of [
      [firewall, dmz],
      [router, dmz],
      [router, lan],
      [host, lan],
    ]) {
      doc = connect(doc, { sourceNodeId: a.id, targetNodeId: b.id }).doc;
    }

    const { top, bottom } = rows(await laidOut(doc));

    expect(bottom(firewall)).toBeLessThanOrEqual(top(dmz));
    expect(bottom(dmz)).toBeLessThanOrEqual(top(router));
    expect(bottom(router)).toBeLessThanOrEqual(top(lan));
    expect(bottom(lan)).toBeLessThanOrEqual(top(host));
  });

  // An external device is hardware in the loop, such as a PLC or a relay:
  // it goes at the bottom, below the hosts.
  describe('an external device', () => {
    // EXT first in the document, so the document's order does not put it
    // at the top.
    function field() {
      let doc = createDocument({ name: 'field' });
      const make = (options) => {
        const added = addNode(doc, options);

        doc = added.doc;

        return added.node;
      };
      const ext = make({
        kind: 'device',
        hostname: 'EXT',
        spec: { external: true, type: 'HIL' },
      });
      const fieldSw = make({ kind: 'switch', networkName: 'FIELD' });
      const plc = make({ kind: 'device', hostname: 'PLC' });
      const controlSw = make({ kind: 'switch', networkName: 'CONTROL' });
      const firewall = make({
        kind: 'device',
        hostname: 'FW',
        look: { iconKey: 'firewall' },
      });
      const connectAll = (pairs) => {
        for (const [a, b] of pairs) {
          doc = connect(doc, { sourceNodeId: a.id, targetNodeId: b.id }).doc;
        }
      };

      connectAll([
        [ext, fieldSw],
        [plc, fieldSw],
        [plc, controlSw],
        [firewall, controlSw],
      ]);

      return { doc, ext, fieldSw, plc, controlSw, firewall };
    }

    test('is not a root ahead of a firewall', async () => {
      const { doc, ext, fieldSw, plc, controlSw, firewall } = field();
      const { top, bottom } = rows(await laidOut(doc));

      expect(bottom(firewall)).toBeLessThanOrEqual(top(controlSw));
      expect(bottom(controlSw)).toBeLessThanOrEqual(top(plc));
      expect(bottom(plc)).toBeLessThanOrEqual(top(fieldSw));
      expect(bottom(fieldSw)).toBeLessThanOrEqual(top(ext));
    });

    test('is not a root without a firewall or a router', async () => {
      const { doc, ext, fieldSw, plc } = field();
      const lone = {
        ...doc,
        nodes: doc.nodes.filter((node) =>
          [ext.id, fieldSw.id, plc.id].includes(node.id),
        ),
        edges: doc.edges.filter((edge) =>
          [edge.sourceNodeId, edge.targetNodeId].every((id) =>
            [ext.id, fieldSw.id, plc.id].includes(id),
          ),
        ),
      };
      const { top, bottom } = rows(await laidOut(lone));

      // The root is the switch, with both devices below it.
      expect(bottom(fieldSw)).toBeLessThanOrEqual(top(ext));
      expect(bottom(fieldSw)).toBeLessThanOrEqual(top(plc));
    });

    test('is below a switch as far from a root', () => {
      const { doc, ext, fieldSw } = field();
      const graph = tierGraph(doc, { selected: [ext.id, fieldSw.id] });
      const ends = graph.edges.map((edge) => [
        edge.sources[0],
        edge.targets[0],
      ]);

      expect(ends).toContainEqual([fieldSw.id, ext.id]);
    });
  });

  test('knows a node by its icon or its type', () => {
    const device = (fields) => ({
      kind: 'device',
      device: { hostname: 'x', spec: {}, ...fields },
    });

    expect(tierKind({ kind: 'switch', switch: {} })).toBe('switch');
    expect(tierKind(device({ iconKey: 'external' }))).toBe('external');
    expect(tierKind(device({ spec: { external: true } }))).toBe('external');
    expect(tierKind(device({ spec: { type: 'HIL' } }))).toBe('external');
    expect(tierKind(device({ iconKey: 'firewall' }))).toBe('firewall');
    expect(tierKind(device({ spec: { type: 'Firewall' } }))).toBe('firewall');
    expect(tierKind(device({ spec: { type: 'Router' } }))).toBe('router');
    expect(tierKind(device({ iconKey: 'server' }))).toBe('host');
  });

  test('lays out an empty diagram', async () => {
    expect(await runLayout('tiers', createDocument())).toEqual({
      positions: {},
      sizes: {},
    });
  });
});
