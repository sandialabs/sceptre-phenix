// The Scenario dialog, which edits the list of Scenario configs a diagram
// names: rendered on the server, then driven through its setup state, as a
// press would. Focus and keys need a browser, and are in
// builder-publish.spec.js.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';

// The dialog for a diagram that lists `scenarios`, on a server that lists
// `stored`, with the store's server calls replaced. `state` is the dialog's
// setup state, whose functions are what its controls call.
async function open({ scenarios = [], stored = [], readOnly = false } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(ScenarioDialog) });
  let state = null;

  app.use(pinia);
  app.mixin({
    created() {
      if (this.$.type === ScenarioDialog) {
        state = this.$.setupState;
      }
    },
  });

  const store = useBuilderStore(pinia);
  const { doc } = sampleDocument();

  store.doc = scenarios.length ? { ...doc, scenarios } : doc;
  store.sources = { ...store.sources, scenarios: stored };
  store.readOnly = readOnly;

  vi.spyOn(store, 'fetchSources').mockResolvedValue(store.sources);
  vi.spyOn(store, 'saveScenarioConfig').mockResolvedValue();
  vi.spyOn(store, 'setScenarios').mockReturnValue(null);

  const html = await renderToString(app);

  return { html, state, store };
}

// A change event of a file field that chose a file holding `text`.
function chose(name, text) {
  const file = { name, size: text.length, text: async () => text };

  return { target: { files: [file] } };
}

const SCENARIO_FILE =
  'apiVersion: phenix.sandia.gov/v2\n' +
  'kind: Scenario\n' +
  'metadata:\n' +
  '  name: plant-ntp\n' +
  '  annotations:\n' +
  '    topology: plant\n' +
  'spec:\n' +
  '  apps:\n' +
  '    - name: ntp\n';

// What a stored file is sent as.
const STORED = {
  apiVersion: 'phenix.sandia.gov/v2',
  kind: 'Scenario',
  metadata: { name: 'plant-ntp', annotations: { topology: 'plant' } },
  spec: { apps: [{ name: 'ntp' }] },
};

// Lets a save the dialog started, and did not wait for, finish.
const settle = () => new Promise((resolve) => setTimeout(resolve));

beforeEach(() => {
  // Moving focus to a control is a browser's: here nothing has focus.
  vi.stubGlobal('document', { getElementById: () => null });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('the list', () => {
  test('shows each listed scenario with Remove, and offers the others the server lists', async () => {
    const { html } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', { name: 'plant-attack' }],
    });
    const [remove] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="scenario-remove-1"'),
    );
    const offered = tags(html, 'option').map(
      (tag) => tag.match(/value="([^"]*)"/)?.[1],
    );

    expect(html).toMatch(/<h2 id="scenario-dialog-title"[^>]*>Scenarios<\/h2>/);
    expect(html).toContain('aria-labelledby="scenario-list-heading"');
    expect(html).toMatch(
      /data-testid="scenario-row-1"[^>]*>\s*<span[^>]*>plant-ntp<\/span>/,
    );
    expect(remove).toContain('type="button"');
    expect(html).toMatch(
      /Remove<span class="builder-visually-hidden"[^>]*>\s*scenario plant-ntp<\/span>/,
    );
    // A listed scenario is not offered again.
    expect(offered).toEqual(['', 'plant-attack']);
    expect(html).toContain('role="alert"');
  });

  test('says when there are none, and when the server lists none', async () => {
    const { html } = await open();

    expect(html).toContain('data-testid="scenario-none"');
    expect(html).toContain('data-testid="scenario-empty"');
  });

  test('a stored scenario is added, one is removed, and Save writes the list', async () => {
    const { state, store } = await open({
      scenarios: ['plant-ntp', 'plant-dns'],
      stored: ['plant-ntp', 'plant-dns', 'plant-attack'],
    });

    state.form.stored = 'plant-attack';
    state.addStored();
    expect(state.names).toEqual(['plant-ntp', 'plant-dns', 'plant-attack']);
    expect(state.status.text).toBe('Added scenario plant-attack to the list.');
    expect(state.form.stored).toBe('');

    await state.remove(0);
    expect(state.names).toEqual(['plant-dns', 'plant-attack']);
    expect(state.status.text).toBe('Removed scenario plant-ntp from the list.');

    // Nothing is written until Save, which writes it in one step.
    expect(store.setScenarios).not.toHaveBeenCalled();
    state.submit();
    expect(store.setScenarios).toHaveBeenCalledWith([
      'plant-dns',
      'plant-attack',
    ]);
  });

  test('Add asks for a scenario, and refuses one past the most a diagram lists', async () => {
    const { state } = await open({ stored: ['plant-ntp'] });

    state.addStored();
    expect(state.error.text).toBe('Choose a scenario to add.');
    expect(state.error.field).toBe('stored');

    const full = await open({
      scenarios: Array.from({ length: 20 }, (_, i) => `s${i}`),
      stored: ['plant-ntp'],
    });

    expect(full.html).toContain('data-testid="scenario-limit"');
    full.state.form.stored = 'plant-ntp';
    full.state.addStored();
    expect(full.state.error.text).toBe(
      'A diagram lists at most 20 scenarios. Remove one first.',
    );
    expect(full.state.names).toHaveLength(20);
  });

  test('a read-only draft shows the list only', async () => {
    const { html } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', 'plant-attack'],
      readOnly: true,
    });

    expect(html).toContain('plant-ntp');
    expect(html).not.toContain('scenario-remove-1');
    expect(html).not.toContain('scenario-name');
    expect(html).not.toContain('scenario-file');
    expect(html).not.toContain('scenario-submit');
    expect(html).toMatch(/<button[^>]*>\s*Close\s*<\/button>/);
  });
});

