// The Scenario dialog and its editor, rendered on the server: what they
// show when they open. scenario-dialog.test.js drives them in a page.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderBusyButton from '@/components/builder/BuilderBusyButton.vue';
import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
import ScenarioEditorDialog from '@/components/builder/dialogs/ScenarioEditorDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags, textOf } from './fixtures.js';

// The HTML of the dialog for a diagram listing `scenarios`, on a server
// listing `stored`.
async function open({ scenarios = [], stored = [], readOnly = false } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(ScenarioDialog) });

  app.use(pinia);

  const store = useBuilderStore(pinia);
  const { doc } = sampleDocument();

  store.doc = scenarios.length ? { ...doc, scenarios } : doc;
  store.sources = { ...store.sources, scenarios: stored };
  store.readOnly = readOnly;

  return { html: await renderToString(app) };
}

// The HTML of the scenario editor with `props`.
async function openEditor(props) {
  const app = createSSRApp({
    render: () => h(ScenarioEditorDialog, props),
  });

  return { html: await renderToString(app) };
}

const EDIT_TEXT = [
  'apiVersion: phenix.sandia.gov/v2',
  'kind: Scenario',
  'metadata:',
  '  name: plant-ntp',
  'spec:',
  '  apps: []',
  '',
].join('\n');

describe('the list', () => {
  test('lists every scenario, sorted by name, with a box set for each the diagram lists', async () => {
    const { html } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', { name: 'plant-attack' }],
    });
    const boxes = tags(html, 'input').filter((tag) =>
      tag.includes('type="checkbox"'),
    );

    expect(html).toMatch(/<h2 id="scenario-dialog-title"[^>]*>Scenarios<\/h2>/);
    expect(html).toMatch(/<legend[^>]*>Scenarios of this diagram<\/legend>/);
    expect(
      boxes.map((tag) => tag.match(/data-testid="scenario-check-([^"]+)"/)[1]),
    ).toEqual(['plant-attack', 'plant-ntp']);
    expect(boxes).toHaveLength(2);
    expect(boxes[0]).toContain('data-testid="scenario-check-plant-attack"');
    expect(boxes[0]).not.toMatch(/\schecked/);
    expect(boxes[1]).toContain('data-testid="scenario-check-plant-ntp"');
    expect(boxes[1]).toMatch(/\schecked/);
    // Each box is named by its label, the scenario's name.
    expect(html).toMatch(
      /<label for="scenario-check-1"[^>]*>\s*plant-ntp\s*<\/label>/,
    );
    // View and Edit name the scenario they are for.
    expect(html).toMatch(
      /View<span class="builder-visually-hidden"[^>]*>\s*plant-ntp<\/span>/,
    );
    expect(html).toMatch(
      /Edit<span class="builder-visually-hidden"[^>]*>\s*plant-attack<\/span>/,
    );
    expect(html).toContain('data-testid="scenario-create"');
    expect(html).toContain('1 scenario selected.');
    expect(html).toContain('role="alert"');
    // Few scenarios need no filter.
    expect(html).not.toContain('data-testid="scenario-filter"');
  });

  test('an editable draft has no hints on the list or the upload field, and the legend names the file field', async () => {
    const { html } = await open({ stored: ['plant-ntp'] });
    const [file] = tags(html, 'input').filter((tag) =>
      tag.includes('type="file"'),
    );

    expect(html).not.toContain('that this diagram uses');
    expect(html).not.toContain('scenario-file-hint');
    expect(html).not.toContain('5 MiB');
    expect(html).toMatch(
      /<legend id="scenario-file-legend"[^>]*>\s*Upload a scenario \(YAML or JSON\)\s*<\/legend>/,
    );
    expect(html).not.toContain('<label for="scenario-file"');
    expect(file).toContain('aria-labelledby="scenario-file-legend"');
    expect(file).not.toContain('aria-describedby');
  });

  test('View and Edit of a row are item actions: an eye, and a blue pencil', async () => {
    const { html } = await open({ stored: ['plant-ntp'] });
    const button = (id) => {
      const start = html.lastIndexOf('<button', html.indexOf(id));

      return html.slice(start, html.indexOf('</button>', start));
    };
    const view = button('scenario-view-plant-ntp');
    const edit = button('scenario-edit-plant-ntp');

    expect(view).toContain('builder-icon--eye');
    expect(view).not.toMatch(/builder-button--(primary|danger)/);
    expect(edit).toContain('builder-icon--pencil');
    expect(edit).toContain('builder-button--primary');
    expect(textOf(view)).toBe('View plant-ntp');
    expect(textOf(edit)).toBe('Edit plant-ntp');
  });

  test('Create new scenario has the plus icon of an add action', async () => {
    const { html } = await open();
    const start = html.lastIndexOf('<button', html.indexOf('scenario-create'));
    const create = html.slice(start, html.indexOf('</button>', start));

    expect(create).toContain('builder-icon--plus');
    expect(textOf(create)).toBe('Create new scenario');
  });

  test('says when there are no scenarios', async () => {
    const { html } = await open();

    expect(html).toContain('data-testid="scenario-none"');
    expect(html).toContain('0 scenarios selected.');
  });

  test('a read-only draft shows the list with its boxes disabled, and View only', async () => {
    const { html } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', 'plant-attack'],
      readOnly: true,
    });
    const boxes = tags(html, 'input').filter((tag) =>
      tag.includes('type="checkbox"'),
    );

    expect(html).toContain('plant-ntp');
    expect(html).toContain(
      'The selected scenarios are the ones that this diagram uses.',
    );
    expect(boxes).toHaveLength(2);
    boxes.forEach((tag) => expect(tag).toMatch(/\sdisabled/));
    expect(html).toContain('data-testid="scenario-view-plant-ntp"');
    expect(html).not.toContain('scenario-edit-');
    expect(html).not.toContain('scenario-create');
    expect(html).not.toContain('scenario-file');
    expect(html).not.toContain('scenario-submit');
    expect(html).toMatch(/<button[^>]*>\s*Close\s*<\/button>/);
  });
});

