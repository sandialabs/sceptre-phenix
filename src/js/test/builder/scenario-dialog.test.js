// @vitest-environment happy-dom
// The Scenario dialog, which edits the Scenario configs a diagram names, and
// its editor: mounted and driven as a user would.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
import ScenarioEditorDialog from '@/components/builder/dialogs/ScenarioEditorDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

// Opens the dialog in the page for a diagram listing `scenarios`, on a server
// listing `stored`.
async function open({ scenarios = [], stored = [], readOnly = false } = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);
  const { doc } = sampleDocument();

  store.doc = scenarios.length ? { ...doc, scenarios } : doc;
  store.sources = { ...store.sources, scenarios: stored };
  store.readOnly = readOnly;

  vi.spyOn(store, 'fetchSources').mockResolvedValue(store.sources);
  vi.spyOn(store, 'saveScenarioConfig').mockResolvedValue();
  vi.spyOn(store, 'setScenarios').mockReturnValue(null);

  const wrapper = mount(ScenarioDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await flushPromises();

  return { wrapper, store };
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

const byId = (wrapper, testId) => wrapper.find(`[data-testid="${testId}"]`);
const textOf = (wrapper, testId) => byId(wrapper, testId).text();
const box = (wrapper, name) => byId(wrapper, `scenario-check-${name}`);
const rowNames = (wrapper) =>
  wrapper
    .findAll('[data-testid^="scenario-row-"]')
    .map((row) => row.attributes('data-testid').slice('scenario-row-'.length));

// Focuses a control and clicks it, as a user's press does.
async function press(control) {
  control.element.focus();
  await control.trigger('click');
  await flushPromises();
}

// Chooses `name` with `text` in the scenario file field.
async function chooseFile(wrapper, name, text) {
  const field = byId(wrapper, 'scenario-file');

  Object.defineProperty(field.element, 'files', {
    configurable: true,
    value: [new File([text], name, { type: 'application/yaml' })],
  });
  await field.trigger('change');
  await flushPromises();
}

// Saves the list, and resolves to the names Save wrote into the diagram.
async function saved(wrapper, store) {
  await byId(wrapper, 'scenario-submit').trigger('click');

  return store.setScenarios.mock.lastCall?.[0];
}

describe('the list', () => {
  test('a scenario the server does not have is marked, with no View or Edit', async () => {
    const { wrapper, store } = await open({
      scenarios: ['gone'],
      stored: ['plant-ntp'],
    });
    const gone = box(wrapper, 'gone');

    expect(rowNames(wrapper)).toEqual(['gone', 'plant-ntp']);
    expect(gone.element.checked).toBe(true);
    expect(box(wrapper, 'plant-ntp').element.checked).toBe(false);
    expect(wrapper.get('#scenario-missing-0').text()).toBe(
      'Not found on the server',
    );
    expect(gone.attributes('aria-describedby')).toBe('scenario-missing-0');
    expect(byId(wrapper, 'scenario-view-gone').exists()).toBe(false);
    expect(byId(wrapper, 'scenario-edit-gone').exists()).toBe(false);

    // Its box cleared, its row stays, so focus has a place to stay.
    gone.element.focus();
    await gone.setValue(false);

    expect(rowNames(wrapper)).toEqual(['gone', 'plant-ntp']);
    expect(box(wrapper, 'gone').element.checked).toBe(false);
    expect(byId(wrapper, 'scenario-missing').exists()).toBe(true);
    expect(document.activeElement).toBe(box(wrapper, 'gone').element);
    expect(textOf(wrapper, 'scenario-count')).toBe('0 scenarios selected.');
    expect(await saved(wrapper, store)).toEqual([]);
  });

  test('a box set adds its scenario at the end, a box cleared removes it, and Save writes the list', async () => {
    const { wrapper, store } = await open({
      scenarios: ['plant-ntp', 'plant-dns'],
      stored: ['plant-ntp', 'plant-dns', 'plant-attack'],
    });

    await box(wrapper, 'plant-attack').setValue(true);

    // Nothing is written until Save, which writes it in one step.
    expect(store.setScenarios).not.toHaveBeenCalled();
    expect(await saved(wrapper, store)).toEqual([
      'plant-ntp',
      'plant-dns',
      'plant-attack',
    ]);

    await box(wrapper, 'plant-ntp').setValue(false);

    expect(await saved(wrapper, store)).toEqual(['plant-dns', 'plant-attack']);
    expect(store.setScenarios).toHaveBeenCalledTimes(2);
    expect(wrapper.emitted('close')).toHaveLength(2);
  });

  test('at the most a diagram lists, the other boxes and Create new scenario say why they do not apply', async () => {
    const listed = Array.from({ length: 20 }, (_, i) => `s${i}`);
    const { wrapper, store } = await open({
      scenarios: listed,
      stored: [...listed, 'plant-ntp'],
    });
    const other = box(wrapper, 'plant-ntp');
    const create = byId(wrapper, 'scenario-create');

    expect(byId(wrapper, 'scenario-limit').exists()).toBe(true);
    expect(other.attributes()).toHaveProperty('disabled');
    expect(other.attributes('aria-describedby')).toBe('scenario-limit');
    expect(create.attributes('aria-disabled')).toBe('true');
    expect(create.attributes('aria-describedby')).toBe('scenario-limit');

    await press(create);

    expect(textOf(wrapper, 'scenario-error')).toBe(
      'A diagram lists at most 20 scenarios. To select another, clear one first.',
    );
    expect(byId(wrapper, 'scenario-editor').exists()).toBe(false);
    expect(textOf(wrapper, 'scenario-count')).toBe('20 scenarios selected.');
    expect(document.activeElement).toBe(create.element);
    expect(await saved(wrapper, store)).toHaveLength(20);
  });

  test('a long list has a filter, which keeps the boxes set', async () => {
    const stored = Array.from({ length: 9 }, (_, i) => `plant-${i}`);
    const { wrapper, store } = await open({
      scenarios: ['plant-3'],
      stored: [...stored, 'attack'],
    });
    const filter = byId(wrapper, 'scenario-filter');

    expect(wrapper.get('label[for="scenario-filter"]').text()).toBe(
      'Filter scenarios',
    );
    expect(textOf(wrapper, 'scenario-filter-count')).toBe(
      'Showing all 10 scenarios.',
    );

    await filter.setValue('ATT');
    // Enter in the filter field does not save.
    await filter.trigger('keydown', { key: 'Enter' });

    expect(rowNames(wrapper)).toEqual(['attack']);
    expect(textOf(wrapper, 'scenario-filter-count')).toBe(
      'Showing 1 of 10 scenarios.',
    );
    expect(textOf(wrapper, 'scenario-count')).toBe('1 scenario selected.');
    expect(store.setScenarios).not.toHaveBeenCalled();
    expect(await saved(wrapper, store)).toEqual(['plant-3']);
  });
});

describe('View, Edit and Create new scenario', () => {
  const STORED_CONFIG = {
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: {
      name: 'plant-ntp',
      created: '2026-01-01T00:00:00Z',
      annotations: { topology: 'plant' },
    },
    spec: { apps: [{ name: 'ntp' }] },
  };

  const editorOf = (wrapper) => byId(wrapper, 'scenario-editor');

  test('View reads the stored config, and shows it in the editor read only', async () => {
    const { wrapper, store } = await open({ stored: ['plant-ntp'] });
    let answer;

    vi.spyOn(store, 'readScenarioConfig').mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );

    const view = byId(wrapper, 'scenario-view-plant-ntp');
    const edit = byId(wrapper, 'scenario-edit-plant-ntp');

    await press(view);

    // A spinner replaces the button while the config is read, and the
    // other buttons do nothing.
    expect(view.attributes('aria-busy')).toBe('true');
    expect(view.attributes('aria-label')).toBe('Loading plant-ntp');
    expect(edit.attributes('aria-busy')).toBeUndefined();
    expect(edit.attributes('aria-disabled')).toBe('true');
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Loading scenario plant-ntp…',
    );
    expect(document.activeElement).toBe(view.element);
    await edit.trigger('click');
    expect(store.readScenarioConfig).toHaveBeenCalledTimes(1);

    answer(STORED_CONFIG);
    await flushPromises();

    expect(store.readScenarioConfig).toHaveBeenCalledWith('plant-ntp');
    expect(view.attributes('aria-busy')).toBeUndefined();
    expect(edit.attributes('aria-disabled')).toBeUndefined();
    expect(byId(wrapper, 'scenario-status').exists()).toBe(false);
    expect(editorOf(wrapper).get('h2').text()).toBe('Scenario plant-ntp');
    expect(textOf(wrapper, 'scenario-editor-view')).toContain(
      "created: '2026-01-01T00:00:00Z'",
    );
    expect(document.activeElement).toBe(editorOf(wrapper).element);

    // Closing the editor returns focus to View.
    await byId(wrapper, 'scenario-editor-close').trigger('click');

    expect(editorOf(wrapper).exists()).toBe(false);
    expect(document.activeElement).toBe(view.element);
  });

  test('Edit shows the config without the times the server sets, and Save replaces it as it is', async () => {
    const { wrapper, store } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp'],
    });

    vi.spyOn(store, 'readScenarioConfig').mockResolvedValue(STORED_CONFIG);

    const edit = byId(wrapper, 'scenario-edit-plant-ntp');

    await press(edit);

    const field = byId(wrapper, 'scenario-editor-text');

    expect(editorOf(wrapper).get('h2').text()).toBe('Edit scenario plant-ntp');
    expect(field.element.value).not.toContain('created');
    expect(document.activeElement).toBe(field.element);

    await field.setValue(
      field.element.value.replace(/apps:[\s\S]*$/, 'apps: []\n'),
    );
    await byId(wrapper, 'scenario-editor-save').trigger('click');
    await flushPromises();

    const { created: _, ...metadata } = STORED_CONFIG.metadata;

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(
      { ...STORED_CONFIG, metadata, spec: { apps: [] } },
      { replace: true, exact: true },
    );
    expect(editorOf(wrapper).exists()).toBe(false);
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Saved scenario plant-ntp on the server.',
    );
    expect(document.activeElement).toBe(edit.element);
    expect(await saved(wrapper, store)).toEqual(['plant-ntp']);
  });

  test('a refusal to read shows, and the button comes back', async () => {
    const { wrapper, store } = await open({ stored: ['plant-ntp'] });

    vi.spyOn(store, 'readScenarioConfig').mockRejectedValue(
      Object.assign(new Error('status 403'), {
        response: { status: 403, data: { message: 'not allowed' } },
      }),
    );

    const edit = byId(wrapper, 'scenario-edit-plant-ntp');

    await press(edit);

    expect(textOf(wrapper, 'scenario-error')).toBe(
      'You do not have permission to read scenario plant-ntp.',
    );
    expect(editorOf(wrapper).exists()).toBe(false);
    expect(edit.attributes('aria-busy')).toBeUndefined();
    expect(edit.attributes('aria-disabled')).toBeUndefined();
    expect(document.activeElement).toBe(edit.element);
  });

  test('Create new scenario opens the editor with a new config, and Save creates and selects it', async () => {
    const { wrapper, store } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', 'Sample-scenario'],
    });
    const create = byId(wrapper, 'scenario-create');

    store.fetchSources.mockClear();
    await press(create);

    // The names are read again, so that the new one is unique.
    expect(store.fetchSources).toHaveBeenCalled();

    const field = byId(wrapper, 'scenario-editor-text');

    expect(editorOf(wrapper).get('h2').text()).toBe('Create new scenario');
    expect(field.element.value).toBe(
      [
        'apiVersion: phenix.sandia.gov/v2',
        'kind: Scenario',
        'metadata:',
        '  name: Sample-scenario-2',
        '  annotations:',
        '    topology: Sample',
        'spec:',
        '  apps: []',
        '',
      ].join('\n'),
    );
    expect(document.activeElement).toBe(field.element);
    // Nothing is stored before Save.
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();

    const config = {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'Sample-scenario-2' },
      spec: { apps: [] },
    };

    await field.setValue(
      'apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: Sample-scenario-2\nspec:\n  apps: []\n',
    );
    await byId(wrapper, 'scenario-editor-save').trigger('click');
    await flushPromises();

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(config, {
      replace: false,
      exact: true,
    });
    expect(editorOf(wrapper).exists()).toBe(false);
    expect(box(wrapper, 'Sample-scenario-2').element.checked).toBe(true);
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Created scenario Sample-scenario-2 on the server and added it to the ' +
        'list. Save scenarios to keep the list in this diagram.',
    );
    expect(document.activeElement).toBe(create.element);
    // Only Save scenarios writes the list into the diagram.
    expect(store.setScenarios).not.toHaveBeenCalled();
    expect(await saved(wrapper, store)).toEqual([
      'plant-ntp',
      'Sample-scenario-2',
    ]);
  });

  test("the server's refusal of a save is shown in the editor, and nothing is selected", async () => {
    const { wrapper, store } = await open();

    await press(byId(wrapper, 'scenario-create'));
    store.saveScenarioConfig.mockRejectedValueOnce(
      Object.assign(new Error('status 403'), { response: { status: 403 } }),
    );

    await byId(wrapper, 'scenario-editor-text').setValue(
      'apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: x\nspec:\n  apps: []\n',
    );
    await byId(wrapper, 'scenario-editor-save').trigger('click');
    await flushPromises();

    expect(store.saveScenarioConfig).toHaveBeenCalledTimes(1);
    expect(editorOf(wrapper).exists()).toBe(true);
    expect(textOf(wrapper, 'scenario-editor-error')).toBe(
      'You do not have permission to create scenario x.',
    );
    expect(textOf(wrapper, 'scenario-count')).toBe('0 scenarios selected.');
  });
});

