// @vitest-environment happy-dom
// Node descriptions on the canvas: the rule of the setting Show node
// descriptions and each node's own toggle, the drawings that follow it, and
// the toggle beside the Description field in the Inspector.

import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import {
  descriptionPath,
  descriptionShownLabel,
} from '@/builder/adapters/forms.js';
import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { toDrawio } from '@/builder/drawio.js';
import {
  addNode,
  createDocument,
  drawnNodeComment,
  findNode,
  nodeComment,
  nodeDescriptionHidden,
  setDescriptionHidden,
} from '@/builder/model.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import {
  nodeOptionsFromTemplate,
  templateFromNode,
} from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { fakeHost, fieldAt, mountInspector } from './inspector-mount.js';

enableAutoUnmount(afterEach);

beforeEach(() => {
  resetSettings(null);
});

afterEach(() => {
  resetSettings(null);
});

// A diagram with a device, a group and a note, each with text to draw.
function described() {
  let doc = createDocument({ name: 'Described' });
  const device = addNode(doc, {
    kind: 'device',
    hostname: 'web-01',
    spec: {
      type: 'VirtualMachine',
      general: { hostname: 'web-01', description: 'Front end web server' },
    },
  });

  doc = device.doc;

  const group = addNode(doc, {
    kind: 'group',
    title: 'Zone',
    description: 'DMZ hosts',
    position: { x: 400, y: 0 },
  });

  doc = group.doc;

  const note = addNode(doc, {
    kind: 'note',
    text: 'Patch panel B',
    position: { x: 0, y: 400 },
  });

  return {
    doc: note.doc,
    device: device.node,
    group: group.node,
    note: note.node,
  };
}

// The comment that the canvas draws for each node, by node id.
function drawnComments(doc, options) {
  return Object.fromEntries(
    toFlowNodes(doc, options).map((flow) => [flow.id, flow.data.comment]),
  );
}

describe('the rule', () => {
  test('a description shows only while the setting is on and the node does not hide it', () => {
    const { doc, device, group, note } = described();
    const hidden = setDescriptionHidden(doc, device.id, true);

    expect(drawnNodeComment(device)).toBe('Front end web server');
    expect(drawnNodeComment(device, false)).toBe('');
    expect(drawnNodeComment(findNode(hidden, device.id))).toBe('');
    expect(drawnNodeComment(findNode(hidden, device.id), false)).toBe('');
    expect(drawnNodeComment(group, false)).toBe('');

    // A note's text is no description: it always draws.
    expect(drawnNodeComment(note, false)).toBe('Patch panel B');

    // The accessible name keeps the description (nodeComment).
    expect(nodeComment(findNode(hidden, device.id))).toBe(
      'Front end web server',
    );
  });

  test('the setting off hides every description and changes no node', () => {
    const { doc, device, group, note } = described();

    expect(drawnComments(doc, { showDescriptions: false })).toEqual({
      [device.id]: '',
      [group.id]: '',
      [note.id]: 'Patch panel B',
    });
    expect(drawnComments(doc)).toEqual({
      [device.id]: 'Front end web server',
      [group.id]: 'DMZ hosts',
      [note.id]: 'Patch panel B',
    });
    expect(nodeDescriptionHidden(findNode(doc, device.id))).toBe(false);
  });
});

describe('setDescriptionHidden', () => {
  test('keeps hideDescription only while the description is hidden', () => {
    const { doc, device, group, note } = described();
    const hidden = setDescriptionHidden(
      setDescriptionHidden(doc, device.id, true),
      group.id,
      true,
    );

    expect(findNode(hidden, device.id).device.hideDescription).toBe(true);
    expect(findNode(hidden, group.id).group.hideDescription).toBe(true);
    expect(validateDocument(hidden)).toEqual(validateDocument(doc));

    const shown = setDescriptionHidden(hidden, device.id, false);

    expect(findNode(shown, device.id).device).not.toHaveProperty(
      'hideDescription',
    );
    expect(findNode(shown, device.id)).toEqual(findNode(doc, device.id));

    // No change, and a kind without a description, give the same document.
    expect(setDescriptionHidden(hidden, device.id, true)).toBe(hidden);
    expect(setDescriptionHidden(doc, note.id, true)).toBe(doc);
    expect(setDescriptionHidden(doc, 'missing', true)).toBe(doc);
  });

  test('a copy, a paste and a template keep it', () => {
    const { doc, device, group } = described();
    const hidden = setDescriptionHidden(
      setDescriptionHidden(doc, device.id, true),
      group.id,
      true,
    );
    const payload = copySelection(hidden, { nodes: [device.id, group.id] });
    const { doc: pasted, nodeIds } = pasteClipboard(hidden, payload);
    const copies = nodeIds.map((id) => findNode(pasted, id));

    expect(copies.map((node) => nodeDescriptionHidden(node))).toEqual([
      true,
      true,
    ]);

    const template = templateFromNode(findNode(hidden, device.id));
    const made = addNode(
      createDocument(),
      nodeOptionsFromTemplate(template, createDocument()),
    );

    expect(template.device.hideDescription).toBe(true);
    expect(made.node.device.hideDescription).toBe(true);
  });
});

