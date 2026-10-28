// @vitest-environment happy-dom
// The Publish dialog, mounted and driven as a user would: what publishing
// changes as the form changes, and a partial publication's result.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
import { PREVIEW_DELAY_MS } from '@/components/builder/dialogs/publishPreview.js';
import { connect } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import axiosInstance from '@/utils/axios.js';

import { sampleDocument } from './fixtures.js';

// Only Publish itself reaches the client: the store's other reads are spied.
vi.mock('@/utils/axios.js', () => ({ default: { post: vi.fn() } }));

enableAutoUnmount(afterEach);

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

// The sample diagram with every interface connected, which publishes. Or the
// diagram as it is, with an interface that has no VLAN, which Publish refuses.
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

// Opens the dialog in the page. `setup` changes the store first, and
// `previewPublish` answers each dry run.
function open({
  publishable = true,
  setup = () => {},
  previewPublish = async () => null,
} = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = diagram({ publishable });
  setup(store);

  vi.spyOn(store, 'fetchSources').mockResolvedValue(store.sources);
  vi.spyOn(store, 'fetchDocuments').mockResolvedValue([]);
  vi.spyOn(store, 'previewPublish').mockImplementation(previewPublish);

  const wrapper = mount(PublishDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store, pinia };
}

const byId = (wrapper, testId) => wrapper.find(`[data-testid="${testId}"]`);

// What publishing changes, as its section shows it.
const section = (wrapper) => byId(wrapper, 'publish-preview');

// Resolves to a promise the test settles, and the function that settles it.
function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });

  return { promise, resolve };
}

// A draft whose changes the server holds, which store.publish requires.
function savedDraft(store) {
  const saved = { ...store.saveState, status: 'saved', pending: 0 };

  store.draftId = 'd1';
  store.saveState = saved;
  store.autosave = {
    flush: async () => saved,
    hold: () => () => {},
    idle: async () => {},
    sent: 0,
    setETag: async () => {},
    dispose: () => {},
  };
}

// A dry run's answer: topology `name` is updated, and nothing else changes.
const changesTo = (name) => ({
  topology: { name, action: 'update' },
  experiment: null,
  includes: [],
  scenarios: [],
  images: [],
  vlanAliases: [],
});

