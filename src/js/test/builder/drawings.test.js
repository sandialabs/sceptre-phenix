// Shapes, icons and lines: what the model makes of them, and how the
// Inspector, the clipboard, the layouts, the outline and the GEXF download
// treat them. Their validation is in the corpus shared with the server
// (validate.test.js), their decoding in decode.test.js and their canvas
// nodes in vueflow.test.js.

import { describe, expect, test } from 'vitest';

import {
  applyFormData,
  insertedListItem,
  inspectorName,
  inspectorTarget,
  newListItem,
} from '@/builder/adapters/forms.js';
import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { toGEXF } from '@/builder/gexf.js';
import { embedIcons, iconRefs, settleIcons } from '@/builder/icons.js';
import { runLayout } from '@/builder/layouts/index.js';
import {
  addNode,
  canConnect,
  connectNodes,
  documentSummary,
  findNode,
  groupNodes,
  insertLinePoint,
  keyResizedSize,
  kindLabel,
  linePointKey,
  linePointName,
  MAX_LINE_POINTS,
  minimumSize,
  moveLinePoint,
  moveNodes,
  nearestSegment,
  newNodeSize,
  nodeLabel,
  placedLine,
  removeElements,
  removeLinePoint,
  resizedBox,
  resizeNode,
  setLinePoints,
  setParent,
  sizeOf,
  updateNode,
} from '@/builder/model.js';
import { buildOutline, outlineLabel } from '@/builder/outline.js';
import { selectionItemName } from '@/builder/selection.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';
import { ICON_DATA } from './png.js';

const errorsOf = (doc) =>
  validateDocument(doc).filter((entry) => entry.level === 'error');

// A custom icon of the server's icon library, which an icon node names.
const ICON_NAME = 'plc';
const library = {
  lookup: (name) =>
    name.toLowerCase() === ICON_NAME
      ? { name: ICON_NAME, aliases: [], data: ICON_DATA }
      : null,
};

// The sample document with a rectangle, a circle, an icon and a line.
function drawnDocument() {
  const sample = sampleDocument();
  const rect = addNode(sample.doc, {
    kind: 'shape',
    position: { x: 400, y: 400 },
  });
  const circle = addNode(rect.doc, {
    kind: 'shape',
    shape: 'circle',
    label: 'DMZ',
    fillColor: '#eef4fb',
    borderStyle: 'dotted',
    position: { x: 600, y: 400 },
  });
  const icon = addNode(circle.doc, {
    kind: 'icon',
    label: 'Internet',
    position: { x: 800, y: 400 },
  });
  const line = addNode(icon.doc, {
    kind: 'line',
    position: { x: 100, y: 100 },
    points: [
      { x: 40, y: -20 },
      { x: 0, y: 30 },
      { x: 80, y: 10 },
    ],
    label: 'uplink',
    color: '#c0392b',
    lineStyle: 'dashed',
    startArrow: false,
    endArrow: true,
  });

  return {
    ...sample,
    doc: line.doc,
    rect: rect.node,
    circle: circle.node,
    icon: icon.node,
    line: line.node,
  };
}

describe('shapes', () => {
  test('a new shape is a rectangle or a circle of its own size', () => {
    const { doc, rect, circle } = drawnDocument();

    expect(rect).toMatchObject({
      kind: 'shape',
      label: '',
      size: { width: 160, height: 96 },
      shape: { shape: 'rectangle' },
    });
    expect(Object.keys(rect.shape)).toEqual(['shape']);
    expect(circle).toMatchObject({
      label: 'DMZ',
      size: { width: 120, height: 120 },
      shape: {
        shape: 'circle',
        label: 'DMZ',
        fillColor: '#eef4fb',
        borderStyle: 'dotted',
      },
    });
    expect(nodeLabel(rect)).toBe('Rectangle');
    expect(nodeLabel(circle)).toBe('DMZ');
    expect(kindLabel(circle)).toBe('Circle');
    expect(newNodeSize({ kind: 'shape', shape: 'circle' })).toEqual({
      width: 120,
      height: 120,
    });
    expect(addNode(doc, { kind: 'shape', shape: 'star' }).node.shape).toEqual({
      shape: 'rectangle',
    });
    expect(errorsOf(doc)).toEqual([]);
  });

  test('an update keeps a known figure and drops emptied fields', () => {
    const { doc, circle } = drawnDocument();
    const next = updateNode(doc, circle.id, {
      shape: { shape: 'hexagon', label: 'Core', fillColor: '' },
    });

    expect(findNode(next, circle.id)).toMatchObject({
      label: 'Core',
      shape: { shape: 'circle', label: 'Core', borderStyle: 'dotted' },
    });
    expect(findNode(next, circle.id).shape).not.toHaveProperty('fillColor');
  });
});

