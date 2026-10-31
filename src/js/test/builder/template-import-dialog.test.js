// @vitest-environment happy-dom
// Import templates: a template file read into the library as a new
// collection, mounted and driven as a user would.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import YAML from 'js-yaml';

import TemplateImportDialog from '@/components/builder/dialogs/TemplateImportDialog.vue';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons } from '@/builder/icons.js';
import { useBuilderStore } from '@/builder/store.js';
import { TEMPLATE_FILE_SCHEMA_URI } from '@/builder/templateFile.js';

import { libraryOf } from './fixtures.js';
import { base64Of, png } from './png.js';

// A template file as Download writes it: one collection, no ids.
const [server] = BUILTIN_TEMPLATES;
const TEMPLATE = {
  name: server.name,
  description: server.description,
  device: server.device,
};
const FILE = {
  $schema: TEMPLATE_FILE_SCHEMA_URI,
  name: 'Plant',
  templates: [TEMPLATE],
};

enableAutoUnmount(afterEach);

beforeEach(() => {
  vi.restoreAllMocks();
  iconLibrary.state.index = indexIcons([]);
});

// Opens the dialog in the page over a library of `collections`. The library
// takes the templates through `create`.
function open({ collections = [], create = async () => {} } = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.templates = libraryOf({ collections });
  vi.spyOn(store, 'createLibraryTemplates').mockImplementation(create);

  const wrapper = mount(TemplateImportDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
}

const byId = (wrapper, testId) => wrapper.find(`[data-testid="${testId}"]`);
const field = (wrapper) => byId(wrapper, 'template-import-file');

// Chooses `name` with `text` in the file field, as the file picker does.
async function chooseFile(wrapper, name, text) {
  Object.defineProperty(field(wrapper).element, 'files', {
    configurable: true,
    value: [new File([text], name, { type: 'application/yaml' })],
  });
  await field(wrapper).trigger('change');
  await flushPromises();
}

async function importIt(wrapper) {
  await byId(wrapper, 'template-import-submit').trigger('click');
  await flushPromises();
}

describe('Import templates', () => {
  test('a valid file shows what it holds, and Import adds it to the library as a collection and closes', async () => {
    const { wrapper, store } = open();

    expect(document.activeElement).toBe(field(wrapper).element);

    await chooseFile(wrapper, 'plant.yaml', YAML.dump(FILE));

    expect(byId(wrapper, 'template-import-summary').text()).toBe(
      'Plant: 1 template.',
    );
    expect(field(wrapper).attributes('aria-invalid')).toBeUndefined();
    expect(
      byId(wrapper, 'template-import-submit').attributes('aria-disabled'),
    ).toBeUndefined();

    await importIt(wrapper);

    expect(store.createLibraryTemplates).toHaveBeenCalledWith(
      [expect.objectContaining({ name: server.name, device: server.device })],
      { collection: { name: 'Plant' } },
    );
    expect(store.announcement).toBe('Imported 1 template as collection Plant.');
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('a collection of the same name in the library gets a number, which the summary says first', async () => {
    const { wrapper, store } = open({ collections: [{ name: 'plant' }] });

    await chooseFile(wrapper, 'plant.json', JSON.stringify(FILE));

    expect(byId(wrapper, 'template-import-summary').text()).toBe(
      'Plant: 1 template. The library has a collection of that name, so Import adds it as Plant (2).',
    );

    await importIt(wrapper);

    expect(store.createLibraryTemplates.mock.calls[0][1]).toEqual({
      collection: { name: 'Plant (2)' },
    });
    expect(store.announcement).toBe(
      'Imported 1 template as collection Plant (2).',
    );
  });

  test('a file that cannot be imported lists each problem on the field, and Import sends nothing and focuses the field', async () => {
    const { wrapper, store } = open();

    await chooseFile(
      wrapper,
      'plant.yaml',
      YAML.dump({ ...FILE, templates: [{ ...TEMPLATE, name: '' }] }),
    );

    const error = byId(wrapper, 'template-import-error');

    expect(error.text()).toBe('You cannot import this template file:');
    expect(
      wrapper
        .findAll('[data-testid="template-import-issue"]')
        .map((item) => item.text()),
    ).toEqual(['templates[0].name: a template needs a name']);
    expect(field(wrapper).attributes('aria-invalid')).toBe('true');
    expect(field(wrapper).attributes('aria-describedby')).toBe(
      'template-import-file-hint template-import-error',
    );
    expect(byId(wrapper, 'template-import-summary').exists()).toBe(false);
    expect(
      byId(wrapper, 'template-import-submit').attributes('aria-disabled'),
    ).toBe('true');

    byId(wrapper, 'template-import-submit').element.focus();
    await importIt(wrapper);

    // The file's own problem stays, and focus goes back to the file.
    expect(byId(wrapper, 'template-import-error').text()).toBe(
      'You cannot import this template file:',
    );
    expect(document.activeElement).toBe(field(wrapper).element);
    expect(store.createLibraryTemplates).not.toHaveBeenCalled();
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  test('Import with no file asks for one, and a later valid file clears the problem', async () => {
    const { wrapper } = open();

    byId(wrapper, 'template-import-submit').element.focus();
    await importIt(wrapper);

    expect(byId(wrapper, 'template-import-error').text()).toBe(
      'Choose a template file to import.',
    );
    expect(document.activeElement).toBe(field(wrapper).element);

    await chooseFile(wrapper, 'plant.yaml', YAML.dump(FILE));
    expect(byId(wrapper, 'template-import-error').exists()).toBe(false);
    expect(field(wrapper).attributes('aria-invalid')).toBeUndefined();
  });

  test('Cancel and Escape do nothing while the library takes the templates, and close the dialog otherwise', async () => {
    let finish;
    const { wrapper } = open({
      create: () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    });

    await chooseFile(wrapper, 'plant.yaml', YAML.dump(FILE));
    await importIt(wrapper);

    const submit = byId(wrapper, 'template-import-submit');

    expect(submit.text()).toBe('Import');
    expect(wrapper.text()).toContain('Importing the templates…');
    expect(submit.attributes('aria-busy')).toBe('true');
    await byId(wrapper, 'template-import-cancel').trigger('click');
    await wrapper.get('dialog').trigger('keydown', { key: 'Escape' });
    await submit.trigger('click');
    expect(wrapper.emitted('close')).toBeUndefined();

    finish();
    await flushPromises();
    expect(wrapper.emitted('close')).toHaveLength(1);

    // With a file chosen and nothing sent, Cancel closes at once.
    const other = open().wrapper;

    await chooseFile(other, 'plant.yaml', YAML.dump(FILE));
    await byId(other, 'template-import-cancel').trigger('click');
    expect(other.emitted('close')).toHaveLength(1);
  });

  test('icons the library could not take keep the dialog open on what became of them, with focus on Close', async () => {
    const theirs = base64Of(png(2, 2, [200, 40, 80, 255]));
    const ours = base64Of(png(2, 2, [10, 120, 40, 255]));

    vi.spyOn(iconLibrary, 'load').mockImplementation(async () => {
      iconLibrary.state.index = indexIcons([
        { name: 'hmi', aliases: [], data: theirs },
      ]);

      return iconLibrary.state;
    });

    const { wrapper, store } = open();

    await chooseFile(
      wrapper,
      'plant.yaml',
      YAML.dump({ ...FILE, icons: { hmi: { data: ours } } }),
    );
    expect(byId(wrapper, 'template-import-summary').text()).toBe(
      'Plant: 1 template and 1 custom icon.',
    );

    await importIt(wrapper);

    expect(store.createLibraryTemplates).toHaveBeenCalledTimes(1);
    expect(byId(wrapper, 'template-import-done').text()).toBe(
      'Imported 1 template as collection Plant.',
    );
    expect(
      wrapper
        .findAll('[data-testid="template-import-warning"]')
        .map((item) => item.text()),
    ).toEqual([
      "The server already has an icon named hmi that differs from this file's. The imported templates show the server's icon.",
    ]);
    expect(store.announcement).toBe('Imported 1 template as collection Plant.');
    expect(field(wrapper).attributes()).toHaveProperty('disabled');
    expect(document.activeElement).toBe(
      byId(wrapper, 'template-import-close').element,
    );
    expect(wrapper.emitted('close')).toBeUndefined();

    await byId(wrapper, 'template-import-close').trigger('click');
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test("the library's refusal is shown, and the dialog stays open to try again", async () => {
    const { wrapper } = open({
      create: async () => {
        throw Object.assign(new Error('status 403'), {
          response: { status: 403 },
        });
      },
    });

    await chooseFile(wrapper, 'plant.yaml', YAML.dump(FILE));
    await importIt(wrapper);

    expect(byId(wrapper, 'template-import-error').text()).toBe(
      'Could not import the templates. Your role does not include the Builder.',
    );
    expect(byId(wrapper, 'template-import-submit').text()).toBe('Import');
    expect(byId(wrapper, 'template-import-summary').exists()).toBe(true);
    expect(wrapper.emitted('close')).toBeUndefined();
  });
});