describe('uploading a scenario file', () => {
  test('a new scenario is created on the server and listed', async () => {
    const { state, store } = await open({ stored: ['plant-attack'] });

    await state.onFile(chose('anything.yaml', SCENARIO_FILE));
    expect(state.upload.name).toBe('plant-ntp');
    expect(state.replaces).toBe(false);
    expect(state.uploadHint).toBe(
      'A new scenario will be stored on the server.',
    );

    state.storeUpload();
    await settle();

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(STORED, {
      replace: false,
    });
    expect(state.names).toEqual(['plant-ntp']);
    expect(state.upload.config).toBeNull();
    expect(state.status.text).toBe(
      'Stored scenario plant-ntp on the server and added it to the list. ' +
        'Save scenarios to keep the list in this diagram.',
    );
  });

  test('a scenario the server has is replaced only once the user confirms', async () => {
    const { state, store } = await open({ stored: ['plant-ntp'] });

    await state.onFile(chose('plant.yaml', SCENARIO_FILE));
    expect(state.replaces).toBe(true);
    expect(state.uploadHint).toBe(
      'The server has a scenario named plant-ntp: storing replaces its spec ' +
        'and keeps its annotations.',
    );

    state.storeUpload();
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();
    // The confirmation says what is replaced, and what is kept: the
    // stored scenario's annotations, its topologies among them.
    expect(state.confirming).toMatchObject({
      title: 'Replace scenario plant-ntp?',
      message:
        'Storing this file replaces the spec of the scenario "plant-ntp" on ' +
        'the server, for every diagram and experiment that uses it. Its ' +
        'annotations are kept, with those of the file added, so its ' +
        'topology annotation still names every topology it names. This ' +
        'cannot be undone.',
      confirmLabel: 'Replace scenario',
    });

    // Cancel stores nothing.
    state.confirming = null;
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();

    state.storeUpload();
    state.confirmReplace();
    await settle();

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(STORED, {
      replace: true,
    });
    expect(state.confirming).toBeNull();
    expect(state.names).toEqual(['plant-ntp']);
    expect(state.status.text).toBe(
      'Replaced scenario plant-ntp on the server and added it to the list. ' +
        'Save scenarios to keep the list in this diagram.',
    );
  });

  test('a stored name the list has is not listed again, and takes the stored spelling', async () => {
    const { state, store } = await open({
      scenarios: ['Plant-NTP', 'plant-dns'],
      stored: ['plant-dns'],
    });

    await state.onFile(chose('plant.yaml', SCENARIO_FILE));
    state.storeUpload();
    await settle();

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(STORED, {
      replace: false,
    });
    // The list names the config just stored, in its place in the list.
    expect(state.names).toEqual(['plant-ntp', 'plant-dns']);
    expect(state.status.text).toBe(
      'Stored scenario plant-ntp on the server, and the list names it ' +
        'plant-ntp in place of Plant-NTP. Save scenarios to keep the list ' +
        'in this diagram.',
    );

    // Stored again under the same name, the list is left as it is.
    await state.onFile(chose('plant.yaml', SCENARIO_FILE));
    state.storeUpload();
    await settle();

    expect(state.names).toEqual(['plant-ntp', 'plant-dns']);
    expect(state.status.text).toBe(
      'Stored scenario plant-ntp on the server. The list already names it.',
    );
  });

  test('the name comes from the file name when the file names none a config can have', async () => {
    const { state } = await open();

    await state.onFile(
      chose(
        'Plant NTP (v2).yml',
        SCENARIO_FILE.replace('name: plant-ntp', 'name: plant ntp'),
      ),
    );

    expect(state.upload.name).toBe('Plant-NTP-v2');

    // A name typed that a config cannot have is refused on its field.
    state.upload.name = 'plant/ntp';
    state.storeUpload();
    expect(state.error.field).toBe('uploadName');
    expect(state.error.text).toBe(
      'Scenario name "plant/ntp" may use only letters, numbers, underscores, at signs, periods and hyphens.',
    );
  });

  test('the server’s refusal is shown, and nothing is listed', async () => {
    const { state, store } = await open();

    store.saveScenarioConfig.mockRejectedValueOnce(
      Object.assign(new Error('status 403'), {
        response: {
          status: 403,
          data: { message: 'creating configs not allowed for alice' },
        },
      }),
    );

    await state.onFile(chose('plant.yaml', SCENARIO_FILE));
    state.storeUpload();
    await settle();

    expect(state.error.text).toBe(
      'Could not store scenario plant-ntp. Creating configs not allowed for alice.',
    );
    expect(state.names).toEqual([]);
    expect(state.upload.config).not.toBeNull();
  });

  test('a scenario of another version, or not a scenario, is refused', async () => {
    const { state } = await open();

    await state.onFile(
      chose(
        'old.yaml',
        SCENARIO_FILE.replace('phenix.sandia.gov/v2', 'phenix.sandia.gov/v1'),
      ),
    );
    expect(state.error.text).toMatch(
      /^This scenario is phenix\.sandia\.gov\/v1, and the Builder stores only phenix\.sandia\.gov\/v2 scenarios\./,
    );
    expect(state.upload.config).toBeNull();

    await state.onFile(
      chose(
        'topo.yaml',
        SCENARIO_FILE.replace('kind: Scenario', 'kind: Topology'),
      ),
    );
    expect(state.error.field).toBe('file');
    expect(state.upload.config).toBeNull();
  });
});