describe('icons', () => {
  test('an icon names exactly one icon, the built-in one by default', () => {
    const { doc, icon } = drawnDocument();
    const id = icon.id;

    expect(icon).toMatchObject({
      label: 'Internet',
      size: { width: 64, height: 64 },
      icon: { iconKey: 'external', label: 'Internet' },
    });

    const custom = updateNode(doc, id, { icon: { icon: ICON_NAME } });

    expect(findNode(custom, id).icon).toEqual({
      icon: ICON_NAME,
      label: 'Internet',
    });
    expect(
      findNode(updateNode(custom, id, { icon: { iconKey: 'firewall' } }), id)
        .icon,
    ).toEqual({ iconKey: 'firewall', label: 'Internet' });
    expect(
      findNode(updateNode(custom, id, { icon: { icon: '' } }), id).icon,
    ).toEqual({ iconKey: 'external', label: 'Internet' });

    const unlabelled = findNode(
      updateNode(doc, id, { icon: { label: '' } }),
      id,
    );

    expect(unlabelled.label).toBe('');
    expect(unlabelled.icon).toEqual({ iconKey: 'external' });
    expect(nodeLabel(unlabelled)).toBe('Icon');
  });

  test('a custom icon is a name the library resolves, as a device names one', () => {
    const { doc, icon } = drawnDocument();
    const custom = updateNode(doc, icon.id, { icon: { icon: ICON_NAME } });

    expect([...iconRefs(custom)]).toEqual([ICON_NAME]);
    // A name the document carries no copy of is the library's to resolve.
    expect(errorsOf(custom)).toEqual([]);
    expect(
      errorsOf(updateNode(doc, icon.id, { icon: { icon: 'plc icon' } })),
    ).toEqual([
      expect.objectContaining({
        path: `nodes[${custom.nodes.length - 2}].icon.icon`,
      }),
    ]);

    // Drawn from the library, else from the document's copy; with neither,
    // the node shows the built-in icon of its kind.
    const drawn = (next, library) =>
      toFlowNodes(next, { library }).find((node) => node.id === icon.id).data;

    expect(drawn(custom, library)).toMatchObject({
      iconKey: 'external',
      iconSrc: `data:image/png;base64,${ICON_DATA}`,
    });
    expect(drawn(custom, null)).toMatchObject({
      iconKey: 'external',
      iconSrc: '',
    });
    expect(
      drawn({ ...custom, icons: { [ICON_NAME]: { data: ICON_DATA } } }, null)
        .iconSrc,
    ).toBe(`data:image/png;base64,${ICON_DATA}`);

    // A draft keeps no copy of an icon the library holds as it is, and a
    // download carries one.
    const copied = { ...custom, icons: { [ICON_NAME]: { data: ICON_DATA } } };

    expect(settleIcons(copied, library)).not.toHaveProperty('icons');
    expect(settleIcons(copied, null).icons).toEqual(copied.icons);
    expect(embedIcons(custom, library)).toMatchObject({
      doc: { icons: { [ICON_NAME]: { data: ICON_DATA } } },
      missing: [],
    });
    expect(embedIcons(custom, null).missing).toEqual([ICON_NAME]);

    // The built-in icon names no custom icon.
    expect([...iconRefs(doc)]).toEqual([]);
  });
});

