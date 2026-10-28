import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  layoutChanges,
  restoreLayoutChanges,
  withGeometry,
  withLayoutChoice,
} from '@/builder/layout.js';
import {
  addNode,
  connect,
  createDocument,
  groupNodes,
  removeElements,
  setParent,
  sizeOf,
} from '@/builder/model.js';
import {
  LAYOUT_DEFAULTS,
  layout as standardLayout,
} from '@/builder/layouts/standard.js';
import { nextPosition } from '@/components/builder/paletteDnd.js';

import { applyOps, documentOp, makeCounter, scaffold } from './arbitraries.js';
import { sampleDocument } from './fixtures.js';

describe('automatic layout', () => {
  test('is deterministic for the same document', () => {
    const { doc } = sampleDocument();

    expect(standardLayout(doc).positions).toEqual(
      standardLayout(doc).positions,
    );
  });

  test('does not depend on node ordering', () => {
    const { doc } = sampleDocument();
    const shuffled = { ...doc, nodes: [...doc.nodes].reverse() };

    expect(standardLayout(shuffled).positions).toEqual(
      standardLayout(doc).positions,
    );
  });

  test('snaps positions to the layout grid', () => {
    const { doc } = sampleDocument();
    const positions = Object.values(standardLayout(doc).positions);

    positions.forEach((position) => {
      expect(position.x % LAYOUT_DEFAULTS.grid).toBe(0);
      expect(position.y % LAYOUT_DEFAULTS.grid).toBe(0);
    });
  });

  test('applying layout keeps every node and resizes groups around members', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const laid = withGeometry(grouped, standardLayout(grouped));
    const group = laid.nodes.find((node) => node.kind === 'group');

    expect(laid.nodes).toHaveLength(grouped.nodes.length);
    expect(group.size.width).toBeGreaterThan(0);
    expect(group.size.height).toBeGreaterThan(0);
  });

  test('direction is configurable', () => {
    const { doc } = sampleDocument();

    expect(standardLayout(doc, { direction: 'LR' }).positions).not.toEqual(
      standardLayout(doc, { direction: 'TB' }).positions,
    );
  });

  // Two switches and four devices, each device on a switch and d2 on both.
  // The layout reads nodes in id order, so each order below gives other ids.
  const ORDERS = [
    ['s0', 's1', 'd0', 'd1', 'd2', 'd3', 'd4'],
    ['d4', 'd3', 'd2', 'd1', 'd0', 's1', 's0'],
    ['d3', 'd0', 's1', 'd2', 's0', 'd4', 'd1'],
    ['d2', 'd4', 's0', 'd3', 's1', 'd1', 'd0'],
    ['s1', 'd1', 'd3', 's0', 'd4', 'd0', 'd2'],
  ];

  function network(names, order) {
    let doc = createDocument();
    const ids = {};
    const add = (name, options) => {
      const added = addNode(doc, {
        ...options,
        id: `${order.indexOf(name)}-${name}`,
      });
      doc = added.doc;
      ids[name] = added.node;
    };

    add('s0', { kind: 'switch', networkName: 'A' });
    add('s1', { kind: 'switch', networkName: 'B' });
    for (const name of names) {
      add(name, { kind: 'device', hostname: name });
    }
    for (const [device, sw] of [
      ['d0', 's0'],
      ['d1', 's0'],
      ['d2', 's1'],
      ['d3', 's1'],
      ['d2', 's0'],
    ]) {
      doc = connect(doc, {
        sourceNodeId: ids[device].id,
        targetNodeId: ids[sw].id,
      }).doc;
    }

    return { doc, ids };
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

  test('a group holds its members, and only them, whatever the node order', () => {
    for (const order of ORDERS) {
      const label = order.join();
      const { doc, ids } = network(['d0', 'd1', 'd2', 'd3'], order);
      const grouped = groupNodes(doc, [ids.d0.id, ids.d3.id]);
      const laid = withGeometry(grouped.doc, standardLayout(grouped.doc));
      const group = boxOf(laid.nodes.find((n) => n.id === grouped.group.id));

      for (const node of laid.nodes) {
        if (node.id === grouped.group.id) {
          continue;
        }
        if (node.parentId === grouped.group.id) {
          expect(inside(group, boxOf(node)), `order ${label}`).toBe(true);
        } else {
          expect(overlaps(group, boxOf(node)), `order ${label}`).toBe(false);
        }
      }
    }
  });

  // Moving a node into a group or out of one after a layout puts it
  // on no other node, and a group that grows moves what it would cover.
  test('moving a node into or out of a laid-out group overlaps nothing', () => {
    const check = (doc, message) => {
      const nodes = doc.nodes;
      const byId = new Map(nodes.map((n) => [n.id, n]));
      const ancestors = (node) => {
        const found = [];

        for (let at = byId.get(node.parentId); at; at = byId.get(at.parentId)) {
          found.push(at.id);
        }

        return found;
      };

      for (const node of nodes) {
        for (const other of nodes) {
          if (node === other) {
            continue;
          }

          if (node.kind === 'group' && ancestors(other).includes(node.id)) {
            expect(inside(boxOf(node), boxOf(other)), message).toBe(true);
          } else if (
            !ancestors(node).includes(other.id) &&
            !ancestors(other).includes(node.id)
          ) {
            expect(overlaps(boxOf(node), boxOf(other)), message).toBe(false);
          }
        }
      }
    };

    for (const order of ORDERS) {
      const label = order.join();
      const { doc, ids } = network(['d0', 'd1', 'd2', 'd3', 'd4'], order);
      const grouped = groupNodes(doc, [ids.d0.id, ids.d3.id]);
      const laid = withGeometry(grouped.doc, standardLayout(grouped.doc));

      check(laid, `laid out, order ${label}`);

      const joined = setParent(laid, ids.d1.id, grouped.group.id);

      check(joined, `d1 joined, order ${label}`);
      check(setParent(joined, ids.d0.id, null), `d0 left, order ${label}`);
      check(setParent(laid, ids.d3.id, null), `d3 left, order ${label}`);

      // A group so small that it grows for a new member.
      const alone = groupNodes(doc, [ids.d4.id]).doc;
      const small = setParent(
        withGeometry(alone, standardLayout(alone)),
        ids.d2.id,
        null,
      );
      const tiny = small.nodes.find((n) => n.kind === 'group');

      check(setParent(small, ids.s0.id, tiny.id), `s0 joined, order ${label}`);
    }
  });

  test('a nested group is laid out inside its own group', () => {
    for (const order of ORDERS) {
      const { doc, ids } = network(['d0', 'd1', 'd2', 'd3'], order);
      const inner = groupNodes(doc, [ids.d1.id]);
      // Far from where the layout puts it, so a stale box would show.
      const moved = {
        ...inner.doc,
        nodes: inner.doc.nodes.map((n) =>
          n.id === inner.group.id
            ? { ...n, position: { x: 5000, y: 5000 } }
            : n,
        ),
      };
      const outer = groupNodes(moved, [ids.d0.id]);
      const nested = setParent(outer.doc, inner.group.id, outer.group.id);
      const laid = withGeometry(nested, standardLayout(nested));
      const find = (id) => boxOf(laid.nodes.find((n) => n.id === id));

      expect(inside(find(outer.group.id), find(inner.group.id))).toBe(true);
      expect(inside(find(inner.group.id), find(ids.d1.id))).toBe(true);
      expect(inside(find(outer.group.id), find(ids.d0.id))).toBe(true);
      for (const name of ['d2', 'd3', 's0', 's1']) {
        expect(overlaps(find(outer.group.id), find(ids[name].id)), name).toBe(
          false,
        );
      }
    }
  });
});

