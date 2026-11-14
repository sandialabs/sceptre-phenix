// @vitest-environment node
// The Publish dialog rendered on the server: its titles, Open draft and hints.
// publish-dialog.test.js drives what publishing changes in a page.

import { describe, expect, test } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
import { connect } from '@/builder/model.js';
import { configNameHint } from '@/builder/publish.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags, textOf } from './fixtures.js';

// The sample diagram with every interface connected, which publishes. Or the
// sample as it is, with an interface that has no VLAN, which Publish refuses.
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

// `setup` changes the store and `form` the fields, both before the dialog
// renders.
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

  test('Open draft and the publish button have the icons of their actions, and only the publish button is primary', async () => {
    const html = await render({ landing: true, name: 'Network lab' });
    const button = (id) => {
      const start = html.lastIndexOf('<button', html.indexOf(id));

      return html.slice(start, html.indexOf('</button>', start));
    };

    expect(button('publish-open-draft')).toContain('builder-icon--folder-open');
    expect(button('publish-submit')).toContain('builder-icon--publish');
    expect(button('publish-submit')).toContain('type="submit"');
    // Open draft is not blue here, so it does not outrank Create topology.
    expect(button('publish-open-draft')).not.toMatch(
      /builder-button--(primary|danger)/,
    );
    expect(button('publish-submit')).toMatch(/builder-button--primary/);
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
      /<p[^>]*data-testid="publish-open-hint"[^>]*>\s*Open the draft to fix the errors\. Then publish it\.\s*<\/p>/,
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

  test('says nothing under Checks about how the server publishes', async () => {
    for (const publishable of [true, false]) {
      const html = await render({}, { publishable });

      expect(html).not.toContain('last saved snapshot');
    }
  });
});

// The diagram imported from topology lab, which the server has, so the dialog
// offers to update it under the diagram's topology name.
const topologyImported = (store) => {
  store.doc = {
    ...store.doc,
    metadata: { ...store.doc.metadata, name: 'lab' },
    source: { kind: 'topology', name: 'lab' },
  };
  store.sources = { ...store.sources, topologies: ['lab'] };
};

// A diagram imported from experiment exp, which the server has, so the
// dialog offers to update it.
const experimentImported = (store) => {
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

describe('an update of a config that exists', () => {
  test.each([
    {
      kind: 'topology',
      id: 'publish-topology-action-hint',
      setup: topologyImported,
      form: null,
      words: 'A topology with this name exists. Publishing updates it.',
      buttons: ['Cancel', 'Update topology'],
    },
    {
      kind: 'experiment',
      id: 'publish-experiment-hint',
      setup: experimentImported,
      form: experiment('exp'),
      words: 'An experiment with this name exists. Publishing updates it.',
      buttons: ['Cancel', 'Create topology and update experiment'],
    },
  ])(
    'of the $kind warns: a sign before the words, on a yellow ground',
    async ({ id, setup, form, words, buttons }) => {
      const html = await render({}, { setup, form });
      const hint = element(html, id);
      const [classes] = hint.match(/^<p [^>]*class="([^"]*)"/).slice(1);

      expect(textOf(hint)).toBe(words);
      expect(classes.split(' ').sort()).toEqual([
        'builder-hint',
        'builder-hint--warning',
      ]);
      // The sign is decorative: the words say it.
      expect(hint).toMatch(
        /<svg[^>]*class="builder-icon builder-icon--warning"[^>]*aria-hidden="true"/,
      );
      expect(hint.indexOf('<svg')).toBeLessThan(hint.indexOf(words));
      expect(actions(html)).toEqual(buttons);
    },
  );
});

