// Node icon sizes: the diagram's and each node's own, how the document keeps
// them, their Inspector fields as rendered (choosing in them is in
// icon-size-select.test.js), and how the canvas draws each kind.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import InspectorDiagram from '@/components/builder/inspector/InspectorDiagram.vue';
import DeviceNode from '@/components/builder/nodes/DeviceNode.vue';
import GroupNode from '@/components/builder/nodes/GroupNode.vue';
import SwitchNode from '@/components/builder/nodes/SwitchNode.vue';
import { iconSizeClass } from '@/components/builder/nodes/nodeIconSize.js';

import {
  applyFormData,
  fieldDefault,
  inspectorTarget,
  lookChangeLabel,
} from '@/builder/adapters/forms.js';
import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { decodeDocument, parseDocument } from '@/builder/decode.js';
import {
  addNode,
  DEFAULT_ICON_SIZE,
  documentIconSize,
  findNode,
  ICON_SIZE_PIXELS,
  ICON_SIZES,
  iconPixels,
  LOOK_KEYS,
  lookOf,
  nodeIconSize,
  setIconSize,
  updateNode,
} from '@/builder/model.js';
import {
  builderSchemaV1,
  schemaForKind,
  UNSET_KEYWORD,
} from '@/builder/schema.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';
import {
  nodeOptionsFromTemplate,
  templateFromNode,
} from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument, tags } from './fixtures.js';
import { renderWithStore } from './render.js';
import { ICON_DATA } from './png.js';

// Rendered alone, a node has no canvas handles or tooltip wrapper.
vi.mock('@vue-flow/core', async (importOriginal) => {
  const { ref } = await import('vue');

  return {
    ...(await importOriginal()),
    Handle: { name: 'Handle', render: () => null },
    useNode: () => ({ id: '', nodeEl: ref(null) }),
  };
});

async function render(component, props) {
  return renderToString(createSSRApp({ render: () => h(component, props) }));
}

// The sample document with a group, and its device, switch and group by kind.
function iconDocument() {
  const sample = sampleDocument();
  const group = addNode(sample.doc, { kind: 'group', title: 'Zone' });

  return {
    ...sample,
    doc: group.doc,
    group: group.node,
    node: { device: sample.alpha, switch: sample.sw, group: group.node },
  };
}

// The opening tag of a node's box, and of the icon drawn in it.
function nodeTag(html) {
  return tags(html, 'div').find((tag) => tag.includes('builder-node '));
}

function iconTag(html) {
  return tags(html, 'svg').find((tag) => tag.includes('builder-icon'));
}

describe('the size a node icon is drawn at', () => {
  test('is Small, Medium or Large: 16, 24 or 32 pixels', () => {
    expect(ICON_SIZES).toEqual(['small', 'medium', 'large']);
    expect(ICON_SIZE_PIXELS).toEqual({ small: 16, medium: 24, large: 32 });
    expect(DEFAULT_ICON_SIZE).toBe('small');
    expect(ICON_SIZES.map(iconPixels)).toEqual([16, 24, 32]);
    // Medium is one and a half times Small, and Large twice it.
    expect(iconPixels('medium') / iconPixels('small')).toBe(1.5);
    expect(iconPixels('large') / iconPixels('small')).toBe(2);
    // Anything else is drawn Small.
    for (const value of [undefined, '', 'huge', 'Large', 32]) {
      expect(iconPixels(value), String(value)).toBe(16);
    }
  });

  test('is the node’s own, else the diagram’s, else Small', () => {
    const { doc, alpha, sw, group } = iconDocument();
    const node = (document, id) => findNode(document, id);

    expect(documentIconSize(doc)).toBe('small');
    expect(documentIconSize({ ...doc, iconSize: 'huge' })).toBe('small');

    for (const { id } of [alpha, sw, group]) {
      expect(nodeIconSize(doc, node(doc, id))).toBe('small');
    }

    const large = setIconSize(doc, 'large');

    expect(documentIconSize(large)).toBe('large');

    for (const { id } of [alpha, sw, group]) {
      expect(nodeIconSize(large, node(large, id))).toBe('large');
    }

    const own = updateNode(
      updateNode(
        updateNode(large, alpha.id, { device: { iconSize: 'small' } }),
        sw.id,
        { switch: { iconSize: 'medium' } },
      ),
      group.id,
      { group: { iconSize: 'medium' } },
    );

    expect(nodeIconSize(own, node(own, alpha.id))).toBe('small');
    expect(nodeIconSize(own, node(own, sw.id))).toBe('medium');
    expect(nodeIconSize(own, node(own, group.id))).toBe('medium');

    // A note names none, and a size its kind has no field for is not its.
    const note = addNode(own, { kind: 'note', text: 'n' });

    expect(nodeIconSize(note.doc, note.node)).toBe('large');
    expect(
      nodeIconSize(own, { kind: 'note', note: { iconSize: 'small' } }),
    ).toBe('large');
  });
});