// Where a palette click or an Add command puts a node.
describe('the next free spot', () => {
  const boxOf = (node) => ({ ...node.position, ...sizeOf(node) });
  const overlaps = (a, b) =>
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height;
  const inside = (area, box) =>
    box.x >= area.x &&
    box.y >= area.y &&
    box.x + box.width <= area.x + area.width &&
    box.y + box.height <= area.y + area.height;
  // The tooltip draft of the browser tests: eleven devices in rows and a
  // switch between them.
  const crowdedDocument = () => {
    const positions = [
      ...Array.from({ length: 8 }, (_, index) => ({
        x: (index % 4) * 224,
        y: Math.floor(index / 4) * 160,
      })),
      { x: 0, y: 480 },
      { x: 224, y: 480 },
      { x: 448, y: 480 },
    ];
    let doc = createDocument();

    for (const position of positions) {
      doc = addNode(doc, { kind: 'device', position }).doc;
    }

    return addNode(doc, { kind: 'switch', position: { x: 384, y: 368 } }).doc;
  };
  const addAt = (doc, kind, options = {}) => {
    const added = addNode(doc, {
      kind,
      position: nextPosition(doc, { kind, ...options }),
    });

    return added;
  };

  test('never lands on a node, after a deletion either', () => {
    let doc = createDocument();
    const ids = [];

    for (let n = 0; n < 5; n += 1) {
      const added = addAt(doc, 'device');
      doc = added.doc;
      ids.push(added.node.id);
    }
    expect(doc.nodes[0].position).toEqual({ x: 80, y: 80 });

    doc = removeElements(doc, { nodes: [ids[1]] });
    const next = addAt(doc, 'device');

    // The second device's spot, free again. Not the fifth's. Without Snap
    // to grid, it is where the walk puts it. With it, on the grid.
    expect(next.node.position).toEqual({ x: 300, y: 80 });
    expect(addAt(doc, 'device', { snap: true }).node.position).toEqual({
      x: 304,
      y: 80,
    });
    for (const node of doc.nodes) {
      expect(overlaps(boxOf(next.node), boxOf(node))).toBe(false);
    }
  });

  test("follows the user's Snap to grid setting, with the diagram's grid size", () => {
    const area = { x: 3001, y: -499, width: 900, height: 600 };
    const at = (grid, snap) =>
      nextPosition(
        { ...createDocument(), grid },
        { kind: 'device', area, snap },
      );
    const on = (point, step) =>
      Math.abs(point.x % step) === 0 && Math.abs(point.y % step) === 0;

    // Off by default: whole pixels.
    const loose = at({ enabled: true, size: 16 });
    expect(Number.isInteger(loose.x) && Number.isInteger(loose.y)).toBe(true);
    expect(on(loose, 16)).toBe(false);

    // On: the diagram's grid size.
    expect(on(at({ enabled: true, size: 16 }, true), 16)).toBe(true);
    expect(on(at({ enabled: true, size: 50 }, true), 50)).toBe(true);
  });

  test('keeps out of a group, and lands in the part of the canvas in view', () => {
    let doc = createDocument();
    const area = { x: 3000, y: -500, width: 900, height: 600 };

    doc = addAt(doc, 'group', { area, snap: true }).doc;
    for (const kind of ['device', 'switch', 'note', 'device', 'device']) {
      const added = addAt(doc, kind, { area, snap: true });
      const box = boxOf(added.node);

      for (const node of doc.nodes) {
        expect(overlaps(box, boxOf(node)), kind).toBe(false);
      }
      expect(box.x).toBeGreaterThanOrEqual(area.x);
      expect(box.x + box.width).toBeLessThanOrEqual(area.x + area.width);
      expect(box.y).toBeGreaterThanOrEqual(area.y);
      // With Snap to grid on, on the diagram's grid, as Vue Flow draws it.
      expect(Math.abs(box.x % 16)).toBe(0);
      expect(Math.abs(box.y % 16)).toBe(0);
      doc = added.doc;
    }
  });
  test('puts each node of an empty view where all of it is in view, a group too', () => {
    const areas = [
      // The Builder's canvas in the browser tests.
      { x: 0, y: 0, width: 950, height: 674 },
      { x: 0, y: 0, width: 900, height: 600 },
      { x: 3001, y: -499, width: 1210, height: 707 },
    ];

    for (const area of areas) {
      for (const snap of [false, true]) {
        const label = `${area.width}x${area.height}, snap ${snap}`;
        let doc = createDocument();

        // Three nodes in the first row leave room in it only for a device:
        // the group goes in the next row, not half out of view.
        for (const kind of ['device', 'switch', 'note', 'group']) {
          const added = addAt(doc, kind, { area, snap });
          const box = boxOf(added.node);

          expect(inside(area, box), `${kind}, ${label}`).toBe(true);
          for (const node of doc.nodes) {
            expect(overlaps(box, boxOf(node)), `${kind}, ${label}`).toBe(false);
          }
          doc = added.doc;
        }
      }
    }
  });

  test('puts a note and a group in view in a crowded view, where they fit', () => {
    // A view as full as the tooltip draft of the browser tests: a switch
    // and devices in rows, with room only at the right side.
    const area = { x: 0, y: 0, width: 1150, height: 674 };

    for (const snap of [false, true]) {
      let doc = crowdedDocument();

      for (const kind of ['note', 'group']) {
        const added = addAt(doc, kind, { area, snap });
        const box = boxOf(added.node);

        expect(inside(area, box), `${kind}, snap ${snap}`).toBe(true);
        for (const node of doc.nodes) {
          expect(overlaps(box, boxOf(node)), `${kind}, snap ${snap}`).toBe(
            false,
          );
        }
        doc = added.doc;
      }
    }
  });

  test('puts a group beside the nodes in view, not below the view, when no spot in view is free', () => {
    // The tooltip draft's canvas in the browser tests: a note fits in view and
    // a group does not, so a group in a row below would move the view.
    const area = { x: 0, y: 0, width: 950, height: 674 };
    // The view after revealNodes (BuilderCanvas.vue): unchanged if the node is
    // all in view, else one of the same size centred on the node.
    const revealed = (view, box) =>
      inside(view, box)
        ? view
        : {
            ...view,
            x: box.x + box.width / 2 - view.width / 2,
            y: box.y + box.height / 2 - view.height / 2,
          };

    for (const snap of [false, true]) {
      let doc = crowdedDocument();
      const note = addAt(doc, 'note', { area, snap });
      const noteBox = boxOf(note.node);

      expect(inside(area, noteBox), `snap ${snap}`).toBe(true);
      doc = note.doc;

      const box = boxOf(addAt(doc, 'group', { area, snap }).node);

      // The group starts in view: its top left corner is in the view, so
      // most of it is not below the view or at its right.
      expect(
        inside(area, { ...box, width: 0, height: 0 }),
        `snap ${snap}`,
      ).toBe(true);
      for (const node of doc.nodes) {
        expect(overlaps(box, boxOf(node)), `snap ${snap}`).toBe(false);
      }

      // After the view moves to the group, all of the group and all of the
      // note are in view: the browser test points to the note then.
      const view = revealed(area, box);

      expect(inside(view, box), `snap ${snap}`).toBe(true);
      expect(inside(view, noteBox), `snap ${snap}`).toBe(true);
    }
  });

  test('puts a group wider than the view in the first column', () => {
    const area = { x: 0, y: 0, width: 300, height: 600 };

    expect(nextPosition(createDocument(), { kind: 'group', area }).x).toBe(
      nextPosition(createDocument(), { kind: 'device', area }).x,
    );
  });
});