describe('the topology name’s hints', () => {
  test('a new topology is said plainly, with no warning', async () => {
    const html = await render();
    const hint = element(html, 'publish-topology-action-hint');

    expect(textOf(hint)).toBe('Publishing creates a new topology.');
    expect(hint).not.toContain('builder-hint--warning');
    expect(hint).not.toContain('<svg');
  });

  test('a valid name shows no naming rule, and is described by its hint alone', async () => {
    const html = await render({}, { setup: topologyImported });
    const [field] = tags(html, 'input').filter((tag) =>
      tag.includes('data-testid="publish-name"'),
    );

    expect(field).toContain('value="lab"');
    expect(field).toContain('aria-describedby="publish-topology-action-hint"');
    expect(html).not.toContain('publish-name-rule');
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
  test('a new experiment, and one this diagram cannot update, carry no warning', async () => {
    for (const name of ['new-exp', 'other']) {
      const html = await render(
        {},
        { setup: experimentImported, form: experiment(name) },
      );
      const hint = element(html, 'publish-experiment-hint');

      expect(hint, name).not.toContain('builder-hint--warning');
      expect(hint, name).not.toContain('<svg');
    }
  });

  test('an invalid name shows why with the rule, and a valid one neither', async () => {
    const bad = await render(
      {},
      { setup: experimentImported, form: experiment('my exp/1') },
    );

    expect(textOf(element(bad, 'publish-experiment-rule'))).toBe(
      configNameHint('my exp/1'),
    );
    expect(input(bad, 'publish-experiment')).toContain(
      'aria-describedby="publish-experiment-hint publish-experiment-rule"',
    );

    const good = await render(
      {},
      { setup: experimentImported, form: experiment('exp') },
    );

    expect(good).not.toContain('publish-experiment-rule');
    expect(input(good, 'publish-experiment')).toContain(
      'aria-describedby="publish-experiment-hint"',
    );
  });
});

describe('the scenarios', () => {
  // A diagram that lists two scenarios.
  const listed = (store) => {
    store.doc = { ...store.doc, scenarios: ['plant-ntp', 'plant-attack'] };
  };
  const withExperiment = { mode: 'topology-experiment', experimentName: 'exp' };

  // The options of the Experiment scenario select: their values, labels,
  // and which is selected.
  function options(html) {
    const select = element(html, 'publish-scenario');

    return tags(select, 'option').map((tag) => ({
      value: tag.match(/value="([^"]*)"/)?.[1],
      selected: /\sselected\b/.test(tag),
    }));
  }

  test('an experiment uses the first listed unless another or none is chosen', async () => {
    const html = await render({}, { setup: listed, form: withExperiment });
    const select = element(html, 'publish-scenario');

    expect(html).toMatch(
      /<label for="publish-scenario"[^>]*>Experiment scenario<\/label>/,
    );
    expect(options(html)).toEqual([
      { value: 'plant-ntp', selected: true },
      { value: 'plant-attack', selected: false },
      { value: '', selected: false },
    ]);
    expect(textOf(select)).toContain('No scenario');
    expect(tags(select, 'select')[0]).toContain(
      'aria-describedby="publish-scenario-hint"',
    );

    const none = await render(
      {},
      { setup: listed, form: { ...withExperiment, scenarioName: '' } },
    );

    expect(options(none).find((option) => option.selected)?.value).toBe('');
  });

  test('either mode says the topology is added to each listed scenario', async () => {
    for (const form of [null, withExperiment]) {
      const html = await render({}, { setup: listed, form });

      expect(textOf(element(html, 'publish-scenario-hint'))).toBe(
        'Publishing adds this topology to the topology annotation of each of ' +
          'the scenarios plant-ntp and plant-attack. Experiments of the ' +
          'topology can then use it.',
      );
    }

    // A topology alone picks no scenario.
    const html = await render({}, { setup: listed });

    expect(html).not.toContain('<select');
    expect(html).toMatch(/<h3[^>]*>Scenarios<\/h3>/);
  });

  test('a diagram that lists none has no scenario section', async () => {
    const html = await render({}, { form: withExperiment });

    expect(html).not.toContain('publish-scenario');
  });
});
