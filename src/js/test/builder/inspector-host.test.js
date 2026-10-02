// The Inspector on a host that is not the Builder store, and its 'template'
// variant: the form the template editor shows (see the top of
// BuilderInspector.vue and dialogs/TemplateDialog.vue).

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h, reactive } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import {
  INSPECTOR_CHANGED,
  INSPECTOR_FIELD_WARNINGS,
  INSPECTOR_ICONS,
} from '@/components/builder/inspector/control.js';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { settleIcons } from '@/builder/icons.js';
import { addNode, createDocument, findNode } from '@/builder/model.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';
import { ICON_DATA, ICON_KEY } from './png.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// A host as the template editor makes one: a document, the one node the
// form shows, and a commit that replaces the document. It records what the
// Inspector commits and says.
function fakeHost(doc, nodeId, init = {}) {
  const host = reactive({
    doc,
    inspectorSelection: { type: 'node', id: nodeId },
    schema: builderSchemaV1,
    schemaError: '',
    readOnly: false,
    disks: null,
    issues: [],
    canRedo: false,
    canCreateDrafts: false,
    iconShelf: new Map(),
    commits: [],
    said: [],
    commit(next, label) {
      host.doc = settleIcons(next, host.iconShelf).doc;
      host.commits.push(label);

      return true;
    },
    announce(message) {
      host.said.push(message);
    },
    shelveIcons(icons) {
      Object.entries(icons).forEach(([id, entry]) =>
        host.iconShelf.set(id, entry),
      );
    },
    addInterface() {},
    removeInterface() {},
    remove() {},
    moveNodes() {},
    ...init,
  });

  return host;
}

// The Inspector rendered on the server with `props`, as the other Inspector
// tests render it: its watchers run once. `edit` changes the form's data,
// as JSON Forms sends it once a field commits.
async function openInspector(props, edit = null) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector, props) });
  let instance = null;

  app.use(pinia);
  app.mixin({
    created() {
      if (this.$.type === BuilderInspector) {
        instance = this.$;
      }
    },
  });

  const html = await renderToString(app);

  if (edit) {
    const data = JSON.parse(
      JSON.stringify(
        inspectorTarget(props.host.doc, props.host.inspectorSelection).data,
      ),
    );

    edit(data);
    instance.setupState.onChange({ data, errors: [] });
  }

  return {
    html,
    store: useBuilderStore(pinia),
    exposed: instance.exposed,
    provides: instance.provides,
    // The Inspector's own state: whether Apply and Cancel show.
    setup: instance.setupState,
  };
}

// A document that holds one device, as the template editor's does.
function oneDevice(options = {}) {
  const added = addNode(createDocument({ name: 'Scratch' }), {
    kind: 'device',
    hostname: 'plc',
    ...options,
  });

  return { doc: added.doc, node: added.node };
}

const vlan = (name) => ({
  name: 'eth0',
  type: 'ethernet',
  proto: 'manual',
  vlan: name,
});

