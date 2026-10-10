// The size node icons are drawn at: the diagram's (root iconSize) and a
// device's, a switch's or a group's own, how the document keeps them, the
// Inspector's fields for them, and how the canvas draws each kind at each
// size. Go and the editor validating them alike is the shared corpus's
// part (validate.test.js); a browser's layout of the nodes is the
// Playwright specs'.

import { describe, expect, test, vi } from 'vitest';
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
import { useBuilderStore } from '@/builder/store.js';
import {
  nodeOptionsFromTemplate,
  templateFromNode,
} from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument, tags } from './fixtures.js';
import { ICON_DATA } from './png.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// A device's and a switch's connection handles, and the wrapper their
// tooltip listens on, are the canvas's; rendered alone, the nodes have
// neither.
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

// The sample document with a group, and each of its nodes by kind.
function iconDocument() {
  const sample = sampleDocument();
  const group = addNode(sample.doc, { kind: 'group', title: 'Zone' });

  return { ...sample, doc: group.doc, group: group.node };
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
      const id = {
        device: sample.alpha,
        switch: sample.sw,
        group: sample.group,
      }[kind].id;
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

// The Inspector rendered on the server for `doc`, as the other Inspector
// tests render it: its watchers run once, so the form stays as it opened
// whatever the store does next. It is open on the node `id`, or on the
// diagram's own section without one. Returns the store, the HTML, the
// Inspector's own state and its settle, and the state of its diagram
// section, whose Icon size select sits outside the form.
async function openInspector(doc, { id = null, readOnly = false } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  const instances = {};

  app.use(pinia);
  app.mixin({
    created() {
      if (this.$.type === BuilderInspector) {
        instances.inspector = this.$;
      } else if (this.$.type === InspectorDiagram) {
        instances.diagram = this.$;
      }
    },
  });

  const store = useBuilderStore(pinia);

  // The draft as loaded, so an undo goes back to it.
  store.commit(doc, '');
  store.readOnly = readOnly;
  store.select({ nodes: id ? [id] : [] });

  const html = await renderToString(app);

  return {
    store,
    html,
    setup: instances.inspector.setupState,
    settle: instances.inspector.exposed.settle,
    diagram: instances.diagram?.setupState,
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

  test('chosen with the pointer applies at once, and stepped to with keys once the choice is made', async () => {
    const { doc } = sampleDocument();
    const { store, diagram } = await openInspector(doc);
    const choice = diagram.iconSizeChoice;
    const steps = store.history.entries.length;
    const label = () => store.history.undoLabel();

    // A choice from the list with the pointer is one step at once.
    choice.point();
    expect(choice.change('large')).toBe(true);
    expect(store.doc.iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(label()).toBe("Changed the diagram's icon size to Large");

    // Each size the arrow keys step to is held; a modifier alone keeps it
    // held, and Enter applies the last, as one step.
    diagram.onIconSizeKey({ key: 'ArrowUp' });
    expect(choice.change('medium')).toBe(false);
    diagram.onIconSizeKey({ key: 'ArrowUp' });
    expect(choice.change('small')).toBe(false);
    diagram.onIconSizeKey({ key: 'Shift' });
    expect(choice.held).toBe('small');
    expect(store.doc.iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);

    diagram.onIconSizeKey({ key: 'Enter' });
    expect(choice.held).toBeNull();
    expect(store.doc).not.toHaveProperty('iconSize');
    expect(store.history.entries).toHaveLength(steps + 2);
    expect(label()).toBe("Changed the diagram's icon size to Small");

    // Focus leaving the select applies the size held (its blur flushes).
    diagram.onIconSizeKey({ key: 'ArrowDown' });
    expect(choice.change('medium')).toBe(false);
    expect(store.doc).not.toHaveProperty('iconSize');
    expect(choice.flush()).toBe(true);
    expect(store.doc.iconSize).toBe('medium');
    expect(store.history.entries).toHaveLength(steps + 3);
    expect(label()).toBe("Changed the diagram's icon size to Medium");

    // Undo puts the size before back.
    store.undo();
    expect(store.doc).not.toHaveProperty('iconSize');
  });

  // The select's binding does not change when the document's size does
  // not, so the select is given that size again.
  test('a size the store refuses leaves the select showing the diagram’s size', async () => {
    const { doc } = sampleDocument();
    const { store, diagram } = await openInspector(setIconSize(doc, 'medium'));
    const select = { value: 'medium' };
    const steps = store.history.entries.length;

    // The element the template's ref holds in a browser.
    diagram.iconSizeSelect = select;

    // While a conflict is resolved, a choice with the pointer.
    store.resolvingConflict = true;
    select.value = 'large';
    diagram.iconSizeChoice.point();
    expect(diagram.iconSizeChoice.change('large')).toBe(false);
    expect(store.doc.iconSize).toBe('medium');
    expect(select.value).toBe('medium');

    // In a draft that turned read only, a size stepped to with the keys
    // shows while it is held, and goes once focus leaves.
    store.resolvingConflict = false;
    store.readOnly = true;
    diagram.onIconSizeKey({ key: 'ArrowDown' });
    select.value = 'large';
    expect(diagram.iconSizeChoice.change('large')).toBe(false);
    expect(select.value).toBe('large');
    expect(diagram.iconSizeChoice.flush()).toBe(false);
    expect(select.value).toBe('medium');
    expect(store.doc.iconSize).toBe('medium');
    expect(store.error).toBe('This draft is read only.');
    expect(store.history.entries).toHaveLength(steps);

    // A size taken leaves the select as it is: its binding follows.
    store.readOnly = false;
    select.value = 'small';
    diagram.iconSizeChoice.point();
    expect(diagram.iconSizeChoice.change('small')).toBe(true);
    expect(select.value).toBe('small');
    expect(store.doc).not.toHaveProperty('iconSize');
  });
});

describe('a node’s Icon size in the Inspector', () => {
  // A key on the Icon size select of the form, which onFieldKey reads by
  // the data path of the field it is in.
  const select = {
    tagName: 'SELECT',
    closest: () => ({ dataset: { path: 'iconSize' } }),
  };

  // The form's data with an Icon size chosen, as JSON Forms sends it once
  // the select changes: Diagram default (no size) leaves no key.
  function choose(setup, size) {
    const data = JSON.parse(JSON.stringify(setup.draft));

    if (size) {
      data.iconSize = size;
    } else {
      delete data.iconSize;
    }

    setup.onChange({ data, errors: [] });
  }

  test('of a device stepped to with keys is applied once the choice is made, Diagram default too', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, setup } = await openInspector(doc, { id: alpha.id });
    const key = (name) => setup.onFieldKey({ key: name, target: select });
    const device = () => findNode(store.doc, alpha.id).device;
    const steps = store.history.entries.length;

    key('ArrowDown');
    choose(setup, 'medium');
    key('ArrowDown');
    choose(setup, 'large');

    // Held: the device has no size of its own yet, and nothing waits for
    // Apply.
    expect('iconSize' in device()).toBe(false);
    expect(store.history.entries).toHaveLength(steps);
    expect(setup.dirty).toBe(false);

    // Enter applies the last size, as one step.
    key('Enter');
    expect(device().iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe(
      'Changed the icon size of Device alpha to Large',
    );

    // Diagram default, stepped to, is held as well, and Tab applies it: the
    // device draws the diagram's size again.
    key('ArrowUp');
    choose(setup, undefined);
    expect(device().iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);

    key('Tab');
    expect('iconSize' in device()).toBe(false);
    expect(store.history.entries).toHaveLength(steps + 2);
    expect(store.history.undoLabel()).toBe(
      'Changed the icon size of Device alpha to the diagram default',
    );

    // A size chosen with no key before it is applied at once.
    choose(setup, 'small');
    expect(device().iconSize).toBe('small');
    expect(store.history.entries).toHaveLength(steps + 3);
  });

  test.each(['switch', 'group'])(
    'of a %s waits for Apply, keys or not, Diagram default too',
    async (kind) => {
      const sample = iconDocument();
      const id = (kind === 'switch' ? sample.sw : sample.group).id;
      let opened = await openInspector(sample.doc, { id });
      const own = () => findNode(opened.store.doc, id)[kind];
      const key = (name) =>
        opened.setup.onFieldKey({ key: name, target: select });
      const steps = opened.store.history.entries.length;

      key('ArrowDown');
      choose(opened.setup, 'large');
      key('Enter');

      expect('iconSize' in own()).toBe(false);
      expect(opened.store.history.entries).toHaveLength(steps);
      expect(opened.setup.dirty).toBe(true);

      expect(opened.settle()).toBe('');
      expect(own().iconSize).toBe('large');
      expect(opened.store.history.undoLabel()).toMatch(/^Applied changes to /);

      // Diagram default on a node of its own size waits for Apply too, and
      // then leaves the node without one.
      const sized = updateNode(sample.doc, id, {
        [kind]: { iconSize: 'large' },
      });

      opened = await openInspector(sized, { id });
      key('ArrowUp');
      choose(opened.setup, undefined);
      key('Enter');

      expect(own().iconSize).toBe('large');
      expect(opened.setup.dirty).toBe(true);

      expect(opened.settle()).toBe('');
      expect('iconSize' in own()).toBe(false);
    },
  );
});
