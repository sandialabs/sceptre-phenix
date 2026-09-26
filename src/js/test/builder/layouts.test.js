import { afterEach, describe, expect, test, vi } from 'vitest';

import { withGeometry } from '@/builder/layout.js';
import { GRID } from '@/builder/layouts/common.js';
import {
  DEFAULT_LAYOUT_ALGORITHM,
  LAYOUT_ALGORITHMS,
  runLayout,
  stopLayoutEngine,
} from '@/builder/layouts/index.js';
import {
  addNetwork,
  addNode,
  connect,
  createDocument,
  groupNodes,
  setParent,
  sizeOf,
} from '@/builder/model.js';
import { handleOffsetY } from '@/builder/routes.js';
import { endBuilderSession } from '@/builder/session.js';

// Four networks joined by firewalls, as a small range is: every device a
// connection source on a new interface, as the editor makes them, except
// HMI-2, connected from its switch's side. Names cut across networks (PLC
// on two), and IT-WS-2 must come before IT-WS-10. A note and a device on
// no network join nothing.
const DEVICES = [
  ['IT-WS-10', ['CORP']],
  ['DB-HR', ['CORP']],
  ['IT-WS-2', ['CORP']],
  ['IT-PRINTER', ['CORP']],
  ['DB-ERP', ['CORP']],
  ['IT-WS-1', ['CORP']],
  ['FW-CORP', ['DMZ', 'CORP']],
  ['WEB-1', ['DMZ']],
  ['WEB-2', ['DMZ']],
  ['DMZ-MAIL', ['DMZ']],
  ['FW-OT', ['OT', 'CORP']],
  ['PLC-1', ['OT']],
  ['HMI-1', ['OT']],
  ['PLC-2', ['OT']],
  ['FW-SIS', ['SAFETY', 'OT']],
  ['PLC-SIS-1', ['SAFETY']],
];

function range() {
  let doc = createDocument({ name: 'range' });
  const sw = {};
  const dev = {};

  for (const name of ['CORP', 'DMZ', 'OT', 'SAFETY']) {
    const network = addNetwork(doc, { name });
    const added = addNode(network.doc, {
      kind: 'switch',
      networkId: network.network.id,
    });

    doc = added.doc;
    sw[name] = added.node;
  }

  const add = (hostname) => {
    const added = addNode(doc, { kind: 'device', hostname });

    doc = added.doc;
    dev[hostname] = added.node;
  };

  for (const [hostname, networks] of DEVICES) {
    add(hostname);
    for (const name of networks) {
      doc = connect(doc, {
        sourceNodeId: dev[hostname].id,
        targetNodeId: sw[name].id,
      }).doc;
    }
  }

  add('HMI-2');
  doc = connect(doc, {
    sourceNodeId: sw.OT.id,
    targetNodeId: dev['HMI-2'].id,
  }).doc;
  add('SPARE');
  doc = addNode(doc, { kind: 'note', text: 'Read me' }).doc;

  return { doc, sw, dev };
}

// Builds a document with ids counted in the order they are made, so it is
// the same document every run: dagre takes nodes and connections in id
// order, and lays them out differently in another.
function countedIds(build) {
  let count = 0;

  vi.stubGlobal('crypto', {
    randomUUID: () => {
      count += 1;

      return `00000000-0000-4000-8000-${String(count).padStart(12, '0')}`;
    },
  });

  try {
    return build();
  } finally {
    vi.unstubAllGlobals();
  }
}

const NETWORK_LAYOUTS = ['elk', 'cards', 'dagre'];

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

async function laidOut(id, doc) {
  return withGeometry(doc, await runLayout(id, doc));
}

// No node on another, and every group around its members only.
function expectTidy(doc, message) {
  const byId = new Map(doc.nodes.map((node) => [node.id, node]));
  const ancestors = (node) => {
    const found = [];

    for (let at = byId.get(node.parentId); at; at = byId.get(at.parentId)) {
      found.push(at.id);
    }

    return found;
  };

  for (const node of doc.nodes) {
    for (const other of doc.nodes) {
      if (node === other) {
        continue;
      }

      if (ancestors(other).includes(node.id)) {
        expect(inside(boxOf(node), boxOf(other)), message).toBe(true);
      } else if (!ancestors(node).includes(other.id)) {
        expect(overlaps(boxOf(node), boxOf(other)), message).toBe(false);
      }
    }
  }
}