describe('the Node icon size setting', () => {
  afterEach(() => resetSettings(null));

  test('as an override comes before the node’s size and the diagram’s', () => {
    const { doc, alpha, sw, group } = iconDocument();
    const own = updateNode(setIconSize(doc, 'medium'), alpha.id, {
      device: { iconSize: 'small' },
    });

    for (const size of ICON_SIZES) {
      for (const { id } of [alpha, sw, group]) {
        expect(nodeIconSize(own, findNode(own, id), size)).toBe(size);
      }
    }

    // No override, or one that is no size, leaves the document's sizes.
    for (const override of [undefined, '', 'off', 'huge']) {
      expect(nodeIconSize(own, findNode(own, alpha.id), override)).toBe(
        'small',
      );
      expect(nodeIconSize(own, findNode(own, sw.id), override)).toBe('medium');
    }
  });

  test('sizes every icon on the canvas, and the diagram keeps its sizes', () => {
    const { doc, alpha, sw, group } = iconDocument();
    const sized = updateNode(setIconSize(doc, 'medium'), alpha.id, {
      device: { iconSize: 'small' },
    });
    const note = addNode(sized, { kind: 'note', text: 'n' });
    const before = JSON.stringify(note.doc);
    const data = (options) => (id) =>
      toFlowNodes(note.doc, options).find((node) => node.id === id).data;
    const large = data({ iconSize: 'large' });
    const off = data({ iconSize: undefined });

    for (const { id } of [alpha, sw, group]) {
      expect(large(id).iconSize).toBe('large');
    }
    expect(large(note.node.id).iconSize).toBeUndefined();
    expect(off(alpha.id).iconSize).toBe('small');
    expect(off(sw.id).iconSize).toBe('medium');
    expect(JSON.stringify(note.doc)).toBe(before);
  });

  test('is never written to the diagram, nor to its history', () => {
    const { doc } = iconDocument();
    const store = useBuilderStore(createPinia());

    store.commit(setIconSize(doc, 'medium'), '');
    const before = JSON.stringify(store.doc);
    const steps = store.history.entries.length;

    for (const size of [...ICON_SIZES, 'off']) {
      expect(setSetting('nodeIconSize', size, null)).toBe(true);
      expect(JSON.stringify(store.doc)).toBe(before);
      expect(store.history.entries).toHaveLength(steps);
      expect(documentIconSize(store.doc)).toBe('medium');
    }
  });

  test('draws each kind at its size', async () => {
    const sample = iconDocument();

    setSetting('nodeIconSize', 'large', null);

    for (const [component, { id }] of [
      [DeviceNode, sample.alpha],
      [SwitchNode, sample.sw],
      [GroupNode, sample.group],
    ]) {
      const flow = toFlowNodes(sample.doc, { iconSize: 'large' }).find(
        (node) => node.id === id,
      );
      const html = await render(component, { id, data: flow.data });

      expect(iconTag(html)).toContain('width="32"');
      expect(nodeTag(html)).toContain('builder-node--icon-large');
    }
  });
});

