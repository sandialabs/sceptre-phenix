// @vitest-environment happy-dom
// The Inspector's own JSON Forms renderers, mounted in the Inspector and
// used from the keyboard and the pointer: a field that suggests values
// (InspectorComboboxControl), lists (InspectorArrayRenderer) and the kind of
// an interface (InspectorOneOfRenderer).

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import { resetCollapsed } from '@/components/builder/inspector/collapse.js';

import { addNode, findNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import {
  actionsShown,
  disclosureOf,
  fieldAt,
  formData,
  keydown,
  mountInspector,
  type,
} from './inspector-mount.js';

enableAutoUnmount(afterEach);
afterEach(resetCollapsed);

const DISKS = ['ubuntu.qc2', 'kali.qc2', 'ubuntu-22.qc2', 'win10.qc2'];

// The Inspector on the sample's alpha device, the server's disk images known.
async function openAlpha() {
  const sample = sampleDocument();
  const opened = await mountInspector({
    doc: sample.doc,
    element: sample.alpha,
    prepare(store) {
      store.disks = DISKS;
    },
  });

  return { ...opened, ...sample };
}

// A choice from a select with the pointer: its press ends as its click does.
async function pick(select, value) {
  await select.trigger('pointerdown', { button: 0 });
  await select.setValue(value);
  await select.trigger('pointerup');
  await new Promise((resolve) => setTimeout(resolve));
  await flushPromises();
}

async function apply(wrapper) {
  await wrapper.get('[data-testid="inspector-apply"]').trigger('click');
  await flushPromises();
}

describe('a field that suggests values (a drive’s image)', () => {
  const IMAGE = 'spec.hardware.drives.0.image';

  async function openImage() {
    const opened = await openAlpha();
    const field = fieldAt(opened.wrapper, IMAGE);
    const listbox = () =>
      opened.wrapper.get(`[id="${field.attributes('aria-controls')}"]`);

    return {
      ...opened,
      field,
      listbox,
      options: () => listbox().findAll('[role="option"]'),
      expanded: () => field.attributes('aria-expanded'),
    };
  }

  test('typing filters the suggestions, and the arrow keys move through them', async () => {
    const { field, listbox, options, expanded } = await openImage();
    const wanted = DISKS.filter((disk) => disk.startsWith('ub')).sort((a, b) =>
      a.localeCompare(b),
    );

    expect(field.attributes('role')).toBe('combobox');
    expect(expanded()).toBe('false');

    field.element.focus();
    await type(field, 'ub');
    expect(expanded()).toBe('true');
    expect(listbox().isVisible()).toBe(true);
    expect(options().map((option) => option.text())).toEqual(wanted);
    expect(field.attributes('aria-activedescendant')).toBeUndefined();

    const activeId = () => field.attributes('aria-activedescendant');
    const selected = () =>
      options()
        .filter((option) => option.attributes('aria-selected') === 'true')
        .map((option) => option.attributes('id'));

    expect(keydown(field, 'ArrowDown').defaultPrevented).toBe(true);
    await flushPromises();
    expect(activeId()).toBe(options()[0].attributes('id'));
    expect(selected()).toEqual([activeId()]);

    keydown(field, 'ArrowDown');
    await flushPromises();
    expect(activeId()).toBe(options()[1].attributes('id'));

    // The ends wrap around.
    keydown(field, 'ArrowDown');
    await flushPromises();
    expect(activeId()).toBe(options()[0].attributes('id'));
    keydown(field, 'ArrowUp');
    await flushPromises();
    expect(activeId()).toBe(options().at(-1).attributes('id'));
    expect(document.activeElement).toBe(field.element);
  });

  test('Enter takes the suggestion in view and closes the list; the edit waits for Apply', async () => {
    const { wrapper, store, alpha, field, expanded } = await openImage();
    const image = () =>
      findNode(store.doc, alpha.id).device.spec.hardware?.drives?.[0]?.image;

    field.element.focus();
    await type(field, 'kal');
    keydown(field, 'ArrowDown');
    await flushPromises();

    const enter = keydown(field, 'Enter');

    await flushPromises();
    // Taken, not submitted: the form is not applied.
    expect(enter.defaultPrevented).toBe(true);
    expect(field.element.value).toBe('kali.qc2');
    expect(expanded()).toBe('false');
    expect(field.attributes('aria-activedescendant')).toBeUndefined();
    expect(formData(wrapper).spec.hardware.drives[0].image).toBe('kali.qc2');
    expect(actionsShown(wrapper)).toBe(true);
    expect(image()).not.toBe('kali.qc2');

    await apply(wrapper);
    expect(image()).toBe('kali.qc2');
  });

  test('a click takes a suggestion too', async () => {
    const { wrapper, field, options } = await openImage();

    field.element.focus();
    await type(field, 'win');
    await options()[0].trigger('click');

    expect(field.element.value).toBe('win10.qc2');
    expect(formData(wrapper).spec.hardware.drives[0].image).toBe('win10.qc2');
  });

  test('Escape closes the list, keeps the text typed, and goes no further', async () => {
    const { field, expanded } = await openImage();
    const reached = vi.fn();

    field.element.focus();
    await type(field, 'ub');
    keydown(field, 'ArrowDown');
    await flushPromises();
    document.addEventListener('keydown', reached);

    try {
      const escape = keydown(field, 'Escape');

      await flushPromises();
      expect(escape.defaultPrevented).toBe(true);
      expect(reached).not.toHaveBeenCalled();
      expect(expanded()).toBe('false');
      expect(field.attributes('aria-activedescendant')).toBeUndefined();
      expect(field.element.value).toBe('ub');

      // With the list closed, Escape is the dialog's or the view's.
      expect(keydown(field, 'Escape').defaultPrevented).toBe(false);
      expect(reached).toHaveBeenCalledTimes(1);
    } finally {
      document.removeEventListener('keydown', reached);
    }

    // Alt+Down Arrow opens it again, with no suggestion in view.
    field.element.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowDown',
        altKey: true,
        bubbles: true,
        cancelable: true,
      }),
    );
    await flushPromises();
    expect(expanded()).toBe('true');
    expect(field.attributes('aria-activedescendant')).toBeUndefined();
  });

  test('with no images known it is a plain text field', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper } = await mountInspector({ doc, element: alpha });
    const field = fieldAt(wrapper, IMAGE);

    expect(field.attributes('role')).toBeUndefined();
    expect(field.attributes('aria-expanded')).toBeUndefined();
    await type(field, 'ub');
    expect(wrapper.find('[role="listbox"]').exists()).toBe(false);
  });
});

