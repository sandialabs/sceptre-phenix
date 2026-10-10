// Renaming a node from the Outline (F2, commitRename in BuilderOutline.vue):
// renamePatch writes the label in the field each kind is drawn and edited
// with, so the canvas, the Outline and the Inspector agree, and a later
// edit of the node keeps the new name.

import { describe, expect, test } from 'vitest';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import {
  addNode,
  createDocument,
  findNode,
  nodeLabel,
  renamePatch,
  updateNode,
} from '@/builder/model.js';

// A document with one node of these addNode options.
function withNode(options) {
  const { doc, node } = addNode(createDocument(), {
    position: { x: 0, y: 0 },
    ...options,
  });

  return { doc, id: node.id };
}

// The node renamed as the Outline renames it.
function renamed(doc, id, label) {
  return updateNode(doc, id, renamePatch(findNode(doc, id), label));
}

describe('renaming a drawing', () => {
  const drawings = [
    [
      'a circle',
      { kind: 'shape', shape: 'circle' },
      'shape',
      { fillColor: '#ffffff' },
    ],
    ['a rectangle', { kind: 'shape' }, 'shape', { outlineColor: '#000000' }],
    ['an icon', { kind: 'icon' }, 'icon', { iconKey: 'server' }],
    ['a line', { kind: 'line' }, 'line', { color: '#ff0000' }],
  ];

  test.each(drawings)(
    'writes %s’s payload label, which the canvas and the Inspector read',
    (_, options, payload, style) => {
      const { doc, id } = withNode(options);
      const next = renamed(doc, id, 'DMZ');
      const node = findNode(next, id);

      expect(renamePatch(findNode(doc, id), 'DMZ')).toEqual({
        [payload]: { label: 'DMZ' },
      });
      // The canvas draws the payload's label (ShapeNode, IconNode and
      // LineNode), and the Outline and the Inspector show the same.
      expect(node[payload].label).toBe('DMZ');
      expect(nodeLabel(node)).toBe('DMZ');
      expect(inspectorTarget(next, { type: 'node', id }).data.label).toBe(
        'DMZ',
      );

      // An Inspector edit of another field keeps it.
      const styled = findNode(updateNode(next, id, { [payload]: style }), id);

      expect(styled[payload].label).toBe('DMZ');
      expect(nodeLabel(styled)).toBe('DMZ');
    },
  );
});

describe('renaming other nodes', () => {
  test('a device by its hostname, a group by its title, a note by its label', () => {
    const device = withNode({ kind: 'device', hostname: 'alpha' });
    const group = withNode({ kind: 'group', title: 'Lab' });
    const note = withNode({ kind: 'note', text: 'Check the PLC' });

    expect(renamePatch(findNode(device.doc, device.id), 'bravo')).toEqual({
      device: { hostname: 'bravo' },
    });
    expect(renamePatch(findNode(group.doc, group.id), 'DMZ')).toEqual({
      group: { title: 'DMZ' },
    });
    expect(renamePatch(findNode(note.doc, note.id), 'Reminder')).toEqual({
      label: 'Reminder',
    });

    const devices = renamed(device.doc, device.id, 'bravo');
    const groups = renamed(group.doc, group.id, 'DMZ');
    const notes = renamed(note.doc, note.id, 'Reminder');

    expect(findNode(devices, device.id).device.hostname).toBe('bravo');
    expect(nodeLabel(findNode(devices, device.id))).toBe('bravo');
    expect(findNode(groups, group.id).group.title).toBe('DMZ');
    expect(nodeLabel(findNode(groups, group.id))).toBe('DMZ');
    expect(nodeLabel(findNode(notes, note.id))).toBe('Reminder');
  });
});