describe('the diagram’s icon size', () => {
  test('is kept at the root, and Small leaves none', () => {
    const { doc } = sampleDocument();
    const medium = setIconSize(doc, 'medium');

    expect(medium.iconSize).toBe('medium');
    expect(doc.iconSize).toBeUndefined();
    // The size it has already is the same document.
    expect(setIconSize(medium, 'medium')).toBe(medium);
    expect(setIconSize(doc, 'small')).toBe(doc);

    const back = setIconSize(medium, 'small');

    expect('iconSize' in back).toBe(false);
    expect(JSON.stringify(back)).toBe(JSON.stringify(doc));
    // A size that is none of the three is Small.
    expect('iconSize' in setIconSize(medium, 'huge')).toBe(false);
  });

  test('decodes from the root and from each kind, and null is none', () => {
    const { doc, alpha, sw } = sampleDocument();
    const sized = {
      ...updateNode(
        updateNode(doc, alpha.id, { device: { iconSize: 'large' } }),
        sw.id,
        { switch: { iconSize: 'medium' } },
      ),
      iconSize: 'medium',
    };
    const decoded = parseDocument(JSON.parse(JSON.stringify(sized)));

    expect(decoded.iconSize).toBe('medium');
    expect(findNode(decoded, alpha.id).device.iconSize).toBe('large');
    expect(findNode(decoded, sw.id).switch.iconSize).toBe('medium');
    expect('iconSize' in decodeDocument({ ...doc, iconSize: null })).toBe(
      false,
    );
  });

  test('of a value the server refuses is refused here too', () => {
    const { doc, alpha } = sampleDocument();
    const issues = (document) =>
      validateDocument(document)
        .filter((entry) => entry.level === 'error')
        .map((entry) => [entry.path, entry.message]);

    expect(issues({ ...doc, iconSize: 'huge' })).toEqual([
      [
        'iconSize',
        'unknown icon size "huge" (expected one of small, medium, large)',
      ],
    ]);

    const index = doc.nodes.findIndex((node) => node.id === alpha.id);

    expect(
      issues(updateNode(doc, alpha.id, { device: { iconSize: '24px' } })),
    ).toEqual([
      [
        `nodes[${index}].device.iconSize`,
        'unknown icon size "24px" (expected one of small, medium, large)',
      ],
    ]);
  });
});

describe('a node’s own icon size', () => {
  test('is one of a device’s look fields, which a new device takes', () => {
    expect(LOOK_KEYS).toContain('iconSize');
    expect(lookOf({}).iconSize).toBe('');
    expect(lookOf({ iconSize: 'large' }).iconSize).toBe('large');

    const { doc } = sampleDocument();
    const added = addNode(doc, {
      kind: 'device',
      hostname: 'plc',
      look: { iconSize: 'medium' },
    });

    expect(added.node.device.iconSize).toBe('medium');
    // Without one, the device has no key for it.
    expect('iconSize' in addNode(doc, { kind: 'device' }).node.device).toBe(
      false,
    );
  });

  test('emptied is the diagram’s again, and leaves no key', () => {
    const { doc, alpha, sw } = sampleDocument();
    const { doc: withGroup, node: group } = addNode(doc, {
      kind: 'group',
      iconSize: 'large',
    });

    expect(group.group.iconSize).toBe('large');

    const sized = updateNode(
      updateNode(doc, alpha.id, { device: { iconSize: 'large' } }),
      sw.id,
      { switch: { iconSize: 'large' } },
    );
    const emptied = updateNode(
      updateNode(sized, alpha.id, { device: { iconSize: '' } }),
      sw.id,
      { switch: { iconSize: '' } },
    );

    expect('iconSize' in findNode(emptied, alpha.id).device).toBe(false);
    expect('iconSize' in findNode(emptied, sw.id).switch).toBe(false);
    expect(
      'iconSize' in
        findNode(
          updateNode(withGroup, group.id, { group: { iconSize: '' } }),
          group.id,
        ).group,
    ).toBe(false);
  });

  test('is copied and pasted with its device, switch or group', () => {
    const { doc, alpha, sw, group } = iconDocument();
    const sized = updateNode(
      updateNode(
        updateNode(doc, alpha.id, { device: { iconSize: 'large' } }),
        sw.id,
        { switch: { iconSize: 'medium' } },
      ),
      group.id,
      { group: { iconSize: 'small' } },
    );
    const pasted = pasteClipboard(
      sized,
      copySelection(sized, { nodes: [alpha.id, sw.id, group.id] }),
    );
    const copies = pasted.nodeIds.map((id) => findNode(pasted.doc, id));
    const sizeOf = (kind) =>
      copies.find((node) => node.kind === kind)[kind].iconSize;

    expect(sizeOf('device')).toBe('large');
    expect(sizeOf('switch')).toBe('medium');
    expect(sizeOf('group')).toBe('small');
  });

  test('travels with a device into a template, and back', () => {
    const { doc, alpha } = sampleDocument();
    const sized = updateNode(doc, alpha.id, { device: { iconSize: 'large' } });
    const template = templateFromNode(findNode(sized, alpha.id), {
      name: 'Alpha',
    });

    expect(template.device.iconSize).toBe('large');
    expect(nodeOptionsFromTemplate(template, doc).look.iconSize).toBe('large');
  });
});

