// @vitest-environment happy-dom
// The drafts landing, mounted and used from the keyboard and the mouse: its
// tabs, the cards' keys and selection, Delete and what follows it.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';

// No times, so each card is named by its title alone.
const DRAFTS = [
  { id: 'd1', title: 'Alpha', createdBy: 'alice' },
  { id: 'd2', title: 'Bravo', createdBy: 'bob' },
  { id: 'd3', title: 'Charlie', createdBy: 'erin' },
];
const PUBLISHED = [
  { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
  { id: 'p2', kind: 'Topology', target: 'plant', source: 'store' },
];
// The five tabs every user has: the view adds the last three.
const TABS = ['drafts', 'published', 'templates', 'layers', 'icons'];

enableAutoUnmount(afterEach);

function landing(props = {}) {
  return mount(BuilderDrafts, {
    attachTo: document.body,
    props: {
      drafts: DRAFTS,
      published: PUBLISHED,
      templates: { count: 2 },
      layers: { count: 1 },
      icons: { count: 3 },
      ...props,
    },
    slots: {
      templates: '<p>The templates</p>',
      layers: '<p>The layers</p>',
      icons: '<p>The icons</p>',
    },
  });
}

const get = (wrapper, testid) => wrapper.get(`[data-testid="${testid}"]`);
const tab = (wrapper, id) => get(wrapper, `drafts-tab-${id}`);
const cardsOf = (wrapper, id) =>
  wrapper.findAll(`[data-testid="drafts-list-${id}"] > li`);
const countOf = (wrapper, id) => get(wrapper, `bulk-count-${id}`).text();
const confirmOf = (wrapper) => wrapper.find('[data-testid="builder-confirm"]');
const confirmName = (wrapper) =>
  document.getElementById(confirmOf(wrapper).attributes('aria-labelledby'))
    .textContent;
const announced = (wrapper) =>
  (wrapper.emitted('announce') || []).map(([text]) => text);

describe('the collection tabs', () => {
  test('move with the arrow keys, and wrap, and a click shows a tab', async () => {
    const wrapper = landing();
    const tabs = TABS.map((id) => tab(wrapper, id));
    const focused = () =>
      tabs.find((each) => each.element === document.activeElement);

    function expectActive(index) {
      for (const [i, each] of tabs.entries()) {
        expect(each.attributes('aria-selected'), TABS[i]).toBe(
          String(i === index),
        );
        expect(each.attributes('tabindex'), TABS[i]).toBe(
          i === index ? '0' : '-1',
        );
      }
      expect(
        wrapper
          .findAll('[role="tabpanel"]')
          .filter((panel) => panel.attributes('hidden') === undefined)
          .map((panel) => panel.attributes('id')),
      ).toEqual([`panel-${TABS[index]}`]);
      expect(document.activeElement).toBe(tabs[index].element);
    }

    const tablist = wrapper.get('[role="tablist"]');

    expect(tablist.attributes('aria-label')).toBe('Builder collections');
    expect(tablist.findAll('[role="tab"]')).toHaveLength(5);

    tabs[0].element.focus();
    expect(tabs[0].attributes('aria-selected')).toBe('true');

    for (const index of [1, 2, 3, 4, 0]) {
      await focused().trigger('keydown', { key: 'ArrowRight' });
      expectActive(index);
    }
    for (const index of [4, 3]) {
      await focused().trigger('keydown', { key: 'ArrowLeft' });
      expectActive(index);
    }

    // A press focuses the tab before its click, as a browser does.
    tabs[0].element.focus();
    await tabs[0].trigger('click');
    expectActive(0);
  });
});

describe('the cards of a tab', () => {
  test('are one Tab stop that Home and End move, and Space and Escape select and clear', async () => {
    const wrapper = landing();
    const cards = cardsOf(wrapper, 'drafts');
    const stops = () =>
      cardsOf(wrapper, 'drafts')
        .filter((card) => card.attributes('tabindex') === '0')
        .map((card) => card.element);

    expect(stops()).toEqual([cards[0].element]);

    cards[0].element.focus();
    await cards[0].trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(cards[2].element);
    expect(stops()).toEqual([cards[2].element]);

    await cards[2].trigger('keydown', { key: ' ' });
    expect(get(wrapper, 'card-select-d3').element.checked).toBe(true);
    expect(cards[2].classes()).toContain('is-selected');
    expect(wrapper.emitted('announce').at(-1)).toEqual([
      '1 of 3 selected.',
      'selection',
    ]);

    await cards[2].trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(cards[0].element);

    await cards[0].trigger('keydown', { key: 'Escape' });
    expect(countOf(wrapper, 'drafts')).toBe('0 of 3 selected');
    expect(announced(wrapper).at(-1)).toBe('0 of 3 selected.');
  });

  test('a checkbox selects its card, Shift a range, and Select all every card, then none; the selection outlives a change of tab', async () => {
    const wrapper = landing();

    await get(wrapper, 'card-select-d1').setValue(true);
    expect(countOf(wrapper, 'drafts')).toBe('1 of 3 selected');
    expect(wrapper.get('#card-state-drafts-0').text()).toBe('selected');

    await get(wrapper, 'card-select-d3').trigger('click', { shiftKey: true });
    expect(countOf(wrapper, 'drafts')).toBe('3 of 3 selected');
    expect(get(wrapper, 'card-select-d2').element.checked).toBe(true);

    await get(wrapper, 'bulk-all-drafts').setValue(false);
    expect(countOf(wrapper, 'drafts')).toBe('0 of 3 selected');
    expect(announced(wrapper).at(-1)).toBe('0 of 3 selected.');
    await get(wrapper, 'bulk-all-drafts').setValue(true);
    expect(announced(wrapper).at(-1)).toBe('3 of 3 selected.');

    await tab(wrapper, 'published').trigger('click');
    expect(countOf(wrapper, 'published')).toBe('0 of 2 selected');
    await tab(wrapper, 'drafts').trigger('click');
    expect(countOf(wrapper, 'drafts')).toBe('3 of 3 selected');
  });
});

describe('deleting from the landing', () => {
  test('a draft’s Delete asks first, and focus moves to the card in its place once it is gone', async () => {
    const wrapper = landing();
    const remove = get(wrapper, 'draft-delete-d2');

    remove.element.focus();
    await remove.trigger('click');
    expect(confirmName(wrapper)).toBe('Delete draft Bravo?');

    await get(wrapper, 'confirm-cancel').trigger('click');
    expect(confirmOf(wrapper).exists()).toBe(false);
    expect(wrapper.emitted('delete')).toBeUndefined();
    expect(document.activeElement).toBe(remove.element);

    await remove.trigger('click');
    await get(wrapper, 'confirm-accept').trigger('click');
    expect(wrapper.emitted('delete')).toEqual([[DRAFTS[1], 'Bravo']]);

    await wrapper.setProps({ drafts: [DRAFTS[0], DRAFTS[2]] });
    await flushPromises();
    expect(document.activeElement).toBe(get(wrapper, 'draft-open-d3').element);
  });

  test('a published topology’s Delete asks first, then asks the view to delete it', async () => {
    const wrapper = landing();

    await tab(wrapper, 'published').trigger('click');
    await get(wrapper, 'published-delete-p1').trigger('click');
    expect(confirmName(wrapper)).toBe('Delete topology lab?');

    await get(wrapper, 'confirm-accept').trigger('click');
    expect(wrapper.emitted('delete-published')).toEqual([
      [PUBLISHED[0], 'lab'],
    ]);
  });

  test('Delete selected asks once for the batch: Cancel keeps the selection, and Delete hands the batch to the view', async () => {
    const wrapper = landing();

    await get(wrapper, 'card-select-d1').setValue(true);
    await get(wrapper, 'card-select-d2').setValue(true);
    await get(wrapper, 'bulk-delete-drafts').trigger('click');
    expect(confirmName(wrapper)).toBe('Delete 2 drafts?');

    await get(wrapper, 'confirm-cancel').trigger('click');
    expect(confirmOf(wrapper).exists()).toBe(false);
    expect(countOf(wrapper, 'drafts')).toBe('2 of 3 selected');
    expect(wrapper.emitted('bulk-delete')).toBeUndefined();

    // Delete on a card asks the same question.
    await cardsOf(wrapper, 'drafts')[0].trigger('keydown', { key: 'Delete' });
    expect(confirmName(wrapper)).toBe('Delete 2 drafts?');
    await get(wrapper, 'confirm-accept').trigger('click');
    expect(wrapper.emitted('bulk-delete')).toEqual([
      ['drafts', [DRAFTS[0], DRAFTS[1]], { d1: 'Alpha', d2: 'Bravo' }],
    ]);
  });

  test('Delete selected with one card asks as its own Delete does, and topologies are asked about together', async () => {
    const wrapper = landing();

    await get(wrapper, 'card-select-d3').setValue(true);
    await get(wrapper, 'bulk-delete-drafts').trigger('click');
    expect(confirmName(wrapper)).toBe('Delete draft Charlie?');
    await get(wrapper, 'confirm-cancel').trigger('click');

    await tab(wrapper, 'published').trigger('click');
    await get(wrapper, 'bulk-all-published').setValue(true);
    await get(wrapper, 'bulk-delete-published').trigger('click');
    expect(confirmName(wrapper)).toBe('Delete 2 topologies?');
    await get(wrapper, 'confirm-accept').trigger('click');
    expect(wrapper.emitted('bulk-delete-published')).toEqual([
      [PUBLISHED, { p1: 'lab', p2: 'plant' }],
    ]);
  });

  test('what a batch left undone shows on its tab with focus, and Dismiss moves focus to Select all', async () => {
    const wrapper = landing();

    await wrapper.setProps({
      bulkResult: {
        tab: 'published',
        heading:
          '1 of 2 topologies could not be deleted. The other 1 was deleted.',
        items: [{ key: 'p2', name: 'plant', reason: 'It is in use.' }],
      },
    });
    await flushPromises();

    const summary = get(wrapper, 'bulk-summary');

    expect(tab(wrapper, 'published').attributes('aria-selected')).toBe('true');
    expect(
      wrapper.get('#panel-published').element.contains(summary.element),
    ).toBe(true);
    expect(summary.findAll('li').map((item) => item.text())).toEqual([
      'plant: It is in use.',
    ]);
    expect(document.activeElement).toBe(summary.element);

    await get(wrapper, 'bulk-summary-dismiss').trigger('click');
    await flushPromises();
    expect(wrapper.emitted('dismiss-bulk-result')).toHaveLength(1);
    expect(document.activeElement).toBe(
      get(wrapper, 'bulk-all-published').element,
    );
  });
});

describe('Duplicate on a draft card', () => {
  test('asks the view to copy the draft, and says so while it does', async () => {
    const wrapper = landing({ damaged: [{ id: 'x1', damaged: true }] });
    const button = get(wrapper, 'draft-duplicate-d1');

    expect(button.attributes('aria-label')).toBe('Duplicate Alpha');
    await button.trigger('click');
    expect(wrapper.emitted('duplicate')).toEqual([[DRAFTS[0]]]);

    // A damaged draft cannot be read, so it has no Duplicate.
    expect(wrapper.find('[data-testid="draft-duplicate-x1"]').exists()).toBe(
      false,
    );
    // Nor has a published diagram.
    expect(wrapper.find('[data-testid="draft-duplicate-p1"]').exists()).toBe(
      false,
    );

    await wrapper.setProps({ busy: true, duplicating: 'd1' });
    expect(button.attributes('aria-label')).toBe('Duplicating Alpha');
    expect(button.attributes('aria-busy')).toBe('true');
    expect(button.attributes('aria-disabled')).toBe('true');
    expect(button.find('.builder-toolbar__spinner').exists()).toBe(true);

    // While busy, a second press does nothing.
    await button.trigger('click');
    expect(wrapper.emitted('duplicate')).toHaveLength(1);
  });
});
