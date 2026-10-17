// The Scenario dialog, which edits the Scenario configs a diagram names:
// rendered on the server, then driven through its setup state.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderBusyButton from '@/components/builder/BuilderBusyButton.vue';
import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
import ScenarioEditorDialog from '@/components/builder/dialogs/ScenarioEditorDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';

// The dialog for a diagram listing `scenarios`, on a server listing `stored`;
// `state` is its setup state, whose functions its controls call.
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
  test('lists every scenario, sorted by name, with a box set for each the diagram lists', async () => {
    const { html, state } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', { name: 'plant-attack' }],
    });
    const boxes = tags(html, 'input').filter((tag) =>
      tag.includes('type="checkbox"'),
    );

    expect(html).toMatch(/<h2 id="scenario-dialog-title"[^>]*>Scenarios<\/h2>/);
    expect(html).toMatch(/<legend[^>]*>Scenarios of this diagram<\/legend>/);
    expect(state.rows.map((row) => row.name)).toEqual([
      'plant-attack',
      'plant-ntp',
    ]);
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

  test('a scenario the server does not have is marked, with no View or Edit', async () => {
    const { html, state } = await open({
      scenarios: ['gone'],
      stored: ['plant-ntp'],
    });

    expect(state.rows).toEqual([
      { name: 'gone', checked: true, missing: true },
      { name: 'plant-ntp', checked: false, missing: false },
    ]);
    expect(html).toMatch(
      /<span id="scenario-missing-0"[^>]*>\s*Not found on the server\s*<\/span>/,
    );
    expect(html).toContain('aria-describedby="scenario-missing-0"');
    expect(html).not.toContain('scenario-view-gone');
    expect(html).not.toContain('scenario-edit-gone');

    // Its box cleared, its row stays, so focus has a place to stay.
    state.toggle(state.rows[0], false);
    expect(state.names).toEqual([]);
    expect(state.rows[0]).toEqual({
      name: 'gone',
      checked: false,
      missing: true,
    });
  });

  test('says when there are no scenarios', async () => {
    const { html } = await open();

    expect(html).toContain('data-testid="scenario-none"');
    expect(html).toContain('0 scenarios selected.');
  });

  test('a box set adds its scenario at the end, a box cleared removes it, and Save writes the list', async () => {
    const { state, store } = await open({
      scenarios: ['plant-ntp', 'plant-dns'],
      stored: ['plant-ntp', 'plant-dns', 'plant-attack'],
    });
    const row = (name) => state.rows.find((item) => item.name === name);

    state.toggle(row('plant-attack'), true);
    expect(state.names).toEqual(['plant-ntp', 'plant-dns', 'plant-attack']);

    state.toggle(row('plant-ntp'), false);
    expect(state.names).toEqual(['plant-dns', 'plant-attack']);

    // Nothing is written until Save, which writes it in one step.
    expect(store.setScenarios).not.toHaveBeenCalled();
    state.submit();
    expect(store.setScenarios).toHaveBeenCalledWith([
      'plant-dns',
      'plant-attack',
    ]);
  });

  test('at the most a diagram lists, the other boxes and Create new scenario say why they do not apply', async () => {
    const listed = Array.from({ length: 20 }, (_, i) => `s${i}`);
    const { html, state } = await open({
      scenarios: listed,
      stored: [...listed, 'plant-ntp'],
    });
    const box = tags(html, 'input').find((tag) =>
      tag.includes('data-testid="scenario-check-plant-ntp"'),
    );
    const create = tags(html, 'button').find((tag) =>
      tag.includes('data-testid="scenario-create"'),
    );

    expect(html).toContain('data-testid="scenario-limit"');
    expect(box).toMatch(/\sdisabled/);
    expect(box).toContain('aria-describedby="scenario-limit"');
    expect(create).toContain('aria-disabled="true"');
    expect(create).toContain('aria-describedby="scenario-limit"');

    await state.createNew();
    expect(state.error.text).toBe(
      'A diagram lists at most 20 scenarios. To select another, clear one first.',
    );
    expect(state.editor).toBeNull();
    expect(state.names).toHaveLength(20);
  });

  test('a long list has a filter, which keeps the boxes set', async () => {
    const stored = Array.from({ length: 9 }, (_, i) => `plant-${i}`);
    const { html, state } = await open({
      scenarios: ['plant-3'],
      stored: [...stored, 'attack'],
    });

    expect(html).toMatch(/<label for="scenario-filter"[^>]*>Filter scenarios/);
    expect(html).toContain('Showing all 10 scenarios.');

    state.filter = 'ATT';
    expect(state.shown.map((row) => row.name)).toEqual(['attack']);
    expect(state.shownText).toBe('Showing 1 of 10 scenarios.');
    expect(state.names).toEqual(['plant-3']);
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

  test('View reads the stored config, and shows it in the editor read only', async () => {
    const { state, store } = await open({ stored: ['plant-ntp'] });
    let answer;

    vi.spyOn(store, 'readScenarioConfig').mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );

    const opening = state.openScenario('plant-ntp', 'view');

    // A spinner replaces the button while the config is read, and the
    // other buttons do nothing.
    expect(state.isPending('view', 'plant-ntp')).toBe(true);
    expect(state.isPending('edit', 'plant-ntp')).toBe(false);
    expect(state.status.text).toBe('Loading scenario plant-ntp…');
    await state.openScenario('plant-ntp', 'edit');
    expect(store.readScenarioConfig).toHaveBeenCalledTimes(1);

    answer(STORED_CONFIG);
    await opening;

    expect(store.readScenarioConfig).toHaveBeenCalledWith('plant-ntp');
    expect(state.pending).toBeNull();
    expect(state.status.text).toBe('');
    expect(state.editor).toMatchObject({ mode: 'view', name: 'plant-ntp' });
    expect(state.editor.text).toContain("created: '2026-01-01T00:00:00Z'");
  });

  test('Edit shows the config without the times the server sets, and Save replaces it as it is', async () => {
    const { state, store } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp'],
    });

    vi.spyOn(store, 'readScenarioConfig').mockResolvedValue(STORED_CONFIG);
    await state.openScenario('plant-ntp', 'edit');

    expect(state.editor.mode).toBe('edit');
    expect(state.editor.text).not.toContain('created');

    const edited = { ...STORED_CONFIG, spec: { apps: [] } };

    await state.saveEdited(edited);

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(edited, {
      replace: true,
      exact: true,
    });
    expect(state.editor).toBeNull();
    expect(state.status.text).toBe('Saved scenario plant-ntp on the server.');
    expect(state.names).toEqual(['plant-ntp']);
  });

  test('a refusal to read shows, and the button comes back', async () => {
    const { state, store } = await open({ stored: ['plant-ntp'] });

    vi.spyOn(store, 'readScenarioConfig').mockRejectedValue(
      Object.assign(new Error('status 403'), {
        response: { status: 403, data: { message: 'not allowed' } },
      }),
    );
    await state.openScenario('plant-ntp', 'edit');

    expect(state.error.text).toBe(
      'You do not have permission to read scenario plant-ntp.',
    );
    expect(state.editor).toBeNull();
    expect(state.pending).toBeNull();
  });

  test('Create new scenario opens the editor with a new config, and Save creates and selects it', async () => {
    const { state, store } = await open({
      scenarios: ['plant-ntp'],
      stored: ['plant-ntp', 'Sample-scenario'],
    });

    await state.createNew();

    // The names are read again, so that the new one is unique.
    expect(store.fetchSources).toHaveBeenCalled();
    expect(state.editor).toMatchObject({
      mode: 'create',
      name: 'Sample-scenario-2',
    });
    expect(state.editor.text).toBe(
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
    // Nothing is stored before Save.
    expect(store.saveScenarioConfig).not.toHaveBeenCalled();

    const config = {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'Sample-scenario-2' },
      spec: { apps: [] },
    };

    await state.saveEdited(config);

    expect(store.saveScenarioConfig).toHaveBeenCalledWith(config, {
      replace: false,
      exact: true,
    });
    expect(state.editor).toBeNull();
    expect(state.names).toEqual(['plant-ntp', 'Sample-scenario-2']);
    expect(state.status.text).toBe(
      'Created scenario Sample-scenario-2 on the server and added it to the ' +
        'list. Save scenarios to keep the list in this diagram.',
    );
    // Only Save scenarios writes the list into the diagram.
    expect(store.setScenarios).not.toHaveBeenCalled();
  });

  test("the server's refusal of a save is thrown to the editor, and nothing is selected", async () => {
    const { state, store } = await open();

    await state.createNew();
    store.saveScenarioConfig.mockRejectedValueOnce(new Error('status 403'));

    await expect(
      state.saveEdited({
        apiVersion: 'phenix.sandia.gov/v2',
        kind: 'Scenario',
        metadata: { name: 'x' },
        spec: { apps: [] },
      }),
    ).rejects.toThrow('status 403');
    expect(state.editor).not.toBeNull();
    expect(state.names).toEqual([]);
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
        'the server, for every diagram and experiment that uses it. The ' +
        'scenario keeps its annotations and also gets those of the file. ' +
        'Thus its topology annotation keeps every topology name that it ' +
        'has. You cannot undo this.',
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

// The scenario editor with `props`, its setup state, and what it emitted.
async function openEditor(props) {
  const closed = vi.fn();
  const app = createSSRApp({
    render: () => h(ScenarioEditorDialog, { ...props, onClose: closed }),
  });
  let state = null;

  app.mixin({
    created() {
      if (this.$.type === ScenarioEditorDialog) {
        state = this.$.setupState;
      }
    },
  });

  const html = await renderToString(app);

  return { html, state, closed };
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
    expect(html).toMatch(/<button[^>]*scenario-editor-copy[^>]*>\s*Copy YAML/);
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
    expect(field).toContain('aria-describedby="scenario-editor-hint"');
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
    expect(html).toMatch(/scenario-editor-save[^>]*>[\s\S]*?Create scenario/);
  });

  test('Save sends the config read from the text, and a busy Save keeps the dialog open', async () => {
    let done;
    const save = vi.fn(
      () =>
        new Promise((resolve) => {
          done = resolve;
        }),
    );
    const { state, closed } = await openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save,
    });

    state.draft = EDIT_TEXT.replace('apps: []', 'apps:\n    - name: ntp');

    const saving = state.submit();

    expect(save).toHaveBeenCalledWith({
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'plant-ntp' },
      spec: { apps: [{ name: 'ntp' }] },
    });
    expect(state.saving).toBe(true);
    expect(state.status.text).toBe('Saving scenario plant-ntp…');

    // Cancel and Escape do nothing while Save runs, and Save is not sent
    // twice.
    state.requestClose();
    expect(closed).not.toHaveBeenCalled();
    expect(state.confirming).toBe(false);
    state.submit();
    expect(save).toHaveBeenCalledTimes(1);

    done();
    await saving;
    expect(state.saving).toBe(false);
  });

  test('a problem in the text shows on the field, and nothing is sent', async () => {
    const save = vi.fn();
    const { state } = await openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save,
    });

    state.draft = EDIT_TEXT.replace('name: plant-ntp', 'name: other');
    await state.submit();
    expect(state.error.text).toBe(
      'The name must stay plant-ntp. To store the scenario with a new name, select Create new scenario.',
    );
    expect(state.error.field).toBe('text');

    state.draft = 'spec: [1, 2\nkind: x';
    await state.submit();
    expect(state.error.text).toMatch(
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
    const { state } = await openEditor({
      mode: 'create',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save,
    });

    await state.submit();
    expect(state.error.text).toBe(
      'Could not create scenario plant-ntp. Apps[0]: missing name.',
    );
    expect(state.problems).toEqual(['apps[0]: missing name', '  hint: add it']);
    expect(state.error.field).toBe('text');
    expect(state.status.text).toBe('');
    expect(state.saving).toBe(false);

    // A refusal of the user leaves Save focused, to try again.
    save.mockRejectedValueOnce(
      Object.assign(new Error('status 403'), { response: { status: 403 } }),
    );
    await state.submit();
    expect(state.error.text).toBe(
      'You do not have permission to create scenario plant-ntp.',
    );
    expect(state.problems).toEqual([]);
    expect(state.error.field).toBe('');
  });

  test('Cancel asks before it discards a change, and closes at once without one', async () => {
    const { state, closed } = await openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save: vi.fn(),
    });

    state.draft = `${EDIT_TEXT}# changed\n`;
    state.requestClose();
    expect(state.confirming).toBe(true);
    expect(state.discardMessage).toBe(
      'You changed scenario plant-ntp. If you close the editor, your changes are lost.',
    );
    expect(closed).not.toHaveBeenCalled();

    state.discard();
    expect(closed).toHaveBeenCalledTimes(1);

    const again = await openEditor({
      mode: 'edit',
      name: 'plant-ntp',
      text: EDIT_TEXT,
      save: vi.fn(),
    });

    again.state.requestClose();
    expect(again.state.confirming).toBe(false);
    expect(again.closed).toHaveBeenCalledTimes(1);
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
