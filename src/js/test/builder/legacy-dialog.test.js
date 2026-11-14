// @vitest-environment happy-dom
// Upload and Import of a legacy Builder diagram, rendered on the server: the
// source Upload offers, the warnings, and how Import marks such a topology.
// The XML hint of Upload is checked in a mounted dialog.

import { describe, expect, test } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';

import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
import ImportWarnings from '@/components/builder/dialogs/ImportWarnings.vue';
import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

import { tag, textOf } from './fixtures.js';

async function render(component, props = {}, setup = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);
  setup(useBuilderStore(pinia));

  return renderToString(app);
}

const WARNINGS = [
  'The Builder added a switch for 1 network that had none in the legacy diagram: b.',
  'The Builder did not import 1 line that was not a network link.',
  'The Builder kept text and containers as notes and groups. The Builder did not convert their colors, fonts and other formatting.',
];

describe('the Upload dialog', () => {
  test('offers the legacy source last, as one more radio of the group', async () => {
    const html = await render(UploadDialog);
    const radios = [
      ...html.matchAll(/<input\b[^>]*name="upload-source"[^>]*>([^<]*)</g),
    ];

    expect(radios).toHaveLength(4);
    expect(radios.every(([input]) => input.includes('type="radio"'))).toBe(
      true,
    );
    expect(radios[3][0]).toContain('value="legacy"');
    expect(radios[3][1].trim()).toBe('Legacy Builder diagram or Topology');
    // The group opens on File, so nothing of the legacy source is drawn.
    expect(radios[0][0]).toContain('checked');
    expect(html).not.toContain('upload-legacy-file');
    expect(tag(html, 'data-testid="upload-submit"')).not.toContain(
      'aria-disabled',
    );
    expect(
      textOf(
        html.match(/data-testid="upload-submit"[^>]*>([\s\S]*?)<\/button>/)[1],
      ),
    ).toBe('Upload');
  });

  test('has no field to paste a legacy diagram into', async () => {
    const html = await render(UploadDialog);

    expect(html).not.toContain('upload-legacy-text');
  });

  test('its status region is there, empty, before a conversion says anything', async () => {
    const html = await render(UploadDialog);
    const status = tag(html, 'data-testid="upload-status"');

    expect(status).toContain('role="status"');
    expect(status).toContain('builder-visually-hidden');
    expect(html).toMatch(
      /data-testid="upload-status"[^>]*>(<!--[^>]*-->)*<\/p>/,
    );
    expect(tag(html, 'id="upload-error"')).toContain('role="alert"');
  });
});

