// @vitest-environment happy-dom
// Upload refuses a template file and names Import templates, while a diagram
// with its own templates still uploads. Mounted and driven as a user would.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import YAML from 'js-yaml';

import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { useBuilderStore } from '@/builder/store.js';
import { TEMPLATE_FILE_SCHEMA_URI } from '@/builder/templateFile.js';

import { sampleDocument } from './fixtures.js';

const REFUSED =
  'This is a node template file, not a diagram. To add its templates to the library, select Import templates on the Node Templates tab.';

// A template file as Download writes it: one collection, no ids.
const [server] = BUILTIN_TEMPLATES;
const FILE = {
  $schema: TEMPLATE_FILE_SCHEMA_URI,
  name: 'Plant',
  templates: [
    {
      name: server.name,
      description: server.description,
      device: server.device,
    },
  ],
};

enableAutoUnmount(afterEach);

// Opens the dialog in the page, as Builder does.
function open() {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);
  const wrapper = mount(UploadDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store, before: store.doc };
}

// Chooses `name` with `text` in the file field, as the file picker does.
async function chooseFile(wrapper, name, text) {
  const field = wrapper.get('[data-testid="upload-file"]');

  Object.defineProperty(field.element, 'files', {
    configurable: true,
    value: [new File([text], name, { type: 'application/yaml' })],
  });
  await field.trigger('change');
  await flushPromises();
}

// Pastes `text`, then selects Upload.
async function paste(wrapper, text) {
  await wrapper.get('input[value="text"]').setValue();
  await wrapper.get('[data-testid="upload-text"]').setValue(text);
  await wrapper.get('[data-testid="upload-submit"]').trigger('click');
  await flushPromises();
}

const errorOf = (wrapper) => wrapper.find('[data-testid="upload-error"]');

describe('Upload and a template file', () => {
  test('a file that names the template file schema is refused, and the error names Import templates', async () => {
    const { wrapper, store, before } = open();

    await chooseFile(wrapper, 'plant.yaml', YAML.dump(FILE));
    await wrapper.get('[data-testid="upload-submit"]').trigger('click');
    await flushPromises();

    expect(errorOf(wrapper).text()).toBe(REFUSED);
    expect(
      wrapper.get('[data-testid="upload-file"]').attributes('aria-invalid'),
    ).toBe('true');
    expect(wrapper.emitted('uploaded')).toBeUndefined();
    expect(wrapper.emitted('close')).toBeUndefined();
    expect(store.doc).toBe(before);
  });

  test('pasted text with a list of templates and no schema is refused too, and focus moves to the text', async () => {
    const { $schema: _, ...unnamed } = FILE;
    const { wrapper } = open();

    await paste(wrapper, JSON.stringify(unnamed));

    const text = wrapper.get('[data-testid="upload-text"]');

    expect(errorOf(wrapper).text()).toBe(REFUSED);
    expect(text.attributes('aria-invalid')).toBe('true');
    expect(text.attributes('aria-describedby')).toBe('upload-error');
    expect(document.activeElement).toBe(text.element);
    expect(wrapper.emitted('uploaded')).toBeUndefined();
  });

  test('a diagram that carries its own templates uploads as a new draft', async () => {
    const { doc } = sampleDocument();
    const { wrapper, store } = open();

    await paste(
      wrapper,
      JSON.stringify({
        ...doc,
        templates: [{ ...server, id: '3f1c6f7e-2b9a-4c1d-9e8f-0a1b2c3d4e5f' }],
      }),
    );

    expect(errorOf(wrapper).exists()).toBe(false);
    expect(wrapper.emitted('uploaded')).toEqual([[{}]]);
    expect(wrapper.emitted('close')).toHaveLength(1);
    expect(store.doc.templates.map((template) => template.name)).toEqual([
      'Server',
    ]);
  });
});

describe('the Upload dialog, from the keyboard', () => {
  test('opens as a modal dialog with focus on it, and Escape asks to close it', async () => {
    const { wrapper } = open();
    const dialog = wrapper.get('dialog');

    expect(dialog.element.open).toBe(true);
    expect(document.activeElement).toBe(dialog.element);

    await dialog.trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('choosing another source clears the error of the last one', async () => {
    const { wrapper } = open();

    await wrapper.get('[data-testid="upload-submit"]').trigger('click');
    await flushPromises();
    expect(errorOf(wrapper).text()).toBe('Choose a file to upload.');
    expect(document.activeElement?.id).toBe('upload-file');

    await wrapper.get('input[value="text"]').setValue();
    expect(errorOf(wrapper).exists()).toBe(false);
    expect(wrapper.find('[data-testid="upload-file"]').exists()).toBe(false);
  });
});

describe('the Upload dialog, opening a published diagram', () => {
  test('a second press while it opens opens nothing more', async () => {
    const { wrapper, store } = open();
    let finish;
    const openPublished = vi
      .spyOn(store, 'openPublishedDocument')
      .mockImplementation(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      );

    store.documents = [{ id: 'plant', name: 'Plant' }];
    await wrapper.get('input[value="published"]').setValue();
    await wrapper.get('[data-testid="upload-published"]').setValue('plant');

    const submit = wrapper.get('[data-testid="upload-submit"]');

    await submit.trigger('click');
    await flushPromises();
    await submit.trigger('click');
    await flushPromises();
    expect(openPublished).toHaveBeenCalledTimes(1);

    finish(true);
    await flushPromises();
    expect(wrapper.emitted('uploaded')).toEqual([[{ draftCreated: true }]]);
  });
});
