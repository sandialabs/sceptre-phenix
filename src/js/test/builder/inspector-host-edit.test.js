// @vitest-environment happy-dom
// The Inspector on a host other than the Builder store, mounted and edited
// in its fields: beside the canvas, in the template editor ('template') and
// in the Node Editor ('editor'). Its markup is in inspector-host.test.js.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { addNode, createDocument, findNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import {
  actionsShown,
  fakeHost,
  fieldAt,
  mountInspector,
  oneDevice,
  sendFormData,
} from './inspector-mount.js';

enableAutoUnmount(afterEach);

const TEMPLATE = { variant: 'template', labelledby: 'template-fields-title' };
const EDITOR = { variant: 'editor', labelledby: 'node-editor-title' };

// The Inspector mounted on `host`, with `props` beside it.
async function openInspector(host, props = {}) {
  const { wrapper, store } = await mountInspector({
    props: { host, ...props },
  });

  return {
    wrapper,
    store,
    changed: () => wrapper.vm.changed,
    errors: () => wrapper.vm.errors,
    settle: () => wrapper.vm.settle(),
    field: (path) => fieldAt(wrapper, path),
  };
}

describe('the Inspector on a host of its own', () => {
  test('applies unapplied edits to the host’s document', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id);
    const opened = await openInspector(host);

    await opened.field('spec.general.description').setValue('Edited');

    expect(opened.changed()).toBe(true);
    expect(actionsShown(opened.wrapper)).toBe(true);
    expect(opened.settle()).toBe('');
    expect(findNode(host.doc, alpha.id).device.spec.general.description).toBe(
      'Edited',
    );
    expect(host.commits).toEqual(['Applied changes to Device alpha']);
    expect(opened.changed()).toBe(false);
    expect(opened.store.doc.nodes).toEqual([]);
  });

  test('a host that is read only takes no edit', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id, { readOnly: true });
    const opened = await openInspector(host);
    const description = opened.field('spec.general.description');

    expect(description.attributes('readonly')).toBeDefined();
    await description.setValue('Edited');

    expect(opened.settle()).toBe('');
    expect(host.commits).toEqual([]);

    // A change that reaches the form all the same, as the host turned read
    // only after it was made.
    const data = JSON.parse(
      JSON.stringify(inspectorTarget(host.doc, host.inspectorSelection).data),
    );

    data.spec.general = { ...data.spec.general, description: 'Edited' };
    await sendFormData(opened.wrapper, data);

    expect(opened.settle()).toBe('');
    expect(host.commits).toEqual([]);
  });

  test('Apply commits the edits to the host, and Cancel puts the fields back', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id);
    const opened = await openInspector(host);
    const description = () => opened.field('spec.general.description');

    await description().setValue('Applied');
    await opened.wrapper
      .get('[data-testid="inspector-apply"]')
      .trigger('click');
    await flushPromises();

    expect(host.commits).toEqual(['Updated device alpha']);
    expect(findNode(host.doc, alpha.id).device.spec.general.description).toBe(
      'Applied',
    );
    expect(actionsShown(opened.wrapper)).toBe(false);

    await description().setValue('Cancelled');
    await opened.wrapper
      .get('[data-testid="inspector-cancel"]')
      .trigger('click');
    await flushPromises();

    expect(host.commits).toHaveLength(1);
    expect(description().element.value).toBe('Applied');
    expect(actionsShown(opened.wrapper)).toBe(false);
    expect(host.said).toContain('Discarded unapplied changes.');
  });
});