describe('the Inspector’s Icon size', () => {
  const choices = (field) => field.oneOf.map((branch) => branch.const);

  test.each(['device', 'switch', 'group'])(
    'of a %s offers Diagram default and the three sizes',
    (kind) => {
      const schema = schemaForKind(builderSchemaV1, kind, {});
      const field = schema.properties.iconSize;

      expect(field.title).toBe('Icon size');
      expect(choices(field)).toEqual(['small', 'medium', 'large']);
      expect(field.oneOf.map((branch) => branch.title)).toEqual([
        'Small',
        'Medium',
        'Large',
      ]);
      expect(field[UNSET_KEYWORD]).toBe('Diagram default');
      expect(field.description).toContain('Small (16 pixels)');
      expect(field.description).toContain('Large (32)');
    },
  );

  test('a note, a shape and an icon node have none', () => {
    for (const kind of ['note', 'shape', 'icon', 'line', 'edge', 'document']) {
      expect(
        schemaForKind(builderSchemaV1, kind, {}).properties.iconSize,
        kind,
      ).toBeUndefined();
    }
  });

  test('says the diagram’s size while it is Diagram default', () => {
    const { doc, alpha, sw, group } = iconDocument();
    const large = setIconSize(doc, 'large');

    for (const { id } of [alpha, sw, group]) {
      const target = inspectorTarget(large, { type: 'node', id });

      expect(target.data.iconSize).toBe('');
      expect(fieldDefault(target, 'iconSize')).toEqual({
        value: 'large',
        note: "The diagram's icon size",
      });
    }

    expect(
      fieldDefault(
        inspectorTarget(doc, { type: 'node', id: sw.id }),
        'iconSize',
      ).value,
    ).toBe('small');
    expect(
      fieldDefault(inspectorTarget(doc, { type: 'document' }), 'iconSize'),
    ).toBeUndefined();
  });

  test('of a switch and a group is applied with their other fields', () => {
    const { doc, sw, group } = iconDocument();
    const apply = (document, id, change) => {
      const selection = { type: 'node', id };
      const { data } = inspectorTarget(document, selection);

      return applyFormData(document, selection, { ...data, ...change });
    };

    const sized = apply(apply(doc, sw.id, { iconSize: 'medium' }), group.id, {
      iconSize: 'large',
    });

    expect(findNode(sized, sw.id).switch.iconSize).toBe('medium');
    expect(findNode(sized, group.id).group.iconSize).toBe('large');
    expect(
      inspectorTarget(sized, { type: 'node', id: sw.id }).data.iconSize,
    ).toBe('medium');

    // Diagram default leaves the form's data without it, and the node
    // without one of its own.
    const reset = apply(
      apply(sized, sw.id, { iconSize: undefined }),
      group.id,
      { iconSize: undefined },
    );

    expect('iconSize' in findNode(reset, sw.id).switch).toBe(false);
    expect('iconSize' in findNode(reset, group.id).group).toBe(false);
  });

  test('of a device is a look change, named in Undo', () => {
    const { doc, alpha } = sampleDocument();
    const selection = { type: 'node', id: alpha.id };
    const { data, title } = inspectorTarget(doc, selection);
    const sized = applyFormData(doc, selection, { ...data, iconSize: 'large' });

    expect(findNode(sized, alpha.id).device.iconSize).toBe('large');

    const none = lookOf({});

    expect(lookChangeLabel(title, none, { ...none, iconSize: 'large' })).toBe(
      'Changed the icon size of Device alpha to Large',
    );
    expect(lookChangeLabel(title, { ...none, iconSize: 'large' }, none)).toBe(
      'Changed the icon size of Device alpha to the diagram default',
    );
  });
});

