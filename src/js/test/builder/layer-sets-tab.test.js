// @vitest-environment happy-dom
// The Layers tab, mounted: the filter, the cards' selection and keys, and
// the batch actions, which keep a built-in set and say so.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

const save = vi.hoisted(() => vi.fn());
const api = vi.hoisted(() => ({
  listLayerSets: vi.fn(),
  deleteLayerSet: vi.fn(),
  listIcons: vi.fn(),
}));

vi.mock('file-saver', () => ({ saveAs: save }));
vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import BuilderLayerSets from '@/components/builder/BuilderLayerSets.vue';
import { readLayerSet } from '@/builder/api.js';
import { useBuilderStore } from '@/builder/store.js';

const set = (id, name, more = {}) =>
  readLayerSet({
    id,
    name,
    layers: [{ id: 1, label: 'One' }],
    version: 1,
    etag: '"1"',
    ...more,
  });

const PURDUE = set('builtin:purdue', 'Purdue Model', {
  description: 'The levels of the Purdue model.',
  builtin: true,
});
const PLANT = set('plant', 'Plant zones', { description: 'Water plant' });
const OFFICE = set('office', 'Office', { description: 'Desks and printers' });
const SETS = [PURDUE, PLANT, OFFICE];

enableAutoUnmount(afterEach);

beforeEach(() => {
  vi.resetAllMocks();
  sessionStorage.clear();
  api.listLayerSets.mockResolvedValue({
    layerSets: SETS,
    defaultId: null,
    defaultEtag: '"0"',
    damaged: 0,
  });
  api.listIcons.mockResolvedValue({ icons: [] });
});

afterEach(() => {
  vi.useRealTimers();
});

function tab(sets = SETS) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.layerSets = {
    ...store.layerSets,
    status: 'ready',
    loaded: true,
    items: sets,
  };

  const wrapper = mount(BuilderLayerSets, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
}

const get = (wrapper, testid) => wrapper.get(`[data-testid="${testid}"]`);
const card = (wrapper, id) => wrapper.get(`[data-layer-set="${id}"]`);
const box = (wrapper, id) =>
  card(wrapper, id).get('[data-testid="layer-set-select"]');
const shownIds = (wrapper) =>
  wrapper
    .findAll('[data-testid="layer-sets-list"] > li')
    .map((item) => item.attributes('data-layer-set'));
const countOf = (wrapper) => get(wrapper, 'bulk-count-layer-sets').text();

describe('the filter of the Layers tab', () => {
  test('shows the sets that match by name or description, and says how many', async () => {
    vi.useFakeTimers();

    const { wrapper, store } = tab();
    const field = get(wrapper, 'layer-sets-filter');

    expect(wrapper.get('label[for="layer-sets-filter"]').text()).toBe(
      'Filter layer sets',
    );
    expect(field.attributes('type')).toBe('search');
    expect(field.attributes('aria-describedby')).toBe(
      'layer-sets-filter-count',
    );
    expect(get(wrapper, 'layer-sets-filter-count').text()).toBe('');

    await field.setValue('office');
    expect(shownIds(wrapper)).toEqual(['office']);
    expect(get(wrapper, 'layer-sets-filter-count').text()).toBe(
      '1 of 3 layer sets shown.',
    );

    // The description finds a set too, and the count is said once typing
    // stops.
    await field.setValue('water');
    expect(shownIds(wrapper)).toEqual(['plant']);
    await vi.advanceTimersByTimeAsync(500);
    expect(store.announcement).toBe('1 of 3 layer sets shown.');
    expect(store.announcementSlot).toBe('filter');
    expect(sessionStorage.getItem('phenix.builder.filter.layers')).toBe(
      'water',
    );

    await field.setValue('zebra');
    expect(shownIds(wrapper)).toEqual([]);
    expect(get(wrapper, 'layer-sets-filter-none').text()).toBe(
      'No layer sets match the filter.',
    );

    // Escape clears the text, and shows every set again.
    await field.trigger('keydown', { key: 'Escape' });
    expect(field.element.value).toBe('');
    expect(shownIds(wrapper)).toEqual(['builtin:purdue', 'plant', 'office']);
    expect(sessionStorage.getItem('phenix.builder.filter.layers')).toBeNull();
  });

  test('keeps its text for the browser tab, and a hidden set leaves the selection', async () => {
    sessionStorage.setItem('phenix.builder.filter.layers', 'o');

    const { wrapper } = tab();

    expect(get(wrapper, 'layer-sets-filter').element.value).toBe('o');
    await get(wrapper, 'layer-sets-filter').setValue('');
    await get(wrapper, 'bulk-all-layer-sets').setValue(true);
    expect(countOf(wrapper)).toBe('3 of 3 selected');

    await get(wrapper, 'layer-sets-filter').setValue('plant');
    expect(countOf(wrapper)).toBe('1 of 1 selected');
  });
});