describe.each(NETWORK_LAYOUTS)('the %s layout', (id) => {
  test('is deterministic, whatever the order of the document', async () => {
    const { doc } = range();
    const shuffled = {
      ...doc,
      nodes: [...doc.nodes].reverse(),
      edges: [...doc.edges].reverse(),
    };
    const first = await runLayout(id, doc);

    expect(await runLayout(id, doc)).toEqual(first);
    expect(await runLayout(id, shuffled)).toEqual(first);
  });

  test('places every node on the grid, on no other node', async () => {
    const { doc } = range();
    const laid = await laidOut(id, doc);

    expect(laid.nodes).toHaveLength(doc.nodes.length);
    for (const node of laid.nodes) {
      expect(node.position.x % GRID).toBe(0);
      expect(node.position.y % GRID).toBe(0);
    }
    expectTidy(laid, id);
  });

  test('runs every connection left to right', async () => {
    const { doc } = range();
    const laid = await laidOut(id, doc);
    const byId = new Map(laid.nodes.map((node) => [node.id, node]));

    for (const edge of laid.edges) {
      const source = boxOf(byId.get(edge.sourceNodeId));
      const target = boxOf(byId.get(edge.targetNodeId));

      expect(target.x).toBeGreaterThan(source.x + source.width);
    }
  });

  test('keeps each network together', async () => {
    const { doc, sw, dev } = range();
    const laid = await laidOut(id, doc);
    const at = (node) => boxOf(laid.nodes.find((n) => n.id === node.id));
    // Each network's switch and the devices that go with it: a firewall
    // with its smaller network, HMI-2 with OT.
    const clusters = {
      CORP: ['IT-WS-10', 'DB-HR', 'IT-WS-2', 'IT-PRINTER', 'DB-ERP', 'IT-WS-1'],
      DMZ: ['FW-CORP', 'WEB-1', 'WEB-2', 'DMZ-MAIL'],
      OT: ['FW-OT', 'PLC-1', 'HMI-1', 'PLC-2', 'HMI-2'],
      SAFETY: ['FW-SIS', 'PLC-SIS-1'],
    };
    const bounds = Object.entries(clusters).map(([name, hosts]) => {
      const boxes = [sw[name], ...hosts.map((host) => dev[host])].map(at);
      const x = Math.min(...boxes.map((box) => box.x));
      const y = Math.min(...boxes.map((box) => box.y));

      return {
        name,
        x,
        y,
        width: Math.max(...boxes.map((box) => box.x + box.width)) - x,
        height: Math.max(...boxes.map((box) => box.y + box.height)) - y,
      };
    });

    for (const a of bounds) {
      for (const b of bounds) {
        if (a !== b) {
          expect(overlaps(a, b), `${a.name} and ${b.name}`).toBe(false);
        }
      }
    }

    // What joins no network goes below the rest.
    const lowest = Math.max(...bounds.map((bound) => bound.y + bound.height));
    const note = laid.nodes.find((node) => node.kind === 'note');

    expect(at(dev.SPARE).y).toBeGreaterThan(lowest);
    expect(note.position.y).toBeGreaterThan(lowest);
  });

  test('keeps groups around their members, nested groups too', async () => {
    for (let run = 0; run < 5; run += 1) {
      const { doc, sw, dev } = range();
      // Two workstations; a whole network with its switch; and PLC-1 in a
      // group of its own inside another with HMI-1.
      let grouped = groupNodes(doc, [dev['IT-WS-1'].id, dev['IT-WS-2'].id]);
      grouped = groupNodes(grouped.doc, [
        sw.DMZ.id,
        dev['WEB-1'].id,
        dev['WEB-2'].id,
      ]);
      const inner = groupNodes(grouped.doc, [dev['PLC-1'].id]);
      const outer = groupNodes(inner.doc, [dev['HMI-1'].id]);
      const nested = setParent(outer.doc, inner.group.id, outer.group.id);
      const laid = await laidOut(id, nested);
      const find = (node) => laid.nodes.find((n) => n.id === node.id);

      expectTidy(laid, `${id}, run ${run}`);
      expect(inside(boxOf(find(outer.group)), boxOf(find(inner.group)))).toBe(
        true,
      );
      // A group is sized after its members, not what it was.
      expect(find(inner.group).size).not.toEqual(inner.group.size);
      // A laid-out diagram lays out the same again.
      expect(await laidOut(id, laid)).toEqual(laid);
    }
  });
});

