// fast-check generators shared by the contract suites. Pure: a document gets
// its ids and names from the counter of its own run (makeCounter).

import fc from 'fast-check';

import {
  addInterface,
  addNetwork,
  addNode,
  connect,
  createDocument,
  groupNodes,
  moveNodes,
  removeElements,
  removeInterface,
  setParent,
  ungroup,
  updateNetwork,
  updateNode,
} from '@/builder/model.js';

/**
 * Ids and names for one run. Names share the count, so they are unique, as
 * validateDocument requires of hostnames and networks.
 */
export function makeCounter() {
  let count = 0;

  return {
    id: () => `00000000-0000-4000-8000-${String(++count).padStart(12, '0')}`,
    name: (prefix) => `${prefix}${++count}`,
  };
}

// The item at `index` modulo the length, so an op can name any item.
const pick = (items, index) =>
  items.length ? items[index % items.length] : undefined;

const coordinate = fc.integer({ min: -500, max: 500 });

const OPS = [
  [2, fc.record({ op: fc.constant('network') })],
  [
    5,
    fc.record({
      op: fc.constant('device'),
      x: coordinate,
      y: coordinate,
      interfaces: fc.nat(3),
    }),
  ],
  [
    3,
    fc.record({
      op: fc.constant('switch'),
      network: fc.nat(),
      x: coordinate,
      y: coordinate,
    }),
  ],
  [1, fc.record({ op: fc.constant('note'), x: coordinate, y: coordinate })],
  [1, fc.record({ op: fc.constant('line'), x: coordinate, y: coordinate })],
  [
    6,
    fc.record({
      op: fc.constant('connect'),
      device: fc.nat(),
      switch: fc.nat(),
      handle: fc.nat(),
    }),
  ],
  [
    2,
    fc.record({
      op: fc.constant('group'),
      nodes: fc.array(fc.nat(), { maxLength: 3 }),
    }),
  ],
  [1, fc.record({ op: fc.constant('ungroup'), group: fc.nat() })],
  [
    2,
    fc.record({ op: fc.constant('parent'), node: fc.nat(), group: fc.nat() }),
  ],
  [1, fc.record({ op: fc.constant('remove'), node: fc.nat() })],
  [1, fc.record({ op: fc.constant('rename'), device: fc.nat() })],
  [2, fc.record({ op: fc.constant('addInterface'), device: fc.nat() })],
  [
    1,
    fc.record({
      op: fc.constant('removeInterface'),
      device: fc.nat(),
      handle: fc.nat(),
    }),
  ],
  [1, fc.record({ op: fc.constant('renameNetwork'), network: fc.nat() })],
  [
    1,
    fc.record({
      op: fc.constant('move'),
      node: fc.nat(),
      x: coordinate,
      y: coordinate,
    }),
  ],
  [1, fc.record({ op: fc.constant('disconnect'), edge: fc.nat() })],
];

/** One edit of a document, its nodes named by fc.nat() modulo their count. */
export const documentOp = fc.oneof(
  ...OPS.map(([weight, arbitrary]) => ({ weight, arbitrary })),
);

/**
 * The document after one edit through the model API, or the same document when
 * the edit names nothing it can apply to.
 */