describe('the Inspector on a host of its own', () => {
  test('shows the host’s selection and leaves the store alone', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id);
    const { html, store } = await openInspector({ host });

    expect(html).toContain('Device alpha');
    expect(html).toContain('id="inspector-title"');
    expect(html).toContain('Connection points');
    expect(html).toContain('data-testid="inspector-position"');
    // The store still holds the empty diagram it starts with.
    expect(store.doc.nodes).toEqual([]);
    expect(store.canUndo).toBe(false);
  });

  test('applies unapplied edits to the host’s document', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id);
    const { exposed, store } = await openInspector({ host }, (data) => {
      data.spec.general = { ...data.spec.general, description: 'Edited' };
    });

    expect(exposed.changed.value).toBe(true);
    expect(exposed.settle()).toBe('');
    expect(findNode(host.doc, alpha.id).device.spec.general.description).toBe(
      'Edited',
    );
    expect(host.commits).toEqual(['Applied changes to Device alpha']);
    expect(exposed.changed.value).toBe(false);
    expect(store.doc.nodes).toEqual([]);
  });

  test('a host that is read only takes no edit', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id, { readOnly: true });
    const { exposed } = await openInspector({ host }, (data) => {
      data.spec.general = { ...data.spec.general, description: 'Edited' };
    });

    expect(exposed.settle()).toBe('');
    expect(host.commits).toEqual([]);
  });

  test('reads and keeps custom icons through the host', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const { provides } = await openInspector({ host });
    const icons = provides[INSPECTOR_ICONS];

    expect(icons.entry(ICON_KEY)).toBeUndefined();
    expect(icons.diagram()).toEqual([]);

    icons.shelve(ICON_KEY, { name: 'plc', data: ICON_DATA });

    expect(host.iconShelf.get(ICON_KEY)).toEqual({
      name: 'plc',
      data: ICON_DATA,
    });
    expect(icons.entry(ICON_KEY)).toEqual({ name: 'plc', data: ICON_DATA });
  });

  // A device's look is applied without Apply, through the host's commit,
  // which makes the document carry the icon.
  test('a custom icon chosen in the form is committed to the host at once', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);

    host.shelveIcons({ [ICON_KEY]: { name: 'plc', data: ICON_DATA } });

    const { exposed } = await openInspector({ host }, (data) => {
      data.icon = ICON_KEY;
    });

    expect(findNode(host.doc, node.id).device.icon).toBe(ICON_KEY);
    expect(host.doc.icons).toEqual({
      [ICON_KEY]: { name: 'plc', data: ICON_DATA },
    });
    expect(host.commits).toEqual([
      'Changed the custom icon of Device plc to plc',
    ]);
    // It is no unapplied edit.
    expect(exposed.changed.value).toBe(false);
  });
});