describe('the ELK layout', () => {
  test('routes the connections in a network from handle to handle, across and down', async () => {
    const { doc, sw, dev } = range();
    // WEB-1 and WEB-2 with their switch in a group: routed inside it.
    const grouped = groupNodes(doc, [
      sw.DMZ.id,
      dev['WEB-1'].id,
      dev['WEB-2'].id,
    ]).doc;
    const laid = withGeometry(grouped, await runLayout('elk', grouped));
    const byId = new Map(laid.nodes.map((node) => [node.id, node]));
    const between = (source, target) =>
      laid.edges.find(
        (edge) =>
          edge.sourceNodeId === source.id && edge.targetNodeId === target.id,
      );
    const routed = laid.edges.filter((edge) => edge.route);

    for (const edge of routed) {
      const source = byId.get(edge.sourceNodeId);
      const target = byId.get(edge.targetNodeId);
      const { route } = edge;

      expect(route[0]).toEqual({
        x: source.position.x + sizeOf(source).width,
        y: source.position.y + handleOffsetY(source, edge.sourceHandleId),
      });
      expect(route[route.length - 1]).toEqual({
        x: target.position.x,
        y: target.position.y + handleOffsetY(target, edge.targetHandleId),
      });
      route.slice(1).forEach((point, index) => {
        expect(
          point.x === route[index].x || point.y === route[index].y,
          'across or down',
        ).toBe(true);
      });
    }

    expect(between(dev['WEB-1'], sw.DMZ).route).toBeDefined();
    expect(between(dev['IT-WS-1'], sw.CORP).route).toBeDefined();
    // From a switch, too.
    expect(between(sw.OT, dev['HMI-2']).route).toBeDefined();
    // Between networks, and out of the group, ELK draws no route.
    expect(between(dev['FW-OT'], sw.CORP).route).toBeUndefined();
    expect(between(dev['FW-CORP'], sw.DMZ).route).toBeUndefined();
    // The other layouts draw none.
    expect((await runLayout('cards', doc)).routes).toBeUndefined();
    expect((await runLayout('dagre', doc)).routes).toBeUndefined();
  });
});

describe('the network cards layout', () => {
  test('orders a network by name prefix, then naturally', async () => {
    const { doc, dev, sw } = range();
    const laid = await laidOut('cards', doc);
    const at = (node) => laid.nodes.find((n) => n.id === node.id).position;
    const corp = DEVICES.filter(([, networks]) => networks[0] === 'CORP')
      .map(([host]) => host)
      .sort((a, b) => at(dev[a]).y - at(dev[b]).y);

    // IT (four) before DB (two), IT-WS-2 before IT-WS-10, and a wider gap
    // between the two.
    expect(corp).toEqual([
      'IT-PRINTER',
      'IT-WS-1',
      'IT-WS-2',
      'IT-WS-10',
      'DB-ERP',
      'DB-HR',
    ]);
    expect(at(dev['DB-ERP']).y - at(dev['IT-WS-10']).y).toBeGreaterThan(
      at(dev['IT-WS-10']).y - at(dev['IT-WS-2']).y,
    );

    // A gateway first, then its network's devices; the switch right of
    // them, HMI-2 (connected from the switch) right of the switch.
    expect(at(dev['FW-OT']).y).toBeLessThan(at(dev['HMI-1']).y);
    expect(at(sw.OT).x).toBeGreaterThan(at(dev['PLC-1']).x);
    expect(at(dev['HMI-2']).x).toBeGreaterThan(at(sw.OT).x);
  });

  test('stacks a large network in staggered columns', async () => {
    let doc = createDocument();
    const network = addNetwork(doc, { name: 'BIG' });
    const sw = addNode(network.doc, {
      kind: 'switch',
      networkId: network.network.id,
    });

    doc = sw.doc;
    for (let n = 1; n <= 20; n += 1) {
      const added = addNode(doc, { kind: 'device', hostname: `WS-${n}` });

      doc = connect(added.doc, {
        sourceNodeId: added.node.id,
        targetNodeId: sw.node.id,
      }).doc;
    }

    const laid = await laidOut('cards', doc);
    const columns = new Set(
      laid.nodes
        .filter((node) => node.kind === 'device')
        .map((node) => node.position.x),
    );

    expect(columns.size).toBe(3);
    expectTidy(laid, 'cards');
  });
});

