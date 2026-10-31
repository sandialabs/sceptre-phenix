// @vitest-environment happy-dom
// The merge dialog, mounted: two radio buttons per clashing field, neither
// chosen, and Save merged unavailable until each has one.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import MergeDialog from '@/components/builder/dialogs/MergeDialog.vue';
import { addNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

function renamed(doc, id, name) {
  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === id
        ? {
            ...node,
            label: name,
            device: {
              ...node.device,
              hostname: name,
              spec: {
                ...node.device.spec,
                general: { ...node.device.spec.general, hostname: name },
              },
            },
          }
        : node,
    ),
  };
}

// The dialog over `mine` and `theirs`, both changed from the sample bob
// saved, once it has opened (its onMounted has run).
async function open(change) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);
  const { doc: base, alpha, bravo } = sampleDocument();
  const { mine, theirs } = change(base, alpha, bravo);

  store.doc = mine;
  store.merge = {
    status: 'review',
    from: 'bob',
    reason: '',
    clashes: 1,
    base,
    server: { document: theirs },
  };

  const wrapper = mount(MergeDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await flushPromises();

  return { wrapper, store };
}

// Both devices renamed differently: two clashes.
const twoClashes = (base, alpha, bravo) => ({
  mine: renamed(renamed(base, alpha.id, 'mine'), bravo.id, 'bravo-mine'),
  theirs: renamed(renamed(base, alpha.id, 'theirs'), bravo.id, 'bravo-theirs'),
});

const save = (wrapper) => wrapper.get('[data-testid="merge-save"]');
const countOf = (wrapper) => wrapper.get('[data-testid="merge-count"]').text();

describe('the merge dialog', () => {
  test('lists each clash with Keep mine and Keep theirs, none chosen', async () => {
    const { wrapper } = await open(twoClashes);
    const legends = wrapper.findAll('legend').map((legend) => legend.text());
    const labels = wrapper
      .findAll('[data-testid="merge-clash-0"] label')
      .map((label) => label.text());

    expect(wrapper.get('h2').text()).toBe('Merge changes from bob');
    expect(legends).toEqual(['alpha name', 'bravo name']);
    expect(wrapper.text()).toContain('You and they both changed alpha name.');
    expect(labels).toEqual(['Keep mine: mine', 'Keep theirs: theirs']);
    expect(
      wrapper
        .findAll('input[type="radio"]')
        .some((radio) => radio.element.checked),
    ).toBe(false);
    expect(countOf(wrapper)).toBe('0 of 2 chosen');
    expect(save(wrapper).attributes('aria-disabled')).toBe('true');
    expect(wrapper.get('[data-testid="merge-all-mine"]').text()).toBe(
      'Keep all mine',
    );
    expect(wrapper.get('[data-testid="merge-all-theirs"]').text()).toBe(
      'Keep all theirs',
    );
  });

  test('a deleted device offers to delete it or keep it with the other changes', async () => {
    const { wrapper } = await open((base, _, bravo) => ({
      mine: {
        ...base,
        nodes: base.nodes.filter((node) => node.id !== bravo.id),
      },
      theirs: renamed(base, bravo.id, 'bravo-2'),
    }));
    const clash = wrapper.get('[data-testid="merge-clash-0"]');

    expect(clash.get('legend').text()).toBe('bravo');
    expect(clash.text()).toContain('You deleted bravo. They changed its name.');
    expect(clash.findAll('label').map((label) => label.text())).toEqual([
      'Keep mine: delete bravo',
      'Keep theirs: keep bravo with their changes',
    ]);
  });

  test('with nothing to choose, lists why the merged diagram is refused, offers only Cancel, and Save merged is unavailable', async () => {
    // Both add a device charlie: no field clashes, but the merged diagram
    // has two devices of one hostname, which the strict check refuses.
    const charlie = (doc) =>
      addNode(doc, { kind: 'device', hostname: 'charlie' }).doc;
    const { wrapper } = await open((base) => ({
      mine: charlie(base),
      theirs: charlie(base),
    }));

    expect(wrapper.get('#merge-dialog-intro').text()).toMatch(
      /You and bob made no changes that clash\. There is nothing to\s+choose\. But the Builder cannot save the merged diagram as it is\./,
    );
    expect(wrapper.find('[data-testid="merge-all-mine"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="merge-count"]').exists()).toBe(false);
    expect(wrapper.get('[data-testid="merge-error"]').text()).toBe(
      'The Builder cannot save the merged diagram because of the problems listed below. After Cancel, you can save your history as a new draft or discard it.',
    );
    expect(wrapper.text()).not.toContain('Make different choices');
    expect(wrapper.get('[data-testid="merge-issues"]').text()).toContain(
      'duplicate hostname "charlie"',
    );
    expect(save(wrapper).attributes('aria-disabled')).toBe('true');
    expect(save(wrapper).attributes('aria-describedby')).toBe('merge-error');
  });

  test('with a clash, nothing is refused as it opens, Save merged is described by the count, and focus is on the first choice', async () => {
    const { wrapper } = await open((base, alpha) => ({
      mine: renamed(base, alpha.id, 'mine'),
      theirs: renamed(base, alpha.id, 'theirs'),
    }));

    expect(wrapper.find('[data-testid="merge-issues"]').exists()).toBe(false);
    expect(save(wrapper).attributes('aria-describedby')).toBe('merge-count');
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="merge-clash-0-mine"]').element,
    );
  });
});

describe('choosing in the merge dialog', () => {
  test('each choice counts, and Save merged is available once every field has one', async () => {
    const { wrapper } = await open(twoClashes);

    await wrapper.get('[data-testid="merge-clash-0-theirs"]').setValue();
    expect(countOf(wrapper)).toBe('1 of 2 chosen');
    expect(save(wrapper).attributes('aria-disabled')).toBe('true');

    await wrapper.get('[data-testid="merge-all-mine"]').trigger('click');
    expect(countOf(wrapper)).toBe('2 of 2 chosen');
    expect(
      wrapper.get('[data-testid="merge-clash-0-mine"]').element.checked,
    ).toBe(true);
    expect(save(wrapper).attributes('aria-disabled')).toBeUndefined();
  });

  test('Save merged with a field unchosen says so and moves focus to it, and saves nothing', async () => {
    const { wrapper, store } = await open(twoClashes);
    const saveChoices = vi.spyOn(store, 'saveMergeChoices');

    await wrapper.get('[data-testid="merge-clash-0-mine"]').setValue();
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(wrapper.get('[data-testid="merge-error"]').text()).toBe(
      'Choose the version to keep for each field. You chose 1 of 2.',
    );
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="merge-clash-1-mine"]').element,
    );
    expect(saveChoices).not.toHaveBeenCalled();
  });

  test('Save merged with every field chosen saves those choices and reports the merge', async () => {
    const { wrapper, store } = await open(twoClashes);
    const saveChoices = vi
      .spyOn(store, 'saveMergeChoices')
      .mockResolvedValue({ saved: true });

    await wrapper.get('[data-testid="merge-clash-0-mine"]').setValue();
    await wrapper.get('[data-testid="merge-clash-1-theirs"]').setValue();
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(Object.values(saveChoices.mock.calls[0][0]).sort()).toEqual([
      'mine',
      'theirs',
    ]);
    expect(wrapper.emitted('merged')).toHaveLength(1);
    expect(wrapper.find('[data-testid="merge-error"]').exists()).toBe(false);
  });
});
