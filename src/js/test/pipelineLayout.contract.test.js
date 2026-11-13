// The layout of a SCORCH pipeline graph (components/pipeline/service.js):
// the grid cell of each node, the lines between them and the cycle check.
import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import { DefaultStyleService } from '@/components/pipeline/line.js';
import { Pipeline } from '@/components/pipeline/service.js';

const [X, Y, XSTEP, YSTEP] = [70, 100, 125, 75];

// Nodes as the SCORCH API gives them: `next` is null for a node with no
// children. `adjacency[i]` holds the children of node i, of weight 0.
const nodesOf = (adjacency, weights = []) =>
  adjacency.map((children, i) => ({
    name: `node${i}`,
    status: 'unknown',
    next: children.length
      ? children.map((index, j) => ({ index, weight: weights[i]?.[j] ?? 0 }))
      : null,
  }));

// A copy of the nodes, as the layout writes x and y into them.
const pipelineOf = (nodes) =>
  new Pipeline(structuredClone(nodes), X, Y, XSTEP, YSTEP);

// Each node's grid cell as [column, row].
const layout = (adjacency) => {
  const pipeline = pipelineOf(nodesOf(adjacency));
  pipeline.calculateAllPosition();
  return pipeline.nodes.map(({ x, y }) => [(x - X) / XSTEP, (y - Y) / YSTEP]);
};

const edgesOf = (nodes) =>
  nodes.flatMap((node, i) =>
    (node.next ?? []).map((edge) => ({ from: i, ...edge })),
  );

describe('calculateAllPosition', () => {
  // Main bug: a node with several parents went left of one of them.
  test('a node with several parents goes right of each of them', () => {
    expect(layout([[1, 2, 4, 6], [3], [], [5, 6], [], [], []])).toEqual([
      [0, 0],
      [1, 0],
      [1, 1],
      [2, 0],
      [1, 2],
      [3, 0],
      [3, 1],
    ]);
  });

  // Main bug: a branch drew over a node that it had already placed.
  test('a branch does not take a cell that another branch holds', () => {
    // node 4's path (4, 5) must not take the cell of node 3
    expect(layout([[1, 4], [2, 3, 5], [], [], [5], []])).toEqual([
      [0, 0],
      [1, 0],
      [2, 0],
      [2, 1],
      [1, 2],
      [2, 2],
    ]);
  });

  // Main bug: a graph with two or more start nodes threw and drew nothing.
  test('a graph with two roots puts the second root on a new row', () => {
    expect(layout([[2], [2], []])).toEqual([
      [0, 0],
      [0, 1],
      [1, 0],
    ]);
  });

  // Main bug: a graph with two or more start nodes threw and drew nothing.
  test('a graph whose node 0 is not a root starts at its roots', () => {
    expect(layout([[], [0], [0]])).toEqual([
      [1, 0],
      [0, 0],
      [0, 1],
    ]);
  });

  test('the size holds every node, with the start offset as a margin', () => {
    const pipeline = pipelineOf(nodesOf([[1, 2], [], []]));
    pipeline.calculateAllPosition();
    expect([pipeline.width, pipeline.height]).toEqual([
      X + XSTEP + X,
      Y + YSTEP + Y,
    ]);
  });
});

describe('getLines', () => {
  test('draws the lines of low weight first, so the others are on top', () => {
    const nodes = nodesOf([[1, 2], [3], [3], []], [[2, 0], [1], [0]]);
    const pipeline = pipelineOf(nodes);
    pipeline.calculateAllPosition();
    const draw = new DefaultStyleService(XSTEP, YSTEP);
    const line = (from, to, weight) => ({
      path: draw.drawEdge(pipeline.nodes[from], pipeline.nodes[to]),
      weight,
    });
    expect(pipeline.getLines()).toEqual([
      line(0, 2, 0),
      line(2, 3, 0),
      line(1, 3, 1),
      line(0, 1, 2),
    ]);
  });
});

describe('hasCircle', () => {
  // Main bug: a graph whose node 0 is not a start node showed a false cycle.
  test('is false for a graph whose node 0 is not a root', () => {
    expect(pipelineOf(nodesOf([[], [0], [0]])).hasCircle()).toBe(false);
  });
});

