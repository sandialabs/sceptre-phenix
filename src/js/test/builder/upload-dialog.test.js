// Upload refuses a template file and names Import templates, while a diagram
// with its own templates still uploads; rendered on the server.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
import YAML from 'js-yaml';

import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { useBuilderStore } from '@/builder/store.js';
import { TEMPLATE_FILE_SCHEMA_URI } from '@/builder/templateFile.js';

import { sampleDocument } from './fixtures.js';

const REFUSED =
  'This is a node template file, not a diagram. To add its templates to the library, select Import templates on the Node Templates tab.';

// A template file as Export writes it: one collection, no ids.
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

// Opens the dialog, submits `text` from the source given, and returns the
// dialog's bindings, the store, and the events it emitted.
async function submitted(text, source = 'text') {
  const pinia = createPinia();
  const emitted = { uploaded: vi.fn(), close: vi.fn() };
  const app = createSSRApp({
    render: () =>
      h(UploadDialog, {
        onUploaded: emitted.uploaded,
        onClose: emitted.close,
      }),
  });
  let bindings = null;

  app.use(pinia);
  app.mixin({
    created() {
      if (this.$.type === UploadDialog) {
        bindings = this.$.setupState;
      }
    },
  });

  const store = useBuilderStore(pinia);

  await renderToString(app);
  bindings.form.source = source;
  bindings.form.text = text;
  if (source === 'file') {
    bindings.form.file = 'read';
    bindings.form.fileName = 'plant.yaml';
  }

  const before = store.doc;

  await bindings.submit();

  return { bindings, store, emitted, before };
}

// A refusal moves focus to the control of its source.
beforeEach(() => {
  vi.stubGlobal('document', { getElementById: () => null });

  return () => vi.unstubAllGlobals();
});

describe('Upload and a template file', () => {
  test('a file that names the template file schema is refused, and the error names Import templates', async () => {
    const { bindings, store, emitted, before } = await submitted(
      YAML.dump(FILE),
      'file',
    );

    expect(bindings.error.text).toBe(REFUSED);
    expect(bindings.invalid('file')).toBe('true');
    expect(emitted.uploaded).not.toHaveBeenCalled();
    expect(emitted.close).not.toHaveBeenCalled();
    expect(store.doc).toBe(before);
  });

  test('pasted text with a list of templates and no schema is refused too', async () => {
    const { $schema: _, ...unnamed } = FILE;
    const { bindings, emitted } = await submitted(JSON.stringify(unnamed));

    expect(bindings.error.text).toBe(REFUSED);
    expect(bindings.invalid('text')).toBe('true');
    expect(emitted.uploaded).not.toHaveBeenCalled();
  });

  test('a diagram that carries its own templates uploads as a new draft', async () => {
    const { doc } = sampleDocument();
    const { bindings, store, emitted } = await submitted(
      JSON.stringify({
        ...doc,
        templates: [{ ...server, id: '3f1c6f7e-2b9a-4c1d-9e8f-0a1b2c3d4e5f' }],
      }),
    );

    expect(bindings.error.text).toBe('');
    expect(emitted.uploaded).toHaveBeenCalledTimes(1);
    expect(store.doc.templates.map((template) => template.name)).toEqual([
      'Server',
    ]);
  });
});