describe('the scenario editor', () => {
  test('View shows the config read only, in a region that Tab reaches, with Copy YAML', async () => {
    const { html } = await openEditor({
      mode: 'view',
      name: 'plant-ntp',
      text: 'kind: Scenario\nnote: "<b>"\n',
    });

    expect(html).toMatch(
      /<h2 id="scenario-editor-title"[^>]*>Scenario plant-ntp<\/h2>/,
    );
    expect(html).toMatch(
      /<pre[^>]*tabindex="0"[^>]*role="region"[^>]*aria-label="Scenario config \(YAML\)"/,
    );
    // The text is shown as it is, and escaped.
    expect(html).toContain(
      '>kind: Scenario\nnote: &quot;&lt;b&gt;&quot;\n</code></pre>',
    );
    expect(html).not.toContain('<textarea');
    expect(html).toMatch(
      /<button[^>]*scenario-editor-copy[^>]*>\s*<svg[^>]*builder-icon--copy[\s\S]*?<\/svg>\s*Copy YAML/,
    );
  });

  test('Edit shows the text in a labelled code field, and Save scenario', async () => {
    const { html } = await openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save: vi.fn(),
    });
    const [field] = tags(html, 'textarea');

    expect(html).toMatch(/Edit scenario plant-ntp<\/h2>/);
    expect(html).toMatch(
      /<label for="scenario-editor-text"[^>]*>Scenario config \(YAML\)<\/label>/,
    );
    expect(field).toContain('id="scenario-editor-text"');
    expect(field).toContain('spellcheck="false"');
    expect(field).not.toContain('aria-describedby');
    expect(html).not.toContain('scenario-editor-hint');
    expect(html).not.toContain('as necessary');
    expect(html).toMatch(/scenario-editor-save[^>]*>[\s\S]*?Save scenario/);
  });

  test('Create new scenario says Create scenario', async () => {
    const { html } = await openEditor({
      mode: 'create',
      name: 'plant-scenario',
      text: EDIT_TEXT,
      save: vi.fn(),
    });

    expect(html).toMatch(/Create new scenario<\/h2>/);
    expect(html).not.toContain('scenario-editor-hint');
    expect(html).not.toContain('as necessary');
    expect(html).toMatch(/scenario-editor-save[^>]*>[\s\S]*?Create scenario/);
  });
});

describe('the busy button', () => {
  async function render(props) {
    return renderToString(
      createSSRApp({
        render: () => h(BuilderBusyButton, props, () => 'View'),
      }),
    );
  }

  test('a spinner replaces the label, which keeps the button size, and the busy label names it', async () => {
    const idle = await render({ busy: false, busyLabel: 'Loading nera' });
    const busy = await render({ busy: true, busyLabel: 'Loading nera' });

    expect(idle).not.toContain('aria-busy');
    expect(idle).not.toContain('aria-label');
    expect(idle).not.toContain('builder-spinner');
    expect(busy).toMatch(/<button[^>]*aria-busy="true"/);
    expect(busy).toMatch(/<button[^>]*aria-disabled="true"/);
    expect(busy).toMatch(/<button[^>]*aria-label="Loading nera"/);
    expect(busy).toMatch(
      /<span class="builder-busy-button__label" aria-hidden="true"[^>]*>(<!--\[-->)?View/,
    );
    expect(busy).toMatch(
      /<span class="builder-toolbar__spinner builder-busy-button__ring" aria-hidden="true"/,
    );
  });

  test('disabled marks the button aria-disabled only', async () => {
    const html = await render({ disabled: true });

    expect(html).toMatch(/<button[^>]*aria-disabled="true"/);
    expect(html).not.toMatch(/<button[^>]*\sdisabled/);
  });
});
