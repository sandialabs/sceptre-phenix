// The work an edit or a click costs on a large diagram. Each edit rebuilds
// the canvas, the outline, the header counts and the diagram checks from
// the document; each must stay linear in the size of the document, never
// nodes times connections. Reads of the document's lists are counted rather
// than timed, so the check is exact and never flaky.

import { describe, expect, test } from 'vitest';

import {
  flowChanges,
  toFlowEdges,
  toFlowNodes,
  withSelection,
} from '@/builder/adapters/vueflow.js';
import { nodeIssueSummaries } from '@/builder/issues.js';
import {
  addNetwork,
  addNode,
  connect,
  createDocument,
  groupNodes,
  moveNode,
} from '@/builder/model.js';
import {
  buildOutline,
  diagramCounts,
  networkOutline,
} from '@/builder/outline.js';
import { keepUnchanged } from '@/builder/stable.js';
import { validateDocument } from '@/builder/validate.js';

import { testId } from './fixtures.js';

const PER_NETWORK = 50;

// `devices` devices, each connected to its network's switch, fifty to a
// network, with the first few in a group and a note.
function largeDocument(devices) {
  let doc = createDocument({ id: testId(), name: 'Large' });
  let sw = null;

  for (let index = 0; index < devices; index += 1) {
    const column = Math.floor(index / PER_NETWORK);

    if (index % PER_NETWORK === 0) {
      const created = addNetwork(doc, { name: `NET-${column + 1}` });
      const added = addNode(created.doc, {
        kind: 'switch',
        networkId: created.network.id,
        position: { x: column * 520 + 280, y: 0 },
      });

      doc = added.doc;
      sw = added.node;
    }

    const device = addNode(doc, {
      kind: 'device',
      hostname: `host-${index + 1}`,
      position: { x: column * 520, y: (index % PER_NETWORK) * 112 },
      interfaces: [{ name: 'eth0' }],
    });

    doc = connect(device.doc, {
      sourceNodeId: device.node.id,
      sourceHandleId: device.node.device.interfaces[0].id,
      targetNodeId: sw.id,
    }).doc;
  }

  const members = doc.nodes
    .filter((node) => node.kind === 'device')
    .slice(0, 5)
    .map((node) => node.id);

  doc = groupNodes(doc, members).doc;

  return addNode(doc, { kind: 'note', text: 'Range notes' }).doc;
}

// The document with its lists counting every item read.
function counting(doc) {
  const reads = { count: 0 };
  const watch = (list) =>
    new Proxy(list, {
      get(target, key, receiver) {
        if (typeof key === 'string' && /^\d+$/.test(key)) {
          reads.count += 1;
        }

        return Reflect.get(target, key, receiver);
      },
    });

  return {
    doc: {
      ...doc,
      nodes: watch(doc.nodes),
      edges: watch(doc.edges),
      networks: watch(doc.networks),
    },
    reads,
  };
}

// What every edit rebuilds from the document.
const WORK = {
  'canvas nodes': (doc) => toFlowNodes(doc),
  'canvas connections': (doc) => toFlowEdges(doc),
  outline: (doc) => buildOutline(doc),
  'network list': (doc) => networkOutline(doc),
  'header counts': (doc) => diagramCounts(doc),
  'diagram checks': (doc) => nodeIssueSummaries(doc, validateDocument(doc)),
};

describe('work on a large diagram', () => {
  const small = largeDocument(600);
  const large = largeDocument(1200);

  test('the diagrams are large', () => {
    expect(small.nodes.length).toBeGreaterThanOrEqual(600);
    expect(small.edges).toHaveLength(600);
    expect(large.nodes.length).toBeGreaterThanOrEqual(1200);
  });

  for (const [name, work] of Object.entries(WORK)) {
    test(`${name} read the document a number of times that grows with its size, not its square`, () => {
      const reads = (doc) => {
        const watched = counting(doc);

        work(watched.doc);

        return watched.reads.count;
      };
      const once = reads(small);
      const twice = reads(large);

      // Twice the diagram, twice the reads; nodes times connections would
      // be four times.
      expect(twice / once).toBeLessThan(2.5);
      expect(once).toBeLessThan((small.nodes.length * small.edges.length) / 20);
    });
  }

  test('a click or a move hands Vue Flow only the nodes it changed', () => {
    const base = toFlowNodes(small);
    const [first, second] = small.nodes.filter(
      (node) => node.kind === 'device' && !node.parentId,
    );
    const before = withSelection(base, [first.id]);
    const clicked = keepUnchanged(withSelection(base, [second.id]), before);
    const moved = keepUnchanged(
      toFlowNodes(moveNode(small, second.id, { x: -400, y: -400 })),
      base,
    );

    expect(flowChanges(before, clicked).changed.map((item) => item.id)).toEqual(
      [first.id, second.id],
    );
    expect(flowChanges(base, moved).changed.map((item) => item.id)).toEqual([
      second.id,
    ]);
  });
});
