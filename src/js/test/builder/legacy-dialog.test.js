// The parts of Upload and Import that convert a diagram of the legacy
// Builder, rendered on the server: the source Upload offers, the warnings
// both dialogs show before a draft is made, and how Import marks a topology
// that has a legacy diagram. Choosing a file, the refusals, focus and keys
// need a browser, so they are left to builder-legacy.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
import ImportWarnings from '@/components/builder/dialogs/ImportWarnings.vue';
import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

async function render(component, props = {}, setup = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);
  setup(useBuilderStore(pinia));

  return renderToString(app);
}

// The attributes of the one element the pattern finds.
function tag(html, pattern) {
  const found = html.match(new RegExp(`<[a-z]+\\b[^>]*${pattern}[^>]*>`));

  expect(found, pattern).not.toBeNull();

  return found[0];
}

const WARNINGS = [
  'Added a switch for 1 network that had none in the legacy diagram: b.',
  'Left out 1 line that was not a network link.',
  'Text and containers were kept as notes and groups. Their colors, fonts and other formatting were not converted.',
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
    expect(html).toMatch(/data-testid="upload-submit"[^>]*>\s*Upload\s*</);
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
        'Renamed node "<b>a</b>" to "a": a hostname cannot hold spaces.',
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
        .matchAll(/<option value="([^"]*)"[^>]*>([^<]*)</g),
    ].map((match) => [match[1], match[2].trim()]);

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