describe('selecting layer sets', () => {
  test('a card is a Tab stop named by its set, and its keys select as the drafts page’s do', async () => {
    const { wrapper, store } = tab();
    const purdue = card(wrapper, 'builtin:purdue');
    const office = card(wrapper, 'office');

    expect(purdue.attributes('tabindex')).toBe('0');
    expect(office.attributes('tabindex')).toBe('-1');
    expect(
      purdue
        .attributes('aria-labelledby')
        .split(' ')
        .map((id) => document.getElementById(id).textContent.trim()),
    ).toEqual([expect.stringMatching(/^Purdue Model/), 'not selected']);
    expect(box(wrapper, 'plant').element.labels[0].textContent.trim()).toBe(
      'Select Plant zones',
    );

    purdue.element.focus();
    await purdue.trigger('keydown', { key: ' ' });
    expect(countOf(wrapper)).toBe('1 of 3 selected');
    expect(store.announcement).toBe('1 of 3 selected.');
    expect(purdue.classes()).toContain('is-selected');

    await purdue.trigger('keydown', { key: 'End', shiftKey: true });
    expect(document.activeElement).toBe(office.element);
    expect(countOf(wrapper)).toBe('3 of 3 selected');

    await office.trigger('keydown', { key: 'Escape' });
    expect(countOf(wrapper)).toBe('0 of 3 selected');

    // Shift and a checkbox select the range from the box last pressed.
    await box(wrapper, 'builtin:purdue').setValue(true);
    await box(wrapper, 'office').trigger('click', { shiftKey: true });
    expect(countOf(wrapper)).toBe('3 of 3 selected');
  });

  test('Download selected saves a file for each set, the built-in one too', async () => {
    const { wrapper } = tab();

    await box(wrapper, 'builtin:purdue').setValue(true);
    await box(wrapper, 'office').setValue(true);

    const download = get(wrapper, 'bulk-download-layer-sets');

    expect(download.text()).toBe('Download selected');
    expect(download.find('.builder-icon--download').exists()).toBe(true);
    await download.trigger('click');
    await flushPromises();

    expect(save.mock.calls.map(([, name]) => name)).toEqual([
      'purdue-model.layers.yaml',
      'office.layers.yaml',
    ]);
    expect(get(wrapper, 'layer-sets-status').text()).toBe(
      'Downloaded 2 layer sets.',
    );
  });

  test('with nothing selected, the row’s buttons do nothing and say why', async () => {
    const { wrapper } = tab();

    for (const testid of [
      'bulk-download-layer-sets',
      'bulk-delete-layer-sets',
    ]) {
      const button = get(wrapper, testid);

      expect(button.attributes('aria-disabled'), testid).toBe('true');
      expect(button.attributes('aria-describedby'), testid).toBe(
        'bulk-count-layer-sets',
      );
      await button.trigger('click');
    }

    expect(save).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="builder-confirm"]').exists()).toBe(
      false,
    );
  });
});