describe('the dagre layout', () => {
  test('lays out networks joined both ways', async () => {
    // The small DMZ joins LAN both ways: its firewalls connect to LAN's
    // switch, and WEB-1 is connected from it. Dagre threw "Not possible to
    // find intersection inside of the rectangle" on the parallel edges
    // between the two, when the smaller network's name came first.
    const doc = countedIds(() => {
      let built = createDocument({ name: 'dmz' });
      const sw = {};
      const add = (hostname) => {
        const added = addNode(built, { kind: 'device', hostname });

        built = added.doc;

        return added.node;
      };
      const link = (source, target) => {
        built = connect(built, {
          sourceNodeId: source.id,
          targetNodeId: target.id,
        }).doc;
      };

      for (const name of ['LAN', 'DMZ', 'INTERNET']) {
        const network = addNetwork(built, { name });
        const added = addNode(network.doc, {
          kind: 'switch',
          networkId: network.network.id,
        });

        built = added.doc;
        sw[name] = added.node;
      }
      for (let n = 1; n <= 5; n += 1) {
        link(add(`WS-${n}`), sw.LAN);
      }
      for (let n = 1; n <= 4; n += 1) {
        link(add(`EXT-${n}`), sw.INTERNET);
      }
      for (const hostname of ['FW-1', 'FW-2']) {
        const firewall = add(hostname);

        link(firewall, sw.LAN);
        link(firewall, sw.DMZ);
      }

      const web = add('WEB-1');

      link(sw.LAN, web);
      link(web, sw.DMZ);

      const edge = add('EDGE');

      link(edge, sw.INTERNET);
      link(edge, sw.DMZ);

      return built;
    });

    expectTidy(await laidOut('dagre', doc), 'dagre');
  });

  test('wraps a rank of many networks into columns', async () => {
    // Twelve sites, each gatewayed into one core: one rank of twelve.
    let doc = createDocument();
    const core = addNetwork(doc, { name: 'CORE' });
    const coreSwitch = addNode(core.doc, {
      kind: 'switch',
      networkId: core.network.id,
    });

    doc = coreSwitch.doc;
    for (let site = 1; site <= 12; site += 1) {
      const network = addNetwork(doc, { name: `SITE-${site}` });
      const sw = addNode(network.doc, {
        kind: 'switch',
        networkId: network.network.id,
      });

      doc = sw.doc;
      for (const role of ['GW', 'WS-1', 'WS-2', 'WS-3']) {
        const device = addNode(doc, {
          kind: 'device',
          hostname: `S${site}-${role}`,
        });

        doc = connect(device.doc, {
          sourceNodeId: device.node.id,
          targetNodeId: sw.node.id,
        }).doc;
        if (role === 'GW') {
          doc = connect(doc, {
            sourceNodeId: device.node.id,
            targetNodeId: coreSwitch.node.id,
          }).doc;
        }
      }
    }

    const laid = await laidOut('dagre', doc);
    const boxes = laid.nodes.map(boxOf);
    const width =
      Math.max(...boxes.map((box) => box.x + box.width)) -
      Math.min(...boxes.map((box) => box.x));
    const height =
      Math.max(...boxes.map((box) => box.y + box.height)) -
      Math.min(...boxes.map((box) => box.y));
    const switches = laid.nodes.filter(
      (node) => node.kind === 'switch' && node.id !== coreSwitch.node.id,
    );

    // Near 16:10, not one tall column.
    expect(width / height).toBeGreaterThan(1.2);
    expect(width / height).toBeLessThan(2.2);
    expect(
      new Set(switches.map((node) => node.position.x)).size,
    ).toBeGreaterThan(1);
    expectTidy(laid, 'dagre');

    // Still left to right, the core after every site.
    const byId = new Map(laid.nodes.map((node) => [node.id, node]));

    for (const edge of laid.edges) {
      const source = boxOf(byId.get(edge.sourceNodeId));
      const target = boxOf(byId.get(edge.targetNodeId));

      expect(target.x).toBeGreaterThan(source.x + source.width);
    }
  });
});

