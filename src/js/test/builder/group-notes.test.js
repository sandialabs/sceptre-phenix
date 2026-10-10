// Groups and the notes below their members: Group, Auto-group, Move to
// group and resizing size a group around its members' footprints
// (footprintBounds in nodeNotes.js), so a device's or a switch's notes stay
// inside its group while the canvas shows them, as the layouts leave room
// for them (layouts.test.js).

import { describe, expect, test } from 'vitest';

import { applyGroups } from '@/builder/grouping.js';
import {
  addNode,
  createDocument,
  findNode,
  fitGroups,
  groupMinimumSize,
  groupNodes,
  keyResizedSize,
  moveNodes,
  resizedBox,
  setParent,
  sizeOf,
  updateNode,
} from '@/builder/model.js';
import {
  footprintBounds,
  nodeFootprint,
  notesHeight,
} from '@/builder/nodeNotes.js';

// Room groupNodes and fitGroups leave around the members.
const PADDING = 40;
const HIDDEN = { showNotes: false };

// Seven notes: the block shows five and "+2 more".
const NOTES = ['one', 'two', 'three', 'four', 'five', 'six', 'seven'];

// A document with a device that has NOTES at (0, 0), and a plain device far
// to its right.
function notedDocument() {
  const noted = addNode(createDocument({ name: 'notes' }), {
    kind: 'device',
    hostname: 'plc-01',
    position: { x: 0, y: 0 },
  });
  const plain = addNode(noted.doc, {
    kind: 'device',
    hostname: 'hmi-01',
    position: { x: 1000, y: 0 },
  });
  const { spec } = noted.node.device;
  const doc = updateNode(plain.doc, noted.node.id, {
    device: { spec: { ...spec, general: { ...spec.general, notes: NOTES } } },
  });

  return { doc, plc: findNode(doc, noted.node.id), hmi: plain.node };
}

// The bottom edge of a node's box, and of its footprint.
const boxBottom = (node) => node.position.y + sizeOf(node).height;
const notesBottom = (node, options) =>
  node.position.y + nodeFootprint(node, options).height;
const groupBottom = (group) => group.position.y + sizeOf(group).height;

describe('the room notes take', () => {
  test('is in the bounds, with the padding asked for', () => {
    const { plc } = notedDocument();
    const bounds = footprintBounds([plc]);

    expect(notesHeight(plc)).toBeGreaterThan(0);
    expect(bounds.height).toBe(sizeOf(plc).height + notesHeight(plc));
    expect(footprintBounds([plc], { padding: 10 })).toEqual({
      x: bounds.x - 10,
      y: bounds.y - 10,
      width: bounds.width + 20,
      height: bounds.height + 20,
    });
    expect(footprintBounds([plc], HIDDEN).height).toBe(sizeOf(plc).height);
    expect(footprintBounds([], { padding: 10 })).toEqual({
      x: 0,
      y: 0,
      width: 0,
      height: 0,
    });
  });
});

describe('a group around a node with notes', () => {
  test('Group holds the notes while they are shown, and only the box while they are not', () => {
    const { doc, plc } = notedDocument();
    const shown = groupNodes(doc, [plc.id]).group;
    const hidden = groupNodes(doc, [plc.id], HIDDEN).group;

    expect(groupBottom(shown)).toBe(notesBottom(plc) + PADDING);
    expect(groupBottom(hidden)).toBe(boxBottom(plc) + PADDING);
  });

  test('Auto-group holds the notes too', () => {
    const { doc, plc, hmi } = notedDocument();
    const planned = [{ title: 'Plant', color: '', members: [plc.id, hmi.id] }];
    const [shown] = applyGroups(doc, planned).groups;
    const [hidden] = applyGroups(doc, planned, HIDDEN).groups;

    expect(groupBottom(shown)).toBe(notesBottom(plc) + PADDING);
    expect(groupBottom(hidden)).toBe(boxBottom(plc) + PADDING);
  });

  test('a group grows to a moved member’s notes', () => {
    const { doc, plc } = notedDocument();
    const { doc: grouped, group } = groupNodes(doc, [plc.id]);
    const moved = moveNodes(grouped, [
      { id: plc.id, position: { x: 0, y: 400 } },
    ]);
    const member = findNode(moved, plc.id);

    expect(groupBottom(findNode(fitGroups(moved, [group.id]), group.id))).toBe(
      notesBottom(member) + PADDING,
    );
    expect(
      groupBottom(findNode(fitGroups(moved, [group.id], HIDDEN), group.id)),
    ).toBe(boxBottom(member) + PADDING);
  });

  test('resizing it never cuts off a member’s notes', () => {
    const { doc, plc } = notedDocument();
    const { doc: grouped, group } = groupNodes(doc, [plc.id]);
    const spare = grouped.grid?.size || 16;

    // The least size holds the notes, with a grid step to spare.
    expect(group.position.y + groupMinimumSize(grouped, group.id).height).toBe(
      notesBottom(plc) + spare,
    );
    expect(
      group.position.y + groupMinimumSize(grouped, group.id, HIDDEN).height,
    ).toBe(boxBottom(plc) + spare);

    // A mouse resize to a sliver keeps the notes inside.
    const dragged = resizedBox(grouped, group.id, {
      ...group.position,
      width: 10,
      height: 10,
    });

    expect(dragged.position.y + dragged.size.height).toBe(
      notesBottom(plc) + spare,
    );

    // A keyboard resize stops there too.
    let size = sizeOf(group);

    for (let step = 0; step < 100; step += 1) {
      size = keyResizedSize(
        updateNode(grouped, group.id, { size }),
        group.id,
        'ArrowUp',
        10,
      );
    }

    expect(group.position.y + size.height).toBeGreaterThanOrEqual(
      notesBottom(plc),
    );
  });

  test('Move to group puts a node that does not fit below the notes', () => {
    const { doc, plc } = notedDocument();
    const { doc: grouped, group } = groupNodes(doc, [plc.id]);
    // A note is wider than the room the group has inside.
    const added = addNode(grouped, {
      kind: 'note',
      text: 'Wide',
      position: { x: 1000, y: 600 },
    });

    const shown = setParent(added.doc, added.node.id, group.id);
    const placed = findNode(shown, added.node.id);
    const grown = findNode(shown, group.id);

    expect(placed.parentId).toBe(group.id);
    expect(placed.position.y).toBeGreaterThanOrEqual(notesBottom(plc));
    expect(groupBottom(grown)).toBeGreaterThanOrEqual(
      boxBottom(placed) + PADDING,
    );
    expect(groupBottom(grown)).toBeGreaterThanOrEqual(notesBottom(plc));

    // With notes hidden it goes right below the box, over where the notes
    // would be.
    const hidden = setParent(added.doc, added.node.id, group.id, HIDDEN);

    expect(findNode(hidden, added.node.id).position.y).toBeLessThan(
      notesBottom(plc),
    );
  });
});
