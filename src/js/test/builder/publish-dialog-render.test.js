// The Publish dialog as a draft's card on the drafts page opens it
// (landing), rendered on the server: its title, Open draft, and the hint
// that goes with the diagram's errors. What a press does needs a browser,
// and is in builder-drafts.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
import { connect } from '@/builder/model.js';
import { CONFIG_NAME_RULE } from '@/builder/publish.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';

// The sample diagram with every interface connected, which publishes; or as
// it is, with an interface that has no VLAN, which Publish refuses.
function diagram({ publishable }) {
  const { doc, sw, bravo } = sampleDocument();

  return publishable
    ? connect(doc, {
        sourceNodeId: bravo.id,
        sourceHandleId: bravo.device.interfaces[0].id,
        targetNodeId: sw.id,
      }).doc
    : doc;
}

// `setup` changes the store before the dialog renders, and `form` the
// dialog's fields, as the user would have changed them: Vue runs `created`
// after the dialog's setup and before it renders.
async function render(
  props = {},
  { publishable = true, setup = () => {}, form = null } = {},
) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(PublishDialog, props) });

  app.use(pinia);

  if (form) {
    app.mixin({
      created() {
        if (this.$.type === PublishDialog) {
          Object.assign(this.$.setupState.form, form);
        }
      },
    });
  }

  const store = useBuilderStore(pinia);

  store.doc = diagram({ publishable });
  setup(store);

  return renderToString(app);
}

// The element with that id, from its tag to its end, as the server
// rendered it.
function element(html, id) {
  const start = html.indexOf(`id="${id}"`);

  expect(start, id).toBeGreaterThan(-1);

  const open = html.lastIndexOf('<', start);
  const name = html.slice(open + 1).match(/^[a-z]+/)[0];

  return html.slice(open, html.indexOf(`</${name}>`, start) + name.length + 3);
}

// Its text, without tags and Vue's comment markers, with the characters the
// server escapes put back.
function text(markup) {
  return markup
    .replace(/<!--[^>]*-->/g, '')
    .replace(/<[^>]+>/g, '')
    .replace(/\s+/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .trim();
}

// The texts of the buttons of the form's last row, in order.
function actions(html) {
  const row = html.slice(html.lastIndexOf('builder-dialog__actions'));

  return [...row.matchAll(/<button\b[^>]*>([\s\S]*?)<\/button>/g)].map(
    ([, text]) => text.replace(/<[^>]+>/g, '').trim(),
  );
}

describe('the Publish dialog on the drafts page', () => {
  test('is named for the draft, and offers Open draft before Cancel', async () => {
    const html = await render({ landing: true, name: 'Network lab' });
    const [open] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="publish-open-draft"'),
    );

    expect(html).toMatch(
      /<h2 id="publish-dialog-title"[^>]*>Publish Network lab<\/h2>/,
    );
    expect(open).toContain('type="button"');
    expect(open).not.toContain('aria-disabled');
    expect(actions(html)).toEqual(['Open draft', 'Cancel', 'Create topology']);
    // The diagram has no error, so nothing sends the user to the editor.
    expect(html).not.toContain('publish-open-hint');
  });

  test('says where the diagram’s errors are fixed, and cannot be sent', async () => {
    const html = await render(
      { landing: true, name: 'Network lab' },
      { publishable: false },
    );
    const [submit] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="publish-submit"'),
    );

    expect(html).toMatch(/<strong[^>]*>\s*Error:\s*<\/strong>/);
    expect(html).toMatch(
      /<p[^>]*data-testid="publish-open-hint"[^>]*>\s*Open the draft to fix the errors, then publish\.\s*<\/p>/,
    );
    // The hint follows the list of what is wrong.
    expect(html.indexOf('publish-open-hint')).toBeGreaterThan(
      html.indexOf('builder-issues'),
    );
    expect(submit).toMatch(/\sdisabled\b/);
    expect(html).toContain('publish-open-draft');
  });

  test('without a name, keeps the editor’s title', async () => {
    const html = await render({ landing: true });

    expect(html).toContain('>Publish diagram</h2>');
    expect(html).toContain('publish-open-draft');
  });
});

describe('the Publish dialog in the editor', () => {
  test('has the editor’s title and no way to the editor', async () => {
    const html = await render({ name: 'Network lab' });

    expect(html).toContain('>Publish diagram</h2>');
    expect(html).not.toContain('publish-open-draft');
    expect(actions(html)).toEqual(['Cancel', 'Create topology']);
  });

  test('lists the diagram’s errors without the hint', async () => {
    const html = await render({}, { publishable: false });

    expect(html).toMatch(/<strong[^>]*>\s*Error:\s*<\/strong>/);
    expect(html).not.toContain('publish-open-hint');
    expect(html).not.toContain('Open the draft');
  });
});

