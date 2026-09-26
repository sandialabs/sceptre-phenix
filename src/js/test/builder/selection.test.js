import { describe, expect, test } from 'vitest';

import { pressSelection, selectionItemName } from '@/builder/selection.js';

import { sampleDocument } from './fixtures.js';

const none = () => ({ nodes: [], edges: [] });

describe('pressing a node or connection', () => {
  test('names nodes by label and connections by their ends', () => {
    const { doc, alpha, edge } = sampleDocument();

    expect(selectionItemName(doc, { kind: 'nodes', id: alpha.id })).toBe(
      'alpha',
    );
    expect(selectionItemName(doc, { kind: 'edges', id: edge.id })).toMatch(
      /^the connection between alpha and \S+/,
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
        /^Removed the connection between alpha and .+ from the selection, 1 item selected$/,
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