describe('what publishing changes', () => {
  const changes = changesTo('lab');
  const legacyWarning = {
    code: 'publish.legacy.replaced',
    severity: 'warning',
    message:
      'This diagram replaced the legacy Builder diagram of topology lab.',
  };

  test('lists the dry run’s warnings after its changes, by severity and with their codes', async () => {
    const { wrapper } = open({
      previewPublish: async () => ({
        changes,
        errorIssues: [],
        warningIssues: [legacyWarning],
      }),
    });

    await flushPromises();

    const shown = section(wrapper);
    const issues = byId(shown, 'publish-preview-issues');

    expect(shown.attributes('aria-busy')).toBe('false');
    expect(shown.text()).toContain('Updates Topology config lab');
    expect(issues.exists()).toBe(true);
    expect(byId(shown, 'publish-preview-issues-warning').exists()).toBe(true);
    expect(byId(shown, 'publish-preview-issues-error').exists()).toBe(false);
    expect(shown.text()).toContain('1 warning');
    expect(issues.get('strong').text()).toBe('Warning:');
    expect(byId(issues, 'issue-code').text()).toBe('publish.legacy.replaced');
    expect(
      byId(shown, 'publish-preview-configs').element.compareDocumentPosition(
        issues.element,
      ) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  test('while it is read again, keeps the last answer shown, marked busy', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    const answers = [deferred(), deferred()];
    const { wrapper, store } = open({
      previewPublish: () =>
        answers[store.previewPublish.mock.calls.length - 1].promise,
    });

    await flushPromises();
    answers[0].resolve({ changes, errorIssues: [], warningIssues: [] });
    await flushPromises();
    expect(section(wrapper).attributes('aria-busy')).toBe('false');

    await byId(wrapper, 'publish-name').setValue('lab-2');

    // Busy at once, while the pause in typing runs, and while it is read.
    for (const wait of [0, PREVIEW_DELAY_MS]) {
      vi.advanceTimersByTime(wait);
      await flushPromises();

      expect(section(wrapper).attributes('aria-busy')).toBe('true');
      expect(section(wrapper).text()).toContain('Updates Topology config lab');
      expect(section(wrapper).text()).not.toContain(
        'Calculating what publishing changes',
      );
    }

    expect(store.previewPublish).toHaveBeenCalledTimes(2);
    answers[1].resolve({
      changes: changesTo('lab-2'),
      errorIssues: [],
      warningIssues: [],
    });
    await flushPromises();

    expect(section(wrapper).attributes('aria-busy')).toBe('false');
    expect(section(wrapper).text()).toContain('Updates Topology config lab-2');
  });

  test('the topology, experiment and scenario are read once typing pauses, and only the latest answer shows, whichever comes first', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    for (const latestFirst of [true, false]) {
      const answers = [deferred(), deferred(), deferred()];
      const { wrapper, store } = open({
        setup: (builder) => {
          builder.doc = {
            ...builder.doc,
            scenarios: ['plant-ntp', 'plant-dns'],
          };
        },
        previewPublish: () =>
          answers[store.previewPublish.mock.calls.length - 1].promise,
      });

      await flushPromises();
      answers[0].resolve({ changes, errorIssues: [], warningIssues: [] });
      await flushPromises();

      await byId(wrapper, 'publish-mode-experiment').setValue();
      await byId(wrapper, 'publish-name').setValue('plant');
      await byId(wrapper, 'publish-experiment').setValue('run-1');
      await byId(wrapper, 'publish-scenario-select').setValue('plant-dns');

      // Each change restarts the pause: nothing is asked until it ends.
      vi.advanceTimersByTime(PREVIEW_DELAY_MS - 1);
      await flushPromises();
      expect(store.previewPublish).toHaveBeenCalledTimes(1);
      vi.advanceTimersByTime(1);
      await flushPromises();
      expect(store.previewPublish).toHaveBeenCalledTimes(2);
      expect(store.previewPublish).toHaveBeenLastCalledWith({
        mode: 'topology-experiment',
        topology: { name: 'plant', action: 'create' },
        scenario: { name: 'plant-dns' },
        experiment: { name: 'run-1', action: 'create' },
      });

      // A newer form is read before the older answer comes.
      await byId(wrapper, 'publish-experiment').setValue('run-2');
      vi.advanceTimersByTime(PREVIEW_DELAY_MS);
      await flushPromises();
      expect(store.previewPublish).toHaveBeenCalledTimes(3);

      const older = {
        changes: changesTo('older'),
        errorIssues: [],
        warningIssues: [],
      };
      const latest = {
        changes: changesTo('latest'),
        errorIssues: [],
        warningIssues: [],
      };
      const order = latestFirst
        ? [() => answers[2].resolve(latest), () => answers[1].resolve(older)]
        : [() => answers[1].resolve(older), () => answers[2].resolve(latest)];

      order[0]();
      await flushPromises();
      expect(
        section(wrapper).attributes('aria-busy'),
        `latest first: ${latestFirst}`,
      ).toBe(latestFirst ? 'false' : 'true');
      expect(section(wrapper).text()).not.toContain('Topology config older');

      order[1]();
      await flushPromises();
      expect(section(wrapper).attributes('aria-busy')).toBe('false');
      expect(section(wrapper).text()).toContain(
        'Updates Topology config latest',
      );
      expect(section(wrapper).text()).not.toContain('Topology config older');

      wrapper.unmount();
    }
  });

  test('a refusal is listed as errors, with no changes', async () => {
    const refusal = {
      code: 'publish.topology.exists',
      severity: 'error',
      message: 'config lab already exists',
    };
    const { wrapper } = open({
      previewPublish: async () => ({
        changes: null,
        errorIssues: [refusal],
        warningIssues: [legacyWarning],
      }),
    });

    await flushPromises();

    const shown = section(wrapper);

    expect(byId(shown, 'publish-preview-issues-error').exists()).toBe(true);
    expect(byId(shown, 'publish-preview-issues-warning').exists()).toBe(true);
    expect(shown.text()).toContain('1 error');
    expect(shown.text()).not.toContain('blocks publishing');
    expect(byId(shown, 'publish-preview-refuses').text()).toBe(
      'The server would refuse this.',
    );
    expect(byId(shown, 'publish-preview-configs').exists()).toBe(false);
    expect(byId(shown, 'publish-preview-checks').exists()).toBe(false);
  });

  test('a refusal leaves out what the checks list, and says their errors block publishing', async () => {
    const clash = {
      code: 'publish.include.clash',
      severity: 'error',
      message: 'hostname "bravo" is also a device of included topology plant',
    };
    let errorIssues = [];
    // The server refuses bravo's eth0, which has no VLAN, as the checks do.
    const setup = (store) => {
      const bravo = store.doc.nodes.find(
        (node) => node.device?.hostname === 'bravo',
      );

      errorIssues = [
        {
          code: 'interface.vlan.missing',
          severity: 'error',
          message:
            'interface "eth0" of device "bravo" has no VLAN: connect it to a network, or type a VLAN for it',
          nodeId: bravo.id,
        },
        clash,
      ];
    };
    const { wrapper } = open({
      publishable: false,
      setup,
      previewPublish: async () => ({
        changes: null,
        errorIssues,
        warningIssues: [],
      }),
    });

    await flushPromises();

    const text = section(wrapper).text();

    expect(text).toContain('The errors listed under Checks block publishing.');
    expect(text).toContain('The server would refuse this.');
    expect(text).toContain(clash.message);
    expect(text).not.toContain('has no VLAN');
  });

  test('a problem only the server finds is listed once after a refused Publish, whichever answer came first', async () => {
    const missing = {
      code: 'publish.scenario.missing',
      severity: 'error',
      message: 'scenario plant-ntp does not exist',
    };

    for (const refusalFirst of [true, false]) {
      const { wrapper, store } = open({
        previewPublish: async () => ({
          changes: null,
          errorIssues: [missing],
          warningIssues: [],
        }),
      });

      // The dialog clears the refusal when it opens, and asks before the
      // answer comes.
      if (refusalFirst) {
        store.publishIssues = [missing];
      }

      await flushPromises();

      if (!refusalFirst) {
        store.publishIssues = [missing];
        await flushPromises();
      }

      const refusal = byId(wrapper, 'publish-refusal');

      expect(refusal.exists(), `refusal first: ${refusalFirst}`).toBe(true);
      expect(refusal.text()).toContain(missing.message);
      expect(section(wrapper).text()).not.toContain(missing.message);
      expect(byId(wrapper, 'publish-preview-issues').exists()).toBe(false);
      expect(wrapper.text().split(missing.message)).toHaveLength(2);

      wrapper.unmount();
    }
  });

  test('a dry run that cannot be read says Publish still works only while it does', async () => {
    const failure = {
      failed: true,
      message: 'Could not read what publishing changes: the server is down.',
    };

    for (const publishable of [true, false]) {
      const { wrapper } = open({
        publishable,
        previewPublish: async () => failure,
      });

      await flushPromises();

      const shown = byId(section(wrapper), 'publish-preview-error').text();
      const submit = byId(wrapper, 'publish-submit');

      if (publishable) {
        expect(shown).toBe(`${failure.message} You can still publish.`);
        expect(submit.attributes()).not.toHaveProperty('disabled');
      } else {
        // The checks' errors keep Publish unavailable, which nothing
        // contradicts.
        expect(shown).toBe(failure.message);
        expect(submit.attributes()).toHaveProperty('disabled');
        expect(wrapper.text()).not.toContain('You can still publish');
      }

      wrapper.unmount();
    }
  });
});

describe('a publication that fails part way', () => {
  // The server's partial-failure answer (writePublishPartial), as
  // builder-publish.spec.js mocks it.
  const partial = (topology) => ({
    status: 'partial',
    stages: [
      {
        name: 'document',
        status: 'created',
        message: 'phenix stored the immutable Builder document',
      },
      { name: 'topology', status: 'created', config: `Topology/${topology}` },
      {
        name: 'experiment',
        status: 'failed',
        message: 'experiment publication failed',
      },
    ],
    warnings: [],
    errors: ['experiment publication failed'],
  });

  test('lists the failed stage, moves focus to the result, and Back restores the form', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    const topology = 'partial';
    const experiment = 'partial-exp';

    axiosInstance.post.mockRejectedValue({
      response: { status: 500, data: partial(topology) },
    });

    const { wrapper, store } = open({
      setup: savedDraft,
      previewPublish: async () => ({
        changes: changesTo(topology),
        errorIssues: [],
        warningIssues: [],
      }),
    });

    await flushPromises();
    await byId(wrapper, 'publish-mode-experiment').setValue();
    await byId(wrapper, 'publish-name').setValue(topology);
    await byId(wrapper, 'publish-experiment').setValue(experiment);

    const submit = byId(wrapper, 'publish-submit');

    expect(submit.text()).toBe('Create topology and experiment');
    await submit.trigger('click');
    await flushPromises();

    expect(axiosInstance.post).toHaveBeenCalledWith(
      'builder/drafts/d1/publish',
      expect.objectContaining({
        topology: { name: topology, action: 'create' },
        experiment: { name: experiment, action: 'create' },
      }),
      expect.anything(),
    );

    // The result replaces the form, and focus moves to its summary.
    const result = byId(wrapper, 'publish-result');
    const summary = byId(result, 'publish-summary');

    expect(wrapper.find('form').exists()).toBe(false);
    expect(summary.text()).toBe(
      'Published with failures. Publish wrote some configs. The list below shows the failed stages.',
    );
    expect(document.activeElement).toBe(summary.element);
    expect(
      Object.fromEntries(
        result
          .findAll('li[data-status]')
          .map((item) => [
            item.get('strong').text().replace(/:$/, ''),
            item.attributes('data-status'),
          ]),
      ),
    ).toEqual({
      document: 'created',
      topology: 'created',
      experiment: 'failed',
    });
    expect(
      result.get('li[data-status="failed"]').attributes('data-level'),
    ).toBe('error');
    expect(
      result.get('li[data-status="created"]').attributes('data-level'),
    ).toBe('info');
    expect(result.get('li[data-level="error"]:not([data-status])').text()).toBe(
      'Error: experiment publication failed',
    );

    // Under a heading that counts the errors. It names no element of the
    // diagram, so it has no Go to.
    const reported = byId(result, 'publish-result-issues');

    expect(reported.findAll('h3').map((heading) => heading.text())).toEqual([
      '1 error',
    ]);
    expect(byId(reported, 'issue-go-to').exists()).toBe(false);
    // The dialog reports the failure in place of its form's alert. The page
    // holds its own alert and announcement while the modal is open (e2e).
    expect(byId(wrapper, 'publish-error').exists()).toBe(false);
    expect(store.error).toBe(
      'Publish finished with failures. It wrote some configs.',
    );

    await byId(result, 'publish-retry').trigger('click');
    await flushPromises();

    expect(byId(wrapper, 'publish-result').exists()).toBe(false);
    expect(byId(wrapper, 'publish-name').element.value).toBe(topology);
    expect(byId(wrapper, 'publish-experiment').element.value).toBe(experiment);
    expect(byId(wrapper, 'publish-submit').attributes()).not.toHaveProperty(
      'disabled',
    );
    // Back replaces the result, and the button that had focus, with the form.
    expect(document.activeElement).toBe(
      byId(wrapper, 'publish-submit').element,
    );
  });
});

