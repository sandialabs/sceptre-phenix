import { describe, expect, test } from 'vitest';

import {
  applyLayout,
  computeLayout,
  LAYOUT_DEFAULTS,
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
import { nextPosition } from '@/components/builder/paletteDnd.js';

import { sampleDocument } from './fixtures.js';

describe('automatic layout', () => {
  test('is deterministic for the same document', () => {
    const { doc } = sampleDocument();

    expect(computeLayout(doc)).toEqual(computeLayout(doc));
  });

  test('does not depend on node ordering', () => {
    const { doc } = sampleDocument();
    const shuffled = { ...doc, nodes: [...doc.nodes].reverse() };

    expect(computeLayout(shuffled)).toEqual(computeLayout(doc));
  });

  test('snaps positions to the layout grid', () => {
    const { doc } = sampleDocument();
    const positions = Object.values(computeLayout(doc));

    positions.forEach((position) => {
      expect(position.x % LAYOUT_DEFAULTS.grid).toBe(0);
      expect(position.y % LAYOUT_DEFAULTS.grid).toBe(0);
    });
  });

  test('applying layout keeps every node and resizes groups around members', () => {
    const { doc, alpha, bravo } = sampleDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]).doc;
    const laid = applyLayout(grouped);
    const group = laid.nodes.find((node) => node.kind === 'group');

    expect(laid.nodes).toHaveLength(grouped.nodes.length);
    expect(group.size.width).toBeGreaterThan(0);
    expect(group.size.height).toBeGreaterThan(0);
  });

  test('direction is configurable', () => {
    const { doc } = sampleDocument();

    expect(computeLayout(doc, { direction: 'LR' })).not.toEqual(
      computeLayout(doc, { direction: 'TB' }),
    );
  });

  // Two switches and four devices, each device on a switch and d2 on both.
  // Ids are drawn at random, as the Builder's are, so the order dagre sees
  // the nodes in changes from run to run.
  function network(names) {
    let doc = createDocument();
    const ids = {};
    const add = (name, options) => {
      const added = addNode(doc, {
        ...options,
        id: `${Math.random()}-${name}`,
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
    for (let run = 0; run < 50; run += 1) {
      const { doc, ids } = network(['d0', 'd1', 'd2', 'd3']);
      const grouped = groupNodes(doc, [ids.d0.id, ids.d3.id]);
      const laid = applyLayout(grouped.doc);
      const group = boxOf(laid.nodes.find((n) => n.id === grouped.group.id));

      for (const node of laid.nodes) {
        if (node.id === grouped.group.id) {
          continue;
        }
        if (node.parentId === grouped.group.id) {
          expect(inside(group, boxOf(node)), `run ${run}`).toBe(true);
        } else {
          expect(overlaps(group, boxOf(node)), `run ${run}`).toBe(false);
        }
      }
    }
  });

  // R56: moving a node into a group or out of one after a layout puts it
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

    for (let run = 0; run < 60; run += 1) {
      const { doc, ids } = network(['d0', 'd1', 'd2', 'd3', 'd4']);
      const grouped = groupNodes(doc, [ids.d0.id, ids.d3.id]);
      const laid = applyLayout(grouped.doc);

      check(laid, `laid out, run ${run}`);

      const joined = setParent(laid, ids.d1.id, grouped.group.id);

      check(joined, `d1 joined, run ${run}`);
      check(setParent(joined, ids.d0.id, null), `d0 left, run ${run}`);
      check(setParent(laid, ids.d3.id, null), `d3 left, run ${run}`);

      // A group so small that it grows for a new member.
      const small = setParent(
        applyLayout(groupNodes(doc, [ids.d4.id]).doc),
        ids.d2.id,
        null,
      );
      const tiny = small.nodes.find((n) => n.kind === 'group');

      check(setParent(small, ids.s0.id, tiny.id), `s0 joined, run ${run}`);
    }
  });

  test('a nested group is laid out inside its own group', () => {
    for (let run = 0; run < 20; run += 1) {
      const { doc, ids } = network(['d0', 'd1', 'd2', 'd3']);
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
      const laid = applyLayout(nested);
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

// Where a palette click or an Add command puts a node (R94).
describe('the next free spot', () => {
  const boxOf = (node) => ({ ...node.position, ...sizeOf(node) });
  const overlaps = (a, b) =>
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height;
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

    // The second device's spot, free again; not the fifth's.
    expect(next.node.position).toEqual({ x: 304, y: 80 });
    for (const node of doc.nodes) {
      expect(overlaps(boxOf(next.node), boxOf(node))).toBe(false);
    }
  });

  test('keeps out of a group, and lands in the part of the canvas in view', () => {
    let doc = createDocument();
    const area = { x: 3000, y: -500, width: 900, height: 600 };

    doc = addAt(doc, 'group', { area }).doc;
    for (const kind of ['device', 'switch', 'note', 'device', 'device']) {
      const added = addAt(doc, kind, { area });
      const box = boxOf(added.node);

      for (const node of doc.nodes) {
        expect(overlaps(box, boxOf(node)), kind).toBe(false);
      }
      expect(box.x).toBeGreaterThanOrEqual(area.x);
      expect(box.x + box.width).toBeLessThanOrEqual(area.x + area.width);
      expect(box.y).toBeGreaterThanOrEqual(area.y);
      // On the diagram's grid, as Vue Flow draws it.
      expect(Math.abs(box.x % 16)).toBe(0);
      expect(Math.abs(box.y % 16)).toBe(0);
      doc = added.doc;
    }
  });
});