describe('uploading a scenario file', () => {
  const nameField = (wrapper) => byId(wrapper, 'scenario-upload-name');

  test('a new scenario is created on the server and listed', async () => {
    const { wrapper, store } = await open({ stored: ['plant-attack'] });

    await chooseFile(wrapper, 'anything.yaml', SCENARIO_FILE);
    expect(nameField(wrapper).element.value).toBe('plant-ntp');
    expect(
      byId(wrapper, 'scenario-upload-hint').classes('builder-hint--warning'),
    ).toBe(false);
    expect(textOf(wrapper, 'scenario-upload-hint')).toBe(
      'Storing creates a new scenario on the server.',
    );

    await press(byId(wrapper, 'scenario-upload'));

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(STORED, {
      replace: false,
    });
    expect(nameField(wrapper).exists()).toBe(false);
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Stored scenario plant-ntp on the server and added it to the list. ' +
        'Save scenarios to keep the list in this diagram.',
    );
    // The name field is gone: focus goes back to the file field.
    expect(document.activeElement?.id).toBe('scenario-file');
    expect(await saved(wrapper, store)).toEqual(['plant-ntp']);
  });

  test('a scenario the server has is replaced only once the user confirms', async () => {
    const { wrapper, store } = await open({ stored: ['plant-ntp'] });

    await chooseFile(wrapper, 'plant.yaml', SCENARIO_FILE);
    expect(
      byId(wrapper, 'scenario-upload-hint').classes('builder-hint--warning'),
    ).toBe(true);
    expect(textOf(wrapper, 'scenario-upload-hint')).toBe(
      'The server has a scenario named plant-ntp. Storing replaces its spec ' +
        'and keeps its annotations.',
    );

    const storeButton = byId(wrapper, 'scenario-upload');

    await press(storeButton);
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();

    // The confirmation says what is replaced, and what is kept: the
    // stored scenario's annotations, its topologies among them.
    const confirm = byId(wrapper, 'builder-confirm');

    expect(confirm.get('h2').text()).toBe('Replace scenario plant-ntp?');
    expect(confirm.get('#scenario-confirm-message').text()).toBe(
      'Storing this file replaces the spec of the scenario "plant-ntp" on ' +
        'the server, for every diagram and experiment that uses it. The ' +
        'scenario keeps its annotations and also gets those of the file. ' +
        'Thus its topology annotation keeps every topology name that it ' +
        'has. You cannot undo this.',
    );
    expect(textOf(wrapper, 'confirm-accept')).toBe('Replace scenario');
    expect(document.activeElement).toBe(
      byId(wrapper, 'confirm-cancel').element,
    );

    // Cancel stores nothing, and returns focus to Store and add.
    await byId(wrapper, 'confirm-cancel').trigger('click');
    expect(byId(wrapper, 'builder-confirm').exists()).toBe(false);
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(storeButton.element);

    await press(storeButton);
    await byId(wrapper, 'confirm-accept').trigger('click');
    await flushPromises();

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(STORED, {
      replace: true,
    });
    expect(byId(wrapper, 'builder-confirm').exists()).toBe(false);
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Replaced scenario plant-ntp on the server and added it to the list. ' +
        'Save scenarios to keep the list in this diagram.',
    );
    expect(document.activeElement?.id).toBe('scenario-file');
    expect(await saved(wrapper, store)).toEqual(['plant-ntp']);
  });

  test('a stored name the list has is not listed again, and takes the stored spelling', async () => {
    const { wrapper, store } = await open({
      scenarios: ['Plant-NTP', 'plant-dns'],
      stored: ['plant-dns'],
    });

    await chooseFile(wrapper, 'plant.yaml', SCENARIO_FILE);
    await press(byId(wrapper, 'scenario-upload'));

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(STORED, {
      replace: false,
    });
    // The list names the config just stored, in its place in the list.
    expect(await saved(wrapper, store)).toEqual(['plant-ntp', 'plant-dns']);
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Stored scenario plant-ntp on the server, and the list names it ' +
        'plant-ntp in place of Plant-NTP. Save scenarios to keep the list ' +
        'in this diagram.',
    );

    // Stored again under the same name, the list is left as it is.
    await chooseFile(wrapper, 'plant.yaml', SCENARIO_FILE);
    await press(byId(wrapper, 'scenario-upload'));

    expect(await saved(wrapper, store)).toEqual(['plant-ntp', 'plant-dns']);
    expect(textOf(wrapper, 'scenario-status')).toBe(
      'Stored scenario plant-ntp on the server. The list already names it.',
    );
  });

  test('the name comes from the file name when the file names none a config can have', async () => {
    const { wrapper, store } = await open();

    await chooseFile(
      wrapper,
      'Plant NTP (v2).yml',
      SCENARIO_FILE.replace('name: plant-ntp', 'name: plant ntp'),
    );

    expect(nameField(wrapper).element.value).toBe('Plant-NTP-v2');

    // A name typed that a config cannot have is refused on its field, and
    // Enter in the field stores the upload as Store and add does.
    await nameField(wrapper).setValue('plant/ntp');
    await nameField(wrapper).trigger('keydown', { key: 'Enter' });
    await flushPromises();

    expect(textOf(wrapper, 'scenario-error')).toBe(
      'Scenario name "plant/ntp" may use only letters, numbers, underscores, at signs, periods and hyphens.',
    );
    expect(nameField(wrapper).attributes('aria-invalid')).toBe('true');
    expect(nameField(wrapper).attributes('aria-describedby')).toBe(
      'scenario-upload-hint scenario-error',
    );
    expect(document.activeElement).toBe(nameField(wrapper).element);
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();
  });

  test('the server’s refusal is shown, and nothing is listed', async () => {
    const { wrapper, store } = await open();

    store.saveScenarioConfig.mockRejectedValueOnce(
      Object.assign(new Error('status 403'), {
        response: {
          status: 403,
          data: { message: 'creating configs not allowed for alice' },
        },
      }),
    );

    await chooseFile(wrapper, 'plant.yaml', SCENARIO_FILE);
    await press(byId(wrapper, 'scenario-upload'));

    expect(textOf(wrapper, 'scenario-error')).toBe(
      'Could not store scenario plant-ntp. Creating configs not allowed for alice.',
    );
    expect(textOf(wrapper, 'scenario-count')).toBe('0 scenarios selected.');
    // The upload stays, its name field marked and focused, to try again.
    expect(nameField(wrapper).exists()).toBe(true);
    expect(document.activeElement).toBe(nameField(wrapper).element);
  });

  test('a scenario of another version, or not a scenario, is refused', async () => {
    const { wrapper } = await open();

    await chooseFile(
      wrapper,
      'old.yaml',
      SCENARIO_FILE.replace('phenix.sandia.gov/v2', 'phenix.sandia.gov/v1'),
    );
    expect(textOf(wrapper, 'scenario-error')).toMatch(
      /^This scenario is phenix\.sandia\.gov\/v1\. The Builder stores only phenix\.sandia\.gov\/v2 scenarios\./,
    );
    expect(nameField(wrapper).exists()).toBe(false);

    await chooseFile(
      wrapper,
      'topo.yaml',
      SCENARIO_FILE.replace('kind: Scenario', 'kind: Topology'),
    );

    const field = byId(wrapper, 'scenario-file');

    expect(field.attributes('aria-invalid')).toBe('true');
    expect(field.attributes('aria-describedby')).toBe('scenario-error');
    expect(nameField(wrapper).exists()).toBe(false);
  });
});