describe('a node drawn on the canvas', () => {
  test('carries the size its icon is drawn at', () => {
    const { doc, alpha, sw, group } = iconDocument();
    const sized = updateNode(setIconSize(doc, 'medium'), alpha.id, {
      device: { iconSize: 'large' },
    });
    const note = addNode(sized, { kind: 'note', text: 'n' });
    const data = (id) =>
      toFlowNodes(note.doc).find((node) => node.id === id).data;

    expect(data(alpha.id).iconSize).toBe('large');
    expect(data(sw.id).iconSize).toBe('medium');
    expect(data(group.id).iconSize).toBe('medium');
    expect(data(note.node.id).iconSize).toBeUndefined();
  });

  test('takes the layout of its size: none for Small', () => {
    expect(iconSizeClass('small')).toBe('');
    expect(iconSizeClass(undefined)).toBe('');
    expect(iconSizeClass('huge')).toBe('');
    expect(iconSizeClass('medium')).toBe('builder-node--icon-medium');
    expect(iconSizeClass('large')).toBe('builder-node--icon-large');
  });

  const KINDS = {
    device: DeviceNode,
    switch: SwitchNode,
    group: GroupNode,
  };

  // Each kind at each size: the icon's pixels, and the class that puts a
  // larger icon beside the node's lines.
  describe.each(Object.keys(KINDS))('a %s', (kind) => {
    test.each(ICON_SIZES)('draws its icon %s', async (size) => {
      const sample = iconDocument();
      const { id } = sample.node[kind];
      const doc = setIconSize(sample.doc, size);
      const flow = toFlowNodes(doc).find((node) => node.id === id);
      const html = await render(KINDS[kind], { id, data: flow.data });
      const box = nodeTag(html);
      const icon = iconTag(html);
      const pixels = String(ICON_SIZE_PIXELS[size]);

      expect(icon).toContain(`width="${pixels}"`);
      expect(icon).toContain(`height="${pixels}"`);
      expect(box).toContain(`data-icon-size="${size}"`);

      if (size === 'small') {
        expect(box).not.toMatch(/builder-node--icon-/);
      } else {
        expect(box).toContain(`builder-node--icon-${size}`);
      }
    });
  });

  // Show node icons off: no icon and no icon layout. The document keeps both.
  describe('with node icons hidden', () => {
    afterEach(() => resetSettings(null));

    test.each(Object.keys(KINDS))('a %s draws no icon', async (kind) => {
      const size = 'large';
      const sample = iconDocument();
      const { id } = sample.node[kind];
      const doc = setIconSize(sample.doc, size);
      const flow = toFlowNodes(doc).find((node) => node.id === id);

      setSetting('showNodeIcons', false, null);
      const html = await render(KINDS[kind], { id, data: flow.data });
      const box = nodeTag(html);

      expect(iconTag(html)).toBeUndefined();
      expect(box).not.toMatch(/builder-node--icon-/);
      expect(box).toContain(`data-icon-size="${size}"`);
      expect(flow.data.iconKey).toBeTruthy();

      setSetting('showNodeIcons', true, null);
      expect(
        iconTag(await render(KINDS[kind], { id, data: flow.data })),
      ).toBeTruthy();
    });
  });

  test('a custom icon is drawn at the node’s size too', async () => {
    const { doc, alpha } = sampleDocument();
    const sized = updateNode(setIconSize(doc, 'large'), alpha.id, {
      device: { iconSize: 'medium' },
    });
    const flow = toFlowNodes(sized).find((node) => node.id === alpha.id);
    const html = await render(DeviceNode, {
      id: alpha.id,
      data: { ...flow.data, iconSrc: `data:image/png;base64,${ICON_DATA}` },
    });
    const [image] = tags(html, 'img');

    expect(image).toContain('width="24"');
    expect(image).toContain('style="width:24px;height:24px;"');
  });
});

// The Inspector open on node `id`, or on the diagram section without one.
async function openInspector(doc, { id = null, readOnly = false } = {}) {
  const { store, html, instances } = await renderWithStore(BuilderInspector, {
    capture: { diagram: InspectorDiagram },
    prepare(store) {
      store.commit(doc, '');
      store.readOnly = readOnly;
      store.select({ nodes: id ? [id] : [] });
    },
  });

  return {
    store,
    html,
    diagram: instances.diagram[0]?.setupState,
  };
}

