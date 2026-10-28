// @vitest-environment happy-dom
// The drafts landing's tab strip scrolls sideways in a narrow window. The
// shown tab is scrolled into view when the page mounts and when it changes.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';

enableAutoUnmount(afterEach);

let scrolled;

beforeEach(() => {
  scrolled = vi
    .spyOn(HTMLElement.prototype, 'scrollIntoView')
    .mockImplementation(() => {});
});

afterEach(() => {
  scrolled.mockRestore();
});

function landing() {
  return mount(BuilderDrafts, {
    attachTo: document.body,
    props: {
      drafts: [{ id: 'd1', title: 'Alpha', createdBy: 'alice' }],
      published: [],
      templates: { count: 2 },
      layers: { count: 1 },
      icons: { count: 3 },
    },
    slots: {
      templates: '<p>The templates</p>',
      layers: '<p>The layers</p>',
      icons: '<p>The icons</p>',
    },
  });
}

const tab = (wrapper, id) => wrapper.get(`[data-testid="drafts-tab-${id}"]`);

// The ids of the tabs scrollIntoView was called on, in order.
const scrolledTabs = () =>
  scrolled.mock.instances
    .map((element) => element.dataset.testid)
    .filter((testid) => testid?.startsWith('drafts-tab-'))
    .map((testid) => testid.slice('drafts-tab-'.length));

const NEAREST = { block: 'nearest', inline: 'nearest' };

describe('the shown tab', () => {
  test('is scrolled into view on mount', () => {
    landing();

    expect(scrolledTabs()).toEqual(['drafts']);
    expect(scrolled).toHaveBeenCalledWith(NEAREST);
  });

  test('is scrolled into view when a click or an arrow key shows it', async () => {
    const wrapper = landing();
    scrolled.mockClear();

    await tab(wrapper, 'icons').trigger('click');
    expect(scrolledTabs()).toEqual(['icons']);

    tab(wrapper, 'icons').element.focus();
    await tab(wrapper, 'icons').trigger('keydown', { key: 'ArrowRight' });
    expect(scrolledTabs()).toEqual(['icons', 'drafts']);
    expect(scrolled).toHaveBeenLastCalledWith(NEAREST);
  });

  test('is scrolled into view when the command palette shows it', async () => {
    const wrapper = landing();
    scrolled.mockClear();

    wrapper.vm.showTab('layers');
    await wrapper.vm.$nextTick();

    expect(scrolledTabs()).toEqual(['layers']);
  });
});
