// The Inspector on a host other than the Builder store, and its 'template'
// variant (see BuilderInspector.vue and dialogs/TemplateDialog.vue), as
// rendered. Editing it is in inspector-host-edit.test.js.

import { describe, expect, test } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import {
  INSPECTOR_CHANGED,
  INSPECTOR_FIELD_WARNINGS,
  INSPECTOR_ICONS,
  INSPECTOR_SUGGESTIONS,
} from '@/components/builder/inspector/control.js';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { setNodeLayer } from '@/builder/model.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';
import { fakeHost, oneDevice } from './inspector-mount.js';
import { ICON_DATA, ICON_KEY } from './png.js';

// The Inspector rendered on the server with `props` (its watchers run once).
// `edit` changes the form's data as JSON Forms sends it.
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
    provides: instance.provides,
  };
}

// One device at layer 1.
function oneLayeredDevice() {
  const { doc, node } = oneDevice();

  return { doc: setNodeLayer(doc, node.id, 1), node };
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

  test('reads the copies of icons the host’s document carries', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const { provides } = await openInspector({ host });
    const icons = provides[INSPECTOR_ICONS];

    expect(icons.entry('plc')).toBeUndefined();
    expect(icons.diagram()).toEqual([]);

    host.doc = { ...host.doc, icons: { plc: { data: ICON_DATA } } };

    expect(icons.entry('plc')).toEqual({ name: 'plc', data: ICON_DATA });
    expect(icons.entry('PLC')).toBeUndefined();
    expect(icons.entry(ICON_KEY)).toBeUndefined();
    expect(icons.diagram()).toEqual([{ name: 'plc', data: ICON_DATA }]);
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
    expect(html).toContain('data-testid="inspector-icon"');
    expect(html).toContain('eth0');
  });

  test('its fields say what they are to a template', async () => {
    const { doc, node } = oneDevice();
    const { html } = await openInspector(props(fakeHost(doc, node.id)));

    expect(html).toContain(
      'Name new devices start from. Builder adds a number when another device has the name.',
    );
    expect(html).toContain('Icon drawn on the canvas and under Templates.');
    expect(html).toContain('Save sets it from the Hostname above.');
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
      'Unique hostname. Apply also sets it as the node hostname.',
    );
    expect(canvas.properties.iconKey.description).toMatch(
      /^Icon of the node on the canvas\./,
    );
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

  // The names come from the working copy, so a ruleset or an interface
  // added before Apply is suggested at once.
  test("ruleset fields and interface name values suggest the working copy's names", async () => {
    const { doc, node } = oneDevice();
    const edit = (data) => {
      data.spec.network = {
        ...data.spec.network,
        interfaces: [
          { name: 'eth0', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
          { name: ' ', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
          { name: 'eth0', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
          { name: 'mgmt0', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
        ],
        rulesets: [
          { name: 'web-in', default: 'drop', rules: [] },
          { name: '', default: 'drop', rules: [] },
          { name: 'web-in', default: 'accept', rules: [] },
          { name: 'dmz', default: 'drop', rules: [] },
        ],
      };
    };
    const host = fakeHost(doc, node.id, { disks: ['ubuntu.qc2'] });
    const { provides } = await openInspector({ host }, edit);

    expect(provides[INSPECTOR_SUGGESTIONS].value).toEqual({
      disks: ['ubuntu.qc2'],
      rulesets: ['web-in', 'dmz'],
      interfaces: ['eth0', 'mgmt0'],
    });
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

  // Under ids of its own (the canvas's Inspector is on the page too), after
  // the colors. The layers are a copy, so there is no Edit layers….
  test('shows the layer of the template’s device after its colors', async () => {
    const { doc, node } = oneLayeredDevice();
    const { html } = await openInspector(props(fakeHost(doc, node.id)));
    const select = tags(html, 'select').find((tag) =>
      tag.includes('id="template-layer-select"'),
    );
    const form = html.slice(html.indexOf('<form'), html.indexOf('</form>'));
    const at = (text) => form.indexOf(text);

    expect(html).toContain('data-testid="inspector-layer"');
    expect(html).not.toContain(' id="inspector-layer-select"');
    expect(html).toMatch(
      /<label for="template-layer-select"[^>]*>Layer<\/label>/,
    );
    expect(html).not.toMatch(/<h[34][^>]*><label for="template-layer-select"/);
    expect(select).toContain('aria-describedby="template-layer-hint"');
    expect(select).toContain('value="1"');
    expect(at('data-path="fillColor"')).toBeGreaterThan(-1);
    expect(at('data-testid="inspector-layer"')).toBeGreaterThan(
      at('data-path="fillColor"'),
    );
    expect(at('data-testid="inspector-layer"')).toBeLessThan(
      at('data-path="spec.type"'),
    );
    expect(html).not.toContain('inspector-layer-edit');
  });

  // The canvas shows it in a section of its own, after the form.
  test('the canvas shows the layer in a section of its own', async () => {
    const { doc, node } = oneLayeredDevice();
    const { html } = await openInspector({ host: fakeHost(doc, node.id) });
    const form = html.slice(html.indexOf('<form'), html.indexOf('</form>'));

    expect(form).not.toContain('data-testid="inspector-layer"');
    expect(html).toMatch(
      /<h3[^>]*><label for="inspector-layer-select"[^>]*>Layer<\/label><\/h3>/,
    );
    // Edit layers… shows wherever the layers are the diagram's.
    expect(html).toContain('data-testid="inspector-layer-edit"');
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

describe('the editor variant', () => {
  const props = (host) => ({
    host,
    variant: 'editor',
    labelledby: 'node-editor-title',
  });

  test('shows the node’s form with room, its checks, connection points and position', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id, {
      issues: [
        {
          path: 'nodes[1].device.spec',
          message: 'something about the device',
          level: 'warning',
          nodeId: alpha.id,
        },
      ],
    });
    const { html } = await openInspector(props(host));
    const [section] = tags(html, 'section');

    expect(section).toContain('aria-labelledby="node-editor-title"');
    expect(section).toContain('builder-inspector--wide');
    expect(section).toContain('builder-inspector--editor');
    expect(section).not.toContain('builder-panel');
    // The dialog's title names the node: no heading, subject or Open Node
    // Editor of its own.
    expect(html).not.toContain('id="inspector-title"');
    expect(html).not.toContain('builder-inspector__subject');
    expect(html).not.toContain('data-testid="inspector-node-editor"');
    expect(html).toContain('data-testid="inspector-checks"');
    expect(html).toContain('Connection points');
    expect(html).toContain('aria-describedby="node-editor-ifaces-hint"');
    // Its parts have ids of their own: the Inspector beside the canvas is
    // on the page too.
    expect(html).toContain('id="node-editor-position-title"');
    expect(html).toContain('id="node-editor-position-x"');
    expect(html).not.toContain('id="inspector-position-');
    expect(html).toContain('Hostname');
  });

  test('puts the layer in the form, after the colors, with Edit layers…', async () => {
    const { doc, node } = oneLayeredDevice();
    const { html } = await openInspector(props(fakeHost(doc, node.id)));
    const at = (text) => html.indexOf(text);

    expect(html.match(/data-testid="inspector-layer"/g)).toHaveLength(1);
    expect(html).toContain('id="node-editor-layer-select"');
    expect(at('data-testid="inspector-layer"')).toBeGreaterThan(
      at('data-path="fillColor"'),
    );
    expect(at('data-testid="inspector-layer"')).toBeLessThan(
      at('data-path="spec.type"'),
    );
    expect(html).toContain('data-testid="inspector-layer-edit"');
  });
});

describe('Open Node Editor in the Inspector', () => {
  test('shows for a node, named, with its keys and its dialog', async () => {
    const { doc, alpha } = sampleDocument();
    const { html } = await openInspector({ host: fakeHost(doc, alpha.id) });
    const [button] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="inspector-node-editor"'),
    );

    expect(button).toContain('aria-label="Open Node Editor"');
    expect(button).toContain('aria-haspopup="dialog"');
    expect(button).toContain('aria-keyshortcuts="Alt+Shift+E"');
    // At the right of the subject line.
    expect(html.indexOf('builder-inspector__subject')).toBeLessThan(
      html.indexOf('data-testid="inspector-node-editor"'),
    );
  });

  test('does not show for a connection or the diagram', async () => {
    const { doc, edge } = sampleDocument();
    const connection = fakeHost(doc, null, {
      inspectorSelection: { type: 'edge', id: edge.id },
    });
    const diagram = fakeHost(doc, null, {
      inspectorSelection: { type: 'document' },
    });

    for (const host of [connection, diagram]) {
      const { html } = await openInspector({ host });

      expect(html).not.toContain('inspector-node-editor');
    }
  });
});