describe('lines', () => {
  test('a line sits at the top left corner of its points, its box its size', () => {
    const { doc, line } = drawnDocument();
    const plain = addNode(doc, { kind: 'line', position: { x: 10, y: 20 } });

    expect(plain.node).toMatchObject({
      position: { x: 10, y: 20 },
      size: { width: 160, height: 1 },
      line: {
        points: [
          { x: 0, y: 0 },
          { x: 160, y: 0 },
        ],
      },
    });
    expect(line.position).toEqual({ x: 100, y: 80 });
    expect(line.size).toEqual({ width: 80, height: 50 });
    expect(line.line).toEqual({
      points: [
        { x: 40, y: 0 },
        { x: 0, y: 50 },
        { x: 80, y: 30 },
      ],
      label: 'uplink',
      color: '#c0392b',
      lineStyle: 'dashed',
      endArrow: true,
    });
    expect(line.label).toBe('uplink');
    expect(newNodeSize({ kind: 'line' })).toEqual({ width: 160, height: 1 });
    expect(
      placedLine({ x: 0, y: 0 }, [
        { x: 5, y: 5 },
        { x: 5, y: 5 },
      ]),
    ).toEqual({
      position: { x: 5, y: 5 },
      points: [
        { x: 0, y: 0 },
        { x: 0, y: 0 },
      ],
      size: { width: 1, height: 1 },
    });
    expect(errorsOf(doc)).toEqual([]);
  });

  test('a point moves, and a bend is added and removed, as one edit each', () => {
    const { doc, line } = drawnDocument();
    const id = line.id;
    let next = moveLinePoint(doc, id, 1, { x: -20, y: 50 });

    expect(findNode(next, id)).toMatchObject({
      position: { x: 80, y: 80 },
      size: { width: 100, height: 50 },
      line: {
        points: [
          { x: 60, y: 0 },
          { x: 0, y: 50 },
          { x: 100, y: 30 },
        ],
      },
    });

    next = insertLinePoint(next, id, 1);

    expect(findNode(next, id).line.points).toEqual([
      { x: 60, y: 0 },
      { x: 30, y: 25 },
      { x: 0, y: 50 },
      { x: 100, y: 30 },
    ]);

    next = insertLinePoint(next, id, 3, { x: 50, y: 70 });

    expect(findNode(next, id).line.points[3]).toEqual({ x: 50, y: 70 });
    expect(findNode(next, id).size).toEqual({ width: 100, height: 70 });

    next = removeLinePoint(next, id, 1);

    expect(findNode(next, id).line.points).toHaveLength(4);
    // The label, colors and arrowheads stay.
    expect(findNode(next, id).line).toMatchObject({
      label: 'uplink',
      color: '#c0392b',
      endArrow: true,
    });
  });

  test('a line keeps from two to the most points, all finite', () => {
    const { doc } = drawnDocument();
    const added = addNode(doc, { kind: 'line' });
    const id = added.node.id;

    expect(removeLinePoint(added.doc, id, 0)).toBe(added.doc);

    const full = setLinePoints(
      added.doc,
      id,
      Array.from({ length: MAX_LINE_POINTS }, (_, index) => ({
        x: index * 10,
        y: (index % 2) * 8,
      })),
    );

    expect(findNode(full, id).line.points).toHaveLength(MAX_LINE_POINTS);
    expect(insertLinePoint(full, id, 1)).toBe(full);
    expect(setLinePoints(full, id, [{ x: 0, y: 0 }])).toBe(full);
    expect(
      setLinePoints(full, id, [
        { x: 0, y: 0 },
        { x: Number.NaN, y: 0 },
      ]),
    ).toBe(full);
    expect(errorsOf(full)).toEqual([]);
  });

  test('a line is resized only by its points', () => {
    const { doc, line } = drawnDocument();
    const next = resizeNode(doc, line.id, { width: 500, height: 500 });

    expect(findNode(next, line.id).size).toEqual({ width: 80, height: 50 });
  });

  // The keys of a point's handle on the canvas (see LineNode.vue).
  test('the keys of a focused point move it, remove it, or leave it', () => {
    const { doc } = drawnDocument();
    const added = addNode(doc, { kind: 'line', position: { x: 160, y: 160 } });
    const line = added.node;
    const grid = { size: 16, snap: true };
    const key = (index, event, on = grid) =>
      linePointKey(line, index, event, on);

    // An arrow key moves a point a grid step, onto the grid; Shift moves
    // it a pixel, wherever it lands.
    expect(key(1, { key: 'ArrowDown' })).toEqual({
      action: 'move',
      point: { x: 160, y: 16 },
    });
    expect(key(0, { key: 'ArrowUp' })).toEqual({
      action: 'move',
      point: { x: 0, y: -16 },
    });
    expect(key(1, { key: 'ArrowLeft', shiftKey: true })).toEqual({
      action: 'move',
      point: { x: 159, y: 0 },
    });
    expect(
      linePointKey(
        { ...line, position: { x: 165, y: 160 } },
        0,
        { key: 'ArrowRight' },
        grid,
      ),
    ).toEqual({ action: 'move', point: { x: 11, y: 0 } });
    // Without snapping, a grid step all the same.
    expect(key(0, { key: 'ArrowRight' }, { size: 16, snap: false })).toEqual({
      action: 'move',
      point: { x: 16, y: 0 },
    });

    // Delete and Backspace remove the point, and Escape leaves the handle.
    expect(key(1, { key: 'Delete' })).toEqual({ action: 'remove' });
    expect(key(0, { key: 'Backspace' })).toEqual({ action: 'remove' });
    expect(key(1, { key: 'Escape' })).toEqual({ action: 'leave' });

    // Other keys, and arrows with Alt, Ctrl or ⌘, are not the handle's.
    for (const event of [
      { key: 'ArrowUp', altKey: true },
      { key: 'ArrowUp', ctrlKey: true },
      { key: 'ArrowUp', metaKey: true },
      { key: 'Enter' },
      { key: 'a' },
    ]) {
      expect(key(1, event), event.key).toBeNull();
    }
    expect(key(5, { key: 'ArrowUp' })).toBeNull();

    // A removed point leaves at least two: the bend of three goes, an end
    // of two stays.
    const bent = insertLinePoint(added.doc, line.id, 1);

    expect(findNode(bent, line.id).line.points).toHaveLength(3);
    expect(
      findNode(removeLinePoint(bent, line.id, 1), line.id).line.points,
    ).toEqual(line.line.points);
    expect(removeLinePoint(added.doc, line.id, 1)).toBe(added.doc);
    expect(removeLinePoint(added.doc, line.id, 0)).toBe(added.doc);
  });

  test('its points are named and its segments found', () => {
    const { line } = drawnDocument();

    expect([0, 1, 2].map((index) => linePointName(line, index))).toEqual([
      'the start of uplink',
      'bend 1 of uplink',
      'the end of uplink',
    ]);

    const points = [
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 100 },
    ];

    expect(nearestSegment(points, { x: 50, y: 5 })).toBe(1);
    expect(nearestSegment(points, { x: 90, y: 60 })).toBe(2);
  });
});