export function applyOp(doc, edit, counter) {
  const { nodes } = doc;
  const devices = nodes.filter((node) => node.kind === 'device');
  const switches = nodes.filter((node) => node.kind === 'switch');
  const groups = nodes.filter((node) => node.kind === 'group');
  const at = (x, y) => ({ x, y });

  switch (edit.op) {
    case 'network':
      return addNetwork(doc, { id: counter.id(), name: counter.name('n') }).doc;
    case 'device':
      return addNode(doc, {
        id: counter.id(),
        kind: 'device',
        hostname: counter.name('h'),
        position: at(edit.x, edit.y),
        interfaces: Array.from({ length: edit.interfaces }, (_, i) => ({
          id: counter.id(),
          name: `eth${i}`,
        })),
      }).doc;
    case 'switch': {
      const network = pick(doc.networks, edit.network);

      return network
        ? addNode(doc, {
            id: counter.id(),
            kind: 'switch',
            networkId: network.id,
            position: at(edit.x, edit.y),
          }).doc
        : doc;
    }
    case 'note':
    case 'line':
      return addNode(doc, {
        id: counter.id(),
        kind: edit.op,
        position: at(edit.x, edit.y),
      }).doc;
    case 'connect': {
      const device = pick(devices, edit.device);
      const target = pick(switches, edit.switch);

      if (!device || !target) {
        return doc;
      }

      return connect(doc, {
        sourceNodeId: device.id,
        sourceHandleId: pick(device.device.interfaces || [], edit.handle)?.id,
        targetNodeId: target.id,
      }).doc;
    }
    case 'group':
      return groupNodes(
        doc,
        edit.nodes.map((index) => pick(nodes, index)?.id).filter(Boolean),
      ).doc;
    case 'ungroup': {
      const group = pick(groups, edit.group);

      return group ? ungroup(doc, group.id) : doc;
    }
    case 'parent': {
      const node = pick(nodes, edit.node);

      return node
        ? setParent(doc, node.id, pick(groups, edit.group)?.id ?? null)
        : doc;
    }
    case 'remove': {
      const node = pick(nodes, edit.node);

      return node ? removeElements(doc, { nodes: [node.id] }) : doc;
    }
    case 'disconnect': {
      const edge = pick(doc.edges, edit.edge);

      return edge ? removeElements(doc, { edges: [edge.id] }) : doc;
    }
    case 'rename': {
      const device = pick(devices, edit.device);

      return device
        ? updateNode(doc, device.id, { hostname: counter.name('host') })
        : doc;
    }
    case 'addInterface': {
      const device = pick(devices, edit.device);

      return device
        ? addInterface(doc, device.id, { id: counter.id() }).doc
        : doc;
    }
    case 'removeInterface': {
      const device = pick(devices, edit.device);
      const handle =
        device && pick(device.device.interfaces || [], edit.handle);

      return handle ? removeInterface(doc, device.id, handle.id) : doc;
    }
    case 'renameNetwork': {
      const network = pick(doc.networks, edit.network);

      return network
        ? updateNetwork(doc, network.id, { name: counter.name('net') })
        : doc;
    }
    case 'move': {
      const node = pick(nodes, edit.node);

      return node
        ? moveNodes(doc, [{ id: node.id, position: at(edit.x, edit.y) }])
        : doc;
    }
    default:
      throw new Error(`unknown op ${edit.op}`);
  }
}

/** The document after each edit in turn. */
export function applyOps(doc, edits, counter) {
  return edits.reduce((next, edit) => applyOp(next, edit, counter), doc);
}

// Two networks with a switch each, three devices, and two connections.
const SCAFFOLD = [
  { op: 'network' },
  { op: 'network' },
  { op: 'switch', network: 0, x: 300, y: 0 },
  { op: 'switch', network: 1, x: 300, y: 300 },
  { op: 'device', x: 0, y: 0, interfaces: 2 },
  { op: 'device', x: 0, y: 150, interfaces: 2 },
  { op: 'device', x: 0, y: 300, interfaces: 0 },
  { op: 'connect', device: 0, switch: 0, handle: 0 },
  { op: 'connect', device: 1, switch: 1, handle: 0 },
];

/** A small valid diagram built through the model API. */
export function scaffold(counter) {
  return applyOps(
    createDocument({ id: counter.id(), name: 'Scaffold' }),
    SCAFFOLD,
    counter,
  );
}

// Physical keys: [code, key, key with Shift], and what Option types on a Mac.
const KEYS = [
  ['KeyA', 'a', 'A'],
  ['Digit1', '1', '!'],
  ['Numpad5', '5', '5'],
  ['Slash', '/', '?'],
  ['Equal', '=', '+'],
  ['Space', ' ', ' '],
  ['Enter', 'Enter', 'Enter'],
  ['ArrowUp', 'ArrowUp', 'ArrowUp'],
  ['F3', 'F3', 'F3'],
];
const MAC_OPTION = { KeyA: 'å', Slash: '÷' };

/**
 * A key press as a KeyboardEvent reports it, with its platform: the key
 * matches the code, the modifiers and the platform.
 */
export const keyPress = fc
  .record({
    entry: fc.constantFrom(...KEYS),
    ctrlKey: fc.boolean(),
    metaKey: fc.boolean(),
    altKey: fc.boolean(),
    shiftKey: fc.boolean(),
    platform: fc.constantFrom('mac', 'other'),
  })
  .map(({ entry: [code, key, shifted], platform, ...modifiers }) => {
    const option =
      platform === 'mac' && modifiers.altKey ? MAC_OPTION[code] : undefined;

    return {
      platform,
      event: {
        code,
        key: option || (modifiers.shiftKey ? shifted : key),
        ...modifiers,
      },
    };
  });