describe('the template variant', () => {
  const props = (host) => ({
    host,
    variant: 'template',
    labelledby: 'template-fields-title',
  });

  test('shows the form without what belongs to a canvas', async () => {
    const { doc, node } = oneDevice({ interfaces: [{ name: 'eth0' }] });
    const host = fakeHost(doc, node.id, {
      // A warning the diagram's checks would list.
      issues: [
        {
          path: 'nodes[0].device.spec',
          message: 'something about the device',
          level: 'warning',
          nodeId: node.id,
        },
      ],
    });
    const { html } = await openInspector(props(host));
    const [section] = tags(html, 'section');

    expect(section).toContain('aria-labelledby="template-fields-title"');
    expect(section).toContain('builder-inspector--template');
    expect(section).not.toContain('builder-panel');
    expect(html).not.toContain('id="inspector-title"');
    expect(html).not.toContain('builder-inspector__subject');
    expect(html).not.toContain('data-testid="inspector-checks"');
    expect(html).not.toContain('Connection points');
    expect(html).not.toContain('data-testid="inspector-add-interface"');
    expect(html).not.toContain('data-testid="inspector-position"');
    expect(html).not.toContain('data-testid="inspector-actions"');
    // The form itself is the device's.
    expect(html).toContain('Hostname');
    expect(html).toContain('Outline Color');
    expect(html).toContain('Fill Color');
    expect(html).toContain('Custom icon');
    expect(html).toContain('eth0');
  });

  test('its fields say what they are to a template', async () => {
    const { doc, node } = oneDevice();
    const { html } = await openInspector(props(fakeHost(doc, node.id)));

    expect(html).toContain(
      'Name new devices start from. A number is added when the name is taken.',
    );
    expect(html).toContain('Icon drawn on the canvas and in Add nodes.');
    expect(html).toContain(
      'Set from the Hostname above when the template is saved.',
    );
    expect(html).not.toMatch(/without Apply|Apply also sets/);
  });

  test('the canvas keeps its own words for the same fields', () => {
    const spec = { type: 'VirtualMachine' };
    const canvas = schemaForKind(builderSchemaV1, 'device', { spec });
    const template = schemaForKind(builderSchemaV1, 'device', {
      spec,
      template: true,
    });

    expect(canvas).not.toBe(template);
    expect(canvas.properties.hostname.description).toBe(
      'Unique host name; Apply also sets it as the node hostname.',
    );
    expect(canvas.properties.fillColor.description).toMatch(/without Apply\.$/);
    // Both describe the same fields with the same rules.
    const rules = (schema) =>
      JSON.parse(
        JSON.stringify(schema, (key, value) =>
          key === 'description' ? undefined : value,
        ),
      );

    expect(rules(template)).toEqual(rules(canvas));
    // The same object for the same variant, so it is compiled once.
    expect(
      schemaForKind(builderSchemaV1, 'device', { spec, template: true }),
    ).toBe(template);
  });

  // Its edits wait for the editor's Save: there is nothing to apply them
  // with, as there is on the canvas.
  test('unapplied edits bring up Apply and Cancel on the canvas, not in the template editor', async () => {
    const { doc, node } = oneDevice();
    const edit = (data) => {
      data.hostname = 'historian';
    };
    const canvas = await openInspector({ host: fakeHost(doc, node.id) }, edit);
    const editor = await openInspector(props(fakeHost(doc, node.id)), edit);

    expect(canvas.exposed.changed.value).toBe(true);
    expect(canvas.setup.pending).toBe(true);
    expect(editor.exposed.changed.value).toBe(true);
    expect(editor.setup.pending).toBe(false);
  });

  test('settle applies the working copy to the host’s document', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const { exposed } = await openInspector(props(host), (data) => {
      data.hostname = 'historian';
      data.spec.hardware = { ...data.spec.hardware, memory: 4096 };
    });

    expect(exposed.changed.value).toBe(true);
    expect(exposed.errors.value).toEqual([]);
    expect(exposed.settle()).toBe('');

    const device = findNode(host.doc, node.id).device;

    expect(device.hostname).toBe('historian');
    expect(device.spec.general.hostname).toBe('historian');
    expect(device.spec.hardware.memory).toBe(4096);
    expect(exposed.changed.value).toBe(false);
  });

  test('an edit with an error is not applied, and settle says why', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const { exposed } = await openInspector(props(host), (data) => {
      data.hostname = 'two words';
    });

    expect(exposed.errors.value).toHaveLength(1);
    expect(exposed.settle()).toBe('1 field needs attention');
    expect(findNode(host.doc, node.id).device.hostname).toBe('plc');
    expect(host.commits).toEqual([]);
  });

  // A template is in no diagram: a VLAN names a network of the diagram a
  // device is made in. Other warnings show as on the canvas.
  test('an interface’s VLAN gets no warning for naming no network, other fields keep theirs', async () => {
    const { doc, node } = oneDevice();
    const edit = (data) => {
      data.spec.network = { interfaces: [vlan('PLANT')] };
      data.spec.hardware = {
        ...data.spec.hardware,
        drives: [{ image: 'missing.qc2' }],
      };
    };
    const warned = async (init) => {
      const host = fakeHost(doc, node.id, { disks: ['ubuntu.qc2'] });
      const { provides } = await openInspector({ host, ...init }, edit);

      return Object.keys(provides[INSPECTOR_FIELD_WARNINGS].value).sort();
    };

    expect(await warned({})).toEqual([
      'spec.hardware.drives.0.image',
      'spec.network.interfaces.0.vlan',
    ]);
    expect(
      await warned({
        variant: 'template',
        labelledby: 'template-fields-title',
      }),
    ).toEqual(['spec.hardware.drives.0.image']);
  });

  // The mark on a changed field says the change waits for Apply, which the
  // template editor does not have.
  test('a changed field is marked on the canvas, and not in the template editor', async () => {
    const { doc, node } = oneDevice();
    const edit = (data) => {
      data.hostname = 'historian';
    };
    const marked = async (init) => {
      const host = fakeHost(doc, node.id);
      const { provides } = await openInspector({ host, ...init }, edit);

      return provides[INSPECTOR_CHANGED].value('hostname');
    };

    expect(await marked({})).toBe(true);
    expect(
      await marked({
        variant: 'template',
        labelledby: 'template-fields-title',
      }),
    ).toBe(false);
  });

  test('a read-only host locks every field', async () => {
    const { doc, node } = oneDevice();
    const { html } = await openInspector(
      props(fakeHost(doc, node.id, { readOnly: true })),
    );
    const fields = [...tags(html, 'input'), ...tags(html, 'textarea')].filter(
      (tag) => !/type="(hidden|checkbox)"/.test(tag),
    );

    expect(fields.length).toBeGreaterThan(3);
    expect(fields.every((tag) => /\sreadonly/.test(tag))).toBe(true);
  });
});