describe('drawings in a diagram', () => {
  test('take no connections', () => {
    const { doc, alpha, rect, icon, line } = drawnDocument();

    for (const drawing of [rect, icon, line]) {
      const connection = {
        sourceNodeId: alpha.id,
        sourceHandleId: alpha.device.interfaces[0].id,
        targetNodeId: drawing.id,
      };

      expect(canConnect(doc, connection)).toEqual({
        valid: false,
        reason: 'Shapes, icons and lines do not take connections.',
      });
      expect(connectNodes(doc, connection).doc).toBe(doc);
    }
  });

  test('go in groups and move with them, and are removed', () => {
    const { doc, alpha, circle, line } = drawnDocument();
    const grouped = groupNodes(doc, [alpha.id]);
    const group = grouped.group;
    let next = setParent(grouped.doc, circle.id, group.id);

    expect(findNode(next, circle.id).parentId).toBe(group.id);
    expect(errorsOf(next)).toEqual([]);

    const before = findNode(next, circle.id).position;
    const at = findNode(next, group.id).position;

    next = moveNodes(next, [
      { id: group.id, position: { x: at.x + 30, y: at.y + 40 } },
    ]);

    expect(findNode(next, circle.id).position).toEqual({
      x: before.x + 30,
      y: before.y + 40,
    });

    next = removeElements(next, { nodes: [circle.id, line.id] });

    expect(findNode(next, circle.id)).toBeUndefined();
    expect(findNode(next, line.id)).toBeUndefined();
  });

  test('are counted, listed in the outline and named', () => {
    const { doc, rect, circle, icon, line } = drawnDocument();

    expect(documentSummary(doc)).toMatchObject({ drawings: 4, devices: 2 });
    expect(
      buildOutline(doc)
        .slice(-4)
        .map((item) => item.accessibleName),
    ).toEqual([
      'Circle DMZ',
      'Rectangle',
      'Icon Internet',
      'Line uplink, arrowhead at its end',
    ]);
    expect(outlineLabel(doc, line)).toBe('Line uplink, arrowhead at its end');
    expect(
      [rect, circle, icon, line].map((node) =>
        selectionItemName(doc, { kind: 'nodes', id: node.id }),
      ),
    ).toEqual(['the rectangle', 'circle DMZ', 'icon Internet', 'line uplink']);
  });

  test('are copied and pasted with their payloads', () => {
    const drawn = drawnDocument();
    // The diagram carries a copy of the icon node's custom icon, which the
    // copy brings along.
    const doc = {
      ...updateNode(drawn.doc, drawn.icon.id, { icon: { icon: ICON_NAME } }),
      icons: { [ICON_NAME]: { data: ICON_DATA } },
    };
    const ids = [drawn.circle.id, drawn.icon.id, drawn.line.id];
    const payload = copySelection(doc, { nodes: ids });

    expect(Object.keys(payload.icons)).toEqual([ICON_NAME]);

    const pasted = pasteClipboard(doc, payload, { offset: { x: 40, y: 40 } });
    const copies = pasted.nodeIds.map((id) => findNode(pasted.doc, id));

    expect(copies).toHaveLength(3);

    copies.forEach((copy, index) => {
      const original = findNode(doc, ids[index]);

      expect(copy.id).not.toBe(original.id);
      expect(copy.kind).toBe(original.kind);
      expect(copy[copy.kind]).toEqual(original[original.kind]);
      expect(copy.size).toEqual(original.size);
      expect(copy.position).toEqual({
        x: original.position.x + 40,
        y: original.position.y + 40,
      });
    });
    expect(errorsOf(pasted.doc)).toEqual([]);
  });

  test('are left where they are by the layouts, moving with their group', async () => {
    const { doc, alpha, bravo } = drawnDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]);
    const group = grouped.group;
    const inside = addNode(grouped.doc, {
      kind: 'shape',
      parentId: group.id,
      position: { x: group.position.x + 10, y: group.position.y + 10 },
    });
    const drawings = inside.doc.nodes.filter((node) =>
      ['shape', 'icon', 'line'].includes(node.kind),
    );

    for (const algorithm of ['standard', 'cards', 'dagre']) {
      const { positions } = await runLayout(algorithm, inside.doc);
      const moved = positions[group.id];

      expect(moved, algorithm).toBeTruthy();

      for (const node of drawings) {
        if (node.id === inside.node.id) {
          expect(positions[node.id], algorithm).toEqual({
            x: moved.x + 10,
            y: moved.y + 10,
          });
        } else {
          expect(positions[node.id], algorithm).toBeUndefined();
        }
      }
    }
  });

  // A layout sizes a group around the members it lays out, which leave the
  // drawings out: the group then grows, right and down, to hold them with
  // the room a layout leaves around members.
  test('stay inside their group after a layout, which grows around them', async () => {
    const { doc, alpha, bravo } = drawnDocument();
    const grouped = groupNodes(doc, [alpha.id, bravo.id]);
    const group = grouped.group;
    const far = addNode(grouped.doc, {
      kind: 'shape',
      parentId: group.id,
      position: { x: group.position.x + 600, y: group.position.y + 400 },
    });
    // A group in that group, holding a drawing of its own.
    const inner = addNode(far.doc, {
      kind: 'group',
      parentId: group.id,
      position: { x: group.position.x + 8, y: group.position.y + 300 },
    });
    const nested = addNode(inner.doc, {
      kind: 'line',
      parentId: inner.node.id,
      position: {
        x: inner.node.position.x + 900,
        y: inner.node.position.y + 20,
      },
    });
    const sized = (positions, sizes, node) => ({
      right: positions[node.id].x + sizes[node.id].width,
      bottom: positions[node.id].y + sizes[node.id].height,
    });
    // Where the shape is in its group, which a layout keeps.
    const before = (id) => findNode(nested.doc, id).position;
    const offset = {
      x: before(far.node.id).x - before(group.id).x,
      y: before(far.node.id).y - before(group.id).y,
    };

    expect(findNode(nested.doc, inner.node.id).parentId).toBe(group.id);

    for (const algorithm of ['standard', 'cards', 'dagre']) {
      const { positions, sizes } = await runLayout(algorithm, nested.doc);
      const outer = sized(positions, sizes, group);
      const shape = positions[far.node.id];
      const line = positions[nested.node.id];

      expect(shape, algorithm).toEqual({
        x: positions[group.id].x + offset.x,
        y: positions[group.id].y + offset.y,
      });
      expect(outer.right, algorithm).toBeGreaterThanOrEqual(shape.x + 160 + 48);
      expect(outer.bottom, algorithm).toBeGreaterThanOrEqual(shape.y + 96 + 48);
      expect(sizes[group.id].width % 16, algorithm).toBe(0);
      expect(sizes[group.id].height % 16, algorithm).toBe(0);

      // The inner group grows around its line, and the outer around it.
      const around = sized(positions, sizes, inner.node);

      expect(around.right, algorithm).toBeGreaterThanOrEqual(line.x + 160 + 48);
      expect(outer.right, algorithm).toBeGreaterThanOrEqual(around.right + 48);
      expect(outer.bottom, algorithm).toBeGreaterThanOrEqual(
        around.bottom + 48,
      );
    }
  });

  test('are no part of the GEXF graph', () => {
    const plain = sampleDocument();
    const modified = '2026-03-04T12:00:00Z';
    let doc = plain.doc;

    for (const options of [
      { kind: 'shape', label: 'DMZ' },
      { kind: 'icon', label: 'Internet' },
      { kind: 'line', label: 'uplink', endArrow: true },
    ]) {
      doc = addNode(doc, options).doc;
    }

    const text = toGEXF(doc, { modified }).text;

    expect(text).toBe(toGEXF(plain.doc, { modified }).text);
    expect(text).not.toContain('uplink');
  });
});