describe('the topology name’s hints', () => {
  // The diagram imported from topology lab, which the server has, so the
  // dialog offers to update it.
  const imported = (store) => {
    store.doc = {
      ...store.doc,
      metadata: { ...store.doc.metadata, name: 'lab' },
      source: { kind: 'topology', name: 'lab' },
    };
    store.sources = { ...store.sources, topologies: ['lab'] };
  };

  test('a new topology is said plainly, with no warning', async () => {
    const html = await render();
    const hint = element(html, 'publish-topology-action-hint');

    expect(text(hint)).toBe('A new topology will be created.');
    expect(hint).not.toContain('builder-hint--warning');
    expect(hint).not.toContain('<svg');
  });

  test('an update warns: a sign before the same words, on a yellow ground', async () => {
    const html = await render({}, { setup: imported });
    const hint = element(html, 'publish-topology-action-hint');

    expect(text(hint)).toBe(
      'A topology with this name exists and will be updated.',
    );
    const [classes] = hint.match(/^<p [^>]*class="([^"]*)"/).slice(1);

    expect(classes.split(' ').sort()).toEqual([
      'builder-hint',
      'builder-hint--warning',
    ]);
    // The sign is decorative: the words say it.
    expect(hint).toMatch(
      /<svg[^>]*class="builder-icon builder-icon--warning"[^>]*aria-hidden="true"/,
    );
    expect(hint.indexOf('<svg')).toBeLessThan(hint.indexOf('A topology'));
    expect(actions(html)).toEqual(['Cancel', 'Update topology']);
  });

  test('a valid name shows no naming rule, and is described by its hint alone', async () => {
    const html = await render({}, { setup: imported });
    const [field] = tags(html, 'input').filter((tag) =>
      tag.includes('data-testid="publish-name"'),
    );

    expect(field).toContain('value="lab"');
    expect(field).toContain('aria-describedby="publish-topology-action-hint"');
    expect(html).not.toContain('publish-name-rule');
    expect(html).not.toContain('Names can use only');
  });
});

// The input with that test id, as the server rendered it.
function input(html, testId) {
  const [tag] = tags(html, 'input').filter((markup) =>
    markup.includes(`data-testid="${testId}"`),
  );

  expect(tag, testId).toBeTruthy();

  return tag;
}

describe('the experiment name’s hints', () => {
  // A diagram imported from experiment exp, which the server has, so the
  // dialog offers to update it.
  const imported = (store) => {
    store.doc = {
      ...store.doc,
      metadata: { ...store.doc.metadata, name: 'core' },
      source: { kind: 'experiment', name: 'exp', topology: 'core' },
    };
    store.sources = { ...store.sources, experiments: ['exp', 'other'] };
  };
  const experiment = (name) => ({
    mode: 'topology-experiment',
    experimentName: name,
  });

  test('an update warns as the topology’s does: a sign before the same words, on a yellow ground', async () => {
    const html = await render({}, { setup: imported, form: experiment('exp') });
    const hint = element(html, 'publish-experiment-hint');

    expect(text(hint)).toBe(
      'An experiment with this name exists and will be updated.',
    );
    const [classes] = hint.match(/^<p [^>]*class="([^"]*)"/).slice(1);

    expect(classes.split(' ').sort()).toEqual([
      'builder-hint',
      'builder-hint--warning',
    ]);
    // The sign is decorative: the words say it.
    expect(hint).toMatch(
      /<svg[^>]*class="builder-icon builder-icon--warning"[^>]*aria-hidden="true"/,
    );
    expect(hint.indexOf('<svg')).toBeLessThan(hint.indexOf('An experiment'));
    expect(actions(html)).toEqual([
      'Cancel',
      'Create topology and update experiment',
    ]);
  });

  test('a new experiment, and one this diagram cannot update, carry no warning', async () => {
    for (const name of ['new-exp', 'other']) {
      const html = await render(
        {},
        { setup: imported, form: experiment(name) },
      );
      const hint = element(html, 'publish-experiment-hint');

      expect(hint, name).not.toContain('builder-hint--warning');
      expect(hint, name).not.toContain('<svg');
    }
  });

  test('an invalid name shows why with the rule, and a valid one neither', async () => {
    const bad = await render(
      {},
      { setup: imported, form: experiment('my exp/1') },
    );

    expect(text(element(bad, 'publish-experiment-rule'))).toBe(
      'This name is not allowed: it contains a space and characters that ' +
        `are not allowed: "/". ${CONFIG_NAME_RULE}`,
    );
    expect(input(bad, 'publish-experiment')).toContain(
      'aria-describedby="publish-experiment-hint publish-experiment-rule"',
    );

    const good = await render({}, { setup: imported, form: experiment('exp') });

    expect(good).not.toContain('publish-experiment-rule');
    expect(good).not.toContain('Names can use only');
    expect(input(good, 'publish-experiment')).toContain(
      'aria-describedby="publish-experiment-hint"',
    );
  });
});

describe('the scenario name’s rule', () => {
  // A diagram that carries an uploaded scenario, which the dialog writes to
  // the config the Scenario name field names.
  const uploaded = (store) => {
    store.doc = {
      ...store.doc,
      scenario: { kind: 'uploaded', name: 'sc.yaml', content: {} },
    };
  };

  test('an invalid name shows why with the rule, and a valid one neither', async () => {
    const bad = await render(
      {},
      { setup: uploaded, form: { scenarioName: 'plant#2' } },
    );

    expect(text(element(bad, 'publish-scenario-rule'))).toBe(
      `This name is not allowed: it contains characters that are not allowed: "#". ${CONFIG_NAME_RULE}`,
    );
    expect(input(bad, 'publish-scenario-name')).toContain(
      'aria-describedby="publish-scenario-rule"',
    );

    // The uploaded scenario's name starts the field as a valid name.
    const good = await render({}, { setup: uploaded });

    expect(input(good, 'publish-scenario-name')).toContain('value="sc.yaml"');
    expect(good).not.toContain('publish-scenario-rule');
    expect(good).not.toContain('Names can use only');
    expect(input(good, 'publish-scenario-name')).not.toContain(
      'aria-describedby',
    );
  });
});