describe('the ELK layout', () => {
  test('clusters each network, with ports where the Builder draws handles', async () => {
    const { doc } = range();
    const scopes = [];
    const fake = {
      layout: async (graph) => {
        scopes.push(graph);

        // Every node in a column: enough to place them.
        let y = 0;

        return {
          ...graph,
          children: graph.children.map((cluster) => ({
            ...cluster,
            x: 0,
            y: 0,
            children: cluster.children.map((child) => {
              y += child.height + GRID;

              return { ...child, x: 0, y };
            }),
          })),
        };
      },
    };

    await runLayout('elk', doc, { elk: fake });

    const [graph] = scopes;

    expect(graph.children.map((cluster) => cluster.children.length)).toEqual([
      7, 5, 6, 3,
    ]);
    for (const cluster of graph.children) {
      for (const node of cluster.children) {
        for (const port of node.ports) {
          const side = port.layoutOptions['elk.port.side'];

          expect(port.x).toBe(side === 'EAST' ? node.width : 0);
        }
      }
    }
    // Every connection, from a port on its source to one on its target.
    expect(graph.edges).toHaveLength(doc.edges.length);
  });

  describe('in a Web Worker', () => {
    // Workers as ELK's, each answering a layout with every node at the
    // origin, or holding its answers while `held` is set.
    const workers = [];
    let held = false;

    class FakeWorker {
      constructor() {
        this.layouts = 0;
        this.terminated = false;
        workers.push(this);
      }

      addEventListener() {}

      postMessage(message) {
        this.layouts += message.cmd === 'layout' ? 1 : 0;
        if (message.cmd === 'layout' && !held) {
          const place = (node) => ({
            ...node,
            x: 0,
            y: 0,
            children: node.children?.map(place),
          });

          this.onmessage({
            data: { id: message.id, data: place(message.graph) },
          });
        }
      }

      terminate() {
        this.terminated = true;
      }
    }

    afterEach(() => {
      stopLayoutEngine();
      workers.length = 0;
      held = false;
      vi.useRealTimers();
      vi.unstubAllEnvs();
      vi.unstubAllGlobals();
    });

    test('keeps its worker for the next layout, until the session ends', async () => {
      // The tests before laid out in-thread, as tests do.
      stopLayoutEngine();
      vi.stubEnv('MODE', 'production');
      vi.stubGlobal('Worker', FakeWorker);
      // Time runs, and can be moved on.
      vi.useFakeTimers({ shouldAdvanceTime: true });

      const { doc } = range();
      const settled = (promise) =>
        promise.then(
          () => 'laid out',
          (error) => error.name,
        );

      expect(await settled(runLayout('elk', doc))).toBe('laid out');
      // Long idle, the worker stays: another would download elkjs again.
      await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
      expect(await settled(runLayout('elk', doc))).toBe('laid out');
      expect(workers).toHaveLength(1);
      expect(workers[0].terminated).toBe(false);

      // The session's end stops it, and the layout under way.
      held = true;

      const running = settled(runLayout('elk', doc));

      await vi.waitFor(() => expect(workers[0].layouts).toBe(3));
      await endBuilderSession({
        localStorage: null,
        sessionStorage: null,
        clearDatabase: async () => true,
      });
      expect(workers[0].terminated).toBe(true);
      expect(await running).toBe('AbortError');

      // The next layout starts another.
      held = false;
      expect(await settled(runLayout('elk', doc))).toBe('laid out');
      expect(workers).toHaveLength(2);
      expect(workers[1].terminated).toBe(false);
    });
  });
});