describe('host pins that an experiment update removes', () => {
  // The server's warning (hostPinsWarning in builder_publish.go), for a
  // device renamed and a device deleted.
  const pinsWarning = {
    code: 'publish.experiment.pins-removed',
    severity: 'warning',
    message:
      'Publish removed the host pins of VMs that are no longer in the topology: plc-1 (on compute1, renamed to plc-1b), plc-2 (on compute3).',
  };

  test('what publishing changes warns of them, with their hosts', async () => {
    const { wrapper } = open({
      previewPublish: async () => ({
        changes: {
          ...changesTo('lab'),
          experiment: { name: 'lab-exp', action: 'update' },
        },
        errorIssues: [],
        warningIssues: [pinsWarning],
      }),
    });

    await flushPromises();

    const issues = byId(section(wrapper), 'publish-preview-issues');

    expect(section(wrapper).text()).toContain(
      'Updates Experiment config lab-exp',
    );
    expect(byId(issues, 'publish-preview-issues-warning').text()).toContain(
      '1 warning',
    );
    expect(byId(issues, 'issue-message').text()).toBe(
      `Warning: ${pinsWarning.message}`,
    );
    expect(byId(issues, 'issue-code').text()).toBe(
      'publish.experiment.pins-removed',
    );
    expect(byId(issues, 'issue-go-to').exists()).toBe(false);
  });

  test('the result of the publication warns of them after its stages', async () => {
    axiosInstance.post.mockResolvedValue({
      headers: {},
      data: {
        status: 'succeeded',
        stages: [
          { name: 'document', status: 'created' },
          { name: 'topology', status: 'updated', config: 'Topology/lab' },
          {
            name: 'experiment',
            status: 'updated',
            config: 'Experiment/lab-exp',
          },
          { name: 'draft', status: 'ok' },
        ],
        warnings: [pinsWarning],
        errors: [],
        removedHostPins: [
          { hostname: 'plc-1', host: 'compute1', renamedTo: 'plc-1b' },
          { hostname: 'plc-2', host: 'compute3' },
        ],
      },
    });

    const { wrapper } = open({ setup: savedDraft });

    await flushPromises();
    await byId(wrapper, 'publish-mode-experiment').setValue();
    await byId(wrapper, 'publish-name').setValue('lab');
    await byId(wrapper, 'publish-experiment').setValue('lab-exp');
    await byId(wrapper, 'publish-submit').trigger('click');
    await flushPromises();

    const result = byId(wrapper, 'publish-result');
    const reported = byId(result, 'publish-result-issues');

    expect(byId(result, 'publish-summary').text()).toBe(
      'Published. Every stage succeeded.',
    );
    expect(reported.findAll('h3').map((heading) => heading.text())).toEqual([
      '1 warning',
    ]);
    expect(byId(reported, 'issue-message').text()).toBe(
      `Warning: ${pinsWarning.message}`,
    );
    expect(byId(reported, 'issue-code').text()).toBe(
      'publish.experiment.pins-removed',
    );
    expect(
      byId(result, 'publish-summary').element.compareDocumentPosition(
        reported.element,
      ) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

describe('a source config that changed', () => {
  const sourceChanged = {
    code: 'publish.source.changed',
    severity: 'error',
    message:
      'this diagram comes from topology lab. That topology changed on the server after Builder made the diagram. ' +
      'Publish refuses this diagram under any name. ' +
      'To continue, make a new draft of topology lab: import it again or open its published diagram. ' +
      'Then copy your changes to the new draft',
  };

  test('the dialog warns of the refusal before Publish, first and only once', async () => {
    const answers = [
      { changes: null, errorIssues: [sourceChanged], warningIssues: [] },
      // A refusal for another reason does not tell whether the source is
      // still changed, so the warning stays.
      {
        changes: null,
        errorIssues: [{ code: 'publish.topology.exists', message: 'taken' }],
        warningIssues: [],
      },
      { changes: changesTo('lab'), errorIssues: [], warningIssues: [] },
    ];
    let answer = 0;
    const { wrapper } = open({
      previewPublish: async () => answers[answer++],
    });

    await flushPromises();

    const warning = byId(wrapper, 'publish-source-changed');

    expect(warning.text()).toBe(
      'This diagram comes from topology lab. That topology changed on the server after Builder made the diagram. ' +
        'Publish refuses this diagram under any name. ' +
        'To continue, make a new draft of topology lab: import it again or open its published diagram. ' +
        'Then copy your changes to the new draft.',
    );
    expect(warning.classes()).toContain('builder-hint--warning');
    expect(
      byId(wrapper, 'publish-source-changed-region').attributes('aria-live'),
    ).toBe('polite');
    expect(
      warning.element.compareDocumentPosition(
        wrapper.find('fieldset').element,
      ) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // What publishing changes does not list it a second time.
    expect(byId(section(wrapper), 'publish-preview-issues').exists()).toBe(
      false,
    );
    expect(
      byId(section(wrapper), 'publish-preview-source-changed').text(),
    ).toBe(
      'The server refuses to publish this draft. The warning at the top of this dialog says why.',
    );

    vi.useFakeTimers();
    await byId(wrapper, 'publish-name').setValue('taken');
    await vi.advanceTimersByTimeAsync(PREVIEW_DELAY_MS);
    await flushPromises();

    expect(byId(wrapper, 'publish-source-changed').exists()).toBe(true);
    expect(section(wrapper).text()).toContain('publish.topology.exists');

    await byId(wrapper, 'publish-name').setValue('lab');
    await vi.advanceTimersByTimeAsync(PREVIEW_DELAY_MS);
    await flushPromises();

    // A dry run that passes every check says the source is fresh.
    expect(byId(wrapper, 'publish-source-changed').exists()).toBe(false);
  });
});

describe('the name of a config this draft may update', () => {
  // A draft imported from riverside-water, whose diagram name is Riverside
  // Water, and which published experiment lab with that topology.
  function imported(store) {
    store.doc = {
      ...store.doc,
      metadata: { ...store.doc.metadata, name: 'Riverside Water' },
      source: { kind: 'topology', name: 'riverside-water' },
    };
    store.draftRecord = {
      ...store.draftRecord,
      id: 'd1',
      publication: {
        topologyTarget: 'riverside-water',
        experimentTarget: 'lab',
        documentId: 'p1',
      },
    };
    store.documents = [
      { id: 'p1', kind: 'Topology', target: 'riverside-water', draftId: 'd1' },
    ];
    store.sources = {
      ...store.sources,
      topologies: [{ name: 'riverside-water' }],
      experiments: [{ name: 'lab' }],
    };
  }

  test('a button sets the topology name in one click, then goes', async () => {
    const { wrapper } = open({ setup: imported });

    await flushPromises();

    const field = byId(wrapper, 'publish-name');
    const suggestion = byId(wrapper, 'publish-name-suggestion');

    // The name still starts as the diagram name.
    expect(field.element.value).toBe('Riverside-Water');
    expect(byId(wrapper, 'publish-topology-action-hint').text()).toBe(
      'Publishing creates a new topology.',
    );
    // A button, so the keyboard reaches it, named for the topology.
    expect(suggestion.element.tagName).toBe('BUTTON');
    expect(suggestion.attributes('type')).toBe('button');
    expect(suggestion.attributes('tabindex')).toBeUndefined();
    expect(suggestion.text().replace(/\s+/g, ' ')).toBe(
      'Use riverside-water as the topology name',
    );

    await suggestion.trigger('click');
    await flushPromises();

    expect(field.element.value).toBe('riverside-water');
    expect(document.activeElement).toBe(field.element);
    expect(byId(wrapper, 'publish-name-suggestion').exists()).toBe(false);
    expect(byId(wrapper, 'publish-topology-action-hint').text()).toBe(
      'A topology with this name exists. Publishing updates it.',
    );
    expect(byId(wrapper, 'publish-submit').text()).toBe('Update topology');

    // It comes back for another name.
    await field.setValue('other');

    expect(byId(wrapper, 'publish-name-suggestion').exists()).toBe(true);
  });

  test('a button sets the experiment name the draft published to', async () => {
    const { wrapper } = open({ setup: imported });

    await flushPromises();
    await byId(wrapper, 'publish-mode-experiment').setValue(true);

    const suggestion = byId(wrapper, 'publish-experiment-suggestion');

    expect(suggestion.text().replace(/\s+/g, ' ')).toBe(
      'Use lab as the experiment name',
    );

    await suggestion.trigger('click');
    await flushPromises();

    const field = byId(wrapper, 'publish-experiment');

    expect(field.element.value).toBe('lab');
    expect(document.activeElement).toBe(field.element);
    expect(byId(wrapper, 'publish-experiment-suggestion').exists()).toBe(false);
  });

  test('no button for a draft that may update no existing config', async () => {
    const { wrapper } = open({
      setup: (store) => {
        imported(store);
        store.draftRecord = {
          ...store.draftRecord,
          sourceToken: 'uploaded/Topology/riverside-water',
          publication: null,
        };
        store.documents = [];
      },
    });

    await flushPromises();
    await byId(wrapper, 'publish-mode-experiment').setValue(true);

    expect(byId(wrapper, 'publish-name-suggestion').exists()).toBe(false);
    expect(byId(wrapper, 'publish-experiment-suggestion').exists()).toBe(false);
  });
});