describe('resizing', () => {
  test('a node is at least its least size, a group around its members', () => {
    const { doc, alpha, rect, icon } = drawnDocument();

    expect(minimumSize(doc, rect.id)).toEqual({ width: 16, height: 16 });
    expect(minimumSize(doc, icon.id)).toEqual({ width: 24, height: 24 });
    expect(
      resizedBox(doc, rect.id, { x: 5.4, y: 6.6, width: 3, height: 300 }),
    ).toEqual({
      position: { x: 5, y: 7 },
      size: { width: 16, height: 300 },
    });

    const grouped = groupNodes(doc, [alpha.id]);

    // alpha is at (0, 0), 160 by 96; a grid step to spare around it.
    expect(
      resizedBox(grouped.doc, grouped.group.id, {
        x: 20,
        y: 20,
        width: 50,
        height: 50,
      }),
    ).toEqual({
      position: { x: -16, y: -16 },
      size: { width: 192, height: 128 },
    });
    expect(
      sizeOf(
        resizeNode(doc, rect.id, { width: 200, height: 80 }).nodes.find(
          (node) => node.id === rect.id,
        ),
      ),
    ).toEqual({ width: 200, height: 80 });
  });

  // Alt+Shift and an arrow key on the canvas (see BuilderCanvas.vue).
  test('the arrow keys resize a shape, an icon and a note, never past their least size', () => {
    const { doc: drawn, alpha, rect, icon, line } = drawnDocument();
    const withNote = addNode(drawn, { kind: 'note' });
    const doc = withNote.doc;

    for (const node of [rect, icon, withNote.node]) {
      const { width, height } = sizeOf(node);
      const least = minimumSize(doc, node.id);
      const key = (from, name) => keyResizedSize(from, node.id, name, 10);

      expect(least.width, node.kind).toBeLessThan(width);
      expect(key(doc, 'ArrowRight'), node.kind).toEqual({
        width: width + 10,
        height,
      });
      expect(key(doc, 'ArrowDown'), node.kind).toEqual({
        width,
        height: height + 10,
      });
      expect(key(doc, 'ArrowLeft'), node.kind).toEqual({
        width: Math.max(least.width, width - 10),
        height,
      });

      // A step past the least size stops at it, and at it nothing shrinks.
      const near = resizeNode(doc, node.id, {
        width: least.width + 4,
        height: least.height + 4,
      });

      expect(key(near, 'ArrowUp'), node.kind).toEqual({
        width: least.width + 4,
        height: least.height,
      });

      const smallest = resizeNode(doc, node.id, least);

      expect(key(smallest, 'ArrowLeft'), node.kind).toEqual(least);
      expect(key(smallest, 'ArrowUp'), node.kind).toEqual(least);
    }

    // A line is resized by its points, and a device not at all.
    expect(keyResizedSize(doc, line.id, 'ArrowRight', 10)).toBeNull();
    expect(keyResizedSize(doc, alpha.id, 'ArrowRight', 10)).toBeNull();
    expect(keyResizedSize(doc, rect.id, 'Enter', 10)).toBeNull();
  });
});