// A DAG of up to 12 nodes with 1 to 3 roots. Each other node has one to three
// parents that come before it, and each edge has a weight of 0 to 2. Then
// `keys` shuffles the node labels, so a root can have any index.
const dagArb = fc
  .record({
    roots: fc.integer({ min: 1, max: 3 }),
    others: fc.array(
      fc.uniqueArray(
        fc.record({ parent: fc.nat(), weight: fc.integer({ min: 0, max: 2 }) }),
        { minLength: 1, maxLength: 3, selector: (edge) => edge.parent },
      ),
      { maxLength: 9 },
    ),
    keys: fc.array(fc.nat(), { minLength: 12, maxLength: 12 }),
  })
  .map(({ roots, others, keys }) => {
    const count = roots + others.length;
    const order = Array.from({ length: count }, (_, i) => i).sort(
      (a, b) => keys[a] - keys[b] || a - b,
    );
    const label = [];
    order.forEach((node, i) => (label[node] = i));

    const adjacency = Array.from({ length: count }, () => []);
    const weights = adjacency.map(() => []);
    others.forEach((edges, j) => {
      const child = roots + j;
      const parents = new Set();
      for (const { parent, weight } of edges) {
        const from = parent % child;
        if (parents.has(from)) {
          continue;
        }
        parents.add(from);
        adjacency[label[from]].push(label[child]);
        weights[label[from]].push(weight);
      }
    });
    return nodesOf(adjacency, weights);
  });

describe('the layout of any DAG', () => {
  test('gives each node a finite position in a cell of its own', () => {
    fc.assert(
      fc.property(dagArb, (nodes) => {
        const pipeline = pipelineOf(nodes);
        pipeline.calculateAllPosition();
        const cells = new Set();
        for (const { x, y } of pipeline.nodes) {
          expect(Number.isFinite(x) && Number.isFinite(y)).toBe(true);
          expect(Number.isInteger((x - X) / XSTEP)).toBe(true);
          expect(Number.isInteger((y - Y) / YSTEP)).toBe(true);
          cells.add(`${x},${y}`);
        }
        expect(cells.size).toBe(nodes.length);
      }),
      { numRuns: 300 },
    );
  });

  test('puts each child in a column right of each of its parents', () => {
    fc.assert(
      fc.property(dagArb, (nodes) => {
        const pipeline = pipelineOf(nodes);
        pipeline.calculateAllPosition();
        for (const { from, index } of edgesOf(pipeline.nodes)) {
          expect(pipeline.nodes[index].x).toBeGreaterThan(
            pipeline.nodes[from].x,
          );
        }
      }),
      { numRuns: 300 },
    );
  });

  test('draws one line for each edge, in the order of weight', () => {
    fc.assert(
      fc.property(dagArb, (nodes) => {
        const pipeline = pipelineOf(nodes);
        pipeline.calculateAllPosition();
        const draw = new DefaultStyleService(XSTEP, YSTEP);
        // Array sort is stable: lines of one weight stay in edge order.
        const expected = edgesOf(nodes)
          .map(({ from, index, weight }) => ({
            path: draw.drawEdge(pipeline.nodes[from], pipeline.nodes[index]),
            weight,
          }))
          .sort((a, b) => a.weight - b.weight);
        expect(pipeline.getLines()).toEqual(expected);
      }),
      { numRuns: 200 },
    );
  });

  test('has no cycle, and has one when an edge goes back to a parent', () => {
    fc.assert(
      fc.property(dagArb, (nodes) => {
        expect(pipelineOf(nodes).hasCircle()).toBe(false);

        const last = nodes.length - 1;
        const back = edgesOf(nodes).find((edge) => edge.index === last);
        const cyclic = nodes.map((node) => ({
          ...node,
          next: [...(node.next ?? [])],
        }));
        // a root with no parent gets an edge to itself
        cyclic[last].next.push({ index: back ? back.from : last, weight: 0 });
        expect(pipelineOf(cyclic).hasCircle()).toBe(true);
      }),
      { numRuns: 200 },
    );
  });
});
