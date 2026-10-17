// @vitest-environment happy-dom
// The Icon field's menu, mounted and used with the pointer and the keys: the
// built-in icons and Custom icon…, which opens the Custom icons dialog
// (InspectorIconControl and BuilderMenuButton). Its markup is in
// inspector-render.test.js.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import { isIconKey } from '@/builder/catalog.js';
import { ICON_ORDER, iconLabel } from '@/builder/iconLabels.js';
import { addNode, findNode, updateNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import { mountInspector } from './inspector-mount.js';
import { ICON_DATA } from './png.js';

enableAutoUnmount(afterEach);

const offered = ICON_ORDER.filter(isIconKey);

// The Inspector on the sample's alpha device, or on what `change` selects.
async function openIconField({ change, readOnly = false } = {}) {
  const sample = sampleDocument();
  const { doc, element, icons } = change
    ? change(sample)
    : { doc: sample.doc, element: sample.alpha };
  const { wrapper, store } = await mountInspector({
    doc,
    element,
    prepare(opened) {
      // As loaded: a commit drops copies the document need not carry.
      if (icons) {
        opened.doc = { ...opened.doc, icons };
      }
      opened.readOnly = readOnly;
    },
  });
  const button = wrapper.get('[data-testid="inspector-icon"]');
  const menu = () => wrapper.find('[role="menu"]');
  const items = () => menu().findAll('[role^="menuitem"]');

  return {
    wrapper,
    store,
    button,
    menu,
    items,
    node: () => findNode(store.doc, element.id),
    item: (id) => wrapper.get(`[data-testid="inspector-icon-item-${id}"]`),
    // The item ids, from their test ids.
    ids: () =>
      items().map((item) =>
        item.attributes('data-testid').replace('inspector-icon-item-', ''),
      ),
    name: () => wrapper.get('[data-testid="inspector-icon-name"]').text(),
  };
}

// The alpha device names the custom icon plc. The diagram carries copies of
// plc and rtu.
const withCustomIcon = ({ doc, alpha }) => ({
  doc: updateNode(doc, alpha.id, { device: { icon: 'plc' } }),
  element: alpha,
  icons: { plc: { data: ICON_DATA }, rtu: { data: ICON_DATA } },
});

async function press(target, key) {
  await target.trigger('keydown', { key });
}

describe('the Icon field’s menu', () => {
  test('lists Default, the built-in icons by their labels, then Custom icon…', async () => {
    const field = await openIconField();

    await field.button.trigger('click');

    expect(field.button.attributes('aria-expanded')).toBe('true');
    expect(field.button.attributes('aria-controls')).toBe(
      field.menu().attributes('id'),
    );
    expect(field.menu().attributes('aria-label')).toBe('Icon');
    expect(field.ids()).toEqual(['default', ...offered, 'custom-icon']);
    expect(
      field
        .items()
        .map((item) => item.get('.builder-menu__text > span').text()),
    ).toEqual(['Default (Server)', ...offered.map(iconLabel), 'Custom icon…']);
    expect(
      field
        .items()
        .every((item) => item.attributes('role') === 'menuitemradio'),
    ).toBe(true);
    // Each built-in icon is drawn beside its label.
    expect(field.items()[1].get('.builder-menu__icon').classes()).toContain(
      `builder-icon--${offered[0]}`,
    );
    // The icon the device draws is the one checked, and the menu opens on it.
    const checked = field
      .items()
      .filter((item) => item.attributes('aria-checked') === 'true');

    expect(checked.map((item) => item.attributes('data-testid'))).toEqual([
      'inspector-icon-item-linux',
    ]);
    expect(document.activeElement).toBe(field.item('linux').element);
    // Custom icon… comes after a separator, unchecked.
    const custom = field.item('custom-icon');

    expect(custom.attributes('aria-checked')).toBe('false');
    expect(custom.element.previousElementSibling.getAttribute('role')).toBe(
      'separator',
    );
  });

  // An icon drawing draws one icon or the other: no Default.
  test('an icon drawing has no Default', async () => {
    const field = await openIconField({
      change: ({ doc }) => {
        const added = addNode(doc, { kind: 'icon', iconKey: 'router' });

        return { doc: added.doc, element: added.node };
      },
    });

    await field.button.trigger('click');

    expect(field.ids()[0]).not.toBe('default');
    expect(
      field
        .items()
        .filter((item) => item.attributes('aria-checked') === 'true')
        .map((item) => item.attributes('data-testid')),
    ).toEqual(['inspector-icon-item-router']);
  });

  test('the arrow keys, Home and End move through it, and Escape closes it with focus back on the button', async () => {
    const field = await openIconField();
    const focused = () => document.activeElement;

    field.button.element.focus();
    await press(field.button, 'ArrowDown');
    // Opened on the checked item.
    expect(focused()).toBe(field.item('linux').element);

    await press(field.menu(), 'Home');
    expect(focused()).toBe(field.item('default').element);
    await press(field.menu(), 'ArrowDown');
    expect(focused()).toBe(field.item(offered[0]).element);
    expect(focused().getAttribute('tabindex')).toBe('0');
    await press(field.menu(), 'End');
    expect(focused()).toBe(field.item('custom-icon').element);
    // The ends wrap around.
    await press(field.menu(), 'ArrowDown');
    expect(focused()).toBe(field.item('default').element);
    await press(field.menu(), 'ArrowUp');
    expect(focused()).toBe(field.item('custom-icon').element);

    await press(field.menu(), 'Escape');
    expect(field.menu().exists()).toBe(false);
    expect(field.button.attributes('aria-expanded')).toBe('false');
    expect(focused()).toBe(field.button.element);

    // Up Arrow opens it on its last item.
    await press(field.button, 'ArrowUp');
    expect(focused()).toBe(field.item('custom-icon').element);
  });

  test('Enter chooses the icon with focus, applied at once, and focus goes back to the button', async () => {
    const field = await openIconField();

    field.button.element.focus();
    await press(field.button, 'ArrowDown');
    await press(field.menu(), 'Home');
    await press(field.menu(), 'ArrowDown');
    await press(field.menu(), 'Enter');
    await flushPromises();

    expect(field.menu().exists()).toBe(false);
    expect(document.activeElement).toBe(field.button.element);
    expect(field.node().device.iconKey).toBe(offered[0]);
    expect(field.store.history.undoLabel()).toBe(
      `Changed the icon of Device alpha to ${iconLabel(offered[0])}`,
    );
    expect(field.name()).toBe(iconLabel(offered[0]));
  });

  test('a built-in icon chosen removes the custom icon, and Custom icon… opens the dialog', async () => {
    const field = await openIconField({ change: withCustomIcon });

    await field.button.trigger('click');
    await field.item('custom-icon').trigger('click');
    await flushPromises();

    const dialog = field.wrapper.get('[data-testid="icon-dialog"]');

    expect(dialog.element.open).toBe(true);
    await dialog
      .get('[data-icon="rtu"] [data-testid="icon-use"]')
      .trigger('click');
    await flushPromises();

    expect(field.wrapper.find('[data-testid="icon-dialog"]').exists()).toBe(
      false,
    );
    expect(field.node().device.icon).toBe('rtu');
    expect(field.name()).toBe('rtu');

    // The icon chosen already, while a custom icon is in use, takes its
    // place too.
    await field.button.trigger('click');
    await field.item('server').trigger('click');
    await flushPromises();
    expect(field.node().device.icon).toBeUndefined();
    expect(field.node().device.iconKey).toBe('server');

    await field.button.trigger('click');
    await field.item('firewall').trigger('click');
    await flushPromises();
    expect(field.node().device.iconKey).toBe('firewall');
    expect(document.activeElement).toBe(field.button.element);
  });

  test('a custom icon in use is the one checked, named under Custom icon…', async () => {
    const field = await openIconField({ change: withCustomIcon });

    await field.button.trigger('click');

    const checked = field
      .items()
      .filter((item) => item.attributes('aria-checked') === 'true');
    const custom = field.item('custom-icon');

    expect(checked.map((item) => item.attributes('data-testid'))).toEqual([
      'inspector-icon-item-custom-icon',
    ]);
    expect(
      field.wrapper.get(`#${custom.attributes('aria-describedby')}`).text(),
    ).toBe('plc');
  });

  test('of a read-only draft does not open', async () => {
    const field = await openIconField({ readOnly: true });

    await field.button.trigger('click');
    await press(field.button, 'ArrowDown');

    expect(field.menu().exists()).toBe(false);
    expect(field.button.attributes('aria-expanded')).toBe('false');
  });
});