describe('the Inspector', () => {
  test('edits a line by its points on the canvas', () => {
    const { doc, alpha, line } = drawnDocument();
    const selection = { type: 'node', id: line.id };
    const target = inspectorTarget(doc, selection);

    expect(target.kind).toBe('line');
    expect(target.data).toEqual({
      label: 'uplink',
      color: '#c0392b',
      lineStyle: 'dashed',
      startArrow: false,
      endArrow: true,
      points: [
        { x: 140, y: 80 },
        { x: 100, y: 130 },
        { x: 180, y: 110 },
      ],
    });
    expect(inspectorName(doc, selection)).toBe('line uplink');
    expect(newListItem(doc, selection, 'points', target.data)).toEqual({
      x: 196,
      y: 110,
    });

    // "Insert point after" puts a bend halfway along the segment after a
    // point, in the working copy; the last point has no segment after it,
    // and no other list takes an item between two others.
    expect(insertedListItem(doc, selection, 'points', 0, target.data)).toEqual({
      x: 120,
      y: 105,
    });
    expect(insertedListItem(doc, selection, 'points', 1)).toEqual({
      x: 140,
      y: 120,
    });
    expect(
      insertedListItem(doc, selection, 'points', 0, {
        ...target.data,
        points: [
          { x: 0, y: 0 },
          { x: 31, y: 10 },
        ],
      }),
    ).toEqual({ x: 15.5, y: 5 });
    expect(insertedListItem(doc, selection, 'points', 2)).toBeUndefined();
    expect(insertedListItem(doc, selection, 'label', 0)).toBeUndefined();
    expect(
      insertedListItem(doc, { type: 'node', id: alpha.id }, 'points', 0),
    ).toBeUndefined();

    const inserted = applyFormData(doc, selection, {
      ...target.data,
      points: [
        target.data.points[0],
        insertedListItem(doc, selection, 'points', 0),
        ...target.data.points.slice(1),
      ],
    });

    expect(findNode(inserted, line.id).line.points).toEqual(
      findNode(insertLinePoint(doc, line.id, 1), line.id).line.points,
    );

    const next = applyFormData(doc, selection, {
      ...target.data,
      color: '',
      startArrow: true,
      points: [...target.data.points.slice(0, 2), { x: 200, y: 60 }],
    });

    expect(findNode(next, line.id)).toMatchObject({
      position: { x: 100, y: 60 },
      size: { width: 100, height: 70 },
      line: {
        points: [
          { x: 40, y: 20 },
          { x: 0, y: 70 },
          { x: 100, y: 0 },
        ],
        startArrow: true,
        endArrow: true,
      },
    });
    expect(findNode(next, line.id).line).not.toHaveProperty('color');
  });

  test('edits a shape and an icon with their size', () => {
    const { doc, circle, icon } = drawnDocument();
    const shape = { type: 'node', id: circle.id };

    expect(inspectorTarget(doc, shape).data).toEqual({
      shape: 'circle',
      label: 'DMZ',
      fillColor: '#eef4fb',
      outlineColor: '',
      borderStyle: 'dotted',
      width: 120,
      height: 120,
    });

    let next = applyFormData(doc, shape, {
      shape: 'rectangle',
      label: 'Core',
      outlineColor: '#2f6fbf',
      borderStyle: 'double',
      width: 200,
      height: 100,
    });

    expect(findNode(next, circle.id)).toMatchObject({
      label: 'Core',
      size: { width: 200, height: 100 },
      shape: {
        shape: 'rectangle',
        label: 'Core',
        outlineColor: '#2f6fbf',
        borderStyle: 'double',
      },
    });
    expect(inspectorName(next, shape)).toBe('rectangle Core');

    next = applyFormData(
      next,
      { type: 'node', id: icon.id },
      { iconKey: 'external', icon: ICON_NAME, label: 'Net', width: 48 },
    );

    expect(findNode(next, icon.id)).toMatchObject({
      label: 'Net',
      size: { width: 48, height: 64 },
      icon: { icon: ICON_NAME, label: 'Net' },
    });
    expect(findNode(next, icon.id).icon).not.toHaveProperty('iconKey');
  });
});