describe('the Inspector’s diagram Icon size', () => {
  // The rendered select, from its opening tag to its end.
  const selectOf = (html) => {
    const start = html.search(
      /<select[^>]*data-testid="inspector-icon-size-select"/,
    );

    return start === -1
      ? ''
      : html.slice(start, html.indexOf('</select>', start) + 9);
  };
  // The values and the names of its choices, in order.
  const valuesOf = (select) =>
    [...select.matchAll(/<option[^>]*value="([^"]*)"/g)].map((m) => m[1]);
  const namesOf = (select) =>
    (select.match(/<option[^>]*>[^<]*<\/option>/g) || []).map((option) =>
      option.replace(/<[^>]+>/g, '').trim(),
    );

  test('is a select named by its heading and described by its help, with the three sizes', async () => {
    const { doc } = sampleDocument();
    const { html, diagram } = await openInspector(setIconSize(doc, 'medium'));
    const select = selectOf(html);
    const [opening] = tags(select, 'select');

    expect(opening).toContain('aria-labelledby="inspector-icon-size-label"');
    expect(opening).toContain('aria-describedby="inspector-icon-size-help"');
    expect(html).toMatch(
      /<h3 id="inspector-icon-size-label"[^>]*>\s*Icon size\s*<\/h3>/,
    );
    expect(html).toMatch(
      /<p id="inspector-icon-size-help"[^>]*>\s*Devices, switches and groups draw their icons at this size, unless one has\s+a size of its own\.\s*<\/p>/,
    );
    expect(valuesOf(select)).toEqual(['small', 'medium', 'large']);
    expect(namesOf(select)).toEqual([
      'Small (16 pixels)',
      'Medium (24 pixels)',
      'Large (32 pixels)',
    ]);
    // It shows the diagram's size.
    expect(diagram.iconSize).toBe('medium');
    expect(html).not.toContain('data-testid="inspector-icon-size-value"');
  });

  test('says when the Node icon size setting overrides it', async () => {
    const { doc } = sampleDocument();

    try {
      setSetting('nodeIconSize', 'large', null);
      const { html } = await openInspector(setIconSize(doc, 'medium'));
      const [opening] = tags(selectOf(html), 'select');

      expect(opening).toContain(
        'aria-describedby="inspector-icon-size-help inspector-icon-size-override"',
      );
      expect(html).toMatch(
        /<p id="inspector-icon-size-override"[^>]*>\s*Your Node icon size setting \(Large\) overrides this on your canvas\.\s*<\/p>/,
      );

      const readOnly = await openInspector(doc, { readOnly: true });

      expect(readOnly.html).toContain('id="inspector-icon-size-override"');
    } finally {
      resetSettings(null);
    }

    const { html } = await openInspector(doc);

    expect(html).not.toContain('inspector-icon-size-override');
  });

  test('of a read-only draft is text, the size’s name', async () => {
    const { doc } = sampleDocument();
    const { html } = await openInspector(setIconSize(doc, 'large'), {
      readOnly: true,
    });

    expect(selectOf(html)).toBe('');
    expect(html).toMatch(
      /data-testid="inspector-icon-size-value"[^>]*>\s*Large\s*</,
    );
    expect(html).toContain('id="inspector-icon-size-help"');
  });
});

describe('a node’s Icon size in the Inspector', () => {
  afterEach(() => resetSettings(null));

  // The opening tag of the form's Icon size select, and the note under it.
  const fieldSelect = (html) =>
    tags(html, 'select').find((tag) =>
      /id="[^"]*iconSize[^"]*-input"/.test(tag),
    );
  const notesOf = (html) =>
    [...html.matchAll(/data-testid="inspector-field-note"[^>]*>([^<]*)</g)].map(
      (match) => match[1].trim(),
    );

  test.each(['device', 'switch', 'group'])(
    'of a %s says when the Node icon size setting overrides it',
    async (kind) => {
      const sample = iconDocument();
      const { id } = sample.node[kind];
      const doc = updateNode(sample.doc, id, {
        [kind]: { iconSize: 'medium' },
      });

      setSetting('nodeIconSize', 'small', null);
      const { html } = await openInspector(doc, { id });
      const select = fieldSelect(html);
      const noteId = select.match(/id="([^"]*)-input"/)[1] + '-note';

      // One note, on the Icon size only, which describes its select.
      expect(notesOf(html)).toEqual([
        'Your Node icon size setting (Small) overrides this on your canvas.',
      ]);
      expect(html).toContain(`id="${noteId}"`);
      expect(select).toMatch(
        new RegExp(`aria-describedby="[^"]*\\b${noteId}\\b`),
      );
    },
  );

  test('a device says nothing about it when the setting is off', async () => {
    const sample = iconDocument();
    const { id } = sample.node.device;
    const doc = updateNode(sample.doc, id, { device: { iconSize: 'medium' } });
    const { html } = await openInspector(doc, { id });

    expect(notesOf(html)).toEqual([]);
    expect(fieldSelect(html)).not.toContain('-note');
  });

  test('a note has no Icon size, and so no note', async () => {
    const sample = iconDocument();
    const note = addNode(sample.doc, { kind: 'note', text: 'n' });

    setSetting('nodeIconSize', 'large', null);
    const { html } = await openInspector(note.doc, { id: note.node.id });

    expect(notesOf(html)).toEqual([]);
  });
});