describe('what a layout leaves in the document', () => {
  test('keeps routes it can draw, and the layout choice, and puts both back', () => {
    const { doc, edge } = sampleDocument();
    const route = [
      { x: 0, y: 0 },
      { x: 30, y: 0 },
    ];

    // A route of one point, or with a point off the canvas, is not kept.
    for (const drawn of [[{ x: 0, y: 0 }], [route[0], { x: NaN, y: 1 }]]) {
      const laid = withGeometry(doc, {
        positions: {},
        routes: { [edge.id]: drawn },
      });

      expect(laid.edges[0].route).toBeUndefined();
    }

    const laid = withLayoutChoice(
      withGeometry(doc, { positions: {}, routes: { [edge.id]: route } }),
      'dagre',
    );

    expect(laid.edges[0].route).toEqual(route);
    expect(laid.layout).toBe('dagre');
    expect('layout' in withLayoutChoice(laid, '')).toBe(false);

    const changes = layoutChanges(doc, laid);

    expect(changes).toEqual({
      geometry: {},
      routes: { [edge.id]: null },
      layout: { id: null },
    });
    expect(restoreLayoutChanges(laid, changes)).toEqual(doc);
    expect(layoutChanges(doc, doc)).toBeNull();
  });
});

describe('undoing a layout, for any layout', () => {
  const spot = fc.record({
    x: fc.integer({ min: -400, max: 400 }),
    y: fc.integer({ min: -400, max: 400 }),
  });
  const box = fc.record({
    width: fc.integer({ min: 40, max: 400 }),
    height: fc.integer({ min: 40, max: 400 }),
  });
  const route = fc.array(spot, { minLength: 2, maxLength: 4 });
  const choice = fc.constantFrom(undefined, 'elk', 'dagre', 'tiers');
  // What a layout gives each element: by fc.nat() modulo the count, or nothing.
  const laid = fc.array(
    fc.record({ at: fc.nat(), spot, box: fc.option(box), route }),
    { maxLength: 8 },
  );

  // The document with the layout's geometry for the elements it names.
  function laidOut(doc, picks) {
    const positions = {};
    const sizes = {};
    const routes = {};

    for (const { at, spot: position, box: size, route: drawn } of picks) {
      const node = doc.nodes[at % doc.nodes.length];
      const edge = doc.edges.length ? doc.edges[at % doc.edges.length] : null;

      positions[node.id] = position;
      if (size) {
        sizes[node.id] = size;
      }
      if (edge) {
        routes[edge.id] = drawn;
      }
    }

    return withGeometry(doc, { positions, sizes, routes });
  }

  test('puts back every position, size, route and the layout choice', () => {
    fc.assert(
      fc.property(
        fc.array(documentOp, { maxLength: 6 }),
        laid,
        choice,
        laid,
        choice,
        (edits, earlier, first, picks, second) => {
          const counter = makeCounter();
          const built = applyOps(scaffold(counter), edits, counter);
          const before = withLayoutChoice(laidOut(built, earlier), first);
          const after = withLayoutChoice(laidOut(before, picks), second);
          const changes = layoutChanges(before, after);

          if (!changes) {
            expect(after).toEqual(before);

            return;
          }
          expect(restoreLayoutChanges(after, changes)).toEqual(before);
        },
      ),
      { numRuns: 100 },
    );
  });
});
