// @vitest-environment happy-dom
// The Node Templates tab marks a card whose template names a drive image
// that the server does not have, and Show lists those of the library.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderTemplates from '@/components/builder/BuilderTemplates.vue';
import { createDocument } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { SHOW_MISSING_IMAGES } from '@/builder/templates.js';

import { libraryOf } from './fixtures.js';

const MISSING_LABEL = 'Templates with missing images';

function template(id, drives) {
  return {
    id,
    name: `Range ${id}`,
    device: {
      iconKey: 'router',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: {
          os_type: 'linux',
          drives: drives.map((image) => ({ image })),
        },
        network: { interfaces: [] },
      },
    },
  };
}

enableAutoUnmount(afterEach);

afterEach(() => {
  vi.restoreAllMocks();
});

// The tab, in the page, on a library of four templates, with the disk
// images `disks` (null: unknown).
function mountTab(disks, props = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = createDocument({ name: 'Plant' });
  store.templates = libraryOf({
    templates: [
      template('alfa', ['ubuntu.qc2']),
      template('bravo', ['gone.qc2']),
      template('charlie', []),
      template('delta', ['Ubuntu.qc2', 'old.qc2']),
    ],
  });
  store.disks = disks;
  store.fetchDisks = vi.fn();

  const wrapper = mount(BuilderTemplates, {
    props,
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
}

const card = (wrapper, id) =>
  wrapper.get(`[data-testid="template-card-${id}"]`);
const mark = (wrapper, id) =>
  wrapper.find(`[data-testid="template-missing-images-${id}"]`);
const show = (wrapper) => wrapper.get('[data-testid="templates-show"]');
const optionLabels = (wrapper) =>
  show(wrapper)
    .findAll('option')
    .map((option) => option.text());
const cardIds = (wrapper) =>
  wrapper
    .findAll('[data-testid="templates-list"] > li')
    .map((item) =>
      item.attributes('data-testid').replace('template-card-', ''),
    );

// The text of the elements an attribute names, by their ids.
function textOfIds(ids) {
  return (ids || '')
    .split(' ')
    .filter(Boolean)
    .map((id) => document.getElementById(id)?.textContent.trim())
    .join(' ');
}

describe('a card whose template names a missing image', () => {
  test('says which images, with a sign, and the card’s description says it', () => {
    const { wrapper, store } = mountTab(['ubuntu.qc2']);

    expect(store.fetchDisks).toHaveBeenCalledTimes(1);
    expect(mark(wrapper, 'alfa').exists()).toBe(false);
    expect(mark(wrapper, 'charlie').exists()).toBe(false);
    expect(card(wrapper, 'alfa').attributes('aria-describedby')).toBe(
      undefined,
    );

    expect(mark(wrapper, 'bravo').text()).toBe(
      'The server has no disk image named "gone.qc2".',
    );
    expect(
      mark(wrapper, 'bravo').find('.builder-icon, svg, img').exists(),
    ).toBe(true);
    // Case counts, as in the diagram's drive image check.
    expect(mark(wrapper, 'delta').text()).toBe(
      'The server has no disk images named "Ubuntu.qc2" and "old.qc2".',
    );
    expect(
      textOfIds(card(wrapper, 'bravo').attributes('aria-describedby')),
    ).toBe('The server has no disk image named "gone.qc2".');
  });

  test('is not marked while the disk images are unknown', () => {
    const { wrapper, store } = mountTab(null);

    expect(store.fetchDisks).toHaveBeenCalledTimes(1);
    expect(
      wrapper.findAll('[data-testid^="template-missing-images-"]'),
    ).toHaveLength(0);
    expect(card(wrapper, 'bravo').attributes('aria-describedby')).toBe(
      undefined,
    );
  });

  test('loses its mark when the server lists the image', async () => {
    const { wrapper, store } = mountTab(['ubuntu.qc2']);

    store.disks = ['ubuntu.qc2', 'gone.qc2'];
    await flushPromises();
    expect(mark(wrapper, 'bravo').exists()).toBe(false);
  });
});

describe('Show: Templates with missing images', () => {
  test('is offered after All library templates while the images are known', () => {
    const known = mountTab(['ubuntu.qc2']).wrapper;

    expect(optionLabels(known).slice(0, 2)).toEqual([
      'All library templates',
      MISSING_LABEL,
    ]);

    const unknown = mountTab(null).wrapper;

    expect(optionLabels(unknown)).not.toContain(MISSING_LABEL);
  });

  test('lists the templates with missing images, says why, and the row names it', async () => {
    const { wrapper } = mountTab(['ubuntu.qc2']);

    await show(wrapper).setValue(SHOW_MISSING_IMAGES);
    expect(cardIds(wrapper)).toEqual(['bravo', 'delta']);
    expect(wrapper.get('[data-testid="templates-missing-note"]').text()).toBe(
      'These templates name a drive image that the server does not have. To choose another image, select Edit.',
    );
    expect(wrapper.get('[data-testid="bulk-count-templates"]').text()).toBe(
      '0 of 2 selected',
    );
    expect(
      wrapper
        .get('[data-testid="bulk-bar-templates"]')
        .attributes('aria-label'),
    ).toBe(`Bulk actions: ${MISSING_LABEL}`);
  });

  test('says so when every image is on the server', async () => {
    const { wrapper, store } = mountTab(['ubuntu.qc2']);

    await show(wrapper).setValue(SHOW_MISSING_IMAGES);
    store.disks = ['ubuntu.qc2', 'gone.qc2', 'Ubuntu.qc2', 'old.qc2'];
    await flushPromises();
    expect(wrapper.find('[data-testid="templates-list"]').exists()).toBe(false);
    expect(
      wrapper.find('[data-testid="templates-missing-note"]').exists(),
    ).toBe(false);
    expect(wrapper.get('[data-testid="templates-empty"]').text()).toBe(
      'The server has the disk images of every library template.',
    );
  });

  test('stays chosen, empty and announced, while the images are unknown', async () => {
    const { wrapper, store } = mountTab(['ubuntu.qc2']);
    const lostLabel = `${MISSING_LABEL} (disk images unknown)`;

    await show(wrapper).setValue(SHOW_MISSING_IMAGES);
    const seq = store.announcementSeq;

    store.disks = null;
    await flushPromises();
    expect(show(wrapper).element.value).toBe(SHOW_MISSING_IMAGES);
    expect(optionLabels(wrapper)).toContain(lostLabel);
    expect(
      show(wrapper)
        .findAll('option')
        .find((option) => option.text() === lostLabel)
        .attributes('disabled'),
    ).toBeDefined();
    expect(cardIds(wrapper)).toEqual([]);
    expect(wrapper.get('[data-testid="templates-empty"]').text()).toBe(
      'The server’s disk images are unknown. The Builder cannot list the templates with missing images. To see templates, choose another list in Show.',
    );
    expect(store.announcementSeq).toBe(seq + 1);
    expect(store.announcement).toBe(
      'The server’s disk images are unknown. The Builder cannot list the templates with missing images.',
    );

    // A failed read again says nothing more.
    store.disks = null;
    await flushPromises();
    expect(store.announcementSeq).toBe(seq + 1);

    store.disks = ['ubuntu.qc2'];
    await flushPromises();
    expect(show(wrapper).element.value).toBe(SHOW_MISSING_IMAGES);
    expect(cardIds(wrapper)).toEqual(['bravo', 'delta']);
    expect(optionLabels(wrapper)).toContain(MISSING_LABEL);
    expect(optionLabels(wrapper)).not.toContain(lostLabel);
  });

  test('is not offered once another list is chosen while the images are unknown', async () => {
    const { wrapper, store } = mountTab(['ubuntu.qc2']);

    await show(wrapper).setValue(SHOW_MISSING_IMAGES);
    store.disks = null;
    await flushPromises();
    await show(wrapper).setValue('');
    expect(cardIds(wrapper)).toEqual(['alfa', 'bravo', 'charlie', 'delta']);
    expect(
      optionLabels(wrapper).filter((label) => label.startsWith(MISSING_LABEL)),
    ).toEqual([]);
  });
});

describe('the disk images', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  test('are read when the tab opens, and again when the page shows after 30 seconds', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-10-26T12:00:00Z'));

    const { wrapper, store } = mountTab(['ubuntu.qc2']);
    const shows = () => document.dispatchEvent(new Event('visibilitychange'));

    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    expect(store.fetchDisks).toHaveBeenCalledTimes(1);

    vi.setSystemTime(new Date('2026-10-26T12:00:29Z'));
    shows();
    expect(store.fetchDisks).toHaveBeenCalledTimes(1);

    vi.setSystemTime(new Date('2026-10-26T12:00:30Z'));
    shows();
    expect(store.fetchDisks).toHaveBeenCalledTimes(2);

    // A closed tab reads nothing.
    wrapper.unmount();
    vi.setSystemTime(new Date('2026-10-26T12:05:00Z'));
    shows();
    expect(store.fetchDisks).toHaveBeenCalledTimes(2);
  });

  test('are read when a hidden tab shows, unless its last read is new', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-10-26T12:00:00Z'));

    const { wrapper, store } = mountTab(['ubuntu.qc2'], { active: false });

    expect(store.fetchDisks).not.toHaveBeenCalled();

    await wrapper.setProps({ active: true });
    expect(store.fetchDisks).toHaveBeenCalledTimes(1);

    await wrapper.setProps({ active: false });
    vi.setSystemTime(new Date('2026-10-26T12:00:10Z'));
    await wrapper.setProps({ active: true });
    expect(store.fetchDisks).toHaveBeenCalledTimes(1);

    // A hidden tab does not read when the page shows.
    await wrapper.setProps({ active: false });
    vi.setSystemTime(new Date('2026-10-26T12:01:00Z'));
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    expect(store.fetchDisks).toHaveBeenCalledTimes(1);

    await wrapper.setProps({ active: true });
    expect(store.fetchDisks).toHaveBeenCalledTimes(2);
  });
});