describe('choosing a layout', () => {
  test('every algorithm has a layout, and an unknown one runs the default', async () => {
    const { doc } = range();

    expect(LAYOUT_ALGORITHMS.map((algorithm) => algorithm.id)).toEqual([
      'elk',
      'cards',
      'dagre',
      'standard',
    ]);
    expect(await runLayout('nonesuch', doc)).toEqual(
      await runLayout(DEFAULT_LAYOUT_ALGORITHM, doc),
    );
  });

  test('the standard layout is the original Auto layout', async () => {
    const { doc, grouped, sw, dev } = countedIds(() => {
      const built = range();

      return {
        ...built,
        grouped: groupNodes(built.doc, [
          built.dev['IT-WS-1'].id,
          built.dev['IT-WS-2'].id,
        ]).doc,
      };
    });
    const names = new Map([
      ...Object.entries(sw).map(([name, node]) => [node.id, `switch ${name}`]),
      ...Object.entries(dev).map(([name, node]) => [node.id, name]),
    ]);
    const geometry = (laid) =>
      Object.fromEntries(
        laid.nodes.map((node) => [
          names.get(node.id) || node.kind,
          node.kind === 'group'
            ? { ...node.position, ...node.size }
            : node.position,
        ]),
      );

    // What layout.js gave, before the layouts here: the range, then the
    // range with IT-WS-1 and IT-WS-2 in a group.
    expect(geometry(await laidOut('standard', doc))).toEqual({
      'switch CORP': { x: 780, y: 230 },
      'switch DMZ': { x: 1770, y: 230 },
      'switch OT': { x: 2650, y: 230 },
      'switch SAFETY': { x: 3300, y: 230 },
      'IT-WS-10': { x: 1340, y: 30 },
      'DB-HR': { x: 900, y: 30 },
      'IT-WS-2': { x: 680, y: 30 },
      'IT-PRINTER': { x: 460, y: 30 },
      'DB-ERP': { x: 240, y: 30 },
      'IT-WS-1': { x: 20, y: 30 },
      'FW-CORP': { x: 1120, y: 30 },
      'WEB-1': { x: 2220, y: 30 },
      'WEB-2': { x: 2000, y: 30 },
      'DMZ-MAIL': { x: 1560, y: 30 },
      'FW-OT': { x: 1780, y: 30 },
      'PLC-1': { x: 2880, y: 30 },
      'HMI-1': { x: 2660, y: 30 },
      'PLC-2': { x: 2440, y: 30 },
      'FW-SIS': { x: 3100, y: 30 },
      'PLC-SIS-1': { x: 3320, y: 30 },
      'HMI-2': { x: 2660, y: 390 },
      SPARE: { x: 3540, y: 30 },
      note: { x: 3760, y: 20 },
    });
    expect(geometry(await laidOut('standard', grouped))).toEqual({
      'switch CORP': { x: 810, y: 320 },
      'switch DMZ': { x: 1700, y: 320 },
      'switch OT': { x: 2690, y: 320 },
      'switch SAFETY': { x: 3340, y: 320 },
      'IT-WS-10': { x: 20, y: 80 },
      'DB-HR': { x: 240, y: 80 },
      'IT-WS-2': { x: 480, y: 80 },
      'IT-PRINTER': { x: 940, y: 80 },
      'DB-ERP': { x: 1160, y: 80 },
      'IT-WS-1': { x: 700, y: 80 },
      'FW-CORP': { x: 1380, y: 80 },
      'WEB-1': { x: 1600, y: 80 },
      'WEB-2': { x: 1820, y: 80 },
      'DMZ-MAIL': { x: 2040, y: 80 },
      'FW-OT': { x: 2260, y: 80 },
      'PLC-1': { x: 2480, y: 80 },
      'HMI-1': { x: 2700, y: 80 },
      'PLC-2': { x: 2920, y: 80 },
      'FW-SIS': { x: 3140, y: 80 },
      'PLC-SIS-1': { x: 3360, y: 80 },
      'HMI-2': { x: 2700, y: 480 },
      SPARE: { x: 3580, y: 80 },
      note: { x: 3800, y: 70 },
      group: { x: 440, y: 20, width: 460, height: 210 },
    });
  });
});