describe('Delete selected on the Layers tab', () => {
  test('asks once, keeps the built-in set, and lists it in the summary', async () => {
    api.deleteLayerSet.mockResolvedValue(undefined);

    const { wrapper } = tab();

    await get(wrapper, 'bulk-all-layer-sets').setValue(true);

    const remove = get(wrapper, 'bulk-delete-layer-sets');

    expect(remove.classes()).toContain('builder-button--danger');
    expect(remove.find('.builder-icon--trash').exists()).toBe(true);
    await remove.trigger('click');

    const confirm = get(wrapper, 'builder-confirm');

    expect(
      document.getElementById(confirm.attributes('aria-labelledby'))
        .textContent,
    ).toBe('Delete 2 layer sets?');
    expect(confirm.text()).toContain(
      'Delete Plant zones and Office from the server? Diagrams keep their copies of the layers. You cannot undo this. Purdue Model stays because you cannot delete a built-in layer set.',
    );
    expect(get(wrapper, 'confirm-accept').text()).toBe('Delete 2 layer sets');

    await get(wrapper, 'confirm-accept').trigger('click');
    await flushPromises();

    expect(api.deleteLayerSet.mock.calls.map(([id]) => id)).toEqual([
      'plant',
      'office',
    ]);

    const summary = get(wrapper, 'bulk-summary');

    expect(summary.text()).toContain(
      '1 of 3 layer sets could not be deleted. The other 2 were deleted.',
    );
    expect(summary.text()).toContain(
      'Purdue Model: You cannot delete a built-in layer set.',
    );
    expect(document.activeElement).toBe(summary.element);
  });

  test('while it runs, a card’s Download is off and saves nothing', async () => {
    let finish;

    api.deleteLayerSet.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );

    const { wrapper } = tab();

    await box(wrapper, 'plant').setValue(true);
    await box(wrapper, 'office').setValue(true);
    await get(wrapper, 'bulk-delete-layer-sets').trigger('click');
    await get(wrapper, 'confirm-accept').trigger('click');
    await flushPromises();

    const download = card(wrapper, 'builtin:purdue').get(
      '[data-testid="layer-set-download"]',
    );

    expect(download.attributes('aria-disabled')).toBe('true');
    await download.trigger('click');
    await flushPromises();
    expect(save).not.toHaveBeenCalled();
    expect(get(wrapper, 'bulk-progress-layer-sets').text()).toContain(
      'Deleting',
    );

    finish();
    await flushPromises();
    expect(download.attributes('aria-disabled')).toBeUndefined();
  });

  test('with only built-in sets selected, it says why and asks nothing', async () => {
    const { wrapper } = tab();

    await box(wrapper, 'builtin:purdue').setValue(true);
    await get(wrapper, 'bulk-delete-layer-sets').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="builder-confirm"]').exists()).toBe(
      false,
    );
    expect(get(wrapper, 'bulk-summary').text()).toContain(
      'Purdue Model: You cannot delete a built-in layer set.',
    );
    expect(api.deleteLayerSet).not.toHaveBeenCalled();

    // Dismiss gives focus to Select all.
    await get(wrapper, 'bulk-summary-dismiss').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="bulk-summary"]').exists()).toBe(false);
    expect(document.activeElement).toBe(
      get(wrapper, 'bulk-all-layer-sets').element,
    );
  });

  test('one set alone asks as its own Delete does, also from the Delete key', async () => {
    const { wrapper } = tab();
    const plant = card(wrapper, 'plant');

    plant.element.focus();
    await plant.trigger('keydown', { key: ' ' });
    await plant.trigger('keydown', { key: 'Delete' });

    const confirm = get(wrapper, 'builder-confirm');

    expect(
      document.getElementById(confirm.attributes('aria-labelledby'))
        .textContent,
    ).toBe('Delete layer set?');
    expect(confirm.text()).toContain(
      'Delete Plant zones from the server? Diagrams keep their copies of its layers.',
    );
  });
});
