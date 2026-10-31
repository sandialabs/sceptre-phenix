// @vitest-environment happy-dom
// The Node Templates tab's cards, selected as a user would: a checkbox with
// Shift for a range, the cards' keys, and Delete, which asks about them all.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderTemplates from '@/components/builder/BuilderTemplates.vue';
import { createDocument } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { libraryOf } from './fixtures.js';

const IDS = ['alfa', 'bravo', 'charlie', 'delta'];

function template(id) {
  return {
    id,
    name: `Range ${id}`,
    device: {
      iconKey: 'router',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: `${id}.qc2` }] },
        network: { interfaces: [] },
      },
    },
  };
}

enableAutoUnmount(afterEach);

beforeEach(() => {
  sessionStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

// The tab, in the page, showing a collection of the four templates.
async function rangeSet() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = createDocument({ name: 'Plant' });
  store.templates = libraryOf({
    templates: IDS.map(template),
    collections: [{ id: 'range', name: 'Range set', templateIds: IDS }],
  });

  const wrapper = mount(BuilderTemplates, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await wrapper.get('[data-testid="templates-show"]').setValue('range');

  return { wrapper, store };
}

const card = (wrapper, id) =>
  wrapper.get(`[data-testid="template-card-${id}"]`);
const box = (wrapper, id) =>
  wrapper.get(`[data-testid="template-select-${id}"]`);
const countOf = (wrapper) =>
  wrapper.get('[data-testid="bulk-count-templates"]').text();
const confirmOf = (wrapper) => wrapper.find('[data-testid="builder-confirm"]');

describe('the library’s cards', () => {
  test('are selected in ranges with Shift, and Delete asks about them', async () => {
    const warn = vi.spyOn(console, 'warn');
    const error = vi.spyOn(console, 'error');
    const { wrapper, store } = await rangeSet();
    const [alfa, bravo, charlie, delta] = IDS;

    expect(wrapper.findAll('[data-testid="templates-list"] > li')).toHaveLength(
      4,
    );

    // Shift and a checkbox select the cards between it and the one last
    // pressed.
    await box(wrapper, alfa).setValue(true);
    await box(wrapper, charlie).trigger('click', { shiftKey: true });
    expect(countOf(wrapper)).toBe('3 of 4 selected');
    expect(box(wrapper, bravo).element.checked).toBe(true);
    expect(box(wrapper, delta).element.checked).toBe(false);
    expect(store.announcement).toBe('3 of 4 selected.');

    // On a card, Escape clears, and Space then Shift+End select a range.
    card(wrapper, alfa).element.focus();
    await card(wrapper, alfa).trigger('keydown', { key: 'Escape' });
    expect(countOf(wrapper)).toBe('0 of 4 selected');
    await card(wrapper, alfa).trigger('keydown', { key: ' ' });
    expect(countOf(wrapper)).toBe('1 of 4 selected');
    await card(wrapper, alfa).trigger('keydown', {
      key: 'End',
      shiftKey: true,
    });
    expect(document.activeElement).toBe(card(wrapper, delta).element);
    expect(countOf(wrapper)).toBe('4 of 4 selected');

    // Delete asks to delete the selected templates, and Cancel keeps them.
    await card(wrapper, delta).trigger('keydown', { key: 'Delete' });

    const confirm = confirmOf(wrapper);

    expect(
      document.getElementById(confirm.attributes('aria-labelledby'))
        .textContent,
    ).toBe('Delete 4 templates?');
    expect(confirm.attributes('role')).toBe('alertdialog');

    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click');
    expect(confirmOf(wrapper).exists()).toBe(false);
    expect(countOf(wrapper)).toBe('4 of 4 selected');
    // Vue warned of nothing on the way.
    expect(warn).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });

  test('a checkbox without Shift selects its card alone', async () => {
    const { wrapper } = await rangeSet();

    await box(wrapper, 'alfa').setValue(true);
    await box(wrapper, 'charlie').trigger('click');
    expect(countOf(wrapper)).toBe('2 of 4 selected');
    expect(box(wrapper, 'bravo').element.checked).toBe(false);
  });
});

describe('the filter of the Node Templates tab', () => {
  const ids = (wrapper) =>
    wrapper
      .findAll('[data-testid="templates-list"] > li')
      .map((item) =>
        item.attributes('data-testid').replace('template-card-', ''),
      );

  test('shows the cards that match what t: in the palette finds, and says how many', async () => {
    vi.useFakeTimers();

    const { wrapper, store } = await rangeSet();
    const field = wrapper.get('[data-testid="templates-filter"]');

    expect(wrapper.get('label[for="templates-filter"]').text()).toBe(
      'Filter templates',
    );
    expect(field.attributes('aria-describedby')).toBe('templates-filter-count');

    // The name, then a field: the drive image.
    await field.setValue('range bravo');
    expect(ids(wrapper)).toEqual(['bravo']);
    await field.setValue('charlie.qc2');
    expect(ids(wrapper)).toEqual(['charlie']);
    expect(wrapper.get('[data-testid="templates-filter-count"]').text()).toBe(
      '1 of 4 templates shown.',
    );
    await vi.advanceTimersByTimeAsync(500);
    expect(store.announcement).toBe('1 of 4 templates shown.');
    expect(sessionStorage.getItem('phenix.builder.filter.templates')).toBe(
      'charlie.qc2',
    );

    // The collection a template is in finds it too.
    await field.setValue('Range set');
    expect(ids(wrapper)).toEqual(IDS);

    await field.setValue('zebra');
    expect(ids(wrapper)).toEqual([]);
    expect(wrapper.get('[data-testid="templates-filter-none"]').text()).toBe(
      'No templates match the filter.',
    );

    await field.trigger('keydown', { key: 'Escape' });
    expect(field.element.value).toBe('');
    expect(ids(wrapper)).toEqual(IDS);
  });

  test('Select all and the keys act on the cards shown, and a hidden card leaves the selection', async () => {
    const { wrapper } = await rangeSet();
    const field = wrapper.get('[data-testid="templates-filter"]');

    await wrapper.get('[data-testid="bulk-all-templates"]').setValue(true);
    expect(countOf(wrapper)).toBe('4 of 4 selected');

    await field.setValue('alfa');
    expect(countOf(wrapper)).toBe('1 of 1 selected');

    await field.setValue('');
    expect(countOf(wrapper)).toBe('1 of 4 selected');

    await field.setValue('range');
    card(wrapper, 'bravo').element.focus();
    await card(wrapper, 'bravo').trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(card(wrapper, 'delta').element);
  });
});
