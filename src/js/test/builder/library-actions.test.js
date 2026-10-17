// @vitest-environment happy-dom
// The Node Templates tab's Show and the palette's library sections, mounted
// and used: showList and the Show field, and a section header's collapse.

import { afterEach, describe, expect, test } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderPalette from '@/components/builder/BuilderPalette.vue';
import BuilderTemplates from '@/components/builder/BuilderTemplates.vue';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { createDocument } from '@/builder/model.js';
import { PALETTE_STORAGE_KEY } from '@/builder/paletteSections.js';
import { useBuilderStore } from '@/builder/store.js';
import { SHOW_BUILTIN } from '@/builder/templates.js';

import { libraryOf } from './fixtures.js';

function template(id, init = {}) {
  return {
    id,
    name: id.toUpperCase(),
    device: {
      iconKey: 'router',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: `${id}.qc2` }] },
        network: { interfaces: [] },
      },
    },
    ...init,
  };
}

enableAutoUnmount(afterEach);

afterEach(() => {
  window.localStorage.clear();
});

// Mounts a component in the page with a Builder store that `prepare` sets up.
function mountWith(component, prepare) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = createDocument({ name: 'Plant' });
  prepare(store);

  const wrapper = mount(component, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
}

describe('the Node Templates tab’s Show', () => {
  const library = () =>
    libraryOf({
      templates: [template('plc'), template('hmi'), template('rtu')],
      collections: [
        { id: 'c1', name: 'Plant floor', templateIds: ['rtu', 'plc'] },
        { id: 'c2', name: 'Office', templateIds: ['plc'] },
      ],
    });
  const cardIds = (wrapper) =>
    wrapper
      .findAll('[data-testid="templates-list"] > li')
      .map((card) => card.attributes('data-testid'));

  // The command palette shows a collection through showList (see
  // openTemplateLibrary in Builder.vue).
  test('showList shows a collection and focuses Show, and keeps the list for an unknown value', async () => {
    const { wrapper } = mountWith(BuilderTemplates, (store) => {
      store.templates = library();
    });
    const show = wrapper.get('#templates-show');

    expect(await wrapper.vm.showList('c2')).toBe(true);
    expect(show.element.value).toBe('c2');
    expect(document.activeElement).toBe(show.element);
    expect(cardIds(wrapper)).toEqual(['template-card-plc']);

    show.element.blur();

    expect(await wrapper.vm.showList('gone')).toBe(false);
    expect(await wrapper.vm.showList('preloaded:c2')).toBe(false);
    expect(show.element.value).toBe('c2');
    expect(document.activeElement).not.toBe(show.element);

    expect(await wrapper.vm.showList(SHOW_BUILTIN)).toBe(true);
    expect(show.element.value).toBe(SHOW_BUILTIN);
    expect(cardIds(wrapper)).toEqual(
      BUILTIN_TEMPLATES.map((item) => `template-card-builtin-${item.id}`),
    );

    expect(await wrapper.vm.showList('')).toBe(true);
    expect(show.element.value).toBe('');
    expect(cardIds(wrapper)).toEqual([
      'template-card-plc',
      'template-card-hmi',
      'template-card-rtu',
    ]);
  });

  test('the user’s choice in Show lists that collection, in the order its templates joined it', async () => {
    const { wrapper } = mountWith(BuilderTemplates, (store) => {
      store.templates = library();
    });

    await wrapper.get('#templates-show').setValue('c1');

    expect(wrapper.get('[data-testid="collection-block"] h2').text()).toBe(
      'Plant floor',
    );
    expect(cardIds(wrapper)).toEqual([
      'template-card-rtu',
      'template-card-plc',
    ]);
  });
});

describe('the palette’s library sections', () => {
  test('a section header collapses its list and the palette remembers it, and a second press shows it again', async () => {
    const { wrapper } = mountWith(BuilderPalette, (store) => {
      store.templates = libraryOf({
        templates: [template('plc'), template('hmi'), template('rtu')],
        collections: [
          { id: 'plant', name: 'Plant', templateIds: ['plc', 'hmi'] },
          { id: 'one', name: 'One', templateIds: ['hmi'] },
        ],
      });
    });
    const header = wrapper.get('[data-testid="palette-section-library:plant"]');
    const list = wrapper.get('[data-testid="palette-templates-library:plant"]');
    const other = wrapper.get('[data-testid="palette-templates-library:one"]');

    expect(header.attributes('aria-expanded')).toBe('true');
    expect(list.isVisible()).toBe(true);

    await header.trigger('click');

    expect(header.attributes('aria-expanded')).toBe('false');
    expect(list.element.style.display).toBe('none');
    expect(other.isVisible()).toBe(true);
    expect(
      JSON.parse(window.localStorage.getItem(PALETTE_STORAGE_KEY)),
    ).toEqual({ collapsed: ['library:plant'] });

    await header.trigger('click');

    expect(header.attributes('aria-expanded')).toBe('true');
    expect(list.isVisible()).toBe(true);
    expect(window.localStorage.getItem(PALETTE_STORAGE_KEY)).toBeNull();
  });
});