// A Builder document is never XML: the sources that read one say where a
// legacy diagram goes, on the field that holds it.
describe('the Upload dialog, given XML', () => {
  const XML_HINT =
    'This is possibly a legacy Builder diagram (XML). ' +
    'Select "Legacy Builder diagram or Topology" to convert it.';

  test('File and Paste text point it at the legacy source, and make no draft', async () => {
    const wrapper = mount(UploadDialog, {
      attachTo: document.body,
      global: { plugins: [createPinia()] },
    });
    const error = () => wrapper.get('[data-testid="upload-error"]').text();
    const submit = async () => {
      await wrapper.get('[data-testid="upload-submit"]').trigger('click');
      await flushPromises();
    };
    const file = wrapper.get('[data-testid="upload-file"]');

    Object.defineProperty(file.element, 'files', {
      configurable: true,
      value: [
        new File(['<mxGraphModel><root/></mxGraphModel>'], 'diagram.xml', {
          type: 'text/xml',
        }),
      ],
    });
    await file.trigger('change');
    await flushPromises();
    await submit();

    expect(error()).toBe(XML_HINT);
    expect(file.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(file.element);

    await wrapper.get('input[value="text"]').setValue();

    const text = wrapper.get('[data-testid="upload-text"]');

    await text.setValue('\n  <mxGraphModel><root/></mxGraphModel>');
    await submit();

    expect(error()).toBe(XML_HINT);
    expect(text.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(text.element);
    expect(wrapper.emitted('uploaded')).toBeUndefined();
    expect(wrapper.emitted('close')).toBeUndefined();
  });
});

describe.each(['upload', 'import'])('the warnings of %s', (prefix) => {
  const props = {
    prefix,
    summary: 'This conversion has 3 warnings.',
    warnings: WARNINGS,
  };

  test('are listed under the summary, each named a warning', async () => {
    const html = await render(ImportWarnings, props);
    const list = tag(html, `id="${prefix}-warnings"`);
    const items = [
      ...html.matchAll(/<li data-level="warning"[^>]*>([\s\S]*?)<\/li>/g),
    ].map((match) => match[1].replace(/<!--[^>]*-->/g, ''));

    expect(tag(html, `data-testid="${prefix}-result"`)).toMatch(/^<div/);
    expect(html).toMatch(/<p[^>]*>This conversion has 3 warnings\.<\/p>/);
    expect(list).toMatch(/^<ul/);
    expect(list).toContain(`data-testid="${prefix}-warnings"`);
    expect(items).toHaveLength(3);
    WARNINGS.forEach((warning, index) => {
      expect(items[index]).toMatch(/^<strong>Warning:<\/strong>\s/);
      expect(items[index]).toContain(warning);
    });
  });

  test('come before the choice, and describe the button that goes on', async () => {
    const html = await render(ImportWarnings, props);
    const cancel = tag(html, `data-testid="${prefix}-cancel"`);
    const next = tag(html, `data-testid="${prefix}-continue"`);

    expect(cancel).toMatch(/^<button/);
    expect(cancel).toContain('type="button"');
    expect(next).toMatch(/^<button/);
    expect(next).toContain('type="button"');
    expect(next).toContain(`aria-describedby="${prefix}-warnings"`);
    expect(next).toContain('builder-button--primary');
    expect(html).toMatch(
      new RegExp(`data-testid="${prefix}-cancel"[^>]*>\\s*Cancel\\s*<`),
    );
    expect(html).toMatch(
      new RegExp(
        `data-testid="${prefix}-continue"[^>]*>\\s*Continue to editor\\s*<`,
      ),
    );
    expect(html.indexOf(`id="${prefix}-warnings"`)).toBeLessThan(
      html.indexOf(`data-testid="${prefix}-cancel"`),
    );
    expect(html.indexOf(`data-testid="${prefix}-cancel"`)).toBeLessThan(
      html.indexOf(`data-testid="${prefix}-continue"`),
    );
    // Only the dialog's own prefix names anything in the view.
    const other = prefix === 'upload' ? 'import' : 'upload';

    expect(html).not.toMatch(new RegExp(`(?:id|data-testid)="${other}-`));
  });

  test('a markup-like warning is shown as text', async () => {
    const html = await render(ImportWarnings, {
      ...props,
      warnings: [
        'The Builder renamed node "<b>a</b>" to "a": a hostname cannot hold spaces.',
      ],
    });

    expect(html).toContain('&lt;b&gt;a&lt;/b&gt;');
    expect(html).not.toContain('<b>a</b>');
  });
});

describe('the Import dialog', () => {
  const sources = {
    images: [],
    scenarios: [],
    experiments: [{ name: 'exp' }],
    topologies: [
      { name: 'plain' },
      { name: 'old', builder: 'builder-xml' },
      { name: 'built', builder: 'builder-doc' },
      'named',
    ],
  };

  test('marks a stored topology that has a legacy Builder diagram', async () => {
    const html = await render(ImportDialog, {}, (store) => {
      store.sources = sources;
    });
    const select = html.slice(html.indexOf('id="import-name"'));
    const options = [
      ...select
        .slice(0, select.indexOf('</select>'))
        // The DOM render writes an empty value as a bare attribute.
        .matchAll(/<option value(?:="([^"]*)")?[^>]*>([^<]*)</g),
    ].map((match) => [match[1] ?? '', match[2].trim()]);

    // The value stays the name the server is asked for.
    expect(options).toEqual([
      ['', 'Choose a topology'],
      ['plain', 'plain'],
      ['old', 'old (legacy Builder diagram)'],
      ['built', 'built'],
      ['named', 'named'],
    ]);
  });

  test('says what the import of a legacy topology does only once one is chosen', async () => {
    const html = await render(ImportDialog, {}, (store) => {
      store.sources = sources;
    });

    expect(html).not.toContain('import-legacy-hint');
    expect(tag(html, 'id="import-name"')).not.toContain('aria-describedby');
  });
});
