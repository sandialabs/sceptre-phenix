// @vitest-environment happy-dom
// The filter of the Drafts and Published Diagrams tabs, mounted: which
// cards show, the tab's count, what Select all and the batch actions act on,
// and the text each tab keeps.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';

const DRAFTS = [
  { id: 'd1', title: 'Pump station', createdBy: 'alice' },
  { id: 'd2', title: 'Water lab', createdBy: 'erin', lastModifiedBy: 'bob' },
  { id: 'd3', title: 'Grid substation', createdBy: 'bob' },
];
const PUBLISHED = [
  { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
  { id: 'p2', kind: 'Topology', target: 'plant', source: 'store' },
];

enableAutoUnmount(afterEach);

beforeEach(() => {
  sessionStorage.clear();
});

afterEach(() => {
  vi.useRealTimers();
});

function landing(props = {}) {
  return mount(BuilderDrafts, {
    attachTo: document.body,
    props: { drafts: DRAFTS, published: PUBLISHED, ...props },
  });
}

const get = (wrapper, testid) => wrapper.get(`[data-testid="${testid}"]`);
const cardIds = (wrapper, id) =>
  wrapper
    .findAll(`[data-testid="drafts-list-${id}"] > li`)
    .map((card) => card.attributes('data-testid').replace('draft-card-', ''));

describe('the filter of a tab of cards', () => {
  test('shows the cards that match by name or user, and the tab counts them', async () => {
    const wrapper = landing();
    const field = get(wrapper, 'drafts-filter-drafts');

    expect(wrapper.get('label[for="drafts-filter-drafts"]').text()).toBe(
      'Filter drafts',
    );
    expect(get(wrapper, 'drafts-tab-drafts').text()).toBe('Drafts (3)');

    await field.setValue('bob');
    expect(cardIds(wrapper, 'drafts')).toEqual(['d2', 'd3']);
    expect(get(wrapper, 'drafts-tab-drafts').text()).toBe('Drafts (2 of 3)');
    expect(get(wrapper, 'drafts-filter-drafts-count').text()).toBe(
      '2 of 3 drafts shown.',
    );
    expect(field.attributes('aria-describedby')).toBe(
      'drafts-filter-drafts-count',
    );

    await field.setValue('pmp');
    expect(cardIds(wrapper, 'drafts')).toEqual(['d1']);

    await field.setValue('zebra');
    expect(cardIds(wrapper, 'drafts')).toEqual([]);
    expect(get(wrapper, 'drafts-filter-none-drafts').text()).toBe(
      'No drafts match the filter.',
    );
    // The field stays, so the filter can be changed.
    expect(wrapper.find('[data-testid="drafts-filter-drafts"]').exists()).toBe(
      true,
    );
  });

  test('Select all and Delete selected act only on the cards shown', async () => {
    const wrapper = landing();

    await get(wrapper, 'bulk-all-drafts').trigger('click');
    expect(get(wrapper, 'bulk-count-drafts').text()).toBe('3 of 3 selected');

    // A card the filter hides leaves the selection.
    await get(wrapper, 'drafts-filter-drafts').setValue('station');
    expect(get(wrapper, 'bulk-count-drafts').text()).toBe('2 of 2 selected');

    await get(wrapper, 'drafts-filter-drafts').setValue('');
    expect(get(wrapper, 'bulk-count-drafts').text()).toBe('2 of 3 selected');

    await get(wrapper, 'drafts-filter-drafts').setValue('water');
    await get(wrapper, 'bulk-all-drafts').trigger('click');
    await get(wrapper, 'bulk-delete-drafts').trigger('click');
    await get(wrapper, 'confirm-accept').trigger('click');

    const [[tabId, items]] = wrapper.emitted('bulk-delete');

    expect(tabId).toBe('drafts');
    expect(items.map((item) => item.id)).toEqual(['d2']);
  });

  test('each tab keeps its own text for the session, and Escape clears it', async () => {
    const first = landing();

    await get(first, 'drafts-filter-drafts').setValue('pump');
    await get(first, 'drafts-tab-published').trigger('click');
    expect(get(first, 'drafts-filter-published').element.value).toBe('');
    await get(first, 'drafts-filter-published').setValue('plant');
    expect(cardIds(first, 'published')).toEqual(['p2']);
    expect(sessionStorage.getItem('phenix.builder.filter.drafts')).toBe('pump');
    first.unmount();

    // A new landing in the same session filters as the last one did.
    const second = landing();

    expect(get(second, 'drafts-filter-drafts').element.value).toBe('pump');
    expect(cardIds(second, 'drafts')).toEqual(['d1']);
    expect(get(second, 'drafts-filter-published').element.value).toBe('plant');

    const field = get(second, 'drafts-filter-drafts');
    const reached = [];
    const listen = (event) => reached.push(event.key);

    document.addEventListener('keydown', listen);

    // Escape clears the text and goes no further. In an empty field it
    // goes on to the page.
    await field.trigger('keydown', { key: 'Escape' });
    expect(reached).toEqual([]);
    expect(field.element.value).toBe('');
    expect(cardIds(second, 'drafts')).toEqual(['d1', 'd2', 'd3']);
    expect(sessionStorage.getItem('phenix.builder.filter.drafts')).toBeNull();

    await field.trigger('keydown', { key: 'Escape' });
    expect(reached).toEqual(['Escape']);
    document.removeEventListener('keydown', listen);
  });

  test('says how many cards show once typing stops', async () => {
    vi.useFakeTimers();

    const wrapper = landing();

    await get(wrapper, 'drafts-filter-drafts').setValue('s');
    await get(wrapper, 'drafts-filter-drafts').setValue('station');
    await vi.advanceTimersByTimeAsync(500);

    expect(wrapper.emitted('announce')).toEqual([
      ['2 of 3 drafts shown.', 'filter'],
    ]);
  });

  test('counts one card in the singular', async () => {
    vi.useFakeTimers();

    const wrapper = landing({ drafts: DRAFTS.slice(0, 1) });
    const field = get(wrapper, 'drafts-filter-drafts');

    await field.setValue('zebra');
    expect(get(wrapper, 'drafts-filter-drafts-count').text()).toBe(
      '0 of 1 draft shown.',
    );

    await field.setValue('');
    await vi.advanceTimersByTimeAsync(500);
    expect(wrapper.emitted('announce')).toEqual([['1 draft.', 'filter']]);
  });

  test('is not shown on a tab without cards', () => {
    const wrapper = landing({ published: [] });

    expect(
      wrapper.find('[data-testid="drafts-filter-published"]').exists(),
    ).toBe(false);
  });

  test('shows a card again when focus goes to it', async () => {
    const wrapper = landing();

    await get(wrapper, 'drafts-filter-drafts').setValue('water');

    expect(await wrapper.vm.focusDraft('d3')).toBe(true);
    expect(get(wrapper, 'drafts-filter-drafts').element.value).toBe('');
    expect(document.activeElement).toBe(get(wrapper, 'draft-open-d3').element);
  });
});