describe('draw.io download', () => {
  test('draws a description only where the canvas does', () => {
    const { doc, device } = described();
    const hidden = setDescriptionHidden(doc, device.id, true);

    expect(toDrawio(doc)).toContain('Front end web server');
    expect(toDrawio(doc)).toContain('DMZ hosts');
    expect(toDrawio(hidden)).not.toContain('Front end web server');
    expect(toDrawio(hidden)).toContain('DMZ hosts');

    const off = toDrawio(doc, { showNodeDescriptions: false });

    expect(off).not.toContain('Front end web server');
    expect(off).not.toContain('DMZ hosts');
    expect(off).toContain('Patch panel B');
  });
});

describe('the Inspector’s toggle', () => {
  const toggleOf = (wrapper) =>
    wrapper.get('[data-testid="inspector-description-toggle"]');

  test('is beside the Description field of a device, applies at once, and is one Undo step', async () => {
    const { doc, device } = described();
    const { wrapper, store } = await mountInspector({ doc, element: device });
    const steps = store.history.entries.length;
    const path = descriptionPath('device');
    const toggle = toggleOf(wrapper);

    expect(path).toBe('spec.general.description');
    expect(
      wrapper
        .find(
          `[data-path="${path}"] [data-testid="inspector-description-toggle"]`,
        )
        .exists(),
    ).toBe(true);
    expect(toggle.attributes('aria-label')).toBe(
      'Show description on the canvas',
    );
    expect(toggle.attributes('aria-pressed')).toBe('true');

    await toggle.trigger('click');
    expect(findNode(store.doc, device.id).device.hideDescription).toBe(true);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe(
      descriptionShownLabel('Device web-01', false),
    );
    expect(toggleOf(wrapper).attributes('aria-pressed')).toBe('false');
    expect(wrapper.find('[data-testid="inspector-actions"]').exists()).toBe(
      false,
    );

    // Undo clears the selection, so the device is selected again.
    store.undo();
    expect(findNode(store.doc, device.id).device).not.toHaveProperty(
      'hideDescription',
    );
    store.select({ nodes: [device.id] });
    await flushPromises();
    expect(toggleOf(wrapper).attributes('aria-pressed')).toBe('true');
  });

  test('is beside a group’s Description field, and not on a note', async () => {
    const { doc, group, note } = described();
    const { wrapper, store } = await mountInspector({ doc, element: group });

    expect(
      wrapper
        .find(
          '[data-path="description"] [data-testid="inspector-description-toggle"]',
        )
        .exists(),
    ).toBe(true);

    await toggleOf(wrapper).trigger('click');
    expect(findNode(store.doc, group.id).group.hideDescription).toBe(true);
    expect(store.history.undoLabel()).toBe(
      'Hid the description of Group on the canvas',
    );

    store.select({ nodes: [note.id] });
    await wrapper.vm.$nextTick();
    expect(
      wrapper.find('[data-testid="inspector-description-toggle"]').exists(),
    ).toBe(false);
  });

  test('cannot change in a read-only draft', async () => {
    const { doc, device } = described();
    const { wrapper, store } = await mountInspector({
      doc,
      element: device,
      prepare: (prepared) => {
        prepared.readOnly = true;
      },
    });

    expect(toggleOf(wrapper).attributes()).toHaveProperty('disabled');
    await toggleOf(wrapper).trigger('click');
    expect(nodeDescriptionHidden(findNode(store.doc, device.id))).toBe(false);
  });

  test('the Description field says when the setting hides every description', async () => {
    const { doc, device } = described();
    const { wrapper } = await mountInspector({ doc, element: device });
    const note = () =>
      wrapper.find(
        '[data-path="spec.general.description"] [data-testid="inspector-field-note"]',
      );

    expect(note().exists()).toBe(false);
    setSetting('showNodeDescriptions', false, null);
    await wrapper.vm.$nextTick();
    expect(note().text()).toBe(
      'Your canvas shows no descriptions, because your Show node descriptions setting is off.',
    );
    expect(fieldAt(wrapper, 'spec.general.description').element.value).toBe(
      'Front end web server',
    );
  });

  test('changes the template editor’s device', async () => {
    const { doc, device } = described();
    const host = fakeHost(doc, device.id);
    const { wrapper } = await mountInspector({
      props: { host, variant: 'template', labelledby: 'x' },
    });

    await toggleOf(wrapper).trigger('click');
    expect(findNode(host.doc, device.id).device.hideDescription).toBe(true);
    expect(host.commits).toEqual([
      'Hid the description of Device web-01 on the canvas',
    ]);
  });
});