describe('a list', () => {
  const COMMANDS = 'spec.commands';

  // The list's items' values, as their fields show them.
  const values = (wrapper) =>
    wrapper
      .findAll(
        `[data-path="${COMMANDS}"] > .array-list-item-wrapper :is(input, textarea)`,
      )
      .map((field) => field.element.value);
  const button = (wrapper, label) => wrapper.get(`[aria-label="${label}"]`);
  const add = (wrapper) =>
    wrapper.get(`[data-path="${COMMANDS}"] > .array-list-add`);

  // Adds a command for each text, typed into the field Add puts focus in.
  async function addCommands(wrapper, texts) {
    for (const text of texts) {
      await add(wrapper).trigger('click');
      await flushPromises();

      const field = document.activeElement;

      field.value = text;
      field.dispatchEvent(new Event('input', { bubbles: true }));
      field.dispatchEvent(new Event('change', { bubbles: true }));
      await flushPromises();
    }
  }

  test('Add puts a new item at the end, with focus in its field, and says so', async () => {
    const { wrapper, store } = await openAlpha();
    const announce = vi.spyOn(store, 'announce');

    expect(wrapper.get(`[data-path="${COMMANDS}"]`).text()).toContain(
      'No miniccc commands.',
    );
    await add(wrapper).trigger('click');
    await flushPromises();

    const [field] = wrapper.findAll(
      `[data-path="${COMMANDS}"] > .array-list-item-wrapper :is(input, textarea)`,
    );

    expect(document.activeElement).toBe(field.element);
    expect(announce).toHaveBeenCalledWith('Added miniccc command 1.');
    expect(actionsShown(wrapper)).toBe(true);
  });

  test('move up and down, and remove, change the items, keep focus in the list, and Apply takes them', async () => {
    const { wrapper, store, alpha } = await openAlpha();
    const announce = vi.spyOn(store, 'announce');

    await addCommands(wrapper, ['one', 'two', 'three']);
    expect(values(wrapper)).toEqual(['one', 'two', 'three']);

    // The first item cannot move up, the last cannot move down.
    expect(
      button(wrapper, 'Move miniccc command 1 up').attributes('disabled'),
    ).toBeDefined();
    expect(
      button(wrapper, 'Move miniccc command 3 down').attributes('disabled'),
    ).toBeDefined();

    await button(wrapper, 'Move miniccc command 3 up').trigger('click');
    await flushPromises();
    expect(values(wrapper)).toEqual(['one', 'three', 'two']);
    expect(announce).toHaveBeenCalledWith(
      'Moved miniccc command 3 up to position 2.',
    );
    expect(document.activeElement).toBe(
      button(wrapper, 'Move miniccc command 2 up').element,
    );

    await button(wrapper, 'Move miniccc command 1 down').trigger('click');
    await flushPromises();
    expect(values(wrapper)).toEqual(['three', 'one', 'two']);
    expect(document.activeElement).toBe(
      button(wrapper, 'Move miniccc command 2 down').element,
    );

    // Focus goes to the Remove button of the item now in its place.
    await button(wrapper, 'Remove miniccc command 2').trigger('click');
    await flushPromises();
    expect(values(wrapper)).toEqual(['three', 'two']);
    expect(announce).toHaveBeenCalledWith('Removed miniccc command 2.');
    expect(document.activeElement).toBe(
      button(wrapper, 'Remove miniccc command 2').element,
    );

    // The last item gone, focus goes to the one before it.
    await button(wrapper, 'Remove miniccc command 2').trigger('click');
    await flushPromises();
    expect(document.activeElement).toBe(
      button(wrapper, 'Remove miniccc command 1').element,
    );

    await apply(wrapper);
    expect(findNode(store.doc, alpha.id).device.spec.commands).toEqual([
      'three',
    ]);
  });

  test('the last item removed leaves focus on Add', async () => {
    const { wrapper } = await openAlpha();

    await addCommands(wrapper, ['one']);
    await button(wrapper, 'Remove miniccc command 1').trigger('click');
    await flushPromises();

    expect(values(wrapper)).toEqual([]);
    expect(document.activeElement).toBe(add(wrapper).element);
  });

  test('an item collapses and expands from its header, and keeps its buttons', async () => {
    const { wrapper } = await openAlpha();

    await addCommands(wrapper, ['one']);
    // The list is in a section that starts closed.
    wrapper
      .get(`[data-collapse-path="${COMMANDS}"]`)
      .element.closest('details').open = true;

    const header = disclosureOf(wrapper, `${COMMANDS}.0`);
    const content = () =>
      wrapper.get(
        `[data-collapse-path="${COMMANDS}.0"] .array-list-item-content`,
      );

    expect(header.attributes('aria-expanded')).toBe('true');
    await header.trigger('click');
    expect(header.attributes('aria-expanded')).toBe('false');
    expect(content().isVisible()).toBe(false);
    expect(button(wrapper, 'Remove miniccc command 1').isVisible()).toBe(true);

    await header.trigger('click');
    expect(header.attributes('aria-expanded')).toBe('true');
    expect(content().isVisible()).toBe(true);
  });

  test('a line’s points take a point inserted halfway after another', async () => {
    const { doc } = sampleDocument();
    const { doc: drawn, node } = addNode(doc, {
      kind: 'line',
      position: { x: 40, y: 40 },
    });
    const { wrapper, store } = await mountInspector({
      doc: drawn,
      element: node,
    });
    const announce = vi.spyOn(store, 'announce');
    const [first, second] = node.line.points;
    const points = () => findNode(store.doc, node.id).line.points;

    await button(wrapper, 'Insert point after point 1').trigger('click');
    await flushPromises();

    expect(announce).toHaveBeenCalledWith('Added point 2.');
    expect(
      wrapper
        .get('[data-collapse-path="points.1"]')
        .element.contains(document.activeElement),
    ).toBe(true);

    await apply(wrapper);
    expect(points()).toHaveLength(node.line.points.length + 1);
    expect(points()[1]).toEqual({
      x: (first.x + second.x) / 2,
      y: (first.y + second.y) / 2,
    });
  });
});