describe('the template variant', () => {
  // Its edits wait for the editor's Save: there is nothing to apply them
  // with, as there is on the canvas.
  test('unapplied edits bring up Apply and Cancel on the canvas, not in the template editor', async () => {
    const { doc, node } = oneDevice();
    const canvas = await openInspector(fakeHost(doc, node.id));
    const editor = await openInspector(fakeHost(doc, node.id), TEMPLATE);
    const buttons = ({ wrapper }) =>
      ['inspector-apply', 'inspector-cancel'].filter((id) =>
        wrapper.find(`[data-testid="${id}"]`).exists(),
      );

    await canvas.field('hostname').setValue('historian');
    await editor.field('hostname').setValue('historian');

    expect(canvas.changed()).toBe(true);
    expect(buttons(canvas)).toEqual(['inspector-apply', 'inspector-cancel']);
    expect(editor.changed()).toBe(true);
    expect(buttons(editor)).toEqual([]);
  });

  // A look field changes the template's working copy at once, as on the
  // canvas: Save saves the template.
  test('a look field is committed to the host at once, with nothing left to settle', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const opened = await openInspector(host, TEMPLATE);
    const data = JSON.parse(
      JSON.stringify(inspectorTarget(host.doc, host.inspectorSelection).data),
    );

    // An icon and a color in one change of the form, which no one control
    // sends.
    await sendFormData(opened.wrapper, {
      ...data,
      iconKey: 'firewall',
      fillColor: '#2f6fbf',
    });

    const device = findNode(host.doc, node.id).device;

    expect(device).toMatchObject({ iconKey: 'firewall', fillColor: '#2f6fbf' });
    expect(host.commits).toEqual([
      'Changed the icon of Device plc to Firewall',
    ]);
    expect(opened.changed()).toBe(false);
    expect(opened.settle()).toBe('');
    expect(host.commits).toHaveLength(1);
  });

  test('settle applies the working copy to the host’s document', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const opened = await openInspector(host, TEMPLATE);

    await opened.field('hostname').setValue('historian');
    await opened.field('spec.hardware.memory').setValue('4096');

    expect(opened.changed()).toBe(true);
    expect(opened.errors()).toEqual([]);
    expect(opened.settle()).toBe('');

    const device = findNode(host.doc, node.id).device;

    expect(device.hostname).toBe('historian');
    expect(device.spec.general.hostname).toBe('historian');
    expect(device.spec.hardware.memory).toBe(4096);
    expect(opened.changed()).toBe(false);
  });

  test('an edit with an error is not applied, and settle says why', async () => {
    const { doc, node } = oneDevice();
    const host = fakeHost(doc, node.id);
    const opened = await openInspector(host, TEMPLATE);

    await opened.field('hostname').setValue('two words');

    expect(opened.errors()).toHaveLength(1);
    expect(opened.field('hostname').attributes('aria-invalid')).toBe('true');
    expect(opened.settle()).toBe('1 field needs attention');
    expect(findNode(host.doc, node.id).device.hostname).toBe('plc');
    expect(host.commits).toEqual([]);
  });
});

describe('the editor variant', () => {
  // The dialog's Apply settles the form: one commit, one undo step. The
  // Inspector's own bar does not show, but the fields keep their marks.
  test('edits wait for settle(), which commits them once', async () => {
    const { doc, alpha } = sampleDocument();
    const host = fakeHost(doc, alpha.id);
    const opened = await openInspector(host, EDITOR);
    const path = 'spec.general.description';

    await opened.field(path).setValue('Edited');

    expect(opened.changed()).toBe(true);
    expect(actionsShown(opened.wrapper)).toBe(false);
    expect(
      opened.wrapper.get(`[data-path="${path}"]`).classes('control--changed'),
    ).toBe(true);
    expect(host.commits).toEqual([]);

    expect(opened.settle()).toBe('');
    expect(host.commits).toEqual(['Applied changes to Device alpha']);
    expect(findNode(host.doc, alpha.id).device.spec.general.description).toBe(
      'Edited',
    );
  });
});

// The Node Editor commits the look to its host at once, as the Inspector
// does (see inspector-settle.test.js for every kind).
describe('the editor variant applies the look at once', () => {
  // A custom icon is named. The document carries no copy of it. A color is
  // typed in its field. A custom icon comes from the server's library.
  test.each([
    ['device', 'fillColor', { hostname: 'plc' }, '#2f6fbf', 'fill color'],
    ['device', 'icon', { hostname: 'plc' }, 'plc', 'custom icon'],
    ['group', 'color', { title: 'Site' }, '#2f6fbf', 'color'],
    ['note', 'color', { text: 'Hello' }, '#2f6fbf', 'color'],
  ])('a %s’s %s', async (kind, key, options, value, name) => {
    const added = addNode(createDocument({ name: 'Scratch' }), {
      kind,
      ...options,
    });
    const host = fakeHost(added.doc, added.node.id);
    const opened = await openInspector(host, EDITOR);
    const title =
      kind === 'device' ? 'Device plc' : kind[0].toUpperCase() + kind.slice(1);

    if (key === 'icon') {
      // The Custom icons dialog lists the server's icons, which no unit
      // test has: the form's change is sent as the dialog's choice makes it.
      const data = JSON.parse(
        JSON.stringify(inspectorTarget(host.doc, host.inspectorSelection).data),
      );

      await sendFormData(opened.wrapper, { ...data, icon: value });
    } else {
      await opened.field(key).setValue(value);
    }

    expect(findNode(host.doc, added.node.id)[kind][key]).toBe(value);
    expect('icons' in host.doc).toBe(false);
    expect(host.commits).toEqual([
      `Changed the ${name} of ${title} to ${value}`,
    ]);
    expect(opened.changed()).toBe(false);
    expect(opened.settle()).toBe('');
    expect(host.commits).toHaveLength(1);
  });
});