// Opens the scenario editor in the page with `props`.
function openEditor(props) {
  return mount(ScenarioEditorDialog, { attachTo: document.body, props });
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

describe('the scenario editor', () => {
  test('Save sends the config read from the text, and a busy Save keeps the dialog open', async () => {
    let done;
    const save = vi.fn(
      () =>
        new Promise((resolve) => {
          done = resolve;
        }),
    );
    const wrapper = openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save,
    });
    const saveButton = byId(wrapper, 'scenario-editor-save');

    await byId(wrapper, 'scenario-editor-text').setValue(
      EDIT_TEXT.replace('apps: []', 'apps:\n    - name: ntp'),
    );
    await saveButton.trigger('click');

    expect(save).toHaveBeenCalledWith({
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'plant-ntp' },
      spec: { apps: [{ name: 'ntp' }] },
    });
    expect(saveButton.attributes('aria-busy')).toBe('true');
    expect(saveButton.attributes('aria-label')).toBe(
      'Saving scenario plant-ntp',
    );
    expect(textOf(wrapper, 'scenario-editor-status')).toBe(
      'Saving scenario plant-ntp…',
    );

    // Cancel and Escape do nothing while Save runs, and Save is not sent
    // twice.
    await byId(wrapper, 'scenario-editor-cancel').trigger('click');
    await wrapper.get('dialog').trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('close')).toBeUndefined();
    expect(byId(wrapper, 'builder-confirm').exists()).toBe(false);
    await saveButton.trigger('click');
    expect(save).toHaveBeenCalledTimes(1);

    done();
    await flushPromises();
    expect(saveButton.attributes('aria-busy')).toBeUndefined();
  });

  test('a problem in the text shows on the field, and nothing is sent', async () => {
    const save = vi.fn();
    const wrapper = openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save,
    });
    const field = byId(wrapper, 'scenario-editor-text');

    await field.setValue(EDIT_TEXT.replace('name: plant-ntp', 'name: other'));
    await press(byId(wrapper, 'scenario-editor-save'));
    expect(textOf(wrapper, 'scenario-editor-error')).toBe(
      'The name must stay plant-ntp. To store the scenario with a new name, select Create new scenario.',
    );
    expect(field.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(field.element);

    await field.setValue('spec: [1, 2\nkind: x');
    await press(byId(wrapper, 'scenario-editor-save'));
    expect(textOf(wrapper, 'scenario-editor-error')).toMatch(
      /^The YAML is not valid: .*\(line \d+, column \d+\)$/,
    );
    expect(save).not.toHaveBeenCalled();
  });

  test("the server's refusal shows with each problem it found, and the field is marked for a refused config", async () => {
    const save = vi.fn().mockRejectedValueOnce(
      Object.assign(new Error('status 400'), {
        response: {
          status: 400,
          data: {
            message: 'apps[0]: missing name',
            metadata: { validation: 'apps[0]: missing name\n  hint: add it' },
          },
        },
      }),
    );
    const wrapper = openEditor({
      mode: 'create',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save,
    });
    const field = byId(wrapper, 'scenario-editor-text');
    const saveButton = byId(wrapper, 'scenario-editor-save');

    await press(saveButton);
    expect(textOf(wrapper, 'scenario-editor-error')).toBe(
      'Could not create scenario plant-ntp. Apps[0]: missing name.',
    );
    expect(
      wrapper
        .findAll('[data-testid="scenario-editor-problems"] li')
        .map((item) => item.element.textContent),
    ).toEqual(['apps[0]: missing name', '  hint: add it']);
    expect(field.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(field.element);
    expect(byId(wrapper, 'scenario-editor-status').exists()).toBe(false);
    expect(saveButton.attributes('aria-busy')).toBeUndefined();

    // A refusal of the user leaves Save focused, to try again.
    save.mockRejectedValueOnce(
      Object.assign(new Error('status 403'), { response: { status: 403 } }),
    );
    await press(saveButton);
    expect(textOf(wrapper, 'scenario-editor-error')).toBe(
      'You do not have permission to create scenario plant-ntp.',
    );
    expect(byId(wrapper, 'scenario-editor-problems').exists()).toBe(false);
    expect(field.attributes('aria-invalid')).toBeUndefined();
    expect(document.activeElement).toBe(saveButton.element);
  });

  test('Cancel asks before it discards a change, and closes at once without one', async () => {
    const wrapper = openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save: vi.fn(),
    });

    await byId(wrapper, 'scenario-editor-text').setValue(
      `${EDIT_TEXT}# changed\n`,
    );
    await byId(wrapper, 'scenario-editor-cancel').trigger('click');

    const confirm = byId(wrapper, 'builder-confirm');

    expect(confirm.exists()).toBe(true);
    expect(confirm.get('#scenario-editor-confirm-message').text()).toBe(
      'You changed scenario plant-ntp. If you close the editor, you lose your changes.',
    );
    expect(document.activeElement).toBe(
      byId(wrapper, 'confirm-cancel').element,
    );
    expect(wrapper.emitted('close')).toBeUndefined();

    await byId(wrapper, 'confirm-accept').trigger('click');
    expect(wrapper.emitted('close')).toHaveLength(1);

    const again = openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save: vi.fn(),
    });

    await byId(again, 'scenario-editor-cancel').trigger('click');
    expect(byId(again, 'builder-confirm').exists()).toBe(false);
    expect(again.emitted('close')).toHaveLength(1);
  });
});