describe('the kind of an interface', () => {
  const INTERFACE = 'spec.network.interfaces.0';

  async function openKind() {
    const opened = await openAlpha();
    const picker = () =>
      opened.wrapper.get(`.one-of > [data-path="${INTERFACE}"] select`);
    const confirm = () =>
      opened.wrapper.find('[data-testid="builder-confirm"]');

    return { ...opened, picker, confirm };
  }

  test('switching a filled interface with the pointer asks first; Keep current value keeps it, with focus back on the picker', async () => {
    const { wrapper, picker, confirm } = await openKind();
    const before = formData(wrapper).spec.network.interfaces[0];

    expect(picker().element.value).toBe('0');
    await pick(picker(), '2');
    await flushPromises();

    expect(confirm().exists()).toBe(true);
    expect(confirm().text()).toContain('Switch Interface kind to Serial?');
    // The current kind shows until the switch is confirmed.
    expect(picker().element.value).toBe('0');

    await confirm().get('[data-testid="confirm-cancel"]').trigger('click');
    await flushPromises();

    expect(confirm().exists()).toBe(false);
    expect(picker().element.value).toBe('0');
    expect(document.activeElement).toBe(picker().element);
    expect(formData(wrapper).spec.network.interfaces[0]).toEqual(before);
  });

  test('Escape in the question keeps the kind too', async () => {
    const { wrapper, picker, confirm } = await openKind();
    const before = formData(wrapper).spec.network.interfaces[0];

    await pick(picker(), '1');
    await flushPromises();
    await confirm().trigger('keydown', { key: 'Escape' });
    await flushPromises();

    expect(confirm().exists()).toBe(false);
    expect(document.activeElement).toBe(picker().element);
    expect(formData(wrapper).spec.network.interfaces[0]).toEqual(before);
  });

  test('Switch and clear switches the kind, and clears the values', async () => {
    const { wrapper, store, alpha, picker, confirm } = await openKind();
    const before = formData(wrapper).spec.network.interfaces[0];
    const kind = () =>
      findNode(store.doc, alpha.id).device.spec.network.interfaces[0].type;

    await pick(picker(), '2');
    await flushPromises();
    await confirm().get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    expect(confirm().exists()).toBe(false);
    expect(picker().element.value).toBe('2');
    expect(before).toMatchObject({ name: 'eth0', vlan: 'EXP' });
    expect(formData(wrapper).spec.network.interfaces[0]).toMatchObject({
      type: 'serial',
      device: '/dev/ttyS0',
    });
    expect(formData(wrapper).spec.network.interfaces[0]).not.toHaveProperty(
      'name',
    );
    expect(formData(wrapper).spec.network.interfaces[0]).not.toHaveProperty(
      'vlan',
    );
    expect(actionsShown(wrapper)).toBe(true);

    // The name went with the other values, and Apply waits for one.
    await apply(wrapper);
    expect(kind()).toBe('ethernet');
    expect(wrapper.get('[data-testid="inspector-error-list"]').text()).toBe(
      'Interface 1: Name must have a value',
    );

    await fieldAt(wrapper, `${INTERFACE}.name`).setValue('serial0');
    await apply(wrapper);
    expect(kind()).toBe('serial');
  });

  test('a kind stepped to with keys asks once the choice is made', async () => {
    const { picker, confirm } = await openKind();

    await picker().trigger('keydown', { key: 'ArrowDown' });
    await picker().setValue('1');
    await flushPromises();
    expect(confirm().exists()).toBe(false);

    // The choice is made once the key is done.
    await picker().trigger('keydown', { key: 'Enter' });
    await new Promise((resolve) => setTimeout(resolve));
    await flushPromises();
    expect(confirm().exists()).toBe(true);
    expect(confirm().text()).toContain(
      'Switch Interface kind to DHCP or manual?',
    );
  });
});
